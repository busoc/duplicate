package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/midbel/toml"
	"golang.org/x/sync/errgroup"
)

var ErrClosed = errors.New("ring already closed")

const (
	DefaultQueueSize  = 1 << 15
	DefaultBufferSize = 8 << 20
)

const DefaultProtocol = "udp"

const help = `
duplicate forwards an incoming stream of UDP or TCP packets to a set of remote hosts
via UDP or TCP, optionally, adding a delay in the transmission of the stream.

options:

  -d DELAY   wait for DELAY before sending packets (in milliseconds)
  -b BUFFER  use a buffer of BUFFER bytes
  -i IFI     use network interface IFI when subscribing to multicast group
  -l PROTO   use PROTO to listen for incoming packets
  -r PROTO   use PROTO to send packets to remote host(s)
  -k         stay listening after current TCP connection is completed
  -h         show this help message and exit

usage:
$ duplicate [-h] <config.toml>
$ duplicate [-d] [-b] [-i] [-l] [-r] [-k] [-k] <[proto://]host:port> <[proto://]host:port...>
`

type Route struct {
	Proto  string `toml:"protocol"`
	Addr   string `toml:"address"`
	Buffer int
	Delay  int
	Cert   Certificate `toml:"certificate"`
}

type Certificate struct {
	Pem      string `toml:"cert-file"`
	Key      string `toml:"key-file"`
	Policy   string
	Insecure bool
	CertAuth []string
}

func (c Certificate) Listen(inner net.Listener) (net.Listener, error) {
	if c.Pem == "" && c.Key == "" {
		return inner, nil
	}

	pool, err := c.buildCertPool()
	if err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(c.Pem, c.Key)
	if err != nil {
		return nil, err
	}

	cfg := tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
	}

	switch strings.ToLower(c.Policy) {
	case "request":
		cfg.ClientAuth = tls.RequestClientCert
	case "require", "any":
		cfg.ClientAuth = tls.RequireAnyClientCert
	case "verify":
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	case "none":
		cfg.ClientAuth = tls.NoClientCert
	case "", "require+verify":
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	default:
		return nil, fmt.Errorf("%s: unknown policy", c.Policy)
	}

	return tls.NewListener(inner, &cfg), nil
}

func (c Certificate) Client(inner net.Conn) (net.Conn, error) {
	if c.Pem == "" && c.Key == "" {
		return inner, nil
	}

	pool, err := c.buildCertPool()
	if err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(c.Pem, c.Key)
	if err != nil {
		return nil, err
	}

	cfg := tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: c.Insecure,
		RootCAs:            pool,
	}
	return tls.Client(inner, &cfg), nil
}

func (c Certificate) buildCertPool() (*x509.CertPool, error) {
	if len(c.CertAuth) == 0 {
		return x509.SystemCertPool()
	}
	pool := x509.NewCertPool()
	for _, f := range c.CertAuth {
		pem, err := ioutil.ReadFile(f)
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(pem)
		if err != nil {
			return nil, err
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(help))
		os.Exit(2)
	}
	var (
		delay  = flag.Int("d", 0, "delay in milliseconds")
		buffer = flag.Int("b", 0, "buffer size")
		keep   = flag.Bool("k", false, "stay listening")
		nic    = flag.String("i", "", "network interface")
		psrc   = flag.String("l", DefaultProtocol, "protocol")
		pdst   = flag.String("r", DefaultProtocol, "protocol")
	)
	flag.Parse()

	c := struct {
		Proto   string      `toml:"protocol"`
		Addr    string      `toml:"address"`
		Ifi     string      `toml:"nic"`
		Forever bool        `toml:"keep-listen"`
		Cert    Certificate `toml:"certificate"`
		Routes  []Route     `toml:"route"`
	}{}

	if flag.NArg() == 1 {
		if err := toml.DecodeFile(flag.Arg(0), &c); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		c.Addr = flag.Arg(0)
		c.Forever = *keep
		c.Ifi = *nic
		c.Proto = *psrc
		for i := 1; i < flag.NArg(); i++ {
			r := Route{
				Addr:   flag.Arg(i),
				Buffer: *buffer,
				Delay:  *delay,
				Proto:  *pdst,
			}
			c.Routes = append(c.Routes, r)
		}
	}

	var (
		ws  = make([]io.Writer, len(c.Routes))
		grp errgroup.Group
	)
	for i, r := range c.Routes {
		var (
			wg io.WriteCloser
			rg io.ReadCloser
		)
		if r.Delay > 0 {
			rg, wg = Ring(r.Buffer, withDelay(r.Delay))
		} else {
			rg, wg = io.Pipe()
			defer wg.Close()
		}
		ws[i] = wg

		fn, err := Duplicate(r, rg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		grp.Go(fn)
	}

	var (
		fn  func() error
		err error
	)
	switch w := io.MultiWriter(ws...); strings.ToLower(c.Proto) {
	case "", "udp":
		fn, err = listenUDP(w, c.Addr, c.Ifi)
	case "tcp":
		fn, err = listenTCP(w, c.Addr, c.Forever, c.Cert)
	default:
		err = fmt.Errorf("unsupported protocol %s", c.Proto)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	grp.Go(fn)

	if err := grp.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

func listenUDP(w io.Writer, addr, nic string) (func() error, error) {
	r, err := Listen(addr, nic)
	if err != nil {
		return nil, err
	}
	return func() error {
		defer r.Close()
		for {
			_, err := io.Copy(w, r)
			if errors.Is(err, io.EOF) {
				break
			}
		}
		return nil
	}, nil
}

func listenTCP(w io.Writer, addr string, forever bool, cert Certificate) (func() error, error) {
	s, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s, err = cert.Listen(s)
	if err != nil {
		return nil, err
	}
	return func() error {
		defer s.Close()
		for {
			r, err := s.Accept()
			if err != nil {
				return err
			}
			if c, ok := r.(*net.TCPConn); ok {
				c.SetKeepAlive(true)
			}
			io.Copy(w, r)
			r.Close()
			if !forever {
				break
			}
		}
		return nil
	}, nil
}

func Duplicate(r Route, rc io.ReadCloser) (func() error, error) {
	if r.Proto == "" {
		r.Proto = DefaultProtocol
	}
	w, err := net.Dial(strings.ToLower(r.Proto), r.Addr)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(r.Proto) == "tcp" {
		w, err = r.Cert.Client(w)
		if err != nil {
			return nil, err
		}
	}
	fn := func() error {
		defer func() {
			rc.Close()
			w.Close()
		}()
		if r.Delay > 0 {
			delay := time.Duration(r.Delay) * time.Millisecond
			time.Sleep(delay)
		}
		for {
			_, err := io.Copy(w, rc)
			if _, ok := w.(*net.TCPConn); ok && err != nil {
				return err
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		return nil
	}
	return fn, nil
}

func Listen(a, ifi string) (net.Conn, error) {
	addr, err := net.ResolveUDPAddr(DefaultProtocol, a)
	if err != nil {
		return nil, err
	}
	var c *net.UDPConn
	if addr.IP.IsMulticast() {
		var i *net.Interface
		if ifi, err := net.InterfaceByName(ifi); err == nil {
			i = ifi
		}
		c, err = net.ListenMulticastUDP(DefaultProtocol, i, addr)
	} else {
		c, err = net.ListenUDP(DefaultProtocol, addr)
	}
	return c, err
}

type poze struct {
	size    int
	offset  int
	elapsed time.Duration
}

type option func(*ring)

func withDelay(wait int) option {
	var (
		w = time.Duration(wait) * time.Millisecond
	)
	return func(r *ring) {
		if w <= 0 {
			return
		}
		r.wait = w
	}
}

func withQueue(z int) option {
	return func(r *ring) {
		if z < 0 {
			return
		}
		r.queue = make(chan poze, z)
	}
}

type ring struct {
	buffer []byte
	queue  chan poze

	offset int
	when   time.Time
	wait   time.Duration

	once sync.Once
}

func Ring(size int, opts ...option) (io.ReadCloser, io.WriteCloser) {
	if size <= 0 {
		size = DefaultBufferSize
	}
	rg := ring{
		buffer: make([]byte, size),
	}
	for _, o := range opts {
		o(&rg)
	}
	if rg.queue == nil {
		rg.queue = make(chan poze, DefaultQueueSize)
	}
	return &rg, &rg
}

func (r *ring) Close() error {
	err := ErrClosed
	r.once.Do(func() {
		close(r.queue)
		err = nil
	})
	return err
}

func (r *ring) Write(xs []byte) (int, error) {
	offset, size := r.offset, len(xs)

	if n := copy(r.buffer[offset:], xs); n < size {
		r.offset = copy(r.buffer, xs[n:])
	} else {
		r.offset += n
	}
	pz := poze{
		size:    size,
		offset:  offset,
		elapsed: r.wait,
	}
	if r.wait > 0 {
		if !r.when.IsZero() {
			pz.elapsed = time.Since(r.when)
		}
		r.when = time.Now().UTC()
	}
	select {
	case r.queue <- pz:
		return size, nil
	default:
		return 0, ErrClosed
	}
}

func (r *ring) Read(xs []byte) (int, error) {
	pz, ok := <-r.queue
	if !ok {
		return 0, io.EOF
	}
	size := len(xs)
	if size < pz.size {
		return 0, io.ErrShortBuffer
	}

	if n := copy(xs, r.buffer[pz.offset:]); n < pz.size {
		copy(xs[n:], r.buffer)
	}
	return pz.size, nil
}

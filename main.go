package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

func main() {
	flag.Parse()
	c := struct {
		Proto  string `toml:"protocol"`
		Remote string
		Ifi    string `toml:"nic"`
		Routes []struct {
			Proto  string `toml:"protocol"`
			Addr   string `toml:"address"`
			Buffer int
			Delay  int
		} `toml:"route"`
	}{}
	if err := toml.DecodeFile(flag.Arg(0), &c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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

		fn, err := Duplicate(r.Proto, r.Addr, r.Delay, rg)
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
		fn, err = listenUDP(c.Remote, c.Ifi, w)
	case "tcp":
		fn, err = listenTCP(c.Remote, w)
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

func listenUDP(addr, nic string, w io.Writer) (func() error, error) {
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

func listenTCP(addr string, w io.Writer) (func() error, error) {
	s, err := net.Listen("tcp", addr)
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
			io.Copy(w, r)
			r.Close()
		}
		return nil
	}, nil
}

func Duplicate(proto, addr string, wait int, r io.ReadCloser) (func() error, error) {
	if proto == "" {
		proto = DefaultProtocol
	}
	w, err := net.Dial(strings.ToLower(proto), addr)
	if err != nil {
		return nil, err
	}
	fn := func() error {
		defer func() {
			r.Close()
			w.Close()
		}()
		if wait > 0 {
			delay := time.Duration(wait) * time.Millisecond
			time.Sleep(delay)
		}
		for {
			_, err := io.Copy(w, r)
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

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
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
		Remote string
		Ifi    string `toml:"nic"`
		Buffer int
		Routes []struct {
			Addr     string `toml:"address"`
			Delay    int
			Interval int
		} `toml:"route"`
	}{}
	if err := toml.DecodeFile(flag.Arg(0), &c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if c.Buffer <= 0 {
		c.Buffer = DefaultBufferSize
	}

	r, err := Listen(c.Remote, c.Ifi)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer r.Close()

	var (
		ws  = make([]io.Writer, len(c.Routes))
		grp errgroup.Group
	)
	for i, r := range c.Routes {
		ws[i] = Ring(c.Buffer, withInterval(r.Delay, r.Interval))

		fn, err := Duplicate(r.Addr, ws[i])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		grp.Go(fn)
	}

	grp.Go(func() error {
		w := io.MultiWriter(ws...)
		for {
			_, err := io.Copy(w, r)
			if errors.Is(err, io.EOF) {
				break
			}
		}
		return nil
	})
	if err := grp.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

func Duplicate(addr string, r io.ReadCloser) (func() error, error) {
	w, err := net.Dial(DefaultProtocol, addr)
	if err != nil {
		return nil, err
	}
	fn := func() error {
		defer func() {
			r.Close()
			w.Close()
		}()
		for {
			_, err := io.Copy(w, r)
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

func withInterval(wait, interval int) option {
	var (
		w = time.Duration(wait) * time.Millisecond
		i = time.Duration(interval) * time.Millisecond
	)
	return func(r *ring) {
		if w <= 0 {
			return
		}
		r.wait = w
		r.interval = i
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

	offset   int
	when     time.Time
	wait     time.Duration
	interval time.Duration

	once sync.Once
}

func Ring(size int, opts ...option) io.ReadWriteCloser {
	r := ring{
		buffer: make([]byte, size),
	}
	for _, o := range opts {
		o(&r)
	}
	if r.queue == nil {
		r.queue = make(chan poze, DefaultQueueSize)
	}
	return &r
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
			pz.elapsed = time.Since(r.when).Truncate(time.Millisecond)
		}
		r.when = time.Now()
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
	if r.wait > 0 {
		sleep := pz.elapsed
		if r.interval > 0 {
			sleep = r.interval
		}
		time.Sleep(sleep)
	}
	return pz.size, nil
}

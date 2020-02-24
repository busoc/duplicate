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
		Routes []struct {
			Addr     string `toml:"address"`
			Buffer   int
			Delay    int
			Interval int
		} `toml:"route"`
	}{}
	if err := toml.DecodeFile(flag.Arg(0), &c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
		var (
			wg io.WriteCloser
			rg io.ReadCloser
		)
		if r.Delay > 0 {
			rg, wg = Ring(r.Buffer, withDelay(r.Delay))
		} else {
			rg, wg = io.Pipe()
		}
		defer wg.Close()
		ws[i] = wg

		fn, err := Duplicate(r.Addr, r.Delay, rg)
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

func Duplicate(addr string, wait int, r io.ReadCloser) (func() error, error) {
	w, err := net.Dial(DefaultProtocol, addr)
	if err != nil {
		return nil, err
	}
	fn := func() error {
		defer func() {
			r.Close()
			w.Close()
		}()
		buf := make([]byte, 1<<16)
		for {
			_, err := io.CopyBuffer(w, r, buf)
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
}

type option func(*ring)

func withDelay(wait int) option {
	return func(r *ring) {
		if wait <= 0 {
			return
		}
		r.wait = time.Duration(wait) * time.Millisecond
	}
}

func withQueue(z int) option {
	return func(r *ring) {
		if z < 0 {
			return
		}
		close(r.queue)
		r.queue = make(chan poze, z)
	}
}

type ring struct {
	buffer []byte

	offset   int
	when     time.Time
	wait     time.Duration

	once sync.Once
	queue  chan poze
	closed bool
}

func Ring(size int, opts ...option) (io.ReadCloser, io.WriteCloser) {
	if size <= 0 {
		size = DefaultBufferSize
	}
	r := ring{
		buffer: make([]byte, size),
		queue: make(chan poze, DefaultQueueSize),
	}
	for _, o := range opts {
		o(&r)
	}
	return &r, &r
}

func (r *ring) Close() error {
	err := ErrClosed
	r.once.Do(func() {
		close(r.queue)
		r.closed = true
		err = nil
	})
	return err
}

func (r *ring) Write(xs []byte) (int, error) {
	if r.closed {
		return 0, io.EOF
	}
	offset, size := r.offset, len(xs)

	if n := copy(r.buffer[offset:], xs); n < size {
		r.offset = copy(r.buffer, xs[n:])
	} else {
		r.offset += n
	}
	pz := poze{
		size:    size,
		offset:  offset,
	}
	go func() {
		time.Sleep(r.wait)
		r.queue <- pz
	}()
	return len(xs), nil
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

	if len(r.buffer) < len(xs) {
		copy(xs, r.buffer)
	} else {
		n := copy(xs, r.buffer[pz.offset:])
		if n < pz.size {
			copy(xs[n:], r.buffer[:pz.size-n])
		}
	}
	return pz.size, nil
}

package main

import (
	"io"
	"time"
)

const DefaultQueueSize = 1 << 15

type poze struct {
	size    int
	offset  int
	elapsed time.Duration
}

type Option func(*ring)

func WithInterval(w, d time.Duration) Option {
	return func(r *ring) {
		if w <= 0 {
			return
		}
		r.wait = w
		if d > 0 {
			r.interval = d
		}
	}
}

func WithQueue(z int) Option {
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
}

func Ring(size int, opts ...Option) io.ReadWriteCloser {
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
	return nil
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
	r.queue <- pz
	return size, nil
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

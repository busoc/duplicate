package main

type Ring struct {
	buffer []byte

	tail  int
	rchan chan int

	head  int
	wchan chan int
}

func NewRing() *Ring {
	return NewRingSize(32 << 10)
}

func NewRingSize(s int) *Ring {
	r := Ring{
		buffer: make([]byte, s),
		rchan:  make(chan int),
		wchan:  make(chan int),
	}
	go r.syncboth()
	return &r
}

func (r *Ring) Read(bs []byte) (int, error) {
	r.rchan <- len(bs)
	<-r.rchan
	n := copy(bs, r.buffer[r.tail:])
	if n < len(bs) {
		r.tail = copy(bs[n:], r.buffer)
	} else {
		r.tail += n
	}
	return len(bs), nil
}

func (r *Ring) Write(bs []byte) (int, error) {
	n := copy(r.buffer[r.head:], bs)
	if n < len(bs) {
		r.head = copy(r.buffer, bs[n:])
	} else {
		r.head += n
	}
	r.wchan <- len(bs)
	return len(bs), nil
}

func (r *Ring) syncboth() {
	var available int
	for {
		select {
		case n := <-r.wchan:
			available += n
		case n := <-r.rchan:
			for available < n {
				nn := <-r.wchan
				available += nn
			}
			available -= n
			r.rchan <- n
		}
	}
}

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/midbel/cli"
)

var commands = []*cli.Command{
	{
		Usage: "store <config.toml>",
		Short: "",
		Run:   runStor,
	},
	{
		Usage: "relay <config.toml>",
		Short: "",
		Run:   runRelay,
	},
	{
		Usage: "split [-i sleep] [-s size] <addr> <file...>",
		Short: "",
		Run:   runSplit,
	},
}

func main() {
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintf(os.Stderr, "unexpected error: %s\n", err)
			os.Exit(2)
		}
	}()
	err := cli.Run(commands, cli.Usage("duplicate", "", commands), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func Listen(a, ifi string) (net.Conn, error) {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		return nil, err
	}
	var conn *net.UDPConn
	if addr.IP.IsMulticast() {
		var i *net.Interface
		if ifi, err := net.InterfaceByName(ifi); err == nil {
			i = ifi
		}
		conn, err = net.ListenMulticastUDP("udp", i, addr)
	} else {
		conn, err = net.ListenUDP("udp", addr)
	}
	return conn, err
}

var MetaLen = binary.Size(uint32(0)) + binary.Size(time.Second)

type metaWriter struct {
	inner io.Writer

	sequence uint32
	last     time.Time

	buf  bytes.Buffer
	size uint32
}

func Meta(w io.Writer) io.Writer {
	return &metaWriter{
		inner: w,
		size:  uint32(MetaLen),
	}
}

func (m *metaWriter) Write(bs []byte) (int, error) {
	var elapsed time.Duration
	if !m.last.IsZero() {
		elapsed = time.Since(m.last)
	}
	size := uint32(len(bs)) + m.size

	binary.Write(&m.buf, binary.BigEndian, size)
	binary.Write(&m.buf, binary.BigEndian, m.sequence)
	binary.Write(&m.buf, binary.BigEndian, elapsed)
	m.buf.Write(bs)

	count := m.buf.Len()

	n, err := io.Copy(m.inner, &m.buf)
	if err != nil {
		return 0, err
	}
	if n != int64(count) {
		return 0, fmt.Errorf("wrong number of bytes written (want: %d, got: %d)", count, n)
	}
	m.sequence++
	m.last = time.Now()

	return len(bs), err
}

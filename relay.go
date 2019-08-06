package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/midbel/cli"
	"github.com/midbel/toml"
	"golang.org/x/sync/errgroup"
)

const DefaultBufferSize = 64 << 20

func runRelay(cmd *cli.Command, args []string) error {
	if err := cmd.Flag.Parse(args); err != nil {
		return err
	}

	r, err := os.Open(cmd.Flag.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()

	var c = struct {
		Local   string
		Ifi     string `toml:"nic"`
		Remotes []string
		Delay   int
		Buffer  int
	}{}
	if err := toml.NewDecoder(r).Decode(&c); err != nil {
		return err
	}
	rc, err := Listen(c.Local, c.Ifi)
	if err != nil {
		return err
	}
	defer rc.Close()

	wc := make([]io.Writer, 0, len(c.Remotes))
	for _, r := range c.Remotes {
		c, err := net.Dial("udp", r)
		if err != nil {
			return err
		}
		defer c.Close()
		wc = append(wc, c)
	}
	if len(wc) == 0 {
		return fmt.Errorf("no remote hosts given")
	}
	if wc := io.MultiWriter(wc...); c.Delay <= 0 {
		_, err = io.Copy(wc, rc)
	} else {
		// pr, pw, err := os.Pipe()
		// if err != nil {
		// 	return err
		// }
		// defer func() {
		// 	pr.Close()
		// 	pw.Close()
		// }()
		if c.Buffer <= 0 {
			c.Buffer = DefaultBufferSize
		}
		var (
			grp errgroup.Group
			rwg = NewRingSize(c.Buffer)
		)
		grp.Go(writeToPipe(rc, rwg))
		grp.Go(readFromPipe(rwg, wc, c.Delay))
		err = grp.Wait()
	}
	return err
}

func writeToPipe(r io.Reader, w io.Writer) func() error {
	return func() error {
		buffer := make([]byte, 1<<16)
		w = Meta(w)
		for {
			switch _, err := io.CopyBuffer(w, r, buffer); err {
			case nil:
			case io.EOF:
				return nil
			default:
				return err
			}
		}
	}
}

func readFromPipe(r io.Reader, w io.Writer, delta int) func() error {
	return func() error {
		var (
			size    uint32
			count   uint32
			elapsed time.Duration
		)
		for {
			binary.Read(r, binary.BigEndian, &size)
			binary.Read(r, binary.BigEndian, &count)
			binary.Read(r, binary.BigEndian, &elapsed)

			if elapsed > 0 {
				time.Sleep(elapsed)
			} else {
				time.Sleep(time.Duration(delta) * time.Second)
			}
			if _, err := io.CopyN(w, r, int64(size)-int64(MetaLen)); err != nil {
				return err
			}
		}
		return nil
	}
}

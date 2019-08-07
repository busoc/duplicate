package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/midbel/cli"
	"github.com/midbel/sizefmt"
)

const (
	DefaultSleep = time.Millisecond
	DefaultSize  = 1380
)

func runSplit(cmd *cli.Command, args []string) error {
	var (
		sleep   = cmd.Flag.Duration("i", DefaultSleep, "interval")
		timeout = cmd.Flag.Duration("t", 0, "timeout")
		size    = cmd.Flag.Int("s", DefaultSize, "packet size")
		limit   = cmd.Flag.Int("n", 0, "packets count")
		repeat  = cmd.Flag.Int("r", 0, "repeat")
	)
	if err := cmd.Flag.Parse(args); err != nil {
		return err
	}

	w, err := dial(cmd.Flag.Arg(0), *timeout)
	if err != nil {
		return err
	}
	defer w.Close()

	files := cmd.Flag.Args()
	files = listFiles(files[1:], *repeat)
	rs := make([]io.Reader, 0, len(files))
	for i := 1; i < len(files); i++ {
		r, err := os.Open(files[i])
		if err != nil {
			return err
		}
		defer r.Close()
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return fmt.Errorf("no files given")
	}
	r := io.MultiReader(rs...)

	return splitFiles(r, w, *size, *limit, *sleep)
}

func dial(addr string, timeout time.Duration) (net.Conn, error) {
	w, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	var expired time.Time
	if timeout > 0 {
		expired = time.Now().Add(timeout)
	}
	return w, w.SetDeadline(expired)
}

func splitFiles(r io.Reader, w io.Writer, size, limit int, sleep time.Duration) error {
	if size <= 0 {
		size = DefaultSize
	}
	if sleep == 0 {
		sleep = DefaultSleep
	}

	var written, count int64
	go func() {
		var i, c, n int64
		for range time.Tick(time.Second) {
			i++
			c += written
			n += count

			w := sizefmt.Format(float64(written), sizefmt.IEC)
			t := sizefmt.Format(float64(c), sizefmt.IEC)
			fmt.Printf("%04d: %d packets - %s (%s - %d)\n", i, count, w, t, n)
			written, count = 0, 0
		}
	}()
	for i := 0; limit <= 0 || i <= limit; i++ {
		if n, err := io.CopyN(w, r, int64(size)); err != nil && n == 0 {
			if err == io.EOF {
				break
			}
			return err
		} else {
			written += n
			count++
		}
		time.Sleep(sleep)
	}
	return nil
}

func listFiles(files []string, repeat int) []string {
	if repeat <= 0 {
		return files
	}
	z := len(files)
	fs := make([]string, z*repeat)
	for i := 0; i < len(fs); i++ {
		fs[i] = files[i%z]
	}
	return fs
}

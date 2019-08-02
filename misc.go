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
	sleep := cmd.Flag.Duration("i", DefaultSleep, "interval")
	size := cmd.Flag.Int("s", DefaultSize, "packet size")
	if err := cmd.Flag.Parse(args); err != nil {
		return err
	}
	w, err := net.Dial("udp", cmd.Flag.Arg(0))
	if err != nil {
		return err
	}
	defer w.Close()

	rs := make([]io.Reader, 0, cmd.Flag.NArg())
	for i := 1; i < cmd.Flag.NArg(); i++ {
		r, err := os.Open(cmd.Flag.Arg(i))
		if err != nil {
			return err
		}
		defer r.Close()
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return fmt.Errorf("no files given")
	}

	if *size <= 0 {
		*size = DefaultSize
	}
	if *sleep == 0 {
		*sleep = DefaultSleep
	}
	r := io.MultiReader(rs...)
  var written, count int64
  go func() {
    var i, c int64
    for range time.Tick(time.Second) {
      i++
      c += written

      w := sizefmt.Format(float64(written), sizefmt.IEC)
      t := sizefmt.Format(float64(c), sizefmt.IEC)
      fmt.Printf("%04d: %d packets - %s (%s)\n", i, count, w, t)
      written, count = 0, 0
    }
  }()
	for {
		if n, err := io.CopyN(w, r, int64(*size)); err != nil && n == 0 {
			if err == io.EOF {
				break
			}
			return err
		} else {
      written += n
      count++
    }
		time.Sleep(*sleep)
	}
	return nil
}

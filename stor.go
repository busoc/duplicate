package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/midbel/cli"
	"github.com/midbel/roll"
	"github.com/midbel/toml"
)

func runStor(cmd *cli.Command, args []string) error {
	if err := cmd.Flag.Parse(args); err != nil {
		return err
	}
	r, err := os.Open(cmd.Flag.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()
	c := struct {
		Meta     bool
		Prefix   string
		Local    string `toml:"addr"`
		Ifi      string `toml:"nic"`
		Data     string `toml:"datadir"`
		Compress bool
		Interval int
		Timeout  int
		Count    int
		Size     int
	}{}
	if err := toml.NewDecoder(r).Decode(&c); err != nil {
		return err
	}

	next, err := openFile(c.Data, c.Prefix, c.Compress)
	if err != nil {
		return err
	}
	options := []roll.Option{
		roll.WithInterval(time.Second * time.Duration(c.Interval)),
		roll.WithTimeout(time.Second * time.Duration(c.Timeout)),
		roll.WithThreshold(c.Size, c.Count),
	}

	writer, err := roll.Roll(next, options...)
	if err != nil {
		return nil
	}
	defer writer.Close()

	var wc io.Writer = writer
	if c.Meta {
		wc = Meta(wc)
	}

	rc, err := Listen(c.Local, c.Ifi)
	if err != nil {
		return err
	}
	defer rc.Close()

	_, err = io.Copy(wc, rc)
	return err
}

func openFile(data, prefix string, gz bool) (roll.NextFunc, error) {
	if err := os.MkdirAll(data, 0755); err != nil {
		return nil, err
	}
	if prefix == "" {
		prefix = "dup"
	}
	return func(fino int, when time.Time) (io.WriteCloser, []io.Closer, error) {
		file := fmt.Sprintf("%s_%06d_%s.bin", prefix, fino, when.Format("20060102-150405"))
		w, err := os.Create(filepath.Join(data, file))
		if err != nil {
			return nil, nil, err
		}

		var wc io.WriteCloser = w
		if gz {
			g := gzip.NewWriter(wc)
			wc = g
		}
		return wc, []io.Closer{w}, nil
	}, nil
}

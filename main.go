package main

import (
	"fmt"
	"net"
	"os"

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

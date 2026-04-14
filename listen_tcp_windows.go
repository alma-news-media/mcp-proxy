//go:build windows

package main

import (
	"context"
	"net"
	"syscall"
)

// listenTCPReuseAddr binds addr with SO_REUSEADDR so the same port can be
// taken again soon after the process exits, instead of staying unavailable
// while the kernel holds TIME_WAIT state on the old socket.
func listenTCPReuseAddr(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: reuseAddrControl,
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

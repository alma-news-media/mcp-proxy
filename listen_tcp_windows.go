//go:build windows

package main

import (
	"context"
	"net"
	"syscall"
)

// soExclusiveAddrUse is WinSock SO_EXCLUSIVEADDRUSE (exclusive bind). The name is not exported
// from package syscall on Windows; the value is -5 per Windows SDK winsock2.h.
const soExclusiveAddrUse = -5

// listenTCPReuseAddr binds addr with SO_EXCLUSIVEADDRUSE (not SO_REUSEADDR) so the listener
// holds the port exclusively and another process cannot bind the same address/port.
func listenTCPReuseAddr(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: reuseAddrControl,
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

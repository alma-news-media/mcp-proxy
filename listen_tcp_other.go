//go:build !(unix || windows)

package main

import (
	"net"
)

// listenTCPReuseAddr binds to addr. SO_REUSEADDR is not applied on this platform;
// use the default listener.
func listenTCPReuseAddr(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

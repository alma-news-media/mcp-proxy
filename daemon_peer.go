package main

import (
	"net"
	"os"
)

// daemonControlListener rejects connections from other users, including those
// queued before the socket's permissions were restricted.
type daemonControlListener struct {
	net.Listener
}

func (l *daemonControlListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if daemonPeerIsOwner(conn) {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func daemonPeerIsOwner(conn net.Conn) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	var uid uint32
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		uid, peerErr = daemonPeerUID(fd)
	}); err != nil || peerErr != nil {
		return false
	}
	return uid == uint32(os.Geteuid())
}

package main

import (
	"net"
	"testing"
)

// TestListenTCPReuseAddr_SequentialRebind checks that after closing a listener
// created by listenTCPReuseAddr, the same address (including ephemeral port)
// can be bound again immediately. This guards the socket option path used for
// TCP (SO_REUSEADDR on Unix, SO_EXCLUSIVEADDRUSE on Windows, plain Listen on
// other GOOS) without asserting OS-specific syscall values.
func TestListenTCPReuseAddr_SequentialRebind(t *testing.T) {
	ln1, err := listenTCPReuseAddr("127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	addr := ln1.Addr().String()
	if err := ln1.Close(); err != nil {
		t.Fatalf("close first listener: %v", err)
	}

	ln2, err := listenTCPReuseAddr(addr)
	if err != nil {
		t.Fatalf("second listen on %q: %v", addr, err)
	}
	if err := ln2.Close(); err != nil {
		t.Fatalf("close second listener: %v", err)
	}
}

// TestListenTCPReuseAddr_BindsTCP ensures we get a TCP listener with a non-nil address.
func TestListenTCPReuseAddr_BindsTCP(t *testing.T) {
	ln, err := listenTCPReuseAddr("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	tcpa, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("addr type = %T, want *net.TCPAddr", ln.Addr())
	}
	if tcpa.Port == 0 {
		t.Fatal("expected ephemeral port to be assigned")
	}
}

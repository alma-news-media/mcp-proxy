//go:build !linux && !darwin && !freebsd

package main

import "errors"

func daemonPeerCredentialsSupported() bool { return false }

func daemonPeerUID(uintptr) (uint32, error) {
	return 0, errors.New("peer credentials are unsupported")
}

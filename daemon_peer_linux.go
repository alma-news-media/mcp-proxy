package main

import "syscall"

func daemonPeerCredentialsSupported() bool { return true }

func daemonPeerUID(fd uintptr) (uint32, error) {
	cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}

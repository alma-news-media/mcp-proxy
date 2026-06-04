//go:build !darwin && !linux

package main

// isMcpProxyDaemonProcess is unsupported; rely on the control socket for liveness.
func isMcpProxyDaemonProcess(pid int) bool {
	_ = pid
	return false
}

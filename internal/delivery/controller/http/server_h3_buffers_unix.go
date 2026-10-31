//go:build unix

package http

import (
	"net"
	"syscall"
)

// socketBuffers reads the receive and send buffer sizes the kernel gave the socket.
func socketBuffers(c *net.UDPConn) (rcv, snd int, err error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var rcvErr, sndErr error
	if err = raw.Control(func(fd uintptr) {
		rcv, rcvErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		snd, sndErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	}); err != nil {
		return 0, 0, err
	}
	if rcvErr != nil {
		return 0, 0, rcvErr
	}
	return rcv, snd, sndErr
}

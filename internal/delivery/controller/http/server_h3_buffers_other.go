//go:build !unix

package http

import (
	"errors"
	"net"
)

func socketBuffers(*net.UDPConn) (int, int, error) {
	return 0, 0, errors.New("socket buffers are read on unix only")
}

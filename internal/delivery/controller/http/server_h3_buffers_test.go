package http

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Linux reads back twice what it set; the other systems read the size as it is.
func TestBufferShortfall(t *testing.T) {
	got, short := bufferShortfall(quicSocketBuffer, 2*quicSocketBuffer, "linux")
	assert.False(t, short, "linux doubled the full size")
	assert.Equal(t, quicSocketBuffer, got)

	got, short = bufferShortfall(quicSocketBuffer, 2*212992, "linux")
	assert.True(t, short, "rmem_max default is far below 7 MiB")
	assert.Equal(t, 212992, got)

	_, short = bufferShortfall(quicSocketBuffer, quicSocketBuffer, "darwin")
	assert.False(t, short)
	_, short = bufferShortfall(quicSocketBuffer, 786896, "darwin")
	assert.True(t, short)
}

// The real socket's buffers can be read, and the check never panics or fails the start whatever the kernel gave.
func TestWarnShortUDPBuffersOnARealSocket(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = pc.Close() }()
	rcv, snd, err := socketBuffers(pc.(*net.UDPConn))
	require.NoError(t, err)
	assert.Positive(t, rcv)
	assert.Positive(t, snd)
	assert.NotPanics(t, func() { warnShortUDPBuffers(pc) })
	assert.NotPanics(t, func() { warnShortUDPBuffers(nil) })
}

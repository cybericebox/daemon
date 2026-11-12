package http

import (
	"net"
	"runtime"

	"github.com/rs/zerolog/log"
)

// quicSocketBuffer is the socket buffer quic-go asks for on the UDP socket (7 MiB, read and write): a smaller one
// drops packets under a burst and slows every HTTP/3 connection.
const quicSocketBuffer = 7 << 20

// bufferShortfall reports whether the buffer the kernel gave (as getsockopt reads it) is smaller than asked. Linux
// reads back twice the size it set, so the value is halved there.
func bufferShortfall(asked, got int, goos string) (actual int, short bool) {
	if goos == "linux" {
		got /= 2
	}
	return got, got < asked
}

// warnShortUDPBuffers asks the kernel for the QUIC socket buffers and, when it gives less (net.core.rmem_max and
// wmem_max cap them), logs one warning with the real sizes and the sysctl to raise. It never fails the start.
func warnShortUDPBuffers(pc net.PacketConn) {
	udp, ok := pc.(*net.UDPConn)
	if !ok {
		return
	}
	_ = udp.SetReadBuffer(quicSocketBuffer)
	_ = udp.SetWriteBuffer(quicSocketBuffer)
	rcv, snd, err := socketBuffers(udp)
	if err != nil {
		log.Debug().Err(err).Msg("HTTP/3: the UDP socket buffers could not be read")
		return
	}
	rcvActual, rcvShort := bufferShortfall(quicSocketBuffer, rcv, runtime.GOOS)
	sndActual, sndShort := bufferShortfall(quicSocketBuffer, snd, runtime.GOOS)
	if !rcvShort && !sndShort {
		return
	}
	log.Warn().Int("receive_buffer", rcvActual).Int("send_buffer", sndActual).Int("wanted", quicSocketBuffer).
		Msg("HTTP/3: the UDP socket buffers are smaller than wanted, so packets may be dropped under load; raise net.core.rmem_max and net.core.wmem_max (sysctl) to at least 7340032 on the host")
}

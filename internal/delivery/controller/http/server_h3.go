package http

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/time/rate"

	"github.com/cybericebox/daemon/internal/config"
)

// QUIC limits (the UDP flood posture):
//   - Retry (address validation) is forced for new connections once more than quicFreeHandshakesPerSec arrive
//     per second (burst quicFreeHandshakesBurst): a spoofed source can not complete the handshake, so a flood
//     costs the daemon one small Retry packet each and no connection state. Under normal load nobody is asked
//     to validate, so the handshake keeps its single round trip.
//   - a handshake that does not finish in quicHandshakeTimeout is dropped;
//   - a connection has quicMaxStreams concurrent request streams (the in-flight limiter caps the requests
//     that run) and quicMaxUniStreams uni streams (HTTP/3 needs three);
//   - 0-RTT is off: early data can be replayed, and the API has requests that change state.
const (
	quicFreeHandshakesPerSec = 100
	quicFreeHandshakesBurst  = 200
	quicHandshakeTimeout     = 5 * time.Second
	quicMaxStreams           = 100
	quicMaxUniStreams        = 10
)

type h3Listener struct {
	srv  *http3.Server
	addr string
	tr   *quic.Transport
}

// altSvc announces the HTTP/3 endpoint on every response of the TCP listener.
func altSvc(next http.Handler, port string) http.Handler {
	value := `h3=":` + port + `"; ma=86400`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", value)
		next.ServeHTTP(w, r)
	})
}

// withDeadlines gives a request the read and write timeouts the TCP server applies (the http3 server has no
// such settings); a long route (SSE, download) moves its own write deadline as on TCP.
func withDeadlines(next http.Handler, read, write time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		now := time.Now()
		if read > 0 {
			_ = rc.SetReadDeadline(now.Add(read))
		}
		if write > 0 {
			_ = rc.SetWriteDeadline(now.Add(write))
		}
		next.ServeHTTP(w, r)
	})
}

func newH3Listener(cfg *config.HTTPServerConfig, tlsCfg *tls.Config, handler http.Handler) *h3Listener {
	port := cfg.HTTP3.Port
	if port == "" {
		port = cfg.HTTPSPort
	}
	return &h3Listener{
		addr: net.JoinHostPort(cfg.Host, port),
		srv: &http3.Server{
			TLSConfig:      http3.ConfigureTLSConfig(tlsCfg),
			Handler:        withDeadlines(handler, cfg.ReadTimeout, cfg.WriteTimeout),
			IdleTimeout:    cfg.IdleTimeout,
			MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
			QUICConfig: &quic.Config{
				MaxIdleTimeout:        cfg.IdleTimeout,
				HandshakeIdleTimeout:  quicHandshakeTimeout,
				MaxIncomingStreams:    quicMaxStreams,
				MaxIncomingUniStreams: quicMaxUniStreams,
			},
		},
	}
}

// serve opens the UDP socket and blocks until the server stops.
func (l *h3Listener) serve() error {
	pc, err := net.ListenPacket("udp", l.addr)
	if err != nil {
		return err
	}
	limiter := rate.NewLimiter(rate.Limit(quicFreeHandshakesPerSec), quicFreeHandshakesBurst)
	l.tr = &quic.Transport{Conn: pc, VerifySourceAddress: func(net.Addr) bool { return !limiter.Allow() }}
	ln, err := l.tr.ListenEarly(l.srv.TLSConfig, l.srv.QUICConfig)
	if err != nil {
		_ = pc.Close()
		return err
	}
	return l.srv.ServeListener(ln)
}

func (l *h3Listener) close() {
	if l.tr != nil {
		_ = l.tr.Close()
	}
}

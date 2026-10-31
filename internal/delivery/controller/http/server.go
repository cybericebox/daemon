package http

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pires/go-proxyproto"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

type (
	// Server runs the API listeners, each optional:
	//   - Port set: the API in plain HTTP (the default);
	//   - TLS cert and key set: the API on HTTPSPort (HTTP/2, optional or required client certificate);
	//   - HealthPort set: a plain listener on HEALTH_BIND that serves only the health route (the kubelet cannot
	//     present a client certificate);
	//   - InternalPort set: a plain listener on HEALTH_BIND with the same API handler for in-cluster callers.
	Server struct {
		listeners []*listener
		h3        *h3Listener
	}

	listener struct {
		name string
		srv  *http.Server
		tls  bool
		// proxyFrom are the peers whose PROXY protocol header is used (the HTTPS listener only); empty is off.
		proxyFrom []netip.Prefix
		// proxySecrets are the accepted values of the secret TLV (current, next); empty: no TLV is required.
		proxySecrets [][]byte
		secretTLV    proxyproto.PP2Type
		// headerTimeout bounds how long the PROXY header of a connection may take, near the TLS handshake limit.
		headerTimeout time.Duration
	}

	// fileStamp is what identifies a version of a file: a renewal swaps the file (or the symlink that points to
	// it), which changes at least one of the two.
	fileStamp struct {
		mod  time.Time
		size int64
	}

	certReloader struct {
		CertFile string // path to the x509 certificate for https
		KeyFile  string // path to the x509 private key matching `CertFile`
		// mu guards the cache: GetCertificate runs on every handshake, concurrently.
		mu         sync.Mutex
		cachedCert *tls.Certificate
		certStamp  fileStamp
		keyStamp   fileStamp
	}

	// poolReloader serves the client CA pool and rereads the bundle when the file changes.
	poolReloader struct {
		file  string
		mu    sync.Mutex
		pool  *x509.CertPool
		stamp fileStamp
	}
)

func stampOf(path string) (fileStamp, error) {
	stat, err := os.Stat(path) // follows the symlink, so a swapped Secret volume is seen
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{mod: stat.ModTime(), size: stat.Size()}, nil
}

func newCertReloader(certFile, keyFile string) *certReloader {
	return &certReloader{
		CertFile: certFile,
		KeyFile:  keyFile,
	}
}

// GetCertificate returns the certificate, reloading the pair when either file changed. A renewal that is
// half written (or broken) keeps the previous certificate in service.
func (cr *certReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	keyStamp, err := stampOf(cr.KeyFile)
	if err == nil {
		var certStamp fileStamp
		if certStamp, err = stampOf(cr.CertFile); err == nil {
			if cr.cachedCert != nil && certStamp == cr.certStamp && keyStamp == cr.keyStamp {
				return cr.cachedCert, nil
			}
			var pair tls.Certificate
			if pair, err = tls.LoadX509KeyPair(cr.CertFile, cr.KeyFile); err == nil {
				cr.cachedCert, cr.certStamp, cr.keyStamp = &pair, certStamp, keyStamp
				return cr.cachedCert, nil
			}
		}
	}
	if cr.cachedCert != nil {
		log.Error().Err(err).Msg("Can not reload the TLS certificate, keeping the previous one")
		return cr.cachedCert, nil
	}
	return nil, fmt.Errorf("failed loading tls key pair: %w", err)
}

func (pr *poolReloader) get() (*x509.CertPool, error) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	stamp, err := stampOf(pr.file)
	if err == nil {
		if pr.pool != nil && stamp == pr.stamp {
			return pr.pool, nil
		}
		var data []byte
		if data, err = os.ReadFile(pr.file); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(data) {
				pr.pool, pr.stamp = pool, stamp
				return pool, nil
			}
			err = errors.New("no certificate in the client CA bundle")
		}
	}
	if pr.pool != nil {
		log.Error().Err(err).Msg("Can not reload the client CA bundle, keeping the previous one")
		return pr.pool, nil
	}
	return nil, fmt.Errorf("client CA bundle %s: %w", pr.file, err)
}

// newTLSConfig builds the server TLS config: the configured minimum version, HTTP/2, the certificate reloaded on change and, with
// client auth "require" a required and verified client certificate, with "optional" a verified one when
// presented; the CA bundle is reloaded on change too. With client auth on and no usable bundle, every handshake
// fails (closed, never open).
func newTLSConfig(cfg *config.HTTPServerTLSConfig) *tls.Config {
	cr := newCertReloader(cfg.CertFile, cfg.KeyFile)
	if _, err := cr.GetCertificate(nil); err != nil {
		log.Error().Err(err).Msg("Failed loading initial certificate")
	}
	minVersion := uint16(tls.VersionTLS12)
	if cfg.MinVersion == "1.3" {
		minVersion = tls.VersionTLS13
	}
	base := &tls.Config{
		MinVersion:     minVersion,
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: cr.GetCertificate,
	}
	var mode tls.ClientAuthType
	switch cfg.ClientAuth {
	case "require":
		mode = tls.RequireAndVerifyClientCert
	case "optional":
		mode = tls.VerifyClientCertIfGiven
	default:
		return base
	}
	pr := &poolReloader{file: cfg.ClientCAFile}
	if _, err := pr.get(); err != nil {
		log.Error().Err(err).Msg("Failed loading the client CA bundle: every connection will be refused until it loads")
	}
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		pool, err := pr.get()
		if err != nil {
			return nil, err
		}
		perConn := base.Clone()
		perConn.GetConfigForClient = nil
		perConn.ClientAuth = mode
		perConn.ClientCAs = pool
		return perConn, nil
	}
	return base
}

// healthHandler serves only the health route; anything else is 404.
func healthHandler() http.Handler {
	r := gin.New()
	r.ContextWithFallback = true
	RegisterHealth(r)
	return r
}

// NewServer builds the listeners for cfg. healthBind is HEALTH_BIND.
func NewServer(cfg *config.HTTPServerConfig, healthBind string, handler http.Handler) *Server {
	newHTTP := func(addr string, h http.Handler, tlsCfg *tls.Config) *http.Server {
		return &http.Server{
			Addr:           addr,
			Handler:        h,
			ReadTimeout:    cfg.ReadTimeout,
			WriteTimeout:   cfg.WriteTimeout,
			IdleTimeout:    cfg.IdleTimeout,
			MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
			TLSConfig:      tlsCfg,
		}
	}
	s := &Server{}
	plain := handler
	if cfg.HTTP3.Enabled && cfg.TLSEnabled() {
		s.h3 = newH3Listener(cfg, newTLSConfig(&cfg.TLS), handler)
		handler = altSvc(handler, cfg.HTTP3.AdvertisedPort)
	}
	if cfg.Port != "" {
		s.listeners = append(s.listeners, &listener{name: "HTTP", srv: newHTTP(net.JoinHostPort(cfg.Host, cfg.Port), plain, nil)})
	}
	if cfg.TLSEnabled() {
		// The config is validated at start; an unparsable list would have stopped it.
		proxyFrom, _ := config.ParseProxyProtocolFrom(cfg.ProxyProtocolFrom)
		s.listeners = append(
			s.listeners,
			&listener{
				name: "HTTPS",
				srv: newHTTP(
					net.JoinHostPort(cfg.Host, cfg.HTTPSPort),
					handler,
					newTLSConfig(&cfg.TLS),
				),
				tls:           true,
				proxyFrom:     proxyFrom,
				proxySecrets:  proxySecrets(cfg),
				secretTLV:     secretTLVOf(cfg),
				headerTimeout: cfg.ReadTimeout,
			},
		)
		if len(proxyFrom) > 0 && cfg.ProxyProtocolSecret == "" {
			log.Warn().
				Msg("HTTP_SERVER_PROXY_PROTOCOL_FROM is set without HTTP_SERVER_PROXY_PROTOCOL_SECRET: any process at a listed address can choose the client address")
		}
	}
	if cfg.HealthPort != "" {
		s.listeners = append(s.listeners, &listener{name: "health", srv: newHTTP(net.JoinHostPort(healthBind, cfg.HealthPort), healthHandler(), nil)})
	}
	if cfg.InternalPort != "" {
		internal := newHTTP(net.JoinHostPort(healthBind, cfg.InternalPort), plain, nil)
		// Its callers are in-cluster (SSR in front of many visitors): the in-flight limiter holds them to the
		// general cap only.
		internal.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
			return middleware.WithInternalListener(ctx)
		}
		s.listeners = append(s.listeners, &listener{name: "internal", srv: internal})
	}
	return s
}

func (s *Server) Start() {
	if s.h3 != nil {
		go func() {
			if err := s.h3.serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Str("listener", "HTTP/3").Msg("Can not start HTTP/3 server")
			}
		}()
	}
	for _, l := range s.listeners {
		go func() {
			var err error
			switch {
			case l.tls && len(l.proxyFrom) > 0:
				err = l.serveProxied()
			case l.tls:
				err = l.srv.ListenAndServeTLS("", "")
			default:
				err = l.srv.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Str("listener", l.name).Msg("Can not start HTTP(S) server")
			}
		}()
	}
}

// serveProxied serves TLS behind the PROXY protocol: the header comes before the ClientHello, so the TLS
// configuration (mTLS, certificates) is unchanged.
func (l *listener) serveProxied() error {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", l.srv.Addr)
	if err != nil {
		return err
	}
	return l.srv.ServeTLS(
		proxyListener(ln, l.proxyFrom, l.proxySecrets, l.secretTLV, l.headerTimeout),
		"",
		"",
	)
}

// ProxySecretTLV is the default custom PROXY v2 TLV type that carries the shared secret from the edge.
const ProxySecretTLV = proxyproto.PP2_TYPE_MIN_CUSTOM

// secretTLVOf is the configured TLV type; 0xE0 when unset (a config built without the env default).
func secretTLVOf(cfg *config.HTTPServerConfig) proxyproto.PP2Type {
	if cfg.ProxyProtocolSecretTLV == 0 {
		return ProxySecretTLV
	}
	return proxyproto.PP2Type(
		cfg.ProxyProtocolSecretTLV,
	) //nolint:gosec // validated to 224-239 at start
}

func proxySecrets(cfg *config.HTTPServerConfig) [][]byte {
	var out [][]byte
	for _, secret := range []string{cfg.ProxyProtocolSecret, cfg.ProxyProtocolSecretNext} {
		if secret != "" {
			out = append(out, []byte(secret))
		}
	}
	return out
}

// proxyListener wraps ln so a peer in trusted may send a PROXY protocol header (optional: a connection without one
// keeps the peer address) and the header of any other peer is read and thrown away. With secrets, the header's
// address is also used only when its secret TLV equals one of them; any other header leaves the peer address.
// Never REQUIRE or REJECT: a stale list must not cut the API off, and a spoofed header must not choose an address.
func proxyListener(
	ln net.Listener,
	trusted []netip.Prefix,
	secrets [][]byte,
	tlvType proxyproto.PP2Type,
	headerTimeout time.Duration,
) net.Listener {
	return &secretListener{
		Listener: &proxyproto.Listener{
			Listener:          ln,
			ConnPolicy:        proxyPolicy(trusted),
			ReadHeaderTimeout: headerTimeout,
		},
		secrets: secrets,
		tlvType: tlvType,
	}
}

// secretListener wraps the accepted connections so the secret is checked when the address is asked for (in the
// connection's own goroutine, after the header is read, never in the accept loop).
type secretListener struct {
	net.Listener

	secrets [][]byte
	tlvType proxyproto.PP2Type
}

func (l *secretListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil || len(l.secrets) == 0 {
		return conn, err
	}
	if proxied, ok := conn.(*proxyproto.Conn); ok {
		return &secretConn{Conn: proxied, secrets: l.secrets, tlvType: l.tlvType}, nil
	}
	return conn, nil
}

type secretConn struct {
	*proxyproto.Conn

	secrets [][]byte
	tlvType proxyproto.PP2Type
	once    sync.Once
	addr    net.Addr
}

// RemoteAddr is the address of the PROXY header when the header carries a secret TLV that matches, else the peer's.
func (c *secretConn) RemoteAddr() net.Addr {
	c.once.Do(func() {
		c.addr = c.Conn.Raw().RemoteAddr()
		if header := c.Conn.ProxyHeader(); header != nil &&
			secretMatches(header, c.secrets, c.tlvType) {
			c.addr = c.Conn.RemoteAddr()
		}
	})
	return c.addr
}

// secretMatches reports whether the header has the secret TLV and its value equals one of secrets. Every secret is
// compared (constant time), whichever matched first.
func secretMatches(header *proxyproto.Header, secrets [][]byte, tlvType proxyproto.PP2Type) bool {
	tlvs, err := header.TLVs()
	if err != nil {
		return false
	}
	matched := 0
	for _, tlv := range tlvs {
		if tlv.Type != tlvType {
			continue
		}
		for _, secret := range secrets {
			matched |= subtle.ConstantTimeCompare(tlv.Value, secret)
		}
	}
	return matched == 1
}

func proxyPolicy(trusted []netip.Prefix) proxyproto.ConnPolicyFunc {
	return func(options proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
		if tcp, isTCP := options.Upstream.(*net.TCPAddr); isTCP {
			if addr, parsed := netip.AddrFromSlice(tcp.IP); parsed {
				addr = addr.Unmap()
				for _, prefix := range trusted {
					if prefix.Contains(addr) {
						return proxyproto.USE, nil
					}
				}
			}
		}
		return proxyproto.IGNORE, nil
	}
}

func (s *Server) Stop(ctx context.Context) {
	var wg sync.WaitGroup
	if s.h3 != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.h3.srv.Shutdown(ctx); err != nil {
				log.Error().Err(err).Str("listener", "HTTP/3").Msg("Can not stop HTTP/3 server")
			}
			s.h3.close()
		}()
	}
	for _, l := range s.listeners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.srv.Shutdown(ctx); err != nil {
				log.Error().Err(err).Str("listener", l.name).Msg("Can not stop HTTP(S) server")
			}
		}()
	}
	wg.Wait()
}

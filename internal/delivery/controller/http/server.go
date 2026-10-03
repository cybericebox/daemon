package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
)

type (
	// Server runs the API listener and, around it, the optional extra ones:
	//   - plain mode: the API on Port;
	//   - TLS mode: the API on TLSPort (HTTP/2, optional client certificate) plus a plain listener on
	//     HEALTH_BIND:HealthPort that serves only the health route (the kubelet cannot present a client cert);
	//   - InternalPort set: a plain listener on HEALTH_BIND with the same API handler for in-cluster callers.
	Server struct {
		listeners []*listener
	}

	listener struct {
		name string
		srv  *http.Server
		tls  bool
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

// newTLSConfig builds the server TLS config: TLS 1.2+, HTTP/2, the certificate reloaded on change and, with
// client auth, a required and verified client certificate whose CA bundle is reloaded on change too. With
// client auth on and no usable bundle, every handshake fails (closed, never open).
func newTLSConfig(cfg *config.TLSConfig) *tls.Config {
	cr := newCertReloader(cfg.CertFile, cfg.KeyFile)
	if _, err := cr.GetCertificate(nil); err != nil {
		log.Error().Err(err).Msg("Failed loading initial certificate")
	}
	base := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: cr.GetCertificate,
	}
	if !cfg.ClientAuth {
		return base
	}
	pr := &poolReloader{file: cfg.CAFile}
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
		perConn.ClientAuth = tls.RequireAndVerifyClientCert
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
			MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
			TLSConfig:      tlsCfg,
		}
	}
	s := &Server{}
	if cfg.TLS.Enabled {
		s.listeners = append(s.listeners,
			&listener{name: "HTTPS", srv: newHTTP(net.JoinHostPort(cfg.Host, cfg.TLSPort), handler, newTLSConfig(&cfg.TLS)), tls: true},
			&listener{name: "health", srv: newHTTP(net.JoinHostPort(healthBind, cfg.HealthPort), healthHandler(), nil)},
		)
	} else {
		s.listeners = append(s.listeners, &listener{name: "HTTP", srv: newHTTP(net.JoinHostPort(cfg.Host, cfg.Port), handler, nil)})
	}
	if cfg.InternalPort != "" {
		s.listeners = append(s.listeners, &listener{name: "internal", srv: newHTTP(net.JoinHostPort(healthBind, cfg.InternalPort), handler, nil)})
	}
	return s
}

func (s *Server) Start() {
	for _, l := range s.listeners {
		go func() {
			var err error
			if l.tls {
				err = l.srv.ListenAndServeTLS("", "")
			} else {
				err = l.srv.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Str("listener", l.name).Msg("Can not start HTTP(S) server")
			}
		}()
	}
}

func (s *Server) Stop(ctx context.Context) {
	var wg sync.WaitGroup
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

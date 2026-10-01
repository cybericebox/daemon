package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
)

type (
	Server struct {
		httpServer *http.Server
		tlsEnabled bool
	}
	certReloader struct {
		CertFile          string // path to the x509 certificate for https
		KeyFile           string // path to the x509 private key matching `CertFile`
		cachedCert        *tls.Certificate
		cachedCertModTime time.Time
	}
)

func newCertReloader(certFile, keyFile string) *certReloader {
	return &certReloader{
		CertFile: certFile,
		KeyFile:  keyFile,
	}
}

func (cr *certReloader) GetCertificate(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
	stat, err := os.Stat(cr.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed checking key file modification time: %w", err)
	}

	if cr.cachedCert == nil || stat.ModTime().After(cr.cachedCertModTime) {
		pair, err := tls.LoadX509KeyPair(cr.CertFile, cr.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed loading tls key pair: %w", err)
		}

		cr.cachedCert = &pair
		cr.cachedCertModTime = stat.ModTime()
	}

	return cr.cachedCert, nil
}

func NewServer(cfg *config.HTTPServerConfig, handler http.Handler) *Server {
	if cfg.TLS.Enabled {
		cr := newCertReloader(cfg.TLS.CertFile, cfg.TLS.KeyFile)

		// initial load of the certificate
		if _, err := cr.GetCertificate(nil); err != nil {
			log.Error().Err(err).Msg("Failed loading initial certificate")
		}

		return &Server{
			httpServer: &http.Server{
				Addr:           fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
				Handler:        handler,
				ReadTimeout:    cfg.ReadTimeout,
				WriteTimeout:   cfg.WriteTimeout,
				MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
				TLSConfig: &tls.Config{
					GetCertificate: cr.GetCertificate,
				},
			},
			tlsEnabled: true,
		}
	}

	server := http.Server{
		Addr:           fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:        handler,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
	}

	return &Server{
		httpServer: &server,
		tlsEnabled: cfg.TLS.Enabled,
	}
}

func (s *Server) Start() {
	go func() {
		if s.tlsEnabled {
			if err := s.httpServer.ListenAndServeTLS(
				"",
				"",
			); err != nil &&
				!errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("Can not start HTTPS server")
			}
		} else {
			if err := s.httpServer.ListenAndServe(); err != nil &&
				!errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("Can not start HTTP server")
			}
		}
	}()
}

func (s *Server) Stop(ctx context.Context) {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("Can not stop HTTP(S) server")
	}
}

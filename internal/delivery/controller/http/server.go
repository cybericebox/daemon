package http

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/rs/zerolog/log"
	"net/http"
)

type Server struct {
	httpServer *http.Server
	TLS        config.HTTPTLSConfig
}

func NewServer(cfg *config.ServerConfig, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:           fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
			Handler:        handler,
			ReadTimeout:    cfg.ReadTimeout,
			WriteTimeout:   cfg.WriteTimeout,
			MaxHeaderBytes: cfg.MaxHeaderMegabytes << 20,
		},
		TLS: cfg.TLS,
	}
}

func (s *Server) Start() {
	go func() {
		if s.TLS.Enabled {
			if err := s.httpServer.ListenAndServeTLS(s.TLS.CertFile, s.TLS.KeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("Can not start HTTPS server")
			}
		} else {
			if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

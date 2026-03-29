package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// Server wraps an http.Server with a clean Run/Shutdown lifecycle.
type Server struct {
	httpServer *http.Server
}

// New creates a Server listening on the given port.
func New(port int, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
	}
}

// Run starts the HTTP server in a goroutine and blocks until ctx is cancelled.
// It then performs a graceful shutdown with a 30-second timeout.
func (s *Server) Run(ctx context.Context, port int) error {
	go func() {
		log.Info().Int("port", port).Msg("HTTP server starting")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info().Msg("server stopped")
	return nil
}

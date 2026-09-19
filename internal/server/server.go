package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"nexus-commerce/internal/config"

	"github.com/go-chi/chi/v5"
)

const shutdownTimeout = 5 * time.Second

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	router chi.Router
}

// New tạo HTTP server với logger và route dependencies đã được composition root wiring.
func New(cfg config.Config, logger *slog.Logger, routeDependencies RouteDependencies) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	router := chi.NewRouter()
	RegisterRoutes(router, routeDependencies)

	return &Server{
		cfg:    cfg,
		logger: logger,
		router: router,
	}
}

// Run lắng nghe HTTP, chờ termination signal và drain request trong shutdown timeout.
func (s *Server) Run() error {
	httpServer := &http.Server{
		Addr:    ":" + s.cfg.Port,
		Handler: s.router,
	}

	serverErrors := make(chan error, 1)
	go func() {
		s.logger.Info("HTTP server started", "address", httpServer.Addr)

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	signalContext, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("listen and serve: %w", err)
	case <-signalContext.Done():
		s.logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	s.logger.Info("HTTP server stopped")
	return nil
}

package main

import (
	"log/slog"
	"os"

	"nexus-commerce/internal/config"
	"nexus-commerce/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	srv := server.New(cfg, logger)

	if err := srv.Run(); err != nil {
		logger.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
}

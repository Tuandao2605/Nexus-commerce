// Command api là entrypoint khởi tạo cấu hình, PostgreSQL và HTTP server của Nexus Commerce.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"nexus-commerce/internal/config"
	"nexus-commerce/internal/database"
	"nexus-commerce/internal/server"
)

// main tạo logger, chạy ứng dụng và kết thúc process với mã lỗi khi bootstrap hoặc server thất bại.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

// run sở hữu lifecycle ứng dụng: load config, kết nối PostgreSQL với timeout, chạy server và đóng pool khi dừng.
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	databaseContext, cancelDatabaseStartup := context.WithTimeout(
		context.Background(),
		cfg.Database.StartupTimeout,
	)
	pool, err := database.NewPool(databaseContext, cfg.Database)
	cancelDatabaseStartup()
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer pool.Close()

	logger.Info("PostgreSQL connection pool initialized")

	srv := server.New(cfg, logger)
	if err := srv.Run(); err != nil {
		return fmt.Errorf("run HTTP server: %w", err)
	}

	return nil
}

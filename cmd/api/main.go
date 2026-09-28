// Command api là entrypoint khởi tạo cấu hình, PostgreSQL và HTTP server của Nexus Commerce.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"nexus-commerce/internal/auth"
	"nexus-commerce/internal/config"
	"nexus-commerce/internal/database"
	"nexus-commerce/internal/server"
	usermodule "nexus-commerce/internal/user"
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

	passwordHasher, err := auth.NewArgon2idHasher(auth.DefaultArgon2idParams())
	if err != nil {
		return fmt.Errorf("initialize password hasher: %w", err)
	}
	accessTokenManager, err := auth.NewRS256AccessTokenManagerFromFile(
		cfg.Auth.AccessTokenPrivateKeyFile,
		auth.AccessTokenConfig{
			Issuer:   cfg.Auth.AccessTokenIssuer,
			Audience: cfg.Auth.AccessTokenAudience,
			ClientID: cfg.Auth.AccessTokenClientID,
			KeyID:    cfg.Auth.AccessTokenKeyID,
			Lifetime: cfg.Auth.AccessTokenTTL,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize access token manager: %w", err)
	}
	registrationRepository := auth.NewPostgresRegistrationRepository(
		pool,
		usermodule.NewPostgresRegistrationWriter(),
	)
	registrationService := auth.NewRegistrationService(registrationRepository, passwordHasher)
	registrationHandler := auth.NewRegistrationHandler(registrationService, logger)
	loginRepository := auth.NewPostgresLoginRepository(pool)
	loginService, err := auth.NewLoginService(
		loginRepository,
		usermodule.NewPostgresLoginStatusReader(pool),
		passwordHasher,
		logger,
	)
	if err != nil {
		return fmt.Errorf("initialize login service: %w", err)
	}
	loginTokenService, err := auth.NewLoginTokenService(loginService, accessTokenManager)
	if err != nil {
		return fmt.Errorf("initialize login token service: %w", err)
	}
	loginHandler := auth.NewLoginHandler(loginTokenService, logger)

	srv := server.New(cfg, logger, server.RouteDependencies{
		Registration: registrationHandler,
		Login:        loginHandler,
	})
	if err := srv.Run(); err != nil {
		return fmt.Errorf("run HTTP server: %w", err)
	}

	return nil
}

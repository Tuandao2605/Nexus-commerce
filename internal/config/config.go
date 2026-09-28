// Package config đọc biến môi trường và kiểm tra cấu hình cần thiết để ứng dụng khởi động an toàn.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultPort = "8080"

const (
	defaultAccessTokenIssuer   = "nexus-commerce"
	defaultAccessTokenAudience = "nexus-commerce-api"
	defaultAccessTokenClientID = "nexus-commerce"
	defaultAccessTokenKeyID    = "local-rsa-1"
	defaultAccessTokenTTL      = "15m"
)

type Config struct {
	Port     string
	Database DatabaseConfig
	Auth     AuthConfig
}

type AuthConfig struct {
	AccessTokenPrivateKeyFile string
	AccessTokenIssuer         string
	AccessTokenAudience       string
	AccessTokenClientID       string
	AccessTokenKeyID          string
	AccessTokenTTL            time.Duration
}

type DatabaseConfig struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	StartupTimeout    time.Duration
}

// Load đọc cấu hình HTTP và PostgreSQL từ biến môi trường.
// Hàm dùng cổng mặc định 8080 khi PORT trống và yêu cầu cấu hình database cùng access-token signing key.
func Load() (Config, error) {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = defaultPort
	}

	databaseConfig, err := loadDatabaseConfig()
	if err != nil {
		return Config{}, fmt.Errorf("load database config: %w", err)
	}
	authConfig, err := loadAuthConfig()
	if err != nil {
		return Config{}, fmt.Errorf("load auth config: %w", err)
	}

	return Config{
		Port:     port,
		Database: databaseConfig,
		Auth:     authConfig,
	}, nil
}

// loadAuthConfig reads access-token signing and claim settings with safe defaults for public claim values.
func loadAuthConfig() (AuthConfig, error) {
	privateKeyFile, err := requireEnv("AUTH_ACCESS_TOKEN_PRIVATE_KEY_FILE")
	if err != nil {
		return AuthConfig{}, err
	}
	lifetime, err := optionalPositiveDurationEnv("AUTH_ACCESS_TOKEN_TTL", defaultAccessTokenTTL)
	if err != nil {
		return AuthConfig{}, err
	}
	if lifetime < time.Minute || lifetime > 15*time.Minute {
		return AuthConfig{}, fmt.Errorf("AUTH_ACCESS_TOKEN_TTL must be between 1m and 15m")
	}
	return AuthConfig{
		AccessTokenPrivateKeyFile: privateKeyFile,
		AccessTokenIssuer:         optionalEnv("AUTH_ACCESS_TOKEN_ISSUER", defaultAccessTokenIssuer),
		AccessTokenAudience:       optionalEnv("AUTH_ACCESS_TOKEN_AUDIENCE", defaultAccessTokenAudience),
		AccessTokenClientID:       optionalEnv("AUTH_ACCESS_TOKEN_CLIENT_ID", defaultAccessTokenClientID),
		AccessTokenKeyID:          optionalEnv("AUTH_ACCESS_TOKEN_KEY_ID", defaultAccessTokenKeyID),
		AccessTokenTTL:            lifetime,
	}, nil
}

// loadDatabaseConfig đọc các biến DATABASE_* và kiểm tra quan hệ giữa các thông số pool.
func loadDatabaseConfig() (DatabaseConfig, error) {
	databaseURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return DatabaseConfig{}, err
	}

	maxConns, err := parseInt32Env("DATABASE_MAX_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}

	minConns, err := parseInt32Env("DATABASE_MIN_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}

	maxConnLifetime, err := parsePositiveDurationEnv("DATABASE_MAX_CONN_LIFETIME")
	if err != nil {
		return DatabaseConfig{}, err
	}

	maxConnIdleTime, err := parsePositiveDurationEnv("DATABASE_MAX_CONN_IDLE_TIME")
	if err != nil {
		return DatabaseConfig{}, err
	}

	healthCheckPeriod, err := parsePositiveDurationEnv("DATABASE_HEALTH_CHECK_PERIOD")
	if err != nil {
		return DatabaseConfig{}, err
	}

	startupTimeout, err := parsePositiveDurationEnv("DATABASE_STARTUP_TIMEOUT")
	if err != nil {
		return DatabaseConfig{}, err
	}

	if maxConns <= 0 {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_MAX_CONNS must be greater than zero")
	}

	if minConns < 0 {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_MIN_CONNS must not be negative")
	}

	if minConns > maxConns {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	}

	return DatabaseConfig{
		URL:               databaseURL,
		MaxConns:          maxConns,
		MinConns:          minConns,
		MaxConnLifetime:   maxConnLifetime,
		MaxConnIdleTime:   maxConnIdleTime,
		HealthCheckPeriod: healthCheckPeriod,
		StartupTimeout:    startupTimeout,
	}, nil
}

// requireEnv trả về giá trị đã loại bỏ khoảng trắng hoặc báo lỗi khi biến môi trường bị thiếu.
func requireEnv(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}

	return value, nil
}

// optionalEnv returns a trimmed environment value or a safe documented default when absent.
func optionalEnv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// optionalPositiveDurationEnv parses an optional positive Go duration and uses the default when unset.
func optionalPositiveDurationEnv(key string, fallback string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		value = fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return duration, nil
}

// parseInt32Env đọc một biến môi trường bắt buộc và chuyển nó thành số nguyên int32.
func parseInt32Env(key string) (int32, error) {
	value, err := requireEnv(key)
	if err != nil {
		return 0, err
	}

	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer: %w", key, err)
	}

	return int32(parsed), nil
}

// parsePositiveDurationEnv đọc duration theo cú pháp Go như 5s, 30m, 1h và yêu cầu giá trị lớn hơn 0.
func parsePositiveDurationEnv(key string) (time.Duration, error) {
	value, err := requireEnv(key)
	if err != nil {
		return 0, err
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}

	return duration, nil
}

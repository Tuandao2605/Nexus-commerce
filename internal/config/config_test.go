// File này kiểm thử việc đọc cấu hình hợp lệ và các trường hợp cấu hình database phải bị từ chối.
package config

import (
	"strings"
	"testing"
	"time"
)

// TestLoad kiểm tra PORT mặc định/tùy chỉnh và toàn bộ quy tắc validation của DATABASE_*.
func TestLoad(t *testing.T) {
	tests := []struct {
		name          string
		port          string
		mutateEnv     func(t *testing.T)
		wantPort      string
		wantErrSubstr string
	}{
		{
			name:     "valid config uses default port and access token defaults",
			wantPort: defaultPort,
		},
		{
			name:     "valid config uses custom port",
			port:     "9000",
			wantPort: "9000",
		},
		{
			name: "missing signing key path",
			mutateEnv: func(t *testing.T) {
				t.Setenv("AUTH_ACCESS_TOKEN_PRIVATE_KEY_FILE", "")
			},
			wantErrSubstr: "AUTH_ACCESS_TOKEN_PRIVATE_KEY_FILE is required",
		},
		{
			name: "invalid access token duration",
			mutateEnv: func(t *testing.T) {
				t.Setenv("AUTH_ACCESS_TOKEN_TTL", "later")
			},
			wantErrSubstr: "AUTH_ACCESS_TOKEN_TTL must be a valid duration",
		},
		{
			name: "access token duration exceeds limit",
			mutateEnv: func(t *testing.T) {
				t.Setenv("AUTH_ACCESS_TOKEN_TTL", "16m")
			},
			wantErrSubstr: "AUTH_ACCESS_TOKEN_TTL must be between 1m and 15m",
		},
		{
			name: "non-positive access token duration",
			mutateEnv: func(t *testing.T) {
				t.Setenv("AUTH_ACCESS_TOKEN_TTL", "0s")
			},
			wantErrSubstr: "AUTH_ACCESS_TOKEN_TTL must be greater than zero",
		},
		{
			name: "missing database URL",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_URL", "")
			},
			wantErrSubstr: "DATABASE_URL is required",
		},
		{
			name: "max connections is zero",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_MAX_CONNS", "0")
			},
			wantErrSubstr: "DATABASE_MAX_CONNS must be greater than zero",
		},
		{
			name: "minimum connections is negative",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_MIN_CONNS", "-1")
			},
			wantErrSubstr: "DATABASE_MIN_CONNS must not be negative",
		},
		{
			name: "minimum connections exceeds maximum",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_MAX_CONNS", "2")
				t.Setenv("DATABASE_MIN_CONNS", "3")
			},
			wantErrSubstr: "DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS",
		},
		{
			name: "invalid connection count",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_MAX_CONNS", "many")
			},
			wantErrSubstr: "DATABASE_MAX_CONNS must be a valid integer",
		},
		{
			name: "invalid duration",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_STARTUP_TIMEOUT", "soon")
			},
			wantErrSubstr: "DATABASE_STARTUP_TIMEOUT must be a valid duration",
		},
		{
			name: "non-positive duration",
			mutateEnv: func(t *testing.T) {
				t.Setenv("DATABASE_MAX_CONN_IDLE_TIME", "0s")
			},
			wantErrSubstr: "DATABASE_MAX_CONN_IDLE_TIME must be greater than zero",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidDatabaseEnv(t)
			t.Setenv("PORT", test.port)
			if test.mutateEnv != nil {
				test.mutateEnv(t)
			}

			cfg, err := Load()
			if test.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("Load() error = nil, want error containing %q", test.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), test.wantErrSubstr) {
					t.Fatalf("Load() error = %q, want error containing %q", err, test.wantErrSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Port != test.wantPort {
				t.Fatalf("Load() Port = %q, want %q", cfg.Port, test.wantPort)
			}
			if cfg.Database.StartupTimeout != 5*time.Second {
				t.Fatalf("Load() StartupTimeout = %s, want 5s", cfg.Database.StartupTimeout)
			}
			if cfg.Auth.AccessTokenTTL != 15*time.Minute || cfg.Auth.AccessTokenPrivateKeyFile != "./secrets/access-token-rsa-private.pem" {
				t.Fatalf("Load() Auth = %#v, want 15m and configured key file", cfg.Auth)
			}
		})
	}
}

// setValidDatabaseEnv thiết lập một bộ biến môi trường hợp lệ làm dữ liệu nền cho từng test case.
func setValidDatabaseEnv(t *testing.T) {
	t.Helper()

	t.Setenv("DATABASE_URL", "postgres://nexus:secret@localhost:5432/nexus_commerce?sslmode=disable")
	t.Setenv("DATABASE_MAX_CONNS", "20")
	t.Setenv("DATABASE_MIN_CONNS", "2")
	t.Setenv("DATABASE_MAX_CONN_LIFETIME", "1h")
	t.Setenv("DATABASE_MAX_CONN_IDLE_TIME", "30m")
	t.Setenv("DATABASE_HEALTH_CHECK_PERIOD", "1m")
	t.Setenv("DATABASE_STARTUP_TIMEOUT", "5s")
	t.Setenv("AUTH_ACCESS_TOKEN_PRIVATE_KEY_FILE", "./secrets/access-token-rsa-private.pem")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL", "")
	t.Setenv("AUTH_ACCESS_TOKEN_ISSUER", "")
	t.Setenv("AUTH_ACCESS_TOKEN_AUDIENCE", "")
	t.Setenv("AUTH_ACCESS_TOKEN_CLIENT_ID", "")
	t.Setenv("AUTH_ACCESS_TOKEN_KEY_ID", "")
}

// File này kiểm thử parsing DSN và kết nối pgxpool với PostgreSQL test thật.
package database

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"nexus-commerce/internal/config"
)

// TestNewPoolRejectsInvalidDSN xác nhận NewPool từ chối chuỗi kết nối sai cú pháp mà không trả pool.
func TestNewPoolRejectsInvalidDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pool, err := NewPool(ctx, testDatabaseConfig("://invalid"))
	if pool != nil {
		pool.Close()
		t.Fatal("NewPool() returned a pool for an invalid DSN")
	}
	if err == nil {
		t.Fatal("NewPool() error = nil, want parse error")
	}
}

// TestNewPoolIntegration kết nối TEST_DATABASE_URL và chạy SELECT 1 để kiểm tra toàn bộ đường kết nối.
func TestNewPoolIntegration(t *testing.T) {
	databaseURL := requireTestDatabaseURL(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, testDatabaseConfig(databaseURL))
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	var result int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if result != 1 {
		t.Fatalf("SELECT 1 result = %d, want 1", result)
	}
}

// TestNewPoolIntegrationRejectsUnknownDatabase xác nhận NewPool báo lỗi khi database đích không tồn tại.
func TestNewPoolIntegrationRejectsUnknownDatabase(t *testing.T) {
	databaseURL := requireTestDatabaseURL(t)

	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	parsedURL.Path = "/nexus_missing_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, testDatabaseConfig(parsedURL.String()))
	if pool != nil {
		pool.Close()
		t.Fatal("NewPool() returned a pool for an unknown database")
	}
	if err == nil {
		t.Fatal("NewPool() error = nil, want connection error")
	}
}

// requireTestDatabaseURL lấy URL database test; test sẽ fail ở chế độ bắt buộc và skip ở chế độ thường nếu URL thiếu.
func requireTestDatabaseURL(t *testing.T) string {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL != "" {
		return databaseURL
	}

	if os.Getenv("REQUIRE_DATABASE_INTEGRATION") == "1" {
		t.Fatal("TEST_DATABASE_URL is required for database integration tests")
	}

	t.Skip("TEST_DATABASE_URL is not set")
	return ""
}

// testDatabaseConfig tạo cấu hình pool nhỏ, có timeout rõ ràng để dùng thống nhất trong database tests.
func testDatabaseConfig(databaseURL string) config.DatabaseConfig {
	return config.DatabaseConfig{
		URL:               databaseURL,
		MaxConns:          5,
		MinConns:          1,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
		StartupTimeout:    5 * time.Second,
	}
}

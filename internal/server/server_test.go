package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"nexus-commerce/internal/config"

	"github.com/go-chi/chi/v5"
)

func TestHealth(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router)

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	expectedBody := map[string]string{"status": "ok"}
	if !reflect.DeepEqual(body, expectedBody) {
		t.Errorf("expected body %v, got %v", expectedBody, body)
	}
}

func TestLoad_DefaultPort(t *testing.T) {
	t.Setenv("PORT", "")

	cfg := config.Load()

	if cfg.Port != "8080" {
		t.Fatalf("expected default port 8080, got %s", cfg.Port)
	}
}

func TestLoad_CustomPort(t *testing.T) {
	t.Setenv("PORT", "9000")

	cfg := config.Load()

	if cfg.Port != "9000" {
		t.Fatalf("expected port 9000, got %s", cfg.Port)
	}
}

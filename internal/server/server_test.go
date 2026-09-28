// File này kiểm thử health contract và wiring business handler của Chi router.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestHealth xác nhận health route giữ nguyên 200 JSON contract khi business dependencies chưa được inject.
func TestHealth(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, RouteDependencies{})

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

// TestRegistrationRouteDelegatesToInjectedHandler xác nhận POST resource được chuyển đúng sang Auth handler.
func TestRegistrationRouteDelegatesToInjectedHandler(t *testing.T) {
	called := false
	registrationHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})
	router := chi.NewRouter()
	RegisterRoutes(router, RouteDependencies{Registration: registrationHandler})

	request := httptest.NewRequest(http.MethodPost, "/auth/registrations", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if !called {
		t.Fatal("registration handler was not called")
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
}

// TestLoginRouteDelegatesToInjectedHandler xác nhận POST /auth/login được chuyển đúng sang Auth handler.
func TestLoginRouteDelegatesToInjectedHandler(t *testing.T) {
	called := false
	loginHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.Method == http.MethodPost
		w.WriteHeader(http.StatusOK)
	})
	router := chi.NewRouter()
	RegisterRoutes(router, RouteDependencies{Login: loginHandler})

	request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if !called {
		t.Fatal("login handler was not called")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

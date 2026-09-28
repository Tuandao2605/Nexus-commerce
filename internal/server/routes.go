// File này đăng ký health route và các business HTTP handlers được application bootstrap inject vào server.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RouteDependencies chứa các HTTP handler đã được wiring đầy đủ, không chứa business logic trong server package.
type RouteDependencies struct {
	Registration http.Handler
	Login        http.Handler
}

// RegisterRoutes gắn process health và Auth HTTP handlers vào Chi router.
func RegisterRoutes(router chi.Router, dependencies RouteDependencies) {
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	if dependencies.Registration != nil {
		router.Method(http.MethodPost, "/auth/registrations", dependencies.Registration)
	}
	if dependencies.Login != nil {
		router.Method(http.MethodPost, "/auth/login", dependencies.Login)
	}
}

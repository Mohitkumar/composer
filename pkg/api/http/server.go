package http

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/coord"
)

// Server implements HTTP API for external clients
type Server struct {
	router      *chi.Mux
	config      *api.APIConfig
	coordinator *coord.Coordinator
}

// NewServer creates a new HTTP API server
func NewServer(config *api.APIConfig, coordinator *coord.Coordinator) *Server {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Create handlers with coordinator
	h := &handlers{coordinator: coordinator}

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Workflow management endpoints
		r.Route("/workflows", func(r chi.Router) {
			r.Post("/", h.createWorkflow)
			r.Get("/", h.listWorkflows)
			r.Get("/{id}", h.getWorkflow)
			r.Put("/{id}", h.updateWorkflow)
			r.Delete("/{id}", h.deleteWorkflow)
			r.Post("/{id}/execute", h.executeWorkflow)
			r.Get("/{id}/status", h.getWorkflowStatus)
		})

		// Execution endpoints
		r.Route("/executions", func(r chi.Router) {
			r.Get("/", h.listExecutions)
			r.Get("/{id}", h.getExecution)
			r.Post("/{id}/cancel", h.cancelExecution)
		})
	})

	return &Server{
		router:      r,
		config:      config,
		coordinator: coordinator,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	return http.ListenAndServe(fmt.Sprintf(":%d", s.config.HTTPPort), s.router)
}

// Handler returns the HTTP handler
func (s *Server) Handler() http.Handler {
	return s.router
}

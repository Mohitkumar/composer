package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/auth"
	"github.com/mohitkumar/composer/pkg/coord"
)

// Server implements HTTP API for external clients
type Server struct {
	router      *chi.Mux
	config      *api.APIConfig
	coordinator *coord.Coordinator
	jwtService  *auth.JWTService
	userService *auth.UserService
}

// corsMiddleware adds CORS headers to responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "3600")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// NewServer creates a new HTTP API server
func NewServer(config *api.APIConfig, coordinator *coord.Coordinator) *Server {
	r := chi.NewRouter()

	// Initialize auth services
	jwtService := auth.NewJWTService(config.JWTSecret, 24*time.Hour) // Token expires in 24 hours
	userService := auth.NewUserService(coordinator.DB)

	// Middleware
	r.Use(corsMiddleware)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Create handlers with coordinator and auth services
	h := &handlers{
		coordinator: coordinator,
		jwtService:  jwtService,
		userService: userService,
	}

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes (no JWT required)
		r.Post("/auth/login", h.login)
		r.Post("/auth/logout", h.logout)

		// Protected routes (require JWT)
		r.Group(func(r chi.Router) {
			// Apply JWT middleware to all routes in this group
			r.Use(JWTAuthMiddleware(jwtService))
			// Workflow management endpoints
			r.Route("/workflows", func(r chi.Router) {
				r.Post("/", h.createWorkflow)
				r.Get("/", h.listWorkflows)
				r.Get("/{id}", h.getWorkflow)
				r.Put("/{id}", h.updateWorkflow)
				r.Delete("/{id}", h.deleteWorkflow)
				r.Post("/{id}/deploy", h.deployWorkflow)
				r.Post("/{id}/rollback", h.rollbackWorkflow)
				r.Get("/{id}/previous-deployed-versions", h.getPreviousDeployedVersions)
				r.Post("/{id}/execute", h.executeWorkflow)
				r.Get("/{id}/status", h.getWorkflowStatus)
				r.Get("/{id}/issue-summary", h.getWorkflowIssueSummary)
			})

			// Execution endpoints
			r.Route("/executions", func(r chi.Router) {
				r.Get("/", h.listExecutions)
				r.Get("/{id}", h.getExecution)
				r.Post("/{id}/cancel", h.cancelExecution)
				r.Post("/{id}/retry", h.retryExecution)
			})

			// Template endpoints
			r.Route("/templates", func(r chi.Router) {
				r.Route("/nodes", func(r chi.Router) {
					r.Get("/", h.listNodeTemplates)
					r.Get("/{id}", h.getNodeTemplate)
				})
				r.Route("/workflows", func(r chi.Router) {
					r.Get("/", h.listWorkflowTemplates)
					r.Get("/{id}", h.getWorkflowTemplate)
				})
			})

			// Configuration endpoints
			r.Route("/configurations", func(r chi.Router) {
				r.Get("/", h.listConfigurations)
				r.Post("/", h.createConfiguration)
				r.Get("/{id}", h.getConfiguration)
				r.Put("/{id}", h.updateConfiguration)
				r.Delete("/{id}", h.deleteConfiguration)
			})
		})
	})

	return &Server{
		router:      r,
		config:      config,
		coordinator: coordinator,
		jwtService:  jwtService,
		userService: userService,
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

package api

import (
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger"
)

// NewRouter wires all HTTP routes to their handlers and wraps with logging middleware.
func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()

	// Swagger UI — accessible at /swagger/index.html
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	// Job lifecycle
	mux.HandleFunc("POST /api/jobs", h.CreateJob)
	mux.HandleFunc("POST /api/jobs/{job_id}/start", h.StartJob)
	mux.HandleFunc("POST /api/jobs/{job_id}/stop", h.StopJob)
	mux.HandleFunc("POST /api/jobs/{job_id}/rollback", h.RollbackJob)

	// Job queries
	mux.HandleFunc("GET /api/jobs", h.ListJobs)
	mux.HandleFunc("GET /api/jobs/{job_id}", h.GetJob)
	mux.HandleFunc("GET /api/jobs/{job_id}/validate", h.ValidateJob)
	mux.HandleFunc("GET /api/jobs/{job_id}/errors", h.GetJobErrors)

	// Metadata & health
	mux.HandleFunc("GET /api/tables", h.ListTables)
	mux.HandleFunc("GET /healthz", h.Healthz)

	return LoggingMiddleware(mux)
}

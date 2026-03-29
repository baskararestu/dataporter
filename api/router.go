package api

import "net/http"

// NewRouter wires all HTTP routes to their handlers.
func NewRouter(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()

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

	return mux
}

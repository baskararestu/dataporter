package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/baskararestu/dataporter/config"
	"github.com/baskararestu/dataporter/database"
	"github.com/baskararestu/dataporter/migration"
	"github.com/baskararestu/dataporter/model"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// maxConcurrentJobs is the maximum number of migration goroutines that may run simultaneously.
// Requests beyond this limit are rejected with HTTP 429.
const maxConcurrentJobs = 5

// Handler holds all HTTP handler dependencies.
type Handler struct {
	appCtx      context.Context
	jobRepo     *repository.JobRepository
	migrator    *migration.Migrator
	tracker     *monitoring.Tracker
	sourcePool  *pgxpool.Pool
	targetDB    *pgxpool.Pool
	sourceDBCfg config.DBConfig
	emrExtraSQL string
	appEnv      string
	// activeJobs stores context.CancelFunc per running job UUID.
	// StopJob calls the cancel func to actually stop the goroutine.
	activeJobs sync.Map // map[uuid.UUID]context.CancelFunc
	// sem is a buffered channel used as a counting semaphore to cap concurrent migration jobs.
	// Acquire: send to channel. Release: receive from channel.
	sem chan struct{}
}

// NewHandler creates a new API handler.
func NewHandler(
	appCtx context.Context,
	jobRepo *repository.JobRepository,
	migrator *migration.Migrator,
	tracker *monitoring.Tracker,
	sourcePool *pgxpool.Pool,
	targetDB *pgxpool.Pool,
	sourceDBCfg config.DBConfig,
	emrExtraSQL string,
	appEnv string,
) *Handler {
	return &Handler{
		appCtx:      appCtx,
		jobRepo:     jobRepo,
		migrator:    migrator,
		tracker:     tracker,
		sourcePool:  sourcePool,
		targetDB:    targetDB,
		sourceDBCfg: sourceDBCfg,
		emrExtraSQL: emrExtraSQL,
		appEnv:      appEnv,
		sem:         make(chan struct{}, maxConcurrentJobs),
	}
}

// CreateJob creates a new migration job in pending state.
//
// @Summary      Create migration job
// @Description  Creates a new migration job in pending state. The job will not start until POST /api/jobs/{job_id}/start is called.
// @Tags         jobs
// @Accept       json
// @Produce      json
// @Param        body  body      model.CreateJobRequest  true  "Job parameters"
// @Success      201   {object}  model.APIResponse
// @Failure      400   {object}  model.APIResponse
// @Failure      409   {object}  model.APIResponse
// @Failure      422   {object}  model.APIResponse
// @Failure      500   {object}  model.APIResponse
// @Router       /api/jobs [post]
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req model.CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.SourceTable == "" || req.TargetTable == "" {
		jsonError(w, http.StatusBadRequest, "source_table and target_table are required")
		return
	}
	if req.BatchSize <= 0 {
		req.BatchSize = 5000
	}
	if _, err := migration.LookupTable(req.SourceTable); err != nil {
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Auto-detect checkpoint from last completed job for this source+target pair.
	var startFromID int64
	if prev, err := h.jobRepo.GetLatestCompleted(r.Context(), req.SourceTable, req.TargetTable); err == nil && prev != nil {
		if prev.Processed >= prev.TotalRecords && prev.TotalRecords > 0 {
			startFromID = prev.LastProcessedID
			log.Info().Str("source_table", req.SourceTable).
				Int64("start_from_id", startFromID).
				Msg("incremental migration: continuing from previous job checkpoint")
		}
	}

	log.Info().Str("source_table", req.SourceTable).Str("target_table", req.TargetTable).
		Int("batch_size", req.BatchSize).Bool("dry_run", req.DryRun).Msg("creating migration job")
	job, err := h.jobRepo.Create(r.Context(), req, startFromID)
	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			jsonError(w, http.StatusConflict, "an active job for this source+target already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create job: "+err.Error())
		return
	}
	log.Info().Str("job_id", job.JobID.String()).Msg("migration job created")
	jsonOK(w, http.StatusCreated, "job created", job)
}

// StartJob begins (or resumes) a migration job in a background goroutine.
//
// @Summary      Start migration job
// @Description  Starts or resumes a pending/paused migration job asynchronously.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      202     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      404     {object}  model.APIResponse
// @Failure      409     {object}  model.APIResponse
// @Router       /api/jobs/{job_id}/start [post]
func (h *Handler) StartJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}
	if job.Status == model.JobStatusRunning {
		jsonError(w, http.StatusConflict, "job is already running")
		return
	}
	if job.Status == model.JobStatusCompleted || job.Status == model.JobStatusRolledBack {
		jsonError(w, http.StatusConflict, "job is in a terminal state and cannot be restarted")
		return
	}
	log.Info().Str("job_id", jobID.String()).Str("source", job.SourceTable).Str("target", job.TargetTable).
		Str("current_status", string(job.Status)).Msg("starting migration job")

	// Acquire semaphore slot — reject immediately if at capacity.
	select {
	case h.sem <- struct{}{}:
	default:
		jsonError(w, http.StatusTooManyRequests, "max concurrent migrations reached, try again later")
		return
	}

	// Create a per-job context so StopJob can cancel this goroutine independently
	// from the global app context. Derive from appCtx so SIGTERM still stops everything.
	jobCtx, cancel := context.WithCancel(h.appCtx)
	h.activeJobs.Store(jobID, cancel)

	go func() {
		defer func() {
			// Always clean up: release semaphore slot, cancel func, and tracker entry.
			<-h.sem
			h.activeJobs.Delete(jobID)
			cancel()
		}()
		if err := h.migrator.Run(jobCtx, job); err != nil {
			log.Error().Str("job_id", jobID.String()).Err(err).Msg("migration goroutine error")
		}
	}()
	jsonOK(w, http.StatusAccepted, "job starting", map[string]string{"job_id": jobID.String(), "status": "starting"})
}

// StopJob signals the running job to pause.
//
// @Summary      Stop (pause) migration job
// @Description  Signals a running migration job to pause. It can be resumed with the start endpoint.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      200     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      404     {object}  model.APIResponse
// @Failure      409     {object}  model.APIResponse
// @Failure      500     {object}  model.APIResponse
// @Router       /api/jobs/{job_id}/stop [post]
func (h *Handler) StopJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}
	if job.Status != model.JobStatusRunning {
		jsonError(w, http.StatusConflict, "job is not running")
		return
	}

	// Cancel the goroutine via its per-job context.
	if val, ok := h.activeJobs.Load(jobID); ok {
		val.(context.CancelFunc)()
		log.Info().Str("job_id", jobID.String()).Msg("job stop signal sent")
	} else {
		// Goroutine not found in memory — service may have restarted.
		// Fall back to direct DB status update so job is not stuck as 'running'.
		log.Warn().Str("job_id", jobID.String()).Msg("no active goroutine found, updating DB status directly")
		if err := h.jobRepo.UpdateStatus(r.Context(), jobID, model.JobStatusPaused); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to stop job: "+err.Error())
			return
		}
	}
	jsonOK(w, http.StatusOK, "job stop signal sent", map[string]string{"job_id": jobID.String(), "status": "pausing"})
}

// RollbackJob deletes all migrated data for the job and marks it rolled_back.
//
// @Summary      Rollback migration job
// @Description  Deletes all rows migrated by this job from the target DB and marks the job as rolled_back.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      200     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      409     {object}  model.APIResponse
// @Failure      500     {object}  model.APIResponse
// @Router       /api/jobs/{job_id}/rollback [post]
func (h *Handler) RollbackJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	if err := h.migrator.Rollback(r.Context(), jobID); err != nil {
		if strings.Contains(err.Error(), "cannot rollback") || strings.Contains(err.Error(), "already rolled back") {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "rollback failed: "+err.Error())
		return
	}
	jsonOK(w, http.StatusOK, "job rolled back", map[string]string{"job_id": jobID.String(), "status": "rolled_back"})
}

// ListJobs returns all jobs, optionally filtered by ?status=.
//
// @Summary      List migration jobs
// @Description  Returns all migration jobs. Optionally filter by status query parameter.
// @Tags         jobs
// @Produce      json
// @Param        status  query     string  false  "Filter by status (pending, running, paused, completed, failed, rolled_back)"
// @Success      200     {object}  model.APIResponse
// @Failure      500     {object}  model.APIResponse
// @Router       /api/jobs [get]
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	jobs, err := h.jobRepo.List(r.Context(), status)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list jobs: "+err.Error())
		return
	}
	jsonOK(w, http.StatusOK, "ok", map[string]any{"jobs": jobs, "total": len(jobs)})
}

// GetJob returns full details and live progress for a single job.
//
// @Summary      Get migration job
// @Description  Returns full details of a migration job. If the job is currently running, also includes live progress counters.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      200     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      404     {object}  model.APIResponse
// @Router       /api/jobs/{job_id} [get]
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}
	processed, success, failed := h.tracker.Get(jobID)
	total := h.tracker.GetTotal(jobID)

	type jobResponse struct {
		*model.MigrationJob
		LiveProcessed int64   `json:"live_processed,omitempty"`
		LiveSuccess   int64   `json:"live_success,omitempty"`
		LiveFailed    int64   `json:"live_failed,omitempty"`
		Percentage    float64 `json:"percentage,omitempty"`
	}
	resp := jobResponse{MigrationJob: job}
	if job.Status == model.JobStatusRunning && total > 0 {
		resp.LiveProcessed = processed
		resp.LiveSuccess = success
		resp.LiveFailed = failed
		resp.Percentage = float64(processed) / float64(total) * 100
	}
	jsonOK(w, http.StatusOK, "ok", resp)
}

// ValidateJob runs a post-migration consistency check for the job.
//
// @Summary      Verify migration consistency
// @Description  Compares source row count (by ID range) against target rows accounted by the job counters.
// @Description  Uses 2 COUNT queries — no UUID generation, O(1) in round-trips regardless of row count.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      200     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      404     {object}  model.APIResponse
// @Failure      500     {object}  model.APIResponse
// @Router       /api/jobs/{job_id}/validate [get]
func (h *Handler) ValidateJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}
	if job.FirstProcessedID == 0 && job.LastProcessedID == 0 {
		jsonError(w, http.StatusConflict, "job has not processed any rows yet")
		return
	}
	validator := migration.NewValidator(h.sourcePool, h.targetDB)
	result, err := validator.Verify(r.Context(), jobID, job.SourceTable,
		job.FirstProcessedID, job.LastProcessedID,
		job.Success, job.Skipped)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "verification failed: "+err.Error())
		return
	}
	msg := "data is consistent"
	if !result.IsConsistent {
		msg = "data is inconsistent"
	}
	jsonOK(w, http.StatusOK, msg, result)
}

// GetJobErrors returns the error_log JSONB for a job as a parsed array.
//
// @Summary      Get job errors
// @Description  Returns the full error log for a migration job as a JSON array.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job UUID"
// @Success      200     {object}  model.APIResponse
// @Failure      400     {object}  model.APIResponse
// @Failure      404     {object}  model.APIResponse
// @Router       /api/jobs/{job_id}/errors [get]
func (h *Handler) GetJobErrors(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := h.jobRepo.GetByID(r.Context(), jobID)
	if err != nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}
	var errorLog []any
	if len(job.ErrorLog) > 0 {
		_ = json.Unmarshal(job.ErrorLog, &errorLog)
	}
	jsonOK(w, http.StatusOK, "ok", map[string]any{"job_id": jobID.String(), "errors": errorLog, "total": len(errorLog)})
}

// ListTables returns all supported source->target migration pairs.
//
// @Summary      List supported tables
// @Description  Returns all source→target table pairs that the migrator supports.
// @Tags         metadata
// @Produce      json
// @Success      200  {object}  model.APIResponse
// @Router       /api/tables [get]
func (h *Handler) ListTables(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, http.StatusOK, "ok", map[string]any{"supported": migration.SupportedTables()})
}

// Healthz returns 200 if the service is alive.
//
// @Summary      Health check
// @Description  Returns HTTP 200 with status ok if the service is up.
// @Tags         metadata
// @Produce      json
// @Success      200  {object}  model.APIResponse
// @Router       /healthz [get]
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, http.StatusOK, "ok", map[string]any{"status": "ok", "time": time.Now().UTC()})
}

func parseJobID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.PathValue("job_id")
	id, err := uuid.Parse(raw)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "job_id must be a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func jsonOK(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(model.APIResponse{Success: true, Message: message, Data: data})
}

func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(model.APIResponse{Success: false, Message: message, Data: nil})
}

// DevSeedEMRExtra inserts 1.5M additional patients (IDs 2000001–3500000) into EMR.
// This endpoint is DISABLED in production (APP_ENV=production).
//
// @Summary      Seed extra EMR data (dev only)
// @Description  Inserts 1.5M additional patients into the EMR database to simulate data growth. Disabled in production. Idempotent — safe to call multiple times.
// @Tags         dev
// @Produce      json
// @Success      200  {object}  model.APIResponse
// @Failure      403  {object}  model.APIResponse
// @Failure      500  {object}  model.APIResponse
// @Router       /api/dev/seed-emr-extra [post]
func (h *Handler) DevSeedEMRExtra(w http.ResponseWriter, r *http.Request) {
	if strings.ToLower(h.appEnv) == "production" {
		jsonError(w, http.StatusForbidden, "this endpoint is disabled in production")
		return
	}

	log.Info().Str("app_env", h.appEnv).Msg("dev seed: starting extra EMR seed (1.5M rows)")

	before, after, err := database.SeedEMRExtra(r.Context(), h.sourceDBCfg, h.emrExtraSQL)
	if err != nil {
		log.Error().Err(err).Msg("dev seed: extra EMR seed failed")
		jsonError(w, http.StatusInternalServerError, "seed failed: "+err.Error())
		return
	}

	added := after - before
	jsonOK(w, http.StatusOK, "extra EMR seed complete", map[string]any{
		"before_count": before,
		"after_count":  after,
		"added":        added,
	})
}

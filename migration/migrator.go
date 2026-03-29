package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/baskararestu/dataporter/model"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Migrator orchestrates the full ETL loop for a single migration job.
type Migrator struct {
	sourcePool *pgxpool.Pool
	targetDB   *pgxpool.Pool
	jobRepo    *repository.JobRepository
	loader     *Loader
	validator  *Validator
	tracker    *monitoring.Tracker
}

// NewMigrator wires up all ETL components.
func NewMigrator(
	sourcePool *pgxpool.Pool,
	targetDB *pgxpool.Pool,
	jobRepo *repository.JobRepository,
	tracker *monitoring.Tracker,
) *Migrator {
	loader := NewLoader(targetDB)
	validator := NewValidator(sourcePool, targetDB)
	return &Migrator{
		sourcePool: sourcePool,
		targetDB:   targetDB,
		jobRepo:    jobRepo,
		loader:     loader,
		validator:  validator,
		tracker:    tracker,
	}
}

// Run executes the full migration for the given job.
// It respects ctx cancellation for graceful shutdown — the current batch finishes
// before the loop exits, and the job is set to 'paused'.
func (m *Migrator) Run(ctx context.Context, job *model.MigrationJob) error {
	log.Info().Str("job_id", job.JobID.String()).
		Str("source", job.SourceTable).Str("target", job.TargetTable).
		Msg("migration started")

	if _, err := LookupTable(job.SourceTable); err != nil {
		return err
	}

	// Mark job as running.
	if err := m.jobRepo.UpdateStatus(ctx, job.JobID, model.JobStatusRunning); err != nil {
		return fmt.Errorf("set running: %w", err)
	}

	// Open server-side cursor on source DB (REPEATABLE READ snapshot).
	// CountTotal runs inside the same transaction — count and cursor see identical data.
	// NewExtractor acquires a dedicated conn from the pool; Close() releases it.
	extractor, total, err := NewExtractor(ctx, m.sourcePool, job.LastProcessedID, job.BatchSize)
	if err != nil {
		return err
	}
	defer func() { _ = extractor.Close(ctx) }()

	if err := m.jobRepo.SetTotalRecords(ctx, job.JobID, total); err != nil {
		return fmt.Errorf("set total records: %w", err)
	}
	m.tracker.SetTotal(job.JobID, total)

	log.Info().Str("job_id", job.JobID.String()).Int64("total", total).Msg("source count complete")

	batchNum := 0
	for {
		// Graceful shutdown: finish current batch then exit.
		select {
		case <-ctx.Done():
			// ctx is already cancelled — use a fresh context for the DB update.
			pauseCtx := context.Background()
			_ = m.jobRepo.UpdateStatus(pauseCtx, job.JobID, model.JobStatusPaused)
			log.Info().Str("job_id", job.JobID.String()).
				Int64("last_id", job.LastProcessedID).
				Msg("migration paused (context cancelled)")
			return nil
		default:
		}

		// Extract next batch from cursor.
		srcBatch, err := extractor.FetchBatch(ctx)
		if err != nil {
			return m.failJob(ctx, job.JobID, fmt.Errorf("fetch batch: %w", err))
		}
		if len(srcBatch) == 0 {
			break // cursor exhausted
		}
		batchNum++

		// Transform: EMR → SIMRS, collect per-row errors.
		dstBatch, transformErrs := TransformBatch(srcBatch)

		// Log and persist transform errors (skip rows, do not stop migration).
		for _, te := range transformErrs {
			log.Warn().Int("id_pasien", te.IDPasien).Err(te.Err).Msg("transform error, row skipped")
			_ = m.jobRepo.AppendError(ctx, job.JobID, map[string]any{
				"id_pasien": te.IDPasien,
				"error":     te.Err.Error(),
			})
		}

		// Load: COPY + upsert + checkpoint in one transaction.
		// Pass only the source rows that were successfully transformed.
		transformedSrc := filterTransformed(srcBatch, transformErrs)
		inserted, skipped, err := m.loader.LoadBatch(ctx, job.JobID, m.jobRepo, dstBatch, transformedSrc, job.DryRun)
		if err != nil {
			return m.failJob(ctx, job.JobID, fmt.Errorf("load batch %d: %w", batchNum, err))
		}

		// Update in-memory tracker.
		m.tracker.Add(job.JobID, int64(len(dstBatch)), int64(len(transformErrs)))

		processed, success, failed := m.tracker.Get(job.JobID)
		log.Info().
			Str("job_id", job.JobID.String()).
			Int("batch", batchNum).
			Int64("inserted", inserted).
			Int64("skipped", skipped).
			Int64("processed", processed).
			Int64("success", success).
			Int64("failed", failed).
			Int64("total", total).
			Msg("batch complete")

		// Backpressure: optional delay to protect source DB under production load.
		if job.BatchDelayMs > 0 {
			time.Sleep(time.Duration(job.BatchDelayMs) * time.Millisecond)
		}
	}

	// Mark completed.
	if err := m.jobRepo.UpdateStatus(ctx, job.JobID, model.JobStatusCompleted); err != nil {
		return fmt.Errorf("set completed: %w", err)
	}

	log.Info().Str("job_id", job.JobID.String()).Int("batches", batchNum).Msg("migration completed")
	return nil
}

// Rollback deletes all data migrated by the given job from the target database.
// Uses a single DELETE with generate_series + uuid_generate_v5 in the DB — no
// UUID rebuilding in Go, no round-trip batches.
func (m *Migrator) Rollback(ctx context.Context, jobID uuid.UUID) error {
	job, err := m.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	if job.Status == model.JobStatusRunning || job.Status == model.JobStatusPending {
		return fmt.Errorf("cannot rollback job with status %q — stop it first", job.Status)
	}
	if job.Status == model.JobStatusRolledBack {
		return fmt.Errorf("job already rolled back")
	}
	if job.FirstProcessedID == 0 || job.LastProcessedID == 0 {
		return fmt.Errorf("job has no processed range to rollback")
	}

	log.Info().Str("job_id", jobID.String()).
		Int64("first_id", job.FirstProcessedID).Int64("last_id", job.LastProcessedID).
		Msg("rollback started")

	// Single DELETE using generate_series + uuid_generate_v5 — entire range in one query,
	// no UUID reconstruction in Go, no round-trip batches.
	tag, err := m.targetDB.Exec(ctx, `
		DELETE FROM public.pasien
		WHERE pasien_uuid IN (
			SELECT uuid_generate_v5($1::uuid, id::text)
			FROM generate_series($2::bigint, $3::bigint) AS id
		)`,
		model.UUIDNamespace.String(), job.FirstProcessedID, job.LastProcessedID,
	)
	if err != nil {
		return fmt.Errorf("rollback delete: %w", err)
	}

	if err := m.jobRepo.UpdateStatus(ctx, jobID, model.JobStatusRolledBack); err != nil {
		return fmt.Errorf("set rolled_back: %w", err)
	}

	log.Info().Str("job_id", jobID.String()).Int64("deleted", tag.RowsAffected()).Msg("rollback complete")
	return nil
}

// failJob sets the job status to 'failed' and returns the original error.
func (m *Migrator) failJob(ctx context.Context, jobID uuid.UUID, err error) error {
	// Use a detached context so the status update succeeds even if ctx was cancelled.
	dbCtx := context.Background()
	_ = m.jobRepo.UpdateStatus(dbCtx, jobID, model.JobStatusFailed)
	log.Error().Str("job_id", jobID.String()).Err(err).Msg("migration failed")
	return err
}

// filterTransformed returns only the source rows whose IDPasien did not appear in the error list.
// Uses map[int]struct{} to avoid string allocations per row in the hot path.
func filterTransformed(src []model.EMRPasien, errs []TransformError) []model.EMRPasien {
	if len(errs) == 0 {
		return src
	}
	failed := make(map[int]struct{}, len(errs))
	for _, e := range errs {
		failed[e.IDPasien] = struct{}{}
	}
	out := make([]model.EMRPasien, 0, len(src)-len(errs))
	for _, r := range src {
		if _, skip := failed[r.IDPasien]; !skip {
			out = append(out, r)
		}
	}
	return out
}

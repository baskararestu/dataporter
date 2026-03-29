package migration

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/baskararestu/dataporter/model"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Migrator orchestrates the full ETL loop for a single migration job.
type Migrator struct {
	sourceConn *pgx.Conn
	targetDB   *pgxpool.Pool
	jobRepo    *repository.JobRepository
	loader     *Loader
	validator  *Validator
	tracker    *monitoring.Tracker
}

// NewMigrator wires up all ETL components.
func NewMigrator(
	sourceConn *pgx.Conn,
	targetDB *pgxpool.Pool,
	jobRepo *repository.JobRepository,
	mappingRepo *repository.MappingRepository,
	tracker *monitoring.Tracker,
) *Migrator {
	loader := NewLoader(targetDB, mappingRepo)
	validator := NewValidator(sourceConn, targetDB)
	return &Migrator{
		sourceConn: sourceConn,
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

	total, err := CountTotal(ctx, m.sourceConn, job.LastProcessedID)
	if err != nil {
		return fmt.Errorf("count total: %w", err)
	}
	if err := m.jobRepo.SetTotalRecords(ctx, job.JobID, total); err != nil {
		return fmt.Errorf("set total records: %w", err)
	}
	m.tracker.SetTotal(job.JobID, total)

	log.Info().Str("job_id", job.JobID.String()).Int64("total", total).Msg("source count complete")

	// Open server-side cursor on source DB (REPEATABLE READ snapshot).
	extractor, err := NewExtractor(ctx, m.sourceConn, job.LastProcessedID, job.BatchSize)
	if err != nil {
		return err
	}
	defer func() { _ = extractor.Close(ctx) }()

	batchNum := 0
	for {
		// Graceful shutdown: finish current batch then exit.
		select {
		case <-ctx.Done():
			_ = m.jobRepo.UpdateStatus(ctx, job.JobID, model.JobStatusPaused)
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

		// Load: COPY + upsert + mapping + checkpoint in one transaction.
		// Pass only the source rows that were successfully transformed.
		transformedSrc := filterTransformed(srcBatch, transformErrs)
		if err := m.loader.LoadBatch(ctx, job.JobID, m.jobRepo, dstBatch, transformedSrc, job.DryRun); err != nil {
			return m.failJob(ctx, job.JobID, fmt.Errorf("load batch %d: %w", batchNum, err))
		}

		// Update in-memory tracker.
		m.tracker.Add(job.JobID, int64(len(dstBatch)), int64(len(transformErrs)))

		processed, success, failed := m.tracker.Get(job.JobID)
		log.Info().
			Str("job_id", job.JobID.String()).
			Int("batch", batchNum).
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
// Uses mapping table to find UUIDs, then deletes in batches to avoid long locks.
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

	log.Info().Str("job_id", jobID.String()).Msg("rollback started")

	// Delete migrated pasien rows using UUID ranges derived from the mapping table.
	const batchSQL = `
		DELETE FROM public.pasien
		WHERE pasien_uuid IN (
			SELECT target_uuid FROM migration.emr_simrs_id_map
			WHERE job_id = $1 AND source_table = $2
			LIMIT 5000
		)`

	deleted := int64(0)
	for {
		tag, err := m.targetDB.Exec(ctx, batchSQL, jobID, job.SourceTable)
		if err != nil {
			return fmt.Errorf("rollback delete batch: %w", err)
		}
		n := tag.RowsAffected()
		deleted += n
		if n == 0 {
			break
		}
	}

	// Clean up mapping entries for this job.
	mappingRepo := repository.NewMappingRepository(m.targetDB)
	if _, err := mappingRepo.DeleteByJobID(ctx, jobID); err != nil {
		return fmt.Errorf("delete mapping entries: %w", err)
	}

	if err := m.jobRepo.UpdateStatus(ctx, jobID, model.JobStatusRolledBack); err != nil {
		return fmt.Errorf("set rolled_back: %w", err)
	}

	log.Info().Str("job_id", jobID.String()).Int64("deleted", deleted).Msg("rollback complete")
	return nil
}

// failJob sets the job status to 'failed' and returns the original error.
func (m *Migrator) failJob(ctx context.Context, jobID uuid.UUID, err error) error {
	_ = m.jobRepo.UpdateStatus(ctx, jobID, model.JobStatusFailed)
	log.Error().Str("job_id", jobID.String()).Err(err).Msg("migration failed")
	return err
}

// filterTransformed returns only the source rows whose IDPasien did not appear in the error list.
func filterTransformed(src []model.EMRPasien, errs []TransformError) []model.EMRPasien {
	if len(errs) == 0 {
		return src
	}
	failed := make(map[string]bool, len(errs))
	for _, e := range errs {
		failed[strconv.Itoa(e.IDPasien)] = true
	}
	out := make([]model.EMRPasien, 0, len(src)-len(errs))
	for _, r := range src {
		if !failed[strconv.Itoa(r.IDPasien)] {
			out = append(out, r)
		}
	}
	return out
}

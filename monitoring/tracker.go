package monitoring

import (
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// jobStats holds atomic counters for a single migration job.
type jobStats struct {
	total     atomic.Int64
	processed atomic.Int64
	success   atomic.Int64
	failed    atomic.Int64
}

// Tracker maintains in-memory per-job progress counters using atomic operations.
// It is safe to use from multiple goroutines (HTTP handler reads while ETL loop writes).
type Tracker struct {
	mu   sync.RWMutex
	jobs map[uuid.UUID]*jobStats
}

// NewTracker creates a new empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{jobs: make(map[uuid.UUID]*jobStats)}
}

// SetTotal records the total row count for a job (called once before the batch loop).
func (t *Tracker) SetTotal(jobID uuid.UUID, total int64) {
	t.getOrCreate(jobID).total.Store(total)
}

// Add increments the processed, success, and failed counters for a job.
func (t *Tracker) Add(jobID uuid.UUID, success, failed int64) {
	s := t.getOrCreate(jobID)
	s.processed.Add(success + failed)
	s.success.Add(success)
	s.failed.Add(failed)
}

// Get returns the current counters for a job.
// Returns zeros if the job is not tracked (e.g. completed jobs loaded from DB).
func (t *Tracker) Get(jobID uuid.UUID) (processed, success, failed int64) {
	t.mu.RLock()
	s, ok := t.jobs[jobID]
	t.mu.RUnlock()
	if !ok {
		return 0, 0, 0
	}
	return s.processed.Load(), s.success.Load(), s.failed.Load()
}

// GetTotal returns the total row count set for a job.
func (t *Tracker) GetTotal(jobID uuid.UUID) int64 {
	t.mu.RLock()
	s, ok := t.jobs[jobID]
	t.mu.RUnlock()
	if !ok {
		return 0
	}
	return s.total.Load()
}

// Remove deletes a job's counters from memory (called after job completes/fails).
func (t *Tracker) Remove(jobID uuid.UUID) {
	t.mu.Lock()
	delete(t.jobs, jobID)
	t.mu.Unlock()
}

func (t *Tracker) getOrCreate(jobID uuid.UUID) *jobStats {
	t.mu.RLock()
	s, ok := t.jobs[jobID]
	t.mu.RUnlock()
	if ok {
		return s
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok = t.jobs[jobID]; ok {
		return s
	}
	s = &jobStats{}
	t.jobs[jobID] = s
	return s
}

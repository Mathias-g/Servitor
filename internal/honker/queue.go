package honker

import (
	"context"
	"encoding/json"
	"fmt"

	hg "github.com/russellromney/honker-go"
)

// Queue is a durable work queue (SPEC: Honker). Workers claim, execute, and
// ack jobs; a claim expires after the visibility timeout and is re-issued.
type Queue struct {
	name string
	q    *hg.Queue
}

// Name returns the queue's name, used when registering scheduled tasks.
func (q *Queue) Name() string { return q.name }

// Enqueue adds a job with the given payload. The payload is JSON-marshaled.
func (q *Queue) Enqueue(payload any) (int64, error) {
	return q.q.Enqueue(payload, hg.EnqueueOptions{})
}

// ClaimOne claims up to one available job for worker, or returns (nil, nil)
// when the queue is empty.
func (q *Queue) ClaimOne(worker string) (*Job, error) {
	job, err := q.q.ClaimOne(worker)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}
	return &Job{job: job, ID: job.ID, Payload: job.Payload, WorkerID: job.WorkerID, Attempts: job.Attempts}, nil
}

// Job is a claimed unit of work.
type Job struct {
	job      *hg.Job
	ID       int64
	Payload  []byte
	WorkerID string
	Attempts int64
}

// UnmarshalPayload decodes the job's JSON payload into dst.
func (j *Job) UnmarshalPayload(dst any) error {
	if err := json.Unmarshal(j.Payload, dst); err != nil {
		return fmt.Errorf("decode job payload: %w", err)
	}
	return nil
}

// Ack deletes the claim if it is still valid, returning whether the caller's
// claim had not expired. Used by the worker loop when no atomic fan-out is
// needed.
func (j *Job) Ack() (bool, error) {
	return j.job.Ack()
}

// Retry returns the claim to pending with a delay, or moves it to the
// dead-letter table when attempts have reached the queue's max. It is how a
// failed step is re-issued; Honker counts attempts and dead-letters on
// repeated failure (SPEC: Execution model step 9).
func (j *Job) Retry(delaySec int64, errMsg string) (bool, error) {
	return j.job.Retry(delaySec, errMsg)
}

// Fail moves the claim straight to the dead-letter table.
func (j *Job) Fail(errMsg string) (bool, error) {
	return j.job.Fail(errMsg)
}

// ClaimWaker blocks until a job is claimable, then claims it. The worker loop
// uses it to avoid busy-polling an empty queue; it wakes on Honker updates and
// on a claim's visibility timeout.
func (q *Queue) ClaimWaker() *ClaimWaker {
	return &ClaimWaker{w: q.q.ClaimWaker()}
}

// ClaimWaker blocks until a job is claimable, then claims and returns it. Next
// returns (nil, nil) when the context is cancelled.
type ClaimWaker struct {
	w *hg.ClaimWaker
}

// Next claims the next available job, blocking until one is claimable or the
// context is cancelled.
func (w *ClaimWaker) Next(ctx context.Context, workerID string) (*Job, error) {
	job, err := w.w.Next(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}
	return &Job{job: job, ID: job.ID, Payload: job.Payload, WorkerID: job.WorkerID, Attempts: job.Attempts}, nil
}

// Close unsubscribes from the update watcher.
func (w *ClaimWaker) Close() {
	w.w.Close()
}

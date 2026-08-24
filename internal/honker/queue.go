package honker

import (
	"encoding/json"
	"fmt"

	hg "github.com/russellromney/honker-go"
)

// Queue is a durable work queue (SPEC: Honker). Workers claim, execute, and
// ack jobs; a claim expires after the visibility timeout and is re-issued.
type Queue struct {
	q *hg.Queue
}

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

// Fail moves the claim straight to the dead-letter table.
func (j *Job) Fail(errMsg string) (bool, error) {
	return j.job.Fail(errMsg)
}

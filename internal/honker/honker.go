// Package honker wraps the Honker SQLite extension (via honker-go) as the
// runner's durability layer (SPEC: Honker, durable queue and scheduler). It
// opens a SQLite file with the extension loaded, sets WAL mode, and owns the
// single write connection, honoring SQLite's single-writer rule by design
// (ADR-0004).
//
// The extension is a loadable `.so` that the caller provides; this package
// loads it at open time via cgo (mattn/go-sqlite3). It is not committed to the
// repo (see ADR-0011).
package honker

import (
	"fmt"

	hg "github.com/russellromney/honker-go"
)

// Store is a Honker handle over a SQLite file with the extension loaded and
// the schema bootstrapped. One process should own it.
type Store struct {
	db   *hg.Database
	path string
}

// Open opens (creating if needed) the SQLite file at path and loads the Honker
// extension at extPath. It sets WAL mode and limits the connection pool to a
// single connection so writes are serialized through this process.
func Open(path, extPath string) (*Store, error) {
	if extPath == "" {
		return nil, fmt.Errorf("honker: extension path is empty; set HONKER_EXTENSION_PATH")
	}
	db, err := hg.Open(path, extPath)
	if err != nil {
		return nil, fmt.Errorf("honker: open %s: %w", path, err)
	}
	// Single connection: SQLite's single-writer rule honored by design.
	db.Raw().SetMaxOpenConns(1)

	if _, err := db.Raw().Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("honker: enable WAL on %s: %w", path, err)
	}

	s := &Store{db: db, path: path}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// Path returns the SQLite file path this store owns.
func (s *Store) Path() string { return s.path }

// Close closes the database, releasing the write connection cleanly so the
// next instance can acquire it (SPEC: graceful shutdown).
func (s *Store) Close() error { return s.db.Close() }

// Queue returns a handle to the named queue, with an explicit visibility
// timeout (seconds) and max attempts. Zero values resolve to Honker's
// defaults (300s / 3 attempts).
func (s *Store) Queue(name string, visibilityTimeoutS, maxAttempts int) *Queue {
	return &Queue{
		name: name,
		q: s.db.Queue(name, hg.QueueOptions{
			VisibilityTimeoutS: visibilityTimeoutS,
			MaxAttempts:        maxAttempts,
		}),
	}
}

// Scheduler returns the Honker scheduler facade, used for cron triggers
// (SPEC: Triggers, `cron`). The daemon runs Scheduler.Run; registering a task
// here enqueues a job to its queue on each fire.
func (s *Store) Scheduler() *hg.Scheduler {
	return s.db.Scheduler()
}

// ScheduledTask is a cron registration: when Schedule fires, Honker enqueues
// Payload to Queue. RegisterScheduledTask is idempotent by Name.
type ScheduledTask struct {
	Name     string
	Queue    string
	Schedule string
	Payload  any
}

// RegisterScheduledTask registers a cron task on the Honker scheduler.
func (s *Store) RegisterScheduledTask(t ScheduledTask) error {
	return s.db.Scheduler().Add(hg.ScheduledTask{
		Name:     t.Name,
		Queue:    t.Queue,
		Schedule: t.Schedule,
		Payload:  t.Payload,
	})
}

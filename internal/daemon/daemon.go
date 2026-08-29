// Package daemon implements the long-lived runner daemon and its loopback
// control-plane HTTP server (ADR-0005, ADR-0009).
//
// The daemon owns the runner's single SQLite write connection (via Honker) and
// binds 127.0.0.1 only. It opens the Honker store at startup and releases it
// cleanly on shutdown. The worker/execution loop is built in a later phase;
// this phase owns the store and the transactional primitive.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/protocol"
	"github.com/Mathias-g/Servitor/internal/runner"
	"github.com/Mathias-g/Servitor/internal/secret"
	"github.com/Mathias-g/Servitor/internal/trigger"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// ErrNonLoopback is returned when the daemon is asked to bind a non-loopback
// address. The control plane is loopback-only by design (ADR-0009).
var ErrNonLoopback = errors.New("refusing to bind a non-loopback address (ADR-0009: control plane is loopback-only)")

// Config controls daemon startup.
type Config struct {
	// Addr is the loopback address to listen on. Must be loopback (ADR-0009).
	// Empty means protocol.DefaultAddr.
	Addr string

	// DBPath is the SQLite file the daemon owns (via Honker). If empty, the
	// daemon runs without a store; the runner's durable layers need it set.
	DBPath string

	// ExtPath is the Honker extension .so to load (ADR-0011). Required when
	// DBPath is set.
	ExtPath string

	// DrainTimeout is how long a stop waits for in-flight work before it is
	// hard-stopped.
	DrainTimeout time.Duration

	// QueueName is the queue the runner's worker loop claims nodes from.
	// Empty means "nodes".
	QueueName string
	// VisibilityTimeoutS is how long a worker's claim lasts before it is
	// re-issued to another worker after a crash (SPEC: Execution model step 9).
	// Zero means 30s.
	VisibilityTimeoutS int
	// MaxAttempts is how many times a node is tried before it is dead-lettered.
	// Zero means 3.
	MaxAttempts int
	// SecretRetryCount bounds how many times a node is retried for a
	// secret-specific failure (a stale secret, or an unreachable source) before
	// it fails (SPEC: Secret invalidity and rotation). Zero means 3.
	SecretRetryCount int
	// Resolver resolves a node's declared secrets per node at spawn (SPEC:
	// Secret resolution, ADR-0032, ADR-0033). It replaces the resolved global
	// secret map; the runner no longer holds the full set.
	Resolver *secret.Resolver
	// Workers is how many worker loops to run. When DBPath is set and Workers
	// is zero, one worker runs; set Workers to 0 with DisableRunner to run
	// the daemon without executing nodes.
	Workers int
	// DisableRunner stops the worker loop and scheduler from starting even
	// when a DBPath is set.
	DisableRunner bool

	// WebhookAddr is the address the webhook receiver listens on for inbound
	// events (for example ":8080"). Unlike the control plane it may bind any
	// interface, because webhooks must be reachable by external senders
	// (SPEC: Triggers). Empty disables the webhook listener.
	WebhookAddr string

	// Started, if set, is called once the listener is bound and the server is
	// serving. It lets the caller report readiness only after a real bind, not
	// before a loopback check or listen error.
	Started func(addr string)
}

// Server is the runner's control-plane HTTP server. It is loopback-only.
type Server struct {
	cfg     Config
	httpSrv *http.Server
	// store is the daemon's Honker handle, set by Run when a DBPath is
	// configured. Handlers reach it for durable operations.
	store *honker.Store
	// queue is the worker's node queue, set by Run when a DBPath is
	// configured. Handlers reach it to register cron tasks.
	queue *honker.Queue
	// receiver handles inbound webhooks and manual triggers, set by Run when a
	// store and webhook address are configured.
	receiver *trigger.Receiver
	// resolver resolves secrets per node; submit uses it to reject a Wafer that
	// references an undeclared secret (ADR-0035).
	resolver *secret.Resolver
}

// NewServer builds the control-plane server. It does not listen; call Serve.
func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, resolver: cfg.Resolver}
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.PathHealth, s.handleHealth)
	mux.HandleFunc(protocol.PathStop, s.handleStop)
	mux.HandleFunc(protocol.PathSubmit, s.handleSubmit)
	mux.HandleFunc(protocol.PathUpdate, s.handleUpdate)
	mux.HandleFunc(protocol.PathEnable, s.handleEnable)
	mux.HandleFunc(protocol.PathDisable, s.handleDisable)
	mux.HandleFunc(protocol.PathTrigger, s.handleTrigger)
	mux.HandleFunc(protocol.PathRuns, s.handleRuns)
	mux.HandleFunc(protocol.PathRun, s.handleRun)
	mux.HandleFunc(protocol.PathCancel, s.handleCancel)
	mux.HandleFunc(protocol.PathResume, s.handleResume)
	s.httpSrv = &http.Server{Handler: mux}
	return s
}

// Serve accepts connections on lis until the server is shut down. It returns
// nil on a clean shutdown (a /stop request or a graceful Stop).
func (s *Server) Serve(lis net.Listener) error {
	err := s.httpSrv.Serve(lis)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop triggers a graceful shutdown, waiting up to the drain timeout for
// in-flight requests before force-closing them.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleStop(w http.ResponseWriter, _ *http.Request) {
	// Acknowledge, then drain and shut down in the background. Shutdown waits
	// for in-flight requests (including this one), so it must not run inline in
	// the handler.
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "stopping\n")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.DrainTimeout)
		defer cancel()
		_ = s.httpSrv.Shutdown(ctx)
	}()
}

// handleSubmit registers a workflow from a Wafer in the request body. It
// validates the Wafer first; an invalid Wafer is a 422 with the structured
// validation errors as the body.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	s.handleRegister(w, r, false)
}

// handleUpdate replaces an already-registered workflow. It is like submit but
// errors when the workflow is not yet registered.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	s.handleRegister(w, r, true)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request, requireExisting bool) {
	if s.store == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	res := wafer.Validate(body)
	if !res.Valid() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(res)
		return
	}
	wf, err := wafer.Parse(body)
	if err != nil {
		http.Error(w, "parse wafer: "+err.Error(), http.StatusBadRequest)
		return
	}
	// A secret the Wafer references but that is not declared must refuse to
	// submit or run (ADR-0035): the run could never complete.
	if s.resolver != nil {
		var undeclared []string
		for _, name := range wf.ReferencedSecrets() {
			if !s.resolver.Declared(name) {
				undeclared = append(undeclared, name)
			}
		}
		if len(undeclared) > 0 {
			http.Error(w, "workflow references undeclared secret(s): "+strings.Join(undeclared, ", ")+" (declare them with `servitor secret add`)", http.StatusUnprocessableEntity)
			return
		}
	}
	if requireExisting {
		existing, gerr := s.store.GetWorkflow(wf.Name)
		if gerr != nil {
			http.Error(w, gerr.Error(), http.StatusInternalServerError)
			return
		}
		if existing == nil {
			http.Error(w, "workflow "+wf.Name+" is not registered; use submit to register it", http.StatusNotFound)
			return
		}
		// Unregister the old workflow's cron triggers before replacing it, so a
		// removed or re-indexed cron trigger does not leave a stale schedule.
		if old, perr := wafer.Parse([]byte(existing.Wafer)); perr == nil {
			if err := s.unregisterCron(old); err != nil {
				http.Error(w, "unregister old cron: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if err := s.unregisterEmail(old); err != nil {
				http.Error(w, "unregister old email: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	if err := s.store.RegisterWorkflow(wf.Name, string(body)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.registerCron(wf); err != nil {
		http.Error(w, "register cron: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.registerEmail(wf); err != nil {
		http.Error(w, "register email: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// registerCron registers a scheduled task for every `cron` trigger in the
// Wafer (SPEC: Triggers, `cron`). Honker's scheduler persists tasks in the
// store, so registering (idempotently, by name) here is all that is needed;
// the daemon already runs the scheduler loop. A workflow with no cron trigger
// registers nothing.
func (s *Server) registerCron(w *wafer.Wafer) error {
	if s.store == nil || s.queue == nil {
		return nil
	}
	for i, tr := range w.On {
		if tr.Type != "cron" {
			continue
		}
		schedule, _ := tr.Config["schedule"].(string)
		if schedule == "" {
			return fmt.Errorf("cron trigger %d has no schedule", i)
		}
		err := runner.RegisterCron(s.store, s.queue, w, runner.CronTask{
			Name:     fmt.Sprintf("%s:cron-%d", w.Name, i),
			Schedule: schedule,
			RunID:    fmt.Sprintf("%s-cron-%d", w.Name, i),
			Event:    map[string]any{"trigger": "cron"},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// handleEnable and handleDisable toggle a registered workflow's triggers.
func (s *Server) handleEnable(w http.ResponseWriter, r *http.Request) {
	s.handleSetEnabled(w, r, true)
}

func (s *Server) handleDisable(w http.ResponseWriter, r *http.Request) {
	s.handleSetEnabled(w, r, false)
}

func (s *Server) handleSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if s.store == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	if err := s.store.SetWorkflowEnabled(name, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Enabling arms a workflow's cron triggers; disabling unregisters them.
	if err := s.syncCron(name, enabled); err != nil {
		http.Error(w, "sync cron: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// syncCron registers a workflow's cron triggers when it is enabled, or
// unregisters them when it is disabled.
func (s *Server) syncCron(name string, enabled bool) error {
	if s.store == nil || s.queue == nil {
		return nil
	}
	wf, err := s.store.GetWorkflow(name)
	if err != nil {
		return err
	}
	if wf == nil {
		return nil
	}
	w, perr := wafer.Parse([]byte(wf.Wafer))
	if perr != nil {
		return perr
	}
	if enabled {
		if err := s.registerCron(w); err != nil {
			return err
		}
		return s.registerEmail(w)
	}
	if err := s.unregisterCron(w); err != nil {
		return err
	}
	return s.unregisterEmail(w)
}

// unregisterCron removes the scheduled task for every `cron` trigger in the
// Wafer (the inverse of registerCron).
func (s *Server) unregisterCron(w *wafer.Wafer) error {
	for i, tr := range w.On {
		if tr.Type != "cron" {
			continue
		}
		if _, err := s.store.UnregisterScheduledTask(fmt.Sprintf("%s:cron-%d", w.Name, i)); err != nil {
			return fmt.Errorf("cron: unregister %s:cron-%d: %w", w.Name, i, err)
		}
	}
	return nil
}

// registerEmail registers a recurring poll for every `email_received` trigger
// in the Wafer (SPEC: Triggers, `email_received`). The poll runs on the
// trigger's `poll` cron schedule (default every 5 minutes), and each poll fans
// out one run per new email. A workflow with no `email_received` trigger
// registers nothing.
func (s *Server) registerEmail(w *wafer.Wafer) error {
	if s.store == nil || s.queue == nil {
		return nil
	}
	for i, tr := range w.On {
		if tr.Type != "email_received" {
			continue
		}
		schedule, _ := tr.Config["poll"].(string)
		if schedule == "" {
			schedule = "*/5 * * * *"
		}
		host, _ := tr.Config["host"].(string)
		username, _ := tr.Config["username"].(string)
		secret, _ := tr.Config["secret"].(string)
		if host == "" || username == "" || secret == "" {
			return fmt.Errorf("email_received trigger %d requires host, username, and secret", i)
		}
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("email_received: locate servitor binary: %w", err)
		}
		err = runner.RegisterPoll(s.store, s.queue, runner.PollTask{
			Name:       fmt.Sprintf("%s:email-%d", w.Name, i),
			Schedule:   schedule,
			WorkflowID: w.Name,
			Kind:       "email",
			Command:    []string{exe, "__email_poll"},
			Secrets:    []string{secret},
			Config:     tr.Config,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// unregisterEmail removes the scheduled poll for every `email_received` trigger
// in the Wafer (the inverse of registerEmail).
func (s *Server) unregisterEmail(w *wafer.Wafer) error {
	for i, tr := range w.On {
		if tr.Type != "email_received" {
			continue
		}
		if _, err := s.store.UnregisterScheduledTask(fmt.Sprintf("%s:email-%d", w.Name, i)); err != nil {
			return fmt.Errorf("email: unregister %s:email-%d: %w", w.Name, i, err)
		}
	}
	return nil
}

// handleTrigger fires a manual run of a workflow with the request body as the
// run's JSON inputs.
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if s.receiver == nil {
		http.Error(w, "no trigger receiver; run the daemon with --db", http.StatusConflict)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var inputs map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &inputs); err != nil {
			http.Error(w, "inputs must be JSON", http.StatusBadRequest)
			return
		}
	}
	if err := s.receiver.Manual(r.Context(), name, inputs); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// handleRuns returns the run history as JSON.
func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	if s.store == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	runs, err := s.store.ListRuns()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, runs)
}

// handleRun returns one run and its node outcomes as JSON.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	run, err := s.store.GetRun(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if run == nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	nodes, err := s.store.RunNodes(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"run": run, "nodes": nodes})
}

// handleCancel cancels an in-flight run.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	run, err := s.store.GetRun(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if run == nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err := s.store.CancelRun(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleResume resumes a parked run by named signal (ADR-0042). The signal name
// is a query param; the optional JSON payload is the request body. An ambiguous
// signal (more than one run parked on the name) is rejected, so the author
// sees their name is not unique.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.queue == nil {
		http.Error(w, "no store; run the daemon with --db", http.StatusConflict)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var payload any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "payload must be JSON", http.StatusBadRequest)
			return
		}
	}
	if err := worker.ResumeBySignal(s.store, s.queue, name, payload); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// Run starts the daemon on cfg.Addr and blocks until it is shut down, either by
// a /stop request, SIGTERM/SIGINT, or ctx cancellation. It returns nil on a
// clean shutdown.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		cfg.Addr = protocol.DefaultAddr
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = 30 * time.Second
	}
	if cfg.QueueName == "" {
		cfg.QueueName = "nodes"
	}
	if cfg.VisibilityTimeoutS <= 0 {
		cfg.VisibilityTimeoutS = 30
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}

	if err := checkLoopback(cfg.Addr); err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("daemon: listen on %s: %w", cfg.Addr, err)
	}

	srv := NewServer(cfg)

	// Open the durable store. The daemon owns this SQLite file and its single
	// write connection; it is released cleanly on shutdown.
	if cfg.DBPath != "" {
		store, err := honker.Open(cfg.DBPath, cfg.ExtPath)
		if err != nil {
			_ = lis.Close()
			return err
		}
		defer func() { _ = store.Close() }()
		srv.store = store

		// The trigger receiver drives inbound webhooks and manual triggers
		// against the same store.
		queue := store.Queue(cfg.QueueName, cfg.VisibilityTimeoutS, cfg.MaxAttempts)
		srv.queue = queue
		srv.receiver = trigger.NewReceiver(store, queue, cfg.Resolver)
	}

	if cfg.Started != nil {
		cfg.Started(lis.Addr().String())
	}

	// Start the runner's worker loop(s) and the cron scheduler when the daemon
	// owns a store and execution is enabled. They stop when the daemon shuts
	// down; in-flight nodes drain as claims expire (SPEC: Graceful shutdown).
	var cancelRunner context.CancelFunc
	if srv.store != nil && !cfg.DisableRunner {
		var rctx context.Context
		rctx, cancelRunner = context.WithCancel(context.Background())
		queue := srv.store.Queue(cfg.QueueName, cfg.VisibilityTimeoutS, cfg.MaxAttempts)
		for i := 0; i < cfg.Workers; i++ {
			w := worker.New(srv.store, queue, fmt.Sprintf("worker-%d", i), worker.Config{
				Resolver:         cfg.Resolver,
				SecretRetryCount: cfg.SecretRetryCount,
				// When a run completes, fire any workflow with a `completed`
				// trigger naming the completed workflow (SPEC: `completed` trigger).
				OnRunComplete: func(workflowID, runID string) {
					if srv.receiver != nil {
						_ = srv.receiver.Completed(workflowID, runID)
					}
				},
				// When a run fails, fire any workflow with a `failed` trigger
				// naming the failed workflow (SPEC: `failed` trigger, ADR-0039),
				// so the operator can wire a notification to a failed secret.
				OnRunFailed: func(workflowID, runID string) {
					if srv.receiver != nil {
						_ = srv.receiver.Failed(workflowID, runID)
					}
				},
				// When a `poll` returns new items, start a run per item
				// (ADR-0027). The receiver dispatches on the poll kind.
				OnPoll: func(workflowID, kind string, items []any) {
					if srv.receiver != nil {
						_ = srv.receiver.Polled(workflowID, kind, items)
					}
				},
			})
			go func() { _ = w.Run(rctx) }()
		}
		go func() { _ = srv.store.Scheduler().Run(rctx, "scheduler") }()
	}
	defer func() {
		if cancelRunner != nil {
			cancelRunner()
		}
	}()

	// Start the webhook listener when configured. It is separate from the
	// loopback control plane and may bind any interface, because webhooks must
	// be reachable by external senders.
	var webhookSrv *http.Server
	var webhookLis net.Listener
	if cfg.WebhookAddr != "" && srv.receiver != nil {
		webhookLis, err = net.Listen("tcp", cfg.WebhookAddr)
		if err != nil {
			_ = lis.Close()
			return fmt.Errorf("daemon: listen for webhooks on %s: %w", cfg.WebhookAddr, err)
		}
		webhookSrv = &http.Server{Handler: srv.receiver}
		go func() { _ = webhookSrv.Serve(webhookLis) }()
	}
	defer func() {
		if webhookSrv != nil {
			_ = webhookSrv.Close()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return srv.Stop(context.Background())
	case <-sigCh:
		return srv.Stop(context.Background())
	}
}

// checkLoopback rejects any bind address that is not a loopback address, so the
// control plane can never escape the box it runs on (ADR-0009).
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNonLoopback, addr)
}

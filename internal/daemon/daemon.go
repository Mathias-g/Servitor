// Package daemon implements the long-lived runner daemon and its loopback
// control-plane HTTP server (ADR-0005, ADR-0009).
//
// The daemon owns the runner's single SQLite write connection (Phase 5, with
// Honker) and binds 127.0.0.1 only. This phase (Phase 1) builds the daemon
// lifecycle and the control protocol: liveness and graceful shutdown. There is
// no workflow state yet.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mathias-g/Servitor/internal/protocol"
)

// ErrNonLoopback is returned when the daemon is asked to bind a non-loopback
// address. The control plane is loopback-only by design (ADR-0009).
var ErrNonLoopback = errors.New("refusing to bind a non-loopback address (ADR-0009: control plane is loopback-only)")

// Config controls daemon startup.
type Config struct {
	// Addr is the loopback address to listen on. Must be loopback (ADR-0009).
	// Empty means protocol.DefaultAddr.
	Addr string

	// DrainTimeout is how long a stop waits for in-flight work before it is
	// hard-stopped. There is no in-flight work in this phase; the field exists
	// so the graceful-shutdown path is already shaped for later phases.
	DrainTimeout time.Duration

	// Started, if set, is called once the listener is bound and the server is
	// serving. It lets the caller report readiness only after a real bind, not
	// before a loopback check or listen error.
	Started func(addr string)
}

// Server is the runner's control-plane HTTP server. It is loopback-only.
type Server struct {
	cfg     Config
	httpSrv *http.Server
}

// NewServer builds the control-plane server. It does not listen; call Serve.
func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.PathHealth, s.handleHealth)
	mux.HandleFunc(protocol.PathStop, s.handleStop)
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

	if err := checkLoopback(cfg.Addr); err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("daemon: listen on %s: %w", cfg.Addr, err)
	}

	srv := NewServer(cfg)

	if cfg.Started != nil {
		cfg.Started(lis.Addr().String())
	}

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

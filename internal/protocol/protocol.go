// Package protocol defines the loopback control protocol between the CLI and
// the runner daemon (ADR-0005). It is kept independent of argument parsing so a
// future MCP adapter can speak the same protocol without a rewrite.
//
// The daemon binds 127.0.0.1 only (ADR-0009); nothing here listens on a
// non-loopback interface.
package protocol

const (
	// DefaultAddr is where the daemon listens unless overridden with --addr.
	// Loopback only, per ADR-0009.
	DefaultAddr = "127.0.0.1:7365"

	// PathHealth is the daemon liveness probe. A running daemon answers 200.
	PathHealth = "/v1/health"

	// PathStop asks the daemon to drain and shut down gracefully.
	PathStop = "/v1/stop"
)

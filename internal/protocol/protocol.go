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

	// PathSubmit registers a workflow from a Wafer (body = Wafer YAML).
	PathSubmit = "/v1/submit"

	// PathUpdate replaces an already-registered workflow (body = Wafer YAML).
	PathUpdate = "/v1/update"

	// PathEnable and PathDisable toggle a workflow's triggers (query: name).
	PathEnable  = "/v1/enable"
	PathDisable = "/v1/disable"

	// PathTrigger fires a manual run of a workflow (query: name; body = JSON
	// inputs).
	PathTrigger = "/v1/trigger"

	// PathRuns lists run history (GET). PathRun inspects one run (query: id).
	PathRuns = "/v1/runs"
	PathRun  = "/v1/run"

	// PathCancel cancels an in-flight run (query: id).
	PathCancel = "/v1/cancel"

	// PathResume resumes a parked run by named signal (query: name; body =
	// optional JSON payload).
	PathResume = "/v1/resume"
)

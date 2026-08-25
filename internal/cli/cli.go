package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Mathias-g/Servitor/internal/capabilities"
	"github.com/Mathias-g/Servitor/internal/daemon"
	"github.com/Mathias-g/Servitor/internal/expression"
	"github.com/Mathias-g/Servitor/internal/protocol"
	"github.com/Mathias-g/Servitor/internal/varlock"
	"github.com/Mathias-g/Servitor/internal/wafer"
)

// Exit codes are the CLI's signal to scripts and to the pipeline (SPEC:
// Control plane). 0 ok, 1 operation failed, 2 usage error, 3 daemon not running.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	exitNoDaemon = 3
)

// Run dispatches the servitor CLI. args excludes the program name. It returns
// the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return exitOK
	}

	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, appVersion())
		return exitOK
	case "help", "--help", "-h":
		printUsage(stdout)
		return exitOK
	case "run":
		return cmdRunDispatch(args[1:], stdout, stderr)
	case "stop":
		return cmdStop(args[1:], stdout, stderr)
	case "dry-run":
		return cmdDryRun(args[1:], stdout, stderr)
	case "capabilities":
		return cmdCapabilities(args[1:], stdout, stderr)
	case "submit":
		return cmdRegister(args[1:], false, stdout, stderr)
	case "update":
		return cmdRegister(args[1:], true, stdout, stderr)
	case "enable":
		return cmdEnable(args[1:], stdout, stderr)
	case "disable":
		return cmdDisable(args[1:], stdout, stderr)
	case "trigger":
		return cmdTrigger(args[1:], stdout, stderr)
	case "runs":
		return cmdRuns(args[1:], stdout, stderr)
	case "cancel":
		return cmdCancel(args[1:], stdout, stderr)
	case "mcp":
		return cmdMCP(args[1:], stdout, stderr)
	case "tap":
		return cmdTap(args[1:], stdout, stderr)
	case "target":
		return cmdTarget(args[1:], stdout, stderr)
	case "__transform":
		// Hidden subprocess entrypoint: the worker runs a `transform` step as
		// a subprocess of the servitor binary itself (ADR-0008), so every step,
		// including pure-computation steps, runs outside the runner's process.
		return cmdTransformStep(args[1:], stdout, stderr)
	case "__switch":
		// Hidden subprocess entrypoint: the worker runs a `switch` step as a
		// subprocess (ADR-0008) that evaluates the routing expression and
		// returns the chosen branch's target step name (ADR-0022).
		return cmdSwitchStep(args[1:], stdout, stderr)
	}

	// The remaining commands are daemon operations scheduled for later phases.
	_, _ = fmt.Fprintf(stderr, "servitor: command %q is not implemented yet (this phase builds run, stop, dry-run, and capabilities)\n", args[0])
	return exitFailure
}

// parseAddr parses the shared --addr flag and returns the remaining positional
// arguments.
func parseAddr(args []string, name string, stderr io.Writer) (addr string, rest []string, code int) {
	addr = protocol.DefaultAddr
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&addr, "addr", protocol.DefaultAddr, "loopback address of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", nil, exitOK
		}
		return "", nil, exitUsage
	}
	return addr, fs.Args(), exitOK
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	addr := protocol.DefaultAddr
	dbPath := ""
	webhookAddr := ""
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&addr, "addr", protocol.DefaultAddr, "loopback address of the daemon")
	fs.StringVar(&dbPath, "db", "", "SQLite file the daemon owns (via Honker)")
	fs.StringVar(&webhookAddr, "webhook-addr", "", "address for the inbound webhook receiver (empty disables)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: run: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	// Self-healing launch: if this process was not started under varlock,
	// re-execute under `varlock run` so the runner always boots with secrets
	// resolved (SPEC: Varlock). SelfHeal blocks until the child exits and
	// returns true once it has taken over.
	if healed, err := varlock.SelfHeal(); healed {
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
			return exitFailure
		}
		return exitOK
	}
	if !varlock.Under() && !varlock.Available() {
		_, _ = fmt.Fprintf(stderr, "servitor: warning: varlock not found on PATH; running without secret resolution (steps that declare secrets will fail)\n")
	}

	cfg := daemon.Config{
		Addr:        addr,
		DBPath:      dbPath,
		ExtPath:     os.Getenv("HONKER_EXTENSION_PATH"),
		WebhookAddr: webhookAddr,
		// Resolved secrets (from varlock, when present) are exposed to the
		// daemon; per-step filtering decides what a subprocess may see.
		Secrets: varlock.ResolvedSecrets(),
		Started: func(a string) {
			_, _ = fmt.Fprintf(stdout, "servitor: daemon listening on %s (loopback only, ADR-0009)\n", a)
		},
	}
	if err := daemon.Run(context.Background(), cfg); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintln(stdout, "servitor: daemon stopped")
	return exitOK
}

func cmdStop(args []string, stdout, stderr io.Writer) int {
	addr, rest, code := parseAddr(args, "stop", stderr)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: stop: unexpected argument %q\n", rest[0])
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := protocol.NewClient(addr)

	// Confirm a daemon is up first, so "daemon not running" is its own exit code.
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}

	if err := c.Stop(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: stop requested; daemon draining and shutting down\n")
	return exitOK
}

// cmdDryRun validates a Wafer and resolves its dependency DAG without
// executing, contacting, or persisting anything (SPEC: dry-run). By default it
// prints a readable plan (steps in run order with their dependencies); --json
// prints the structured result instead. It exits non-zero if there are blocking
// errors.
func cmdDryRun(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	fs := flag.NewFlagSet("dry-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&jsonOut, "json", false, "print the structured result as JSON instead of a readable plan")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor dry-run [--json] <wafer>\n")
		return exitUsage
	}
	path := fs.Arg(0)

	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
		return exitFailure
	}

	res := wafer.DryRun(data)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
			return exitFailure
		}
	} else {
		renderDryRunPlan(stdout, res)
	}

	if !res.Result.Valid() {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %d error(s)\n", len(res.Result.Errors))
		return exitFailure
	}
	_, _ = fmt.Fprintf(stderr, "servitor: dry-run: valid\n")
	return exitOK
}

// cmdRegister validates and registers a workflow on the daemon (SPEC: CLI,
// submit / update). update (requireExisting) replaces an already-registered
// workflow. It reads the Wafer, validates locally first so the common cases
// fail fast without a daemon, then sends it to the daemon.
func cmdRegister(args []string, requireExisting bool, stdout, stderr io.Writer) int {
	name := "submit"
	if requireExisting {
		name = "update"
	}
	addr, rest, code := parseAddr(args, name, stderr)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor %s <wafer>\n", name)
		return exitUsage
	}
	path := rest[0]

	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: %v\n", name, err)
		return exitFailure
	}
	res := wafer.Validate(data)
	if !res.Valid() {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: %d error(s)\n", name, len(res.Errors))
		return exitFailure
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	var msg string
	if requireExisting {
		msg, err = c.Update(ctx, data)
	} else {
		msg, err = c.Submit(ctx, data)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: %v\n", name, err)
		return exitFailure
	}
	if msg != "" {
		_, _ = fmt.Fprint(stdout, msg)
	}
	verb := "submitted"
	if requireExisting {
		verb = "updated"
	}
	_, _ = fmt.Fprintf(stdout, "servitor: %s %s\n", verb, path)
	return exitOK
}

// cmdEnable and cmdDisable toggle a registered workflow's triggers.
func cmdEnable(args []string, stdout, stderr io.Writer) int {
	return cmdSetEnabled(args, true, stdout, stderr)
}

func cmdDisable(args []string, stdout, stderr io.Writer) int {
	return cmdSetEnabled(args, false, stdout, stderr)
}

func cmdSetEnabled(args []string, enabled bool, stdout, stderr io.Writer) int {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	addr, rest, code := parseAddr(args, verb, stderr)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor %s <name>\n", verb)
		return exitUsage
	}
	name := rest[0]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	var err error
	if enabled {
		err = c.Enable(ctx, name)
	} else {
		err = c.Disable(ctx, name)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: %v\n", verb, err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: %sd %s\n", verb[:len(verb)-1], name)
	return exitOK
}

// cmdTrigger fires a manual run of a workflow with optional JSON inputs (SPEC:
// CLI, trigger).
func cmdTrigger(args []string, stdout, stderr io.Writer) int {
	addr, rest, code := parseAddr(args, "trigger", stderr)
	if code != exitOK {
		return code
	}
	if len(rest) == 0 || rest[0] == "" {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor trigger <name> [json-inputs]\n")
		return exitUsage
	}
	name := rest[0]
	var inputs []byte
	if len(rest) > 1 {
		inputs = []byte(rest[1])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	if err := c.Trigger(ctx, name, inputs); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: trigger: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: triggered %s\n", name)
	return exitOK
}

// cmdRunDispatch disambiguates `servitor run`: it parses with the boot flag
// set so flag values (like `--db PATH`) are consumed, then boots the daemon
// when no positional run id remains, or inspects that run when one does (SPEC:
// CLI, `run <id>`).
func cmdRunDispatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("addr", protocol.DefaultAddr, "")
	fs.String("db", "", "")
	fs.String("webhook-addr", "", "")
	if err := fs.Parse(args); err != nil {
		// A flag parsing error here is not diagnostic; fall through to the real
		// command which reports it.
		return cmdRun(args, stdout, stderr)
	}
	if fs.NArg() > 0 {
		return cmdRunDetail(args, stdout, stderr)
	}
	return cmdRun(args, stdout, stderr)
}

// cmdRuns lists run history.
func cmdRuns(args []string, stdout, stderr io.Writer) int {
	addr, rest, code := parseAddr(args, "runs", stderr)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: runs: unexpected argument %q\n", rest[0])
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	body, err := c.ListRuns(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: runs: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintln(stdout, body)
	return exitOK
}

// cmdRunDetail inspects one run and its step outcomes.
func cmdRunDetail(args []string, stdout, stderr io.Writer) int {
	addr, rest, code := parseAddr(args, "run", stderr)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor run <id>\n")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	body, err := c.GetRun(ctx, rest[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: run: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintln(stdout, body)
	return exitOK
}

// cmdCancel stops an in-flight run.
func cmdCancel(args []string, stdout, stderr io.Writer) int {
	addr, rest, code := parseAddr(args, "cancel", stderr)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor cancel <id>\n")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := protocol.NewClient(addr)
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}
	if err := c.Cancel(ctx, rest[0]); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: cancel: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: cancelled %s\n", rest[0])
	return exitOK
}

// cmdTransformStep is the hidden subprocess entrypoint for a `transform` step
// (ADR-0020, ADR-0021). It reads the step's `{event, steps}` input from stdin
// as JSON, evaluates the JSONata expression given as its single argument, and
// writes the result as JSON to stdout. The worker spawns this as a subprocess,
// so a transform never runs inside the runner's process (ADR-0008).
func cmdTransformStep(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: __transform: usage: __transform <expression>\n")
		return exitUsage
	}
	expr := args[0]

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __transform: read input: %v\n", err)
		return exitFailure
	}
	var input any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &input); err != nil {
			_, _ = fmt.Fprintf(stderr, "servitor: __transform: input is not valid JSON: %v\n", err)
			return exitFailure
		}
	}

	out, err := expression.Eval(expr, input)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __transform: %v\n", err)
		return exitFailure
	}
	enc := json.NewEncoder(stdout)
	if err := enc.Encode(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __transform: encode result: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// cmdSwitchStep is the hidden subprocess entrypoint for a `switch` step
// (ADR-0020, ADR-0022). It reads a JSON object on stdin: `input` (the step's
// `{event, steps}` input), `cases` (map of value to target step name), and
// `default` (optional fallback step name). It evaluates the JSONata expression
// given as its single argument, matches the value against `cases`, and writes
// the chosen target step name as JSON to stdout. The worker runs this as a
// subprocess (ADR-0008) and uses the returned target to do the fan-out.
func cmdSwitchStep(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: usage: __switch <expression>\n")
		return exitUsage
	}
	expr := args[0]

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: read input: %v\n", err)
		return exitFailure
	}
	var payload struct {
		Input   any            `json:"input"`
		Cases   map[string]any `json:"cases"`
		Default string         `json:"default"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: input is not valid JSON: %v\n", err)
		return exitFailure
	}

	value, err := expression.Eval(expr, payload.Input)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: %v\n", err)
		return exitFailure
	}
	key := stringify(value)
	target, ok := payload.Cases[key]
	if !ok {
		target = payload.Default
	}
	if target == "" {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: no case matches %q and no default\n", key)
		return exitFailure
	}
	enc := json.NewEncoder(stdout)
	if err := enc.Encode(target); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: __switch: encode result: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// stringify renders a JSONata result as the case-key string to match against
// the `cases` map. A string result is used as-is; anything else is its JSON
// representation, so numeric or boolean keys match consistently.
func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// renderDryRunPlan prints a readable plan: the workflow name and triggers, then
// the steps in run order with their dependencies.
func renderDryRunPlan(w io.Writer, res wafer.DryRunResult) {
	if !res.Result.Valid() {
		_, _ = fmt.Fprintf(w, "workflow: (invalid)\n")
		return
	}
	if res.Name != "" {
		_, _ = fmt.Fprintf(w, "workflow: %s\n", res.Name)
	}
	if len(res.Triggers) > 0 {
		_, _ = fmt.Fprintf(w, "triggers:\n")
		for _, t := range res.Triggers {
			_, _ = fmt.Fprintf(w, "  - %s\n", t.Type)
		}
	}
	if res.DAG == nil {
		return
	}
	if len(res.Secrets) > 0 {
		redacted := make([]string, len(res.Secrets))
		for i, s := range res.Secrets {
			redacted[i] = "<redacted:" + s + ">"
		}
		_, _ = fmt.Fprintf(w, "secrets: %s\n", strings.Join(redacted, ", "))
	}
	_, _ = fmt.Fprintf(w, "\nplan (%d step(s), in run order):\n", len(res.DAG.Steps))
	for i, s := range res.DAG.Steps {
		deps := "start"
		if len(s.DependsOn) > 0 {
			deps = "after " + strings.Join(s.DependsOn, ", ")
		}
		_, _ = fmt.Fprintf(w, "  %d. %s\t%s\t[%s]\n", i+1, s.Name, s.Type, deps)
	}
}

// cmdCapabilities writes the per-server capability set to a directory the agent
// reads on demand (SPEC: How an agent discovers integrations). A pipeline can
// commit the directory so remote agents read it from the repo (ADR-0009).
func cmdCapabilities(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor capabilities [dir]\n")
		return exitUsage
	}
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}

	if err := capabilities.Write(dir); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	if dir == "" {
		dir = capabilities.DefaultDir
	}
	_, _ = fmt.Fprintf(stdout, "servitor: wrote capabilities to %s\n", dir)
	return exitOK
}

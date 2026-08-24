package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Mathias-g/Servitor/internal/capabilities"
	"github.com/Mathias-g/Servitor/internal/daemon"
	"github.com/Mathias-g/Servitor/internal/protocol"
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
		return cmdRun(args[1:], stdout, stderr)
	case "stop":
		return cmdStop(args[1:], stdout, stderr)
	case "dry-run":
		return cmdDryRun(args[1:], stdout, stderr)
	case "capabilities":
		return cmdCapabilities(args[1:], stdout, stderr)
	}

	// The remaining commands are daemon operations scheduled for later phases.
	_, _ = fmt.Fprintf(stderr, "servitor: command %q is not implemented yet (this phase builds run, stop, dry-run, and capabilities)\n", args[0])
	return exitFailure
}

// parseAddr parses the shared --addr flag shared by run and stop.
func parseAddr(args []string, name string, stderr io.Writer) (string, int) {
	addr := protocol.DefaultAddr
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&addr, "addr", protocol.DefaultAddr, "loopback address of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", exitOK
		}
		return "", exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: unexpected argument %q\n", name, fs.Arg(0))
		return "", exitUsage
	}
	return addr, exitOK
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	addr, code := parseAddr(args, "run", stderr)
	if code != exitOK {
		return code
	}

	cfg := daemon.Config{
		Addr: addr,
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
	addr, code := parseAddr(args, "stop", stderr)
	if code != exitOK {
		return code
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
// executing, contacting, or persisting anything (SPEC: dry-run). It prints the
// structured result as JSON and exits non-zero if there are blocking errors.
func cmdDryRun(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor dry-run <wafer>\n")
		return exitUsage
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
		return exitFailure
	}

	res := wafer.DryRun(data)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
		return exitFailure
	}

	if !res.Result.Valid() {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %d error(s)\n", len(res.Result.Errors))
		return exitFailure
	}
	_, _ = fmt.Fprintf(stderr, "servitor: dry-run: valid\n")
	return exitOK
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

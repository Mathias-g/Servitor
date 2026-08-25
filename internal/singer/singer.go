// Package singer implements the record-stream integration layer (SPEC: Singer,
// data movement integrations). A tap is a CLI that emits records as newline
// delimited JSON on stdout; a target is a CLI that consumes records on stdin.
// Both run as subprocesses with a filtered secret env (ADR-0008); nothing runs
// in the runner's process.
//
// Invocation contract (ADR-0016). Singer taps and targets, in the ecosystem
// (singer-python, Meltano SDK), take their config as a file path passed on the
// command line: `tap --config <file>`, plus `--state <file>` and `--catalog
// <file>` when a bookmark or stream selection applies. The executor therefore
// writes config (and state/catalog, when present) to temp files and passes the
// flags, rather than feeding an invocation on stdin. The tap writes its output
// to stdout as Singer protocol messages (SCHEMA, RECORD, STATE), one per line,
// and exits; the executor collects the RECORDs and the last STATE value as the
// next bookmark. `--state` is input only: the next bookmark comes back on
// stdout, matching how singer orchestrators round-trip state. A target reads
// the records to consume on stdin as newline delimited RECORD messages and
// receives its config the same way, via `--config <file>`.
package singer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// TapRequest is one tap subprocess run.
type TapRequest struct {
	// Command is the tap's argv, for example ["tap-stripe"]. It must be
	// non-empty.
	Command []string
	// Env is the filtered environment for the tap (only declared secrets plus
	// PATH), built with exec.FilteredEnv.
	Env []string
	// Config is the step's `config` object.
	Config map[string]any
	// Catalog is the selected-stream catalog authored into the Wafer from
	// capabilities. When nil, the tap syncs all its streams and no `--catalog`
	// flag is passed. The tap's catalog is discovered once at a capabilities
	// refresh, not at execution time (SPEC: Capability discovery).
	Catalog any
	// State is the prior bookmark from the last invocation, or nil on the
	// first run.
	State any
}

// Record is one emitted record.
type Record struct {
	// Stream is the stream the record belongs to.
	Stream string `json:"stream"`
	// Record is the record's payload.
	Record any `json:"record"`
}

// TapResult is the outcome of a tap run.
type TapResult struct {
	// Records are the RECORD messages emitted by the tap.
	Records []Record `json:"records"`
	// Streams are the streams the tap emitted, in first-seen order.
	Streams []string `json:"streams"`
	// State is the last STATE value the tap emitted (the next bookmark), or
	// nil if the tap emitted none.
	State any `json:"state,omitempty"`
}

// TargetRequest is one target subprocess run.
type TargetRequest struct {
	// Command is the target's argv, for example ["target-grist"].
	Command []string
	// Env is the filtered environment for the target.
	Env []string
	// Config is the target's `config` object.
	Config map[string]any
	// Records are the records to consume.
	Records []Record
}

// TargetResult is the outcome of a target run.
type TargetResult struct {
	// Consumed is the number of records fed to the target.
	Consumed int `json:"consumed"`
	// Output is the target's stdout, for diagnostics.
	Output string `json:"output,omitempty"`
}

// message is one line of the Singer protocol on stdout.
type message struct {
	Type   string          `json:"type"`
	Stream string          `json:"stream,omitempty"`
	Record json.RawMessage `json:"record,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

// RunTap runs a tap as a subprocess: it writes the config (and prior state and
// selected-stream catalog, when present) to temp files, passes them as
// `--config` / `--state` / `--catalog`, reads the newline delimited Singer
// messages on stdout, and returns the records and the final state (bookmark).
// It returns an error if the tap cannot start, exits non-zero, or writes
// nothing parseable. The temp files are created with 0600 because the config
// may carry secrets.
func RunTap(ctx context.Context, req TapRequest) (TapResult, error) {
	var out TapResult
	if len(req.Command) == 0 {
		return out, fmt.Errorf("singer: tap: empty command")
	}

	tmp, err := os.MkdirTemp("", "servitor-tap-")
	if err != nil {
		return out, fmt.Errorf("singer: tap: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	name := req.Command[0]
	args := append([]string{}, req.Command[1:]...)

	cfgPath, err := writeJSONFile(tmp, "config.json", req.Config)
	if err != nil {
		return out, fmt.Errorf("singer: tap: config: %w", err)
	}
	args = append(args, "--config", cfgPath)

	if req.State != nil {
		stPath, err := writeJSONFile(tmp, "state.json", req.State)
		if err != nil {
			return out, fmt.Errorf("singer: tap: state: %w", err)
		}
		args = append(args, "--state", stPath)
	}

	if req.Catalog != nil {
		catPath, err := writeJSONFile(tmp, "catalog.json", req.Catalog)
		if err != nil {
			return out, fmt.Errorf("singer: tap: catalog: %w", err)
		}
		args = append(args, "--catalog", catPath)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = req.Env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, fmt.Errorf("singer: tap: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return out, fmt.Errorf("singer: tap: start %s: %w", name, err)
	}

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if uerr := json.Unmarshal(line, &m); uerr != nil {
			continue
		}
		switch m.Type {
		case "RECORD":
			var rec any
			_ = json.Unmarshal(m.Record, &rec)
			out.Records = append(out.Records, Record{Stream: m.Stream, Record: rec})
		case "STATE":
			var v any
			if uerr := json.Unmarshal(m.Value, &v); uerr == nil {
				out.State = v
			}
		case "SCHEMA":
			if !contains(out.Streams, m.Stream) {
				out.Streams = append(out.Streams, m.Stream)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("singer: tap: read stdout: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return out, fmt.Errorf("singer: tap: failed: %w: %s", err, stderr.String())
	}
	return out, nil
}

// RunTarget runs a target as a subprocess: it writes the target's config to a
// temp file and passes it as `--config`, feeds the records on stdin as newline
// delimited RECORD messages, and returns the count consumed plus the target's
// stdout. It returns an error if the target cannot start or exits non-zero.
func RunTarget(ctx context.Context, req TargetRequest) (TargetResult, error) {
	out := TargetResult{Consumed: len(req.Records)}
	if len(req.Command) == 0 {
		return out, fmt.Errorf("singer: target: empty command")
	}

	tmp, err := os.MkdirTemp("", "servitor-target-")
	if err != nil {
		return out, fmt.Errorf("singer: target: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	name := req.Command[0]
	args := append([]string{}, req.Command[1:]...)
	cfgPath, err := writeJSONFile(tmp, "config.json", req.Config)
	if err != nil {
		return out, fmt.Errorf("singer: target: config: %w", err)
	}
	args = append(args, "--config", cfgPath)

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = req.Env

	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, r := range req.Records {
		if err := enc.Encode(map[string]any{
			"type":   "RECORD",
			"stream": r.Stream,
			"record": r.Record,
		}); err != nil {
			return out, fmt.Errorf("singer: target: encode record: %w", err)
		}
	}
	cmd.Stdin = &in

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("singer: target: failed: %w: %s", err, stderr.String())
	}
	out.Output = stdout.String()
	return out, nil
}

// writeJSONFile marshals v as JSON and writes it to dir/name with 0600 perms,
// returning the path.
func writeJSONFile(dir, name string, v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

package singer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTap writes a fake tap executable that speaks the file-flags contract: it
// reads --config and --state, echoes the state file contents (when given) to
// $OUT_STATE_FILE so a test can verify the prior bookmark was passed, and
// always emits a SCHEMA, two RECORDs and a STATE on stdout.
func fakeTap(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/sh
state=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --state) state="$2"; shift 2;;
    --config|--catalog) shift 2;;
    *) shift;;
  esac
done
if [ -n "$state" ] && [ -f "$state" ]; then
  cat "$state" > "$OUT_STATE_FILE"
else
  : > "$OUT_STATE_FILE"
fi
printf '%s\n' '{"type":"SCHEMA","stream":"customers","schema":{}}'
printf '%s\n' '{"type":"RECORD","stream":"customers","record":{"id":1}}'
printf '%s\n' '{"type":"RECORD","stream":"customers","record":{"id":2}}'
printf '%s\n' '{"type":"STATE","value":{"bookmark":"x"}}'
`
	p := filepath.Join(dir, "tap-fake")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeTarget writes a fake target executable that reads RECORD lines on stdin
// and prints how many it consumed.
func fakeTarget(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/sh
n=0
while IFS= read -r line; do
  case "$line" in
    *'"type":"RECORD"'*) n=$((n+1));;
  esac
done
echo "consumed=$n"
`
	p := filepath.Join(dir, "target-fake")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunTapCollectsRecordsAndState(t *testing.T) {
	dir := t.TempDir()
	fakeTap(t, dir)
	stateFile := filepath.Join(dir, "state.json")
	_ = os.WriteFile(stateFile, []byte(`{"bookmark":"old"}`), 0o600)

	env := []string{"PATH=" + os.Getenv("PATH"), "OUT_STATE_FILE=" + stateFile}
	res, err := RunTap(context.Background(), TapRequest{
		Command: []string{filepath.Join(dir, "tap-fake")},
		Env:     env,
		Config:  map[string]any{"client_id": "abc"},
		State:   map[string]any{"bookmark": "old"},
	})
	if err != nil {
		t.Fatalf("RunTap: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
	if res.Records[0].Stream != "customers" {
		t.Fatalf("stream = %q, want customers", res.Records[0].Stream)
	}
	if res.State == nil {
		t.Fatal("state = nil, want bookmark")
	}
	if got, ok := res.State.(map[string]any); !ok || got["bookmark"] != "x" {
		t.Fatalf("state = %#v, want {bookmark:x}", res.State)
	}
	if len(res.Streams) != 1 || res.Streams[0] != "customers" {
		t.Fatalf("streams = %v, want [customers]", res.Streams)
	}

	// The fake tap echoed the --state file it received to state.json; verify
	// the prior bookmark was passed as a file flag.
	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(b), `"bookmark":"old"`) {
		t.Fatalf("state file = %q, want prior bookmark", b)
	}
}

func TestRunTapNonZeroExitIsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tap-bad")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH")}
	_, err := RunTap(context.Background(), TapRequest{Command: []string{p}, Env: env})
	if err == nil {
		t.Fatal("expected an error for a non-zero tap exit")
	}
}

func TestRunTapPassesAuthoredCatalog(t *testing.T) {
	dir := t.TempDir()
	// A tap that echoes its --catalog file to a file so a test can verify the
	// authored catalog was passed through.
	catOut := filepath.Join(dir, "catalog.out")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    --catalog) cat "$2" > "$CAT_OUT"; shift 2;;
    --config) shift 2;;
    *) shift;;
  esac
done
printf '%s\n' '{"type":"STATE","value":{}}'
`
	p := filepath.Join(dir, "tap-cat")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "CAT_OUT=" + catOut}

	// The catalog is authored into the Wafer by the agent from capabilities; it
	// must be passed to the tap unchanged, not re-discovered.
	catalog := []any{
		map[string]any{"stream": "customers", "tap_stream_id": "customers", "schema": map[string]any{"type": "object"}},
	}
	if _, err := RunTap(context.Background(), TapRequest{
		Command: []string{p}, Env: env,
		Config:  map[string]any{},
		Catalog: catalog,
	}); err != nil {
		t.Fatalf("RunTap: %v", err)
	}
	b, err := os.ReadFile(catOut)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("catalog not valid JSON: %v", err)
	}
	if len(got) != 1 || got[0]["stream"] != "customers" {
		t.Fatalf("catalog = %+v, want the authored customers entry", got)
	}
}

func TestRunTargetFeedsRecords(t *testing.T) {
	dir := t.TempDir()
	fakeTarget(t, dir)
	env := []string{"PATH=" + os.Getenv("PATH")}
	res, err := RunTarget(context.Background(), TargetRequest{
		Command: []string{filepath.Join(dir, "target-fake")},
		Env:     env,
		Config:  map[string]any{"token": "s3cr3t"},
		Records: []Record{
			{Stream: "customers", Record: map[string]any{"id": 1}},
			{Stream: "customers", Record: map[string]any{"id": 2}},
			{Stream: "orders", Record: map[string]any{"id": 3}},
		},
	})
	if err != nil {
		t.Fatalf("RunTarget: %v", err)
	}
	if res.Consumed != 3 {
		t.Fatalf("consumed = %d, want 3", res.Consumed)
	}
	if !strings.Contains(res.Output, "consumed=3") {
		t.Fatalf("output = %q, want consumed=3", res.Output)
	}
}

func TestDiscoverTapsEmitsCopyReadyCatalog(t *testing.T) {
	dir := t.TempDir()
	tap := filepath.Join(dir, "tap-fake")
	script := `#!/bin/sh
case "$1" in
  --about) printf '%s' '{"properties":{"client_id":{"type":"string"}},"required":["client_id"]}';;
  --discover) printf '%s' '[{"stream":"customers","schema":{"type":"object"}},{"stream":"orders","schema":{"type":"object"}}]';;
esac
`
	if err := os.WriteFile(tap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Declared by command (ADR-0018): the config names the exact command, so
	// there is no PATH scan.
	taps := DiscoverTaps(map[string][]string{"tap-fake": {tap}})
	if len(taps) != 1 || taps[0].Name != "tap-fake" {
		t.Fatalf("taps = %+v, want one tap-fake", taps)
	}
	d := taps[0]
	if d.Config["required"].([]any)[0] != "client_id" {
		t.Fatalf("config = %v, want client_id required", d.Config)
	}
	if len(d.Catalog) != 2 {
		t.Fatalf("catalog = %+v, want 2 streams", d.Catalog)
	}
	// Each entry is copy-ready and selected, matching the Wafer catalog shape.
	c := d.Catalog[0]
	if c.Stream != "customers" || c.TapStreamID != "customers" || c.Schema["type"] != "object" {
		t.Fatalf("catalog entry = %+v, want customers with schema", c)
	}
	if c.Metadata == nil {
		t.Fatalf("catalog entry %+v missing metadata", c)
	}
}

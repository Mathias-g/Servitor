package exec

import (
	"context"
	"strings"
	"testing"
)

func TestRunParsesJSONOutput(t *testing.T) {
	res, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `printf '{"ok":true,"n":3}'`},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	obj, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want object", res.Output)
	}
	if obj["ok"] != true || obj["n"] != float64(3) {
		t.Fatalf("output = %v, want {ok:true,n:3}", obj)
	}
}

func TestRunFeedsInputOnStdin(t *testing.T) {
	res, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `cat`},
		Input:   map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	obj, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want object", res.Output)
	}
	if obj["x"] != float64(1) {
		t.Fatalf("output = %v, want echoed input {x:1}", obj)
	}
}

func TestRunNonZeroExitIsError(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `echo boom >&2; exit 3`},
	})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should surface stderr, got: %v", err)
	}
}

func TestRunNonJSONOutputIsError(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `printf 'not json'`},
	})
	if err == nil {
		t.Fatal("expected an error for non-JSON stdout")
	}
}

func TestRunEmptyOutputIsError(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `true`},
	})
	if err == nil {
		t.Fatal("expected an error for empty stdout")
	}
}

func TestFilteredEnvOnlyDeclaredSecrets(t *testing.T) {
	env, missing := FilteredEnv(
		map[string]string{"TOKEN": "abc", "OTHER": "secret"},
		[]string{"TOKEN"},
	)
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	seen := map[string]bool{}
	for _, kv := range env {
		seen[strings.SplitN(kv, "=", 2)[0]] = true
	}
	if seen["OTHER"] {
		t.Fatal("undedclared secret OTHER leaked into env")
	}
	if !seen["TOKEN"] {
		t.Fatal("declared secret TOKEN missing from env")
	}
}

func TestFilteredEnvReportsMissing(t *testing.T) {
	_, missing := FilteredEnv(
		map[string]string{"A": "1"},
		[]string{"A", "MISSING"},
	)
	if len(missing) != 1 || missing[0] != "MISSING" {
		t.Fatalf("missing = %v, want [MISSING]", missing)
	}
}

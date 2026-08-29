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

func TestRunRedactsDeclaredSecretsFromOutput(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin", "TOKEN=s3cretvalue"}
	res, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `printf '{"token":"s3cretvalue","ok":true}'`},
		Env:     env,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	obj, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want object", res.Output)
	}
	if obj["token"] != "<redacted:TOKEN>" {
		t.Fatalf("token = %q, want redacted", obj["token"])
	}
	if obj["ok"] != true {
		t.Fatalf("ok = %v, want true", obj["ok"])
	}
}

func TestRunRedactsSecretsFromStderr(t *testing.T) {
	env := []string{"TOKEN=s3cretvalue"}
	_, err := Run(context.Background(), Request{
		Command: []string{"sh", "-c", `echo leaked s3cretvalue >&2; exit 3`},
		Env:     env,
	})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if strings.Contains(err.Error(), "s3cretvalue") {
		t.Fatalf("stderr leaked secret into error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:TOKEN>") {
		t.Fatalf("stderr should carry redacted placeholder, got: %v", err)
	}
}

func TestRedactEnvLeavesPath(t *testing.T) {
	out := redactEnv([]string{"PATH=/usr/bin:/bin", "TOKEN=x"}, "path is /usr/bin:/bin")
	if out != "path is /usr/bin:/bin" {
		t.Fatalf("PATH value was redacted: %s", out)
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

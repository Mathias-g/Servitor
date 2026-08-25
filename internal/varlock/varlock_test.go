package varlock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUnderFalseInNormalProcess(t *testing.T) {
	if Under() {
		t.Fatal("Under() should be false when not launched under varlock")
	}
}

func TestResolvedSecretsIncludesEnv(t *testing.T) {
	t.Setenv("DEMO_ENV_VAR", "hello")
	m := ResolvedSecrets()
	if m["DEMO_ENV_VAR"] != "hello" {
		t.Fatalf("ResolvedSecrets missing DEMO_ENV_VAR, got %v", m)
	}
}

// TestHelperProcess is the child of TestSelfHealResolvesSecrets: it verifies
// that, when run under `varlock run`, the sentinel is set and a declared
// secret is resolved into the environment.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_VARLOCK_HELPER") != "1" {
		return
	}
	if !Under() {
		t.Fatal("not under varlock: sentinel missing")
	}
	if m := ResolvedSecrets(); m["DEMO_SECRET"] != "s3cretvalue" {
		t.Fatalf("DEMO_SECRET not resolved, got %q", m["DEMO_SECRET"])
	}
	os.Exit(0)
}

// TestSelfHealResolvesSecrets exercises the mechanism SelfHeal depends on:
// a program launched under `varlock run` sees the sentinel and the resolved
// secret set. It is skipped when varlock is not installed.
func TestSelfHealResolvesSecrets(t *testing.T) {
	if _, err := exec.LookPath("varlock"); err != nil {
		t.Skip("varlock not installed; skipping varlock integration test")
	}
	dir := t.TempDir()
	// Canonical varlock layout (SPEC: Varlock): the schema declares the secret
	// with @sensitive and an empty value; the actual value lives in the
	// git-ignored, encrypted .env.local, not plaintext in the schema.
	if err := os.WriteFile(filepath.Join(dir, ".env.schema"),
		[]byte("# @sensitive @type=string\nDEMO_SECRET=\n"), 0o644); err != nil {
		t.Fatalf("write .env.schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"),
		[]byte("DEMO_SECRET=s3cretvalue\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	cmd := exec.Command("varlock", "run", "--", exe, "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_VARLOCK_HELPER=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("varlock run failed: %v\n%s", err, out)
	}
}

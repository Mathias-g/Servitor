package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverRoutesToProviderAndReturnsValue(t *testing.T) {
	r := ResolverFromMap(map[string]string{"TOKEN": "abc"})
	values, missing, err := r.Resolve(context.Background(), "node-a", []string{"TOKEN"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if values["TOKEN"] != "abc" {
		t.Fatalf("TOKEN = %q, want abc", values["TOKEN"])
	}
}

func TestResolverMissingSecretReportedMissing(t *testing.T) {
	reg := NewRegistry()
	reg.Register("test", MapProvider{"PRESENT": "v"})
	r := NewResolver(reg, map[string]string{"PRESENT": "test", "NOPE": "test"})
	values, missing, err := r.Resolve(context.Background(), "node-a", []string{"PRESENT", "NOPE"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if values["PRESENT"] != "v" {
		t.Fatalf("PRESENT = %q, want v", values["PRESENT"])
	}
	if len(missing) != 1 || missing[0] != "NOPE" {
		t.Fatalf("missing = %v, want [NOPE]", missing)
	}
}

func TestResolverUndeclaredSecretErrors(t *testing.T) {
	r := ResolverFromMap(map[string]string{})
	_, _, err := r.Resolve(context.Background(), "node-a", []string{"GHOST"})
	if !errors.Is(err, ErrUndeclared) {
		t.Fatalf("err = %v, want ErrUndeclared", err)
	}
}

func TestResolverUnreachableSourceErrors(t *testing.T) {
	// A secret whose source has no registered provider is an unreachable source.
	r := NewResolver(NewRegistry(), map[string]string{"X": "nosuchsource"})
	_, _, err := r.Resolve(context.Background(), "node-a", []string{"X"})
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Fatalf("err = %v, want ErrSourceUnreachable", err)
	}
}

type failProvider struct{ err error }

func (p failProvider) Resolve(context.Context, string, string) (string, error) {
	return "", p.err
}

func TestResolverPropagatesStale(t *testing.T) {
	reg := NewRegistry()
	reg.Register("test", failProvider{err: ErrStale})
	r := NewResolver(reg, map[string]string{"X": "test"})
	_, _, err := r.Resolve(context.Background(), "node-a", []string{"X"})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
}

func TestEnvProviderResolvesFromEnvironment(t *testing.T) {
	t.Setenv("SOME_ENV_VAR", "value123")
	v, err := (EnvProvider{}).Resolve(context.Background(), "", "SOME_ENV_VAR")
	if err != nil || v != "value123" {
		t.Fatalf("Resolve = %q, %v", v, err)
	}
}

func TestEnvProviderMissing(t *testing.T) {
	t.Setenv("SOME_UNSET_VAR", "")
	_, err := (EnvProvider{}).Resolve(context.Background(), "", "SOME_UNSET_VAR")
	if !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("err = %v, want ErrSecretMissing", err)
	}
}

func TestDefaultRegistryIncludesEnvAndVarlock(t *testing.T) {
	names := DefaultRegistry().SourceNames()
	if len(names) != 3 || names[0] != "env" || names[1] != "onbox" || names[2] != "varlock" {
		t.Fatalf("sources = %v, want [env onbox varlock]", names)
	}
}

func TestOnBoxProviderSealsAndResolves(t *testing.T) {
	dir := t.TempDir()
	if err := SealOnBox(dir, "GMAIL_TOKEN", "s3cret-value"); err != nil {
		t.Fatalf("SealOnBox: %v", err)
	}
	// The sealed value on disk is ciphertext, never plaintext.
	raw, err := os.ReadFile(filepath.Join(dir, "GMAIL_TOKEN"))
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}
	if strings.Contains(string(raw), "s3cret-value") {
		t.Fatalf("sealed value leaked plaintext on disk: %q", raw)
	}

	p := NewOnBoxProvider(dir)
	v, err := p.Resolve(context.Background(), "node-a", "GMAIL_TOKEN")
	if err != nil || v != "s3cret-value" {
		t.Fatalf("Resolve = %q, %v", v, err)
	}
}

func TestOnBoxProviderMissingSecret(t *testing.T) {
	dir := t.TempDir()
	if err := SealOnBox(dir, "PRESENT", "v"); err != nil {
		t.Fatalf("SealOnBox: %v", err)
	}
	p := NewOnBoxProvider(dir)
	_, err := p.Resolve(context.Background(), "node-a", "NOPE")
	if !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("err = %v, want ErrSecretMissing", err)
	}
}

func TestOnBoxProviderUninitializedStore(t *testing.T) {
	p := NewOnBoxProvider(filepath.Join(t.TempDir(), "absent"))
	_, err := p.Resolve(context.Background(), "node-a", "X")
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Fatalf("err = %v, want ErrSourceUnreachable", err)
	}
}

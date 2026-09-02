package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mathias-g/Servitor/internal/config"
)

func TestMCPAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp", "add", "--file", path, "atomic", "atomic-server", "ATOMIC_TOKEN"}, &out, &errOut); code != exitOK {
		t.Fatalf("add: code=%d err=%q", code, errOut.String())
	}

	out.Reset()
	if code := Run([]string{"mcp", "list", "--file", path}, &out, &errOut); code != exitOK {
		t.Fatalf("list: code=%d err=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "atomic") || !strings.Contains(out.String(), "atomic-server") {
		t.Fatalf("list output = %q, want atomic/atomic-server", out.String())
	}

	out.Reset()
	if code := Run([]string{"mcp", "remove", "--file", path, "atomic"}, &out, &errOut); code != exitOK {
		t.Fatalf("remove: code=%d err=%q", code, errOut.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.ServerNames()) != 0 {
		t.Fatalf("expected empty after remove, got %v", cfg.ServerNames())
	}
}

func TestTapAndTargetAdd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	if code := Run([]string{"tap", "add", "--file", path, "stripe", "tap-stripe", "STRIPE_KEY"}, &out, &errOut); code != exitOK {
		t.Fatalf("tap add: code=%d err=%q", code, errOut.String())
	}
	if code := Run([]string{"target", "add", "--file", path, "grist", "target-grist"}, &out, &errOut); code != exitOK {
		t.Fatalf("target add: code=%d err=%q", code, errOut.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Singer.Taps["stripe"].Command[0] != "tap-stripe" {
		t.Fatalf("tap command = %v", cfg.Singer.Taps["stripe"].Command)
	}
	if cfg.Singer.Targets["grist"].Command[0] != "target-grist" {
		t.Fatalf("target command = %v", cfg.Singer.Targets["grist"].Command)
	}
}

func TestMCPUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp"}, &out, &errOut); code != exitUsage {
		t.Fatalf("mcp with no subcommand should be usage error, got %d", code)
	}
}

func TestMCPRemoveMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp", "remove", "--file", path, "nope"}, &out, &errOut); code != exitFailure {
		t.Fatalf("remove missing should fail, got %d", code)
	}
}

func TestHelpMentionsIntegrations(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != exitOK {
		t.Fatalf("help: %d", code)
	}
	if !strings.Contains(out.String(), "servitor.config.yaml") {
		t.Fatalf("help should mention config, got %q", out.String())
	}
}

func TestSecretAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	if code := Run([]string{"secret", "add", "--file", path, "--account", "billing@acme.com", "--permissions", "send,read", "--expiry", "2027-01-01", "GMAIL_SEND_TOKEN", "varlock"}, &out, &errOut); code != exitOK {
		t.Fatalf("add: code=%d err=%q", code, errOut.String())
	}
	if strings.Contains(out.String(), "value") && strings.Contains(out.String(), "s3cret") {
		t.Fatalf("add output must never contain a value")
	}

	out.Reset()
	if code := Run([]string{"secret", "list", "--file", path}, &out, &errOut); code != exitOK {
		t.Fatalf("list: code=%d err=%q", code, errOut.String())
	}
	for _, want := range []string{"GMAIL_SEND_TOKEN", "source=varlock", "account=billing@acme.com", "permissions=send,read", "expiry=2027-01-01"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list output missing %q: %q", want, out.String())
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Secrets["GMAIL_SEND_TOKEN"]
	if s.Source != "varlock" || s.Account != "billing@acme.com" || len(s.Permissions) != 2 || s.Expiry != "2027-01-01" {
		t.Fatalf("secret = %+v", s)
	}

	if code := Run([]string{"secret", "remove", "--file", path, "GMAIL_SEND_TOKEN"}, &out, &errOut); code != exitOK {
		t.Fatalf("remove: code=%d err=%q", code, errOut.String())
	}
	if cfg, err = config.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Secrets) != 0 {
		t.Fatalf("expected empty after remove, got %v", cfg.Secrets)
	}
}

func TestSecretUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"secret"}, &out, &errOut); code != exitUsage {
		t.Fatalf("secret with no subcommand should be usage error, got %d", code)
	}
}

func TestSecretSealSealsToOnBox(t *testing.T) {
	// Point the on-box store at a temp dir so the test is self-contained.
	dir := t.TempDir()
	t.Setenv("SERVITOR_SECRET_DIR", dir)

	var out, errOut bytes.Buffer
	old := stdinReader
	stdinReader = func() io.Reader { return strings.NewReader("s3cret\n") }
	defer func() { stdinReader = old }()

	if code := Run([]string{"secret", "seal", "GH_TOKEN"}, &out, &errOut); code != exitOK {
		t.Fatalf("seal: code=%d err=%q", code, errOut.String())
	}

	// The value is not in the output, and is not plaintext on disk.
	if strings.Contains(out.String(), "s3cret") {
		t.Fatalf("seal output leaked the value: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "GH_TOKEN"))
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("sealed value leaked plaintext on disk: %q", raw)
	}
}

func TestWebhookAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	// An hmac receiver with the signing config for an HMAC sender (ADR-0049).
	if code := Run([]string{"webhook", "add", "--file", path, "/hooks/raw", "hmac", "RAW_SECRET", "--header", "x-signature", "--encoding", "hex", "--prefix", "sha256"}, &out, &errOut); code != exitOK {
		t.Fatalf("add hmac: code=%d err=%q", code, errOut.String())
	}
	if code := Run([]string{"webhook", "add", "--file", path, "/hooks/std", "standard", "WH_SECRET"}, &out, &errOut); code != exitOK {
		t.Fatalf("add standard: code=%d err=%q", code, errOut.String())
	}

	out.Reset()
	if code := Run([]string{"webhook", "list", "--file", path}, &out, &errOut); code != exitOK {
		t.Fatalf("list: code=%d err=%q", code, errOut.String())
	}
	for _, want := range []string{"/hooks/raw", "scheme=hmac", "header=x-signature", "prefix=sha256", "/hooks/std", "scheme=standard"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list output missing %q: %q", want, out.String())
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if raw := cfg.Webhook["/hooks/raw"]; raw == nil || raw.Prefix != "sha256" || raw.Encoding != "hex" {
		t.Fatalf("raw receiver = %+v", raw)
	}

	out.Reset()
	if code := Run([]string{"webhook", "remove", "--file", path, "/hooks/raw"}, &out, &errOut); code != exitOK {
		t.Fatalf("remove: code=%d err=%q", code, errOut.String())
	}
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cfg.Webhook["/hooks/raw"]; ok {
		t.Fatal("receiver not removed")
	}
	if _, ok := cfg.Webhook["/hooks/std"]; !ok {
		t.Fatal("other receiver should remain")
	}
}

func TestWebhookRejectsUnknownScheme(t *testing.T) {
	var out, errOut bytes.Buffer
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if code := Run([]string{"webhook", "add", "--file", path, "/hooks/x", "tiktok"}, &out, &errOut); code != exitUsage {
		t.Fatalf("add with unknown scheme should be usage error, got %d (err=%q)", code, errOut.String())
	}
}

func TestWebhookUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"webhook"}, &out, &errOut); code != exitUsage {
		t.Fatalf("webhook with no subcommand should be usage error, got %d", code)
	}
}

package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/protocol"
)

// daemonExtPath returns the Honker extension path, skipping when unset (the
// store-backed handler tests need it).
func handlerExtPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping store-backed daemon handler test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

// newTestServer builds a control-plane Server with a real store over a temp
// DB, wraps it in an httptest server, and returns the protocol client.
func newTestServer(t *testing.T) *protocol.Client {
	t.Helper()
	ext := handlerExtPath(t)
	store, err := honker.Open(filepath.Join(t.TempDir(), "t.db"), ext)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := NewServer(Config{DrainTimeout: 0})
	srv.store = store
	ts := httptest.NewServer(srv.httpSrv.Handler)
	t.Cleanup(ts.Close)
	return protocol.NewClient(strings.TrimPrefix(ts.URL, "http://"))
}

const validWafer = `
name: wf
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
`

func TestSubmitInvalidWaferReturnsValidationError(t *testing.T) {
	ctx := context.Background()
	ctl := newTestServer(t)
	// Invalid: no nodes list.
	_, err := ctl.Submit(ctx, []byte("name: broken\n"))
	if err == nil {
		t.Fatal("expected submit of an invalid wafer to error")
	}
	if !strings.Contains(err.Error(), "missing_nodes") {
		t.Fatalf("error %q should mention missing_nodes", err.Error())
	}
}

func TestUpdateUnknownWorkflowFails(t *testing.T) {
	ctx := context.Background()
	ctl := newTestServer(t)
	if _, err := ctl.Update(ctx, []byte(validWafer)); err == nil {
		t.Fatal("expected updating an unregistered workflow to fail")
	}
}

func TestSubmitThenUpdateSucceeds(t *testing.T) {
	ctx := context.Background()
	ctl := newTestServer(t)
	if _, err := ctl.Submit(ctx, []byte(validWafer)); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := ctl.Update(ctx, []byte(validWafer)); err != nil {
		t.Fatalf("update after submit: %v", err)
	}
}

func TestSubmitRejectsUndeclaredSecret(t *testing.T) {
	ctx := context.Background()
	ext := handlerExtPath(t)
	store, err := honker.Open(filepath.Join(t.TempDir(), "t.db"), ext)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := NewServer(Config{Resolver: secret.ResolverFromMap(map[string]string{"DECLARED": "v"})})
	srv.store = store
	ts := httptest.NewServer(srv.httpSrv.Handler)
	t.Cleanup(ts.Close)
	ctl := protocol.NewClient(strings.TrimPrefix(ts.URL, "http://"))

	wf := `
name: wf
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
    secrets: [UNDECLARED]
`
	if _, err := ctl.Submit(ctx, []byte(wf)); err == nil {
		t.Fatal("expected submit referencing an undeclared secret to fail")
	} else if !strings.Contains(err.Error(), "UNDECLARED") {
		t.Fatalf("error %q should mention the undeclared secret", err.Error())
	}

	// A Wafer referencing a declared secret submits fine.
	ok := `
name: wf
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
    secrets: [DECLARED]
`
	if _, err := ctl.Submit(ctx, []byte(ok)); err != nil {
		t.Fatalf("submit with declared secret: %v", err)
	}
}

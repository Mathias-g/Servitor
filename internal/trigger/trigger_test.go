package trigger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/wafer"
)

func extPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping Honker-backed trigger test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

func newReceiver(t *testing.T, secrets map[string]string) (*Receiver, *honker.Store, *honker.Queue) {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("steps", 30, 3)
	return NewReceiver(store, q, secrets), store, q
}

const wfYAML = `
name: demo
on:
  - type: standard_webhook
    path: /hooks/demo
    secret: WEBHOOK_SECRET
steps:
  - type: shell
    name: a
    command: "printf '{\"ok\":true}'"
`

func register(t *testing.T, store *honker.Store, yaml string) {
	t.Helper()
	var name string
	w, err := wafer.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	name = w.Name
	if err := store.RegisterWorkflow(name, yaml); err != nil {
		t.Fatalf("register: %v", err)
	}
}

// standardWebhookSignature computes the Standard Webhooks signature header
// value for a body.
func standardWebhookSignature(secret, id, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(id + "." + ts + "."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestWebhookPersistsEventAndEnqueuesRun(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"WEBHOOK_SECRET": "s3cret"})
	register(t, store, wfYAML)

	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"event":"lead_created"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
	req.Header.Set("webhook-id", "evt_1")
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", standardWebhookSignature("s3cret", "evt_1", ts, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// Event persisted before matching.
	evCount, err := store.EventCount()
	if err != nil {
		t.Fatalf("events count: %v", err)
	}
	if evCount != 1 {
		t.Fatalf("events persisted = %d, want 1", evCount)
	}

	// A run was enqueued (head step claimable).
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("run not enqueued: %v", err)
	}
	_ = job
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"WEBHOOK_SECRET": "s3cret"})
	register(t, store, wfYAML)

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
	req.Header.Set("webhook-id", "evt_1")
	req.Header.Set("webhook-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("webhook-signature", "v1,c2lnbmF0dXJl")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}

	// The bad event was still persisted (before verification).
	evCount, err := store.EventCount()
	if err != nil {
		t.Fatalf("events count: %v", err)
	}
	if evCount != 1 {
		t.Fatalf("events persisted = %d, want 1 (persist happens before verify)", evCount)
	}

	// No run enqueued.
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatalf("run enqueued despite bad signature")
	}
}

func TestWebhookNoSecretAllowsOpenReceiver(t *testing.T) {
	// No secret resolved for the trigger => open receiver, event accepted.
	r, store, q := newReceiver(t, map[string]string{})
	register(t, store, wfYAML)

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no secret = open)", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job == nil {
		t.Fatal("run not enqueued")
	}
}

func TestWebhookUnmatchedPathStillPersists(t *testing.T) {
	r, store, _ := newReceiver(t, map[string]string{})
	register(t, store, wfYAML)

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/other", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	evCount, err := store.EventCount()
	if err != nil {
		t.Fatalf("events count: %v", err)
	}
	if evCount != 1 {
		t.Fatalf("events persisted = %d, want 1", evCount)
	}
}

func TestDisabledWorkflowDoesNotFire(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{})
	register(t, store, wfYAML)
	if err := store.SetWorkflowEnabled("demo", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatalf("disabled workflow fired a run")
	}
}

func TestManualTriggersWorkflow(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{})
	register(t, store, `
name: m
on:
  - type: manual
steps:
  - type: shell
    name: a
    command: "printf '{\"ok\":true}'"
`)
	if err := r.Manual(context.Background(), "m", map[string]any{"x": 1}); err != nil {
		t.Fatalf("Manual: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("manual run not enqueued: %v", err)
	}
	var head struct {
		RunID  string         `json:"RunID"`
		StepID string         `json:"StepID"`
		Input  map[string]any `json:"Input"`
	}
	if err := json.Unmarshal(job.Payload, &head); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if head.StepID != "a" {
		t.Fatalf("head step = %q, want a", head.StepID)
	}
	// Head input is wrapped as {event, steps} (ADR-0021).
	ev, _ := head.Input["event"].(map[string]any)
	if ev["x"] != float64(1) {
		t.Fatalf("input = %v, want event.x:1", head.Input)
	}
}

func TestManualRejectsUnknownWorkflow(t *testing.T) {
	r, _, _ := newReceiver(t, map[string]string{})
	if err := r.Manual(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unregistered workflow")
	}
}

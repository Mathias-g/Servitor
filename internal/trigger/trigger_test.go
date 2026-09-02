package trigger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/config"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
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

func newReceiver(t *testing.T, secrets map[string]string, receivers map[string]*config.WebhookReceiver) (*Receiver, *honker.Store, *honker.Queue) {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 3)
	return NewReceiver(store, q, secret.ResolverFromMap(secrets), receivers), store, q
}

// openReceiver builds a receiver with no declared webhook receivers and the
// given secrets, for tests of the non-webhook triggers.
func openReceiver(t *testing.T, secrets map[string]string) (*Receiver, *honker.Store, *honker.Queue) {
	t.Helper()
	return newReceiver(t, secrets, nil)
}

const wfYAML = `
name: demo
triggers:
  - type: standard-webhook
    path: /hooks/demo
nodes:
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

// hmacRawSignature computes an HMAC-SHA256 signature of the raw body with the
// given version prefix, hex-encoded: `<prefix>=<hex>`.
func hmacRawSignature(secret, prefix string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return prefix + "=" + hex.EncodeToString(mac.Sum(nil))
}

// hmacTimestampedSignature computes an HMAC-SHA256 signature of
// `<prefix>:<timestamp>:<body>` at the given timestamp, hex-encoded:
// `<prefix>=<hex>`.
func hmacTimestampedSignature(secret, prefix, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(prefix + ":" + ts + ":" + string(body)))
	return prefix + "=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookPersistsEventAndEnqueuesRun(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"WEBHOOK_SECRET": "s3cret"}, map[string]*config.WebhookReceiver{
		"/hooks/demo": {Scheme: config.SchemeStandard, Secret: "WEBHOOK_SECRET"},
	})
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

	// A run was enqueued (head node claimable).
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("run not enqueued: %v", err)
	}
	_ = job
}

func TestWebhookDeliversRawBodyAsEvent(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"WEBHOOK_SECRET": "s3cret"}, map[string]*config.WebhookReceiver{
		"/hooks/demo": {Scheme: config.SchemeStandard, Secret: "WEBHOOK_SECRET"},
	})
	register(t, store, wfYAML)

	body := []byte(`{"event":"lead_created"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
	req.Header.Set("webhook-id", "evt_1")
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", standardWebhookSignature("s3cret", "evt_1", ts, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("run not enqueued: %v", err)
	}
	var head struct {
		Input map[string]any `json:"Input"`
	}
	if err := json.Unmarshal(job.Payload, &head); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The raw body is delivered as event.body (ADR-0049); the workflow parses
	// it itself with a transform node.
	ev, _ := head.Input["event"].(map[string]any)
	if ev["body"] != string(body) {
		t.Fatalf("event.body = %v, want raw body %q", ev["body"], body)
	}
	if ev["path"] != "/hooks/demo" {
		t.Fatalf("event.path = %v, want /hooks/demo", ev["path"])
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"WEBHOOK_SECRET": "s3cret"}, map[string]*config.WebhookReceiver{
		"/hooks/demo": {Scheme: config.SchemeStandard, Secret: "WEBHOOK_SECRET"},
	})
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
	// A receiver with no declared secret is an open receiver: the event is
	// accepted without verification (ADR-0049).
	r, store, q := newReceiver(t, map[string]string{}, map[string]*config.WebhookReceiver{
		"/hooks/demo": {Scheme: config.SchemeStandard},
	})
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

func TestWebhookUndeclaredPathMatchesNothing(t *testing.T) {
	r, store, _ := openReceiver(t, map[string]string{})
	register(t, store, wfYAML)

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
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

func TestWebhookTypeMismatchDoesNotFire(t *testing.T) {
	// The receiver declares scheme hmac, but the workflow's trigger type is
	// standard-webhook (ADR-0049). The mismatch is rejected at submit; at serve
	// time the trigger never matches, so no run fires.
	r, store, q := newReceiver(t, map[string]string{"SECRET": "s"}, map[string]*config.WebhookReceiver{
		"/hooks/demo": {Scheme: config.SchemeHMAC, Secret: "SECRET"},
	})
	register(t, store, wfYAML)

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/demo", bytes.NewReader(body))
	// A valid signature for the receiver's scheme: verification passes, so any
	// failure to fire is purely the type/scheme mismatch, not the signature.
	mac := hmac.New(sha256.New, []byte("s"))
	_, _ = mac.Write(body)
	req.Header.Set("x-servitor-signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for a trigger whose type mismatches the receiver scheme")
	}
}

func TestDisabledWorkflowDoesNotFire(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
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
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: m
triggers:
  - type: manual
nodes:
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
		NodeID string         `json:"NodeID"`
		Input  map[string]any `json:"Input"`
	}
	if err := json.Unmarshal(job.Payload, &head); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if head.NodeID != "a" {
		t.Fatalf("head node = %q, want a", head.NodeID)
	}
	// Head input is wrapped as {event, steps} (ADR-0021).
	ev, _ := head.Input["event"].(map[string]any)
	if ev["x"] != float64(1) {
		t.Fatalf("input = %v, want event.x:1", head.Input)
	}
}

func TestManualRejectsUnknownWorkflow(t *testing.T) {
	r, _, _ := openReceiver(t, map[string]string{})
	if err := r.Manual(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unregistered workflow")
	}
}

func TestHMACWebhookVerifiesAndEnqueues(t *testing.T) {
	// A raw-body hmac receiver: hex encoding, a "sha256=" version prefix, no
	// timestamp (ADR-0049). The mechanism sees none of these details; they are
	// the receiver's config.
	r, store, q := newReceiver(t, map[string]string{"RAW_SECRET": "rawsecret"}, map[string]*config.WebhookReceiver{
		"/hooks/raw": {Scheme: config.SchemeHMAC, Secret: "RAW_SECRET", Header: "x-signature", Encoding: "hex", Prefix: "sha256"},
	})
	register(t, store, `
name: raw
triggers:
  - type: hmac-webhook
    path: /hooks/raw
nodes:
  - type: shell
    name: a
    command: "true"
`)
	body := []byte(`{"kind":"update"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/raw", bytes.NewReader(body))
	req.Header.Set("x-signature", hmacRawSignature("rawsecret", "sha256", body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if job, _ := q.ClaimOne("worker-1"); job == nil {
		t.Fatal("run not enqueued")
	}
}

func TestHMACWebhookRejectsBadSignature(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"RAW_SECRET": "rawsecret"}, map[string]*config.WebhookReceiver{
		"/hooks/raw": {Scheme: config.SchemeHMAC, Secret: "RAW_SECRET", Header: "x-signature", Encoding: "hex", Prefix: "sha256"},
	})
	register(t, store, `
name: raw
triggers:
  - type: hmac-webhook
    path: /hooks/raw
nodes:
  - type: shell
    name: a
    command: "true"
`)
	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/raw", bytes.NewReader(body))
	req.Header.Set("x-signature", "sha256=deadbeef")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued despite bad signature")
	}
}

func TestHMACTimestampedWebhookVerifiesAndEnqueues(t *testing.T) {
	// A timestamped hmac receiver: the body is signed as
	// <prefix>:<timestamp>:<body> and replay is bounded. It is an hmac
	// receiver, not a separate mechanism (ADR-0049).
	r, store, q := newReceiver(t, map[string]string{"TIMED_SECRET": "timedsecret"}, map[string]*config.WebhookReceiver{
		"/hooks/timed": {Scheme: config.SchemeHMAC, Secret: "TIMED_SECRET", Header: "x-signature", Encoding: "hex", TimestampHeader: "x-timestamp", Prefix: "v0"},
	})
	register(t, store, `
name: timed
triggers:
  - type: hmac-webhook
    path: /hooks/timed
nodes:
  - type: shell
    name: a
    command: "true"
`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte(`{"kind":"message","text":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/timed", bytes.NewReader(body))
	req.Header.Set("x-timestamp", ts)
	req.Header.Set("x-signature", hmacTimestampedSignature("timedsecret", "v0", ts, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if job, _ := q.ClaimOne("worker-1"); job == nil {
		t.Fatal("run not enqueued")
	}
}

func TestHMACTimestampedWebhookRejectsStaleTimestamp(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"TIMED_SECRET": "timedsecret"}, map[string]*config.WebhookReceiver{
		"/hooks/timed": {Scheme: config.SchemeHMAC, Secret: "TIMED_SECRET", Header: "x-signature", Encoding: "hex", TimestampHeader: "x-timestamp", Prefix: "v0"},
	})
	register(t, store, `
name: timed
triggers:
  - type: hmac-webhook
    path: /hooks/timed
nodes:
  - type: shell
    name: a
    command: "true"
`)
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	body := []byte(`{"kind":"message"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/timed", bytes.NewReader(body))
	req.Header.Set("x-timestamp", old)
	req.Header.Set("x-signature", hmacTimestampedSignature("timedsecret", "v0", old, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for stale timestamp", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued despite stale timestamp")
	}
}

func TestCompletedFiresDownstreamWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: upstream
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
`)
	register(t, store, `
name: downstream
triggers:
  - type: completed
    workflow: upstream
nodes:
  - type: shell
    name: a
    command: "true"
`)
	if err := r.Completed("upstream", "run-1"); err != nil {
		t.Fatalf("Completed: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("downstream run not enqueued: %v", err)
	}
	var sj worker.NodeJob
	if err := job.UnmarshalPayload(&sj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sj.WorkflowID != "downstream" {
		t.Fatalf("enqueued workflow = %q, want downstream", sj.WorkflowID)
	}
	ev := sj.Input["event"].(map[string]any)
	if ev["trigger"] != "completed" || ev["from"] != "upstream" || ev["from_run"] != "run-1" {
		t.Fatalf("event = %v, want completed/upstream/run-1", ev)
	}
}

func TestCompletedIgnoresOtherWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: downstream
triggers:
  - type: completed
    workflow: upstream
nodes:
  - type: shell
    name: a
    command: "true"
`)
	if err := r.Completed("other-workflow", "run-1"); err != nil {
		t.Fatalf("Completed: %v", err)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for a workflow naming a different upstream")
	}
}

func TestCompletedSkipsDisabledWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: downstream
triggers:
  - type: completed
    workflow: upstream
nodes:
  - type: shell
    name: a
    command: "true"
`)
	if err := store.SetWorkflowEnabled("downstream", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := r.Completed("upstream", "run-1"); err != nil {
		t.Fatalf("Completed: %v", err)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for a disabled workflow")
	}
}

func TestFailedFiresDownstreamWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: upstream
triggers:
  - type: manual
nodes:
  - type: shell
    name: a
    command: "true"
`)
	register(t, store, `
name: alert
triggers:
  - type: failed
    workflow: upstream
nodes:
  - type: shell
    name: notify
    command: "true"
`)
	if err := r.Failed("upstream", "run-1"); err != nil {
		t.Fatalf("Failed: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("alert run not enqueued: %v", err)
	}
	var sj worker.NodeJob
	if err := job.UnmarshalPayload(&sj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sj.WorkflowID != "alert" {
		t.Fatalf("enqueued workflow = %q, want alert", sj.WorkflowID)
	}
	ev := sj.Input["event"].(map[string]any)
	if ev["trigger"] != "failed" || ev["from"] != "upstream" || ev["from_run"] != "run-1" {
		t.Fatalf("event = %v, want failed/upstream/run-1", ev)
	}
}

func TestFailedIgnoresOtherWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: alert
triggers:
  - type: failed
    workflow: upstream
nodes:
  - type: shell
    name: notify
    command: "true"
`)
	if err := r.Failed("other-workflow", "run-1"); err != nil {
		t.Fatalf("Failed: %v", err)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for a workflow naming a different upstream")
	}
}

func TestEmailReceivedFiresRunPerEmail(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: mail
triggers:
  - type: email_received
    host: imap.gmail.com
    username: me@company.com
    secret: GMAIL_APP_PASSWORD
nodes:
  - type: shell
    name: a
    command: "true"
`)
	items := []any{
		map[string]any{"from": "a@b.com", "to": []string{"me@company.com"}, "subject": "One", "body": "first"},
		map[string]any{"from": "c@d.com", "to": []string{"me@company.com"}, "subject": "Two", "body": "second"},
	}
	if err := r.Polled("mail", "email", items); err != nil {
		t.Fatalf("Polled: %v", err)
	}
	// Two emails => two enqueued runs.
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		job, err := q.ClaimOne("worker-1")
		if err != nil || job == nil {
			t.Fatalf("run %d not enqueued: %v", i, err)
		}
		var sj worker.NodeJob
		if err := job.UnmarshalPayload(&sj); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if sj.WorkflowID != "mail" {
			t.Fatalf("workflow = %q, want mail", sj.WorkflowID)
		}
		ev := sj.Input["event"].(map[string]any)
		if ev["trigger"] != "email_received" {
			t.Fatalf("event trigger = %v, want email_received", ev["trigger"])
		}
		seen[ev["subject"].(string)] = true
	}
	if !seen["One"] || !seen["Two"] {
		t.Fatalf("subjects = %v, want One and Two", seen)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("more runs enqueued than emails")
	}
}

func TestEmailReceivedSkipsDisabledWorkflow(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: mail
triggers:
  - type: email_received
    host: imap.gmail.com
    username: me@company.com
    secret: GMAIL_APP_PASSWORD
nodes:
  - type: shell
    name: a
    command: "true"
`)
	if err := store.SetWorkflowEnabled("mail", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := r.Polled("mail", "email", []any{map[string]any{"subject": "Hi"}}); err != nil {
		t.Fatalf("Polled: %v", err)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for a disabled workflow")
	}
}

func TestPolledUnknownKindPassesItemThrough(t *testing.T) {
	r, store, q := openReceiver(t, map[string]string{})
	register(t, store, `
name: feed
triggers:
  - type: poll
    kind: rss
    schedule: "*/5 * * * *"
nodes:
  - type: shell
    name: a
    command: "true"
`)
	if err := r.Polled("feed", "rss", []any{map[string]any{"title": "post"}}); err != nil {
		t.Fatalf("Polled: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("run not enqueued: %v", err)
	}
	var sj worker.NodeJob
	if err := job.UnmarshalPayload(&sj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ev := sj.Input["event"].(map[string]any)
	if ev["trigger"] != "rss" {
		t.Fatalf("event trigger = %v, want rss", ev["trigger"])
	}
	if _, ok := ev["item"].(map[string]any); !ok {
		t.Fatalf("event item missing: %v", ev)
	}
}

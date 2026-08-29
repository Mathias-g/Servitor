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

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/secret"
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

func newReceiver(t *testing.T, secrets map[string]string) (*Receiver, *honker.Store, *honker.Queue) {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 3)
	return NewReceiver(store, q, secret.ResolverFromMap(secrets)), store, q
}

const wfYAML = `
name: demo
triggers:
  - type: standard_webhook
    path: /hooks/demo
    secret: WEBHOOK_SECRET
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

// githubSignature computes the GitHub X-Hub-Signature-256 header value for a
// body.
func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// slackSignature computes the Slack X-Slack-Signature header value for a body
// at the given timestamp.
func slackSignature(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + ts + ":" + string(body)))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
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

	// A run was enqueued (head node claimable).
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
	r, _, _ := newReceiver(t, map[string]string{})
	if err := r.Manual(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unregistered workflow")
	}
}

func TestGithubWebhookVerifiesAndEnqueues(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"GITHUB_SECRET": "ghsecret"})
	register(t, store, `
name: gh
triggers:
  - type: github_webhook
    path: /hooks/github
    secret: GITHUB_SECRET
nodes:
  - type: shell
    name: a
    command: "true"
`)
	body := []byte(`{"action":"opened","issue":{"number":1}}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSignature("ghsecret", body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if job, _ := q.ClaimOne("worker-1"); job == nil {
		t.Fatal("run not enqueued")
	}
}

func TestGithubWebhookRejectsBadSignature(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"GITHUB_SECRET": "ghsecret"})
	register(t, store, `
name: gh
triggers:
  - type: github_webhook
    path: /hooks/github
    secret: GITHUB_SECRET
nodes:
  - type: shell
    name: a
    command: "true"
`)
	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued despite bad signature")
	}
}

func TestSlackWebhookVerifiesAndEnqueues(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"SLACK_SECRET": "slacksecret"})
	register(t, store, `
name: sl
triggers:
  - type: slack_event
    path: /hooks/slack
    secret: SLACK_SECRET
nodes:
  - type: shell
    name: a
    command: "true"
`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte(`{"type":"message","text":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/slack", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSignature("slacksecret", ts, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if job, _ := q.ClaimOne("worker-1"); job == nil {
		t.Fatal("run not enqueued")
	}
}

func TestSlackUrlVerificationEchoesChallenge(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"SLACK_SECRET": "slacksecret"})
	register(t, store, `
name: sl
triggers:
  - type: slack_event
    path: /hooks/slack
    secret: SLACK_SECRET
nodes:
  - type: shell
    name: a
    command: "true"
`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte(`{"token":"t","type":"url_verification","challenge":"abc123"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/slack", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSignature("slacksecret", ts, body))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode challenge response: %v", err)
	}
	if resp.Challenge != "abc123" {
		t.Fatalf("challenge = %q, want abc123", resp.Challenge)
	}
	// A verification handshake must not enqueue a run.
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatal("run enqueued for url_verification handshake")
	}
}

func TestSlackRejectsStaleTimestamp(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{"SLACK_SECRET": "slacksecret"})
	register(t, store, `
name: sl
triggers:
  - type: slack_event
    path: /hooks/slack
    secret: SLACK_SECRET
nodes:
  - type: shell
    name: a
    command: "true"
`)
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	body := []byte(`{"type":"message"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/slack", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", old)
	req.Header.Set("X-Slack-Signature", slackSignature("slacksecret", old, body))
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
	r, store, q := newReceiver(t, map[string]string{})
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
	r, store, q := newReceiver(t, map[string]string{})
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
	r, store, q := newReceiver(t, map[string]string{})
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

func TestEmailReceivedFiresRunPerEmail(t *testing.T) {
	r, store, q := newReceiver(t, map[string]string{})
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
	r, store, q := newReceiver(t, map[string]string{})
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
	r, store, q := newReceiver(t, map[string]string{})
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

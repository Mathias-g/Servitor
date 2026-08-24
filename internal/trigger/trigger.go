// Package trigger implements inbound triggers: webhook reception and the
// manual trigger (SPEC: Triggers, Execution model steps 1-5). A receiver
// persists the raw event, verifies the signature, matches it against the
// registered workflows whose `on:` trigger path it hit, and enqueues a run for
// each match.
//
// The webhook listener is deliberately separate from the loopback-only control
// plane (ADR-0009): webhooks must be reachable by external senders, so the
// control plane stays closed while inbound events are served on their own
// bind address.
package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/runner"
	"github.com/Mathias-g/Servitor/internal/wafer"
)

// Receiver handles inbound webhook events and manual triggers against the
// runner's registered workflows.
type Receiver struct {
	store   *honker.Store
	queue   *honker.Queue
	secrets map[string]string
	// now is injectable for tests.
	now func() time.Time
}

// NewReceiver builds a receiver over the store's queue. secrets is the
// runner's resolved secret map; webhook triggers name a secret to verify with.
func NewReceiver(store *honker.Store, queue *honker.Queue, secrets map[string]string) *Receiver {
	return &Receiver{store: store, queue: queue, secrets: secrets, now: time.Now}
}

// matchPath returns the trigger's configured path, or "" when it has none.
func matchPath(tr wafer.Trigger) string {
	if p, ok := tr.Config["path"].(string); ok {
		return p
	}
	return ""
}

func secretName(tr wafer.Trigger) string {
	if s, ok := tr.Config["secret"].(string); ok {
		return s
	}
	return ""
}

// ServeHTTP handles an inbound webhook. The flow follows the execution model:
// persist the raw event before any matching, verify the signature, match
// registered enabled workflows whose trigger path this request hit, and
// enqueue a run for each.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// 1. Persist the raw event before matching (SPEC step 2).
	if _, err := r.store.AppendEvent(req.URL.Path, string(body)); err != nil {
		http.Error(w, "persist event", http.StatusInternalServerError)
		return
	}

	// 2. Find matching enabled workflows and verify each trigger's signature.
	var matches []*wafer.Wafer
	workflows, err := r.store.ListWorkflows()
	if err != nil {
		http.Error(w, "list workflows", http.StatusInternalServerError)
		return
	}
	for _, wf := range workflows {
		if !wf.Enabled {
			continue
		}
		parsed, perr := wafer.Parse([]byte(wf.Wafer))
		if perr != nil {
			continue
		}
		for _, tr := range parsed.On {
			if !isWebhookType(tr.Type) {
				continue
			}
			if matchPath(tr) != req.URL.Path {
				continue
			}
			if !r.verify(parsed, tr, req.Header, body) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
			matches = append(matches, parsed)
		}
	}

	// 3. Enqueue a run per match (SPEC step 5), with the event as input.
	for _, m := range matches {
		if err := r.enqueueRun(m, string(body)); err != nil {
			http.Error(w, "enqueue run", http.StatusInternalServerError)
			return
		}
	}

	// A recognized-but-unmatched path (no registered workflow) still succeeds;
	// the event is already persisted. Unknown sender behavior is a 2xx.
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// enqueueRun builds and enqueues a run of the workflow from the event payload.
func (r *Receiver) enqueueRun(w *wafer.Wafer, payload string) error {
	var input map[string]any
	_ = json.Unmarshal([]byte(payload), &input)
	if input == nil {
		input = map[string]any{}
	}
	runID := fmt.Sprintf("%s-%d", w.Name, r.now().UnixNano())
	_, err := runner.StartRun(r.store, r.queue, w, input, runID)
	return err
}

// Manual triggers a registered, enabled workflow by name, with the given
// inputs as the run's event payload (SPEC: `manual` trigger).
func (r *Receiver) Manual(ctx context.Context, name string, inputs map[string]any) error {
	wf, err := r.store.GetWorkflow(name)
	if err != nil {
		return err
	}
	if wf == nil {
		return fmt.Errorf("trigger: workflow %q is not registered", name)
	}
	if !wf.Enabled {
		return fmt.Errorf("trigger: workflow %q is disabled", name)
	}
	w, perr := wafer.Parse([]byte(wf.Wafer))
	if perr != nil {
		return fmt.Errorf("trigger: workflow %q: %w", name, perr)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	runID := fmt.Sprintf("%s-%d", name, r.now().UnixNano())
	if _, err := runner.StartRun(r.store, r.queue, w, inputs, runID); err != nil {
		return fmt.Errorf("trigger: manual %q: %w", name, err)
	}
	return nil
}

// isWebhookType reports whether the trigger type is served by the webhook
// receiver. Provider-specific receivers are a later phase; the generic and
// Standard Webhooks types are the ones this phase serves.
func isWebhookType(typ string) bool {
	switch typ {
	case "http_webhook", "standard_webhook":
		return true
	default:
		return false
	}
}

// verify checks the request signature for a webhook trigger. It returns true
// when the signature is valid or when the trigger names no secret (an open
// receiver; varlock secret resolution is Phase 8). It returns false only on a
// definite mismatch.
func (r *Receiver) verify(w *wafer.Wafer, tr wafer.Trigger, h http.Header, body []byte) bool {
	secret := r.secrets[secretName(tr)]
	if secret == "" {
		return true
	}
	switch tr.Type {
	case "standard_webhook":
		return verifyStandardWebhook(secret, h, body, r.now())
	case "http_webhook":
		return verifyHMAC(secret, h, body)
	default:
		return true
	}
}

// verifyStandardWebhook implements Standard Webhooks signature verification
// (SPEC: Standard Webhooks). The message is
// `<webhook-id>.<webhook-timestamp>.<body>`, signed with HMAC-SHA256, base64 in
// the `webhook-signature` header as a comma-separated `v1,<sig>` list. The
// timestamp must be within tolerance to bound replay.
func verifyStandardWebhook(secret string, h http.Header, body []byte, now time.Time) bool {
	id := h.Get("webhook-id")
	tsRaw := h.Get("webhook-timestamp")
	sigHeader := h.Get("webhook-signature")
	if id == "" || tsRaw == "" || sigHeader == "" {
		return false
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return false
	}
	if delta := now.Unix() - ts; delta < -300 || delta > 300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(id + "." + tsRaw + "."))
	_, _ = mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, part := range strings.Split(sigHeader, " ") {
		kv := strings.SplitN(part, ",", 2)
		if len(kv) == 2 && kv[0] == "v1" && hmac.Equal([]byte(kv[1]), []byte(expected)) {
			return true
		}
	}
	return false
}

// verifyHMAC verifies an HMAC-SHA256 signature of the body carried in the
// `x-servitor-signature` header. It is the default scheme for the generic
// `http_webhook` trigger.
func verifyHMAC(secret string, h http.Header, body []byte) bool {
	got := h.Get("x-servitor-signature")
	if got == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(expected))
}

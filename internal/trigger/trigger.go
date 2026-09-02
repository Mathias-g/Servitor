// Package trigger implements inbound triggers: webhook reception and the
// manual trigger (SPEC: Triggers, Execution model steps 1-5). A receiver
// persists the raw event, looks up the declared webhook receiver for the
// request's path, verifies the signature, matches it against the registered
// workflows whose trigger path it hit, and enqueues a run for each.
//
// Webhook receivers are declared in servitor.config.yaml (ADR-0049), keyed by
// path, and loaded once at daemon boot (the same config-loaded-once pattern as
// the secret resolver and the MCP connector lookup; THREATS.md). A Wafer's
// webhook trigger names a receiver by its path; the mechanism (hmac-webhook or
// standard-webhook) is chosen by the receiver's declared scheme. Both
// mechanisms deliver the raw body as the run's event; the workflow parses it
// itself with a `transform` node, so no per-service parsing is compiled in.
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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mathias-g/Servitor/internal/components/email"
	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/config"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/runner"
	"github.com/Mathias-g/Servitor/internal/wafer"
)

// Receiver handles inbound webhook events and manual triggers against the
// runner's registered workflows.
type Receiver struct {
	store    *honker.Store
	queue    *honker.Queue
	resolver *secret.Resolver
	// receivers is the declared webhook receiver config, keyed by path
	// (ADR-0049). It is loaded once at boot and not re-read per request.
	receivers map[string]*config.WebhookReceiver
	// now is injectable for tests.
	now func() time.Time
}

// NewReceiver builds a receiver over the store's queue. resolver resolves a
// receiver's signing key per use (SPEC: Secret resolution); receivers declares
// the webhook receivers from servitor.config.yaml (ADR-0049), keyed by path.
func NewReceiver(store *honker.Store, queue *honker.Queue, resolver *secret.Resolver, receivers map[string]*config.WebhookReceiver) *Receiver {
	return &Receiver{store: store, queue: queue, resolver: resolver, receivers: receivers, now: time.Now}
}

// matchPath returns the trigger's configured path, or "" when it has none.
func matchPath(tr wafer.Trigger) string {
	if p, ok := tr.Config["path"].(string); ok {
		return p
	}
	return ""
}

// isWebhookType reports whether the trigger type is served by the webhook
// receiver. A Wafer's webhook trigger is one of the two verification-scheme
// mechanisms (ADR-0049); the actual mechanism runs as chosen by the declared
// receiver's scheme.
func isWebhookType(typ string) bool {
	return typ == "hmac-webhook" || typ == "standard-webhook"
}

// SchemeForType returns the declared receiver scheme a webhook trigger type
// corresponds to, or "" for a non-webhook type. The trigger's type must match
// the declared receiver's scheme; a mismatch is rejected at submit and never
// matches at serve time.
func SchemeForType(typ string) string {
	switch typ {
	case "hmac-webhook":
		return config.SchemeHMAC
	case "standard-webhook":
		return config.SchemeStandard
	default:
		return ""
	}
}

// TypeForScheme returns the webhook trigger type that runs a declared receiver
// with the given scheme, or "" for an unknown scheme.
func TypeForScheme(scheme string) string {
	switch scheme {
	case config.SchemeHMAC:
		return "hmac-webhook"
	case config.SchemeStandard:
		return "standard-webhook"
	default:
		return ""
	}
}

// ServeHTTP handles an inbound webhook. The flow follows the execution model:
// persist the raw event before any matching, look up the declared receiver for
// the path, verify the signature, match registered enabled workflows whose
// trigger path this request hit, and enqueue a run for each.
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

	// 2. The declared receiver for this path decides how to verify (ADR-0049).
	// A path with no declared receiver matches nothing; the event is already
	// persisted and the sender still gets a 2xx.
	receiver := r.receivers[req.URL.Path]
	if receiver == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if !r.verify(receiver, req.Header, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// 3. Match registered enabled workflows whose webhook trigger path this
	// request hit and whose type matches the receiver's scheme.
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
			if SchemeForType(tr.Type) != receiver.Scheme {
				continue
			}
			matches = append(matches, parsed)
		}
	}

	// 4. Enqueue a run per match (SPEC step 5), with the raw body as the event.
	for _, m := range matches {
		if err := r.enqueueRun(m, req.URL.Path, string(body)); err != nil {
			http.Error(w, "enqueue run", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// enqueueRun builds and enqueues a run of the workflow from the raw webhook
// body. Both webhook mechanisms deliver the raw body as the run's event; the
// workflow parses it itself (SPEC: Using webhook triggers, ADR-0049).
func (r *Receiver) enqueueRun(w *wafer.Wafer, path, body string) error {
	event := map[string]any{
		"trigger": "webhook",
		"path":    path,
		"body":    body,
	}
	runID := fmt.Sprintf("%s-%d", w.Name, r.now().UnixNano())
	_, err := runner.StartRun(r.store, r.queue, w, event, runID)
	return err
}

// verify checks the request signature for a declared webhook receiver. It
// resolves the receiver's signing key fresh per use (SPEC: Secret resolution);
// there is no rollover window, so a message that does not verify with the
// current key is rejected and logged, with no retry. It returns true when the
// signature is valid or when the receiver declares no secret (an open
// receiver). It returns false only on a definite mismatch.
func (r *Receiver) verify(receiver *config.WebhookReceiver, h http.Header, body []byte) bool {
	if receiver.Secret == "" || r.resolver == nil {
		return true
	}
	values, missing, err := r.resolver.Resolve(context.Background(), "webhook", []string{receiver.Secret})
	if err != nil || len(missing) > 0 {
		return true
	}
	secret := values[receiver.Secret]
	if secret == "" {
		return true
	}
	switch receiver.Scheme {
	case config.SchemeStandard:
		return verifyStandardWebhook(secret, h, body, r.now())
	case config.SchemeHMAC:
		return verifyHMAC(secret, h, body, receiver, r.now())
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

// verifyHMAC verifies an HMAC-SHA256 signature of the body (or a timestamped
// form of it) against the receiver's declared signing config (ADR-0049). The
// signature header, digest encoding, and an optional timestamp header and
// version prefix are all receiver config, so a raw-body scheme (hex and
// prefixed, or plain base64) and a timestamped, replay-bounded scheme are all
// config entries, not separate mechanisms.
func verifyHMAC(secret string, h http.Header, body []byte, r *config.WebhookReceiver, now time.Time) bool {
	header := r.Header
	if header == "" {
		header = "x-servitor-signature"
	}
	got := h.Get(header)
	if got == "" {
		return false
	}
	// Strip a version prefix the sender prepends to the digest, for example
	// "sha256=" or "v0=".
	if r.Prefix != "" {
		got = strings.TrimPrefix(got, r.Prefix+"=")
	}

	// The message is the raw body, or `<prefix>:<timestamp>:<body>` when the
	// receiver declares a timestamp header (a replay-bounded scheme).
	message := body
	if r.TimestampHeader != "" {
		tsRaw := h.Get(r.TimestampHeader)
		if tsRaw == "" {
			return false
		}
		ts, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil {
			return false
		}
		if delta := now.Unix() - ts; delta < -300 || delta > 300 {
			return false
		}
		var prefix string
		if r.Prefix != "" {
			prefix = r.Prefix + ":"
		}
		message = []byte(prefix + tsRaw + ":" + string(body))
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)

	encoding := r.Encoding
	if encoding == "" {
		encoding = "base64"
	}
	switch encoding {
	case "hex":
		return hmac.Equal([]byte(got), []byte(hex.EncodeToString(digest)))
	case "base64":
		return hmac.Equal([]byte(got), []byte(base64.StdEncoding.EncodeToString(digest)))
	default:
		return false
	}
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

// Completed fires a registered, enabled workflow whose `completed` trigger
// names the workflow that just completed (SPEC: `completed` trigger). The event
// passed to the downstream run records which workflow and run completed. A
// workflow whose `completed` trigger names another workflow is left untouched.
func (r *Receiver) Completed(completedWorkflow, completedRun string) error {
	workflows, err := r.store.ListWorkflows()
	if err != nil {
		return fmt.Errorf("trigger: completed list workflows: %w", err)
	}
	for _, wf := range workflows {
		if !wf.Enabled {
			continue
		}
		w, perr := wafer.Parse([]byte(wf.Wafer))
		if perr != nil {
			continue
		}
		for _, tr := range w.On {
			if tr.Type != "completed" {
				continue
			}
			upstream, _ := tr.Config["workflow"].(string)
			if upstream != completedWorkflow {
				continue
			}
			event := map[string]any{
				"trigger":  "completed",
				"from":     completedWorkflow,
				"from_run": completedRun,
			}
			runID := fmt.Sprintf("%s-completed-%d", w.Name, r.now().UnixNano())
			if _, err := runner.StartRun(r.store, r.queue, w, event, runID); err != nil {
				return fmt.Errorf("trigger: completed %q: %w", w.Name, err)
			}
		}
	}
	return nil
}

// Failed fires a registered, enabled workflow whose `failed` trigger names the
// workflow that just failed (ADR-0039). It is the distinct signal for a failed
// run (for example a failed secret), so the operator can wire a notification
// to it; it is separate from the `completed` trigger, which stays
// success-completion-only. The event passed to the downstream run records
// which workflow and run failed.
func (r *Receiver) Failed(failedWorkflow, failedRun string) error {
	workflows, err := r.store.ListWorkflows()
	if err != nil {
		return fmt.Errorf("trigger: failed list workflows: %w", err)
	}
	for _, wf := range workflows {
		if !wf.Enabled {
			continue
		}
		w, perr := wafer.Parse([]byte(wf.Wafer))
		if perr != nil {
			continue
		}
		for _, tr := range w.On {
			if tr.Type != "failed" {
				continue
			}
			upstream, _ := tr.Config["workflow"].(string)
			if upstream != failedWorkflow {
				continue
			}
			event := map[string]any{
				"trigger":  "failed",
				"from":     failedWorkflow,
				"from_run": failedRun,
			}
			runID := fmt.Sprintf("%s-failed-%d", w.Name, r.now().UnixNano())
			if _, err := runner.StartRun(r.store, r.queue, w, event, runID); err != nil {
				return fmt.Errorf("trigger: failed %q: %w", w.Name, err)
			}
		}
	}
	return nil
}

// Polled fires a run of the given workflow for each item a `poll` returned
// (ADR-0027), dispatching on the poll kind to build each run's event. For
// "email", each item is an email and becomes `event.subject`, `event.from`, and
// so on. It does nothing when the workflow is not registered or is disabled.
func (r *Receiver) Polled(workflowID, kind string, items []any) error {
	wf, err := r.store.GetWorkflow(workflowID)
	if err != nil {
		return fmt.Errorf("trigger: poll get workflow %q: %w", workflowID, err)
	}
	if wf == nil || !wf.Enabled {
		return nil
	}
	w, perr := wafer.Parse([]byte(wf.Wafer))
	if perr != nil {
		return fmt.Errorf("trigger: poll workflow %q: %w", workflowID, perr)
	}
	for _, item := range items {
		event, err := pollEvent(kind, item)
		if err != nil {
			return err
		}
		runID := fmt.Sprintf("%s-poll-%d", w.Name, r.now().UnixNano())
		if _, err := runner.StartRun(r.store, r.queue, w, event, runID); err != nil {
			return fmt.Errorf("trigger: poll %q: %w", w.Name, err)
		}
	}
	return nil
}

// pollEvent builds a run's event payload from a poll item of the given kind.
func pollEvent(kind string, item any) (map[string]any, error) {
	switch kind {
	case "email":
		e := email.Email{}
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("trigger: poll email encode: %w", err)
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("trigger: poll email decode: %w", err)
		}
		return map[string]any{
			"trigger":    "email_received",
			"from":       e.From,
			"to":         e.To,
			"subject":    e.Subject,
			"body":       e.Body,
			"date":       e.Date,
			"message_id": e.MessageID,
		}, nil
	default:
		return map[string]any{"trigger": kind, "item": item}, nil
	}
}

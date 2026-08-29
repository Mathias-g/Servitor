package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mathias-g/Servitor/internal/expression"
	"github.com/Mathias-g/Servitor/internal/honker"
)

// waitContinuation is the worker-serialized payload of a parked run (ADR-0040).
// It carries the wait node's identity, the `{event, steps}` input it parked
// with, and its whole downstream sub-DAG, so a resume can thread the wait's
// result into the continuation and re-enqueue the frontier. It is stored as
// opaque bytes in the honker continuation row.
type waitContinuation struct {
	RunID      string
	NodeID     string
	NodeName   string
	Input      map[string]any
	Dependents []string
	Downstream []NodeJob
}

// handleWait parks a run at a `wait` node (ADR-0041, ADR-0042). It resolves the
// node's effective signal name and timer resume time, then, in one transaction:
//
//   - If a buffered signal is already waiting for this name (a signal that
//     arrived before the run parked), it is consumed and the wait resumes
//     immediately with `{source: "signal", payload}` instead of parking.
//   - Otherwise it writes the run's continuation, sets the run `waiting`, acks
//     the wait job's claim, and enqueues the one-shot timer resume job (if any).
//
// The run is not completed while `waiting` (checkRunComplete guards on that).
func (w *Worker) handleWait(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	signalName, err := resolveSignalName(sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: wait %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	runAt, err := resolveTimerRunAt(sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: wait %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}

	wc := waitContinuation{
		RunID:      sj.RunID,
		NodeID:     sj.NodeID,
		NodeName:   sj.NodeName,
		Input:      sj.Input,
		Dependents: sj.Dependents,
		Downstream: sj.Downstream,
	}
	payload, _ := json.Marshal(wc)

	err = w.store.WithTx(func(tx *honker.Tx) error {
		if signalName != "" {
			buffered, found, berr := tx.TakeBufferedSignal(signalName)
			if berr != nil {
				return berr
			}
			if found {
				// A signal arrived before the run parked; resume immediately
				// rather than parking (ADR-0042 race rule).
				return w.completeWait(tx, sj, claimed, wc, "signal", buffered)
			}
		}
		if err := tx.WriteContinuation(honker.Continuation{
			RunID:      sj.RunID,
			WorkflowID: sj.WorkflowID,
			SignalName: signalName,
			RunAt:      runAt,
			Payload:    payload,
		}); err != nil {
			return err
		}
		if err := tx.SetRunStatusTx(sj.RunID, honker.RunWaiting); err != nil {
			return err
		}
		if claimed != nil {
			if err := tx.Ack(claimed); err != nil {
				return err
			}
			if err := tx.AdjustPending(sj.RunID, -1); err != nil {
				return err
			}
		}
		if runAt != 0 {
			// One-shot timer resume job, claimable at runAt (ADR-0043).
			if err := tx.EnqueueAt(w.queue, NodeJob{NodeType: "resume", RunID: sj.RunID}, runAt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: wait %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// completeWait finishes a `wait` node inside a transaction with the given
// result: it writes the wait's result row, fans out its downstream (threading
// the result into the continuation's input), and adjusts pending by the net
// change (downstream enqueued minus the wait job's ack). It is used both by the
// buffered-signal immediate-resume path and (via resumeRun) by a later resume.
func (w *Worker) completeWait(tx *honker.Tx, sj NodeJob, claimed *honker.Job, wc waitContinuation, source string, payload any) error {
	result := map[string]any{"source": source, "payload": payload}
	if err := tx.WriteResult(wc.RunID, wc.NodeID, result); err != nil {
		return err
	}
	n, err := fanOutContinuation(tx, w.queue, wc, result)
	if err != nil {
		return err
	}
	if claimed != nil {
		if err := tx.Ack(claimed); err != nil {
			return err
		}
		n--
	}
	return tx.AdjustPending(wc.RunID, n)
}

// fanOutContinuation threads the wait's result into each downstream job's
// `{event, steps}` input and performs the dependency fan-out inside the given
// transaction, returning the number of jobs enqueued (ADR-0023, ADR-0040).
func fanOutContinuation(tx *honker.Tx, queue *honker.Queue, wc waitContinuation, result any) (int, error) {
	ds := make([]honker.Downstream, 0, len(wc.Downstream))
	for i := range wc.Downstream {
		d := wc.Downstream[i]
		d.Input = threadInput(wc.Input, wc.NodeName, result)
		ds = append(ds, honker.Downstream{Queue: queue, Payload: d})
	}
	return tx.FanOut(wc.RunID, wc.Dependents, ds)
}

// resolveSignalName evaluates the wait node's `signal` JSONata expression
// against its `{event, steps}` input to get the effective signal name
// (ADR-0042). It returns "" when the wait has no signal source.
func resolveSignalName(sj NodeJob) (string, error) {
	expr, _ := sj.Config["signal"].(string)
	if expr == "" {
		return "", nil
	}
	v, err := expression.Eval(expr, sj.Input)
	if err != nil {
		return "", fmt.Errorf("wait %s: resolve signal: %w", sj.NodeID, err)
	}
	return stringify(v), nil
}

// resolveTimerRunAt computes the absolute unix epoch the wait's timer resumes
// the run, or 0 when the wait has no timer source (ADR-0043). `timer.after` is a
// duration resolved to now+duration; `timer.at` is an absolute time used
// directly.
func resolveTimerRunAt(sj NodeJob) (int64, error) {
	timer, ok := sj.Config["timer"].(map[string]any)
	if !ok || timer == nil {
		return 0, nil
	}
	if after, ok := timer["after"].(string); ok && after != "" {
		d, err := time.ParseDuration(after)
		if err != nil {
			return 0, fmt.Errorf("wait %s: timer.after: %w", sj.NodeID, err)
		}
		return time.Now().Add(d).Unix(), nil
	}
	if at, ok := timer["at"].(string); ok && at != "" {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return 0, fmt.Errorf("wait %s: timer.at: %w", sj.NodeID, err)
		}
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("wait %s: timer requires `after` or `at`", sj.NodeID)
}

// handleSendSignal wakes a parked run in (typically another) workflow by named
// signal (ADR-0042). It resolves the signal name and optional payload JSONata
// expressions against its own input, then delivers the signal, which resumes
// the parked run or buffers the signal. It then completes as a normal node,
// threading a trivial result forward. An ambiguous signal (more than one run
// parked on the name) is a failure, so the author sees their name is not unique.
func (w *Worker) handleSendSignal(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	expr, _ := sj.Config["signal"].(string)
	if expr == "" {
		w.recordFailure(sj, claimed, fmt.Errorf("send-signal %s: requires a `signal` expression", sj.NodeID))
		return fmt.Errorf("worker: send-signal %s (job %d): no signal expression", sj.NodeID, claimed.ID)
	}
	v, err := expression.Eval(expr, sj.Input)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: send-signal %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	name := stringify(v)

	var payload any
	if pExpr, _ := sj.Config["payload"].(string); pExpr != "" {
		p, perr := expression.Eval(pExpr, sj.Input)
		if perr != nil {
			w.recordFailure(sj, claimed, perr)
			return fmt.Errorf("worker: send-signal %s (job %d): payload: %w", sj.NodeID, claimed.ID, perr)
		}
		payload = p
	}

	if err := ResumeBySignal(w.store, w.queue, name, payload); err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: send-signal %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}

	atom := honker.NodeAtom{
		RunID:      sj.RunID,
		NodeID:     sj.NodeID,
		Result:     map[string]any{"ok": true, "signal": name},
		Job:        claimed,
		Dependents: sj.Dependents,
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		d.Input = threadInput(sj.Input, sj.NodeName, map[string]any{"ok": true, "signal": name})
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitNodeAtom(atom); err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: commit send-signal %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// ResumeBySignal delivers a named signal to the parked run parked on that name
// (ADR-0042). If exactly one run is parked on the name it resumes that run with
// the payload; if none, it buffers the signal so a later `wait` park consumes
// it; if more than one, it rejects the signal as ambiguous (an authoring bug).
func ResumeBySignal(store *honker.Store, queue *honker.Queue, name string, payload any) error {
	runs, err := store.ParkedRunsForSignal(name)
	if err != nil {
		return err
	}
	switch len(runs) {
	case 0:
		return store.BufferSignal(name, payload)
	case 1:
		return resumeRun(store, queue, runs[0], "signal", payload)
	default:
		return fmt.Errorf("signal %q is ambiguous: %d runs are parked on it", name, len(runs))
	}
}

// resumeRun resumes a parked run with the given result source and payload
// (ADR-0040, ADR-0042). It is a no-op if the run is not parked (status not
// `waiting`, or no continuation), which is what makes a repeated resume safe
// and what makes a stale timer fire harmless. It completes the wait node with
// `{source, payload}` inside one transaction.
func resumeRun(store *honker.Store, queue *honker.Queue, runID, source string, payload any) error {
	st, err := store.RunStatus(runID)
	if err != nil || st != honker.RunWaiting {
		return nil
	}
	cont, err := store.GetContinuation(runID)
	if err != nil || cont == nil {
		return nil
	}
	var wc waitContinuation
	if err := json.Unmarshal(cont.Payload, &wc); err != nil {
		return fmt.Errorf("worker: resume %s: decode continuation: %w", runID, err)
	}
	result := map[string]any{"source": source, "payload": payload}
	return store.WithTx(func(tx *honker.Tx) error {
		if err := tx.WriteResult(runID, wc.NodeID, result); err != nil {
			return err
		}
		n, err := fanOutContinuation(tx, queue, wc, result)
		if err != nil {
			return err
		}
		if err := tx.AdjustPending(runID, n); err != nil {
			return err
		}
		if err := tx.SetRunStatusTx(runID, honker.RunRunning); err != nil {
			return err
		}
		return tx.DeleteContinuation(runID)
	})
}

// stringify renders a JSONata result as a string, so a signal name can be any
// expression result (a string is used as-is; anything else is its JSON form).
func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

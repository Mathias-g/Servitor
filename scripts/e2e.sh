#!/usr/bin/env bash
# e2e: build the binary, boot the real daemon against a scratch SQLite file,
# submit, enable, and trigger workflows that exercise the subprocess dispatch
# (shell and transform nodes), and assert the runs complete with the expected
# results. This verifies the moved dispatch path works end to end, not just in
# unit tests.
#
# Requires HONKER_EXTENSION_PATH (ADR-0011), like `make test`. It uses a
# scratch working dir under TMPDIR and a scratch daemon port so it never
# touches a real deployment. It tears the daemon down on exit.
#
# Usage: scripts/e2e.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"

: "${HONKER_EXTENSION_PATH:?HONKER_EXTENSION_PATH must be set to a loadable libhonker_ext.so (see AGENTS.md / ADR-0011)}"
if [ ! -r "$HONKER_EXTENSION_PATH" ]; then
  echo "error: HONKER_EXTENSION_PATH $HONKER_EXTENSION_PATH is not readable" >&2
  exit 1
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/servitor-e2e.XXXXXX")"
db="$work/servitor.db"
daemon_log="$work/daemon.log"
addr="127.0.0.1:${E2E_PORT:-7365}"

cleanup() {
  # Stop the daemon (best-effort) and remove the scratch dir.
  "$root/bin/servitor" stop --addr "$addr" >/dev/null 2>&1 || true
  sleep 1
  rm -rf "$work"
}
trap cleanup EXIT

echo "==> building"
(cd "$root" && make build >/dev/null)
sv="$root/bin/servitor"

echo "==> booting daemon on $addr"
"$sv" run --addr "$addr" --db "$db" >"$daemon_log" 2>&1 &
sleep 2
if ! grep -q "daemon listening" "$daemon_log"; then
  echo "error: daemon did not start; log:" >&2
  cat "$daemon_log" >&2
  exit 1
fi

fail() {
  echo "e2e FAILED: $1" >&2
  exit 1
}

# Workflow 1: transform -> switch -> shell. The switch routes on the transform
# result to a shell leaf; this exercises the registry-dispatched subprocess
# path for transform, switch, and shell, including a switch whose chosen branch
# is a leaf (ADR-0023).
wf1="$work/wf1.yml"
cat >"$wf1" <<'YAML'
name: e2e_check
triggers:
  - type: manual
nodes:
  - type: transform
    name: normalize
    expression: '{"value": 42}'
  - type: switch
    name: route
    depends_on: [normalize]
    expression: "steps.normalize.value > 10 ? 'high' : 'low'"
    cases:
      high: report
      low: report
  - type: shell
    name: report
    depends_on: [route]
    command: "printf '{\"ok\":true,\"from\":\"shell\"}'"
YAML

# Workflow 2: shell -> transform. Confirms a transform reads a prior shell
# node's result through the threaded `steps` input.
wf2="$work/wf2.yml"
cat >"$wf2" <<'YAML'
name: e2e_check2
triggers:
  - type: manual
nodes:
  - type: shell
    name: hello
    command: "printf '{\"ok\":true,\"from\":\"shell\"}'"
  - type: transform
    name: t2
    depends_on: [hello]
    expression: '{"echoed": steps.hello.ok, "from": "transform"}'
YAML

run_workflow() {
  local file="$1" name="$2"
  echo "==> submitting $name" >&2
  "$sv" submit --addr "$addr" "$file" >/dev/null || fail "submit $name"
  "$sv" enable --addr "$addr" "$name" >/dev/null || fail "enable $name"
  "$sv" trigger --addr "$addr" "$name" >/dev/null || fail "trigger $name"
  # Poll for the run to appear and complete (the daemon may still be
  # registering the workflow right after boot). The workflow name is passed to
  # python via the environment so it is never interpolated into the program.
  local run_id="" tries=0
  while [ -z "$run_id" ] && [ "$tries" -lt 20 ]; do
    run_id="$(export E2E_WF="$name"; "$sv" runs --addr "$addr" | python3 -c '
import sys, json, os
name = os.environ["E2E_WF"]
rs = json.load(sys.stdin)
done = [r["ID"] for r in rs if r.get("WorkflowName") == name and r.get("Status") == "completed"]
print(done[0] if done else "")
')"
    [ -n "$run_id" ] || { sleep 1; tries=$((tries+1)); }
  done
  [ -n "$run_id" ] || fail "no completed run found for $name"
  "$sv" run --addr "$addr" "$run_id"
}

echo "==> running workflow 1 (transform->switch->shell)"
out1="$(run_workflow "$wf1" e2e_check)"
echo "$out1" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert d["run"]["Status"] == "completed", "run1 status " + d["run"]["Status"]
by = {n["NodeID"]: n["Result"] for n in d["nodes"]}
assert "normalize" in by, "run1 missing transform node: " + repr(by)
assert "route" in by, "run1 missing switch node: " + repr(by)
assert "report" in by and by["report"] != "", "run1 missing shell leaf: " + repr(by)
assert "\"ok\":true" in by["report"], "run1 shell result wrong: " + repr(by["report"])
print("  ok: transform -> switch -> shell leaf, run completed")
' || fail "workflow 1 assertion"

echo "==> running workflow 2 (shell->transform)"
out2="$(run_workflow "$wf2" e2e_check2)"
echo "$out2" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert d["run"]["Status"] == "completed", "run2 status " + d["run"]["Status"]
by = {n["NodeID"]: n["Result"] for n in d["nodes"]}
assert "hello" in by, "run2 missing shell node: " + repr(by)
assert "t2" in by, "run2 missing transform node: " + repr(by)
assert "\"ok\":true" in by["hello"], "run2 shell result wrong: " + repr(by["hello"])
assert "\"echoed\":true" in by["t2"], "run2 transform result wrong: " + repr(by["t2"])
print("  ok: shell -> transform threaded input, run completed")
' || fail "workflow 2 assertion"

echo "==> e2e PASSED"

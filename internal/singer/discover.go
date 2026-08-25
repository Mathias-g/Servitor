package singer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Stream is one entry of a tap's catalog, in the same shape the `catalog`
// field of a `singer-tap` step accepts (SPEC: Singer, self-describing
// schemas). An agent copies the entries it wants from capabilities into the
// Wafer, so what it sees is what runs; each entry is marked selected so a
// pasted entry syncs its stream.
type Stream struct {
	Stream      string         `json:"stream"`
	TapStreamID string         `json:"tap_stream_id"`
	Schema      map[string]any `json:"schema"`
	Metadata    []any          `json:"metadata"`
}

// DiscoveredTap is an installed tap discovered from PATH, with its config
// schema (from --about) and catalog (from --discover). A discovery that fails
// records the error rather than failing the whole report, so `capabilities`
// still works when a tap is broken (SPEC: How an agent discovers integrations).
type DiscoveredTap struct {
	Name        string         `json:"name"`
	Config      map[string]any `json:"config,omitempty"`
	Catalog     []Stream       `json:"catalog,omitempty"`
	AboutErr    string         `json:"about_error,omitempty"`
	DiscoverErr string         `json:"discover_error,omitempty"`
}

// DiscoverTaps enumerates the `tap-*` executables on PATH and discovers each
// one's config schema and streams by calling `--about` and `--discover`. This
// is invoked during a capabilities refresh, not per step execution (SPEC:
// Capability discovery).
func DiscoverTaps() ([]DiscoveredTap, error) {
	names := tapsOnPath()
	taps := make([]DiscoveredTap, 0, len(names))
	for _, name := range names {
		taps = append(taps, discover(name))
	}
	return taps, nil
}

// tapsOnPath returns the executable names on PATH that look like Singer taps
// (`tap-*`), sorted.
func tapsOnPath() []string {
	var found []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "tap-") {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			if !seen[e.Name()] {
				seen[e.Name()] = true
				found = append(found, e.Name())
			}
		}
	}
	sort.Strings(found)
	return found
}

// discover calls --about and --discover on one tap and returns its schema and
// streams. Failures are captured per-step rather than returned, so one broken
// tap does not hide the rest.
func discover(name string) DiscoveredTap {
	t := DiscoveredTap{Name: name}
	if out, err := exec.Command(name, "--about").Output(); err != nil {
		t.AboutErr = err.Error()
	} else if cfg, uerr := unmarshalObject(out); uerr == nil {
		t.Config = cfg
	}
	if out, err := exec.Command(name, "--discover").Output(); err != nil {
		t.DiscoverErr = err.Error()
	} else {
		t.Catalog = parseCatalog(out)
	}
	return t
}

// unmarshalObject parses JSON into a map, returning nil on failure.
func unmarshalObject(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	return m, nil
}

// parseCatalog parses a tap's `--discover` output, the Singer catalog, into
// copy-ready stream entries. The catalog is either a JSON array of stream
// objects or an object with a `streams` array. Each entry is marked selected so
// an agent can paste it into a Wafer `catalog` unchanged.
func parseCatalog(b []byte) []Stream {
	var raw json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}

	var list []json.RawMessage
	var asObj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		if oerr := json.Unmarshal(raw, &asObj); oerr != nil {
			return nil
		}
		_ = json.Unmarshal(asObj["streams"], &list)
	}

	var out []Stream
	for _, item := range list {
		var rawStream map[string]any
		if err := json.Unmarshal(item, &rawStream); err != nil {
			continue
		}
		name, _ := rawStream["stream"].(string)
		if name == "" {
			continue
		}
		st := Stream{
			Stream:      name,
			TapStreamID: name,
			Metadata: []any{
				map[string]any{"breadcrumb": []any{}, "metadata": map[string]any{"selected": true}},
			},
		}
		if id, ok := rawStream["tap_stream_id"].(string); ok && id != "" {
			st.TapStreamID = id
		}
		if schema, ok := rawStream["schema"].(map[string]any); ok {
			st.Schema = schema
		}
		out = append(out, st)
	}
	return out
}

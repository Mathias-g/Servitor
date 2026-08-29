package secret

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
)

// varlockSource is the source name for the varlock pull provider.
const varlockSource = "varlock"

// VarlockProvider is a pull provider that resolves secrets from varlock
// (ADR-0034): it loads the resolved set once into an in-memory local copy, then
// serves each value from that copy on demand. It is not the recommended default
// mechanism (SPEC: Secret resolution), but it is the slow external store that a
// deployment that already uses varlock can keep as one pull source. Loading
// once and serving from the local copy keeps per-node resolution in-process and
// fast, which a per-node `varlock` CLI boot (~290ms) could not do (ADR-0032).
type VarlockProvider struct {
	once sync.Once
	vals map[string]string
	err  error
}

// Resolve returns secretName's value from the local copy, loading it from
// varlock on the first call. A load failure is an unreachable source; a name
// with no value is missing.
func (p *VarlockProvider) Resolve(_ context.Context, _, secretName string) (string, error) {
	p.once.Do(p.load)
	if p.err != nil {
		return "", fmt.Errorf("%w: %v", ErrSourceUnreachable, p.err)
	}
	v, ok := p.vals[secretName]
	if !ok || v == "" {
		return "", fmt.Errorf("%w: %s", ErrSecretMissing, secretName)
	}
	return v, nil
}

func (p *VarlockProvider) load() {
	out, err := exec.Command("varlock", "load", "--format", "json-full").Output()
	if err != nil {
		p.err = err
		return
	}
	var full struct {
		Config map[string]struct {
			Value string `json:"value"`
		} `json:"config"`
	}
	if uerr := json.Unmarshal(out, &full); uerr != nil {
		p.err = uerr
		return
	}
	p.vals = map[string]string{}
	for name, c := range full.Config {
		if c.Value != "" {
			p.vals[name] = c.Value
		}
	}
}

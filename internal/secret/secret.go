// Package secret implements the pluggable secret-resolution model (SPEC:
// Secret resolution, ADR-0032, ADR-0033, ADR-0035). A Provider resolves a
// secret value on demand, per node, in-process. A Resolver routes a secret
// name to its provider through the declared source (ADR-0035), so that
// per-node delivery hands each node's value only to that node's subprocess and
// holds nothing past it.
//
// The three failure semantics are distinct because they are handled
// differently (SPEC: Secret invalidity and rotation): a source being
// unreachable is transient, a secret being missing is not, and a secret being
// stale or invalid triggers a bounded retry with a fresh resolve.
package secret

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// The failure semantics a provider can return. A provider wraps one of these
// with %w so callers can classify the failure without matching on message
// text.
var (
	// ErrSourceUnreachable means the backing store or provider could not be
	// reached. Transient: retry with backoff.
	ErrSourceUnreachable = errors.New("secret source unreachable")
	// ErrSecretMissing means the secret is declared but has no value in the
	// store. Not transient: fail fast.
	ErrSecretMissing = errors.New("secret missing")
	// ErrStale means the value is stale or invalid. Retry with a fresh resolve.
	ErrStale = errors.New("secret stale")
	// ErrUndeclared means a secret name has no declared source. A Wafer that
	// references it cannot run (ADR-0035).
	ErrUndeclared = errors.New("secret not declared")
)

// Provider resolves a secret's value on demand. It is in-process and per-node
// so that per-node resolution is milliseconds, not a per-node CLI boot
// (ADR-0032). Caching and expiry are provider properties, not part of the
// contract.
type Provider interface {
	// Resolve returns the value of secretName for nodeName. It wraps
	// ErrSourceUnreachable, ErrSecretMissing, or ErrStale to classify failure.
	Resolve(ctx context.Context, nodeName, secretName string) (string, error)
}

// Registry holds the providers available on this box, keyed by source name
// (the value a secret's `source` field names; ADR-0035, ADR-0036).
type Registry struct {
	providers map[string]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider under the given source name. A later registration
// with the same name replaces the earlier one.
func (r *Registry) Register(source string, p Provider) {
	r.providers[source] = p
}

// Provider returns the provider registered for source, or nil.
func (r *Registry) Provider(source string) Provider {
	return r.providers[source]
}

// SourceNames returns the registered source names, sorted.
func (r *Registry) SourceNames() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Resolver routes a secret name to its provider via the declared source and
// resolves per node. It is what the runner hands a node's declared secrets to.
type Resolver struct {
	providers map[string]Provider
	sources   map[string]string // secret name -> source
}

// NewResolver builds a Resolver that routes each secret name to the provider
// registered under that name's declared source. A secret with no entry in
// sources is undeclared and fails resolution with ErrUndeclared.
func NewResolver(reg *Registry, sources map[string]string) *Resolver {
	providers := map[string]Provider{}
	if reg != nil {
		providers = reg.providers
	}
	if sources == nil {
		sources = map[string]string{}
	}
	return &Resolver{providers: providers, sources: sources}
}

// Resolve resolves each of names for nodeName, routing each to its declared
// source's provider. It returns the resolvable values as name -> value, the
// names whose provider found no value (missing), and the first non-missing
// error (an undeclared secret, an unreachable source, or a stale secret), if
// any. A name is reported missing when its provider returns ErrSecretMissing
// (or an empty value); those are not fatal here, so the caller can fail fast
// with the same message shape as today.
func (r *Resolver) Resolve(ctx context.Context, nodeName string, names []string) (values map[string]string, missing []string, err error) {
	values = map[string]string{}
	for _, name := range names {
		source, ok := r.sources[name]
		if !ok {
			return nil, missing, fmt.Errorf("%w: %s", ErrUndeclared, name)
		}
		prov, ok := r.providers[source]
		if !ok {
			return nil, missing, fmt.Errorf("%w: %s (no provider for source %q)", ErrSourceUnreachable, name, source)
		}
		v, rerr := prov.Resolve(ctx, nodeName, name)
		switch {
		case rerr == nil:
			if v == "" {
				missing = append(missing, name)
				continue
			}
			values[name] = v
		case errors.Is(rerr, ErrSecretMissing):
			missing = append(missing, name)
		default:
			return nil, missing, rerr
		}
	}
	return values, missing, nil
}

// Declared reports whether name has a declared source (ADR-0035). A secret a
// Wafer references but that has no declared source is undeclared and must
// refuse to submit or run.
func (r *Resolver) Declared(name string) bool {
	_, ok := r.sources[name]
	return ok
}

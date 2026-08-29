package secret

import (
	"context"
	"fmt"
)

// MapProvider resolves each secret from a fixed name -> value map. It is a test
// helper and a convenient stand-in for a real provider.
type MapProvider map[string]string

// Resolve returns the value for secretName from the map.
func (m MapProvider) Resolve(_ context.Context, _, secretName string) (string, error) {
	if v, ok := m[secretName]; ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: %s", ErrSecretMissing, secretName)
}

// ResolverFromMap builds a Resolver that routes each of m's keys to a
// MapProvider holding m. It is a test helper; a secret name not in m is
// undeclared.
func ResolverFromMap(m map[string]string) *Resolver {
	if m == nil {
		m = map[string]string{}
	}
	reg := NewRegistry()
	reg.Register("test", MapProvider(m))
	sources := map[string]string{}
	for name := range m {
		sources[name] = "test"
	}
	return NewResolver(reg, sources)
}

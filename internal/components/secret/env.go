package secret

import (
	"context"
	"fmt"
	"os"
)

// envSource is the source name for the plain-environment provider.
const envSource = "env"

// EnvProvider resolves a secret from the process environment (SPEC: Secret
// resolution, "plain environment (a dev/testing fallback)"). It is the
// simplest provider: the operator exports the value and the node sees it. It
// returns ErrSecretMissing when the variable is unset or empty.
type EnvProvider struct{}

// Resolve returns the value of secretName from the process environment.
func (EnvProvider) Resolve(_ context.Context, _, secretName string) (string, error) {
	if v := os.Getenv(secretName); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: %s", ErrSecretMissing, secretName)
}

// Package hmacwebhook_test pins the deletion property for the webhook group
// (ADR-0045, ADR-0049): a capability is present only when its mechanism's
// package is imported. This test imports the two webhook mechanism packages
// (not the aggregator), so it proves importing them contributes exactly their
// own capabilities and nothing else. The per-service receiver types are gone;
// only the two verification-scheme mechanisms may exist (ADR-0049).
package hmacwebhook_test

import (
	"testing"

	"github.com/Mathias-g/Servitor/internal/registry"
	_ "github.com/Mathias-g/Servitor/internal/registry/webhook/hmacwebhook"
	_ "github.com/Mathias-g/Servitor/internal/registry/webhook/standardwebhook"
)

func TestWebhookMechanismsPresentBecausePackagesImported(t *testing.T) {
	if registry.LookupTrigger("hmac-webhook") == nil {
		t.Fatal("hmac-webhook should be present because its package is imported")
	}
	if registry.LookupTrigger("standard-webhook") == nil {
		t.Fatal("standard-webhook should be present because its package is imported")
	}
}

func TestUnimportedMechanismsAbsent(t *testing.T) {
	// The per-service receiver types were removed by ADR-0049. This is the
	// deletion property: no package registers them, so they must not exist.
	for _, name := range []string{"http_webhook", "standard_webhook", "github_webhook", "slack_event", "grist_webhook", "atomic_event"} {
		if registry.Lookup(name) != nil {
			t.Fatalf("%s is present without its package being imported; deletion has side effects", name)
		}
	}
}

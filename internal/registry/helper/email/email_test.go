// Package email_test pins the deletion property (ADR-0045): a capability is
// present only when its mechanism's package is imported. This test imports
// only the email package (not the aggregator), so it proves that importing a
// package contributes exactly its own capabilities and nothing else. Removing
// the package's registration (and its import) therefore removes the capability
// with no central reference left to edit.
package email_test

import (
	"testing"

	"github.com/Mathias-g/Servitor/internal/registry"
	_ "github.com/Mathias-g/Servitor/internal/registry/helper/email"
)

func TestEmailCapabilityPresentBecausePackageImported(t *testing.T) {
	if registry.LookupTrigger("email_received") == nil {
		t.Fatal("email_received should be present because the email package is imported")
	}
}

func TestUnimportedMechanismsAbsent(t *testing.T) {
	// core is not imported here, so none of its capabilities may exist. This is
	// the deletion property: not importing a mechanism's package means it is
	// gone from validation and capabilities.
	for _, name := range []string{"http", "shell", "switch", "cron", "mcp-call", "singer-tap", "github_webhook"} {
		if registry.Lookup(name) != nil {
			t.Fatalf("%s is present without its package being imported; deletion has side effects", name)
		}
	}
}

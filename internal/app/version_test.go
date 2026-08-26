package app

import "testing"

// TestVersionDefaultsToDev pins that a build without the ldflags override
// (for example `go build ./...`) reports "dev", so a developer or agent can
// tell a dev build apart from a release build. Release builds stamp VERSION via
// `make release` (see AGENTS.md).
func TestVersionDefaultsToDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("app.Version = %q, want the default \"dev\" in a plain test build", Version)
	}
}

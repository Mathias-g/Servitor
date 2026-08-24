package app

// Version is injected at build time via
//
//	-ldflags "-X github.com/Mathias-g/Servitor/internal/app.Version=$(cat VERSION)"
//
// It defaults to "dev" when built without the ldflags override (e.g. `go build ./...`).
var Version = "dev"

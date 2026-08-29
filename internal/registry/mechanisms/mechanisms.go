// Package mechanisms pulls in every mechanism group's self-registering packages
// so their init functions run and register into the central registry
// (ADR-0045). Consumers that need the full capability set import this package
// for its side effect; they never name a mechanism directly. Removing a
// mechanism's package means also removing its blank import here, after which
// the mechanism no longer exists.
package mechanisms

import (
	_ "github.com/Mathias-g/Servitor/internal/registry/core"
	_ "github.com/Mathias-g/Servitor/internal/registry/helper/email"
	_ "github.com/Mathias-g/Servitor/internal/registry/mcp"
	_ "github.com/Mathias-g/Servitor/internal/registry/singer"
	_ "github.com/Mathias-g/Servitor/internal/registry/webhook"
)

// Package mechanisms pulls in every mechanism's self-registering package so
// its init function runs and registers into the central registry (ADR-0045,
// ADR-0048). Consumers that need the full capability set import this package
// for its side effect; they never name a mechanism directly. Removing a
// mechanism's folder means also removing its blank import here, after which
// the mechanism no longer exists.
package mechanisms

import (
	_ "github.com/Mathias-g/Servitor/internal/registry/core/completed"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/cron"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/failed"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/foreach"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/http"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/manual"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/rerunfailed"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/sendsignal"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/shell"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/switch"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/transform"
	_ "github.com/Mathias-g/Servitor/internal/registry/core/wait"
	_ "github.com/Mathias-g/Servitor/internal/registry/helper/email"
	_ "github.com/Mathias-g/Servitor/internal/registry/mcp/http"
	_ "github.com/Mathias-g/Servitor/internal/registry/mcp/stdio"
	_ "github.com/Mathias-g/Servitor/internal/registry/singer/tap"
	_ "github.com/Mathias-g/Servitor/internal/registry/singer/target"
	_ "github.com/Mathias-g/Servitor/internal/registry/webhook/hmacwebhook"
	_ "github.com/Mathias-g/Servitor/internal/registry/webhook/standardwebhook"
)

// Package components holds the shared building blocks that mechanisms and the
// engine compose (ADR-0046). Each subpackage is one reusable, mechanism-agnostic
// piece: subprocess isolation (exec), expression evaluation (expression), the
// record-streaming protocol (singer), the stdio tool protocol (mcp), and the
// secret provider + resolver (secret).
//
// # What belongs here, and what does not
//
// Three homes cover all of Servitor's code. Decide by asking, in order:
//
//  1. Is this Servitor's core engine: what the runner needs to run the DAG
//     regardless of which mechanisms exist (the worker loop, dispatch, the
//     durability store, the trigger receiver, the daemon, the CLI, Wafer
//     validation)? It lives at internal/ top level (honker, worker, runner,
//     trigger, daemon, cli, wafer, capabilities, protocol, app, integrations).
//     It is never deletable and never a mechanism.
//
//  2. Is this a specific mechanism: one capability an operator might not want,
//     self-registering into the registry? It lives in
//     internal/registry/<group>/<mechanism>/ and is a unit of deletion: remove
//     the folder and the capability disappears with no central reference.
//
//  3. Is this reusable machinery that more than one consumer composes, is
//     mechanism-agnostic, and is not itself a named capability? It is a shared
//     component and lives here, under internal/components/<name>/.
//
// # Invariants
//
//   - A component names no specific capability or mechanism. It is written in
//     terms of the thing it abstracts (a subprocess, a JSONata expression, a
//     record stream, a tool call, a secret source), never a product or a type
//     name in the registry.
//   - A component imports only other components and the standard library. It
//     never imports the registry, a mechanism package, or the engine (a check
//     that currently holds and must be preserved).
//   - Dependency points downstream: engine and mechanisms may import
//     components; components never import them.
//
// # Deletion rule
//
// A mechanism's folder is self-contained and deletable. A component is shared,
// so it is not deleted by removing one mechanism. If a component ends up with a
// single consumer, move it into that consumer (the mechanism folder, or the
// engine) rather than leaving it here: a shared component with one user is a
// speculative seam, which BSSN (ADR-0002) says to take away.
package components

# Servitor example: two linked workflows

Servitor workflows ("Wafers") are YAML with three parts:

- `triggers`: what starts a run
- `nodes`: the steps, as a dependency DAG
- `secrets`: per-node, resolved only when that node runs

Each node runs as an isolated subprocess and only sees the secrets it declared.
Side-effecting nodes carry a `dedupe_key` so a retry never redoes the side effect.

## Workflow 1: process an order

Starts when `order_received` finishes, or manually (triggers are OR'd).

```yaml
name: process_order
triggers:
  - type: completed
    workflow: order_received
  - type: manual

nodes:
  # A `transform` node: pure computation, no side effects.
  # It reshapes the order and pulls out what the rest needs.
  - type: transform
    name: normalize
    expression: >-
      {
        "id": steps.order_received.id,
        "total": $number(steps.order_received.total),
        "items": steps.order_received.items
      }

  # A `transform` acting as a condition/gate: it produces a value
  # the next node routes on. (Servitor has no separate condition
  # node; a transform that gates is the uniform way to do it.)
  - type: transform
    name: gate
    depends_on: [normalize]
    expression: "if (steps.normalize.total <= 0) then 'blocked' else 'ok'"

  # A `switch` routes to a named node based on a value.
  - type: switch
    name: route
    depends_on: [gate]
    expression: "steps.gate"
    cases:
      blocked: reject_order
      ok: charge_customer

  # The "ok" branch: charge the customer. A side-effecting `http`
  # node with its own secret and a dedupe_key (no double charge).
  - type: http
    name: charge_customer
    depends_on: [route]
    dedupe_key: "steps.normalize.id"
    url: "https://api.stripe.example.com/v1/charges"
    method: POST
    headers:
      Authorization: "Bearer $STRIPE_KEY"
    secrets: [STRIPE_KEY]
    body:
      amount: "steps.normalize.total"
      currency: usd

  # The "blocked" branch: reject the order. It ends here, no shipping.
  - type: shell
    name: reject_order
    depends_on: [route]
    command: "printf '{\"rejected\": true}'"

  # Only charged orders ship: foreach fans `ship_item` out per line item.
  - type: foreach
    name: fulfill
    depends_on: [charge_customer]
    over: "steps.normalize.items"
    as: item
    body: ship_item

  # The foreach body: an `mcp-call` node, one shipment per item.
  - type: mcp-call
    name: ship_item
    depends_on: [fulfill]
    server: inventory
    tool: create_shipment
    input:
      sku: "steps.item.sku"
      order_id: "steps.normalize.id"
    secrets: [INVENTORY_TOKEN]

  # The rejoin: `depends_on` both branch ends, so it runs no matter
  # which branch was taken. It carries ONLY common reporting, never a
  # branch-specific side effect. A rejected order was never shipped;
  # this just records the outcome either way.
  - type: shell
    name: record_outcome
    depends_on: [ship_item, reject_order]
    command: "printf '{\"logged\": true}'"
```

## Workflow 2: alert when workflow 1 fails

The `failed` trigger is distinct from `completed` (which fires on success), so
you get alerted on a bad secret without spamming on normal completions.

```yaml
name: order_alert
triggers:
  - type: failed
    workflow: process_order

nodes:
  - type: shell
    name: page_ops
    secrets: [OPS_WEBHOOK]
    command: "curl -s -X POST -H \"Authorization: Bearer $OPS_WEBHOOK\" https://hooks.example.com/ops -d '{\"text\":\"order pipeline failed\"}'"
```

## How they relate

`process_order` runs when `order_received` completes (`completed` trigger);
`order_alert` runs when `process_order` fails (`failed` trigger). That is how
Servitor chains workflows and handles failures: separate Wafers triggering on
each other's completion or failure, rather than one giant script. Each node's
secrets are resolved at the moment that node runs and never live in the daemon
beyond it, and side-effecting nodes use `dedupe_key` to stay safe on retries.

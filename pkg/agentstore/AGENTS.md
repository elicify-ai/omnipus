# pkg/agentstore — agent entity persistence

What it owns: Agent-specific records over `pkg/entity` and post-write cache notifications.
What it does not own: Agent-writable home files or per-message routing reads.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCreate_WriteThenVerify_RoundTrip$' -v -p 1 ./pkg/agentstore/`

## Pitfalls here

- Agent policy records must live under protected `entities/agents/`, not an agent-writable home tree. `store.go` records this boundary; `store_test.go::TestCreate_WriteThenVerify_RoundTrip` covers durable creation.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-054-entity-config-separation.md` (agent entity location and cache contract).

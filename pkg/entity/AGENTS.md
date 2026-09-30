# pkg/entity — per-entity JSON storage

What it owns: Generic create/get/list/update/delete operations for one JSON file per entity, with locking and atomic writes.
What it does not own: Agent-specific records or the in-memory agent registry (`pkg/agentstore`).

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCreate_RejectsDuplicateID$' -v -p 1 ./pkg/entity/`

## Pitfalls here

- Create must reject an existing identifier instead of replacing its record. `store_test.go::TestCreate_RejectsDuplicateID` checks this boundary.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-054-entity-config-separation.md` (entity/config separation and locking).

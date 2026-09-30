# pkg/migrate — import from other agent installations

What it owns: Migration dispatch and the OpenClaw source handler for importing configuration or workspace data.
What it does not own: Ordinary boot migration of Omnipus's own stored records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestMigrateInstanceGetCurrentHandlerNotFound$' -v -p 1 ./pkg/migrate/`

## Pitfalls here

- An unknown migration source must return a visible error, not silently choose a handler. `migrate_test.go::TestMigrateInstanceGetCurrentHandlerNotFound` covers this case.

## Never bring back

— none known

## Where decisions live

— none known

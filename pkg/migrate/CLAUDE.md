# pkg/migrate — import from other agent installations

What it owns: Migration dispatch and the OpenClaw source handler for importing configuration or workspace data.
What it does not own: Ordinary boot migration of Omnipus's own stored records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestMigrateInstanceGetCurrentHandlerNotFound$' -count=1 -v -p 1 ./pkg/migrate/`

## Pitfalls here

- An empty source defaults to `openclaw`. `migrate_test.go::TestMigrateInstanceGetCurrentHandlerNotFound` checks the visible error when that default handler is absent from an empty map; it does not test an explicit unknown source with a registered default handler. Require a named `--- PASS` for the missing-default case.

## Never bring back

— none known

## Where decisions live

— none known

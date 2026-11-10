# pkg/identity — sender identity matching

What it owns: Canonical `platform:id` identities and matching of legacy allow-list entries.
What it does not own: Connector transport or authorization-policy persistence.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestMatchAllowed$' -v -p 1 ./pkg/identity/`

## Pitfalls here

- A legacy username or numeric ID should match only its intended sender. `identity_test.go::TestMatchAllowed` exercises the supported allow-list formats.

## Never bring back

— none known

## Where decisions live

— none known

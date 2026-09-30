# pkg/vaultprops — knowledge query adapter

What it owns: Wiring schemas, note indexes and the properties index into `knowledge_find` and saved-view queries.
What it does not own: The underlying record types or the knowledge-note store; this package joins their dependencies.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson,records_no_sqlite -run '^TestF2_AttachmentCarveOut_WordMissMustRefuseNotAnswerZero$' -count=1 -v -p 1 ./pkg/vaultprops/`

## Pitfalls here

- An unavailable search source must refuse, not answer zero matches. The test above covers that distinction **only with `records_no_sqlite`**; without it, the test skips. Require a named `--- PASS`, not just a green exit or `--- SKIP`.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (Vault records: typed record layer).

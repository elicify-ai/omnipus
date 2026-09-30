# pkg/vaultprops — knowledge query adapter

What it owns: Wiring schemas, note indexes and the properties index into `knowledge_find` and saved-view queries.
What it does not own: The underlying record types or the knowledge-note store; this package joins their dependencies.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestF2_AttachmentCarveOut_WordMissMustRefuseNotAnswerZero$' -v -p 1 ./pkg/vaultprops/`

## Pitfalls here

- An unavailable search source must refuse, not answer zero matches. `TestF2_AttachmentCarveOut_WordMissMustRefuseNotAnswerZero` covers that distinction.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (Vault records: typed record layer).

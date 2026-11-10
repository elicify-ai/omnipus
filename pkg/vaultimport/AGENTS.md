# pkg/vaultimport — one-shot vault importer

What it owns: Inferring record schemas from existing vault note metadata and translating `.base` files into saved views.
What it does not own: A live query parser or an agent tool; imports are operator-initiated.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestInferTypes_TwoCandidates_IsAmbiguousAndWritesNothing$' -v -p 1 ./pkg/vaultimport/`

## Pitfalls here

- Guessing between two plausible property types would persist false schema facts. `typeinfer_test.go::TestInferTypes_TwoCandidates_IsAmbiguousAndWritesNothing` requires an explicit ambiguity instead.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (Vault records: typed record layer); `docs/internal/specs/vault-records-spec-2026-08-25.md`.

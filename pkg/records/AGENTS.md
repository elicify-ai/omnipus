# pkg/records — typed knowledge records

What it owns: Vault-defined record schemas, property values and validation.
What it does not own: Discovering or indexing notes; the knowledge and retrieval layers do that.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDecimal_NoBinaryFPGuardActuallyDetects$' -v -p 1 ./pkg/records/`

## Pitfalls here

- A decimal converted through binary floating point loses exact values. `decimal_no_float_test.go::TestDecimal_NoBinaryFPGuardActuallyDetects` probes the package-wide guard; preserve the original numeric spelling.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (Vault records: typed record layer); `docs/internal/specs/vault-records-spec-2026-08-25.md`.

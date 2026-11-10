# pkg/records — typed knowledge records

What it owns: Vault-defined record schemas, property values and validation.
What it does not own: Discovering or indexing notes; the knowledge and retrieval layers do that.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDecimal_NoBinaryFPTypesInThePackage$' -count=1 -v -p 1 ./pkg/records/`

## Pitfalls here

- Binary floats can lose decimal precision. `decimal_no_float_test.go::TestDecimal_NoBinaryFPTypesInThePackage` scans real package files; `TestDecimal_NoBinaryFPGuardActuallyDetects` checks only synthetic examples. The guard cannot see runtime values arriving through `any`, so preserve numeric spelling at input.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (Vault records: typed record layer); `docs/internal/specs/vault-records-spec-2026-08-25.md`.

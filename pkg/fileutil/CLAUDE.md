# pkg/fileutil — safe file-write and lock primitives

What it owns: Atomic file writes and shared filesystem lock helpers.
What it does not own: Entity schemas or package-specific persistence rules.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestWriteFileAtomic_Permissions$' -v -p 1 ./pkg/fileutil/`

## Pitfalls here

- Atomic writes must preserve the required file permission bits. `file_test.go::TestWriteFileAtomic_Permissions` checks the written mode.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-054-entity-config-separation.md` (sidecar locks for entity storage).

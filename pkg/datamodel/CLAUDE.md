# pkg/datamodel — data-directory bootstrap

What it owns: Initial directory permissions and first-run defaults for the file-based home layout.
What it does not own: Ongoing config reconciliation or the credentials store.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDirectoryInit_BackupsDirGuardedByEditionAuthMode$' -v -p 1 ./pkg/datamodel/`

## Pitfalls here

- Creating a local backup directory in other authentication modes risks placing key and backup together. `init_test.go::TestDirectoryInit_BackupsDirGuardedByEditionAuthMode` checks conditional creation.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-004-credential-boot-contract.md` (credential boot); root `CLAUDE.md` (storage constraints).

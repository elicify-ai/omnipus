# pkg/datamodel — data-directory bootstrap

What it owns: Initial directory permissions and first-run defaults for the file-based home layout.
What it does not own: Ongoing config reconciliation or the credentials store.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDirectoryInitialization$' -v -p 1 ./pkg/datamodel/`

## Pitfalls here

- Backup and Restore was removed. Initialization does not create a backups directory or delete existing archives. The filesystem-policy protections for existing archives stay in place; the gateway boot-control test pins preservation through startup and shutdown.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-004-credential-boot-contract.md` (credential boot); root `CLAUDE.md` (storage constraints).

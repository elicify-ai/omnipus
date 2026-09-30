# pkg/fspolicy — protected filesystem carve-outs

What it owns: App-level and kernel-ready deny paths for secrets and other agents' trees.
What it does not own: Kernel policy installation or agent tool-policy choices.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestIsCarveOut_BackupNotYetOnDiskIsCovered$' -v -p 1 ./pkg/fspolicy/`

## Pitfalls here

- A backup directory created after a turn starts must remain protected. `secret_backup_prefix_test.go::TestIsCarveOut_BackupNotYetOnDiskIsCovered` checks the path rule before creation.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-063-unified-file-access-engine-and-mounts.md` (app/kernel file-access boundaries).

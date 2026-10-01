# pkg/fspolicy — protected filesystem carve-outs

What it owns: App-level and kernel-ready deny paths for secrets and other agents' trees.
What it does not own: Kernel policy installation or agent tool-policy choices.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^(TestIsCarveOut_BackupNotYetOnDiskIsCovered|TestIsCarveOut_BackupWrittenMidTurnIsCovered|TestBuildCarveOuts_MergedSecretSet)$' -count=1 -v -p 1 ./pkg/fspolicy/`

## Pitfalls here

- Protect secret-copy filenames without relying on a directory listing: `TestIsCarveOut_BackupNotYetOnDiskIsCovered` checks nonexistent copies, and `TestIsCarveOut_BackupWrittenMidTurnIsCovered` creates a copy after policy construction. `TestBuildCarveOuts_MergedSecretSet` checks that `backups/` is an app-layer carve-out root; none of these tests simulates creating that directory mid-turn. Require a named `--- PASS` for each selected test.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-063-unified-file-access-engine-and-mounts.md` (app/kernel file-access boundaries).

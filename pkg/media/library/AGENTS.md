# pkg/media/library — workspace binary media storage

What it owns: Raw media bytes, manifest entries, refcounts and integrity checks per workspace.
What it does not own: The path-based text file explorer in `pkg/library` or the knowledge-record model.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected$' -v -p 1 ./pkg/media/library/`

## Pitfalls here

- Returning tampered bytes as ordinary media hides corruption. `TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected` checks the manifest hash on read.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md` (Workspace media library and presentation layer).

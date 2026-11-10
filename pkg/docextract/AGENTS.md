# pkg/docextract — document text extraction

What it owns: Bounded, pure-Go text extraction from uploaded documents and archive manifests.
What it does not own: Rendering or visually reading images and document pages.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestExtractBytes_ZipManifest_CorruptFailsClosed$' -v -p 1 ./pkg/docextract/`

## Pitfalls here

- A corrupt archive must not produce a plausible incomplete manifest. `extract_archive_test.go::TestExtractBytes_ZipManifest_CorruptFailsClosed` checks the failure path.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md` (visual reading is a separate workflow).

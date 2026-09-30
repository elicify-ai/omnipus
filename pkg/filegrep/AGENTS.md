# pkg/filegrep — bounded file-content search

What it owns: Walking and matching workspace files with bounded, readable excerpts.
What it does not own: Knowledge-record queries or full-text index persistence.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestFileGrep_UTF8SafeExcerpts$' -v -p 1 ./pkg/filegrep/`

## Pitfalls here

- Cutting a match excerpt mid-character produces invalid text. `excerpt_test.go::TestFileGrep_UTF8SafeExcerpts` checks Unicode-safe excerpts.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-081-unified-library-search-and-grep-engine.md` (unified search and grep).

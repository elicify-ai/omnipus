# pkg/library — the workspace file explorer

## Not pkg/media/library

This package is the workspace-relative path explorer rooted at
`workspaces/<id>/work/` — the SAME tree every agent file/exec tool is confined
to (ADR-046). `pkg/media/library` is the UUID-keyed binary media store: a
different package solving a different problem. Confusing the two routes
file-explorer paths through the media store or media through the path
explorer; both are wrong.

## Path safety is this package's whole job

Every operation goes through a Go `os.Root` opened at the workspace's `work/`
directory (`root.go`). `os.Root` refuses any path whose resolution — including
through a symlink — would leave the root, closing the TOCTOU class a
resolve-then-return-a-bare-path design leaves open. Two distinct failures
carry two distinct statuses: `CleanRelPath` rejects structural violations
(absolute path, literal `..`, embedded NUL or backslash) as `ErrInvalidPath`
→ 400 at the REST layer; `os.Root` rejects a symlink escape at I/O time as
`ErrOutsideRoot` → 403. Do not collapse them into one error — the split tells
the operator whether the client sent a malformed path or probed an escape.

This package deliberately does NOT import `pkg/tools` (a heavier,
turn/ctx-oriented package this leaf does not need) — read `root.go`'s header
before adding that import.

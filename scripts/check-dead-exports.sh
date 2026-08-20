#!/usr/bin/env bash
# check-dead-exports.sh
#
# Flags exported (capitalized) Go funcs, methods, and named types that have
# ZERO references anywhere in this module's production build graph — dead
# code that `golangci-lint run --enable=unused` structurally cannot see.
#
# WHY THIS EXISTS
#
# `golangci-lint run --build-tags=goolm,stdjson --default=none --enable=unused
# ./...` reports 0 issues on this repo, but a manual audit found real dead
# exported symbols with zero production callers, e.g.:
#   - BrowserManager.CloseSession (pkg/tools/browser/manager.go)  — only
#     caller is reaper_lifecycle_test.go
#   - turnState.GetLastFinishReason (pkg/agent/turn.go)           — zero
#     callers anywhere, including tests
#   - Store.Rotate (pkg/credentials/store.go)                     — zero
#     callers; only RotateWithPassphrase is used
#   - PartitionStore / NewPartitionStore (pkg/session/daypartition.go) —
#     constructed only in tests; pkg/gateway/sse.go hardcodes partitions: nil
#   - restAPI.HandleWorkspace (pkg/gateway/rest_workspace.go)     — defined,
#     never registered on any mux
#
# staticcheck's `unused` analysis (which golangci-lint's `unused` linter
# wraps) deliberately does NOT flag exported package-level identifiers by
# default — it treats them as potentially-public API, since for an
# importable library there's no way to know if an external module calls
# them. This repo is a single binary with no external importers (CLAUDE.md
# constraint #2), so that caveat doesn't apply — but golangci-lint v2.12.2's
# `unused` linter settings (checked against its published JSON schema) expose
# no toggle to opt exported symbols back in. Confirmed against the schema,
# not assumed: `unusedSettings` only has field-writes-are-uses,
# post-statements-are-reads, exported-fields-are-used, parameters-are-used,
# local-variables-are-used, generated-is-used — nothing for exported
# funcs/methods/types. There is no existing tool this reinvents; the
# available one is structurally blind to this class.
#
# HOW IT WORKS (full detail in scripts/dead-exports-scan/main.go's header)
#
# A small Go program (scripts/dead-exports-scan) type-checks every package
# in this module with go/types — driven by `go list -json` for package
# discovery and go/importer's "source" mode for stdlib/third-party
# resolution, so it needs no new go.mod dependency — then compares, by
# types.Object POINTER IDENTITY (not name), every exported declaration
# against every identifier reference in the production (non-test) build
# graph. This is why it does not confuse
# `BrowserManager.CloseSession(sessionID string)` with the unrelated
# `agentLoop.CloseSession(sessionID, "idle")`: grep can't tell two
# same-named methods on different types apart; go/types can.
#
# Because test files (`_test.go`) are excluded from the graph entirely (the
# same set `go build ./...` would compile), a symbol used only from a test —
# like BrowserManager.CloseSession — is correctly still flagged as dead in
# production.
#
# FALSE-POSITIVE HANDLING
#
#   - Interface satisfaction: a method reached only through dynamic dispatch
#     on an interface value resolves, at the call site, to the INTERFACE
#     method's Object, not the concrete type's — invisible to plain
#     reference counting. The scan tool mitigates this itself (not via the
#     allowlist) with a go/types.Implements() check: if a candidate method's
#     receiver type implements some interface that is itself referenced
#     somewhere in production code, and that interface declares a
#     same-named method, the candidate is suppressed. This is the dominant
#     false-positive class in this codebase (pkg/tools and
#     pkg/sysagent/tools both define many types implementing the `tools.Tool`
#     interface, dispatched only through it) — ~900 of the ~1770 raw
#     candidates on this tree.
#   - Generated code: any file starting with the standard
#     "// Code generated ... DO NOT EDIT." marker is skipped entirely.
#   - _test.go-only helpers: symbols DECLARED in a _test.go file are never
#     candidates in the first place (test files aren't part of the
#     production graph on either side of the comparison).
#   - Everything else (e.g. hand-written cross-package test fixtures living
#     in a production file, like pkg/api/generated/fixtures.go) goes through
#     scripts/dead-exports-allowlist.txt, one justified entry per symbol or
#     file — see that file's header for the format and its one seeded entry.
#
# SCOPE: exported top-level funcs, methods, and named types only. Exported
# vars/consts are deliberately out of scope — they are far more often
# legitimate configuration/constant surface than dead code, and including
# them would need a different, higher false-positive-tolerant bar than this
# check is designed for.
#
# Exit: 0 clean, 1 dead exports found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-dead-exports: cannot cd to $REPO_ROOT" >&2; exit 2; }

if [ ! -d pkg ] || [ ! -f go.mod ]; then
  echo "check-dead-exports: expected 'pkg' and 'go.mod' under $REPO_ROOT — wrong cwd or partial checkout" >&2
  exit 2
fi

if [ ! -f scripts/dead-exports-scan/main.go ]; then
  echo "check-dead-exports: scripts/dead-exports-scan/main.go not found" >&2
  exit 2
fi

export CGO_ENABLED=0
export DEAD_EXPORTS_TAGS="${DEAD_EXPORTS_TAGS:-goolm,stdjson}"

OUT="$(mktemp)"
ERR="$(mktemp)"
trap 'rm -f "$OUT" "$ERR"' EXIT

go run ./scripts/dead-exports-scan >"$OUT" 2>"$ERR"
STATUS=$?

# The scan's own stderr summary line (candidate/dead/suppressed/allowlisted
# counts) is useful signal even on a clean run — always show it.
cat "$ERR" >&2

if [ "$STATUS" -eq 2 ]; then
  echo "check-dead-exports: scan tool could not complete (see stderr above) — treating as a hard failure, not a green" >&2
  exit 2
fi

if [ "$STATUS" -eq 1 ]; then
  echo "check-dead-exports: dead exported symbols found (zero production references):" >&2
  cat "$OUT"
  echo "" >&2
  echo "Each line above is a real go/types-resolved dead export. Fix by either:" >&2
  echo "  - deleting the symbol (and anything it makes dead in turn), or" >&2
  echo "  - wiring it to a real caller if it was meant to be used, or" >&2
  echo "  - adding a justified entry to scripts/dead-exports-allowlist.txt if it is" >&2
  echo "    intentionally-unused public surface (see that file's header)." >&2
  exit 1
fi

if [ "$STATUS" -ne 0 ]; then
  echo "check-dead-exports: scan exited $STATUS unexpectedly" >&2
  exit 2
fi

exit 0

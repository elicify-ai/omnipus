#!/usr/bin/env bash
# check-no-duplicate-renderer.sh
#
# Regression guard for ONE component, `LibraryAudioPreview` — the single
# EMB-027 regression this repo has actually hit.
#
# The rule it is derived from is broader than what this script enforces.
# ADR-083 (embedded-content spec, Step 6 / EMB-027) says: "There MUST be
# exactly one renderer per kind, shared between the full-screen pane and the
# inline embed. No inline-only copy may be created." This script does NOT
# check that invariant across kinds: a duplicate `LibraryVideoPreview`,
# `LibraryImagePreview` or `LibraryPdfPreview` introduced by a bad merge
# passes it silently. Read the name `check-no-duplicate-renderer.sh` as "the
# duplicate-renderer check we have", not "the duplicate-renderer check".
# Extending it means either a second invocation with a different
# CANONICAL/pattern pair, or generalising this script to take them as
# arguments; `check-no-duplicate-renderer.test.sh` (which only ever plants
# and removes a `LibraryAudioPreview` duplicate) would need the same.
#
# The specific history: `LibraryAudioPreview` used to be a private,
# un-exported function INSIDE `src/components/library/LibraryPreviewPane.tsx`
# — it was extracted into its own module, `src/components/library/preview/
# LibraryAudioPreview.tsx`, specifically so the inline embed
# (`KbAudioEmbedMount.tsx`) could mount the SAME definition the pane uses.
#
# WHY A SCRIPT AND NOT JUST A NOTE
#
# A note in a doc comment tells a human. It does not stop `git merge`. A
# branch cut before this extraction still contains the OLD private copy
# inside LibraryPreviewPane.tsx; merging or rebasing it re-adds that
# function as an ordinary, conflict-free addition alongside the new,
# extracted module — git has no idea the duplication is exactly the defect
# EMB-027 exists to prevent. Two copies both passing their own tests is
# precisely the drift this repo's markdown pipeline suffered three times
# (see LibraryAudioPreview.test.tsx's own header).
#
# WHAT THIS CHECKS
#
# Exactly one `function LibraryAudioPreview(` (or `const LibraryAudioPreview
# =`) definition may exist anywhere under `src/`, and it must be the
# canonical file, `src/components/library/preview/LibraryAudioPreview.tsx`.
# A second definition anywhere else — most importantly, a reintroduced
# private copy inside LibraryPreviewPane.tsx — fails the build.
#
# WHAT IS ALLOWED
#
# - The one definition in the canonical file itself.
# - Any number of IMPORTS of it (`import { LibraryAudioPreview } from ...`)
#   or references to it as a value/type — those are not definitions.
# - Test files that MOCK the module via `vi.mock('./LibraryAudioPreview', ...)`
#   — a mock factory's returned object literal can shadow the export name
#   inside `{ LibraryAudioPreview: (...) => ... }`, which this script's
#   pattern (anchored on `function`/`const ... =` at statement position)
#   does not match.
#
# Exit: 0 clean, 1 a duplicate definition found, 2 the check itself could
# not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-duplicate-renderer: cannot cd to $REPO_ROOT" >&2; exit 2; }

if [ ! -d src ]; then
  echo "check-no-duplicate-renderer: expected directory 'src' not found under $REPO_ROOT" >&2
  echo "  (wrong cwd, or a partial checkout — refusing to report a green verdict" >&2
  echo "   for a tree this script never actually scanned)" >&2
  exit 2
fi

CANONICAL="src/components/library/preview/LibraryAudioPreview.tsx"

if [ ! -f "$CANONICAL" ]; then
  echo "check-no-duplicate-renderer: canonical file '$CANONICAL' does not exist." >&2
  echo "  Either it was moved (update this script) or the extraction was reverted." >&2
  exit 2
fi

# This script cannot match its OWN doc comment (which names the function)
# because the scan is confined to `src/` with `--include='*.ts' --include=
# '*.tsx'`, and this file is `scripts/*.sh`. `-l` is here only to print
# FILENAMES rather than lines, which is what the loop below consumes — it is
# not what prevents self-matching, and an earlier version of this comment
# claimed it was.
MATCHES=$(grep -rlE '^(export )?function LibraryAudioPreview\(|^(export )?const LibraryAudioPreview =' \
  --include='*.ts' --include='*.tsx' \
  src/ 2>/dev/null || true)

OFFENDERS=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if [ "$f" != "$CANONICAL" ]; then
    OFFENDERS="${OFFENDERS}${f}"$'\n'
  fi
done <<< "$MATCHES"

if [ -n "$OFFENDERS" ]; then
  echo "check-no-duplicate-renderer: FAILED" >&2
  echo "" >&2
  echo "A second definition of LibraryAudioPreview exists outside the canonical" >&2
  echo "module ($CANONICAL). ADR-083 EMB-027 requires exactly ONE renderer per" >&2
  echo "kind, shared between the Library preview pane and the inline note embed." >&2
  echo "" >&2
  echo "Offending file(s):" >&2
  echo "$OFFENDERS" >&2
  exit 1
fi

echo "check-no-duplicate-renderer: OK — LibraryAudioPreview is defined exactly once."
exit 0

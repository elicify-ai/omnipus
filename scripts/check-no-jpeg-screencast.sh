#!/usr/bin/env bash
# check-no-jpeg-screencast.sh
#
# Regression guard for ADR-061: the JPEG CDP-screencast live-browser path is
# DELETED and WebRTC is the only live-video path. This script fails the build if
# any part of it comes back.
#
# WHY A SCRIPT AND NOT JUST A NOTE
#
# A note in CLAUDE.md tells a human. It does not stop `git merge`. Several
# long-lived branches in this repo were cut BEFORE the removal and still contain
# the whole JPEG pipeline; merging or rebasing any of them re-adds these files
# and call sites as an ordinary, conflict-free addition — git has no idea the
# deletion was deliberate. That is not hypothetical: this repo's own CLAUDE.md
# already documents the same failure mode for the Command Center and Schedules
# UI ("a merge can resurrect these files/surfaces — always resolve by keeping
# the deletion"). This is the mechanical half of that rule for ADR-061.
#
# WHAT WAS REMOVED AND WHY IT MUST NOT RETURN
#
# The JPEG path shipped every frame as base64 inside a JSON WebSocket message
# and rendered it with `src={`data:image/jpeg;base64,${...}`}` on an <img>. At
# quality 60 / 1280x720 / every frame that is ~80KB per frame, +33% for base64,
# ~30x/second — each one paying JSON.parse of a ~110KB string, a data-URL parse,
# a base64 decode, a JPEG decode and a React re-render, all on the browser's main
# thread. Being all-intra (no inter-frame compression) it also cost 10-50x the
# bytes of a real codec for screen content, which barely changes between frames.
#
# The operator-decisive property, though, was not cost: an <img> whose src is
# swapped 30x/second is VISUALLY INDISTINGUISHABLE from video. So whenever
# WebRTC failed, the panel silently degraded to the slow path and looked
# completely normal. A fallback nobody can detect is a fallback that hides the
# real defect forever, which is exactly what happened. Deleting it makes a
# WebRTC failure visible by construction.
#
# WHAT IS ALLOWED
#
# - Anything under the WebRTC path (pkg/tools/browser/webrtc, captureext,
#   cdppipe, pkg/gateway/browser_webrtc.go).
# - JPEG anywhere unrelated to the live-browser sink: screenshots
#   (browser_screenshot), uploads, attachments, the media library, image
#   previews. This script deliberately matches the SCREENCAST shapes, not the
#   word "jpeg".
# - Prose in docs/, ADRs, and comments that discuss the removal (the whole point
#   of ADR-061 is to be readable), and this script's own text.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-jpeg-screencast: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg src contracts; do
  if [ ! -d "$d" ]; then
    echo "check-no-jpeg-screencast: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# Each pattern is a shape that ONLY the deleted JPEG screencast path produces.
# Kept deliberately narrow: a false positive here blocks the build, and a guard
# that cries wolf is a guard people delete.
#
#   Page.startScreencast / StopScreencast / ScreencastFrameAck
#       the CDP calls that drove the whole path (pkg/tools/browser/live.go)
#   EventScreencastFrame / screencastFrame
#       the CDP event and its wire name
#   browser_screencast / BrowserScreencastFrame
#       the deleted WS frame type and its generated Go/TS type
#   data:image/jpeg;base64
#       the <img> sink itself (src={`data:image/jpeg;base64,${frame.data}`})
#   screencastQuality / screencastMaxWidth / screencastEveryNthFrame
#       the deleted tuning constants
#   ReconcileScreencast / PauseScreencast / ResumeScreencast
#       the JPEG-vs-WebRTC pause coordination, meaningless without a JPEG path
# Each rule is (pattern, search-root, exclusion) — scoped, because the bare
# words appear in legitimate code that has nothing to do with the deleted path:
#
#   * `data:image/jpeg;base64` is how browser_screenshot returns an image
#     (pkg/tools/browser/tools.go) and how the agent loop resizes media. Only
#     the LIVE-VIEW component is forbidden from producing one.
#   * `StartScreencast`/`screencastFrame` are used by pkg/tools/browser/cdppipe
#     as a CDP TRANSPORT fixture — it drives a screencast purely to prove the
#     pipe carries large messages. That is not the live-view path.
#   * `browser_screencast` legitimately appears in SPA tests that assert the
#     frame type is now DROPPED. Those tests are the proof the removal holds;
#     flagging them would delete the evidence.
#
# Format: <pattern>::<space-separated roots>::<extended-regex of paths to skip>
RULES=(
  # The CDP screencast drivers — live-view package only, never cdppipe.
  'StartScreencast|StopScreencast|ScreencastFrameAck|EventScreencastFrame::pkg/tools/browser pkg/gateway::/cdppipe/'
  # The deleted wire frame + generated types — production code only.
  'browser_screencast|BrowserScreencastFrame::pkg src contracts::(_test\.go|\.test\.tsx?|/__tests__/)'
  # The <img> sink itself — the live-view component may never build one again.
  'data:image/jpeg;base64::src/components/browser::$^'
  # The deleted tuning constants and the JPEG-vs-WebRTC pause coordination.
  'screencastQuality|screencastMaxWidth|screencastMaxHeight|screencastEveryNthFrame|ReconcileScreencast|PauseScreencast|ResumeScreencast::pkg src::$^'
)

HITS=""
for rule in "${RULES[@]}"; do
  pattern="${rule%%::*}"
  rest="${rule#*::}"
  roots="${rest%%::*}"
  skip="${rest##*::}"
  # shellcheck disable=SC2086 — roots is an intentional space-separated list.
  found="$(grep -rInE "$pattern" \
    --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' --include='*.yml' \
    $roots 2>/dev/null | grep -vE "$skip" || true)"
  [ -n "$found" ] && HITS="${HITS}${found}"$'\n'
done

# Drop comment-only lines: a comment explaining the removal is the desired
# outcome, not a violation. Go/TS line comments, YAML comments, and JSDoc
# continuations.
OFFENDERS="$(printf '%s\n' "$HITS" \
  | grep -v '^\s*$' \
  | awk -F: '{ line=""; for (i=3; i<=NF; i++) line = line (i>3 ? ":" : "") $i;
               sub(/^[ \t]+/, "", line);
               if (line ~ /^\/\// || line ~ /^\*/ || line ~ /^\/\*/ || line ~ /^#/) next;
               print }' \
  || true)"

if [ -n "$OFFENDERS" ]; then
  echo "check-no-jpeg-screencast: FOUND the deleted JPEG screencast path in code:" >&2
  echo "" >&2
  printf '%s\n' "$OFFENDERS" >&2
  echo "" >&2
  echo "The JPEG live-browser fallback was deleted deliberately (ADR-061). WebRTC is" >&2
  echo "the only live-video path." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "predates the removal and git re-added the code as an ordinary addition." >&2
  echo "RESOLVE BY KEEPING THE DELETION — do not 'fix the conflict' by restoring it." >&2
  echo "" >&2
  echo "If you genuinely need to reintroduce a non-WebRTC video path, that reverses" >&2
  echo "an accepted ADR: write the superseding ADR first, then update this guard in" >&2
  echo "the same commit." >&2
  exit 1
fi

echo "check-no-jpeg-screencast: OK (JPEG screencast path absent; WebRTC-only per ADR-061)"
exit 0

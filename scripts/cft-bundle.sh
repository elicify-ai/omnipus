#!/bin/bash
# ADR-052 Phase 1 + SEC-ADR052-003 — bundled Chrome-for-Testing payload
# builder. Invoked by .goreleaser.yaml's per-arch build hook (Phase 1
# scope: linux/{amd64,arm64}). Phase 3 adds darwin/{arm64,x64}; Phase 4
# adds windows.
#
# Per-arch invocation:
#   1. Fetch the live CfT "last-known-good-versions-with-downloads.json"
#      manifest (same URL pkg/tools/browser/installer.go's cftManifestURL
#      uses at runtime).
#   2. Pick the Stable channel's "chrome" build for this OS+arch (full
#      Chrome, NOT chrome-headless-shell — per ADR D2 the bundled build
#      must be full Chrome).
#   3. Download chrome-<arch>.zip + verify X-Goog-Hash md5 (M2 /
#      SEC-ADR052-003 — mirrors verifyGoogHashMD5 in
#      pkg/tools/browser/installer.go).
#   4. Unzip into dist/chromium/<arch>/ (goreleaser's working dir).
#   5. Compute SHA-256 of the chrome binary and write
#      dist/chromium/<arch>/chrome.sha256 — the integrity manifest the
#      runtime reads back and verifies at first launch
#      (chrome.sha256 reader: cmd/omnipus/internal/doctor/command.go's
#      readChromeSHA — BOM/CRLF/comment/prefix tolerant per
#      SEC-ADR052-004).
#
# CfT platform availability (verified 2026-07-21): the live manifest's
# chrome.platforms are {linux64, mac-arm64, mac-x64, win32, win64} — CfT
# does NOT publish a linux-arm64 build today. linux/arm64 archives
# therefore ship without a chromium/ payload; the runtime falls back to
# its managed download path on that arch (the install-time
# `[ -d "$CHROMIUM_DIR" ]` gate skips chrome install cleanly).
#
# This script is bash (not POSIX sh) because the per-arch mapping + the
# repeated-header X-Goog-Hash walk read cleaner with bash arrays and
# case-globbing. The parent goreleaser per-arch hook is what invokes it.

set -eu

ARCH="${1:-${GOARCH:-amd64}}"

case "$ARCH" in
  amd64) CFT_PLATFORM="linux64" ;;
  arm64) CFT_PLATFORM="linux-arm64" ;;
  *)
    echo "ADR-052: unsupported arch '$ARCH' for chrome bundling" >&2
    exit 1
    ;;
esac

CFT_MANIFEST_URL='https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json'

require() {
  # Verify the tool is installed AND functional, not just present on
  # PATH. A broken tool (wrong PATH, half-installed, misconfigured)
  # would otherwise sail past `command -v` only to fail downstream with
  # a less-actionable error. The probe uses `--version` because every
  # tool we need supports it EXCEPT unzip, which uses `-v` (its
  # `--version` long-form is parsed as a chain of single-letter flags
  # and exits non-zero with the usage banner). For unzip we probe with
  # `-v`; everything else uses `--version`.
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ADR-052: $1 required for chrome bundling" >&2
    exit 1
  fi
  if [ "$1" = "unzip" ]; then
    probe_arg="-v"
  else
    probe_arg="--version"
  fi
  if ! "$1" "$probe_arg" >/dev/null 2>&1; then
    echo "ADR-052: $1 is on PATH but not functional; refusing to proceed" >&2
    exit 1
  fi
}
require curl
require unzip
require jq
require sha256sum
require md5sum
require base64
require od

# Map CfT platform → on-disk extraction subdir (matches the runtime's
# fullChromeBinaryRelPath layout for linux64; linux-arm64 is hypothetical).
case "$CFT_PLATFORM" in
  linux64)      EXTRACT_SUBDIR="chrome-linux64" ;;
  linux-arm64)  EXTRACT_SUBDIR="chrome-linux-arm64" ;;
esac

# Stage under dist/chromium/ so goreleaser's `archives.files: chromium/**/*`
# picks it up. Wipe any previous-arch payload first so a build matrix that
# includes both amd64 (bundled) and arm64 (no CfT) doesn't leak amd64
# chrome into the arm64 archive.
STAGE="dist/chromium"
rm -rf "$STAGE"
mkdir -p "$STAGE"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

MANIFEST="$WORK/cft.json"
echo "ADR-052: fetching CfT manifest from $CFT_MANIFEST_URL" >&2
curl -fsSL "$CFT_MANIFEST_URL" -o "$MANIFEST"

# Pick the Stable-channel chrome URL for this platform. CfT manifest format:
#   .channels.Stable.downloads.chrome[].platform == "linux64"
#   .channels.Stable.downloads.chrome[].url      == "<zip url>"
ZIP_URL=$(jq -r --arg p "$CFT_PLATFORM" \
  '.channels.Stable.downloads.chrome[] | select(.platform == $p) | .url' \
  "$MANIFEST")

if [ -z "$ZIP_URL" ] || [ "$ZIP_URL" = "null" ]; then
  echo "ADR-052: CfT does not publish chrome for platform '$CFT_PLATFORM'" >&2
  echo "ADR-052: skipping bundled chrome for $CFT_PLATFORM; runtime will fall back to managed download" >&2
  # Remove the staged dir so the archive/nfpm step sees an empty chromium/
  # payload for this arch (no leakage of a previous arch's chrome).
  rm -rf "$STAGE"
  exit 0
fi

ZIP_PATH="$WORK/chrome.zip"
echo "ADR-052: downloading $ZIP_URL" >&2
curl -fsSL "$ZIP_URL" -o "$ZIP_PATH"

# Verify X-Goog-Hash md5 against downloaded content (SEC-ADR052-003).
# CfT serves via storage.googleapis.com which emits X-Goog-Hash with
# crc32c + md5 comma-joined. We tolerate either a single comma-joined
# header line OR multiple separate header lines (a CORS-folding pattern
# some servers emit; a dict-comprehension that keeps only the last
# header value would drop the first — see CORR-008 in the Phase 1
# review). Concretely: we concatenate every X-Goog-Hash header line into
# one stream, then split on comma and walk the pieces looking for the
# md5=... entry.
echo "ADR-052: verifying X-Goog-Hash md5" >&2
RAW_HASHES=$(curl -fsSI "$ZIP_URL" | awk '
  # Case-insensitive header-name match via tolower() — POSIX awk does
  # not have IGNORECASE; we lowercase explicitly.
  tolower($1) == "x-goog-hash:" {
    # Drop the "X-Goog-Hash:" prefix; preserve the value verbatim so
    # commas in the value survive.
    sub(/^[^:]+:[[:space:]]*/, "")
    print
  }
')
MD5_B64=$(printf '%s\n' "$RAW_HASHES" \
  | tr ',' '\n' \
  | tr -d '\r' \
  | awk '/^[[:space:]]*md5=/ { sub(/^[[:space:]]*md5=/, ""); print; exit }')

if [ -z "$MD5_B64" ]; then
  echo "ADR-052: no md5 in X-Goog-Hash for $ZIP_URL" >&2
  exit 1
fi

# Decode base64 md5 → hex string for comparison with `md5sum`'s output.
# Use `od` (POSIX) instead of `xxd` (not on every CI image). `od -An -tx1`
# prints one hex byte per line with no address column; `tr -d ' \n'`
# flattens to a single 32-char hex string.
EXPECTED_MD5_HEX=$(printf '%s' "$MD5_B64" | base64 -d | od -An -tx1 | tr -d ' \n')

# Streaming md5 — never slurp the ~165MB zip into memory (PERF-005).
ACTUAL_MD5_HEX=$(md5sum "$ZIP_PATH" | awk '{print $1}')

if [ "$EXPECTED_MD5_HEX" != "$ACTUAL_MD5_HEX" ]; then
  echo "ADR-052: chrome zip md5 mismatch (expected $EXPECTED_MD5_HEX, got $ACTUAL_MD5_HEX)" >&2
  exit 1
fi
echo "ADR-052: chrome zip md5 OK" >&2

unzip -q "$ZIP_PATH" -d "$STAGE"

if [ ! -x "$STAGE/$EXTRACT_SUBDIR/chrome" ]; then
  echo "ADR-052: chrome binary missing after extraction (looked for $STAGE/$EXTRACT_SUBDIR/chrome)" >&2
  exit 1
fi

# Emit chrome.sha256 (sha256sum format) at the package root. install.sh's
# awk + the runtime's Go reader both consume this format.
(
  cd "$STAGE"
  sha256sum "$EXTRACT_SUBDIR/chrome" | awk '{printf "%s  %s\n", $1, $2}' > chrome.sha256
)

echo "ADR-052: bundled $STAGE/$EXTRACT_SUBDIR/chrome + $STAGE/chrome.sha256"

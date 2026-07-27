#!/bin/bash
# ADR-052 Phase 1 + SEC-ADR052-003 — bundled Chrome-for-Testing payload
# builder. Invoked by .goreleaser.yaml's per-arch build hook (Phase 1
# scope: linux/{amd64,arm64}). Phase 3 adds darwin/{arm64,x64}; Phase 4
# adds windows.
#
# Per-arch invocation:
#   cft-bundle.sh <ARCH> [GOOS]    (GOOS defaults to linux)
#   1. Fetch the live CfT "last-known-good-versions-with-downloads.json"
#      manifest (same URL pkg/tools/browser/installer.go's cftManifestURL
#      uses at runtime).
#   2. Pick the Stable channel's "chrome" build for this OS+arch (full
#      Chrome, NOT chrome-headless-shell — per ADR D2 the bundled build
#      must be full Chrome). GOOS disambiguates the CfT platform:
#        linux+amd64 → linux64,  linux+arm64 → linux-arm64,
#        darwin+arm64 → mac-arm64, darwin+amd64 → mac-x64.
#   3. Download chrome-<arch>.zip + verify X-Goog-Hash md5 (M2 /
#      SEC-ADR052-003 — mirrors verifyGoogHashMD5 in
#      pkg/tools/browser/installer.go).
#   4. Unzip into dist/chromium/<subdir>/ (goreleaser's working dir).
#   5. Compute SHA-256 of the chrome binary and write
#      dist/chromium/<subdir>/chrome.sha256 — the integrity manifest the
#      runtime reads back and verifies at first launch
#      (chrome.sha256 reader: pkg/tools/browser/chromeintegrity +
#      cmd/omnipus/internal/doctor/command.go's readChromeSHA — BOM/CRLF/
#      comment/prefix tolerant per SEC-ADR052-004).
#
#   darwin layout (ADR-052 Phase 3 / C3 option ii): on macOS the binary
#   is inside a Google-signed .app bundle at
#   <subdir>/Google Chrome for Testing.app/Contents/MacOS/Google Chrome
#   for Testing. chrome.sha256 is written BESIDE the .app (NOT inside it
#   — writing inside would break the bundle's signature and Apple
#   notarization); the runtime + install.sh probe <subdir>/chrome.sha256.
#
# Per-binary manifest placement (PERF2-001): the runtime's
# findPackageChrome probes for chrome.sha256 NEXT TO the binary, i.e.
# <extraction-subdir>/chrome.sha256. Writing it at the chromium/ root
# would silently miss the runtime's probe, fall through to the managed
# download path, and surface as an untraceable installation downgrade.
# The bsd/unix convention "manifest beside binary" is also what
# install.sh expects when it lays down chromium/ to <prefix>/share/.../
# chromium.
#
# CfT platform availability (verified 2026-07-21): the live manifest's
# chrome.platforms are {linux64, mac-arm64, mac-x64, win32, win64} — CfT
# does NOT publish a linux-arm64 build today. linux/arm64 archives
# therefore ship without a chromium/ payload; the runtime falls back to
# its managed download path on that arch (the install-time
# `[ -d "$CHROMIUM_DIR" ]` gate skips chrome install cleanly).
# darwin/arm64 (mac-arm64) and darwin/amd64 (mac-x64) ARE published and
# bundled as of Phase 3 (ADR-052 C3 option ii — Google-signed sibling).
#
# This script is bash (not POSIX sh) because the per-arch mapping + the
# repeated-header X-Goog-Hash walk read cleaner with bash arrays and
# case-globbing. The parent goreleaser per-arch hook is what invokes it.

set -eu

# Args: ARCH (arg 1) + GOOS (arg 2). GOOS defaults to "linux" so the
# one-arg call from goreleaser's Phase-1 per-arch hook is unchanged
# (backward compat). Phase 3 adds darwin: pass "darwin" as the second arg,
# e.g. `cft-bundle.sh arm64 darwin` → mac-arm64. GOOS disambiguates arm64
# (linux-arm64 vs mac-arm64) and amd64 (linux64 vs mac-x64).
ARCH="${1:-${GOARCH:-amd64}}"
GOOS="${2:-${GOOS:-linux}}"

case "$GOOS:$ARCH" in
  linux:amd64)  CFT_PLATFORM="linux64" ;;
  linux:arm64)  CFT_PLATFORM="linux-arm64" ;;
  darwin:arm64) CFT_PLATFORM="mac-arm64" ;;
  darwin:amd64) CFT_PLATFORM="mac-x64" ;;
  *)
    echo "ADR-052: unsupported OS/arch '$GOOS/$ARCH' for chrome bundling" >&2
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

# MEDIUM fix (disk-fill family, same root cause as the Go installer's
# FIX-CRIT-001): the full Chrome-for-Testing zip is ~150-200 MB and
# extracts to a comparable size on top of that, so a run that starts with
# well under ~1 GiB free is very likely to hit "disk fills up mid-
# download-or-extract" — exactly the failure mode that leaves a partial,
# corrupted dist/chromium/ payload behind. Fail fast with a clear message
# instead of downloading ~150 MB only to die partway through unzip. Checks
# the CURRENT directory's filesystem (not dist/, which may not exist yet
# on a first run — dist/chromium/ is created on the same filesystem as the
# repo root this script always runs from).
MIN_FREE_KB=$((1024 * 1024)) # 1 GiB, in 1K blocks
AVAIL_KB=$(df -Pk . | awk 'NR==2 {print $4}')
if [ -n "$AVAIL_KB" ] && [ "$AVAIL_KB" -lt "$MIN_FREE_KB" ]; then
  echo "ADR-052: only ${AVAIL_KB}KB free on this filesystem — need at least ${MIN_FREE_KB}KB (1 GiB) to safely download and extract chrome-for-testing. Free up disk space and retry." >&2
  exit 1
fi

# Map CfT platform → on-disk extraction subdir. These match the directory
# names the CfT zip produces (linux: chrome-linux{64,-arm64}/; darwin:
# chrome-mac-{arm64,x64}/) and what the runtime's fullChromeBinaryRelPath()
# in pkg/tools/browser/installer.go expects inside the package root.
case "$CFT_PLATFORM" in
  linux64)      EXTRACT_SUBDIR="chrome-linux64" ;;
  linux-arm64)  EXTRACT_SUBDIR="chrome-linux-arm64" ;;
  mac-arm64)    EXTRACT_SUBDIR="chrome-mac-arm64" ;;
  mac-x64)      EXTRACT_SUBDIR="chrome-mac-x64" ;;
esac

# Stage under dist/chromium/ so goreleaser's `archives.files: chromium/**/*`
# picks it up. Wipe any previous-arch payload first so a build matrix that
# includes both amd64 (bundled) and arm64 (no CfT) doesn't leak amd64
# chrome into the arm64 archive.
STAGE="dist/chromium"
rm -rf "$STAGE"
mkdir -p "$STAGE"

WORK=$(mktemp -d)

# MEDIUM fix: before this, the EXIT trap only ever cleaned up WORK (the
# mktemp scratch dir) — $STAGE (dist/chromium/, 400+ MB once populated) had
# no failure trap at all and was only ever cleared by the START of the
# NEXT run (`rm -rf "$STAGE"` above). A failure anywhere after this point
# (network flake mid-download, a bad checksum, disk-full mid-extract) left
# a partial/corrupted chromium/ payload sitting in dist/ until someone
# noticed or re-ran the bundler — the same disk-fill-accumulation family
# as the Go installer's FIX-CRIT-001 issue. cleanup() removes WORK
# unconditionally and additionally removes $STAGE, but ONLY on a non-zero
# exit (rc -ne 0) — a genuinely successful run (rc=0) leaves $STAGE alone,
# since that's the goreleaser archive payload this whole script exists to
# produce. The "no CfT build for this platform" early-exit path below
# already does its own `rm -rf "$STAGE"; exit 0`, which is rc=0 and so
# does not re-trigger this cleanup (harmless either way — rm -rf on an
# already-removed directory is a no-op).
cleanup() {
  local rc=$?
  rm -rf "$WORK"
  if [ "$rc" -ne 0 ]; then
    echo "ADR-052: build failed (exit $rc) — removing partial $STAGE payload so it can't be mistaken for a complete bundle" >&2
    rm -rf "$STAGE"
  fi
  exit "$rc"
}
trap cleanup EXIT

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

# Full Chrome's on-disk layout differs by OS: linux/windows ship a flat
# `chrome` binary at the extraction-subdir root; darwin ships a .app bundle
# (Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing)
# matching fullChromeBinaryRelPath() in pkg/tools/browser/installer.go.
case "$GOOS" in
  darwin)
    BINARY="$STAGE/$EXTRACT_SUBDIR/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
    ;;
  *)
    BINARY="$STAGE/$EXTRACT_SUBDIR/chrome"
    ;;
esac
if [ ! -x "$BINARY" ]; then
  echo "ADR-052: chrome binary missing after extraction (looked for $BINARY)" >&2
  exit 1
fi

# Emit chrome.sha256 (sha256sum format) at <extract-subdir>/chrome.sha256.
# install.sh's awk + the runtime's Go reader both consume this format. The
# runtime's findPackageChrome probes <extraction-subdir>/chrome.sha256
# first; a root-level chrome.sha256 would silently miss (PERF2-001).
#
# darwin (ADR-052 C3 option ii): the binary lives inside a Google-SIGNED
# .app bundle; writing chrome.sha256 INSIDE the .app would break the
# bundle's CodeDirectory signature (Apple notarization rejects a re-sealed
# bundle). We write it BESIDE the .app at <extract-subdir>/chrome.sha256
# — OUTSIDE the signed bundle, matching the linux <subdir>/chrome.sha256
# convention. The runtime's findPackageChrome darwin layout probes this
# location (the .app's parent = the extract subdir); install.sh's SHA-file
# resolution also checks here. Both parsers key on the 64-char hex digest,
# so the filename field (which awk truncates on the .app path's spaces) is
# cosmetic and harmless.
SHA_TARGET="$STAGE/$EXTRACT_SUBDIR/chrome.sha256"
sha256sum "$BINARY" | awk '{printf "%s  %s\n", $1, $2}' > "$SHA_TARGET"

# Self-verify (SEC-NEW2-004): re-hash and compare against the manifest we
# just wrote. A mismatch here means the pipeline is corrupt (the binary
# changed between the sha256sum invocation and the write, or the awk
# formatter mangled the digest) — neither should ever happen in practice,
# but checking costs nothing and catches a class of bugs where the
# manifest and the binary disagree silently.
#
# Exposed as a standalone function so bats tests can invoke it directly
# against a tampered manifest (a re-run of cft-bundle.sh would just
# overwrite chrome.sha256 before the self-verify, masking the tamper).
self_verify_chrome_sha256() {
  local binary="$1"
  local sha_target="$2"
  local written
  local computed
  written="$(awk '{print $1}' "$sha_target")"
  computed="$(sha256sum "$binary" | awk '{print $1}')"
  if [ "$written" != "$computed" ]; then
    echo "ADR-052: chrome.sha256 self-test FAILED — pipeline corruption detected" >&2
    echo "ADR-052:   manifest: $written" >&2
    echo "ADR-052:   computed: $computed" >&2
    return 1
  fi
  return 0
}

self_verify_chrome_sha256 "$BINARY" "$SHA_TARGET"

echo "ADR-052: bundled $BINARY + $SHA_TARGET"

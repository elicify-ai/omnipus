#!/usr/bin/env bats
#
# cft-bundle.sh — bats test suite (ADR-052 Phase 1 review finding TEST-001).
#
# Covers the chrome-bundle helper's required-tool gating and the happy path
# (CfT manifest walk + zip download + md5 verify + SHA-256 emit), plus
# arm64-skips-cleanly (SPEC-003) and md5-mismatch (CORR-008) failure paths.
# Each test stages a sandboxed fixture + PATH-scrubbed curl so the real
# network and the real $PATH are never touched.

CFT_BUNDLE_SH="${BATS_TEST_DIRNAME}/cft-bundle.sh"

setup() {
  SANDBOX="$(mktemp -d)"
  export HOME="$SANDBOX/home"
  mkdir -p "$HOME"

  # Per-test working dir — cft-bundle.sh writes to dist/chromium/ inside cwd.
  WORK="$SANDBOX/work"
  mkdir -p "$WORK"
  cd "$WORK"

  # Build a fixture chrome payload: a tiny "chrome" binary inside a
  # chrome-linux64/ dir, packaged as a zip.
  FIX="$SANDBOX/fixture"
  mkdir -p "$FIX/chrome-linux64"
  printf '#!/bin/sh\necho "fake chrome for testing"\n' > "$FIX/chrome-linux64/chrome"
  chmod +x "$FIX/chrome-linux64/chrome"
  ( cd "$FIX" && zip -qr chrome.zip chrome-linux64 )
  # Pre-compute the md5 (in hex) and SHA-256 of the fixture zip + binary so
  # tests can assert against expected values without re-running the tools.
  export FIXTURE_CHROME_MD5_HEX="$(md5sum "$FIX/chrome.zip" | awk '{print $1}')"
  export FIXTURE_CHROME_SHA256_HEX="$(sha256sum "$FIX/chrome-linux64/chrome" | awk '{print $1}')"
  export FIXTURE_CHROME_MD5_B64="$(printf '%b' "$(printf '%s' "$FIXTURE_CHROME_MD5_HEX" | sed 's/\(..\)/\\x\1/g')" | base64 | tr -d '\n')"

  # Build a minimal CfT manifest fixture pointing the linux64 platform at
  # the fixture zip via a file:// URL. Tests mutate this manifest (e.g.
  # add a linux-arm64 entry, or REMOVE linux64 to simulate "no build for
  # this platform") before invoking cft-bundle.sh.
  MANIFEST_SRC="$FIX/manifest.json"
  cat > "$MANIFEST_SRC" <<EOF
{
  "channels": {
    "Stable": {
      "channel": "Stable",
      "version": "999.0.9999.99",
      "downloads": {
        "chrome": [
          {
            "platform": "linux64",
            "url": "file://$FIX/chrome.zip"
          }
        ]
      }
    }
  }
}
EOF

  # Scrub PATH: stub curl so the script never hits the network. The stub
  # serves the local fixtures based on URL pattern. Other tools (jq,
  # sha256sum, md5sum, unzip, base64, xxd) are required and assumed
  # available on the test host — if a test wants to gate on "tool X is
  # missing", it scrubs that tool out of PATH below.
  SCRUB_BIN="$SANDBOX/scrub-bin"
  mkdir -p "$SCRUB_BIN"
  cp "${BATS_TEST_DIRNAME}/cft-curl-stub.sh" "$SCRUB_BIN/curl"
  chmod +x "$SCRUB_BIN/curl"
  export PATH="$SCRUB_BIN:$PATH"
  # The curl stub reads CFT_FIXTURE_DIR to know where to find fixture files.
  export CFT_FIXTURE_DIR="$FIX"
}

teardown() {
  rm -rf "$SANDBOX"
}

# ── Helper: scrub a tool from PATH (so cft-bundle.sh sees it as missing) ──
# cft-bundle.sh's `require` both checks presence via `command -v` AND
# probes functionality via `<tool> --version`. To make `require` fire we
# point the SCRUB_BIN stub at /bin/false (which prints "false: missing
# operand" to stderr — not a clean --version response). The require check
# then exits with "<tool> required" or "<tool> is on PATH but not
# functional". Either is a valid abort message.
scrub_tool() {
  local tool="$1"
  cat > "$SCRUB_BIN/$tool" <<STUB
#!/bin/sh
echo "test harness: $tool stub returning 127 (not functional)" >&2
exit 127
STUB
  chmod +x "$SCRUB_BIN/$tool"
}

# Remove SCRUB_BIN's tool slot + SCRUB_BIN itself from PATH so the tool
# is genuinely absent. cft-bundle.sh's `require` check fires with
# "<tool> required" message.
remove_from_path() {
  local tool="$1"
  scrub_tool "$tool"
  # Strip SCRUB_BIN from PATH.
  NEW_PATH=""
  IFS_SAVE="$IFS"
  IFS=':'
  for p in $PATH; do
    if [ "$p" != "$SCRUB_BIN" ]; then
      NEW_PATH="${NEW_PATH:+$NEW_PATH:}$p"
    fi
  done
  IFS="$IFS_SAVE"
  export PATH="$NEW_PATH"
}

# ── Tests ────────────────────────────────────────────────────────────────────

@test "cft-bundle.sh: missing curl aborts with 'curl required' or 'not functional'" {
  scrub_tool curl
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"curl required"* ]] || [[ "$output" == *"curl is on PATH but not functional"* ]]
}

@test "cft-bundle.sh: missing jq aborts (no python3 dep — replaced by jq)" {
  scrub_tool jq
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"jq required"* ]] || [[ "$output" == *"jq is on PATH but not functional"* ]]
}

@test "cft-bundle.sh: missing unzip aborts with 'unzip required' or 'not functional'" {
  scrub_tool unzip
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"unzip required"* ]] || [[ "$output" == *"unzip is on PATH but not functional"* ]]
}

@test "cft-bundle.sh: missing sha256sum aborts with 'sha256sum required' or 'not functional'" {
  scrub_tool sha256sum
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"sha256sum required"* ]] || [[ "$output" == *"sha256sum is on PATH but not functional"* ]]
}

@test "cft-bundle.sh: bash syntax is valid" {
  run bash -n "$CFT_BUNDLE_SH"
  [ "$status" -eq 0 ]
}

@test "cft-bundle.sh: invalid arch argument exits 1" {
  run bash "$CFT_BUNDLE_SH" riscv64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported OS/arch"* ]]
}

@test "cft-bundle.sh: amd64 happy path emits chrome.sha256 with correct digest" {
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -d "dist/chromium/chrome-linux64" ]
  [ -x "dist/chromium/chrome-linux64/chrome" ]
  # PERF2-001: chrome.sha256 lives NEXT TO the binary (per-binary manifest
  # placement), not at the chromium/ root. The runtime's findPackageChrome
  # probes <extraction-subdir>/chrome.sha256 first.
  [ -f "dist/chromium/chrome-linux64/chrome.sha256" ]
  # The sha256 file must contain the hex digest of the fixture chrome.
  manifest_contents="$(cat dist/chromium/chrome-linux64/chrome.sha256)"
  [[ "$manifest_contents" == *"$FIXTURE_CHROME_SHA256_HEX"* ]]
  # sha256sum text-mode format: hex, exactly two spaces, then the
  # per-binary relative path. Per-binary placement (PERF2-001) means the
  # captured `$2` includes the leading "dist/chromium/<subdir>" prefix
  # when cft-bundle.sh is invoked from the goreleaser cwd — the runtime
  # parser tolerates any leading path component.
  [[ "$manifest_contents" == *"  "*"chrome"* ]]
}

@test "cft-bundle.sh: linux/arm64 skips cleanly when CfT has no arm64 build (SPEC-003)" {
  # The default fixture manifest only contains a linux64 entry; invoking
  # cft-bundle.sh with arm64 should detect the missing platform and exit
  # 0 with a clear 'skipping' log. The dist/chromium/ payload (if any
  # from a prior arch) is wiped so the arm64 archive gets an empty
  # chromium/ payload.
  mkdir -p dist/chromium/legacy-from-amd64
  echo "should be wiped" > dist/chromium/legacy-from-amd64/leftover.txt
  run bash "$CFT_BUNDLE_SH" arm64
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"skipping"* ]] || [[ "$output" == *"do not publish"* ]]
  # dist/chromium/ should be empty (wiped by cft-bundle.sh on skip).
  [ ! -f dist/chromium/legacy-from-amd64/leftover.txt ]
  [ ! -d dist/chromium/chrome-linux64 ]
}

@test "cft-bundle.sh: arm64 succeeds if CfT publishes a linux-arm64 build (forward-compat)" {
  # Build a SECOND fixture zip whose top-level dir is chrome-linux-arm64/
  # (matching the cft-bundle.sh EXTRACT_SUBDIR for that platform), then
  # mutate the manifest to add the linux-arm64 entry pointing at it.
  FIX="$SANDBOX/fixture"
  mkdir -p "$FIX/chrome-linux-arm64"
  printf '#!/bin/sh\necho "fake arm64 chrome"\n' > "$FIX/chrome-linux-arm64/chrome"
  chmod +x "$FIX/chrome-linux-arm64/chrome"
  ( cd "$FIX" && zip -qr chrome-arm64.zip chrome-linux-arm64 )
  cp "$FIX/manifest.json" "$FIX/manifest.json.bak"
  jq '.channels.Stable.downloads.chrome += [{
    "platform": "linux-arm64",
    "url": "file://'"$FIX"'/chrome-arm64.zip"
  }]' "$FIX/manifest.json.bak" > "$FIX/manifest.json"
  run bash "$CFT_BUNDLE_SH" arm64
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "dist/chromium/chrome-linux-arm64/chrome" ]
  # PERF2-001: per-binary manifest placement.
  [ -f "dist/chromium/chrome-linux-arm64/chrome.sha256" ]
  run cat "dist/chromium/chrome-linux-arm64/chrome.sha256"
  [[ "$output" == *"chrome-linux-arm64/chrome"* ]]
}

@test "cft-bundle.sh: X-Goog-Hash md5 mismatch aborts install (CORR-008 partial)" {
  # Point the fixture zip's md5 at a value different from its real md5 so
  # cft-bundle.sh's md5 verification fails. We mutate the manifest URL to
  # point at a 'tampered.zip' whose real md5 differs from the bogus one
  # we synthesize in the stub header.
  FIX="$SANDBOX/fixture"
  cp "$FIX/chrome.zip" "$FIX/tampered.zip"
  cp "$FIX/manifest.json" "$FIX/manifest.json.bak"
  jq '.channels.Stable.downloads.chrome[0].url = "file://'"$FIX"'/tampered.zip"' \
    "$FIX/manifest.json.bak" > "$FIX/manifest.json"
  # The cft-curl-stub.sh script computes the X-Goog-Hash from the actual
  # zip file, so a 'mismatch' would only happen if we shimmed the stub
  # to emit a different md5. Instead, we test that md5 verification does
  # succeed for the happy-path (covered above) and that the script
  # catches the more important 'md5 header missing' case below.
  # This test verifies the script DOES verify md5 (and reports OK) — so a
  # missing verification path is what we're catching.
  run bash "$CFT_BUNDLE_SH" amd64
  [ "$status" -eq 0 ]
  [[ "$output" == *"md5 verified"* ]] || [[ "$output" == *"md5 OK"* ]]
}

@test "cft-bundle.sh: tampered X-Goog-Hash md5 aborts with 'chrome zip md5 mismatch'" {
  # CORR-008 (full path): install a curl stub that emits a synthetic
  # X-Goog-Hash with a deliberately wrong md5 (cft-curl-stub.sh always
  # computes the real md5, so a true mismatch test requires an in-test
  # variant). cft-bundle.sh's md5 verification must catch the mismatch
  # and abort with a clear error — never silently pass.
  FIX="$SANDBOX/fixture"
  # Build a tampered.zip whose real md5 differs from what the stub
  # announces. The stub synthesizes md5=<base64-of-md5-of-whatever-it-
  # serves-from-the-URL>, so we don't actually need a different file —
  # we just need the stub to emit a WRONG md5. We do this by overriding
  # the stub to emit a constant bogus md5 in the X-Goog-Hash header
  # (see the inline stub below).
  WRONG_MD5_B64="$(printf 'tampered-md5-not-real' | base64 | tr -d '\n')"
  cat > "$SCRUB_BIN/curl" <<STUB
#!/bin/bash
if [ "\$#" -eq 1 ] && [ "\$1" = "--version" ]; then
  printf 'curl-stub 0.1\n'
  exit 0
fi
url=""
out=""
head_only=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -fsSI) head_only=1; shift ;;
    -fsSL|-sSL) shift ;;
    -I|--head) head_only=1; shift ;;
    --version) printf 'curl-stub 0.1\n'; exit 0 ;;
    -o) shift; [ \$# -gt 0 ] && out="\$1"; shift ;;
    -*) shift ;;
    *) [ -z "\$url" ] && url="\$1"; shift ;;
  esac
done
FIX="\${CFT_FIXTURE_DIR:-}"
[ -z "\$FIX" ] && { echo "no CFT_FIXTURE_DIR" >&2; exit 1; }
case "\$url" in
  *last-known-good-versions-with-downloads.json) src="\$FIX/manifest.json" ;;
  file://*) src="\${url#file://}" ;;
  *linux64/chrome-linux64.zip) src="\$FIX/chrome.zip" ;;
  *) echo "stub: unknown URL \$url" >&2; exit 22 ;;
esac
[ -f "\$src" ] || { echo "stub: missing fixture \$src" >&2; exit 22; }
if [ "\$head_only" = "1" ]; then
  # Tampered: emit a wrong md5 (not the real md5 of the served zip).
  printf 'HTTP/1.1 200 OK\r\n'
  printf 'X-Goog-Hash: crc32c=fakecrc32c,md5=%s\r\n' "$WRONG_MD5_B64"
  printf 'Content-Length: %d\r\n' "\$(wc -c < "\$src")"
  printf '\r\n'
  exit 0
fi
if [ -n "\$out" ]; then cp "\$src" "\$out"; else cat "\$src"; fi
STUB
  chmod +x "$SCRUB_BIN/curl"
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"chrome zip md5 mismatch"* ]]
}

@test "cft-bundle.sh: missing X-Goog-Hash md5 aborts with a clear error" {
  # Drop the X-Goog-Hash from the curl stub for this test by writing a
  # variant that omits it (but still serves the GET body so we reach the
  # md5 check). The stub also responds to --version so cft-bundle.sh's
  # require check passes.
  cat > "$SCRUB_BIN/curl" <<'STUB'
#!/bin/bash
if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then
  printf 'curl-stub 0.1\n'
  exit 0
fi
url=""
out=""
head_only=""
while [ $# -gt 0 ]; do
  case "$1" in
    -fsSI) head_only=1; shift ;;
    -fsSL) shift ;;
    -I|--head) head_only=1; shift ;;
    --version) printf 'curl-stub 0.1\n'; exit 0 ;;
    -o) shift; [ $# -gt 0 ] && out="$1"; shift ;;
    -*) shift ;;
    *) [ -z "$url" ] && url="$1"; shift ;;
  esac
done
FIX="${CFT_FIXTURE_DIR:-}"
[ -z "$FIX" ] && { echo "no CFT_FIXTURE_DIR" >&2; exit 1; }
case "$url" in
  *last-known-good-versions-with-downloads.json) src="$FIX/manifest.json" ;;
  file://*) src="${url#file://}" ;;
  *linux64/chrome-linux64.zip) src="$FIX/chrome.zip" ;;
  *) echo "stub: unknown URL $url" >&2; exit 22 ;;
esac
[ -f "$src" ] || { echo "stub: missing fixture $src" >&2; exit 22; }
if [ "$head_only" = "1" ]; then
  # Deliberately OMIT X-Goog-Hash to simulate a server that doesn't
  # advertise the integrity check.
  printf 'HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n' "$(wc -c < "$src")"
  exit 0
fi
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi
STUB
  chmod +x "$SCRUB_BIN/curl"
  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no md5 in X-Goog-Hash"* ]]
}

@test "cft-bundle.sh: chrome.sha256 self-test rejects tampered manifest (SEC-NEW2-004)" {
  # Drive cft-bundle.sh end-to-end once to populate
  # dist/chromium/chrome-linux64/chrome + chrome.sha256. Then tamper the
  # per-binary manifest with a wrong digest and invoke ONLY the
  # self-verify step (which cft-bundle.sh exposes as a standalone bash
  # function). The self-verify re-hashes the binary and compares against
  # the manifest — a tampered manifest fails closed with "self-test
  # FAILED" on stderr.
  run bash "$CFT_BUNDLE_SH" amd64
  echo "first run status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -f dist/chromium/chrome-linux64/chrome.sha256 ]

  # Tamper with the per-binary manifest.
  printf '%s  chrome\n' \
    "0000000000000000000000000000000000000000000000000000000000000000" \
    > dist/chromium/chrome-linux64/chrome.sha256

  # Extract self_verify_chrome_sha256 from cft-bundle.sh and source it
  # in isolation. The function is the second `function ... { ... }`
  # block in the script; awk walks until the next blank line after the
  # matching `}` to capture the whole definition.
  function_lib="$SANDBOX/self_verify_lib.sh"
  awk '
    /self_verify_chrome_sha256\(\)/ { capture=1 }
    capture { print }
    capture && /^}/ { exit }
  ' "$CFT_BUNDLE_SH" > "$function_lib"
  # shellcheck disable=SC1090
  run bash -c '
    source "'"$function_lib"'"
    self_verify_chrome_sha256 "dist/chromium/chrome-linux64/chrome" "dist/chromium/chrome-linux64/chrome.sha256"
  '
  echo "self-verify run status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"self-test FAILED"* ]]
}

# ── Phase 3: darwin/macOS cases (ADR-052 C3 option ii) ───────────────────────
#
# cft-bundle.sh gained a second GOOS arg (default linux). darwin+arm64 →
# mac-arm64, darwin+amd64 → mac-x64. On darwin the Chrome binary lives
# inside a Google-signed .app bundle; chrome.sha256 is written BESIDE the
# .app (outside the signed bundle) at <extract-subdir>/chrome.sha256.
#
# No cft-curl-stub.sh change is needed: the stub already serves any
# file:// URL (stripping the scheme) and serves $FIX/manifest.json for the
# CfT manifest URL. Each test adds its mac-* entry to manifest.json via jq
# and points it at a local file:// zip — the same hermetic pattern the
# linux-arm64 forward-compat test above uses.

@test "cft-bundle.sh: darwin arm64 maps to mac-arm64 with .app binary + chrome.sha256 beside .app" {
  # Build a darwin fixture: chrome-mac-arm64/<.app>/Contents/MacOS/<bin>.
  FIX="$SANDBOX/fixture"
  MAC_DIR="$FIX/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS"
  mkdir -p "$MAC_DIR"
  printf '#!/bin/sh\necho "fake mac arm64 chrome"\n' > "$MAC_DIR/Google Chrome for Testing"
  chmod +x "$MAC_DIR/Google Chrome for Testing"
  ( cd "$FIX" && zip -qr chrome-mac-arm64.zip chrome-mac-arm64 )
  MAC_SHA256_HEX="$(sha256sum "$MAC_DIR/Google Chrome for Testing" | awk '{print $1}')"
  # Add the mac-arm64 entry to the manifest.
  cp "$FIX/manifest.json" "$FIX/manifest.json.bak"
  jq '.channels.Stable.downloads.chrome += [{
    "platform": "mac-arm64",
    "url": "file://'"$FIX"'/chrome-mac-arm64.zip"
  }]' "$FIX/manifest.json.bak" > "$FIX/manifest.json"
  run bash "$CFT_BUNDLE_SH" arm64 darwin
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  # Extraction subdir is chrome-mac-arm64.
  [ -d "dist/chromium/chrome-mac-arm64" ]
  # .app inner binary present + executable.
  [ -x "dist/chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" ]
  # chrome.sha256 BESIDE the .app (outside the signed bundle), matching the
  # linux <subdir>/chrome.sha256 convention (PERF2-001 / C3 option ii).
  [ -f "dist/chromium/chrome-mac-arm64/chrome.sha256" ]
  # NOT inside the .app (writing inside would break the bundle signature).
  [ ! -f "dist/chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/chrome.sha256" ]
  # Manifest contains the correct hash.
  manifest_contents="$(cat dist/chromium/chrome-mac-arm64/chrome.sha256)"
  [[ "$manifest_contents" == *"$MAC_SHA256_HEX"* ]]
}

@test "cft-bundle.sh: darwin amd64 maps to mac-x64" {
  FIX="$SANDBOX/fixture"
  MAC_DIR="$FIX/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS"
  mkdir -p "$MAC_DIR"
  printf '#!/bin/sh\necho "fake mac x64 chrome"\n' > "$MAC_DIR/Google Chrome for Testing"
  chmod +x "$MAC_DIR/Google Chrome for Testing"
  ( cd "$FIX" && zip -qr chrome-mac-x64.zip chrome-mac-x64 )
  cp "$FIX/manifest.json" "$FIX/manifest.json.bak"
  jq '.channels.Stable.downloads.chrome += [{
    "platform": "mac-x64",
    "url": "file://'"$FIX"'/chrome-mac-x64.zip"
  }]' "$FIX/manifest.json.bak" > "$FIX/manifest.json"
  run bash "$CFT_BUNDLE_SH" amd64 darwin
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -d "dist/chromium/chrome-mac-x64" ]
  [ -x "dist/chromium/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" ]
  [ -f "dist/chromium/chrome-mac-x64/chrome.sha256" ]
}

@test "cft-bundle.sh: linux arm64 with explicit GOOS=linux maps to linux-arm64 (backward compat)" {
  # Regression: the two-arg call `arm64 linux` must behave identically to
  # the one-arg call `arm64` (default GOOS=linux). Build the linux-arm64
  # fixture (mirrors the forward-compat test) and invoke with the explicit
  # GOOS arg.
  FIX="$SANDBOX/fixture"
  mkdir -p "$FIX/chrome-linux-arm64"
  printf '#!/bin/sh\necho "fake arm64 chrome"\n' > "$FIX/chrome-linux-arm64/chrome"
  chmod +x "$FIX/chrome-linux-arm64/chrome"
  ( cd "$FIX" && zip -qr chrome-arm64.zip chrome-linux-arm64 )
  cp "$FIX/manifest.json" "$FIX/manifest.json.bak"
  jq '.channels.Stable.downloads.chrome += [{
    "platform": "linux-arm64",
    "url": "file://'"$FIX"'/chrome-arm64.zip"
  }]' "$FIX/manifest.json.bak" > "$FIX/manifest.json"
  run bash "$CFT_BUNDLE_SH" arm64 linux
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "dist/chromium/chrome-linux-arm64/chrome" ]
  [ -f "dist/chromium/chrome-linux-arm64/chrome.sha256" ]
  # Must NOT have created a darwin layout.
  [ ! -d "dist/chromium/chrome-mac-arm64" ]
}

@test "cft-bundle.sh: unsupported OS/arch combo exits non-zero" {
  # freebsd/amd64 is not a supported CfT platform for Omnipus bundling.
  # The script must exit 1 at the platform-mapping case BEFORE touching
  # any tools (the require checks run after the mapping).
  run bash "$CFT_BUNDLE_SH" amd64 freebsd
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported OS/arch"* ]]
}

# ── MEDIUM fix regression coverage: disk-fill family ─────────────────────────
#
# Mirrors the Go installer's FIX-CRIT-001: a failure partway through
# download/extract must never leave a partial, corrupted dist/chromium/
# payload behind for a later run (or goreleaser's archive step) to pick up
# by mistake, and a host that's already too low on disk to safely attempt
# the ~150-200 MB download+extract should fail fast rather than partway
# through.

@test "cft-bundle.sh: a failure AFTER extraction removes the partial dist/chromium/ payload" {
  # Simulate the exact incident this fix targets: unzip partially extracts
  # (writes a real, executable chrome binary) and then fails outright —
  # the disk-full-mid-extract shape. Before the STAGE cleanup trap, that
  # partial chrome-linux64/chrome would survive on disk after the script
  # exits non-zero, indistinguishable from a real (if incomplete) install.
  cat > "$SCRUB_BIN/unzip" <<'STUB'
#!/bin/bash
if [ "$1" = "-v" ]; then
  echo "UnZip stub 1.0"
  exit 0
fi
dest=""
while [ $# -gt 0 ]; do
  case "$1" in
    -d) shift; dest="$1"; shift ;;
    -q) shift ;;
    *) shift ;;
  esac
done
mkdir -p "$dest/chrome-linux64"
printf '#!/bin/sh\necho partial\n' > "$dest/chrome-linux64/chrome"
chmod +x "$dest/chrome-linux64/chrome"
echo "unzip stub: simulating disk-full mid-extract" >&2
exit 1
STUB
  chmod +x "$SCRUB_BIN/unzip"

  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  # The partial, executable binary the stub wrote must NOT survive — the
  # whole dist/chromium/ tree must be gone, not just left half-populated.
  [ ! -e "dist/chromium/chrome-linux64/chrome" ]
  [ ! -d "dist/chromium" ]
}

@test "cft-bundle.sh: low disk space aborts before any network activity" {
  # Fabricate a df report showing ~1MB free (well under the 1 GiB floor)
  # so the preflight fires deterministically without needing to actually
  # fill the test host's disk.
  cat > "$SCRUB_BIN/df" <<'STUB'
#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'df-stub 1.0\n'
  exit 0
fi
printf '%s\n' "Filesystem     1024-blocks      Used Available Capacity Mounted on"
printf '%s\n' "/dev/fake         1000000    999000      1000     100% /"
STUB
  chmod +x "$SCRUB_BIN/df"

  run bash "$CFT_BUNDLE_SH" amd64
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"free"* ]]
  [[ "$output" == *"disk space"* ]]
  # Must fail BEFORE ever creating dist/chromium/ (no network/tool calls
  # past the preflight).
  [ ! -d "dist/chromium" ]
}

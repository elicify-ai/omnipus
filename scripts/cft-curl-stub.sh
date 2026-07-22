#!/bin/bash
# cft-curl-stub.sh — bats test stub for curl. Mimics just enough of
# curl's behavior to drive scripts/cft-bundle.sh end-to-end without ever
# hitting the network. The stub consults CFT_FIXTURE_DIR for the local
# fixture manifest + chrome zip, and synthesizes CfT-style X-Goog-Hash
# headers for HEAD requests (md5=<base64-of-md5-of-zip>).
#
# This file is intentionally NOT executable at install time — it's only
# copied into a temp SCRUB_BIN during bats runs (see cft-bundle_test.bats).
set -eu

# cft-bundle.sh's `require` check probes the tool with --version before
# the real call. The stub must respond successfully to `--version` (and
# any other harmless probe flags) so the require check passes; only the
# URL-fetching calls get redirected to fixtures.
if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then
  printf 'curl-stub 0.1 (test harness)\n'
  exit 0
fi

url=""
out=""
head_only=""
# The stub only needs to recognize two curl invocations: the GET
# (cft-bundle.sh's manifest + zip downloads) and the HEAD (md5 verification).
# curl may combine -f/s/S/L/-I flags in any order, so we walk args and
# pattern-match the relevant ones.
while [ "$#" -gt 0 ]; do
  case "$1" in
    -fsSI)
      head_only=1
      ;;
    -fsSL|-sSL)
      : # GET with sensible defaults — url follows
      ;;
    -I|--head)
      head_only=1
      ;;
    --version)
      printf 'curl-stub 0.1 (test harness)\n'
      exit 0
      ;;
    -o)
      shift
      [ "$#" -gt 0 ] && out="$1"
      ;;
    -*)
      : # ignore any other flag (we don't model them)
      ;;
    *)
      # First non-flag arg is the URL.
      [ -z "$url" ] && url="$1"
      ;;
  esac
  shift
done

FIX="${CFT_FIXTURE_DIR:-}"
if [ -z "$FIX" ]; then
  echo "cft-curl-stub: CFT_FIXTURE_DIR is not set" >&2
  exit 1
fi

# Map URL → local fixture file. The patterns here mirror the live CfT
# manifest's URL layout (last-known-good-versions-with-downloads.json +
# linux64/chrome-linux64.zip). Tests use file:// URLs in the fixture
# manifest so the stub can serve fixtures directly; strip the file://
# prefix to find the local path.
case "$url" in
  *last-known-good-versions-with-downloads.json)
    src="$FIX/manifest.json"
    ;;
  file://*)
    # Test fixtures reference local files via file:// URLs. Strip the
    # scheme and resolve to an absolute path.
    src="${url#file://}"
    ;;
  *linux64/chrome-linux64.zip)
    src="$FIX/chrome.zip"
    ;;
  *)
    echo "cft-curl-stub: unknown URL: $url" >&2
    exit 22
    ;;
esac

if [ ! -f "$src" ]; then
  echo "cft-curl-stub: missing fixture file: $src" >&2
  exit 22
fi

if [ "$head_only" = "1" ]; then
  # Synthesize CfT-style X-Goog-Hash. The real CfT feed emits crc32c FIRST
  # and md5 SECOND comma-joined; we mirror that here.
  #
  # md5sum outputs HEX; X-Goog-Hash carries base64-encoded RAW BYTES (16
  # bytes for md5). Convert hex → raw bytes via printf "%b" on a \xNN
  # sequence, then base64-encode. (A naive "hex-as-ASCII → base64" would
  # encode the ASCII of the hex digits — a 22-byte string of digits 0-9
  # + letters a-f — not the 16 raw bytes.)
  md5_hex=$(md5sum "$src" | awk '{print $1}')
  md5_raw=$(printf '%b' "$(printf '%s' "$md5_hex" | sed 's/\(..\)/\\x\1/g')")
  md5_b64=$(printf '%s' "$md5_raw" | base64 | tr -d '\n')
  crc32c_b64=$(printf 'fake-crc32c' | base64)
  printf 'HTTP/1.1 200 OK\r\n'
  printf 'X-Goog-Hash: crc32c=%s,md5=%s\r\n' "$crc32c_b64" "$md5_b64"
  printf 'Content-Length: %d\r\n' "$(wc -c < "$src")"
  printf '\r\n'
  exit 0
fi

if [ -n "$out" ]; then
  cp "$src" "$out"
else
  cat "$src"
fi

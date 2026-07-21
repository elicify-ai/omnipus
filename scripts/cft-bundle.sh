#!/bin/bash
# ADR-052 Phase 1 + SEC-ADR052-003 — bundled-Chrome-for-Testing payload
# builder. Invoked by .goreleaser.yaml's pre-build hook on linux/amd64
# only (Phase 1 gate; Phases 3/4 add darwin and windows).
#
# Pipeline:
#   1. Fetch the live CfT "last-known-good-versions-with-downloads.json"
#      manifest via curl (same URL pkg/tools/browser/installer.go's
#      cftManifestURL uses at runtime).
#   2. Parse with python3 to resolve the chrome-linux64.zip URL for the
#      Stable channel's "chrome" build (full Chrome, NOT
#      chrome-headless-shell — per ADR D2 the bundled build must be full
#      Chrome).
#   3. Download chrome-linux64.zip to a temp dir + verify X-Goog-Hash md5
#      against the response header (mirrors verifyGoogHashMD5 in
#      pkg/tools/browser/installer.go — M2 / SEC-ADR052-003).
#   4. Unzip into dist/chromium/ (goreleaser's working dir).
#   5. Compute SHA-256 of dist/chromium/chrome-linux64/chrome and write
#      to dist/chromium/chrome.sha256 — the integrity manifest the
#      runtime reads back and verifies at first launch.
#
# This script is bash (not POSIX sh) because python3 + heredoc syntax +
# associative maps in one block are simpler in bash. The parent
# goreleaser hook is what decides to invoke it; nothing else calls this.

set -eu

CFT_MANIFEST_URL='https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json'

if ! command -v curl >/dev/null 2>&1; then
  echo "ADR-052: curl required for chrome bundling" >&2
  exit 1
fi
if ! command -v unzip >/dev/null 2>&1; then
  echo "ADR-052: unzip required for chrome bundling" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "ADR-052: python3 required for chrome manifest parsing" >&2
  exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

MANIFEST="$WORK/cft.json"
curl -fsSL "$CFT_MANIFEST_URL" -o "$MANIFEST"

ZIP_URL=$(python3 -c '
import json, sys
m = json.load(open(sys.argv[1]))
print(m["channels"]["Stable"]["downloads"]["chrome"]["url"])
' "$MANIFEST")

ZIP_PATH="$WORK/chrome.zip"
curl -fsSL "$ZIP_URL" -o "$ZIP_PATH"

# Verify X-Goog-Hash md5 against downloaded content (per ADR-052 M2 +
# SEC-ADR052-003 — mirrors verifyGoogHashMD5 in
# pkg/tools/browser/installer.go:414-461). The CfT feed is published via
# storage.googleapis.com which sets X-Goog-Hash with crc32c FIRST and md5
# SECOND on a comma-joined line. python3 reads ALL X-Goog-Hash values
# (multiple header lines folded into one) and walks them for the md5
# prefix.
python3 - "$ZIP_URL" "$ZIP_PATH" <<'PY'
import base64, hashlib, sys, urllib.request

url, zip_path = sys.argv[1], sys.argv[2]
req = urllib.request.Request(url, method="HEAD")
with urllib.request.urlopen(req) as r:
    headers = {k.lower(): v for k, v in r.getheaders()}

md5 = None
# Header.get returns only the first X-Goog-Hash value; the live CfT feed
# joins crc32c + md5 onto one line, so we must read the raw header.
for raw in (headers.get("x-goog-hash") or "").split(","):
    raw = raw.strip()
    if raw.startswith("md5="):
        md5 = base64.b64decode(raw[4:])
        break

if md5 is None:
    print("ADR-052: no md5 in X-Goog-Hash for chrome-linux64.zip", file=sys.stderr)
    sys.exit(1)

got = hashlib.md5(open(zip_path, "rb").read()).digest()
if md5 != got:
    print("ADR-052: chrome-linux64.zip md5 mismatch", file=sys.stderr)
    sys.exit(1)

print("ADR-052: chrome-linux64.zip md5 verified")
PY

mkdir -p dist/chromium
unzip -q "$ZIP_PATH" -d dist/chromium

if [ ! -f dist/chromium/chrome-linux64/chrome ]; then
  echo "ADR-052: bundled chrome binary missing after extraction" >&2
  exit 1
fi

# Emit chrome.sha256 in sha256sum format: "<hex>  chrome-linux64/chrome".
# This is the format install.sh + the runtime doctor reader both consume
# (the Go reader is BOM/CRLF/comment/prefix tolerant per SEC-ADR052-004).
(
  cd dist/chromium
  sha256sum chrome-linux64/chrome | awk '{printf "%s  chrome-linux64/chrome\n", $1}' > chrome.sha256
)

echo "ADR-052: bundled dist/chromium/chrome-linux64/chrome + dist/chromium/chrome.sha256 ($(wc -c < dist/chromium/chrome.sha256) bytes)"
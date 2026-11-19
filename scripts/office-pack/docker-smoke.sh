#!/usr/bin/env bash
# Office preview smoke test of one built Docker image (spec T-33, U-L3):
#   - the packaged component folders are at /usr/local/share/omnipus/{office,pdfium}
#     (the resolver's <exe>/../share/omnipus/<slot> for /usr/local/bin/omnipus);
#   - the image's renderer answers a real info and render request;
#   - the image's LibreOffice converts a Word document to PDF.
#
# usage: docker-smoke.sh <image>   (a snapshot tag ending in -amd64 or -arm64)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
image="${1:?image}"
case "$image" in
  *-amd64) platform=linux/amd64 ;;
  *-arm64) platform=linux/arm64 ;;
  *) echo "docker-smoke: cannot tell the platform of $image" >&2; exit 2 ;;
esac
share=/usr/local/share/omnipus

work="$(mktemp -d)"
cid=""
cleanup() {
  [ -z "$cid" ] || docker rm -f "$cid" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# A copy of the image's component folders, for the manifest checks.
cid="$(docker create --platform "$platform" "$image")"
docker cp "$cid:$share/pdfium" "$work/pdfium"
docker cp "$cid:$share/office/manifest.json" "$work/office-manifest.json"
docker rm -f "$cid" >/dev/null
cid=""
python3 - "$work/office-manifest.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
assert m["component"] == "office" and m["trust"] == "packaged", m
print("docker-smoke: office manifest packaged, version %s, executable %s" % (m["version"], m["executable"]))
PY

python3 "$here/renderer-smoke.py" \
  --exec "docker run -i --rm --platform $platform --entrypoint $share/pdfium/omnipus-pdfium $image" \
  "$work/pdfium"

mkdir -p "$work/doc"
chmod 0777 "$work/doc"
python3 "$here/sample-docx.py" "$work/doc/sample.docx"
docker run --rm --platform "$platform" -v "$work/doc:/work" --entrypoint "$share/office/program/soffice" "$image" \
  --headless -env:UserInstallation=file:///tmp/lo-profile --convert-to pdf --outdir /work /work/sample.docx
head -c 5 "$work/doc/sample.pdf" | grep -q '%PDF-' \
  || { echo "docker-smoke: $image: LibreOffice wrote no PDF" >&2; exit 1; }
echo "docker-smoke: OK $image: renderer answered, LibreOffice converted sample.docx ($(wc -c < "$work/doc/sample.pdf" | tr -d ' ') bytes of PDF)"

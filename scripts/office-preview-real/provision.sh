#!/usr/bin/env bash
# Puts the pinned LibreOffice and a freshly built PDF renderer on this Linux
# amd64 runner and exports their folders for the tests.
# usage: provision.sh <output dir> <download cache dir>
#
# LibreOffice: the official archive, refused unless its SHA-256 matches
# pins.env (a restored cache is checked the same way), unpacked without
# installing, then checked for the pinned version and started once so a missing
# system library stops here with its own message.
# Renderer: native/omnipus-pdfium/build.sh, which verifies its own pins.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
# shellcheck source=pins.env
. "$here/pins.env"

out="${1:?output dir}"
dl="${2:?download dir}"

if [ "$(uname -s)-$(uname -m)" != "Linux-x86_64" ]; then
  echo "office-real: the pins cover Linux x86_64 only" >&2
  exit 2
fi

rm -rf "$out"
mkdir -p "$out" "$dl"
out="$(cd "$out" && pwd)"
dl="$(cd "$dl" && pwd)"

archive="$dl/$LIBREOFFICE_ARCHIVE"
if [ ! -f "$archive" ]; then
  curl -sSLf --retry 3 --retry-all-errors "$LIBREOFFICE_URL" -o "$archive.part"
  mv "$archive.part" "$archive"
fi
got="$(sha256sum "$archive" | awk '{print $1}')"
if [ "$got" != "$LIBREOFFICE_SHA256" ]; then
  rm -f "$archive"
  echo "office-real: checksum mismatch for $LIBREOFFICE_ARCHIVE: got $got want $LIBREOFFICE_SHA256" >&2
  exit 3
fi
echo "office-real: verified $LIBREOFFICE_ARCHIVE sha256=$got"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
tar -xzf "$archive" -C "$work"
found=0
while IFS= read -r -d '' deb; do
  dpkg-deb -x "$deb" "$out/libreoffice"
  found=$((found + 1))
done < <(find "$work" -name '*.deb' -print0)
[ "$found" -gt 0 ] || { echo "office-real: the archive held no packages" >&2; exit 4; }

office="$out/libreoffice/$LIBREOFFICE_DIR"
for f in program/soffice program/soffice.bin share/registry/main.xcd; do
  [ -f "$office/$f" ] || { echo "office-real: $f missing from the unpacked LibreOffice" >&2; exit 4; }
done
if ! grep -q "oor:name=\"ooSetupVersionAboutBox\"><value>$LIBREOFFICE_VERSION<" "$office/share/registry/main.xcd"; then
  echo "office-real: the unpacked LibreOffice is not version $LIBREOFFICE_VERSION" >&2
  exit 4
fi

profile="$work/profile"
version_output="$("$office/program/soffice.bin" --headless "-env:UserInstallation=file://$profile" --version 2>&1)" || {
  echo "office-real: LibreOffice did not start: $version_output" >&2
  exit 5
}
case "$version_output" in
  *"$LIBREOFFICE_VERSION"*) echo "office-real: started $version_output" ;;
  *) echo "office-real: unexpected version output: $version_output" >&2; exit 5 ;;
esac

renderer="$out/pdfium"
bash "$repo/native/omnipus-pdfium/build.sh" linux-amd64 "$renderer" "$dl"
for f in omnipus-pdfium libpdfium.so manifest.json; do
  [ -f "$renderer/$f" ] || { echo "office-real: $f missing from the renderer build" >&2; exit 6; }
done

if [ -n "${GITHUB_ENV:-}" ]; then
  {
    echo "OMNIPUS_TEST_OFFICE_ROOT=$office"
    echo "OMNIPUS_TEST_RENDERER_ROOT=$renderer"
  } >> "$GITHUB_ENV"
fi
echo "office-real: office=$office renderer=$renderer"

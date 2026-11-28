#!/usr/bin/env bash
# Builds the Office preview components for one platform and packs them as the
# separate release downloads the installer fetches (office-preview spec §10,
# founder decisions 2026-11-19):
#
#   pdfium_<os>_<arch>.tar.gz   the renderer folder's contents
#   office_<os>_<arch>.tar.gz   the LibreOffice folder's contents (Linux, macOS)
#   pdfium_windows_amd64.zip    a pdfium/ folder, for `omnipus pdf-renderer
#                               register` (Windows registers, AD-12)
#
# <os>_<arch> are Go's names: linux_amd64, linux_arm64, darwin_arm64,
# darwin_amd64 (the names install.sh and check-release-contents.sh expect).
# A tarball holds the folder's contents at its top level: install.sh unpacks
# it straight into the §10 folder.
#
# usage: build-components.sh <platform> <out-dir> <download-dir>
#   platform: linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64
#
# Runs on the platform's own runner: the renderer is compiled natively
# (native/omnipus-pdfium/build.sh) and both components are started once here,
# so a broken build stops the release instead of reaching a user.
#
# Output:
#   <out-dir>/stage/{pdfium,office}/   the unpacked component folders
#   <out-dir>/release/                 the release files, nothing else
#
# LibreOffice is the official build, refused unless its SHA-256 matches
# pins.env. Its packaged manifest (trust "packaged") is written by the Go
# build-time tool; on macOS that also writes the filtered services list
# beside LibreOffice.app, never inside the signed bundle.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
# shellcheck source=pins.env
. "$here/pins.env"

platform="${1:?platform}"
out="${2:?out dir}"
dl="${3:?download dir}"

die() {
  echo "office-pack: $*" >&2
  exit 1
}

case "$platform" in
  linux-amd64|linux-arm64) os=Linux; office=yes ;;
  darwin-arm64|darwin-amd64) os=Darwin; office=yes ;;
  windows-amd64) os=Windows; office=no ;;
  *) die "unknown platform $platform" ;;
esac
key="${platform/-/_}"

# The release must ship the LibreOffice the real-program tests run against.
real_version="$(sed -n 's/^LIBREOFFICE_VERSION=//p' "$repo/scripts/office-preview-real/pins.env")"
real_sha="$(sed -n 's/^LIBREOFFICE_SHA256=//p' "$repo/scripts/office-preview-real/pins.env")"
[ "$real_version" = "$LIBREOFFICE_VERSION" ] \
  || die "pins.env has LibreOffice $LIBREOFFICE_VERSION but the real-program tests pin $real_version"
[ "$real_sha" = "$LIBREOFFICE_SHA256_linux_amd64" ] \
  || die "the Linux x86-64 LibreOffice digest differs from scripts/office-preview-real/pins.env"

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

python_cmd() {
  if [ "$os" = Windows ]; then echo python; else echo python3; fi
}

rm -rf "$out"
mkdir -p "$out/stage" "$out/release" "$dl"
out="$(cd "$out" && pwd)"
dl="$(cd "$dl" && pwd)"
stage="$out/stage"
release="$out/release"
work="$(mktemp -d)"
mnt=""
cleanup() {
  if [ -n "$mnt" ] && [ -d "$mnt" ]; then hdiutil detach "$mnt" -quiet || hdiutil detach "$mnt" -force -quiet || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# ── PDF renderer ─────────────────────────────────────────────────────────────
bash "$repo/native/omnipus-pdfium/build.sh" "$platform" "$stage/pdfium" "$dl"
"$(python_cmd)" "$here/renderer-smoke.py" "$stage/pdfium"

# ── LibreOffice ──────────────────────────────────────────────────────────────
fetch_office() {
  local rel_var="LIBREOFFICE_ARCHIVE_$key" sha_var="LIBREOFFICE_SHA256_$key"
  local rel="${!rel_var}" want="${!sha_var}"
  local file="$dl/$(basename "$rel")"
  if [ ! -f "$file" ]; then
    curl -sSLf --retry 3 --retry-all-errors "$LIBREOFFICE_BASE_URL/$rel" -o "$file.part"
    mv "$file.part" "$file"
  fi
  local got
  got="$(sha256 "$file")"
  if [ "$got" != "$want" ]; then
    rm -f "$file"
    die "checksum mismatch for $(basename "$rel"): got $got want $want"
  fi
  echo "office-pack: verified $(basename "$rel") sha256=$got" >&2
  printf '%s\n' "$file"
}

office_version_check() { # <started version output>
  case "$1" in
    *"$LIBREOFFICE_VERSION"*) echo "office-pack: started $1" ;;
    *) die "unexpected LibreOffice version output: $1" ;;
  esac
}

stage_linux_office() {
  local archive="$1" unpack="$work/unpack" found=0
  mkdir -p "$unpack" "$work/tar"
  tar -xzf "$archive" -C "$work/tar"
  while IFS= read -r -d '' deb; do
    dpkg-deb -x "$deb" "$unpack"
    found=$((found + 1))
  done < <(find "$work/tar" -name '*.deb' -print0)
  [ "$found" -gt 0 ] || die "the LibreOffice archive held no packages"
  [ -d "$unpack/$LIBREOFFICE_LINUX_DIR" ] || die "$LIBREOFFICE_LINUX_DIR missing from the unpacked LibreOffice"
  mv "$unpack/$LIBREOFFICE_LINUX_DIR" "$stage/office"
  local f
  for f in program/soffice program/soffice.bin share/registry/main.xcd; do
    [ -f "$stage/office/$f" ] || die "$f missing from the unpacked LibreOffice"
  done
  grep -q "oor:name=\"ooSetupVersionAboutBox\"><value>$LIBREOFFICE_VERSION<" "$stage/office/share/registry/main.xcd" \
    || die "the unpacked LibreOffice is not version $LIBREOFFICE_VERSION"
  local v
  v="$("$stage/office/program/soffice.bin" --headless "-env:UserInstallation=file://$work/profile" --version 2>&1)" \
    || die "LibreOffice did not start: $v"
  office_version_check "$v"
}

stage_darwin_office() {
  local dmg="$1"
  mnt="$work/mnt"
  mkdir -p "$mnt" "$stage/office"
  hdiutil attach -nobrowse -readonly -noautoopen -mountpoint "$mnt" "$dmg" < /dev/null >/dev/null \
    || die "cannot mount $(basename "$dmg")"
  [ -d "$mnt/LibreOffice.app" ] || die "LibreOffice.app missing from $(basename "$dmg")"
  ditto "$mnt/LibreOffice.app" "$stage/office/LibreOffice.app"
  hdiutil detach "$mnt" -quiet
  mnt=""
  local app="$stage/office/LibreOffice.app" shown
  codesign --verify --deep --strict "$app" || die "LibreOffice.app signature does not verify after copying"
  shown="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")"
  case "$LIBREOFFICE_VERSION" in
    "$shown"*) echo "office-pack: LibreOffice.app version $shown" ;;
    *) die "LibreOffice.app is version $shown, want $LIBREOFFICE_VERSION" ;;
  esac
  local v
  v="$("$app/Contents/MacOS/soffice" --headless "-env:UserInstallation=file://$work/profile" --version 2>&1)" \
    || die "LibreOffice did not start: $v"
  office_version_check "$v"
}

if [ "$office" = yes ]; then
  archive="$(fetch_office)"
  if [ "$os" = Linux ]; then stage_linux_office "$archive"; else stage_darwin_office "$archive"; fi
  # Packaged manifest (trust "packaged"); macOS also gets services/ beside
  # the .app. Written by Go so it is the same code the resolver checks.
  (cd "$repo" && CGO_ENABLED=0 go run -tags goolm,stdjson ./internal/tools/office-manifest write "$stage/office")
  [ -f "$stage/office/manifest.json" ] || die "the manifest tool wrote no manifest.json"
  if [ "$os" = Darwin ]; then
    [ -f "$stage/office/services/services.rdb" ] || die "the manifest tool wrote no filtered services list"
    # The manifest and the services list sit beside the app: the signature
    # must still verify.
    codesign --verify --deep --strict "$stage/office/LibreOffice.app" \
      || die "LibreOffice.app signature broken after writing the manifest"
  fi
fi

# ── Release files ────────────────────────────────────────────────────────────
# COPYFILE_DISABLE keeps macOS tar from adding AppleDouble (._*) entries.
pack_tar() { # <name> <folder>
  COPYFILE_DISABLE=1 tar -czf "$release/$1" -C "$stage/$2" .
}
if [ "$os" = Windows ]; then
  (cd "$stage" && python -m zipfile -c "$release/pdfium_$key.zip" pdfium)
else
  pack_tar "pdfium_$key.tar.gz" pdfium
fi
if [ "$office" = yes ]; then
  pack_tar "office_$key.tar.gz" office
fi

echo "office-pack: release files for $platform:"
for f in "$release"/*; do
  printf '  %s  %s bytes  sha256=%s\n' "$(basename "$f")" "$(wc -c < "$f" | tr -d ' ')" "$(sha256 "$f")"
done

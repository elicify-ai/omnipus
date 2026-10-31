#!/usr/bin/env bash
# Builds omnipus-pdfium for one platform and lays out a release folder:
#   omnipus-pdfium[.exe]  libpdfium.{dylib,so}|pdfium.dll  LICENSES/  manifest.json
# The manifest fingerprints both the program and the shared library (founder
# decision D1 A); the gateway re-hashes both before every launch (AD-4).
#
# usage: build.sh <platform> <out-dir> [download-dir]
#   platform: darwin-arm64 darwin-amd64 linux-amd64 linux-arm64
#             linux-musl-amd64 linux-musl-arm64 windows-amd64
# Needs: curl, tar, a C compiler (clang on macOS, cc on Linux, MSVC cl or
# clang-cl on Windows via Git Bash), shasum or sha256sum.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=pins.env
. "$here/pins.env"

platform="${1:?platform}"
out="${2:?out dir}"
dl="${3:-$(mktemp -d)}"
mkdir -p "$out" "$dl"
out="$(cd "$out" && pwd)"

case "$platform" in
  darwin-arm64) archive=pdfium-mac-arm64.tgz; sha=$PDFIUM_SHA256_mac_arm64; lib=libpdfium.dylib; exe=omnipus-pdfium ;;
  darwin-amd64) archive=pdfium-mac-x64.tgz; sha=$PDFIUM_SHA256_mac_x64; lib=libpdfium.dylib; exe=omnipus-pdfium ;;
  linux-amd64) archive=pdfium-linux-x64.tgz; sha=$PDFIUM_SHA256_linux_x64; lib=libpdfium.so; exe=omnipus-pdfium ;;
  linux-arm64) archive=pdfium-linux-arm64.tgz; sha=$PDFIUM_SHA256_linux_arm64; lib=libpdfium.so; exe=omnipus-pdfium ;;
  linux-musl-amd64) archive=pdfium-linux-musl-x64.tgz; sha=$PDFIUM_SHA256_linux_musl_x64; lib=libpdfium.so; exe=omnipus-pdfium ;;
  linux-musl-arm64) archive=pdfium-linux-musl-arm64.tgz; sha=$PDFIUM_SHA256_linux_musl_arm64; lib=libpdfium.so; exe=omnipus-pdfium ;;
  windows-amd64) archive=pdfium-win-x64.tgz; sha=$PDFIUM_SHA256_win_x64; lib=pdfium.dll; exe=omnipus-pdfium.exe ;;
  *) echo "unknown platform $platform" >&2; exit 2 ;;
esac

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

fetch() { # url file sha
  if [ ! -f "$dl/$2" ]; then curl -sSLf "$1" -o "$dl/$2"; fi
  got="$(sha256 "$dl/$2")"
  if [ "$got" != "$3" ]; then echo "checksum mismatch for $2: got $got want $3" >&2; exit 3; fi
}

fetch "$PDFIUM_BASE_URL/$archive" "$archive" "$sha"
fetch "$ZLIB_URL" "zlib-$ZLIB_VERSION.tar.gz" "$ZLIB_SHA256"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/pdfium" "$work/zlib"
tar -xzf "$dl/$archive" -C "$work/pdfium"
tar -xzf "$dl/zlib-$ZLIB_VERSION.tar.gz" -C "$work/zlib" --strip-components=1

build_id="omnipus-pdfium/${PDFIUM_VERSION}+r${OMNIPUS_PDFIUM_REVISION}/${platform}"
src="$here/omnipus_pdfium.c"
# deflate needs only these zlib units; zlib is compiled in (Linux and
# Windows hosts may lack libz; S-1 note).
zsrc=(adler32.c crc32.c deflate.c trees.c zutil.c)
zfiles=()
for f in "${zsrc[@]}"; do zfiles+=("$work/zlib/$f"); done

rm -rf "${out:?}"/*
case "$platform" in
  darwin-*)
    arch=arm64; [ "$platform" = darwin-amd64 ] && arch=x86_64
    clang -arch "$arch" -O2 -Wall -Wextra -Werror=implicit-function-declaration -mmacosx-version-min=13.0 -DCOMPONENT_BUILD \
      -DOMNIPUS_PDFIUM_BUILD_ID="\"$build_id\"" \
      -I"$work/pdfium/include" -I"$work/zlib" "$src" "${zfiles[@]}" \
      -L"$work/pdfium/lib" -lpdfium -o "$out/$exe"
    # The prebuilt dylib's install name is ./libpdfium.dylib (relative to the
    # CURRENT directory): point the renderer at its own folder instead (S-1).
    install_name_tool -change ./libpdfium.dylib @executable_path/libpdfium.dylib "$out/$exe"
    cp "$work/pdfium/lib/libpdfium.dylib" "$out/$lib"
    codesign --force --sign - "$out/$exe"
    ;;
  linux-*)
    cc -O2 -DCOMPONENT_BUILD -DOMNIPUS_PDFIUM_BUILD_ID="\"$build_id\"" \
      -I"$work/pdfium/include" -I"$work/zlib" "$src" "${zfiles[@]}" \
      -L"$work/pdfium/lib" -lpdfium -Wl,-rpath,'$ORIGIN' -o "$out/$exe"
    cp "$work/pdfium/lib/libpdfium.so" "$out/$lib"
    strip "$out/$exe"
    # musl archives ship with debug info (S-1: 17.6 MB unstripped).
    case "$platform" in linux-musl-*) strip --strip-unneeded "$out/$lib" ;; esac
    ;;
  windows-*)
    cl /nologo /O2 /DCOMPONENT_BUILD "/DOMNIPUS_PDFIUM_BUILD_ID=\"$build_id\"" \
      "/I$work/pdfium/include" "/I$work/zlib" "$src" "${zfiles[@]}" \
      "$work/pdfium/lib/pdfium.dll.lib" "/Fe:$out/$exe" "/Fo:$work/"
    cp "$work/pdfium/bin/pdfium.dll" "$out/$lib"
    ;;
esac

mkdir -p "$out/LICENSES"
cp "$work/pdfium/LICENSE" "$out/LICENSES/pdfium-binaries-LICENSE.txt"
cp "$work/pdfium/licenses/"* "$out/LICENSES/"
cp "$work/zlib/LICENSE" "$out/LICENSES/zlib-ours.txt" 2>/dev/null || sed -n '/Copyright/,$p' "$work/zlib/README" > "$out/LICENSES/zlib-ours.txt"

exe_sha="$(sha256 "$out/$exe")"
lib_sha="$(sha256 "$out/$lib")"
# file_list_sha256 = SHA-256 of the sorted "<path> <sha256>\n" lines
# (pkg/preview/component::FileListDigest).
list="$(printf '%s %s\n%s %s\n' "$exe" "$exe_sha" "$lib" "$lib_sha" | LC_ALL=C sort)"
list_sha="$(printf '%s\n' "$list" | { if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi; } | awk '{print $1}')"
goos="${platform%%-*}"; goarch="${platform##*-}"
cat > "$out/manifest.json" <<JSON
{
  "schema": 1,
  "component": "pdf-renderer",
  "version": "$PDFIUM_VERSION+r$OMNIPUS_PDFIUM_REVISION",
  "build_id": "$build_id",
  "protocol_version": 1,
  "platform": "$goos-$goarch",
  "executable": "$exe",
  "files": {
    "$exe": "$exe_sha",
    "$lib": "$lib_sha"
  },
  "file_list_sha256": "$list_sha",
  "trust": "packaged"
}
JSON
echo "built $build_id in $out"

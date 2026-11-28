#!/usr/bin/env bash
# Release contents check (office-preview packaging P7, #1713; Chrome bundle
# #1913). It lists what the release files really contain and fails on
# anything missing — the check that would have caught the archives shipping
# without chromium/, because GoReleaser silently ignores a file pattern that
# matches nothing.
#
# usage: check-release-contents.sh [--contents] <release-dir> [image ...]
#
# Always (the interface scripts/release/check-release-contents_test.bats pins):
#   - every required release file is present:
#       omnipus_{Linux_x86_64,Linux_arm64,Darwin_arm64,Darwin_x86_64}.tar.gz
#       office_{linux,darwin}_{amd64,arm64}.tar.gz
#       pdfium_{linux,darwin}_{amd64,arm64}.tar.gz, pdfium_windows_amd64.zip
#   - checksums.txt has an entry for each, and the entry matches the file;
#   - the main archives of the Chrome-for-Testing platforms carry the bundled
#     Chrome (ADR-052): chromium/chrome-linux64/{chrome,chrome.sha256} in
#     Linux x86_64, and chromium/chrome-mac-{arm64,x64}/ with
#     "Google Chrome for Testing.app" and chrome.sha256 beside it in the two
#     macOS archives. Linux arm64 is not required to carry one (the runtime
#     has no linux-arm64 package layout; it uses the managed download).
#
# --contents (the release pipelines and the dry run) also opens every file:
#   - each main archive holds the omnipus binary;
#   - each component download holds its manifest.json and programs;
#   - the deb/rpm packages (omnipus_{x86_64,aarch64}.{deb,rpm}) hold the
#     binary, both components under /usr/share/omnipus and, for x86_64, the
#     bundled Chrome;
#   - each named Docker image holds both components under
#     /usr/local/share/omnipus.
# and prints, per file, its entry count and the entries that were checked.
#
# Exit 0: everything present. Exit 1: each missing item printed on its own
# line, naming the file. Exit 2: usage or a missing listing tool.
set -uo pipefail

usage() {
  echo "usage: check-release-contents.sh [--contents] <release-dir> [image ...]" >&2
  exit 2
}

deep=0
if [ "${1:-}" = "--contents" ]; then
  deep=1
  shift
fi
[ "$#" -ge 1 ] || usage
rel="$1"
shift
# The remaining arguments are the Docker images ("$@").
[ -d "$rel" ] || { echo "check-release-contents: $rel is not a folder" >&2; exit 2; }
if [ "$#" -gt 0 ] && [ "$deep" -eq 0 ]; then
  echo "check-release-contents: images are checked only with --contents" >&2
  exit 2
fi

missing=0
miss() {
  echo "MISSING: $*"
  missing=$((missing + 1))
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

need_tool() {
  command -v "$1" >/dev/null 2>&1 || { echo "check-release-contents: $1 is needed to list $2" >&2; exit 2; }
}

main_archives=(omnipus_Linux_x86_64.tar.gz omnipus_Linux_arm64.tar.gz omnipus_Darwin_arm64.tar.gz omnipus_Darwin_x86_64.tar.gz)
components=()
for p in linux_amd64 linux_arm64 darwin_arm64 darwin_amd64; do
  components+=("office_$p.tar.gz" "pdfium_$p.tar.gz")
done
components+=(pdfium_windows_amd64.zip)

# ── Presence and checksums ───────────────────────────────────────────────────
sums="$rel/checksums.txt"
[ -f "$sums" ] || miss "checksums.txt"
for f in "${main_archives[@]}" "${components[@]}"; do
  if [ ! -f "$rel/$f" ]; then
    miss "$f (release file)"
    continue
  fi
  [ -f "$sums" ] || continue
  want="$(awk -v n="$f" '$2 == n || $2 == "*" n {print $1}' "$sums")"
  if [ -z "$want" ]; then
    miss "$f (no checksums.txt entry)"
  elif [ "$want" != "$(sha256 "$rel/$f")" ]; then
    miss "$f (checksums.txt entry does not match the file)"
  fi
done

# ── Archive entries ──────────────────────────────────────────────────────────
# entries <file>: one entry per line, without a leading "./".
entries() {
  case "$1" in
    *.zip) python3 -c 'import sys, zipfile; print("\n".join(zipfile.ZipFile(sys.argv[1]).namelist()))' "$1" ;;
    *.tar.gz) tar -tzf "$1" ;;
    *.deb) dpkg-deb -c "$1" | awk '{ $1=$2=$3=$4=$5=""; sub(/^ +/, ""); sub(/ -> .*/, ""); print }' ;;
    *.rpm) rpm -qlp "$1" 2>/dev/null ;;
  esac | sed -e 's|^\./||' -e 's|/$||'
}

# require <file> <listing> <entry>...: each entry must be in the listing.
require() {
  local file="$1" listing="$2" e
  shift 2
  for e in "$@"; do
    if grep -qxF -- "$e" <<<"$listing"; then
      echo "  ok   $e"
    else
      miss "$file: $e"
    fi
  done
}

list_file() { # <file> -> listing (also prints the entry count)
  local listing
  listing="$(entries "$rel/$1")" || listing=""
  echo "== $1: $(grep -c . <<<"$listing") entries" >&2
  printf '%s' "$listing"
}

app="Google Chrome for Testing.app"
chrome_entries() { # <subdir>
  case "$1" in
    chrome-linux64) echo "chromium/chrome-linux64/chrome"; echo "chromium/chrome-linux64/chrome.sha256" ;;
    *) echo "chromium/$1/$app/Contents/MacOS/Google Chrome for Testing"; echo "chromium/$1/chrome.sha256" ;;
  esac
}

for pair in omnipus_Linux_x86_64.tar.gz:chrome-linux64 omnipus_Darwin_arm64.tar.gz:chrome-mac-arm64 omnipus_Darwin_x86_64.tar.gz:chrome-mac-x64 omnipus_Linux_arm64.tar.gz:; do
  f="${pair%%:*}"
  sub="${pair#*:}"
  [ -f "$rel/$f" ] || continue
  listing="$(list_file "$f")"
  if [ -n "$sub" ]; then
    want_a="$(chrome_entries "$sub" | sed -n 1p)"
    want_b="$(chrome_entries "$sub" | sed -n 2p)"
    require "$f (bundled Chrome, chromium/)" "$listing" "$want_a" "$want_b"
  fi
  if [ "$deep" -eq 1 ]; then
    require "$f" "$listing" omnipus
  fi
done

if [ "$deep" -eq 1 ]; then
  for p in linux_amd64 linux_arm64 darwin_arm64 darwin_amd64; do
    f="office_$p.tar.gz"
    if [ -f "$rel/$f" ]; then
      listing="$(list_file "$f")"
      case "$p" in
        linux_*) require "$f" "$listing" manifest.json program/soffice program/soffice.bin share/registry/main.xcd ;;
        darwin_*) require "$f" "$listing" manifest.json services/services.rdb LibreOffice.app/Contents/MacOS/soffice LibreOffice.app/Contents/Info.plist ;;
      esac
    fi
    f="pdfium_$p.tar.gz"
    if [ -f "$rel/$f" ]; then
      listing="$(list_file "$f")"
      case "$p" in
        linux_*) require "$f" "$listing" manifest.json omnipus-pdfium libpdfium.so ;;
        darwin_*) require "$f" "$listing" manifest.json omnipus-pdfium libpdfium.dylib ;;
      esac
    fi
  done
  f=pdfium_windows_amd64.zip
  if [ -f "$rel/$f" ]; then
    need_tool python3 "$f"
    listing="$(list_file "$f")"
    require "$f" "$listing" pdfium/manifest.json pdfium/omnipus-pdfium.exe pdfium/pdfium.dll
  fi

  # Linux packages.
  for arch in x86_64 aarch64; do
    for fmt in deb rpm; do
      f="omnipus_$arch.$fmt"
      if [ ! -f "$rel/$f" ]; then
        miss "$f (release file)"
        continue
      fi
      if [ "$fmt" = deb ]; then need_tool dpkg-deb "$f"; else need_tool rpm "$f"; fi
      listing="$(list_file "$f" | sed 's|^/||')"
      want=(usr/bin/omnipus
        usr/share/omnipus/office/manifest.json usr/share/omnipus/office/program/soffice.bin
        usr/share/omnipus/pdfium/manifest.json usr/share/omnipus/pdfium/omnipus-pdfium)
      if [ "$arch" = x86_64 ]; then
        want+=(usr/share/omnipus/chromium/chrome-linux64/chrome usr/share/omnipus/chromium/chrome-linux64/chrome.sha256)
      fi
      require "$f" "$listing" "${want[@]}"
    done
  done

  # Docker images.
  for image in "$@"; do
    need_tool docker "$image"
    listing="$(docker run --rm --entrypoint find "$image" /usr/local/bin/omnipus /usr/local/share/omnipus -maxdepth 3 2>/dev/null | sed 's|^/||')"
    echo "== image $image: $(grep -c . <<<"$listing") entries (depth 3)" >&2
    require "image $image" "$listing" usr/local/bin/omnipus \
      usr/local/share/omnipus/office/manifest.json usr/local/share/omnipus/office/program/soffice.bin \
      usr/local/share/omnipus/pdfium/manifest.json usr/local/share/omnipus/pdfium/omnipus-pdfium
  done
fi

if [ "$missing" -gt 0 ]; then
  echo "check-release-contents: $missing item(s) missing"
  exit 1
fi
echo "check-release-contents: all required release contents present"

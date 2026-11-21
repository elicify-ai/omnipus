#!/usr/bin/env bash
# Install-path check for the Office preview packaging dry run (#1713, spec
# §10; Chrome bundle #1913). Runs scripts/install.sh against an unpublished
# release folder on the machine it should install on, then checks what
# landed:
#
#   1. default install: the binary, office/ and pdfium/ in the §10 folders
#      (Linux <prefix>/share/omnipus/<slot>, macOS <prefix>/<slot>) and the
#      bundled Chrome where the platform carries one;
#   2. the omnipus binary resolves both components as packaged and verified
#      (internal/tools/office-manifest resolve);
#   3. the installed renderer answers a real request and the installed
#      LibreOffice converts a Word document to PDF (macOS: the app's code
#      signature is still valid);
#   4. the opt-outs: --without-office removes office/ and keeps pdfium/,
#      --without-pdf-renderer brings office/ back and removes pdfium/;
#   5. a tampered component download refuses the install and leaves no
#      component folder behind.
#
# usage: install-check.sh <release-dir> <prefix>
#   <release-dir> holds omnipus_<Os>_<arch>.tar.gz, office_<os>_<arch>.tar.gz,
#   pdfium_<os>_<arch>.tar.gz and checksums.txt for this machine.
#   <prefix> is created; the binary goes to <prefix>/bin.
set -euo pipefail

[ "$#" -eq 2 ] || { echo "usage: install-check.sh <release-dir> <prefix>" >&2; exit 2; }
rel="$(cd "$1" && pwd)"
mkdir -p "$2"
prefix="$(cd "$2" && pwd)"
repo="$(cd "$(dirname "$0")/../.." && pwd)"
here="$repo/scripts/office-pack"

die() { echo "install-check: FAIL: $*" >&2; exit 1; }
step() { echo; echo "== install-check: $*"; }

case "$(uname -s)" in
  Linux) os=linux; goos_root="$prefix/share/omnipus" ;;
  Darwin) os=darwin; goos_root="$prefix" ;;
  *) die "unsupported OS $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
office="$goos_root/office"
pdfium="$goos_root/pdfium"
chromium="$goos_root/chromium"

run_install() { # <prefix> <release-dir> [flags]
  local p="$1" r="$2"
  shift 2
  OMNIPUS_INSTALL_BASE_URL="file://$r" OMNIPUS_INSTALL_DIR="$p/bin" sh "$repo/scripts/install.sh" "$@"
}

# ── 1. Default install ───────────────────────────────────────────────────────
step "default install from $rel"
run_install "$prefix" "$rel" || die "install.sh exited $?"
step "installed tree (depth 4)"
find "$prefix" -maxdepth 4 | sort | sed "s|^$prefix|<prefix>|"

[ -x "$prefix/bin/omnipus" ] || die "no binary at $prefix/bin/omnipus"
[ -f "$office/manifest.json" ] || die "no office/manifest.json at $office"
[ -f "$pdfium/manifest.json" ] || die "no pdfium/manifest.json at $pdfium"
[ -x "$pdfium/omnipus-pdfium" ] || die "no renderer program at $pdfium/omnipus-pdfium"
if [ "$os" = linux ]; then
  soffice="$office/program/soffice"
else
  soffice="$office/LibreOffice.app/Contents/MacOS/soffice"
fi
[ -x "$soffice" ] || die "no LibreOffice program at $soffice"

# Bundled Chrome (ADR-052): every platform but Linux arm64 carries one.
case "$os-$arch" in
  linux-amd64) chrome="$chromium/chrome-linux64/chrome"; sha="$chromium/chrome-linux64/chrome.sha256" ;;
  darwin-arm64) chrome="$chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"; sha="$chromium/chrome-mac-arm64/chrome.sha256" ;;
  darwin-amd64) chrome="$chromium/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"; sha="$chromium/chrome-mac-x64/chrome.sha256" ;;
  *) chrome=""; sha="" ;;
esac
if [ -n "$chrome" ]; then
  [ -x "$chrome" ] || die "no bundled Chrome at $chrome"
  [ -f "$sha" ] || die "no chrome.sha256 at $sha"
  echo "install-check: bundled Chrome present: $chrome"
else
  [ ! -e "$chromium" ] || die "unexpected chromium/ on $os/$arch at $chromium"
  echo "install-check: no bundled Chrome on $os/$arch, as planned"
fi

# ── 2. The binary resolves both components as packaged ───────────────────────
step "resolve the installed components"
(cd "$repo" && CGO_ENABLED=0 go run -tags goolm,stdjson ./internal/tools/office-manifest resolve "$prefix/bin/omnipus") \
  || die "the installed components do not resolve as packaged"

# ── 3. The installed programs work ───────────────────────────────────────────
step "renderer answers a real request"
python3 "$here/renderer-smoke.py" "$pdfium" || die "renderer smoke failed"

if [ "$os" = darwin ]; then
  step "LibreOffice.app signature"
  codesign --verify --deep --strict "$office/LibreOffice.app" || die "LibreOffice.app signature is not valid after install"
  echo "install-check: LibreOffice.app signature valid"
fi

step "LibreOffice converts a Word document"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
python3 "$here/sample-docx.py" "$work/sample.docx"
"$soffice" --headless "-env:UserInstallation=file://$work/lo-profile" --convert-to pdf --outdir "$work" "$work/sample.docx" \
  || die "soffice --convert-to pdf exited $?"
[ -f "$work/sample.pdf" ] || die "no sample.pdf produced"
[ "$(head -c 5 "$work/sample.pdf")" = "%PDF-" ] || die "sample.pdf is not a PDF"
echo "install-check: converted sample.docx ($(wc -c < "$work/sample.pdf" | tr -d ' ') bytes of PDF)"

# ── 4. Opt-outs on re-run ────────────────────────────────────────────────────
step "re-run with --without-office"
run_install "$prefix" "$rel" --without-office || die "install.sh --without-office exited $?"
[ ! -e "$office" ] || die "office/ still present after --without-office"
[ -f "$pdfium/manifest.json" ] || die "pdfium/ missing after --without-office"
echo "install-check: office/ removed, pdfium/ kept"

step "re-run with --without-pdf-renderer"
run_install "$prefix" "$rel" --without-pdf-renderer || die "install.sh --without-pdf-renderer exited $?"
[ -f "$office/manifest.json" ] || die "office/ not reinstalled"
[ ! -e "$pdfium" ] || die "pdfium/ still present after --without-pdf-renderer"
echo "install-check: office/ reinstalled, pdfium/ removed"

# ── 5. A tampered component refuses the install ──────────────────────────────
step "tampered office download is refused"
bad="$work/bad-release"
mkdir -p "$bad"
cp "$rel"/* "$bad"/
printf 'tampered' >> "$bad/office_${os}_${arch}.tar.gz"
if run_install "$work/bad-prefix" "$bad"; then
  die "install.sh accepted a tampered office_${os}_${arch}.tar.gz"
fi
if [ "$os" = linux ]; then bad_root="$work/bad-prefix/share/omnipus"; else bad_root="$work/bad-prefix"; fi
[ ! -e "$bad_root/office" ] || die "a tampered install left $bad_root/office behind"
[ ! -e "$bad_root/pdfium" ] || die "a tampered install left $bad_root/pdfium behind"
echo "install-check: tampered download refused, nothing installed"

echo
echo "install-check: OK on $os/$arch"

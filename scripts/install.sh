#!/bin/sh
# Omnipus installer — POSIX sh, no bashisms.
#
# Downloads the latest GitHub Release binary for the current OS+arch,
# verifies the SHA256 against checksums.txt, and installs to
# ${OMNIPUS_INSTALL_DIR:-/usr/local/bin}/omnipus.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
#
# Environment overrides:
#   OMNIPUS_VERSION       tag to install (default: latest GitHub Release)
#   OMNIPUS_INSTALL_DIR   target directory (default: /usr/local/bin)
#   OMNIPUS_INSTALL_URL   override full archive URL (for local testing,
#                         e.g. file:///path/to/omnipus_Linux_x86_64.tar.gz —
#                         when set, checksum verification is skipped)
#   OMNIPUS_REPO          GitHub repo path (default: elicify-ai/omnipus)
#
# Supported platforms (v0.1):
#   Linux  x86_64, Linux  aarch64,  macOS arm64 (Apple Silicon)
# Other platforms are planned but not yet shipped — see README "Platform support".
#
# ADR-052 Phase 1 (Bundles + installer):
#   - On Linux, after extracting the archive we verify the bundled Chrome's
#     chrome.sha256 against the binary's actual SHA-256. A mismatch aborts
#     the install (refuse-to-install, no fall-back). Bare-binary users with
#     no chromium/ payload (older archives, hand-built tarballs, linux/arm64
#     archives since CfT publishes no linux-arm64 build) skip this check
#     cleanly.
#   - On Linux, we ensure Chrome's documented host shared libraries are
#     installed (apt or dnf), absent which a bundled full Chrome exits
#     "error while loading shared libraries". This is the C2 host-prereq
#     admission from ADR-052 — a minimal Debian / RHEL install needs this;
#     a stock desktop already has them.
#   - The chromium/ directory is moved to a sibling of the installed binary
#     so the runtime's os.Executable()-based resolver
#     (pkg/tools/browser/exec_resolver.go::packageChromeRoot) finds it. With
#     the default /usr/local/bin install, that is
#     /usr/local/share/omnipus/chromium; with $HOME/.local/bin, that is
#     $HOME/.local/share/omnipus/chromium. The runtime's findPackageChrome
#     looks for chromium/<fullChromeBinaryRelPath()> (linux:
#     chromium/chrome-linux64/chrome) — the install.sh's staged chromium/
#     dir is laid out to match.
#   - macOS Phase 1: Chrome is NOT bundled in the Darwin archive (CfT's
#     mac-arm64 build is real but the macOS .app notarization decision is
#     deferred to Phase 3 per ADR-052 §2/§3). Runtime falls back to
#     managed download on Darwin; operators wanting the bundled-chrome
#     floor on macOS will get it in Phase 3.

set -eu

REPO="${OMNIPUS_REPO:-elicify-ai/omnipus}"
INSTALL_DIR="${OMNIPUS_INSTALL_DIR:-/usr/local/bin}"
VERSION="${OMNIPUS_VERSION:-}"
EXPLICIT_URL="${OMNIPUS_INSTALL_URL:-}"

# ── Helpers ──────────────────────────────────────────────────────────────────

err() {
  printf 'omnipus install: %s\n' "$*" >&2
  exit 1
}

info() {
  printf 'omnipus install: %s\n' "$*"
}

require() {
  command -v "$1" >/dev/null 2>&1 || err "required command '$1' not found"
}

require uname
require tar
require mkdir
require chmod
require rm

# Need a download tool — prefer curl, fall back to wget.
if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -qO "$2" "$1"; }
else
  err "neither curl nor wget is installed"
fi

# Pick the available SHA256 tool.
if command -v sha256sum >/dev/null 2>&1; then
  sha256_of() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_of() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  sha256_of() { echo ""; }  # no verifier available
fi

# ── Detect platform ──────────────────────────────────────────────────────────

OS_RAW="$(uname -s)"
ARCH_RAW="$(uname -m)"

case "$OS_RAW" in
  Linux)  OS="Linux"  ;;
  Darwin) OS="Darwin" ;;
  *)
    err "unsupported OS '$OS_RAW'. v0.1 supports Linux and macOS only. See https://github.com/${REPO}#platform-support"
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)
    ARCH="x86_64"
    [ "$OS" = "Darwin" ] && err "Intel Mac (darwin/amd64) is planned but not in v0.1. See https://github.com/${REPO}#platform-support"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    err "unsupported architecture '$ARCH_RAW'. v0.1 supports x86_64 (Linux) and arm64 (Linux/macOS). See https://github.com/${REPO}#platform-support"
    ;;
esac

PLATFORM="${OS}_${ARCH}"
ARCHIVE_NAME="omnipus_${PLATFORM}.tar.gz"

# ── Resolve URL ──────────────────────────────────────────────────────────────

if [ -n "$EXPLICIT_URL" ]; then
  ARCHIVE_URL="$EXPLICIT_URL"
  CHECKSUMS_URL=""
  info "using explicit OMNIPUS_INSTALL_URL — checksum verification skipped"
else
  if [ -z "$VERSION" ]; then
    BASE_URL="https://github.com/${REPO}/releases/latest/download"
  else
    BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
  fi
  ARCHIVE_URL="${BASE_URL}/${ARCHIVE_NAME}"
  CHECKSUMS_URL="${BASE_URL}/checksums.txt"
fi

# ── Download + verify + extract ──────────────────────────────────────────────

WORK_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t omnipus-install)"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

info "downloading ${ARCHIVE_URL}"
ARCHIVE_PATH="${WORK_DIR}/${ARCHIVE_NAME}"
case "$ARCHIVE_URL" in
  file://*)
    # file:// URLs aren't supported by curl in all environments — strip and cp.
    SRC_PATH="$(printf '%s' "$ARCHIVE_URL" | sed 's|^file://||')"
    cp "$SRC_PATH" "$ARCHIVE_PATH"
    ;;
  *)
    download "$ARCHIVE_URL" "$ARCHIVE_PATH" \
      || err "failed to download $ARCHIVE_URL"
    ;;
esac

if [ -n "$CHECKSUMS_URL" ]; then
  info "verifying checksum"
  CHECKSUMS_PATH="${WORK_DIR}/checksums.txt"
  download "$CHECKSUMS_URL" "$CHECKSUMS_PATH" \
    || err "failed to download $CHECKSUMS_URL"

  EXPECTED="$(awk -v name="$ARCHIVE_NAME" '$2 == name {print $1}' "$CHECKSUMS_PATH")"
  if [ -z "$EXPECTED" ]; then
    err "no checksum entry for $ARCHIVE_NAME in checksums.txt"
  fi

  ACTUAL="$(sha256_of "$ARCHIVE_PATH")"
  if [ -z "$ACTUAL" ]; then
    err "no SHA256 tool available (need sha256sum or shasum); refusing to install unverified binary. Set OMNIPUS_INSTALL_URL to override."
  fi
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    err "checksum mismatch for $ARCHIVE_NAME (expected $EXPECTED, got $ACTUAL)"
  fi
  info "checksum OK"
fi

info "extracting archive"
tar -xzf "$ARCHIVE_PATH" -C "$WORK_DIR"

# ── Verify bundled Chrome integrity (ADR-052 M2) ──────────────────────────────
# A chromium/ directory is present in every Phase 1+ archive that has a
# CfT-published chrome build for its OS+arch (built by the goreleaser
# pre-build hook). When present, refuse to install on hash mismatch — a
# corrupted or substituted Chrome is the exact failure mode SHA-256 pinning
# exists to catch. Bare-binary archives (linux/arm64 today, since CfT
# publishes no linux-arm64 build) skip this check cleanly.
#
# Layout the runtime's findPackageChrome consults:
#   chromium/<fullChromeBinaryRelPath()>
# which on linux resolves to chromium/chrome-linux64/chrome (and would be
# chromium/chrome-linux-arm64/chrome if CfT published one — see cft-bundle.sh
# for the per-arch mapping). We accept either layout defensively.
CHROMIUM_DIR="${WORK_DIR}/chromium"
if [ -d "$CHROMIUM_DIR" ]; then
  # Resolve the chrome binary: try the linux CfT layout first, then the
  # (hypothetical) arm64 CfT layout, then a glob for future darwin/win.
  if [ -x "$CHROMIUM_DIR/chrome-linux64/chrome" ]; then
    CHROME_BIN="$CHROMIUM_DIR/chrome-linux64/chrome"
  elif [ -x "$CHROMIUM_DIR/chrome-linux-arm64/chrome" ]; then
    CHROME_BIN="$CHROMIUM_DIR/chrome-linux-arm64/chrome"
  else
    CHROME_BIN="$(find "$CHROMIUM_DIR" -type f \
      \( -name chrome -o -name 'Google Chrome for Testing' -o -name chrome.exe \) \
      -print 2>/dev/null | head -n 1)"
  fi
  SHA_FILE="${CHROMIUM_DIR}/chrome.sha256"
  if [ ! -f "$SHA_FILE" ]; then
    err "bundled chrome present but chrome.sha256 missing — refusing to install (corrupted archive)"
  fi
  if [ -z "$CHROME_BIN" ] || [ ! -f "$CHROME_BIN" ]; then
    err "bundled chrome.sha256 present but chrome binary missing — refusing to install (corrupted archive)"
  fi
  ACTUAL="$(sha256_of "$CHROME_BIN")"
  if [ -z "$ACTUAL" ]; then
    err "no SHA256 tool available (need sha256sum or shasum); refusing to install unverified chrome. Set OMNIPUS_INSTALL_URL to override."
  fi
  EXPECTED="$(awk '
    # SHA-256 parser (ADR-052 SEC-ADR052-004). POSIX-clean awk: no IGNORECASE,
    # no \xHH escapes, no gawk extensions, no `continue` outside loops (use
    # `next` which is valid in any pattern-action context). Tolerates:
    #   - CRLF line endings (CR stripped)
    #   - "sha256: <hex>" / "SHA-256: <hex>" prefix (CORR2-010: some
    #     toolchains emit the uppercase + hyphen form; the parser must
    #     accept both)
    #   - "# comment" lines
    #   - Whitespace-only lines
    #   - sha256sum text mode ("<hex>  filename") AND binary mode ("<hex> *filename")
    # Rejects (no field matches):
    #   - Uppercase hex (sha256sum + shasum both default lowercase; uppercase
    #     indicates a toolchain mismatch and is surfaced as a parse failure
    #     — consistent with the runtime Go readers)
    #   - Wrong-length digests (no "match by chance")
    {
      line = $0
      # Strip CR (CRLF tolerance).
      sub(/\r$/, "", line)
      # Strip leading whitespace.
      sub(/^[[:space:]]+/, "", line)
      if (line == "") next
      # Skip comment lines.
      if (substr(line, 1, 1) == "#") next
      # Strip optional "sha256:" / "SHA-256:" prefix. POSIX awk has no
      # IGNORECASE and no character-class negation in regex, so we
      # expand the prefix explicitly. The two stripped forms are kept
      # adjacent so a reader can verify both are exercised.
      sub(/^sha256:/, "", line)
      sub(/^SHA-256:/, "", line)
      sub(/^[[:space:]]+/, "", line)
      # Push the stripped line back into $0 so NF and $i see the
      # post-prefix text (sub() on a local variable does NOT update
      # $0/NF in POSIX awk — see CORR2-010 fix-up).
      $0 = line
      nf = split($0, parts, /[[:space:]]+/)
      # Walk fields; emit the first 64-char lowercase-hex one.
      for (i = 1; i <= nf; i++) {
        if (length(parts[i]) == 64 && parts[i] ~ /^[0-9a-f]{64}$/) { print parts[i]; exit }
      }
    }
  ' "$SHA_FILE")"
  if [ -z "$EXPECTED" ]; then
    # Per SEC-ADR052-004: uppercase hex or non-hex chars are toolchain
    # mismatches and must fail closed. The above regex only accepts
    # lowercase hex, so an uppercase or non-hex chrome.sha256 lands here.
    err "could not parse SHA256 from $SHA_FILE (non-lowercase hex or unexpected format — toolchain mismatch)"
  fi
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    err "bundled chrome SHA256 mismatch (expected $EXPECTED, got $ACTUAL) — refusing to install"
  fi
  info "bundled chrome SHA256 OK"
fi

# ── Install ──────────────────────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR" || err "cannot create $INSTALL_DIR (try a writable OMNIPUS_INSTALL_DIR, e.g. \$HOME/.local/bin)"
TARGET="${INSTALL_DIR}/omnipus"
# Atomic-ish install: move into place after chmod.
mv "${WORK_DIR}/omnipus" "${TARGET}.new" \
  || err "cannot write to $INSTALL_DIR (you may need sudo, or set OMNIPUS_INSTALL_DIR=\$HOME/.local/bin)"
chmod +x "${TARGET}.new"
mv -f "${TARGET}.new" "$TARGET"

# ── Lay down chromium/ next to the binary (ADR-052 D2) ───────────────────────
# Compute the runtime's package-Chrome root via the same arithmetic
# pkg/tools/browser/exec_resolver.go uses: <dir(binary)>/../chromium.
# For /usr/local/bin/omnipus → /usr/local/share/omnipus/chromium.
# For $HOME/.local/bin/omnipus → $HOME/.local/share/omnipus/chromium.
if [ -d "$CHROMIUM_DIR" ]; then
  PARENT_DIR="$(dirname "$INSTALL_DIR")"
  SHARE_DIR="${PARENT_DIR}/share/omnipus"
  TARGET_CHROMIUM="${SHARE_DIR}/chromium"
  mkdir -p "$SHARE_DIR" || err "cannot create $SHARE_DIR (you may need sudo)"
  rm -rf "$TARGET_CHROMIUM"
  mv "$CHROMIUM_DIR" "$TARGET_CHROMIUM" \
    || err "cannot move chromium/ to $TARGET_CHROMIUM"
  # Best-effort +x on the chrome binary — it's typically already executable
  # from the tarball, but some filesystems strip the bit during transfer.
  chmod +x "$TARGET_CHROMIUM/chrome-linux64/chrome" 2>/dev/null || true
  chmod +x "$TARGET_CHROMIUM/chrome-linux-arm64/chrome" 2>/dev/null || true
  info "bundled chrome installed at $TARGET_CHROMIUM"
fi

# ── Linux host shared libraries (ADR-052 C2) ─────────────────────────────────
# The bundled full Chrome dynamically links against a documented set of
# host libraries. They are present on a stock desktop; on a minimal server
# (Debian slim, RHEL minimal, Alpine-without-gcompat) they are absent and
# Chrome exits "error while loading shared libraries". install.sh installs
# them via the host package manager when missing — a one-time install step,
# no daemon, no rebuild. linux/arm64 ships without a bundled chrome (CfT
# has no linux-arm64 build), so the post-install managed download is what
# matters there — runtime will need the same host libs.
SHARE_CHROMIUM="${INSTALL_DIR}/../share/omnipus/chromium"
if [ "$OS" = "Linux" ] && { [ -d "$CHROMIUM_DIR" ] || [ -d "$SHARE_CHROMIUM" ]; }; then
  install_host_libs_apt() {
    HOST_LIBS="libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 libgbm1 libxkbcommon0 libxcomposite1 libxdamage1 libxrandr2 libxshmfence1 libasound2 libpango-1.0-0 libcairo2"
    set --
    for lib in $HOST_LIBS; do
      if ! dpkg -s "$lib" >/dev/null 2>&1; then
        set -- "$@" "$lib"
      fi
    done
    if [ "$#" -gt 0 ]; then
      info "installing Chrome host libraries: $*"
      if [ "$(id -u)" -ne 0 ]; then
        sudo apt-get update >/dev/null 2>&1 || true
        sudo apt-get install -y "$@" \
          || err "failed to install host libraries (try: sudo apt-get install $*)"
      else
        apt-get update >/dev/null 2>&1 || true
        apt-get install -y "$@" \
          || err "failed to install host libraries (try: apt-get install $*)"
      fi
    fi
  }
  install_host_libs_dnf() {
    # dnf package names differ slightly (no version suffix on libdrm2/libgbm1;
    # no .0 suffix on libnss3 etc.). Map the apt set to the corresponding dnf
    # names.
    HOST_LIBS_DNF="nss nspr atk at-spi2-atk cups-libs libdrm mesa-libgbm libxkbcommon libXcomposite libXdamage libXrandr libxshmfence alsa-lib pango cairo"
    set --
    for lib in $HOST_LIBS_DNF; do
      if ! rpm -q "$lib" >/dev/null 2>&1; then
        set -- "$@" "$lib"
      fi
    done
    if [ "$#" -gt 0 ]; then
      info "installing Chrome host libraries: $*"
      if [ "$(id -u)" -ne 0 ]; then
        sudo dnf install -y "$@" \
          || err "failed to install host libraries (try: sudo dnf install $*)"
      else
        dnf install -y "$@" \
          || err "failed to install host libraries (try: dnf install $*)"
      fi
    fi
  }
  if command -v apt-get >/dev/null 2>&1; then
    install_host_libs_apt
  elif command -v dnf >/dev/null 2>&1; then
    install_host_libs_dnf
  else
    info "(note: neither apt-get nor dnf detected — Chrome's host shared libraries must be installed manually)"
  fi
fi

info "installed to $TARGET"
if ! printf '%s\n' "$PATH" | tr ':' '\n' | grep -Fxq "$INSTALL_DIR"; then
  info "warning: $INSTALL_DIR is not on your PATH — run with full path or add it"
fi

# Quick sanity check.
if "$TARGET" version >/dev/null 2>&1; then
  info "verifying installed binary:"
  "$TARGET" version | head -1
else
  info "(note: '$TARGET version' did not exit 0 — binary may still work; check 'omnipus --help')"
fi

info "next: run 'omnipus start' and open http://localhost:5000/"

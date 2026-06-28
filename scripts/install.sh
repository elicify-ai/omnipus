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

# ── Install ──────────────────────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR" || err "cannot create $INSTALL_DIR (try a writable OMNIPUS_INSTALL_DIR, e.g. \$HOME/.local/bin)"
TARGET="${INSTALL_DIR}/omnipus"
# Atomic-ish install: move into place after chmod.
mv "${WORK_DIR}/omnipus" "${TARGET}.new" \
  || err "cannot write to $INSTALL_DIR (you may need sudo, or set OMNIPUS_INSTALL_DIR=\$HOME/.local/bin)"
chmod +x "${TARGET}.new"
mv -f "${TARGET}.new" "$TARGET"

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

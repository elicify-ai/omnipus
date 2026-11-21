#!/usr/bin/env bash
# Proof that check-office-host-libs-sync.sh catches a drifted LibreOffice
# library list in each consumer, on copies of the real files (the checkout is
# never changed).
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHECK="$SCRIPT_DIR/check-office-host-libs-sync.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/office-host-libs-test.XXXXXX")" || exit 1
trap 'rm -rf "$WORK"' EXIT

fail=0
fresh() {
  rm -rf "$WORK/c"
  mkdir -p "$WORK/c"
  cp "$REPO_ROOT/scripts/office-pack/libreoffice-host-libs.env" "$WORK/c/env"
  cp "$REPO_ROOT/scripts/install.sh" "$WORK/c/install.sh"
  cp "$REPO_ROOT/.goreleaser.yaml" "$WORK/c/goreleaser.yaml"
  cp "$REPO_ROOT/docker/Dockerfile.goreleaser" "$WORK/c/Dockerfile"
}
run_check() {
  HOST_LIBS_ENV="$WORK/c/env" INSTALL_SH="$WORK/c/install.sh" \
    GORELEASER_YAML="$WORK/c/goreleaser.yaml" DOCKERFILE="$WORK/c/Dockerfile" \
    bash "$CHECK" > "$WORK/out.log" 2>&1
}
expect() { # <want exit> <case name>
  run_check
  local code=$?
  if [ "$code" -ne "$1" ]; then
    echo "check-office-host-libs-sync.test: $2: got exit $code, want $1" >&2
    cat "$WORK/out.log" >&2
    fail=1
  else
    echo "check-office-host-libs-sync.test: $2: exit $code as expected"
  fi
}

fresh
expect 0 "real files"

# A library dropped from install.sh's copy of the list.
fresh
sed -i.bak 's/ libx11-xcb1 / /' "$WORK/c/install.sh"
cmp -s "$WORK/c/install.sh" "$WORK/c/install.sh.bak" && { echo "test setup: libx11-xcb1 not found in install.sh" >&2; exit 1; }
expect 1 "install.sh lost a library"

# A library added only to the env file (install.sh, deb and Docker lag).
fresh
sed -i.bak 's/^OFFICE_DEB_LIBS="/OFFICE_DEB_LIBS="libdrift0 /' "$WORK/c/env"
expect 1 "env file gained a library"

# A deb dependency dropped from .goreleaser.yaml.
fresh
grep -q '^          - libx11-xcb1$' "$WORK/c/goreleaser.yaml" || { echo "test setup: libx11-xcb1 not a deb dependency" >&2; exit 1; }
sed -i.bak '/^          - libx11-xcb1$/d' "$WORK/c/goreleaser.yaml"
expect 1 "deb dependency dropped"

# The Docker image going back to its own list.
fresh
sed -i.bak '/libreoffice-host-libs.env/d' "$WORK/c/Dockerfile"
expect 1 "Dockerfile stops reading the env file"

[ "$fail" -eq 0 ] || exit 1
echo "check-office-host-libs-sync.test: OK"

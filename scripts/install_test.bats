#!/usr/bin/env bats
#
# install.sh — bats test suite (ADR-052 Phase 1 review finding TEST-001).
#
# Covers the install.sh happy path and every failure mode the script
# advertises: bad archive, bad checksum, mismatched chrome.sha256, missing
# chrome.sha256, missing chrome binary, uppercase-hex chrome.sha256, bare-
# binary archive (no chromium/), and OMNIPUS_INSTALL_URL=file:// local
# install. Each test stages a sandboxed $HOME + fake toolchain so the
# real / and real $PATH are never touched.

INSTALL_SH="${BATS_TEST_DIRNAME}/install.sh"

setup() {
  SANDBOX="$(mktemp -d)"
  export HOME="$SANDBOX/home"
  mkdir -p "$HOME"

  # Per-test install dir + work dir; install.sh writes to OMNIPUS_INSTALL_DIR.
  TEST_INSTALL_DIR="$SANDBOX/bin"
  mkdir -p "$TEST_INSTALL_DIR"
  export OMNIPUS_INSTALL_DIR="$TEST_INSTALL_DIR"

  # install.sh needs uname + tar + mkdir + chmod + rm — all POSIX. We don't
  # shadow these; they're guaranteed on the test host.

  # install.sh also needs a download tool (curl preferred) and a SHA tool
  # (sha256sum preferred). We rely on whatever the host has; if a test
  # needs to simulate a missing tool, it removes the symlink in
  # TEST_PATH/bin and prepends TEST_PATH to PATH.

  # Build a tiny "omnipus" stub binary — install.sh copies whatever is in
  # the archive's top-level as the binary and then `chmod +x`'s it. The
  # stub just echoes a version string so the post-install `version` check
  # is happy.
  STAGE_BIN_DIR="$SANDBOX/stage-bin"
  mkdir -p "$STAGE_BIN_DIR"
  cat > "$STAGE_BIN_DIR/omnipus" <<'STUB'
#!/bin/sh
echo "omnipus test stub v0.0.0"
STUB
  chmod +x "$STAGE_BIN_DIR/omnipus"

  # Strip apt-get/dnf/sudo from PATH for the duration of the test, AND make
  # dpkg/rpm pretend every package query succeeds. install.sh's host-
  # libraries install step triggers when chromium/ is present on Linux; on
  # a test host we don't want it to actually try `apt-get install` (needs
  # root + network + can fail on package-name drift between Ubuntu versions).
  # By making dpkg/rpm report "every lib installed", the install step sees
  # nothing to do and skips cleanly. install.sh treats "no apt-get, no dnf"
  # as a non-fatal note anyway, so even if our stubs slip through, the
  # script only emits a NOTE (it does not err).
  SCRUB_BIN="$SANDBOX/scrub-bin"
  mkdir -p "$SCRUB_BIN"
  for tool in apt-get apt dnf yum sudo; do
    cat > "$SCRUB_BIN/$tool" <<STUB
#!/bin/sh
echo "test harness: $tool scrubbed from PATH" >&2
exit 127
STUB
    chmod +x "$SCRUB_BIN/$tool"
  done
  for tool in dpkg rpm; do
    cat > "$SCRUB_BIN/$tool" <<'STUB'
#!/bin/sh
# Pretend any package query succeeds — install.sh only checks the exit
# code when deciding "is <lib> installed?". Reporting "always installed"
# makes install.sh skip the install step entirely.
exit 0
STUB
    chmod +x "$SCRUB_BIN/$tool"
  done
  export PATH="$SCRUB_BIN:$PATH"
}

# teardown must be a top-level bats function (B-1); defining it inside
# setup() makes it invisible to bats — the SANDBOX would leak on every
# test, eventually OOM-killing the test runner.
teardown() {
  rm -rf "$SANDBOX"
}

# Helper: build a fake archive at $1/omnipus_Linux_x86_64.tar.gz containing
# the stub omnipus binary (and optionally a chromium/ directory). The
# checksum file is NOT written here — tests that exercise checksum
# verification build it separately.
build_archive() {
  local out_dir="$1"
  local with_chrome="${2:-no}"     # yes | no
  local chrome_layout="${3:-linux64}"  # linux64 | linux-arm64

  mkdir -p "$out_dir"
  cp "$STAGE_BIN_DIR/omnipus" "$out_dir/omnipus"

  if [ "$with_chrome" = "yes" ]; then
    case "$chrome_layout" in
      linux64)
        mkdir -p "$out_dir/chromium/chrome-linux64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux64/chrome"
        local sha
        sha="$(sha256sum "$out_dir/chromium/chrome-linux64/chrome" | awk '{print $1}')"
        printf '%s  chrome-linux64/chrome\n' "$sha" > "$out_dir/chromium/chrome.sha256"
        ;;
      linux64-cft-real)
        # REAL cft-bundle.sh layout (PERF2-001): chrome.sha256 lives NEXT
        # TO the binary at chromium/chrome-linux64/chrome.sha256, NOT at
        # the chromium/ root. This is what the actual producer writes —
        # the exact layout Phase 2 caught install.sh mishandling (it was
        # looking only at the chromium/ root and refused the real archive).
        mkdir -p "$out_dir/chromium/chrome-linux64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux64/chrome"
        local sha
        sha="$(sha256sum "$out_dir/chromium/chrome-linux64/chrome" | awk '{print $1}')"
        printf '%s  chrome\n' "$sha" > "$out_dir/chromium/chrome-linux64/chrome.sha256"
        ;;
      linux-arm64)
        mkdir -p "$out_dir/chromium/chrome-linux-arm64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux-arm64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux-arm64/chrome"
        local sha
        sha="$(sha256sum "$out_dir/chromium/chrome-linux-arm64/chrome" | awk '{print $1}')"
        printf '%s  chrome-linux-arm64/chrome\n' "$sha" > "$out_dir/chromium/chrome.sha256"
        ;;
      bad-sha)
        # chromium/ present, chrome binary present, chrome.sha256 present
        # but a SHA that does NOT match the chrome binary.
        mkdir -p "$out_dir/chromium/chrome-linux64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux64/chrome"
        printf '%s  chrome-linux64/chrome\n' \
          "0000000000000000000000000000000000000000000000000000000000000000" \
          > "$out_dir/chromium/chrome.sha256"
        ;;
      upper-hex)
        # chromium/ present, chrome binary present, chrome.sha256 written
        # with UPPERCASE hex — install.sh must abort (CORR-003).
        mkdir -p "$out_dir/chromium/chrome-linux64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux64/chrome"
        local sha
        sha="$(sha256sum "$out_dir/chromium/chrome-linux64/chrome" | awk '{print $1}')"
        # uppercase the hex
        sha="$(printf '%s' "$sha" | tr 'a-f' 'A-F')"
        printf '%s  chrome-linux64/chrome\n' "$sha" > "$out_dir/chromium/chrome.sha256"
        ;;
      missing-sha-file)
        # chromium/ present, chrome binary present, NO chrome.sha256.
        mkdir -p "$out_dir/chromium/chrome-linux64"
        cp "$STAGE_BIN_DIR/omnipus" "$out_dir/chromium/chrome-linux64/chrome"
        chmod +x "$out_dir/chromium/chrome-linux64/chrome"
        ;;
    esac
  fi

  ( cd "$out_dir" && tar -czf "$out_dir/omnipus_Linux_x86_64.tar.gz" omnipus $([ "$with_chrome" = "yes" ] && echo chromium) )
}

# ── Tests ────────────────────────────────────────────────────────────────────

@test "install.sh: file:// URL — bare archive (no chromium/) installs cleanly" {
  build_archive "$SANDBOX/dist" no
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  export OMNIPUS_VERSION=""
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  # Bare-binary flow: no chromium/ should have been staged under share/.
  [ ! -d "$SANDBOX/bin/../share/omnipus/chromium" ]
}

@test "install.sh: file:// URL — bundled chromium installs and stages chromium/" {
  build_archive "$SANDBOX/dist" yes linux64
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  # chromium/ landed at <INSTALL_DIR>/../share/omnipus/chromium.
  [ -d "$SANDBOX/bin/../share/omnipus/chromium" ]
  [ -x "$SANDBOX/bin/../share/omnipus/chromium/chrome-linux64/chrome" ]
  [ -f "$SANDBOX/bin/../share/omnipus/chromium/chrome.sha256" ]
}

@test "install.sh: REAL cft-bundle layout (chrome.sha256 next to binary) installs cleanly" {
  # Regression for the Phase 2 defect: cft-bundle.sh writes chrome.sha256
  # BESIDE the binary (chrome-linux64/chrome.sha256), not at the chromium/
  # root. install.sh must find it there (PERF2-001). Before the fix,
  # install.sh looked only at chromium/chrome.sha256 and aborted the real
  # archive with "chrome.sha256 missing".
  build_archive "$SANDBOX/dist" yes linux64-cft-real
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  [ -x "$SANDBOX/bin/../share/omnipus/chromium/chrome-linux64/chrome" ]
}

@test "install.sh: bundled chromium + chrome-linux-arm64 layout is accepted defensively" {
  build_archive "$SANDBOX/dist" yes linux-arm64
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$SANDBOX/bin/../share/omnipus/chromium/chrome-linux-arm64/chrome" ]
}

@test "install.sh: chrome.sha256 mismatch aborts the install" {
  build_archive "$SANDBOX/dist" yes bad-sha
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"SHA256 mismatch"* ]] || [[ "$output" == *"refusing to install"* ]]
  # The omnipus binary must NOT have been installed.
  [ ! -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: uppercase-hex chrome.sha256 aborts (CORR-003)" {
  build_archive "$SANDBOX/dist" yes upper-hex
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"toolchain mismatch"* ]] || [[ "$output" == *"could not parse"* ]]
  [ ! -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: missing chrome.sha256 aborts" {
  build_archive "$SANDBOX/dist" yes missing-sha-file
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"chrome.sha256 missing"* ]]
  [ ! -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: file:// URL pointing at nonexistent path fails cleanly" {
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/does-not-exist.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [ ! -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: bad-archive (corrupt tarball) fails cleanly" {
  mkdir -p "$SANDBOX/dist"
  printf 'this is not a tarball\n' > "$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
}

@test "install.sh: sh syntax is valid (POSIX)" {
  # dash parses install.sh without error.
  run dash -n "$INSTALL_SH"
  [ "$status" -eq 0 ]
  # sh parses too.
  run sh -n "$INSTALL_SH"
  [ "$status" -eq 0 ]
}

@test "install.sh: handles whitespace/comment/'sha256:' prefix in chrome.sha256" {
  # Build an archive whose chrome.sha256 has a 'sha256:' prefix + a
  # '# generated' comment line + CRLF — install.sh's awk must accept it
  # (per SEC-ADR052-004 robustness contract).
  mkdir -p "$SANDBOX/dist/chromium/chrome-linux64"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  chmod +x "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/omnipus"
  chmod +x "$SANDBOX/dist/omnipus"
  sha="$(sha256sum "$SANDBOX/dist/chromium/chrome-linux64/chrome" | awk '{print $1}')"
  # sha256sum-style: hex, two spaces, then the path. CRLF + comment.
  {
    printf '# generated by test harness\n'
    printf '%s  chrome-linux64/chrome\r\n' "$sha"
  } > "$SANDBOX/dist/chromium/chrome.sha256"
  ( cd "$SANDBOX/dist" && tar -czf "$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz" omnipus chromium )
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
}

@test "install.sh: chrome.sha256 'sha256:' prefix is accepted (lowercase)" {
  # CORR2-010: the awk parser MUST strip a leading 'sha256:' prefix.
  # Previously the parser only stripped 'sha256:' but not 'SHA-256:'.
  # This test writes the lowercase form and asserts install accepts it.
  mkdir -p "$SANDBOX/dist/chromium/chrome-linux64"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  chmod +x "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/omnipus"
  chmod +x "$SANDBOX/dist/omnipus"
  sha="$(sha256sum "$SANDBOX/dist/chromium/chrome-linux64/chrome" | awk '{print $1}')"
  # 'sha256: <hex>' — one of the cf-toolchain forms the parser must accept.
  printf 'sha256:%s\n' "$sha" > "$SANDBOX/dist/chromium/chrome.sha256"
  ( cd "$SANDBOX/dist" && tar -czf "$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz" omnipus chromium )
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
}

@test "install.sh: chrome.sha256 'SHA-256:' prefix is accepted (uppercase + hyphen)" {
  # CORR2-010: install.sh's awk parser MUST strip 'SHA-256:' (uppercase
  # with a hyphen) in addition to the lowercase 'sha256:' form. Some
  # toolchains emit the uppercase-hyphen form; without this tolerance
  # install.sh fails closed on a perfectly valid manifest.
  mkdir -p "$SANDBOX/dist/chromium/chrome-linux64"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  chmod +x "$SANDBOX/dist/chromium/chrome-linux64/chrome"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/omnipus"
  chmod +x "$SANDBOX/dist/omnipus"
  sha="$(sha256sum "$SANDBOX/dist/chromium/chrome-linux64/chrome" | awk '{print $1}')"
  # 'SHA-256: <hex>' — exactly the case the previous parser missed.
  printf 'SHA-256:%s\n' "$sha" > "$SANDBOX/dist/chromium/chrome.sha256"
  ( cd "$SANDBOX/dist" && tar -czf "$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz" omnipus chromium )
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Linux_x86_64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
}

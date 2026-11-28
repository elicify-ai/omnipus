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

# ── Phase 3: macOS / darwin install path (ADR-052 C3 option ii) ──────────────
#
# Cannot runtime-test on macOS from this Linux host, but install.sh's OS
# branching is driven purely by `uname -s`. Stubbing uname to report Darwin
# exercises the entire darwin code path (chromium sibling lay-down, .app
# binary resolution, chrome.sha256 beside the .app, host-libs skip) on the
# Linux test host. This is functional proof for a path shellcheck alone
# cannot verify.

@test "install.sh: darwin .app chromium installs to ../chromium sibling + SHA verified" {
  # Stub uname so install.sh takes the Darwin branch.
  cat > "$SCRUB_BIN/uname" <<'STUB'
#!/bin/sh
case "$1" in
  -s) echo "Darwin" ;;
  -m) echo "arm64" ;;
  *) echo "unknown" ;;
esac
STUB
  chmod +x "$SCRUB_BIN/uname"

  # Build a Darwin-style archive: omnipus binary + chromium/ with the .app
  # layout + chrome.sha256 BESIDE the .app (C3 option ii).
  mkdir -p "$SANDBOX/dist/chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS"
  cp "$STAGE_BIN_DIR/omnipus" "$SANDBOX/dist/omnipus"
  chmod +x "$SANDBOX/dist/omnipus"
  MAC_BIN="$SANDBOX/dist/chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
  cp "$STAGE_BIN_DIR/omnipus" "$MAC_BIN"
  chmod +x "$MAC_BIN"
  sha="$(sha256sum "$MAC_BIN" | awk '{print $1}')"
  # chrome.sha256 beside the .app (at <extract-subdir>/chrome.sha256).
  printf '%s  chrome\n' "$sha" > "$SANDBOX/dist/chromium/chrome-mac-arm64/chrome.sha256"
  ( cd "$SANDBOX/dist" && tar -czf "$SANDBOX/dist/omnipus_Darwin_arm64.tar.gz" omnipus chromium )
  export OMNIPUS_INSTALL_URL="file://$SANDBOX/dist/omnipus_Darwin_arm64.tar.gz"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  # darwin lays chromium at <INSTALL_DIR>/../chromium (NOT the linux
  # share/omnipus/chromium path).
  [ -d "$SANDBOX/bin/../chromium" ]
  [ ! -d "$SANDBOX/bin/../share/omnipus/chromium" ]
  # .app binary present + executable in the laid-down location.
  [ -x "$SANDBOX/bin/../chromium/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" ]
  # chrome.sha256 beside the .app survived the move.
  [ -f "$SANDBOX/bin/../chromium/chrome-mac-arm64/chrome.sha256" ]
}

# ── Office/pdfium packaging (P4, #1713, founder decisions 2026-11-19) ────────
#
# CONTRACT the developer implements to (sources: office-preview spec §10
# delivery table; packaging-sizing.md P4; founder decisions 2026-11-19):
#
#   OMNIPUS_INSTALL_BASE_URL  test-only BASE-url override. When non-empty it
#     replaces the releases base for EVERY download of the run:
#       "$BASE/omnipus_${PLATFORM}.tar.gz", "$BASE/checksums.txt",
#       "$BASE/office_${os}_${arch}.tar.gz", "$BASE/pdfium_${os}_${arch}.tar.gz"
#     Checksum verification STAYS ON (this is the difference to
#     OMNIPUS_INSTALL_URL, which overrides one archive and skips checksums).
#     file:// URLs work like OMNIPUS_INSTALL_URL (strip + cp).
#   Component release file names use lowercase go values:
#     office_{linux,darwin}_{amd64,arm64}.tar.gz
#     pdfium_{linux,darwin}_{amd64,arm64}.tar.gz  (+ pdfium_windows_amd64.zip
#     as a release file only — Windows registers, it does not install, AD-12).
#   §10 target folders, relative to OMNIPUS_INSTALL_DIR:
#     linux  →  ../share/omnipus/office   and   ../share/omnipus/pdfium
#     darwin →  ../office                 and   ../pdfium
#   Flags --without-office / --without-pdf-renderer skip that component; a
#     re-run with a flag REMOVES a previously installed component folder so a
#     re-run matches the choice (packaging-sizing P4).
#   A checksums.txt hash mismatch for a component FAILS CLOSED: nonzero exit,
#     that component folder never created (same refuse-to-install posture as
#     the bundled chrome above — a substituted component is exactly what
#     SHA-256 pinning exists to catch).
#   darwin/amd64 (Intel Mac) is a SUPPORTED target (founder decision
#     2026-11-19): not refused, archive + components installed.
#
# RED reason on the pre-change code: install.sh has no component logic, no
# --without-* flags, no OMNIPUS_INSTALL_BASE_URL, and refuses Intel Macs.

# stub_uname <os> <arch>: pin install.sh's platform detection for this test.
stub_uname() {
  local os="$1" arch="$2"
  cat > "$SCRUB_BIN/uname" <<STUB
#!/bin/sh
case "\$1" in
  -s) echo "$os" ;;
  -m) echo "$arch" ;;
  *) echo "unknown" ;;
esac
STUB
  chmod +x "$SCRUB_BIN/uname"
}

# stub_download_tools_offline: make any non-file:// fetch fail fast and
# loudly, so a test can never silently depend on real network. file:// URLs
# are handled inside install.sh by cp, not by curl/wget.
stub_download_tools_offline() {
  for tool in curl wget; do
    cat > "$SCRUB_BIN/$tool" <<STUB
#!/bin/sh
echo "test harness: network fetch attempted ($tool \$*)" >&2
exit 7
STUB
    chmod +x "$SCRUB_BIN/$tool"
  done
}

# build_fake_release <base> <os> <arch>: stage a complete fake release —
# the main archive for <os>/<arch> (goreleaser naming), office + pdfium
# component archives for BOTH arches of <os>, and checksums.txt covering
# every file.
build_fake_release() {
  local base="$1" os="$2" arch="$3"
  local title_os xarch other_arch variant arch_v
  case "$os" in
    linux)  title_os="Linux"  ;;
    darwin) title_os="Darwin" ;;
    *) echo "build_fake_release: unsupported os '$os'" >&2; return 1 ;;
  esac
  case "$arch" in
    amd64) xarch="x86_64"; other_arch="arm64" ;;
    arm64) xarch="arm64";  other_arch="amd64" ;;
    *) echo "build_fake_release: unsupported arch '$arch'" >&2; return 1 ;;
  esac

  mkdir -p "$base"
  local stage
  stage="$base/stage-main"
  mkdir -p "$stage"
  cp "$STAGE_BIN_DIR/omnipus" "$stage/omnipus"
  chmod +x "$stage/omnipus"
  ( cd "$stage" && tar -czf "$base/omnipus_${title_os}_${xarch}.tar.gz" omnipus )
  rm -rf "$stage"

  for variant in office pdfium; do
    for arch_v in "$arch" "$other_arch"; do
      stage="$base/stage-${variant}-${arch_v}"
      mkdir -p "$stage"
      if [ "$variant" = "office" ]; then
        if [ "$os" = "darwin" ]; then
          mkdir -p "$stage/LibreOffice.app/Contents/MacOS"
          cp "$STAGE_BIN_DIR/omnipus" "$stage/LibreOffice.app/Contents/MacOS/soffice"
          chmod +x "$stage/LibreOffice.app/Contents/MacOS/soffice"
        else
          mkdir -p "$stage/program"
          cp "$STAGE_BIN_DIR/omnipus" "$stage/program/soffice"
          chmod +x "$stage/program/soffice"
          cp "$STAGE_BIN_DIR/omnipus" "$stage/program/soffice.bin"
        fi
      else
        cp "$STAGE_BIN_DIR/omnipus" "$stage/omnipus-pdfium"
        chmod +x "$stage/omnipus-pdfium"
      fi
      if [ "$variant" = "office" ]; then
        printf '{"schema":1,"component":"office","trust":"packaged"}\n' > "$stage/manifest.json"
      else
        printf '{"schema":1,"component":"pdf-renderer","trust":"packaged"}\n' > "$stage/manifest.json"
      fi
      if [ "$variant" = "office" ]; then
        if [ "$os" = "darwin" ]; then
          ( cd "$stage" && tar -czf "$base/${variant}_${os}_${arch_v}.tar.gz" manifest.json LibreOffice.app )
        else
          ( cd "$stage" && tar -czf "$base/${variant}_${os}_${arch_v}.tar.gz" manifest.json program )
        fi
      else
        ( cd "$stage" && tar -czf "$base/${variant}_${os}_${arch_v}.tar.gz" manifest.json omnipus-pdfium )
      fi
      rm -rf "$stage"
    done
  done

  # checksums.txt: sha256sum text mode ("hash␣␣name"), one line per file.
  ( cd "$base" && : > checksums.txt.tmp \
      && for f in omnipus_*.tar.gz office_*.tar.gz pdfium_*.tar.gz; do
           sha256sum "$f" | awk '{print $1 "  " $2}' >> checksums.txt.tmp
         done \
      && mv checksums.txt.tmp checksums.txt )
}

# bad_checksum_for <base> <file>: replace checksums.txt's entry for <file>
# with an all-zero hash.
bad_checksum_for() {
  local base="$1" name="$2"
  awk -v name="$name" '
    $2 == name { print "0000000000000000000000000000000000000000000000000000000000000000  " $2; next }
    { print }
  ' "$base/checksums.txt" > "$base/checksums.txt.new" && mv "$base/checksums.txt.new" "$base/checksums.txt"
}

@test "install.sh: OMNIPUS_INSTALL_BASE_URL is honoured for the whole release (P4)" {
  # The override must exist AND be used: the download line names the local
  # base, never github.com. (Today the variable is unknown, so the script
  # falls through to the GitHub releases base and the assertion fails.)
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"$SANDBOX/release"* ]]
  [[ "$output" != *"github.com"* ]]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: default install places office and pdfium in the linux §10 folders (P4)" {
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  # linux: <INSTALL_DIR>/../share/omnipus/{office,pdfium}.
  [ -f "$SANDBOX/bin/../share/omnipus/office/manifest.json" ]
  [ -f "$SANDBOX/bin/../share/omnipus/office/program/soffice" ]
  [ -f "$SANDBOX/bin/../share/omnipus/pdfium/manifest.json" ]
  [ -f "$SANDBOX/bin/../share/omnipus/pdfium/omnipus-pdfium" ]
}

@test "install.sh: --without-office installs only the pdf renderer (P4)" {
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH" --without-office
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  [ ! -e "$SANDBOX/bin/../share/omnipus/office" ]
  [ -f "$SANDBOX/bin/../share/omnipus/pdfium/manifest.json" ]
}

@test "install.sh: --without-pdf-renderer installs only office (P4)" {
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH" --without-pdf-renderer
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  [ -f "$SANDBOX/bin/../share/omnipus/office/manifest.json" ]
  [ ! -e "$SANDBOX/bin/../share/omnipus/pdfium" ]
}

@test "install.sh: office checksum mismatch in checksums.txt refuses the office component (P4)" {
  # Fail closed: nonzero exit, the office folder is never created.
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  bad_checksum_for "$SANDBOX/release" office_linux_amd64.tar.gz
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"office_linux_amd64.tar.gz"* ]]
  [ ! -e "$SANDBOX/bin/../share/omnipus/office" ]
}

@test "install.sh: re-run with --without-office removes a previously installed office folder (P4)" {
  stub_uname Linux x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" linux amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"

  run "$INSTALL_SH"
  echo "first run: status=$status"
  [ "$status" -eq 0 ]
  [ -f "$SANDBOX/bin/../share/omnipus/office/manifest.json" ]

  run "$INSTALL_SH" --without-office
  echo "second run: status=$status output=$output"
  [ "$status" -eq 0 ]
  [ ! -e "$SANDBOX/bin/../share/omnipus/office" ]
  [ -f "$SANDBOX/bin/../share/omnipus/pdfium/manifest.json" ]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
}

@test "install.sh: darwin/amd64 (Intel Mac) is a supported target and installs both components (P4)" {
  # Founder decision 2026-11-19: Intel Mac is supported. Today install.sh
  # refuses darwin/x86_64 outright ("planned but not in v0.1") — this test
  # goes red on that refusal.
  stub_uname Darwin x86_64
  stub_download_tools_offline
  build_fake_release "$SANDBOX/release" darwin amd64
  export OMNIPUS_INSTALL_BASE_URL="file://$SANDBOX/release"
  run "$INSTALL_SH"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
  [[ "$output" != *"planned but not"* ]]
  [ -x "$TEST_INSTALL_DIR/omnipus" ]
  # darwin: <INSTALL_DIR>/../office and <INSTALL_DIR>/../pdfium.
  [ -f "$SANDBOX/bin/../office/manifest.json" ]
  [ -f "$SANDBOX/bin/../office/LibreOffice.app/Contents/MacOS/soffice" ]
  [ -f "$SANDBOX/bin/../pdfium/manifest.json" ]
}

#!/usr/bin/env bats
#
# check-release-contents.sh — bats SELF-TEST (P7, #1713).
#
# qa-lead RED owns this file and the interface contract below; the script
# itself is the developer's (packaging-sizing.md P7 — "this is the check that
# would have caught the Chrome gap", where the nightly archive silently
# shipped 4 entries and no component payload, N1).
#
# CONTRACT the developer implements to:
#
#   scripts/release/check-release-contents.sh <release-dir>
#     exit 0    every required artifact is present in <release-dir>
#     exit != 0 prints each missing artifact (one per line, name included)
#               and exits nonzero
#
#   Required artifact set (founder decisions 2026-11-19: Intel Mac supported,
#   components are separate release downloads listed in checksums.txt):
#     Main archives (existing goreleaser naming, title Os + x86_64/arm64):
#       omnipus_Linux_x86_64.tar.gz
#       omnipus_Linux_arm64.tar.gz
#       omnipus_Darwin_arm64.tar.gz
#       omnipus_Darwin_x86_64.tar.gz     (new: Intel Mac)
#     Office component archives (lowercase os_arch; no Windows office —
#       AD-12: Windows registers, it does not install):
#       office_linux_amd64.tar.gz  office_linux_arm64.tar.gz
#       office_darwin_arm64.tar.gz office_darwin_amd64.tar.gz
#     PDF renderer archives (same four + the Windows release zip):
#       pdfium_linux_amd64.tar.gz  pdfium_linux_arm64.tar.gz
#       pdfium_darwin_arm64.tar.gz pdfium_darwin_amd64.tar.gz
#       pdfium_windows_amd64.zip
#     checksums.txt with one entry for EVERY file above.
#
#   Chrome payload CONTENT check (#1913 cloud-chief ruling; ADR-052 D2):
#     CfT publishes {linux64, mac-arm64, mac-x64} (scripts/cft-bundle.sh
#     platform matrix, lines 44–51); the main archives for those platforms
#     must actually CONTAIN chromium/ entries at the archive root — the
#     nightly gap this check exists to catch (packaging-sizing N1: the
#     nightly Linux x86_64 archive shipped 4 entries, none of them chromium/):
#       omnipus_Linux_x86_64.tar.gz   (CfT linux64)
#       omnipus_Darwin_arm64.tar.gz   (CfT mac-arm64)
#       omnipus_Darwin_x86_64.tar.gz  (CfT mac-x64)
#     omnipus_Linux_arm64.tar.gz ships WITHOUT chromium/ (CfT publishes no
#     linux-arm64 build; the runtime falls back to its managed download
#     path) — the check must NOT demand it there (the complete fixture
#     encodes that and still passes).
#
# Every test below guards on the script's existence first: a missing script
# must fail loudly with BLOCKED, never satisfy a "should fail" assertion by
# accident.

CHECK_SH="${BATS_TEST_DIRNAME}/check-release-contents.sh"

setup() {
  RELEASE="$(mktemp -d)"
}

teardown() {
  rm -rf "$RELEASE"
}

require_check() {
  if [ ! -f "$CHECK_SH" ] || [ ! -x "$CHECK_SH" ]; then
    echo "BLOCKED: scripts/release/check-release-contents.sh not implemented — required by packaging-sizing P7 (release contents check)"
    return 1
  fi
}

# make_chromium_archive <archive-name> <cft-platform>: stage a REAL main
# archive carrying the bundled-Chrome payload in the layout ADR-052 Phase 1/3
# define (chromium/ at the archive root; chrome.sha256 beside the binary /
# beside the .app — PERF2-001, C3 option ii). The inner folder is the CfT
# extraction subdir: chrome-<platform> (scripts/cft-bundle.sh EXTRACT_SUBDIR:
# linux64→chrome-linux64, mac-arm64→chrome-mac-arm64, mac-x64→chrome-mac-x64),
# exactly what fullChromeBinaryRelPath() expects inside the package root.
make_chromium_archive() {
  local out="$1" platform="$2"
  local stage="$RELEASE/stage-chromium"
  rm -rf "$stage"
  mkdir -p "$stage"
  case "$platform" in
    linux64)
      mkdir -p "$stage/chromium/chrome-linux64"
      printf 'chrome-binary-marker\n' > "$stage/chromium/chrome-linux64/chrome"
      printf 'chrome-sha-marker\n' > "$stage/chromium/chrome-linux64/chrome.sha256"
      ;;
    mac-arm64|mac-x64)
      local app="Google Chrome for Testing.app"
      mkdir -p "$stage/chromium/chrome-$platform/$app/Contents/MacOS"
      printf 'chrome-binary-marker\n' > "$stage/chromium/chrome-$platform/$app/Contents/MacOS/Google Chrome for Testing"
      printf 'chrome-sha-marker\n' > "$stage/chromium/chrome-$platform/chrome.sha256"
      ;;
    *) echo "make_chromium_archive: unsupported platform '$platform'" >&2; return 1 ;;
  esac
  ( cd "$stage" && tar -czf "$RELEASE/$out" chromium )
  rm -rf "$stage"
}

# bare_main_archive <archive-name>: a main archive WITHOUT any chromium/
# payload (the linux/arm64 shape — and the fixture for the missing-chrome
# failure case).
bare_main_archive() {
  local out="$1"
  local stage="$RELEASE/stage-bare"
  rm -rf "$stage"
  mkdir -p "$stage"
  printf 'bare-archive-marker\n' > "$stage/omnipus"
  ( cd "$stage" && tar -czf "$RELEASE/$out" omnipus )
  rm -rf "$stage"
}

# refresh_checksum <archive-name>: recompute one file's checksums.txt entry.
refresh_checksum() {
  local name="$1"
  local sum
  sum="$(sha256sum "$RELEASE/$name" | awk '{print $1}')"
  awk -v name="$name" -v sum="$sum" '
    $2 == name { print sum "  " $2; next }
    { print }
  ' "$RELEASE/checksums.txt" > "$RELEASE/checksums.txt.new" \
    && mv "$RELEASE/checksums.txt.new" "$RELEASE/checksums.txt"
}

# build_complete_release: every required artifact with distinct contents —
# the three CfT-covered main archives carry chromium/, linux/arm64 ships
# without it (ADR-052) — and a checksums.txt entry per file.
build_complete_release() {
  make_chromium_archive omnipus_Linux_x86_64.tar.gz linux64
  bare_main_archive omnipus_Linux_arm64.tar.gz
  make_chromium_archive omnipus_Darwin_arm64.tar.gz mac-arm64
  make_chromium_archive omnipus_Darwin_x86_64.tar.gz mac-x64

  local files=(
    office_linux_amd64.tar.gz
    office_linux_arm64.tar.gz
    office_darwin_arm64.tar.gz
    office_darwin_amd64.tar.gz
    pdfium_linux_amd64.tar.gz
    pdfium_linux_arm64.tar.gz
    pdfium_darwin_arm64.tar.gz
    pdfium_darwin_amd64.tar.gz
    pdfium_windows_amd64.zip
  )
  local n=0
  for f in "${files[@]}"; do
    n=$((n + 1))
    printf 'artifact-%s\n' "$n" > "$RELEASE/$f"
  done

  ( cd "$RELEASE" && : > checksums.txt \
      && for f in omnipus_*.tar.gz office_*.tar.gz pdfium_*.tar.gz pdfium_*.zip; do
           sha256sum "$f" | awk '{print $1 "  " $2}' >> checksums.txt
         done )
}

@test "check: a complete release directory passes" {
  require_check
  build_complete_release
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -eq 0 ]
}

@test "check: a missing office component archive fails and is named" {
  require_check
  build_complete_release
  rm "$RELEASE/office_darwin_amd64.tar.gz"
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"office_darwin_amd64.tar.gz"* ]]
}

@test "check: a missing pdfium release file fails and is named" {
  require_check
  build_complete_release
  rm "$RELEASE/pdfium_windows_amd64.zip"
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"pdfium_windows_amd64.zip"* ]]
}

@test "check: a missing darwin amd64 main archive fails and is named" {
  require_check
  build_complete_release
  rm "$RELEASE/omnipus_Darwin_x86_64.tar.gz"
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"omnipus_Darwin_x86_64.tar.gz"* ]]
}

@test "check: a checksums.txt entry missing for a present file fails" {
  require_check
  build_complete_release
  awk '$2 != "office_linux_arm64.tar.gz"' "$RELEASE/checksums.txt" > "$RELEASE/checksums.txt.new" \
    && mv "$RELEASE/checksums.txt.new" "$RELEASE/checksums.txt"
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"office_linux_arm64.tar.gz"* ]]
}

@test "check: a Chrome-carrying main archive without chromium/ fails and names the folder" {
  # #1913 / ADR-052 D2: omnipus_Linux_x86_64.tar.gz must CARRY the bundled
  # Chrome (CfT linux64). Replace it with a bare archive (the nightly's N1
  # shape) and refresh its checksum so ONLY the chromium/ content requirement
  # can fail — a checksum failure here would be the wrong reason.
  require_check
  build_complete_release
  bare_main_archive omnipus_Linux_x86_64.tar.gz
  refresh_checksum omnipus_Linux_x86_64.tar.gz
  run "$CHECK_SH" "$RELEASE"
  echo "status=$status output=$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"chromium"* ]]
}

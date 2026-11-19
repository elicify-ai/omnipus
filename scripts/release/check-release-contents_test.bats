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

# build_complete_release: every required artifact with distinct contents, and
# a checksums.txt entry per file.
build_complete_release() {
  local files=(
    omnipus_Linux_x86_64.tar.gz
    omnipus_Linux_arm64.tar.gz
    omnipus_Darwin_arm64.tar.gz
    omnipus_Darwin_x86_64.tar.gz
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
      && for f in "${files[@]}"; do
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

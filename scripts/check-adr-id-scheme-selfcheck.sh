#!/usr/bin/env bash
# Exercise both diff-guard companions inside a disposable enclosing repository.
# Repeated random values, bad allocator output, or a setup error must never
# change that repository's HEAD, branch, refs, index, or working tree.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/guard-companion-containment.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
WORK="$(cd "$WORK" && pwd -P)"
mkdir -p "$WORK/home" "$WORK/tmp"
export HOME="$WORK/home" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
# A caller's Git environment must not redirect fixture operations.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR

PASS=0
FAIL=0
assert_equal() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$expected" == "$actual" ]]; then
    printf '  PASS [%s]\n' "$label"
    PASS=$((PASS + 1))
  else
    printf '  FAIL [%s]: expected <%s>, got <%s>\n' "$label" "$expected" "$actual"
    FAIL=$((FAIL + 1))
  fi
}

# Only the external allocation/randomness/Git process edges are injected.
# The real companion, guard, generator, and repositories remain in use.
cat > "$WORK/inject.bash" <<'EOF'
case "$0" in
  */check-adr-id-scheme.test.sh|*/check-spec-status.test.sh)
    unset RANDOM
    RANDOM=13442
    mktemp() {
      case "$*" in
        *'/repo.XXXXXX')
          case "$FIXTURE_FAULT" in
            relative) printf 'relative-fixture\n'; return 0 ;;
            enclosing) printf '%s\n' "$ENCLOSING_REPO"; return 0 ;;
          esac
          ;;
      esac
      command mktemp "$@"
    }
    git() {
      if [[ "$FIXTURE_FAULT" == setup && "${3:-}" == init ]]; then
        printf 'injected git init failure\n' >&2
        return 73
      fi
      command git "$@"
    }
    ;;
esac
EOF

check_companion() {
  local name="$1" fault="$2" expected_exit="$3" expected_error="$4"
  local enclosing before_head before_branch before_refs before_index before_status
  local temp_parent="$WORK/tmp" rc=0
  enclosing="$(mktemp -d "$WORK/enclosing.XXXXXX")"
  git -C "$enclosing" init --quiet --initial-branch=main
  git -C "$enclosing" config user.email test@example.invalid
  git -C "$enclosing" config user.name 'guard containment test'
  printf 'enclosing repository sentinel\n' > "$enclosing/README.md"
  git -C "$enclosing" add -A
  git -C "$enclosing" commit --quiet -m 'enclosing repository baseline'
  git -C "$enclosing" checkout --quiet -b enclosing-work
  if [[ "$fault" == relative-temp ]]; then
    mkdir "$enclosing/tmp"
    temp_parent=tmp
  fi
  before_head="$(git -C "$enclosing" rev-parse HEAD)"
  before_branch="$(git -C "$enclosing" symbolic-ref HEAD)"
  before_refs="$(git -C "$enclosing" for-each-ref --format='%(refname) %(objectname)')"
  before_index="$(git -C "$enclosing" ls-files --stage)"
  before_status="$(git -C "$enclosing" status --porcelain --untracked-files=all)"

  printf '\nContainment: %s (%s)\n' "$name" "$fault"
  (
    cd "$enclosing"
    BASH_ENV="$WORK/inject.bash" TMPDIR="$temp_parent" \
      FIXTURE_FAULT="$fault" ENCLOSING_REPO="$enclosing" \
      bash "$SCRIPT_DIR/$name"
  ) > "$WORK/companion.log" 2>&1 || rc=$?
  assert_equal "$name/$fault/exit" "$expected_exit" "$rc"
  if [[ "$rc" != "$expected_exit" ]]; then
    cat "$WORK/companion.log"
  fi
  if [[ -n "$expected_error" ]]; then
    if grep -qF -- "$expected_error" "$WORK/companion.log"; then
      assert_equal "$name/$fault/diagnostic" present present
    else
      assert_equal "$name/$fault/diagnostic" "$expected_error" missing
      cat "$WORK/companion.log"
    fi
  fi
  assert_equal "$name/$fault/head" "$before_head" "$(git -C "$enclosing" rev-parse HEAD)"
  assert_equal "$name/$fault/branch" "$before_branch" "$(git -C "$enclosing" symbolic-ref HEAD)"
  assert_equal "$name/$fault/refs" "$before_refs" "$(git -C "$enclosing" for-each-ref --format='%(refname) %(objectname)')"
  assert_equal "$name/$fault/index" "$before_index" "$(git -C "$enclosing" ls-files --stage)"
  assert_equal "$name/$fault/status" "$before_status" "$(git -C "$enclosing" status --porcelain --untracked-files=all)"
}

for companion in check-adr-id-scheme.test.sh check-spec-status.test.sh; do
  check_companion "$companion" random 0 ''
  check_companion "$companion" relative-temp 0 ''
  check_companion "$companion" relative 1 'fixture path must be absolute'
  check_companion "$companion" enclosing 1 'fixture path is outside temp root'
  check_companion "$companion" setup 1 'injected git init failure'
done

printf '\nResults: %s passed, %s failed\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]

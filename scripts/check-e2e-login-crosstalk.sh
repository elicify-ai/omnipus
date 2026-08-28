#!/usr/bin/env bash
# check-e2e-login-crosstalk.sh
#
# Regression guard: no E2E spec may call POST /api/v1/auth/login.
#
# WHY
#
# `HandleLogin` (pkg/gateway/rest_auth.go) does TWO things, and a spec that
# wants REST auth only ever wants the first: it returns a bearer token, AND it
# re-mints the SINGLE-SLOT `session_token_hash` on the account ("Session-cookie
# token remains single-slot ... overwrite as before"). That second effect
# silently invalidates the `omnipus-session` cookie in the shared storageState
# file — for every spec that runs AFTER the offender.
#
# THE INCIDENT (2026-08-28, release/v0.1.1)
#
# calendar.spec.ts and calendar-recurrence.spec.ts each logged in purely to get
# a bearer token for REST setup. Playwright runs spec FILES alphabetically, so
# both sat between auth.spec.ts (whose afterAll refreshes storageState) and
# create-agent.spec.ts — which then ran with a dead cookie and failed 3 tests on
# all 3 attempts.
#
# It stayed invisible on every route but one. The e2e gateway runs with
# `gateway.dev_mode_bypass: true`, and checkBearerAuth (pkg/gateway/auth.go)
# short-circuits to the synthetic `_dev_bypass` identity BEFORE its cookie
# branch for any request lacking an `Authorization: Bearer` header — i.e. every
# SPA request since ADR-044. So all `withAuth` routes returned 200 without ever
# testing the cookie. `GET /api/v1/providers` is `withOptionalAuth` +
# `requireAuthOutsideOnboarding`, which deliberately does NOT honour bypass, so
# it alone 401'd. In the UI that surfaced as the create-agent wizard's model
# picker rendering `role="alert"` instead of a combobox — a symptom three steps
# removed from its cause.
#
# THE RULE
#
# Specs that need admin REST auth use `newAdminApiContext()`
# (tests/e2e/fixtures/admin-api.ts), which authenticates with the EXISTING
# shared session cookie and mints nothing.
#
# SANCTIONED CALLERS (see SANCTIONED_FILES below) are the places whose job IS
# logging in: global-setup.ts establishes the shared session, auth.spec.ts tests
# the login flow itself and refreshes storageState in an afterAll, and
# fixtures/login.ts is the shared UI-login helper.
#
# WHAT IS MATCHED
#
# Any CODE reference to `auth/login` in tests/e2e. Lines that are pure comment
# (`//`, `*`, `/*`) are deliberately NOT matched, so prose explaining this rule
# — including admin-api.ts's header — does not trip the guard. This is why the
# --self-test asserts BOTH directions: a real call is caught, a comment is not.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

# Every entry needs a REASON. The only safe reasons are (a) this file's job IS
# establishing/refreshing the shared session, or (b) it logs into a gateway it
# spawned itself, so there is no shared session to rotate. "It was already there"
# is not a reason — a stale allow-list is how this class of bug returns.
SANCTIONED_FILES=(
  # (a) establishes the shared session and writes storageState.
  "tests/e2e/global-setup.ts"
  # (a) tests the login flow itself; its afterAll re-logins and REWRITES
  #     storageState, so it leaves the shared session consistent.
  "tests/e2e/auth.spec.ts"
  # (a) the shared UI-login helper.
  "tests/e2e/fixtures/login.ts"
  # (b) infrastructure that spawns an ISOLATED gateway and logs into that one.
  "tests/e2e/fixtures/gateway-process.ts"
  # (b) generic helper taking an explicit baseURL; used only by the two
  #     own-gateway specs below (hot-reload, fr050-preauth-window).
  "tests/e2e/setup.ts"
  # (b) runs its own isolated gateway; test.use sets a BLANK storageState.
  "tests/e2e/hot-reload.spec.ts"
  # (c) KNOWN, ACCEPTED EXCEPTION — not (a) or (b). retention.spec.ts is the one
  #     test that flips dev_mode_bypass OFF, which is the only moment the shared
  #     cookie's validity is observable, so it deliberately re-logins to get a
  #     cookie minted against the just-reloaded config. It does NOT refresh
  #     storageState afterwards, so it leaves the same latent staleness this
  #     guard exists to prevent for the specs after it in the `ui-heavy` shard
  #     (settings-memory, tool-order). Those tolerate it today only because
  #     MemorySection falls back to free-text entry on a providers error.
  #     Tracked for follow-up; do NOT copy this pattern.
  "tests/e2e/retention.spec.ts"
)

# scan <dir> <label> -> prints offenders, returns 0 clean / 1 offenders found
scan() {
  local scan_dir="$1" label="$2"
  local found=0 f rel offenders

  while IFS= read -r f; do
    rel="${f#"$REPO_ROOT"/}"

    local sanctioned=0 s
    for s in "${SANCTIONED_FILES[@]}"; do
      [ "$rel" = "$s" ] && sanctioned=1 && break
    done
    [ "$sanctioned" = 1 ] && continue

    # Match an actual CALL to the login path. Two exclusions keep this precise:
    #   - pure-comment lines (prose explaining this very rule), and
    #   - `auth/login` inside a message/assertion string, which is common in
    #     error text ("should have seeded one via POST /api/v1/auth/login").
    # A call is identified by `.post(` or `fetch(` on the same line, which is
    # every shape used in this suite (ctx.post, page.request.post, fetch).
    offenders="$(grep -n 'auth/login' "$f" 2>/dev/null | awk '
      {
        i = index($0, ":");
        code = substr($0, i + 1);
        sub(/^[ \t]*/, "", code);
        if (code ~ /^\*/)  next;
        if (code ~ /^\/\//) next;
        if (code ~ /^\/\*/) next;
        if (code !~ /\.post\(/ && code !~ /fetch\(/) next;
        print;
      }')"

    if [ -n "$offenders" ]; then
      found=1
      echo "$label: FORBIDDEN login in $rel" >&2
      printf '%s\n' "$offenders" | awk '{ print "    " $0 }' >&2
    fi
  done < <(find "$scan_dir" -type f \( -name '*.ts' -o -name '*.tsx' \) | sort)

  return "$found"
}

# ── self-test ───────────────────────────────────────────────────────────────
# Proves the guard can actually FAIL (a check that cannot fail is not a check)
# AND that it does not fire on prose.
if [ "${1:-}" = "--self-test" ]; then
  tmp="$(mktemp -d)" || { echo "self-test: mktemp failed" >&2; exit 2; }
  trap 'rm -rf "$tmp"' EXIT
  mkdir -p "$tmp/tests/e2e"

  # (1) a real offending call MUST be caught
  cat > "$tmp/tests/e2e/offender.spec.ts" <<'EOF'
const res = await ctx.post('/api/v1/auth/login', { data: {} });
EOF
  if REPO_ROOT="$tmp" scan "$tmp/tests/e2e" "selftest" >/dev/null 2>&1; then
    echo "check-e2e-login-crosstalk: SELF-TEST FAILED — a real login call was not caught" >&2
    exit 2
  fi
  echo "  ok  self-test: real login call is caught (exit 1)"

  # (2) prose about the rule MUST NOT be caught
  rm "$tmp/tests/e2e/offender.spec.ts"
  cat > "$tmp/tests/e2e/prose.spec.ts" <<'EOF'
/**
 * Never call POST /api/v1/auth/login from a spec — it rotates the session.
 */
// See /api/v1/auth/login in rest_auth.go for why.
const ok = true;
EOF
  if ! REPO_ROOT="$tmp" scan "$tmp/tests/e2e" "selftest" >/dev/null 2>&1; then
    echo "check-e2e-login-crosstalk: SELF-TEST FAILED — comment-only prose tripped the guard" >&2
    exit 2
  fi
  echo "  ok  self-test: comment-only prose does not trip the guard (exit 0)"

  # (3) `auth/login` inside a message string MUST NOT be caught
  rm "$tmp/tests/e2e/prose.spec.ts"
  cat > "$tmp/tests/e2e/message.spec.ts" <<'EOF'
throw new Error('no admin session; should have seeded one via POST /api/v1/auth/login.');
EOF
  if ! REPO_ROOT="$tmp" scan "$tmp/tests/e2e" "selftest" >/dev/null 2>&1; then
    echo "check-e2e-login-crosstalk: SELF-TEST FAILED — a message string tripped the guard" >&2
    exit 2
  fi
  echo "  ok  self-test: login path inside a message string does not trip the guard (exit 0)"

  echo "check-e2e-login-crosstalk: self-test passed"
  exit 0
fi

# ── real run ────────────────────────────────────────────────────────────────
cd "$REPO_ROOT" || { echo "check-e2e-login-crosstalk: cannot cd to $REPO_ROOT" >&2; exit 2; }

if [ ! -d "tests/e2e" ]; then
  echo "check-e2e-login-crosstalk: expected directory 'tests/e2e' not found under $REPO_ROOT" >&2
  echo "  (wrong cwd or a partial checkout — refusing to report green for a tree" >&2
  echo "   this script never actually scanned)" >&2
  exit 2
fi

for s in "${SANCTIONED_FILES[@]}"; do
  if [ ! -f "$s" ]; then
    echo "check-e2e-login-crosstalk: sanctioned file '$s' no longer exists." >&2
    echo "  Update SANCTIONED_FILES — a stale allow-list silently widens this guard." >&2
    exit 2
  fi
done

if scan "$REPO_ROOT/tests/e2e" "check-e2e-login-crosstalk"; then
  echo "check-e2e-login-crosstalk: OK (no spec mints a session via POST auth/login)"
  exit 0
fi

cat >&2 <<'EOF'

  A spec called the login endpoint. That rotates the single-slot
  session_token_hash and invalidates the shared storageState cookie for every
  spec that runs after it — which fails LATER, in an unrelated file, on the one
  route dev_mode_bypass does not mask (GET /api/v1/providers).

  Use newAdminApiContext() from tests/e2e/fixtures/admin-api.ts instead: it
  authenticates with the existing shared session and mints nothing.
EOF
exit 1

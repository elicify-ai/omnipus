#!/usr/bin/env bash
# e2e-model.test.sh — RED proof for scripts/e2e-model.sh.
#
# Oracle: design.md §§4.1, 4.2, 4.4 and §7 R7, pinned in
# /workspace/ci-minimax/api-contract.md (the CLI the implementer must match).
# The script under test does not exist yet. Each case calls it and checks the
# contract. A missing script is the missing behaviour, reported per case.
#
# No real secrets. The only key string is the fake test-key-aaaa. The stub is
# local. Nothing is sent off the machine.
#
# Exit 0 when every case passes; 1 otherwise.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
READER="$SCRIPT_DIR/e2e-model.sh"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GOLDEN="$ROOT/pkg/config/testdata/ci_two_row_seed.golden.json"

tmp="$(mktemp -d /tmp/e2e-model-reader-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT

fail=0

# spec_json writes the design.md §4.1 document.
spec_json() {
  cat <<'EOF'
{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
EOF
}

# run_reader <stdout> <stderr> -- <args...>
# Returns the script's exit code. 127 when the script file is absent.
run_reader() {
  local out="$1" err="$2"
  shift 2
  if [ ! -f "$READER" ]; then
    echo "BLOCKED: scripts/e2e-model.sh not implemented — required by design.md §4.2 and §7 R7" >"$err"
    : >"$out"
    return 127
  fi
  bash "$READER" "$@" >"$out" 2>"$err"
}

expect_rc() {
  local name="$1" want="$2" got="$3" err="$4"
  if [ "$got" -eq "$want" ]; then
    echo "$name: PASS (exit $got)"
    return 0
  fi
  echo "$name: FAIL — want exit $want, got $got" >&2
  sed 's/^/  /' "$err" >&2
  fail=1
  return 1
}

# Case: env prints the six lines for the new shape. design.md §4.2.
t_env="$tmp/env.json"
spec_json >"$t_env"
rc=0
run_reader "$tmp/env.out" "$tmp/env.err" env --file "$t_env" || rc=$?
want_env=$'E2E_PRIMARY_PROVIDER=minimax-coding-plan\nE2E_PRIMARY_MODEL=MiniMax-M3.1-Flash\nE2E_PRIMARY_KEYREF=MINIMAX_API_KEY\nE2E_FALLBACK_PROVIDER=openrouter\nE2E_FALLBACK_MODEL=deepseek/deepseek-v4.1-flash\nE2E_FALLBACK_KEYREF=OPENROUTER_API_KEY\n'
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/env.out")" = "$want_env" ]; then
  echo "case-env new-shape-lines: PASS"
else
  echo "case-env new-shape-lines: FAIL — want exit 0 and the six KEY=value lines, got exit $rc" >&2
  echo "  stdout:" >&2
  sed 's/^/  /' "$tmp/env.out" >&2
  echo "  stderr:" >&2
  sed 's/^/  /' "$tmp/env.err" >&2
  fail=1
fi

# Trimmed fields are what env prints. A surrounding space is not part of the value.
t_trim="$tmp/trim.json"
cat >"$t_trim" <<'EOF'
{
  "primary": { "provider": " minimax-coding-plan ", "model": " MiniMax-M3.1-Flash ", "api_key_ref": " MINIMAX_API_KEY " },
  "fallback": { "provider": " openrouter ", "model": " deepseek/deepseek-v4.1-flash ", "api_key_ref": " OPENROUTER_API_KEY " }
}
EOF
rc=0
run_reader "$tmp/trim.out" "$tmp/trim.err" env --file "$t_trim" || rc=$?
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/trim.out")" = "$want_env" ]; then
  echo "case-env trims-fields: PASS"
else
  echo "case-env trims-fields: FAIL — want the trimmed six lines, got exit $rc" >&2
  sed 's/^/  /' "$tmp/trim.out" >&2
  sed 's/^/  /' "$tmp/trim.err" >&2
  fail=1
fi

# Missing .primary.model fails closed. design.md §4.1.
t_miss="$tmp/miss-model.json"
cat >"$t_miss" <<'EOF'
{
  "primary": { "provider": "minimax-coding-plan", "model": "   ", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
EOF
rc=0
run_reader "$tmp/miss.out" "$tmp/miss.err" env --file "$t_miss" || rc=$?
if [ "$rc" -eq 2 ] && grep -q '\.primary\.model' "$tmp/miss.err"; then
  echo "case-env whitespace-model-exit2: PASS"
else
  echo "case-env whitespace-model-exit2: FAIL — want exit 2 and stderr containing .primary.model, got $rc" >&2
  sed 's/^/  /' "$tmp/miss.err" >&2
  fail=1
fi

# Missing fallback object. design.md §4.1.
t_nofb="$tmp/no-fallback.json"
printf '%s\n' '{"primary":{"provider":"minimax-coding-plan","model":"MiniMax-M3.1-Flash","api_key_ref":"MINIMAX_API_KEY"}}' >"$t_nofb"
rc=0
run_reader "$tmp/nofb.out" "$tmp/nofb.err" env --file "$t_nofb" || rc=$?
if [ "$rc" -eq 2 ] && grep -q '\.fallback' "$tmp/nofb.err"; then
  echo "case-env missing-fallback-exit2: PASS"
else
  echo "case-env missing-fallback-exit2: FAIL — want exit 2 and stderr containing .fallback, got $rc" >&2
  sed 's/^/  /' "$tmp/nofb.err" >&2
  fail=1
fi

# primary == fallback is rejected. design.md §4.1.
t_same="$tmp/same.json"
cat >"$t_same" <<'EOF'
{
  "primary": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
EOF
rc=0
run_reader "$tmp/same.out" "$tmp/same.err" env --file "$t_same" || rc=$?
if [ "$rc" -eq 2 ] && grep -q 'same provider/model' "$tmp/same.err"; then
  echo "case-env same-pair-exit2: PASS"
else
  echo "case-env same-pair-exit2: FAIL — want exit 2 and stderr containing 'same provider/model', got $rc" >&2
  sed 's/^/  /' "$tmp/same.err" >&2
  fail=1
fi

# Legacy shape without --legacy-ok is rejected. design.md §4.1 / §7 runci note.
t_old="$tmp/old.json"
printf '%s\n' '{ "model": "vendor/legacy-model" }' >"$t_old"
rc=0
run_reader "$tmp/old.out" "$tmp/old.err" env --file "$t_old" || rc=$?
if [ "$rc" -eq 2 ] && grep -q 'old shape' "$tmp/old.err"; then
  echo "case-env old-shape-rejected: PASS"
else
  echo "case-env old-shape-rejected: FAIL — want exit 2 and stderr containing 'old shape', got $rc" >&2
  sed 's/^/  /' "$tmp/old.err" >&2
  fail=1
fi

# Legacy shape under --legacy-ok maps to openrouter primary and empty fallback,
# with a WARN. design.md §7 (runci legacy read).
rc=0
run_reader "$tmp/leg.out" "$tmp/leg.err" env --legacy-ok --file "$t_old" || rc=$?
want_leg=$'E2E_PRIMARY_PROVIDER=openrouter\nE2E_PRIMARY_MODEL=vendor/legacy-model\nE2E_PRIMARY_KEYREF=OPENROUTER_API_KEY\nE2E_FALLBACK_PROVIDER=\nE2E_FALLBACK_MODEL=\nE2E_FALLBACK_KEYREF=\n'
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/leg.out")" = "$want_leg" ] && grep -q 'WARN' "$tmp/leg.err"; then
  echo "case-env legacy-ok-maps-with-warn: PASS"
else
  echo "case-env legacy-ok-maps-with-warn: FAIL — want exit 0, the openrouter mapping, and WARN, got $rc" >&2
  echo "  stdout:" >&2
  sed 's/^/  /' "$tmp/leg.out" >&2
  echo "  stderr:" >&2
  sed 's/^/  /' "$tmp/leg.err" >&2
  fail=1
fi

# --legacy-ok must not rewrite a document that is already the new shape.
rc=0
run_reader "$tmp/legnew.out" "$tmp/legnew.err" env --legacy-ok --file "$t_env" || rc=$?
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/legnew.out")" = "$want_env" ] && ! grep -q 'WARN' "$tmp/legnew.err"; then
  echo "case-env legacy-ok-keeps-new-shape: PASS"
else
  echo "case-env legacy-ok-keeps-new-shape: FAIL — want the new six lines and no WARN, got $rc" >&2
  sed 's/^/  /' "$tmp/legnew.out" >&2
  sed 's/^/  /' "$tmp/legnew.err" >&2
  fail=1
fi

# seed-config bytes match the golden (port 19999). design.md §7 R5 shares this oracle.
rc=0
run_reader "$tmp/seed.out" "$tmp/seed.err" seed-config 19999 --file "$t_env" || rc=$?
if [ "$rc" -eq 0 ] && [ -f "$GOLDEN" ] && cmp -s "$tmp/seed.out" "$GOLDEN"; then
  echo "case-seed-config matches-golden: PASS"
else
  echo "case-seed-config matches-golden: FAIL — want exit 0 and bytes equal to $GOLDEN, got $rc" >&2
  sed 's/^/  /' "$tmp/seed.err" >&2
  fail=1
fi

# preflight helpers. The stub is a one-shot local server. It records the
# request and can echo the fake key in the body; the script must not print it.
start_stub() {
  local mode="$1" body_file="$2" report="$3" portfile="$4"
  STUB_MODE="$mode" STUB_KEY="test-key-aaaa" STUB_REPORT="$report" STUB_BODY_FILE="$body_file" \
    python3 -I -c '
import json, os
from http.server import BaseHTTPRequestHandler, HTTPServer
mode = os.environ["STUB_MODE"]
key = os.environ["STUB_KEY"]
report = os.environ["STUB_REPORT"]
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(n)
        with open(report, "w", encoding="utf-8") as fh:
            json.dump({
                "path": self.path,
                "x_api_key": self.headers.get("x-api-key", ""),
                "anthropic_version": self.headers.get("anthropic-version", ""),
                "body": raw.decode("utf-8", "replace"),
            }, fh)
        if mode == "sleep":
            import time
            time.sleep(5)
        status = {"200": 200, "429": 429, "503": 503, "401": 401, "400": 400, "529": 529}.get(mode, 500)
        if mode == "401":
            payload = json.dumps({"type": "error", "error": {"type": "authentication_error", "message": key}})
        elif mode == "429":
            payload = json.dumps({"type": "error", "error": {"type": "rate_limit_error", "message": key}})
        elif mode == "529":
            payload = json.dumps({"type": "error", "error": {"type": "overloaded_error", "message": "overloaded"}})
        else:
            payload = "{}"
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload.encode())))
        self.end_headers()
        self.wfile.write(payload.encode())
    def log_message(self, fmt, *args):
        return
server = HTTPServer(("127.0.0.1", 0), H)
print(server.server_address[1], flush=True)
server.timeout = 3
server.handle_request()
' >"$portfile" &
  echo $!
}

# wait until the stub prints a port, or the pid dies.
wait_port() {
  local portfile="$1" pid="$2" i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if [ -s "$portfile" ]; then
      cat "$portfile"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      return 1
    fi
    sleep 0.1
  done
  return 1
}

# run_preflight <mode> <workdir>
# mode "refused" uses a closed port and does not start a stub.
run_preflight() {
  local mode="$1" work="$2"
  mkdir -p "$work"
  local report="$work/seen.json" portfile="$work/port" out="$work/out" err="$work/err"
  local url pid rc=0
  if [ "$mode" = "refused" ]; then
    url="http://127.0.0.1:1/messages"
  else
    pid="$(start_stub "$mode" "$work/body" "$report" "$portfile")"
    local port
    if ! port="$(wait_port "$portfile" "$pid")"; then
      echo "case-preflight $mode: FAIL — stub did not start" >&2
      fail=1
      return
    fi
    url="http://127.0.0.1:${port}/messages"
  fi
  (
    cd "$work"
    unset MINIMAX_API_KEY MINIMAX_API_KEY_CI OPENROUTER_API_KEY OPENROUTER_API_KEY_CI
    export MINIMAX_API_KEY_CI="test-key-aaaa"
    export E2E_PREFLIGHT_URL="$url"
    export E2E_PREFLIGHT_TIMEOUT_SECONDS=1
    run_reader "$out" "$err" preflight --file "$t_env" --route-out "$work/llm-route.json"
  ) || rc=$?
  if [ "$mode" != "refused" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  echo "$rc"
}

check_fallback() {
  local name="$1" work="$2" warning="$3" route_line="$4" rc
  rc="$(run_preflight "$name" "$work")"
  local out="$work/out" err="$work/err" route="$work/llm-route.json"
  local blob
  blob="$(cat "$out" "$err" 2>/dev/null || true)"
  if [ "$rc" -eq 0 ] \
    && [ "$(cat "$out" 2>/dev/null || true)" = $'LLM_ROUTE=fallback\n' ] \
    && grep -qF "$warning" "$err" \
    && [ "$(cat "$route" 2>/dev/null || true)" = "$route_line" ] \
    && ! printf '%s' "$blob" | grep -qF 'test-key-aaaa'; then
    echo "case-preflight $name-falls-back: PASS"
  else
    echo "case-preflight $name-falls-back: FAIL — want LLM_ROUTE=fallback, the warning, the route file, and no key in the output; got exit $rc" >&2
    echo "  stdout:" >&2
    sed 's/^/  /' "$out" >&2
    echo "  stderr:" >&2
    sed 's/^/  /' "$err" >&2
    echo "  route:" >&2
    if [ -f "$route" ]; then
      sed 's/^/  /' "$route" >&2
    else
      echo "  (no llm-route.json)" >&2
    fi
    fail=1
  fi
}

check_fallback "429" "$tmp/pf429" \
  "::warning::LLM: fallback (primary MiniMax unavailable: 429 unavailable)" \
  '{"route":"fallback","status":"429","error_type":"unavailable"}'$'\n'
# The 429 stub echoes the key. The script output must not, and the stub must
# have been called with that key (so a green is not "it never called anyone").
if [ -f "$tmp/pf429/seen.json" ] && python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("x_api_key")=="test-key-aaaa"' "$tmp/pf429/seen.json"; then
  echo "case-preflight 429-sent-key-but-did-not-print-it: PASS"
else
  echo "case-preflight 429-sent-key-but-did-not-print-it: FAIL — stub did not see x-api-key test-key-aaaa (the call never carried the key, or never happened)" >&2
  fail=1
fi

check_fallback "503" "$tmp/pf503" \
  "::warning::LLM: fallback (primary MiniMax unavailable: 503 unavailable)" \
  '{"route":"fallback","status":"503","error_type":"unavailable"}'$'\n'

check_fallback "refused" "$tmp/pfrefused" \
  "::warning::LLM: fallback (primary MiniMax unavailable: connection_refused unavailable)" \
  '{"route":"fallback","status":"connection_refused","error_type":"unavailable"}'$'\n'

check_fallback "401" "$tmp/pf401" \
  "::warning::LLM: fallback (primary MiniMax unavailable: 401 authentication_error)" \
  '{"route":"fallback","status":"401","error_type":"authentication_error"}'$'\n'

# Missing secret fails. It is not a fallback. design.md §4.4.
miss="$tmp/pfmiss"
mkdir -p "$miss"
rc=0
(
  cd "$miss"
  unset MINIMAX_API_KEY MINIMAX_API_KEY_CI OPENROUTER_API_KEY OPENROUTER_API_KEY_CI
  unset E2E_PREFLIGHT_URL
  run_reader "$miss/out" "$miss/err" preflight --file "$t_env"
) || rc=$?
if [ "$rc" -eq 1 ] \
  && grep -q 'MINIMAX_API_KEY_CI' "$miss/err" \
  && grep -q 'MINIMAX_API_KEY' "$miss/err" \
  && ! grep -q 'LLM_ROUTE=fallback' "$miss/out"; then
  echo "case-preflight missing-secret-fails: PASS"
else
  echo "case-preflight missing-secret-fails: FAIL — want exit 1 naming MINIMAX_API_KEY_CI and MINIMAX_API_KEY, not a fallback route; got $rc" >&2
  sed 's/^/  /' "$miss/out" >&2
  sed 's/^/  /' "$miss/err" >&2
  fail=1
fi

# Plain 400 must not fall back. design.md §4.4 lists the fallback outcomes;
# a 400 is not one of them (same rule as the product's Format class, §3).
bad="$tmp/pf400"
rc400="$(run_preflight "400" "$bad")"
blob400="$(cat "$bad/out" "$bad/err" 2>/dev/null || true)"
if [ "$rc400" -eq 1 ] \
  && ! printf '%s' "$blob400" | grep -q 'LLM_ROUTE=fallback' \
  && ! grep -q '"route":"fallback"' "$bad/llm-route.json" 2>/dev/null \
  && ! printf '%s' "$blob400" | grep -qF 'test-key-aaaa'; then
  echo "case-preflight 400-does-not-fall-back: PASS"
else
  echo "case-preflight 400-does-not-fall-back: FAIL — want exit 1 and no fallback route, got $rc400" >&2
  sed 's/^/  /' "$bad/out" >&2
  sed 's/^/  /' "$bad/err" >&2
  fail=1
fi

# 2xx stays on the primary. No warning. design.md §4.4.
ok="$tmp/pf200"
rc200="$(run_preflight "200" "$ok")"
if [ "$rc200" -eq 0 ] \
  && [ "$(cat "$ok/out" 2>/dev/null || true)" = $'LLM_ROUTE=primary\n' ] \
  && ! grep -q '::warning::' "$ok/err" \
  && [ "$(cat "$ok/llm-route.json" 2>/dev/null || true)" = '{"route":"primary","status":"200","error_type":""}'$'\n' ]; then
  echo "case-preflight 200-stays-primary: PASS"
else
  echo "case-preflight 200-stays-primary: FAIL — want LLM_ROUTE=primary and no warning, got $rc200" >&2
  sed 's/^/  /' "$ok/out" >&2
  sed 's/^/  /' "$ok/err" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "e2e-model.test: FAILED" >&2
  exit 1
fi
echo "e2e-model.test: PASS"
exit 0

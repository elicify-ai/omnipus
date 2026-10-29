#!/usr/bin/env bash
# e2e-model-extra.test.sh — hardening cases for scripts/e2e-model.sh added in
# the CI MiniMax fix round (2026-10-29), next to the contract tests in
# scripts/e2e-model.test.sh (owned by the test author, not edited here).
#
#   F3   E2E_PREFLIGHT_URL is a loopback-only test hook: a remote value is
#        refused (exit 2) before any request, so the primary key cannot be
#        redirected off the machine.
#   I2   preflight --strict: 401/403 fails the job loudly (exit 1, ::error::),
#        never a silent fallback.
#   I3   a 2xx must be a JSON object; under --strict an Anthropic message.
#   I1   note-fallbacks: product fallback notes found after the tests make
#        llm-route.json stop saying "primary".
#   leak seed-agent-fallbacks removes its temp dir even when it fails.
#
# Fake key test-key-aaaa only; every stub is local. Exit 0 = all pass.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
READER="$SCRIPT_DIR/e2e-model.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/e2e-model-extra.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fail=0

pass() { echo "$1: PASS"; }
bad() {
  echo "$1: FAIL — $2" >&2
  [ -f "${3:-/dev/null}" ] && sed 's/^/  /' "$3" >&2
  fail=1
}

spec_json="$tmp/model.json"
cat >"$spec_json" <<'EOF'
{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash-Preview", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
EOF

# stub <status> <body-file> <portfile> <hitfile>: one-shot local server.
stub() {
  STUB_STATUS="$1" STUB_BODY="$2" STUB_HIT="$4" python3 -I -c '
import os
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        open(os.environ["STUB_HIT"], "w").write("hit")
        body = open(os.environ["STUB_BODY"], "rb").read()
        self.send_response(int(os.environ["STUB_STATUS"]))
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
s = HTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.timeout = 5
s.handle_request()
' >"$3" &
  STUB_PID=$!
  for _ in $(seq 1 50); do [ -s "$3" ] && return 0; sleep 0.1; done
  return 1
}

# preflight_case <name> <status> <body> <extra-args...>; sets RC, OUT, ERR, ROUTE
preflight_case() {
  local name="$1" status="$2" body="$3"; shift 3
  local d="$tmp/$name"; mkdir -p "$d"
  printf '%s' "$body" >"$d/body"
  if ! stub "$status" "$d/body" "$d/port" "$d/hit"; then
    bad "$name" "stub did not start"; return
  fi
  RC=0
  (
    unset MINIMAX_API_KEY MINIMAX_API_KEY_CI
    MINIMAX_API_KEY_CI=test-key-aaaa E2E_PREFLIGHT_URL="http://127.0.0.1:$(cat "$d/port")/messages" \
      E2E_PREFLIGHT_TIMEOUT_SECONDS=2 \
      bash "$READER" preflight --file "$spec_json" --route-out "$d/route.json" "$@" >"$d/out" 2>"$d/err"
  ) || RC=$?
  kill "$STUB_PID" 2>/dev/null; wait "$STUB_PID" 2>/dev/null
  OUT="$d/out" ERR="$d/err" ROUTE="$d/route.json"
}

MSG='{"type":"message","role":"assistant","content":[{"type":"text","text":"p"}],"stop_reason":"end_turn"}'
# The shape MiniMax really answers for the CI primary with max_tokens 1
# (train 5, run 40807096998): a message whose content is NOT an array.
MINIMAX_MSG='{"id":"r1","type":"message","role":"assistant","model":"m","content":null,"usage":{"input_tokens":3,"output_tokens":1},"stop_reason":"max_tokens","base_resp":{"status_code":0,"status_msg":""}}'

# F3: a remote override is refused before any request.
for url in "http://10.255.255.1:9/messages" "https://example.invalid/messages" "http://127.0.0.1.evil.example/m"; do
  d="$tmp/f3-$RANDOM"; mkdir -p "$d"; rc=0
  ( MINIMAX_API_KEY_CI=test-key-aaaa E2E_PREFLIGHT_URL="$url" \
      bash "$READER" preflight --file "$spec_json" --route-out "$d/route.json" >"$d/out" 2>"$d/err" ) || rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'test hook' "$d/err" && [ ! -e "$d/route.json" ] && ! grep -q 'LLM_ROUTE' "$d/out"; then
    pass "f3 remote-override-refused ($url)"
  else
    bad "f3 remote-override-refused ($url)" "want exit 2, a refusal and no route; got $rc" "$d/err"
  fi
done

# I2: --strict turns 401 into a loud failure, never a fallback.
preflight_case i2-strict-401 401 '{"type":"error","error":{"type":"authentication_error","message":"x"}}' --strict
if [ "$RC" -eq 1 ] && grep -q '::error::' "$ERR" && ! grep -q 'LLM_ROUTE=fallback' "$OUT" && ! grep -q '"route":"fallback"' "$ROUTE" 2>/dev/null; then
  pass "i2 strict-401-fails"
else
  bad "i2 strict-401-fails" "want exit 1, ::error:: and no fallback route; got $RC" "$ERR"
fi
preflight_case i2-strict-403 403 '{"type":"error","error":{"type":"permission_error","message":"x"}}' --strict
if [ "$RC" -eq 1 ] && grep -q '::error::' "$ERR" && ! grep -q 'LLM_ROUTE' "$OUT"; then
  pass "i2 strict-403-fails"
else
  bad "i2 strict-403-fails" "want exit 1 and ::error::; got $RC" "$ERR"
fi
# --strict keeps a real outage a fallback.
preflight_case i2-strict-429 429 '{}' --strict
if [ "$RC" -eq 0 ] && [ "$(cat "$OUT")" = "LLM_ROUTE=fallback" ]; then
  pass "i2 strict-429-still-falls-back"
else
  bad "i2 strict-429-still-falls-back" "want LLM_ROUTE=fallback; got $RC" "$ERR"
fi

# I3: a 2xx must be a real answer.
preflight_case i3-html-200 200 '<html>portal</html>'
if [ "$RC" -eq 1 ] && ! grep -q 'LLM_ROUTE' "$OUT" && [ ! -e "$ROUTE" ]; then
  pass "i3 non-json-200-fails"
else
  bad "i3 non-json-200-fails" "want exit 1 and no route; got $RC" "$ERR"
fi
preflight_case i3-strict-empty 200 '{}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'LLM_ROUTE' "$OUT"; then
  pass "i3 strict-non-message-200-fails"
else
  bad "i3 strict-non-message-200-fails" "want exit 1; got $RC" "$ERR"
fi
preflight_case i3-strict-message 200 "$MSG" --strict
if [ "$RC" -eq 0 ] && [ "$(cat "$OUT")" = "LLM_ROUTE=primary" ]; then
  pass "i3 strict-message-200-primary"
else
  bad "i3 strict-message-200-primary" "want LLM_ROUTE=primary; got $RC" "$ERR"
fi

# --strict accepts the real MiniMax answer: type message, a stop_reason, and
# base_resp.status_code 0 — content null is fine (max_tokens 1).
preflight_case i3-strict-minimax-null-content 200 "$MINIMAX_MSG" --strict
if [ "$RC" -eq 0 ] && [ "$(cat "$OUT")" = "LLM_ROUTE=primary" ] && ! grep -q '::warning::' "$ERR"; then
  pass "i3 strict-minimax-message-null-content-primary"
else
  bad "i3 strict-minimax-message-null-content-primary" "want LLM_ROUTE=primary for the real MiniMax shape; got $RC" "$ERR"
fi

# --strict still refuses a 2xx error body, a non-zero base_resp status and a
# message without stop_reason; the diagnostic names the content TYPE and the
# stop_reason value only.
preflight_case i3-strict-error-body 200 '{"type":"error","error":{"type":"invalid_request_error","message":"test-key-aaaa"}}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'LLM_ROUTE' "$OUT" && grep -q '\.type=error' "$ERR" && ! grep -q 'test-key-aaaa' "$ERR"; then
  pass "i3 strict-200-error-body-fails"
else
  bad "i3 strict-200-error-body-fails" "want exit 1 naming .type=error; got $RC" "$ERR"
fi
preflight_case i3-strict-base-resp-nonzero 200 '{"type":"message","content":null,"stop_reason":"end_turn","base_resp":{"status_code":2013,"status_msg":"invalid model test-key-aaaa"}}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'LLM_ROUTE' "$OUT" && grep -q '\.base_resp\.status_code=2013' "$ERR" \
  && grep -q 'content type: null' "$ERR" && grep -q 'stop_reason=end_turn' "$ERR" && ! grep -q 'test-key-aaaa' "$ERR"; then
  pass "i3 strict-base-resp-nonzero-fails"
else
  bad "i3 strict-base-resp-nonzero-fails" "want exit 1 naming the status, content type and stop_reason; got $RC" "$ERR"
fi
preflight_case i3-strict-no-stop-reason 200 '{"type":"message","content":"text","base_resp":{"status_code":0}}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'LLM_ROUTE' "$OUT" && grep -q 'content type: string' "$ERR"; then
  pass "i3 strict-missing-stop-reason-fails"
else
  bad "i3 strict-missing-stop-reason-fails" "want exit 1 naming content type string; got $RC" "$ERR"
fi
preflight_case i3-strict-hostile-stop-reason 200 '{"type":"message","content":{"x":1},"stop_reason":"Stop test-key-aaaa","base_resp":{"status_code":7}}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'test-key-aaaa' "$ERR" "$OUT" && grep -q 'content type: object' "$ERR" && ! grep -q 'stop_reason=' "$ERR"; then
  pass "i3 strict-hostile-stop-reason-not-shown"
else
  bad "i3 strict-hostile-stop-reason-not-shown" "want exit 1, content type object and no stop_reason value; got $RC" "$ERR"
fi

# I3 diagnostic: a 2xx that is not a message names only the top-level key
# names and the .type / .error.type / .base_resp.status_code values — never
# the body (here status_msg echoes the fake key), and never a key name or a
# value that is not a plain identifier.
preflight_case i3-diag 200 '{"base_resp":{"status_code":2013,"status_msg":"invalid model test-key-aaaa"},"id":"r1","type":"weird_reply","error":{"type":"unknown_model","message":"test-key-aaaa"}}' --strict
if [ "$RC" -eq 1 ] \
  && grep -q 'top-level keys: base_resp, id, type, error' "$ERR" \
  && grep -q '\.type=weird_reply' "$ERR" \
  && grep -q '\.error\.type=unknown_model' "$ERR" \
  && grep -q '\.base_resp\.status_code=2013' "$ERR" \
  && ! grep -q 'test-key-aaaa' "$ERR" "$OUT" \
  && ! grep -q 'invalid model' "$ERR"; then
  pass "i3 non-message-200-safe-diagnostic"
else
  bad "i3 non-message-200-safe-diagnostic" "want exit 1 naming keys, .type, .error.type and .base_resp.status_code only; got $RC" "$ERR"
fi
preflight_case i3-diag-hostile 200 '{"test-key-aaaa":1,"type":"has space test-key-aaaa","base_resp":{"status_code":"test-key-aaaa"}}' --strict
if [ "$RC" -eq 1 ] && ! grep -q 'test-key-aaaa' "$ERR" "$OUT" && grep -q 'top-level keys:' "$ERR"; then
  pass "i3 hostile-key-names-and-values-dropped"
else
  bad "i3 hostile-key-names-and-values-dropped" "want exit 1 and no canary in the diagnostic; got $RC" "$ERR"
fi

# I1: note-fallbacks.
sess="$tmp/sessions/s1"; mkdir -p "$sess"
note='Answered by the Fallback model (deepseek/deepseek-v4.1-flash) because MiniMax-M3.1-Flash-Preview was unavailable.'
printf '{"note":"%s"}\n{"x":1}\n{"note":"%s"}\n' "$note" "$note" >"$sess/transcript.jsonl"
route="$tmp/i1-route.json"
printf '%s\n' '{"route":"primary","status":"200","error_type":""}' >"$route"
rc=0
bash "$READER" note-fallbacks "$tmp/sessions" --file "$spec_json" --route-out "$route" >"$tmp/i1.out" 2>"$tmp/i1.err" || rc=$?
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/i1.out")" = "FALLBACK_NOTES=2" ] \
  && [ "$(jq -r .route "$route")" = "primary_then_fallback" ] && [ "$(jq -r .fallback_notes "$route")" = "2" ] \
  && grep -q '::warning::' "$tmp/i1.err"; then
  pass "i1 midrun-fallback-recorded"
else
  bad "i1 midrun-fallback-recorded" "want FALLBACK_NOTES=2 and route primary_then_fallback; got $rc" "$tmp/i1.err"
fi
mkdir -p "$tmp/empty-sessions"
printf '%s\n' '{"route":"primary","status":"200","error_type":""}' >"$tmp/i1b-route.json"
cp "$tmp/i1b-route.json" "$tmp/i1b-before.json"
rc=0
bash "$READER" note-fallbacks "$tmp/empty-sessions" --file "$spec_json" --route-out "$tmp/i1b-route.json" >"$tmp/i1b.out" 2>"$tmp/i1b.err" || rc=$?
if [ "$rc" -eq 0 ] && [ "$(cat "$tmp/i1b.out")" = "FALLBACK_NOTES=0" ] && cmp -s "$tmp/i1b-route.json" "$tmp/i1b-before.json" \
  && ! grep -q '::warning::' "$tmp/i1b.err"; then
  pass "i1 no-notes-keeps-primary"
else
  bad "i1 no-notes-keeps-primary" "want FALLBACK_NOTES=0 and an unchanged route file; got $rc" "$tmp/i1b.err"
fi

# leak: seed-agent-fallbacks cleans its temp dir on failure.
leakdir="$tmp/leak"; mkdir -p "$leakdir"; rc=0
TMPDIR="$leakdir" bash "$READER" seed-agent-fallbacks "http://127.0.0.1:1" fake-token --file "$spec_json" >/dev/null 2>"$tmp/leak.err" || rc=$?
if [ "$rc" -eq 1 ] && [ -z "$(ls -A "$leakdir")" ]; then
  pass "leak seed-agent-fallbacks-cleans-up-on-failure"
else
  bad "leak seed-agent-fallbacks-cleans-up-on-failure" "want exit 1 and an empty TMPDIR; got $rc, left: $(find "$leakdir" -mindepth 1 -maxdepth 1 -printf '%f ')" "$tmp/leak.err"
fi

if [ "$fail" -ne 0 ]; then
  echo "e2e-model-extra.test: FAILED" >&2
  exit 1
fi
echo "e2e-model-extra.test: PASS"

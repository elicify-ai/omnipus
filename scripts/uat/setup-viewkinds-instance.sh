#!/usr/bin/env bash
# Omnipus — stand up one throwaway gateway instance for the view-kinds UAT.
#
# view-kinds-design-2026-09-03 §8 item 5 needs TWO independent vaults driven by
# a FRESH agent. This script builds one such instance end to end and prints the
# ids the harness needs, so vault A and vault B differ only in their contents.
#
# It follows docs/internal/uat/knowledge-tools-uat-plan.md §2's ordering, and
# carries the two things that have silently broken this setup before:
#
#   * `"version": 1` in config.json. Without it the gateway EXITS SILENTLY at
#     boot — nothing on stdout, the reason only in logs/gateway_panic.log.
#   * the vault goes under <home>/workspaces/<id>/work/ BEFORE the collection is
#     asked for, because the collection id is a hash of the resolved root path
#     (pkg/gateway/rest_knowledge.go::knowledgeCollectionID).
#
# Usage: setup-viewkinds-instance.sh <home-dir> <port> <vault-src-dir> <label>
#   vault-src-dir is copied to <home>/workspaces/<id>/work/vault — the ORIGINAL
#   is never touched, which matters because vault A is the founder's own import.
#
# Requires $OPENROUTER_API_KEY and $OMNIPUS_BIN.
set -uo pipefail

HOME_DIR="${1:?home dir}"; PORT="${2:?port}"; VAULT_SRC="${3:?vault source}"; LABEL="${4:?label}"
BIN="${OMNIPUS_BIN:?set OMNIPUS_BIN to the built binary}"
KEY="${OPENROUTER_API_KEY:?set OPENROUTER_API_KEY}"
MODEL="${UAT_MODEL:-z-ai/glm-5.3-flash}"
# The E2E instance must be onboarded under the credentials tests/e2e's
# global-setup logs in with (admin/admin123), or global-setup onboards a SECOND
# admin against an already-onboarded gateway, gets 409, and then fails to log
# in. Overridable for that reason and no other.
UAT_USER="${UAT_USER:-uat}"
UAT_PASS="${UAT_PASS:-uat-pass-12345}"
BASE="http://127.0.0.1:${PORT}"

say() { printf '[%s] %s\n' "$LABEL" "$*" >&2; }

rm -rf "$HOME_DIR"; mkdir -p "$HOME_DIR"
cat > "$HOME_DIR/config.json" <<EOF
{
  "version": 1,
  "gateway": { "port": ${PORT}, "dev_mode_bypass": false },
  "sandbox": { "audit_log": true }
}
EOF

say "starting gateway on ${PORT}"
OMNIPUS_HOME="$HOME_DIR" "$BIN" gateway > "$HOME_DIR/start.log" 2>&1 &
GW_PID=$!
for _ in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/state" 2>/dev/null)
  [ "$code" = "200" ] && break
  # A silent exit is the failure this loop exists to catch: without the check
  # below it just times out after 30s with no reason on screen.
  if ! kill -0 "$GW_PID" 2>/dev/null; then
    say "GATEWAY EXITED during boot. start.log:"; tail -20 "$HOME_DIR/start.log" >&2
    say "gateway_panic.log:"; tail -20 "$HOME_DIR/logs/gateway_panic.log" 2>/dev/null >&2
    exit 1
  fi
  sleep 0.5
done
[ "$code" = "200" ] || { say "gateway never answered /api/v1/state (last code=$code)"; exit 1; }
say "gateway up (pid $GW_PID)"

# --- onboarding: creates the admin AND stores the provider key encrypted -----
# A real, billable provider probe. A key the provider rejects returns 400 and
# persists nothing, so this is also the credential check.
ONB=$(curl -s -X POST "$BASE/api/v1/onboarding/complete" -H 'Content-Type: application/json' \
  -d "{\"provider\":{\"id\":\"openrouter\",\"api_key\":\"${KEY}\",\"model\":\"${MODEL}\"},\"admin\":{\"username\":\"${UAT_USER}\",\"password\":\"${UAT_PASS}\"}}")
TOKEN=$(printf '%s' "$ONB" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("token") or d.get("session_token") or "")' 2>/dev/null)
if [ -z "$TOKEN" ]; then
  say "onboarding produced no token. response:"; printf '%s\n' "$ONB" | head -c 600 >&2; echo >&2
  kill "$GW_PID" 2>/dev/null; exit 1
fi
printf '%s' "$TOKEN" > "$HOME_DIR/uat.token"
say "onboarded; token stored"

auth() { curl -s -H "Authorization: Bearer $TOKEN" "$@"; }

# --- the tester agent: NEW, never one of the built-in roster -----------------
# All six knowledge tools `allow`, never `ask` — an `ask` policy blocks the turn
# on an approval frame, which for an unattended run is a hang.
AGENT=$(auth -X POST "$BASE/api/v1/agents" -H 'Content-Type: application/json' -d "{
  \"type\": \"Main\",
  \"name\": \"ViewKinds UAT ${LABEL}\",
  \"soul\": \"You are a careful analyst working with a knowledge vault. You use the knowledge tools to answer questions and to build saved views. You never hand-write view files.\",
  \"model\": \"${MODEL}\",
  \"provider\": \"openrouter\",
  \"tools_cfg\": { \"builtin\": { \"policies\": {
    \"knowledge_describe\": \"allow\", \"knowledge_find\": \"allow\",
    \"knowledge_read\": \"allow\", \"knowledge_edit\": \"allow\",
    \"knowledge_restructure\": \"allow\", \"knowledge_configure\": \"allow\"
  } } } }" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))')
[ -n "$AGENT" ] || { say "agent creation failed"; kill "$GW_PID" 2>/dev/null; exit 1; }
say "tester agent: $AGENT"

# --- the workspace: core_team is NOT cosmetic --------------------------------
# The knowledge tools resolve their scope from the turn's workspace; an agent on
# no workspace gets an EMPTY scope with no error at all, and every scenario then
# passes by finding nothing.
WS=$(auth -X POST "$BASE/api/v1/workspaces" -H 'Content-Type: application/json' \
  -d "{\"name\":\"UAT ${LABEL}\",\"core_team\":[\"${AGENT}\"]}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))')
[ -n "$WS" ] || { say "workspace creation failed"; kill "$GW_PID" 2>/dev/null; exit 1; }
say "workspace: $WS"

# --- the vault ---------------------------------------------------------------
WORK="$HOME_DIR/workspaces/$WS/work"
mkdir -p "$WORK"
cp -R "$VAULT_SRC" "$WORK/vault"
NOTES=$(find "$WORK/vault" -name '*.md' | wc -l | tr -d ' ')
say "vault copied: $NOTES notes"

# RESTART, and it is not optional. The properties index and its manifest are
# opened for the collections that exist AT BOOT; a vault dropped in afterwards
# is visible to the file APIs but every view against it answers
# `index_unavailable` — "the properties index is not open, so no record can be
# read". That reads like a corrupt vault and is really just a cold gateway,
# which cost a cycle here before the restart was made part of the recipe.
say "restarting so the indexer opens the new collection"
kill "$GW_PID" 2>/dev/null
wait "$GW_PID" 2>/dev/null
OMNIPUS_HOME="$HOME_DIR" "$BIN" gateway > "$HOME_DIR/start2.log" 2>&1 &
GW_PID=$!
for _ in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/state" 2>/dev/null)
  [ "$code" = "200" ] && break
  if ! kill -0 "$GW_PID" 2>/dev/null; then
    say "GATEWAY EXITED on restart. start2.log:"; tail -20 "$HOME_DIR/start2.log" >&2
    say "gateway_panic.log:"; tail -20 "$HOME_DIR/logs/gateway_panic.log" 2>/dev/null >&2
    exit 1
  fi
  sleep 0.5
done
say "gateway back up (pid $GW_PID)"

# Give the indexer a moment, then ask the server for the collection rather than
# recomputing the hash here — one authority, and it also proves the collection
# is really visible to the API rather than merely present on disk.
#
# `?path=vault` is REQUIRED. The endpoint describes ONE folder, defaulting to
# the work-tree root; without the parameter it answers about `work/` itself,
# which carries no marker, and returns `is_knowledge_base:false` — a perfectly
# valid answer to a question about the wrong folder. Omitting it cost a cycle
# here, so it is spelled out rather than defaulted.
COL=""
for _ in $(seq 1 90); do
  COL=$(auth "$BASE/api/v1/library/$WS/knowledge?path=vault" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("collection_id",""))' 2>/dev/null)
  [ -n "$COL" ] && break
  sleep 1
done
[ -n "$COL" ] || say "WARNING: no collection reported by /api/v1/library/$WS/knowledge"

cat > "$HOME_DIR/uat-env.sh" <<EOF
export UAT_LABEL='${LABEL}'
export UAT_HOME='${HOME_DIR}'
export UAT_BASE='${BASE}'
export UAT_PORT='${PORT}'
export UAT_TOKEN_FILE='${HOME_DIR}/uat.token'
export UAT_AGENT='${AGENT}'
export UAT_WS='${WS}'
export UAT_COLLECTION='${COL}'
export UAT_VAULT='${WORK}/vault'
export UAT_GW_PID='${GW_PID}'
EOF
say "collection: ${COL:-<none>}"
say "env written to $HOME_DIR/uat-env.sh"
cat "$HOME_DIR/uat-env.sh"

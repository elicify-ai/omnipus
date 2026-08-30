#!/usr/bin/env bash
#
# T.14 / T.15 — live sandbox UAT probes for per-agent tool policy enforcement.
#
# T.14: a tool denied by policy is refused at LOAD (via the ToolSearch infra
#       tool), the refusal names the policy, AND an allowed tool (read_file)
#       still works in the same run (positive lower bound — otherwise a
#       gateway that refuses everything would pass this check).
#
# T.15: per-agent policy does not leak between agents — the same tool
#       resolves to different effective verdicts for two different agents
#       when their policy maps say so.
#
# Both checks are made DETERMINISTIC by CREATING the denied/allowed condition
# rather than discovering it: two disposable probe agents are created, each
# with a COMPLETE builtin tool-policy map read back from its own
# GET /api/v1/agents/{id}/tools (`.config.builtin.policies`) and then PUT
# back with exactly one entry ("bash", falling back to "search_web") forced
# to "deny" (AGENT_DENY) or "allow" (AGENT_ALLOW). The PUT endpoint requires
# a gap-free map, so the full map is always round-tripped with one entry
# changed — never a partial map.
#
# Env inputs (required): BASE, TOK, OMNIPUS_BIN, OMNIPUS_HOME, WS_ID, AGENT_ID.
# AGENT_ID is used only as a template to discover which subject tool exists
# in the builtin catalog (bash vs. search_web) — the actual T.14/T.15 checks
# run against the two freshly-created probe agents, never AGENT_ID itself.
#
# See pkg/gateway/rest_tool_registry.go (GET /api/v1/agents/{id}/tools) and
# pkg/gateway/rest.go's updateAgentTools (PUT /api/v1/agents/{id}/tools) for
# the wire shapes this script relies on.
#
# Never `set -e` here — refusals are expected outcomes, not script errors.
set -uo pipefail

: "${BASE:?BASE not set}"
: "${TOK:?TOK not set}"
: "${OMNIPUS_BIN:?OMNIPUS_BIN not set}"
: "${OMNIPUS_HOME:?OMNIPUS_HOME not set}"
: "${WS_ID:?WS_ID not set}"
: "${AGENT_ID:?AGENT_ID not set}"

export OMNIPUS_HOME

OVERALL_FAIL=0
TMP_FILES=()

# shellcheck disable=SC2329 # invoked indirectly via `trap cleanup EXIT` below
cleanup() {
  local f
  for f in "${TMP_FILES[@]:-}"; do
    [ -n "$f" ] && rm -f "$f"
  done
}
trap cleanup EXIT

mk_tmp() {
  local f
  f=$(mktemp)
  TMP_FILES+=("$f")
  printf '%s' "$f"
}

report() {
  # report <check-id> <PASS|FAIL|N/A> <evidence>
  local id=$1 status=$2 evidence=$3
  echo "${id} ${status}: ${evidence}"
  if [ "$status" = "FAIL" ]; then
    OVERALL_FAIL=1
  fi
}

# truncate long tool-output text to keep report lines readable.
trunc() {
  local s=$1 max=${2:-400}
  if [ "${#s}" -gt "$max" ]; then
    printf '%s...[truncated]' "${s:0:$max}"
  else
    printf '%s' "$s"
  fi
}

# http_get <url> -> prints "<code> <body-file>"
http_get() {
  local url=$1
  local body_file code
  body_file=$(mk_tmp)
  code=$(curl -sS -o "$body_file" -w '%{http_code}' \
    -H "Authorization: Bearer ${TOK}" \
    "$url" 2>/dev/null || echo "000")
  echo "${code} ${body_file}"
}

# http_json <method> <url> <json-data> -> prints "<code> <body-file>"
http_json() {
  local method=$1 url=$2 data=$3
  local body_file code
  body_file=$(mk_tmp)
  code=$(curl -sS -X "$method" -o "$body_file" -w '%{http_code}' \
    -H "Authorization: Bearer ${TOK}" \
    -H 'Content-Type: application/json' \
    -d "$data" \
    "$url" 2>/dev/null || echo "000")
  echo "${code} ${body_file}"
}

if ! command -v jq >/dev/null 2>&1; then
  report "T.14" "N/A" "jq not available in this environment — cannot parse policy responses"
  report "T.15" "N/A" "jq not available in this environment — cannot parse policy responses"
  exit "$OVERALL_FAIL"
fi

NEVER_RETRY_CLAUSE="If a tool refuses, quote the refusal verbatim and stop. Never retry."

# ---------------------------------------------------------------------------
# create_probe_agent <suffix> <policy-value>
#
# Creates a disposable Main agent, forces SUBJECT_TOOL to <policy-value> in
# its COMPLETE builtin policy map, and adds it to WS_ID's core_team.
#
# Prints "OK:<agent-id>" on success or "FAIL:<reason>" on any prerequisite
# failure (never partial output) — this function is always invoked via
# command substitution (a subshell), so failures cannot be reported through
# a global variable; the FAIL:/OK: prefix on stdout is the only channel back
# to the caller.
# ---------------------------------------------------------------------------
create_probe_agent() {
  local suffix=$1 policy_value=$2
  local name
  name="tprobe-${suffix}-$(date +%s)-$$"

  local create_body
  create_body=$(jq -n --arg name "$name" '{
    type: "Main",
    name: $name,
    soul: "You are a disposable QA probe agent created only to verify tool-policy enforcement. Be concise and follow instructions exactly."
  }')

  local create_code create_resp_file
  read -r create_code create_resp_file <<<"$(http_json POST "${BASE}/api/v1/agents" "$create_body")"
  if [ "$create_code" != "201" ]; then
    echo "FAIL:POST /api/v1/agents (${suffix}) returned HTTP ${create_code}, expected 201. Body: $(trunc "$(cat "$create_resp_file")" 300)"
    return 1
  fi

  local agent_id
  agent_id=$(jq -r '.id // empty' "$create_resp_file")
  if [ -z "$agent_id" ]; then
    echo "FAIL:POST /api/v1/agents (${suffix}) returned 201 but no .id field: $(trunc "$(cat "$create_resp_file")" 300)"
    return 1
  fi

  local tools_code tools_body
  read -r tools_code tools_body <<<"$(http_get "${BASE}/api/v1/agents/${agent_id}/tools")"
  if [ "$tools_code" != "200" ]; then
    echo "FAIL:GET /api/v1/agents/${agent_id}/tools (${suffix}) returned HTTP ${tools_code}, expected 200"
    return 1
  fi

  local updated_policies
  updated_policies=$(jq --arg tool "$SUBJECT_TOOL" --arg val "$policy_value" '
    .config.builtin.policies as $p
    | ($p // {}) + {($tool): $val}
  ' "$tools_body")
  if [ "$updated_policies" = "null" ] || [ -z "$updated_policies" ]; then
    echo "FAIL:could not derive a complete policy map from GET /api/v1/agents/${agent_id}/tools (${suffix}) — .config.builtin.policies missing"
    return 1
  fi

  local put_body put_code put_resp_file
  put_body=$(jq -n --argjson policies "$updated_policies" '{builtin: {policies: $policies}}')
  read -r put_code put_resp_file <<<"$(http_json PUT "${BASE}/api/v1/agents/${agent_id}/tools" "$put_body")"
  if [ "$put_code" != "200" ]; then
    echo "FAIL:PUT /api/v1/agents/${agent_id}/tools (${suffix}, setting '${SUBJECT_TOOL}'=${policy_value}) returned HTTP ${put_code}, expected 200. Body: $(trunc "$(cat "$put_resp_file")" 300)"
    return 1
  fi

  local ws_get_code ws_get_body
  read -r ws_get_code ws_get_body <<<"$(http_get "${BASE}/api/v1/workspaces/${WS_ID}")"
  if [ "$ws_get_code" != "200" ]; then
    echo "FAIL:GET /api/v1/workspaces/${WS_ID} (${suffix}) returned HTTP ${ws_get_code}, expected 200"
    return 1
  fi

  local new_core_team
  new_core_team=$(jq --arg id "$agent_id" '
    (.core_team // []) as $ct
    | if ($ct | index($id)) then $ct else $ct + [$id] end
  ' "$ws_get_body")

  local ws_put_body ws_put_code ws_put_resp_file
  ws_put_body=$(jq -n --argjson core_team "$new_core_team" '{core_team: $core_team}')
  read -r ws_put_code ws_put_resp_file <<<"$(http_json PUT "${BASE}/api/v1/workspaces/${WS_ID}" "$ws_put_body")"
  if [ "$ws_put_code" != "200" ]; then
    echo "FAIL:PUT /api/v1/workspaces/${WS_ID} (adding ${agent_id}, ${suffix}, to core_team) returned HTTP ${ws_put_code}, expected 200. Body: $(trunc "$(cat "$ws_put_resp_file")" 300)"
    return 1
  fi

  echo "OK:${agent_id}"
  return 0
}

DENIAL_PATTERN='deni(ed|al)|not allowed|polic(y|ies)|Rejected'

# ---------------------------------------------------------------------------
# Prerequisite: pick an ordinary builtin subject tool from AGENT_ID's
# COMPLETE policy map (bash exists for every agent per CLAUDE.md Constraint
# #6; fall back to search_web if it is somehow absent).
# ---------------------------------------------------------------------------
read -r AGENT_TOOLS_CODE AGENT_TOOLS_BODY <<<"$(http_get "${BASE}/api/v1/agents/${AGENT_ID}/tools")"

if [ "$AGENT_TOOLS_CODE" != "200" ]; then
  report "T.14" "N/A" "GET /api/v1/agents/${AGENT_ID}/tools returned HTTP ${AGENT_TOOLS_CODE}, expected 200"
  report "T.15" "N/A" "GET /api/v1/agents/${AGENT_ID}/tools returned HTTP ${AGENT_TOOLS_CODE}, expected 200"
  exit "$OVERALL_FAIL"
fi

BASE_POLICIES=$(jq -c '.config.builtin.policies // {}' "$AGENT_TOOLS_BODY")

SUBJECT_TOOL="bash"
if ! printf '%s' "$BASE_POLICIES" | jq -e --arg t "$SUBJECT_TOOL" 'has($t)' >/dev/null 2>&1; then
  SUBJECT_TOOL="search_web"
fi
if ! printf '%s' "$BASE_POLICIES" | jq -e --arg t "$SUBJECT_TOOL" 'has($t)' >/dev/null 2>&1; then
  report "T.14" "N/A" "neither 'bash' nor 'search_web' present in .config.builtin.policies for agent ${AGENT_ID} — no subject tool to build the deny/allow condition against"
  report "T.15" "N/A" "neither 'bash' nor 'search_web' present in .config.builtin.policies for agent ${AGENT_ID} — no subject tool to build the deny/allow condition against"
  exit "$OVERALL_FAIL"
fi

# ---------------------------------------------------------------------------
# Create AGENT_DENY (SUBJECT_TOOL forced to "deny") — needed by both T.14
# and T.15. If this fails, neither check has a prerequisite to run against.
# ---------------------------------------------------------------------------
DENY_RESULT=$(create_probe_agent "deny" "deny")
if [[ "$DENY_RESULT" == FAIL:* ]]; then
  DENY_FAIL_REASON="${DENY_RESULT#FAIL:}"
  report "T.14" "N/A" "could not create/configure AGENT_DENY: ${DENY_FAIL_REASON}"
  report "T.15" "N/A" "could not create/configure AGENT_DENY: ${DENY_FAIL_REASON}"
  exit "$OVERALL_FAIL"
fi
AGENT_DENY_ID="${DENY_RESULT#OK:}"

# ---------------------------------------------------------------------------
# T.14 — SUBJECT_TOOL refused at load for AGENT_DENY (naming the policy),
#        allowed tool (write_file/read_file) still works in the same run.
# ---------------------------------------------------------------------------
MARKER="qa_probe_t14_$(date +%s)_$$"
PROMPT_T14="Do the following two steps in order and label each result clearly with STEP1: and STEP2: on their own lines. ${NEVER_RETRY_CLAUSE}

STEP1: Use the ToolSearch tool to load the tool named '${SUBJECT_TOOL}' by exact name. Report exactly what happened. If it is refused or denied, quote the refusal message verbatim after STEP1: and do not retry.

STEP2: Regardless of what happened in STEP1, use read_file to read the file named 'qa_probe_t14_marker.txt' in your workspace and report its exact content after STEP2:."

# Seed the marker on disk so the positive lower bound only needs read_file
# (allowed by default). AGENT_DENY is on WS_ID's team, so its work dir is the
# shared workspace work dir.
DENY_WORK="$OMNIPUS_HOME/workspaces/$WS_ID/work"
mkdir -p "$DENY_WORK" 2>/dev/null || true
printf '%s' "$MARKER" > "$DENY_WORK/qa_probe_t14_marker.txt" 2>/dev/null || true
T14_OUT_FILE=$(mk_tmp)
"$OMNIPUS_BIN" "$AGENT_DENY_ID" "$PROMPT_T14" >"$T14_OUT_FILE" 2>&1 || true
T14_OUT=$(cat "$T14_OUT_FILE")

DENIAL_MATCH=$(printf '%s' "$T14_OUT" | grep -iE "$DENIAL_PATTERN" || true)
MARKER_MATCH=$(printf '%s' "$T14_OUT" | grep -F "$MARKER" || true)

if [ -n "$DENIAL_MATCH" ] && [ -n "$MARKER_MATCH" ]; then
  report "T.14" "PASS" "ToolSearch('${SUBJECT_TOOL}') refused for AGENT_DENY (${AGENT_DENY_ID}), evidence: $(trunc "$DENIAL_MATCH" 200); positive lower bound confirmed marker '${MARKER}' read back via read_file: $(trunc "$MARKER_MATCH" 150)"
elif [ -z "$DENIAL_MATCH" ]; then
  report "T.14" "FAIL" "expected a policy/denial refusal for ToolSearch('${SUBJECT_TOOL}') on AGENT_DENY (${AGENT_DENY_ID}, policy explicitly set to deny) but none was observed. Full output: $(trunc "$T14_OUT" 500)"
else
  report "T.14" "FAIL" "denial for '${SUBJECT_TOOL}' was observed on AGENT_DENY (${AGENT_DENY_ID}), but the allowed read_file positive-lower-bound check did not return marker '${MARKER}' (gateway may be refusing everything). Full output: $(trunc "$T14_OUT" 500)"
fi

# ---------------------------------------------------------------------------
# T.15 — per-agent policy does not leak: AGENT_DENY and AGENT_ALLOW resolve
#        SUBJECT_TOOL to opposite verdicts.
# ---------------------------------------------------------------------------
ALLOW_RESULT=$(create_probe_agent "allow" "allow")
if [[ "$ALLOW_RESULT" == FAIL:* ]]; then
  ALLOW_FAIL_REASON="${ALLOW_RESULT#FAIL:}"
  report "T.15" "N/A" "could not create/configure AGENT_ALLOW: ${ALLOW_FAIL_REASON}"
else
  AGENT_ALLOW_ID="${ALLOW_RESULT#OK:}"

  PROMPT_T15="Use the ToolSearch tool to load the tool named '${SUBJECT_TOOL}' by exact name. Report exactly what happened. ${NEVER_RETRY_CLAUSE}"

  A1_OUT_FILE=$(mk_tmp)
  "$OMNIPUS_BIN" "$AGENT_DENY_ID" "$PROMPT_T15" >"$A1_OUT_FILE" 2>&1 || true
  A1_OUT=$(cat "$A1_OUT_FILE")

  A2_OUT_FILE=$(mk_tmp)
  "$OMNIPUS_BIN" "$AGENT_ALLOW_ID" "$PROMPT_T15" >"$A2_OUT_FILE" 2>&1 || true
  A2_OUT=$(cat "$A2_OUT_FILE")

  A1_DENIED=$(printf '%s' "$A1_OUT" | grep -iE "$DENIAL_PATTERN" || true)
  A2_DENIED=$(printf '%s' "$A2_OUT" | grep -iE "$DENIAL_PATTERN" || true)

  if [ -n "$A1_DENIED" ] && [ -z "$A2_DENIED" ]; then
    report "T.15" "PASS" "tool '${SUBJECT_TOOL}' denied for AGENT_DENY (${AGENT_DENY_ID}: $(trunc "$A1_DENIED" 150)) but NOT denied for AGENT_ALLOW (${AGENT_ALLOW_ID}, policy explicitly set to allow) — verdicts differ as expected, no cross-agent leakage"
  elif [ -z "$A1_DENIED" ]; then
    report "T.15" "FAIL" "expected '${SUBJECT_TOOL}' to be denied for AGENT_DENY (${AGENT_DENY_ID}, policy explicitly set to deny) but no denial evidence was observed. AGENT_DENY output: $(trunc "$A1_OUT" 300)"
  else
    report "T.15" "FAIL" "expected '${SUBJECT_TOOL}' to be allowed for AGENT_ALLOW (${AGENT_ALLOW_ID}, policy explicitly set to allow) but it was refused too — policy leaked from AGENT_DENY or was not applied. AGENT_ALLOW output: $(trunc "$A2_OUT" 300)"
  fi
fi

exit "$OVERALL_FAIL"

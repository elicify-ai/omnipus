#!/usr/bin/env bash
#
# T.14 / T.15 — live sandbox UAT probes for per-agent tool policy enforcement.
#
# T.14: a tool denied by policy is refused at LOAD (via the load_tool infra
#       tool), the refusal names the policy, AND an allowed tool (read_file)
#       still works in the same run (positive lower bound — otherwise a
#       gateway that refuses everything would pass this check).
#
# T.15: per-agent policy does not leak between agents — the same tool
#       resolves to different effective verdicts for two different agents
#       when their policy maps say so.
#
# Env inputs (required): BASE, TOK, OMNIPUS_BIN, OMNIPUS_HOME, WS_ID, AGENT_ID.
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
# Prerequisite: read AGENT_ID's effective tool policy map.
# ---------------------------------------------------------------------------
read -r AGENT_TOOLS_CODE AGENT_TOOLS_BODY <<<"$(http_get "${BASE}/api/v1/agents/${AGENT_ID}/tools")"

if [ "$AGENT_TOOLS_CODE" != "200" ]; then
  report "T.14" "N/A" "GET /api/v1/agents/${AGENT_ID}/tools returned HTTP ${AGENT_TOOLS_CODE}, expected 200"
  report "T.15" "N/A" "GET /api/v1/agents/${AGENT_ID}/tools returned HTTP ${AGENT_TOOLS_CODE}, expected 200"
  exit "$OVERALL_FAIL"
fi

# Prefer set_config / create_agent as the denied tool (called out in the task
# brief); fall back to the first tool whose effective_policy is "deny".
DENIED_TOOL=$(jq -r '
  .tools as $t
  | ( [$t[] | select(.name=="set_config" and .effective_policy=="deny")]
    + [$t[] | select(.name=="create_agent" and .effective_policy=="deny")]
    + [$t[] | select(.effective_policy=="deny")]
    ) | (.[0].name // empty)
' "$AGENT_TOOLS_BODY" 2>/dev/null)

READ_FILE_POLICY=$(jq -r '.tools[]? | select(.name=="read_file") | .effective_policy' "$AGENT_TOOLS_BODY" 2>/dev/null | head -n1)

# ---------------------------------------------------------------------------
# T.14 — denied tool refused at load (naming the policy), allowed tool works.
# ---------------------------------------------------------------------------
if [ -z "$DENIED_TOOL" ]; then
  report "T.14" "N/A" "no tool with effective_policy=deny found for agent ${AGENT_ID} in GET /api/v1/agents/${AGENT_ID}/tools"
elif [ "$READ_FILE_POLICY" != "allow" ]; then
  report "T.14" "N/A" "read_file effective_policy for agent ${AGENT_ID} is '${READ_FILE_POLICY:-<absent>}', expected 'allow' — cannot run the positive-lower-bound check"
else
  MARKER="qa_probe_t14_$(date +%s)_$$"
  PROMPT_T14="Do the following two steps in order and label each result clearly with STEP1: and STEP2: on their own lines. ${NEVER_RETRY_CLAUSE}

STEP1: Use the load_tool tool to load the tool named '${DENIED_TOOL}' by exact name. Report exactly what happened. If it is refused or denied, quote the refusal message verbatim after STEP1: and do not retry.

STEP2: Regardless of what happened in STEP1, use write_file to write the exact text '${MARKER}' (nothing else) to a file named 'qa_probe_t14_marker.txt' in your workspace. Then use read_file to read that same file back. Report the exact content you read after STEP2:."

  T14_OUT_FILE=$(mk_tmp)
  "$OMNIPUS_BIN" "$AGENT_ID" "$PROMPT_T14" >"$T14_OUT_FILE" 2>&1 || true
  T14_OUT=$(cat "$T14_OUT_FILE")

  DENIAL_MATCH=$(printf '%s' "$T14_OUT" | grep -iE 'deni(ed|al)|not allowed|polic(y|ies)' || true)
  MARKER_MATCH=$(printf '%s' "$T14_OUT" | grep -F "$MARKER" || true)

  if [ -n "$DENIAL_MATCH" ] && [ -n "$MARKER_MATCH" ]; then
    report "T.14" "PASS" "load_tool('${DENIED_TOOL}') refused, evidence: $(trunc "$DENIAL_MATCH" 200); read_file positive check confirmed marker '${MARKER}' round-tripped: $(trunc "$MARKER_MATCH" 150)"
  elif [ -z "$DENIAL_MATCH" ]; then
    report "T.14" "FAIL" "expected a policy/denial refusal for load_tool('${DENIED_TOOL}') but none was observed. Full output: $(trunc "$T14_OUT" 500)"
  else
    report "T.14" "FAIL" "denial for '${DENIED_TOOL}' was observed, but the allowed read_file/write_file positive-lower-bound check did not round-trip marker '${MARKER}' (gateway may be refusing everything). Full output: $(trunc "$T14_OUT" 500)"
  fi
fi

# ---------------------------------------------------------------------------
# T.15 — per-agent policy does not leak between agents.
# ---------------------------------------------------------------------------
if [ -z "$DENIED_TOOL" ]; then
  report "T.15" "N/A" "no tool with effective_policy=deny found for agent ${AGENT_ID} — no differentiation baseline to build the second agent's opposite policy against"
else
  TOOL_T15="$DENIED_TOOL"
  NEW_AGENT_NAME="qa-probe-t15-$(date +%s)-$$"
  CREATE_BODY=$(jq -n --arg name "$NEW_AGENT_NAME" '{
    type: "Main",
    name: $name,
    soul: "You are a disposable QA probe agent created only to verify tool-policy isolation between agents. Be concise and follow instructions exactly."
  }')

  read -r CREATE_CODE CREATE_RESP_FILE <<<"$(http_json POST "${BASE}/api/v1/agents" "$CREATE_BODY")"

  if [ "$CREATE_CODE" != "201" ]; then
    report "T.15" "N/A" "POST /api/v1/agents to create the second probe agent returned HTTP ${CREATE_CODE}, expected 201. Body: $(trunc "$(cat "$CREATE_RESP_FILE")" 300)"
  else
    AGENT2_ID=$(jq -r '.id // empty' "$CREATE_RESP_FILE")
    if [ -z "$AGENT2_ID" ]; then
      report "T.15" "N/A" "POST /api/v1/agents returned 201 but no .id field in the response: $(trunc "$(cat "$CREATE_RESP_FILE")" 300)"
    else
      # Read agent2's complete builtin policy map (PUT requires the full map, no gaps).
      read -r AGENT2_TOOLS_CODE AGENT2_TOOLS_BODY <<<"$(http_get "${BASE}/api/v1/agents/${AGENT2_ID}/tools")"

      if [ "$AGENT2_TOOLS_CODE" != "200" ]; then
        report "T.15" "N/A" "GET /api/v1/agents/${AGENT2_ID}/tools returned HTTP ${AGENT2_TOOLS_CODE}, expected 200"
      else
        # Build the full policy map from agent2's current config, forcing
        # TOOL_T15 to "allow" (opposite of AGENT_ID's "deny" for the same tool).
        UPDATED_POLICIES=$(jq --arg tool "$TOOL_T15" '
          .config.builtin.policies as $p
          | ($p // {}) + {($tool): "allow"}
        ' "$AGENT2_TOOLS_BODY")

        if [ "$UPDATED_POLICIES" = "null" ] || [ -z "$UPDATED_POLICIES" ]; then
          report "T.15" "N/A" "could not derive a complete policy map from GET /api/v1/agents/${AGENT2_ID}/tools to PUT back (config.builtin.policies missing)"
        else
          PUT_BODY=$(jq -n --argjson policies "$UPDATED_POLICIES" '{builtin: {policies: $policies}}')
          read -r PUT_CODE PUT_RESP_FILE <<<"$(http_json PUT "${BASE}/api/v1/agents/${AGENT2_ID}/tools" "$PUT_BODY")"

          if [ "$PUT_CODE" != "200" ]; then
            report "T.15" "N/A" "PUT /api/v1/agents/${AGENT2_ID}/tools (setting '${TOOL_T15}'=allow) returned HTTP ${PUT_CODE}, expected 200. Body: $(trunc "$(cat "$PUT_RESP_FILE")" 300)"
          else
            # Add AGENT2 to the workspace's core_team (GET-modify-PUT, full replacement field).
            read -r WS_GET_CODE WS_GET_BODY <<<"$(http_get "${BASE}/api/v1/workspaces/${WS_ID}")"

            if [ "$WS_GET_CODE" != "200" ]; then
              report "T.15" "N/A" "GET /api/v1/workspaces/${WS_ID} returned HTTP ${WS_GET_CODE}, expected 200"
            else
              NEW_CORE_TEAM=$(jq --arg id "$AGENT2_ID" '
                (.core_team // []) as $ct
                | if ($ct | index($id)) then $ct else $ct + [$id] end
              ' "$WS_GET_BODY")

              WS_PUT_BODY=$(jq -n --argjson core_team "$NEW_CORE_TEAM" '{core_team: $core_team}')
              read -r WS_PUT_CODE WS_PUT_RESP_FILE <<<"$(http_json PUT "${BASE}/api/v1/workspaces/${WS_ID}" "$WS_PUT_BODY")"

              if [ "$WS_PUT_CODE" != "200" ]; then
                report "T.15" "N/A" "PUT /api/v1/workspaces/${WS_ID} (adding ${AGENT2_ID} to core_team) returned HTTP ${WS_PUT_CODE}, expected 200. Body: $(trunc "$(cat "$WS_PUT_RESP_FILE")" 300)"
              else
                PROMPT_T15="Use the load_tool tool to load the tool named '${TOOL_T15}' by exact name. Report exactly what happened. ${NEVER_RETRY_CLAUSE}"

                A1_OUT_FILE=$(mk_tmp)
                "$OMNIPUS_BIN" "$AGENT_ID" "$PROMPT_T15" >"$A1_OUT_FILE" 2>&1 || true
                A1_OUT=$(cat "$A1_OUT_FILE")

                A2_OUT_FILE=$(mk_tmp)
                "$OMNIPUS_BIN" "$AGENT2_ID" "$PROMPT_T15" >"$A2_OUT_FILE" 2>&1 || true
                A2_OUT=$(cat "$A2_OUT_FILE")

                A1_DENIED=$(printf '%s' "$A1_OUT" | grep -iE 'deni(ed|al)|not allowed|polic(y|ies)' || true)
                A2_DENIED=$(printf '%s' "$A2_OUT" | grep -iE 'deni(ed|al)|not allowed|polic(y|ies)' || true)

                if [ -n "$A1_DENIED" ] && [ -z "$A2_DENIED" ]; then
                  report "T.15" "PASS" "tool '${TOOL_T15}' denied for ${AGENT_ID} ($(trunc "$A1_DENIED" 150)) but NOT denied for ${AGENT2_ID} (policy explicitly set to allow) — verdicts differ as expected, no cross-agent leakage"
                elif [ -z "$A1_DENIED" ]; then
                  report "T.15" "FAIL" "expected '${TOOL_T15}' to still be denied for ${AGENT_ID} but no denial evidence was observed. ${AGENT_ID} output: $(trunc "$A1_OUT" 300)"
                else
                  report "T.15" "FAIL" "expected '${TOOL_T15}' to be allowed for ${AGENT2_ID} (policy set to allow) but it was refused too — policy leaked or was not applied. ${AGENT2_ID} output: $(trunc "$A2_OUT" 300)"
                fi
              fi
            fi
          fi
        fi
      fi
    fi
  fi
fi

exit "$OVERALL_FAIL"

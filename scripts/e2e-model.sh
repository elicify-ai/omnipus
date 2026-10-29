#!/usr/bin/env bash
# e2e-model.sh — the one shell reader of tests/e2e/e2e-model.json.
#
# The real-LLM CI surfaces (pr.yml, sandbox-uat.yml, evals-nightly.yml and
# the Fly runner deploy/ci-worker/runci.sh) read the central model file only
# through this script, so the shape rules and the two-row config seed live in
# one place instead of three hand-copied heredocs.
#
# The file names a primary and a fallback (provider, model, api_key_ref)
# pair. api_key_ref is the PRODUCT credential name a provider row points at
# (for example MINIMAX_API_KEY); CI secret names are a workflow concern.
#
# Subcommands (repo-relative default file: tests/e2e/e2e-model.json):
#
#   env [--file PATH] [--legacy-ok]
#       Print six KEY=value lines (E2E_PRIMARY_PROVIDER/_MODEL/_KEYREF and
#       E2E_FALLBACK_*) for "$GITHUB_ENV" or eval. Fails closed (exit 2) on a
#       missing or empty field, an equal primary/fallback pair, or the legacy
#       {"model": ...} shape. --legacy-ok (runci.sh only, so the shared Fly
#       runner still works against branches cut before the switch) maps the
#       legacy shape to an openrouter primary with an empty fallback and a WARN.
#
#   seed-config <port> [--file PATH] [--route primary|fallback]
#       Print the two-row CI config.json: both provider rows (api_base from
#       the embedded provider catalog) and agents.defaults.default_model = the
#       primary pair. --route fallback (after a failed job-start check) makes
#       the fallback pair the default model; the rows are unchanged.
#
#   seed-agent-fallbacks <base-url> <token> [--file PATH]
#       GET <base-url>/api/v1/agents, then PUT {"fallback_models":[fallback]}
#       on every returned agent. Exit 1 on any non-2xx answer.
#
#   preflight [--file PATH] [--route-out PATH] [--strict]
#       The job-start check: one 1-token Anthropic-messages call to the
#       primary, always at the provider's catalog URL. 2xx with a JSON body
#       prints LLM_ROUTE=primary. 429, 5xx, 408, 401/403, a timeout or a
#       connection failure print LLM_ROUTE=fallback plus a ::warning:: naming
#       the status and the provider's error type (never the body or the key).
#       Any other status, or a missing secret, exits 1.
#       --strict (every CI caller passes it): a 401/403 is a broken or revoked
#       key, not an outage — exit 1 with an ::error:: instead of falling back
#       for ever; and a 2xx must be a finished message (type "message", a
#       string stop_reason, and base_resp.status_code 0 when base_resp is
#       present), else exit 1. The content is not checked (null with
#       max_tokens 1 on a reasoning model). Without --strict the contract-pinned
#       behaviour holds (401/403 fall back; any JSON object is a 2xx answer).
#       Writes llm-route.json (cwd, or --route-out) and, under GitHub Actions,
#       one line to the job summary.
#       Env: E2E_PREFLIGHT_URL — TEST HOOK ONLY: accepted solely when it is a
#            plain-http loopback URL (http://127.0.0.1, localhost or [::1]),
#            so the primary key can never be sent off the machine through it;
#            any other value is refused (exit 2),
#            E2E_PREFLIGHT_TIMEOUT_SECONDS (default 10),
#            secrets by api_key_ref: MINIMAX_API_KEY -> MINIMAX_API_KEY_CI then
#            MINIMAX_API_KEY; OPENROUTER_API_KEY -> OPENROUTER_API_KEY_CI then
#            OPENROUTER_API_KEY (first non-empty wins).
#
#   note-fallbacks <sessions-dir> [--file PATH] [--route-out PATH]
#       After the tests: count the product's fallback notes ("Answered by the
#       Fallback model (<fallback model>) ...") in the transcripts under
#       <sessions-dir>. Any note means agents fell back MID-RUN: a ::warning::,
#       a job-summary line, and llm-route.json no longer says "primary" — its
#       route becomes "primary_then_fallback" with "fallback_notes": N (a job
#       that started on the fallback keeps "fallback"). Prints
#       FALLBACK_NOTES=<N>. A missing directory counts as 0.
#
# Secrets: this script never prints, logs or writes a key. Keys reach curl on
# stdin (curl --config -), never on the command line.
#
# Switching the model: edit tests/e2e/e2e-model.json (both entries) AND add
# every new model or provider id to PATTERN in
# scripts/check-no-hardcoded-e2e-model.sh.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_FILE="$REPO_ROOT/tests/e2e/e2e-model.json"
CATALOG="$REPO_ROOT/pkg/providers/catalog/data/providers_catalog.json"

die() { # die <exit-code> <message...>
  local code="$1"; shift
  echo "e2e-model.sh: $*" >&2
  exit "$code"
}

usage() {
  sed -n '2,/^set -uo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//; /^set -uo/d' >&2
  exit 2
}

command -v jq >/dev/null 2>&1 || die 2 "jq is required"

# ---------------------------------------------------------------------------
# Reading the central file
# ---------------------------------------------------------------------------

# Populated by load_pair (the fallback fields stay empty for a --legacy-ok read).
P_PROVIDER="" P_MODEL="" P_KEYREF="" F_PROVIDER="" F_MODEL="" F_KEYREF=""

# trimmed_field <file> <jq-path>: print the trimmed string at path, or nothing
# when the value is absent, not a string, or whitespace-only.
trimmed_field() {
  jq -r "($1) | if type == \"string\" then gsub(\"^\\\\s+|\\\\s+\$\"; \"\") else \"\" end" "$2"
}

# load_pair <file> <legacy_ok 0|1>
load_pair() {
  local file="$1" legacy_ok="$2"
  [ -f "$file" ] || die 2 "e2e-model.json not found at $file"
  jq -e 'type == "object"' "$file" >/dev/null 2>&1 \
    || die 2 "e2e-model.json at $file is not a JSON object"

  local has_primary has_fallback has_legacy
  has_primary="$(jq -r 'has("primary")' "$file")"
  has_fallback="$(jq -r 'has("fallback")' "$file")"
  has_legacy="$(jq -r 'has("model")' "$file")"

  if [ "$has_primary" = "false" ] && [ "$has_legacy" = "true" ]; then
    if [ "$legacy_ok" != "1" ]; then
      die 2 "e2e-model.json at $file is the old shape {\"model\": ...}; it must carry .primary and .fallback (provider, model, api_key_ref)"
    fi
    local legacy_model
    legacy_model="$(trimmed_field '.model' "$file")"
    [ -n "$legacy_model" ] || die 2 "e2e-model.json at $file: old shape with an empty .model"
    echo "e2e-model.sh: WARN — $file is the old shape {\"model\": ...}; reading it as an openrouter primary with NO fallback (--legacy-ok, for branches cut before the MiniMax switch)" >&2
    P_PROVIDER="openrouter" P_MODEL="$legacy_model" P_KEYREF="OPENROUTER_API_KEY"
    F_PROVIDER="" F_MODEL="" F_KEYREF=""
    return 0
  fi

  if [ "$has_primary" != "true" ] || ! jq -e '.primary | type == "object"' "$file" >/dev/null; then
    die 2 "e2e-model.json at $file: .primary object is missing"
  fi
  if [ "$has_fallback" != "true" ] || ! jq -e '.fallback | type == "object"' "$file" >/dev/null; then
    die 2 "e2e-model.json at $file: .fallback object is missing"
  fi

  P_PROVIDER="$(trimmed_field '.primary.provider' "$file")"
  P_MODEL="$(trimmed_field '.primary.model' "$file")"
  P_KEYREF="$(trimmed_field '.primary.api_key_ref' "$file")"
  F_PROVIDER="$(trimmed_field '.fallback.provider' "$file")"
  F_MODEL="$(trimmed_field '.fallback.model' "$file")"
  F_KEYREF="$(trimmed_field '.fallback.api_key_ref' "$file")"

  local name var
  for name in primary.provider primary.model primary.api_key_ref \
              fallback.provider fallback.model fallback.api_key_ref; do
    case "$name" in
      primary.provider) var="$P_PROVIDER" ;;
      primary.model) var="$P_MODEL" ;;
      primary.api_key_ref) var="$P_KEYREF" ;;
      fallback.provider) var="$F_PROVIDER" ;;
      fallback.model) var="$F_MODEL" ;;
      fallback.api_key_ref) var="$F_KEYREF" ;;
    esac
    [ -n "$var" ] || die 2 "e2e-model.json at $file: .$name is missing, empty or whitespace-only"
  done

  if [ "$P_PROVIDER" = "$F_PROVIDER" ] && [ "$P_MODEL" = "$F_MODEL" ]; then
    die 2 "e2e-model.json at $file: primary and fallback are the same provider/model ($P_PROVIDER/$P_MODEL); the fallback must differ"
  fi
}

# parse_common_opts sets FILE, LEGACY_OK, ROUTE_OUT, ROUTE from the remaining args;
# positional args land in POSITIONAL.
FILE="$DEFAULT_FILE" LEGACY_OK=0 ROUTE_OUT="" ROUTE="primary" STRICT=0
POSITIONAL=()
parse_opts() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --file) [ $# -ge 2 ] || die 2 "--file needs a path"; FILE="$2"; shift 2 ;;
      --file=*) FILE="${1#--file=}"; shift ;;
      --legacy-ok) LEGACY_OK=1; shift ;;
      --strict) STRICT=1; shift ;;
      --route-out) [ $# -ge 2 ] || die 2 "--route-out needs a path"; ROUTE_OUT="$2"; shift 2 ;;
      --route-out=*) ROUTE_OUT="${1#--route-out=}"; shift ;;
      --route) [ $# -ge 2 ] || die 2 "--route needs primary or fallback"; ROUTE="$2"; shift 2 ;;
      --route=*) ROUTE="${1#--route=}"; shift ;;
      -h|--help) usage ;;
      --*) die 2 "unknown option $1" ;;
      *) POSITIONAL+=("$1"); shift ;;
    esac
  done
}

# catalog_field <provider-id> <field>: a provider's field from the embedded catalog.
catalog_field() {
  [ -f "$CATALOG" ] || die 2 "provider catalog not found at $CATALOG"
  jq -r --arg id "$1" --arg f "$2" \
    '[.providers[] | select(.id == $id)][0][$f] // empty' "$CATALOG"
}

# ---------------------------------------------------------------------------
# env
# ---------------------------------------------------------------------------
cmd_env() {
  parse_opts "$@"
  [ ${#POSITIONAL[@]} -eq 0 ] || die 2 "env takes no positional arguments"
  load_pair "$FILE" "$LEGACY_OK"
  printf 'E2E_PRIMARY_PROVIDER=%s\n' "$P_PROVIDER"
  printf 'E2E_PRIMARY_MODEL=%s\n' "$P_MODEL"
  printf 'E2E_PRIMARY_KEYREF=%s\n' "$P_KEYREF"
  printf 'E2E_FALLBACK_PROVIDER=%s\n' "$F_PROVIDER"
  printf 'E2E_FALLBACK_MODEL=%s\n' "$F_MODEL"
  printf 'E2E_FALLBACK_KEYREF=%s\n' "$F_KEYREF"
}

# ---------------------------------------------------------------------------
# seed-config
# ---------------------------------------------------------------------------
cmd_seed_config() {
  parse_opts "$@"
  [ ${#POSITIONAL[@]} -eq 1 ] || die 2 "seed-config needs exactly one argument: the gateway port"
  local port="${POSITIONAL[0]}"
  [[ "$port" =~ ^[0-9]+$ ]] || die 2 "seed-config: port '$port' is not a decimal integer"
  case "$ROUTE" in primary|fallback) ;; *) die 2 "seed-config: --route must be primary or fallback, got '$ROUTE'" ;; esac
  load_pair "$FILE" 0

  local p_base f_base
  p_base="$(catalog_field "$P_PROVIDER" api)"
  f_base="$(catalog_field "$F_PROVIDER" api)"
  [ -n "$p_base" ] || die 2 "seed-config: provider '$P_PROVIDER' has no api base in the catalog"
  [ -n "$f_base" ] || die 2 "seed-config: provider '$F_PROVIDER' has no api base in the catalog"

  local d_provider="$P_PROVIDER" d_model="$P_MODEL"
  if [ "$ROUTE" = "fallback" ]; then
    d_provider="$F_PROVIDER" d_model="$F_MODEL"
  fi

  # The layout (compact one-line objects, then expanded rows) is the committed
  # golden pkg/config/testdata/ci_two_row_seed.golden.json. Values are
  # JSON-encoded through jq so a quote in a value can never break the document.
  jq -nr \
    --argjson port "$port" \
    --arg dp "$d_provider" --arg dm "$d_model" \
    --arg pp "$P_PROVIDER" --arg pm "$P_MODEL" --arg pb "$p_base" --arg pk "$P_KEYREF" \
    --arg fp "$F_PROVIDER" --arg fm "$F_MODEL" --arg fb "$f_base" --arg fk "$F_KEYREF" '
    def row($p; $m; $b; $k):
      "    {\n" +
      "      \"provider\": \($p | tojson),\n" +
      "      \"model\": \($m | tojson),\n" +
      "      \"api_base\": \($b | tojson),\n" +
      "      \"api_key_ref\": \($k | tojson)\n" +
      "    }";
    "{\n" +
    "  \"version\": 1,\n" +
    "  \"gateway\": { \"port\": \($port), \"dev_mode_bypass\": true },\n" +
    "  \"sandbox\": { \"audit_log\": true, \"tool_policies\": { \"spawn\": \"allow\" } },\n" +
    "  \"agents\": { \"defaults\": { \"default_model\": { \"provider\": \($dp | tojson), \"model\": \($dm | tojson) }, \"auto_recap_enabled\": true } },\n" +
    "  \"providers\": [\n" +
    row($pp; $pm; $pb; $pk) + ",\n" +
    row($fp; $fm; $fb; $fk) + "\n" +
    "  ]\n" +
    "}"'
}

# ---------------------------------------------------------------------------
# seed-agent-fallbacks
# ---------------------------------------------------------------------------

# curl_auth <token> <curl args...>: run curl with the bearer token passed on
# stdin, never on the command line. Prints the HTTP status; body to $BODY_FILE.
curl_auth() {
  local token="$1"; shift
  printf 'header = "Authorization: Bearer %s"\n' "$(printf '%s' "$token" | sed 's/[\\"]/\\&/g')" \
    | curl -sS --config - -o "$BODY_FILE" -w '%{http_code}' "$@"
}

cmd_seed_agent_fallbacks() {
  parse_opts "$@"
  [ ${#POSITIONAL[@]} -eq 2 ] || die 2 "seed-agent-fallbacks needs <base-url> <token>"
  local base="${POSITIONAL[0]%/}" token="${POSITIONAL[1]}"
  load_pair "$FILE" 0

  local work
  work="$(mktemp -d "${TMPDIR:-/tmp}/e2e-model-seed.XXXXXX")" || die 1 "cannot create a temp dir"
  # EXIT, not RETURN: die() exits the script, which never runs a RETURN trap
  # and would leave the temp dir (and the agents response) behind.
  # shellcheck disable=SC2064
  trap "rm -rf '$work'" EXIT
  BODY_FILE="$work/body"

  local status
  status="$(curl_auth "$token" --max-time 30 "$base/api/v1/agents")" \
    || die 1 "GET $base/api/v1/agents failed (curl error)"
  [[ "$status" =~ ^2[0-9][0-9]$ ]] || die 1 "GET $base/api/v1/agents answered HTTP $status"

  local ids
  ids="$(jq -r 'if type == "array" then .[] | .id // empty else error("not an array") end' "$BODY_FILE" 2>/dev/null)" \
    || die 1 "GET $base/api/v1/agents did not return a JSON array"
  [ -n "$ids" ] || die 1 "GET $base/api/v1/agents returned no agents; nothing to give a fallback"

  local body id n=0
  body="$(jq -cn --arg p "$F_PROVIDER" --arg m "$F_MODEL" '{fallback_models: [{provider: $p, model: $m}]}')"
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    status="$(curl_auth "$token" --max-time 30 -X PUT -H 'Content-Type: application/json' \
      --data "$body" "$base/api/v1/agents/$id")" \
      || die 1 "PUT $base/api/v1/agents/$id failed (curl error)"
    [[ "$status" =~ ^2[0-9][0-9]$ ]] || die 1 "PUT $base/api/v1/agents/$id answered HTTP $status"
    n=$((n + 1))
  done <<<"$ids"
  echo "e2e-model.sh: seeded fallback $F_PROVIDER/$F_MODEL on $n agent(s)" >&2
}

# ---------------------------------------------------------------------------
# preflight
# ---------------------------------------------------------------------------

# secret_envs <api_key_ref>: the env names to try, in order.
secret_envs() {
  case "$1" in
    MINIMAX_API_KEY) echo "MINIMAX_API_KEY_CI MINIMAX_API_KEY" ;;
    OPENROUTER_API_KEY) echo "OPENROUTER_API_KEY_CI OPENROUTER_API_KEY" ;;
    *) echo "${1}_CI ${1}" ;;
  esac
}

# provider_label <provider-id>: the name used in the warning line.
provider_label() {
  case "$1" in
    minimax*) echo "MiniMax" ;;
    *) echo "$1" ;;
  esac
}

write_route() { # write_route <route> <status> <error_type>
  local out="${ROUTE_OUT:-llm-route.json}"
  jq -cn --arg r "$1" --arg s "$2" --arg e "$3" '{route: $r, status: $s, error_type: $e}' >"$out" \
    || die 1 "cannot write $out"
}

summary_line() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    printf '%s\n' "$1" >>"$GITHUB_STEP_SUMMARY" 2>/dev/null || true
  fi
}

route_fallback() { # route_fallback <status> <error_type>
  local label
  label="$(provider_label "$P_PROVIDER")"
  write_route fallback "$1" "$2"
  echo "::warning::LLM: fallback (primary $label unavailable: $1 $2)" >&2
  summary_line "LLM: fallback (primary $label unavailable: $1 $2) — this job runs on $F_PROVIDER/$F_MODEL"
  printf 'LLM_ROUTE=fallback\n'
  exit 0
}

# body_shape <file>: a SAFE one-line description of a JSON response for a
# failure message: the top-level key names, the values of .type,
# .error.type, .base_resp.status_code (the MiniMax status field) and
# .stop_reason when present, and the jq TYPE (never the value) of .content. Never the body, never free text: a key name or a .type value is
# shown only when it is a short lowercase identifier (^[a-z_][a-z0-9_]{0,31}$)
# — a key, a token or a sentence never matches — and the status code only
# when it is a number. Hidden key names are counted, not shown.
body_shape() {
  jq -r '
    def ident: type == "string" and test("^[a-z_][a-z0-9_]{0,31}$");
    (keys_unsorted) as $k
    | ([$k[] | select(ident)]) as $shown
    | (($k | length) - ($shown | length)) as $hidden
    | [ "top-level keys: " + (if ($shown | length) > 0 then ($shown[:20] | join(", ")) else "none shown" end)
          + (if $hidden > 0 then " (+\($hidden) not shown)" else "" end),
        (.type | select(ident) | ".type=" + .),
        (.error | objects | .type | select(ident) | ".error.type=" + .),
        (.base_resp | objects | .status_code | numbers | ".base_resp.status_code=" + tostring),
        (if has("content") then "content type: " + (.content | type) else empty end),
        (.stop_reason | select(ident) | "stop_reason=" + .)
      ] | join("; ")
  ' "$1" 2>/dev/null || echo "response shape unavailable"
}

cmd_preflight() {
  parse_opts "$@"
  [ ${#POSITIONAL[@]} -eq 0 ] || die 2 "preflight takes no positional arguments"
  load_pair "$FILE" 0

  # Secret lookup (first non-empty wins). Names only in messages.
  local names name key=""
  names="$(secret_envs "$P_KEYREF")"
  for name in $names; do
    if [ -n "${!name:-}" ]; then
      key="${!name}"
      break
    fi
  done
  if [ -z "$key" ]; then
    die 1 "preflight: no secret for the primary ($P_PROVIDER, api_key_ref $P_KEYREF): set one of ${names// / or }. This is a configuration error, not an outage — not falling back"
  fi

  local url="${E2E_PREFLIGHT_URL:-}"
  if [ -n "$url" ]; then
    # Test hook only: a loopback stub. Never a remote host, so the real key
    # cannot be redirected off the machine by an inherited variable.
    if ! [[ "$url" =~ ^http://(127\.0\.0\.1|localhost|\[::1\])(:[0-9]+)?(/|$) ]]; then
      die 2 "preflight: E2E_PREFLIGHT_URL is a test hook for a local stub and must be http://127.0.0.1, localhost or [::1]; refusing to send the primary key to any other address"
    fi
  else
    local proto base
    proto="$(catalog_field "$P_PROVIDER" protocol)"
    base="$(catalog_field "$P_PROVIDER" api)"
    if [ "$proto" != "anthropic" ] || [ -z "$base" ]; then
      die 1 "preflight: provider '$P_PROVIDER' is not an Anthropic-protocol catalog provider; extend this check"
    fi
    if [[ "$base" != https://* ]]; then
      die 1 "preflight: the catalog URL for '$P_PROVIDER' is not https; refusing to send the primary key"
    fi
    url="${base%/}/messages"
  fi
  local timeout="${E2E_PREFLIGHT_TIMEOUT_SECONDS:-10}"
  if ! [[ "$timeout" =~ ^[0-9]+$ ]] || [ "$timeout" -le 0 ]; then
    die 2 "E2E_PREFLIGHT_TIMEOUT_SECONDS must be a positive integer"
  fi

  local work
  work="$(mktemp -d "${TMPDIR:-/tmp}/e2e-model-preflight.XXXXXX")" || die 1 "cannot create a temp dir"
  # shellcheck disable=SC2064
  trap "rm -rf '$work'" EXIT
  local body
  body="$(jq -cn --arg m "$P_MODEL" '{model: $m, max_tokens: 1, messages: [{role: "user", content: "ping"}]}')"

  local status crc
  status="$(printf 'header = "x-api-key: %s"\n' "$(printf '%s' "$key" | sed 's/[\\"]/\\&/g')" \
    | curl -s --config - -o "$work/body" -w '%{http_code}' --max-time "$timeout" \
        -X POST -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
        --data "$body" "$url" 2>/dev/null)"
  crc=$?
  unset key

  if [ "$crc" -ne 0 ]; then
    case "$crc" in
      7) route_fallback connection_refused unavailable ;;
      28) route_fallback timeout unavailable ;;
      5|6|35|52|55|56) route_fallback connection_error unavailable ;;
      *) die 1 "preflight: the request to the primary failed with curl exit $crc (not a recognised outage) — not falling back" ;;
    esac
  fi

  # The provider's error type, only when it is one of the known Anthropic
  # error types; the body itself is never printed (it can echo the key).
  local etype
  etype="$(jq -r '.error.type // empty' "$work/body" 2>/dev/null || true)"
  case "$etype" in
    authentication_error|permission_error|overloaded_error|rate_limit_error|api_error|not_found_error|invalid_request_error) ;;
    *) etype="" ;;
  esac

  case "$status" in
    2[0-9][0-9])
      # A 2xx must be a real answer: at least a JSON object, and under
      # --strict an Anthropic message. A captive portal or a proxy page
      # returning 200 is not "the primary works".
      if ! jq -e 'type == "object"' "$work/body" >/dev/null 2>&1; then
        die 1 "preflight: the primary answered HTTP $status with a body that is not a JSON object — not a working primary, and not an outage either; check the endpoint"
      fi
      # A working answer is a message that finished (a stop_reason string)
      # and, for MiniMax, a zero base_resp status. The content is NOT checked:
      # with max_tokens 1 a reasoning model answers content null (observed
      # 2026-10-29 for the CI primary), and that still proves key, model and
      # endpoint work.
      if [ "$STRICT" = 1 ] && ! jq -e '
            .type == "message"
            and (.stop_reason | type == "string")
            and ((has("base_resp") | not) or (.base_resp | type == "object" and .status_code == 0))
          ' "$work/body" >/dev/null 2>&1; then
        die 1 "preflight: the primary answered HTTP $status but not with a finished message (type \"message\", a stop_reason, base_resp status 0 when present) — refusing to report it as working ($(body_shape "$work/body")). An unknown model id is a common cause: check the primary model against the provider catalog"
      fi
      write_route primary "$status" ""
      summary_line "LLM: primary ($P_PROVIDER/$P_MODEL)"
      printf 'LLM_ROUTE=primary\n'
      exit 0 ;;
    401|403)
      if [ "$STRICT" = 1 ]; then
        echo "::error::LLM: the primary key was rejected (HTTP $status ${etype:-unavailable}) — a broken or revoked key is a configuration error, not an outage; fix the secret ($P_KEYREF) instead of running on the fallback" >&2
        summary_line "LLM: primary key REJECTED (HTTP $status) — job stopped; fix the $P_KEYREF secret"
        exit 1
      fi
      route_fallback "$status" "${etype:-unavailable}" ;;
    529)
      if [ "$etype" = "overloaded_error" ]; then
        route_fallback 529 overloaded_error
      fi
      route_fallback 529 unavailable ;;
    408|429|5[0-9][0-9])
      route_fallback "$status" unavailable ;;
    *)
      die 1 "preflight: the primary ($P_PROVIDER/$P_MODEL) answered HTTP $status${etype:+ ($etype)}; that is not an outage, so the job does not fall back — check the model id and the request" ;;
  esac
}

# ---------------------------------------------------------------------------
# note-fallbacks
# ---------------------------------------------------------------------------
cmd_note_fallbacks() {
  parse_opts "$@"
  [ ${#POSITIONAL[@]} -eq 1 ] || die 2 "note-fallbacks needs exactly one argument: the sessions directory"
  local dir="${POSITIONAL[0]}"
  load_pair "$FILE" 0
  local note="Answered by the Fallback model (${F_MODEL})"
  local count=0
  if [ -d "$dir" ]; then
    count="$(grep -rhoF -- "$note" "$dir" 2>/dev/null | wc -l | tr -d ' ')"
  fi
  printf 'FALLBACK_NOTES=%s\n' "$count"
  [ "$count" -gt 0 ] || return 0

  local out="${ROUTE_OUT:-llm-route.json}" route="primary"
  if [ -f "$out" ]; then
    route="$(jq -r '.route // "primary"' "$out" 2>/dev/null || echo primary)"
  fi
  if [ "$route" = "primary" ]; then
    route="primary_then_fallback"
  fi
  if [ -f "$out" ] && jq -e 'type == "object"' "$out" >/dev/null 2>&1; then
    if ! jq -c --arg r "$route" --argjson n "$count" '.route = $r | .fallback_notes = $n' "$out" >"$out.tmp" \
      || ! mv "$out.tmp" "$out"; then
      die 1 "cannot update $out"
    fi
  else
    jq -cn --arg r "$route" --argjson n "$count" '{route: $r, status: "", error_type: "", fallback_notes: $n}' >"$out" \
      || die 1 "cannot write $out"
  fi
  local label
  label="$(provider_label "$P_PROVIDER")"
  echo "::warning::LLM: $count answer(s) came from the fallback $F_PROVIDER/$F_MODEL mid-run (primary $label failed during the tests)" >&2
  summary_line "LLM: mid-run fallback — $count answer(s) from $F_PROVIDER/$F_MODEL (route: $route)"
}

# ---------------------------------------------------------------------------
[ $# -ge 1 ] || usage
sub="$1"; shift
case "$sub" in
  env) cmd_env "$@" ;;
  seed-config) cmd_seed_config "$@" ;;
  seed-agent-fallbacks) cmd_seed_agent_fallbacks "$@" ;;
  preflight) cmd_preflight "$@" ;;
  note-fallbacks) cmd_note_fallbacks "$@" ;;
  -h|--help|help) usage ;;
  *) die 2 "unknown subcommand '$sub' (env, seed-config, seed-agent-fallbacks, preflight, note-fallbacks)" ;;
esac

#!/usr/bin/env bash
# check-no-shell-deny-patterns-selfcheck.sh
#
# Proves that scripts/check-no-shell-deny-patterns.sh CAN fail — a guard that
# has never been seen to go red is not a guard (this repo's own
# docs/internal/false-green-patterns.md records a guard test that passed
# 673/673 with the feature it guarded deleted). Also proves each of the
# guard's narrow, documented exclusions actually passes clean, so the
# exclusions can't silently widen into "nothing is ever checked."
#
# Builds a throw-away synthetic tree (pkg/cmd/src/contracts, the four roots
# the real script requires to exist) and drives the real script against it
# with REPO_ROOT — never touches the real scripts/ or pkg/ directories.
#
# Exit: 0 all cases behave, 1 a case misbehaves, 2 harness could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-no-shell-deny-patterns.sh"
[ -f "$CHECK" ] || { echo "selfcheck: missing $CHECK" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/no-shell-deny-patterns.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT

fresh_tree() {
  rm -rf "$TMP/tree"
  mkdir -p "$TMP/tree/pkg" "$TMP/tree/cmd" "$TMP/tree/src" "$TMP/tree/contracts"
}

FAIL=0
expect() { # expect <label> <want-exit>
  local label="$1" want="$2" got
  REPO_ROOT="$TMP/tree" bash "$CHECK" >"$TMP/out" 2>&1
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "selfcheck FAIL: $label — wanted exit $want, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
  else
    echo "selfcheck ok:   $label (exit $got)"
  fi
}

# --- 1. clean synthetic tree -------------------------------------------------
fresh_tree
expect "clean synthetic tree" 0

# --- 2. block-list core symbol, live code ------------------------------------
fresh_tree
printf 'package tools\n\nvar defaultDenyPatterns = []string{"rm -rf"}\n' > "$TMP/tree/pkg/fixture.go"
expect "defaultDenyPatterns reintroduced in pkg/" 1

fresh_tree
printf 'package tools\n\n// defaultDenyPatterns used to live here (ADR-092).\n' > "$TMP/tree/pkg/fixture.go"
expect "defaultDenyPatterns mentioned in a comment only" 0

# --- 3. buildSecretGuardPatterns — banned outside pkg/fspolicy/, allowed inside
fresh_tree
printf 'package tools\n\nfunc buildSecretGuardPatterns() []string { return nil }\n' > "$TMP/tree/pkg/fixture.go"
expect "buildSecretGuardPatterns() reintroduced outside pkg/fspolicy/" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/fspolicy"
printf 'package fspolicy\n\nfunc buildSecretGuardPatterns() []string { return nil }\n' > "$TMP/tree/pkg/fspolicy/secretset.go"
expect "buildSecretGuardPatterns() inside pkg/fspolicy/ (allowed, different mechanism)" 0

# --- 4. config/wire field for the block list ---------------------------------
fresh_tree
printf 'package config\n\ntype SandboxConfig struct {\n\tShellDenyPatterns []string `json:"shell_deny_patterns,omitempty"`\n}\n' > "$TMP/tree/pkg/fixture.go"
expect "ShellDenyPatterns field reintroduced" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/sysagent/tools"
printf 'package systools\n\nvar blockedConfigKeys = []string{"sandbox.shell_deny_patterns"}\n' > "$TMP/tree/pkg/sysagent/tools/config.go"
expect "dotted sandbox.shell_deny_patterns key path (not the bare wire key) passes" 0

fresh_tree
mkdir -p "$TMP/tree/pkg/sysagent/tools"
printf 'package systools\n\nvar k = map[string]bool{"shell_deny_patterns": true}\n' > "$TMP/tree/pkg/sysagent/tools/config.go"
expect "bare \"shell_deny_patterns\" in sysagent/tools/config.go (whole-file exclusion removed)" 1

# --- 5. per-agent ShellPolicy surface -----------------------------------------
fresh_tree
printf 'package tools\n\ntype AgentShellPolicy struct {\n\tEnableDenyPatterns bool\n}\n' > "$TMP/tree/pkg/fixture.go"
expect "AgentShellPolicy type reintroduced outside pkg/config/config.go" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/config"
printf 'package config\n\ntype AgentShellPolicy struct {\n\tEnableDenyPatterns bool\n}\n' > "$TMP/tree/pkg/config/config.go"
expect "AgentShellPolicy in pkg/config/config.go (temporary-gap exclusion removed)" 1

fresh_tree
printf 'package gateway\n\nfunc f(body []byte) bool { return bytes.Contains(body, []byte(`"shell_policy"`)) }\n' > "$TMP/tree/pkg/fixture.go"
expect "\"shell_policy\" wire key reintroduced outside the retirement-enforcement sites" 1

# The three retirement-enforcement LINES are excluded, and nothing else in
# their files is.
fresh_tree
mkdir -p "$TMP/tree/pkg/gateway"
cat > "$TMP/tree/pkg/gateway/rest_agents_update.go" <<'EOF'
package gateway

func f(uf updateFields) bool {
	if bytes.Contains(uf.rawBody, []byte(`"shell_policy"`)) {
		jsonErr(uf.w, http.StatusBadRequest,
			`shell_policy is retired — use Auto-approve instead (ADR-092)`)
		return true
	}
	return false
}
EOF
cat > "$TMP/tree/pkg/gateway/agent_field_rules.go" <<'EOF'
package gateway

var subagent3pForbiddenUpdateFields = []string{
	"tools_cfg",
	"shell_policy",
}
EOF
expect "the stale-client 400 sniff + message lines and the forbidden-field entry (allowed, line-level)" 0

fresh_tree
mkdir -p "$TMP/tree/pkg/gateway"
printf 'package gateway\n\nfunc g(req Req) { _ = req.ShellPolicy }\n' > "$TMP/tree/pkg/gateway/rest_agents_update.go"
expect "req.ShellPolicy re-added elsewhere in rest_agents_update.go (no whole-file exclusion)" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/gateway"
printf 'package gateway\n\nvar k = map[string]bool{"shell_policy": true}\n' > "$TMP/tree/pkg/gateway/agent_field_rules.go"
expect "\"shell_policy\" used as a live key in agent_field_rules.go (only the bare list entry is excluded)" 1

# --- 6. exec allowlist ---------------------------------------------------------
fresh_tree
printf 'package tools\n\nfunc HandleExecAllowlist() {}\n' > "$TMP/tree/pkg/fixture.go"
expect "HandleExecAllowlist reintroduced" 1

fresh_tree
printf 'package policy\n\ntype ExecPolicy struct {\n\tAllowedBinaries []string\n}\n' > "$TMP/tree/pkg/fixture.go"
expect "AllowedBinaries reintroduced outside pkg/policy/" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/policy"
printf 'package policy\n\ntype ExecPolicy struct {\n\tAllowedBinaries []string\n}\n' > "$TMP/tree/pkg/policy/policy.go"
expect "AllowedBinaries in pkg/policy/ (gap closed — no longer excluded)" 1

# --- 7. dead exec-approval manager --------------------------------------------
fresh_tree
printf 'package security\n\ntype ExecApprovalManager struct{}\n' > "$TMP/tree/pkg/fixture.go"
expect "ExecApprovalManager reintroduced" 1

# --- 8. banned filenames --------------------------------------------------------
fresh_tree
printf 'package security\n' > "$TMP/tree/pkg/execapproval.go"
expect "execapproval.go file reintroduced" 1

fresh_tree
mkdir -p "$TMP/tree/contracts/components/schemas"
printf 'title: AgentShellPolicy\n' > "$TMP/tree/contracts/components/schemas/AgentShellPolicy.yaml"
expect "AgentShellPolicy.yaml schema file reintroduced" 1

# --- 9. unenforced approval keys, exec-allowlist route and key -------------------
fresh_tree
printf 'package x\n\nvar a = map[string]bool{"enable_deny_patterns": true}\n' > "$TMP/tree/pkg/fixture.go"
expect "enable_deny_patterns reintroduced" 1

fresh_tree
printf 'export const k = { exec_approval: "never" }\n' > "$TMP/tree/src/fixture.ts"
expect "exec_approval settings key reintroduced" 1

fresh_tree
printf 'package x\n\nconst t = "exec_approval_request"\nconst u = "exec_approval_expired"\n' > "$TMP/tree/pkg/fixture.go"
expect "exec_approval_request / exec_approval_expired WS frame names (different surface, allowed)" 0

fresh_tree
mkdir -p "$TMP/tree/contracts"
printf 'paths:\n  /security/exec-allowlist:\n    get: {}\n' > "$TMP/tree/contracts/openapi.yaml"
expect "/security/exec-allowlist route reintroduced" 1

fresh_tree
printf 'package x\n\nvar b = map[string][]string{"allowed_binaries": nil}\n' > "$TMP/tree/pkg/fixture.go"
expect "lower-case allowed_binaries key reintroduced" 1

fresh_tree
mkdir -p "$TMP/tree/pkg/gateway" "$TMP/tree/pkg/sysagent/tools"
printf 'package gateway\n\nfunc T() {\n\tbody := `{"shell_policy":{"enable_deny_patterns":true}}`\n\t_ = body\n}\n' > "$TMP/tree/pkg/gateway/rest_agents_update_test.go"
printf 'package systools\n\nvar cases = []c{\n\t{"widen the exec allow-list", "tools.exec.allowed_binaries", nil},\n}\n' > "$TMP/tree/pkg/sysagent/tools/config_privilege_test.go"
expect "the two test lines proving a retired key is refused (allowed, line-level)" 0

if [ "$FAIL" -ne 0 ]; then
  echo "selfcheck: check-no-shell-deny-patterns.sh does not behave — see above" >&2
  exit 1
fi
echo "selfcheck: OK (check-no-shell-deny-patterns.sh provably fails on offenders and provably passes its documented exclusions)"
exit 0

#!/usr/bin/env bash
# check-no-shell-deny-patterns.sh
#
# Regression guard for ADR-092: the hardcoded shell command-text block list,
# the per-binary exec allowlist, the dead "always allow" exec-approval
# manager, the per-agent ShellPolicy config surface, and the unenforced
# exec_approval / enable_deny_patterns settings keys are all DELETED —
# replaced by the Auto-approve setting (global, per-agent off-switch,
# per-chat toggle), the D3 resolved-binary command-rule engine, and D7/D8's
# kernel-backed pre-flight checks.
# This script fails the build if any of them comes back.
#
# WHY A SCRIPT AND NOT JUST A NOTE
#
# A note in CLAUDE.md tells a human. It does not stop `git merge`. Branches
# cut before ADR-092 still contain `defaultDenyPatterns`, the exec allowlist,
# and `ExecApprovalManager`; merging or rebasing any of them re-adds this
# code as an ordinary, conflict-free addition — git has no idea the deletion
# was deliberate. This repo's own CLAUDE.md documents the same failure mode
# for the JPEG screencast path and the Command Center / Schedules UI
# ("a merge can resurrect these files/surfaces — always resolve by keeping
# the deletion"). This is the mechanical half of that rule for ADR-092.
#
# WHAT WAS REMOVED AND WHY IT MUST NOT RETURN (ADR-092 "Removal inventory")
#
#   1. Block list (D2): `defaultDenyPatterns`/`secretGuardPatterns`/
#      `buildSecretGuardPatterns` (the shell.go block-list builder — NOT
#      pkg/fspolicy's identically-named secret-set builder, a different
#      mechanism the ADR explicitly keeps), `applyDenyPatterns`/
#      `compileDenyPatterns`/`denyPatternMessage`, `operatorDenyPatterns`,
#      `GlobalShellDenyPatterns`, `config.SandboxConfig.ShellDenyPatterns` /
#      wire key `shell_deny_patterns`, `validateShellDenyPatterns`. Bypassable
#      by construction (regexes over lowercased raw command text); neither
#      Claude Code nor Codex ships this, and the kernel sandbox is the real
#      boundary where one exists.
#   2. Dead "always allow" manager (D5): `ExecApprovalManager` /
#      `pkg/security/execapproval.go` — zero production callers, answered a
#      UI flow ADR-036 already deleted.
#   3. Exec allowlist, folded into D3: `HandleExecAllowlist`,
#      `sanitiseAllowlist`, `rest_exec.go`, `config.ExecConfig.
#      AllowedBinaries` — matched raw command strings with no
#      exec.LookPath/filepath.Abs/symlink resolution (issue #83's live
#      "Additional" claim); D3's resolved-binary rule engine replaces it.
#   4. Per-agent ShellPolicy surface: `config.AgentShellPolicy` (type deleted
#      whole — both its fields go, including `enable_deny_patterns`), the
#      `shell_policy` wire key on AgentCreateRequest/AgentUpdateRequest.
#   5. Unenforced approval settings keys (removal item 4): the
#      `exec_approval` / `enable_deny_patterns` settings keys the old
#      SecuritySection wrote and nothing read, the `/security/exec-allowlist`
#      REST route, and the `allowed_binaries` config/wire key of the exec
#      allowlist (item 3's lower-case JSON spelling, which the Go-identifier
#      rule for `AllowedBinaries` cannot see).
#
# WHAT IS ALLOWED
#
# - Comments (including this file's own text) that name the retired symbols
#   in prose, historical/retirement notes, and ADR/spec prose. Matched here
#   only as a live definition, call, or wire-key literal; comment-only lines
#   are dropped (Go `//`, block-comment `*`/`/*`, YAML/shell `#`).
# - `pkg/fspolicy/*` — that package's OWN secret-set builder is discussed in
#   comments under the same name (`buildSecretGuardPatterns`) the retired
#   shell.go block-list builder used; the ADR explicitly distinguishes them
#   (D2's "the secrets regex is a backstop over a boundary the kernel already
#   denies independently"). Only shell.go's block-list-producing definition
#   is banned; a same-named, differently-scoped function under pkg/fspolicy/
#   is not.
# - Retirement ENFORCEMENT, excluded LINE BY LINE (never a whole file, so a
#   retired field re-added anywhere else in the same file still fails):
#     * pkg/gateway/rest_agents_update.go — the stale-client 400 (ADR-092
#       removal item 1, "Stale-client PUT gets a hard 400"): exactly the
#       raw-body sniff line `bytes.Contains(uf.rawBody, []byte(`"shell_policy"`))`
#       and the line carrying the "shell_policy is retired" error message.
#     * pkg/gateway/agent_field_rules.go — exactly the bare `"shell_policy",`
#       entry of the subagent_3p forbidden-field list, which names the key so
#       it is REJECTED.
#     * pkg/gateway/rest_agents_update_test.go — exactly the test body line
#       that sends a retired `{"shell_policy":{"enable_deny_patterns":...}}`
#       to prove the 400 (the rest of that file is still excluded from the
#       ShellPolicy rule because its test names carry the word).
#     * pkg/sysagent/tools/config_privilege_test.go — exactly the line proving
#       `set_config` refuses `tools.exec.allowed_binaries`.
#     * src/components/agents/CreateAgentModal.test.tsx — the SPA test
#       asserting shell_policy is ABSENT from a create payload.
# - Generated code (pkg/api/generated/, src/lib/api/generated/) and
#   contracts/**/*.yaml + pkg/gateway/inboundschemas/*.yaml carry only
#   OpenAPI description prose about the removal, not a live schema — excluded
#   from the ShellPolicy text scan; the file-existence checks below still
#   catch a resurrected schema file.
# - Live WebSocket frame names that merely START with `exec_approval_`
#   (exec_approval_request / exec_approval_expired) are a different surface
#   and are not matched: the `exec_approval` rule requires the key to end
#   there.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-shell-deny-patterns: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd src contracts; do
  if [ ! -d "$d" ]; then
    echo "check-no-shell-deny-patterns: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# Format: <pattern>::<space-separated roots>::<extended-regex of paths to skip>
# The exclude regex matches against the full "path:line:content" grep output
# line, so a bare filename is enough to skip every hit in that file.
RULES=(
  # 1. Block-list core symbols — fully retired, zero legitimate non-comment
  #    reference anywhere in the tree today.
  'defaultDenyPatterns|secretGuardPatterns|applyDenyPatterns\(|compileDenyPatterns\(|denyPatternMessage\(|operatorDenyPatterns|GlobalShellDenyPatterns::pkg cmd src contracts::$^'
  # 2. buildSecretGuardPatterns — banned everywhere EXCEPT pkg/fspolicy/,
  #    whose own secret-set builder is a different mechanism under the same
  #    name (see "WHAT IS ALLOWED" above).
  'buildSecretGuardPatterns\(::pkg cmd src::pkg/fspolicy/'
  # 3. Config/wire field for the block list.
  'ShellDenyPatterns|validateShellDenyPatterns\(|"shell_deny_patterns"::pkg cmd src contracts::$^'
  # 4. Per-agent ShellPolicy config surface — AgentShellPolicy type/bare
  #    ShellPolicy identifier/"shell_policy" wire key. Excludes generated
  #    code, the test files named in the header, and exactly three
  #    retirement-enforcement LINES (see "WHAT IS ALLOWED").
  'AgentShellPolicy|ShellPolicy|"shell_policy"::pkg cmd src::^pkg/gateway/rest_agents_update\.go:[0-9]+:[[:space:]]*if bytes\.Contains\(uf\.rawBody, \[\]byte\(`"shell_policy"`\)\) \{$|^pkg/gateway/rest_agents_update\.go:[0-9]+:[[:space:]]*`shell_policy is retired |^pkg/gateway/agent_field_rules\.go:[0-9]+:[[:space:]]*"shell_policy",$|^pkg/gateway/rest_agents_update_test\.go:|^pkg/api/generated/|^src/lib/api/generated/|^src/components/agents/CreateAgentModal\.test\.tsx:'
  # 5. Exec allowlist — HandleExecAllowlist/sanitiseAllowlist/AllowedBinaries
  #    are all fully retired (the CLEANUP lane deleted pkg/policy/evaluator.go,
  #    pkg/policy/auditor.go, and their uses in pkg/tools/bash_test.go).
  'HandleExecAllowlist|sanitiseAllowlist\(|AllowedBinaries::pkg cmd src::$^'
  # 6. Dead exec-approval manager — fully retired.
  'ExecApprovalManager::pkg cmd src::$^'
  # 7. Unenforced approval settings keys and the exec-allowlist route/key
  #    (header item 5). `exec_approval` must END the key, so the live
  #    exec_approval_request / exec_approval_expired WS frame names never
  #    match. Excludes exactly two test LINES that name a retired key to
  #    prove it is refused.
  'enable_deny_patterns|exec_approval([^_[:alnum:]]|$)|/security/exec-allowlist|allowed_binaries::pkg cmd src contracts::^pkg/gateway/rest_agents_update_test\.go:[0-9]+:[[:space:]]*body := `\{"shell_policy":\{"enable_deny_patterns"|^pkg/sysagent/tools/config_privilege_test\.go:[0-9]+:.*"tools\.exec\.allowed_binaries"'
)

HITS=""
for rule in "${RULES[@]}"; do
  pattern="${rule%%::*}"
  rest="${rule#*::}"
  roots="${rest%%::*}"
  skip="${rest##*::}"
  # shellcheck disable=SC2086 — roots is an intentional space-separated list.
  found="$(grep -rInE "$pattern" \
    --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' --include='*.yml' \
    $roots 2>/dev/null | grep -vE "$skip" || true)"
  [ -n "$found" ] && HITS="${HITS}${found}"$'\n'
done

# Drop comment-only lines: a retirement comment naming a symbol in prose is
# the desired outcome, not a violation. Go/TS line comments, block-comment
# continuations, and YAML/shell `#` comments.
OFFENDERS="$(printf '%s\n' "$HITS" \
  | grep -v '^\s*$' \
  | awk -F: '{ line=""; for (i=3; i<=NF; i++) line = line (i>3 ? ":" : "") $i;
               sub(/^[ \t]+/, "", line);
               if (line ~ /^\/\// || line ~ /^\*/ || line ~ /^\/\*/ || line ~ /^#/) next;
               print }' \
  || true)"

# File-existence checks: names the ADR's removal inventory calls out
# specifically as deleted files, not just symbols. A grep-based rule can't
# catch "the file itself came back empty of the banned identifiers" (e.g. a
# stub re-added ahead of its contents), so these check presence directly.
BANNED_FILES=(
  'execapproval.go'
  'rest_exec.go'
  'AgentShellPolicy.yaml'
  'ExecAllowlist.yaml'
  'ShellDenyPatternsEditor.tsx'
  'ExecAllowlistSection.tsx'
)
FILE_HITS=""
for name in "${BANNED_FILES[@]}"; do
  found_files="$(find pkg cmd src contracts -type f -name "$name" 2>/dev/null || true)"
  [ -n "$found_files" ] && FILE_HITS="${FILE_HITS}${found_files}"$'\n'
done

if [ -n "$OFFENDERS" ] || [ -n "$FILE_HITS" ]; then
  echo "check-no-shell-deny-patterns: FOUND a retired ADR-092 shell-permission surface in code:" >&2
  echo "" >&2
  [ -n "$OFFENDERS" ] && printf '%s\n' "$OFFENDERS" >&2
  if [ -n "$FILE_HITS" ]; then
    echo "Banned filename(s) present:" >&2
    printf '%s\n' "$FILE_HITS" >&2
  fi
  echo "" >&2
  echo "The hardcoded shell command-text block list, the per-binary exec allowlist," >&2
  echo "the dead exec-approval manager, the per-agent ShellPolicy config surface, and the" >&2
  echo "unenforced exec_approval / enable_deny_patterns keys were deleted deliberately" >&2
  echo "(ADR-092 D2/D3/D5, removal inventory). The Auto-approve setting plus the D3" >&2
  echo "resolved-binary command rules and D7/D8's kernel-backed pre-flight checks" >&2
  echo "replace all of it." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "predates the removal and git re-added the code as an ordinary addition." >&2
  echo "RESOLVE BY KEEPING THE DELETION — do not 'fix the conflict' by restoring it." >&2
  echo "" >&2
  echo "If you genuinely need to reintroduce a command-text block list or an exec" >&2
  echo "allowlist, that reverses an accepted ADR: write the superseding ADR first, then" >&2
  echo "update this guard in the same commit." >&2
  exit 1
fi

echo "check-no-shell-deny-patterns: OK (shell block list, exec allowlist, exec-approval manager, per-agent ShellPolicy, and the unenforced approval keys absent per ADR-092)"
exit 0

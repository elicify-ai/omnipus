# Squad S report — issue #767

## Outcome

The bash safety guard now permits the five benign live-instance cases from issue #767 while retaining explicit blocks for destructive removal, secret expansion, dangerous command substitution in both shell syntaxes, and real Omnipus secret paths.

Code correct and tested: yes — the focused regression, related substitution/secret-guard tests, 27 safety guards, and budget gates pass.

Reachable by a user/agent: yes — the change is on the existing `bash` tool's unconditional `guardCommand` path; no new registration or UI is required.

## RED receipt — reproduced before product changes

Test: `TestBashSafetyGuard_PreciseDenyPatterns`

Command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestBashSafetyGuard_PreciseDenyPatterns$' -p 1 ./pkg/tools/
```

Result: exit 1. The failure named the real offending pattern for every live false-positive class:

```text
--- FAIL: TestBashSafetyGuard_PreciseDenyPatterns
benign_commands_are_allowed/backticked_template:
  text "`template`" matched deny pattern `[^`]+`
benign_commands_are_allowed/backticked_regex:
  text "`.*?`" matched deny pattern `[^`]+`
benign_commands_are_allowed/backticked_setting:
  text "`sandbox.god_mode = true`" matched deny pattern `[^`]+`
benign_commands_are_allowed/system_in_prose:
  text "System" matched deny pattern \bsystem\b
benign_commands_are_allowed/config_filename_in_prose:
  text "config.json" matched deny pattern \bconfig\.json\b
FAIL
```

The dangerous half was already green before the fix, providing the required security anchors rather than weakening assertions to obtain RED.

## Root cause

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell.go::defaultDenyPatterns` contained `` `[^`]+` ``, an unanchored match for every non-empty backtick pair. It could not distinguish Markdown-like prose from shell command substitution.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell.go::buildSecretGuardPatterns` generated a context-free word-boundary pattern for every always-secret entry. That turned `config.json` and the ordinary word `system` into bans on mere mention, despite those entries representing protected paths.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell_subst_guard.go::substitutionGuard` already had the correct structural security model for `$(...)`—command position, dangerous inner commands, and hostile outer commands—but legacy backticks were excluded from that model and blanket-blocked earlier.

## Fix

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell_subst_guard.go::extractCommandSubstitutions` now extracts paired and unterminated legacy backticks as well as `$(...)`. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell_subst_guard.go::substitutionGuard` applies the existing R1/R2/R3 structural checks to both syntaxes.
- The blanket backtick regex was removed from `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell.go::defaultDenyPatterns`. This deletes the regex, not the guard: dangerous backticks remain blocked by the structural replacement. Removal is justified because no regex anchoring can reliably separate prose delimiters from actual shell substitution; the existing structural guard can.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell.go::buildSecretGuardPatterns` keeps one generated pattern per always-secret entry, but narrows `config.json` to a slash-prefixed path component and `system` to a directory component followed by a slash. All other secret entries retain their existing context-free matches.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/s-bash-guard/pkg/tools/shell_guard_test.go::TestBashSafetyGuard_PreciseDenyPatterns` is the required two-half production-path table. Existing coupling tests were updated to assert the approved path-shaped contract, and the old harmless ``echo `date` `` assertion was moved from the dangerous table to the benign table with issue provenance.

No wire/API format changed, so contract regeneration was not applicable.

## GREEN receipts

Focused verbose proof after restoring the fix:

```text
--- PASS: TestBashSafetyGuard_PreciseDenyPatterns
    --- PASS: .../benign_commands_are_allowed
        --- PASS: .../backticked_template
        --- PASS: .../backticked_regex
        --- PASS: .../backticked_setting
        --- PASS: .../system_in_prose
        --- PASS: .../config_filename_in_prose
    --- PASS: .../dangerous_commands_stay_blocked
        --- PASS: .../recursive_forced_remove
        --- PASS: .../secret_parameter_expansion
        --- PASS: .../dangerous_dollar_substitution
        --- PASS: .../dangerous_backtick_substitution
        --- PASS: .../omnipus_config_path
        --- PASS: .../omnipus_system_path
PASS
ok github.com/elicify-ai/omnipus/pkg/tools
exit=0
```

Related scoped selection:

```text
CGO_ENABLED=0 go test -v -tags goolm,stdjson \
  -run '^(TestBashSafetyGuard_PreciseDenyPatterns|TestSecretGuardPatterns_.*|TestBashSubstitutionGuard_.*)$' \
  -p 1 ./pkg/tools/
220 named runs; 220 PASS lines
exit=0
```

Repository gates:

```text
make lint-guards
guards run: 27
GUARD RUNNER: all 27 guards passed
exit=0

make lint-budgets
exit=0
```

The budget gate printed existing grandfathered warnings but no failure.

## Revert-proof receipt

I temporarily reverted only the production fix while keeping `TestBashSafetyGuard_PreciseDenyPatterns`, then ran the focused test verbosely.

```text
--- FAIL: TestBashSafetyGuard_PreciseDenyPatterns
    --- FAIL: .../benign_commands_are_allowed
        --- FAIL: .../backticked_template
        --- FAIL: .../backticked_regex
        --- FAIL: .../backticked_setting
        --- FAIL: .../system_in_prose
        --- FAIL: .../config_filename_in_prose
    --- PASS: .../dangerous_commands_stay_blocked
        --- PASS: .../recursive_forced_remove
        --- PASS: .../secret_parameter_expansion
        --- PASS: .../dangerous_dollar_substitution
        --- PASS: .../dangerous_backtick_substitution
        --- PASS: .../omnipus_config_path
        --- PASS: .../omnipus_system_path
FAIL
exit=1
```

After restoring the fix, the same named test produced the focused GREEN receipt above with exit 0. This proves the benign assertions die on reversion while the dangerous anchors independently remain enforced.

## Honest gaps — per narrowed pattern

| Narrowed pattern | Newly allowed class | Why acceptable | Conservative remainder / risk boundary |
|---|---|---|---|
| `` `[^`]+` `` | Backtick-delimited prose and structurally benign legacy substitutions such as ``echo `date` ``. | Backticks now receive the same command-position, dangerous-inner-command, and hostile-host checks as `$(...)`; dangerous ``echo `find ...` `` remains blocked by test. | The scanner deliberately remains fail-closed about quoting. A dangerous command word inside backticks used only as prose can still false-positive. This change does not claim to be a complete shell parser; the kernel/path/egress layers remain the security boundary. |
| `\bconfig\.json\b` | A bare `config.json` mention, including a relative `cat config.json` from an ordinary agent workspace. | In a normal agent workspace, a bare filename refers to that workspace's project file, not `$OMNIPUS_HOME/config.json`. An explicit protected reference such as `~/.omnipus/config.json` still matches and is proven blocked. | `./config.json` still matches because it is path-shaped, so some project-file false positives remain. If a caller were unusually started with `$OMNIPUS_HOME` itself as its working directory, a bare relative secret name would rely on the platform confinement rather than this text backstop; normal agent workdirs are not rooted there. |
| `\bsystem\b` | The ordinary word `system` and a bare relative entry named `system`. | The protected object is the `$OMNIPUS_HOME/system/` directory. A traversal such as `~/.omnipus/system/audit.jsonl` still matches and is proven blocked. | Any unrelated project path containing `system/` still matches, so the narrowing is deliberately modest. As above, an abnormal working directory already inside `$OMNIPUS_HOME` would rely on platform confinement for a bare relative entry. |

No other deny pattern was narrowed or deleted.

## Change-scope receipt

GitNexus `detect-changes --scope unstaged` reported five code/test files, 16 changed symbols, zero affected indexed processes, and LOW aggregate risk. The earlier pre-edit impact check correctly reported CRITICAL reach for `buildSecretGuardPatterns` because it initializes the guard used by every bash invocation; that is why the production-path, dangerous-anchor, revert, and repository-guard proofs above were all required.

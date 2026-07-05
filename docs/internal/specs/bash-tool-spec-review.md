# Adversarial Review: `bash` — Unified Shell Execution Tool

**Spec reviewed**: `docs/internal/specs/bash-tool-spec.md`
**Review date**: 2026-07-04
**Verdict**: BLOCK

## Executive Summary

This spec's central premise — that `bash` merely needs to "match every other builtin tool's deny-by-default convention" for fresh custom agents — is contradicted by the actual compositor code and its own test suite: today, an unlisted `ScopeCore` tool (which `exec` is) resolves to **allow**, not deny, for a newly created custom agent. Implementing FR-B8/User-Story-1-AC3 exactly as specified, on the belief that it's already-true behavior needing no new work, will ship `bash` allow-by-default — reproducing the exact class of fail-open bug this whole consolidation exists to close. Combined with an unaddressed CRITICAL-risk migration path with no rollback plan and a symlink-based workspace-escape gap in the `cwd` guard, this spec is not ready for implementation.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 6 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **15** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] "Deny-by-default for new custom agents" is not an existing convention — it's unbuilt, unstaffed new scope

- **Lens**: Incorrectness
- **Affected section**: User Story 1, Acceptance Scenario 3; FR-B8
- **Description**: The spec states plainly: *"Given a fresh custom agent with no explicit `bash` policy... it is denied by default, matching every other builtin tool's deny-by-default convention."* This is false as a description of current behavior. Verified directly against the codebase (`/home/dev/omnipus2`, commit `59e093e7`):
  - `pkg/tools/compositor.go`'s `effectiveToolPolicyWith` explicitly *defers* `ScopeCore`-on-custom-agent to the global×agent merge rather than hard-denying it (`if !passesScopeGate(scope, agentType) && scope != ScopeCore { return "deny" }` — note the `scope != ScopeCore` exclusion).
  - `pkg/sysagent/tools/agent.go:288-290` seeds exactly one wildcard deny for new custom agents: `system.*`. There is no `exec`-specific (or blanket `ScopeCore`) seed.
  - `ExecTool.Scope()` (`pkg/tools/shell.go:449`) returns `ScopeCore`, and `pkg/tools/shell.go`'s own comment at the `executeRun` call site reads: *"exec is now governed purely by per-agent ToolPolicyCfg. Agents that should not use exec on remote channels must have `exec: deny` in their tool policy."* — i.e., no built-in deny floor exists for `exec` beyond an operator's explicit entry.
  - The compositor's own test suite (`pkg/tools/effective_tool_policy_test.go`) encodes this directly: under an `allowDefaultCfg()` (the config shape new custom agents actually get, generated-schema-documented as *"Custom agents are seeded with default_policy=allow and a system.*=deny entry"*), an unlisted `ScopeCore` tool resolves to `"allow"`. Only under an explicit `denyDefaultCfg()` does an unlisted `ScopeCore` tool resolve to `"deny"` — and nothing seeds `DefaultPolicy: "deny"` for new custom agents today.
- **Impact**: An implementer who trusts the spec's framing ("matching... convention") and does not add new seeding logic will ship `bash` reachable-by-default on every freshly created custom agent — the identical fail-open shape as the `exec`/`workspace_shell` inconsistency this spec exists to close, just relocated. `TestBash_NewCustomAgentDeniedByDefault` (Test Order #7) will either be written to pass against behavior that doesn't otherwise exist in production, or will be skipped/soft-failed because "it already works."
- **Recommendation**: Add an explicit functional requirement: the system MUST seed `bash: deny` (or an equivalent mechanism) into every newly created custom agent's `AgentBuiltinToolsCfg.Policies`, analogous to today's `system.*: deny` seed in `pkg/sysagent/tools/agent.go`. Name the exact file/function this seeding must be added to (mirroring the Symbols Involved table's precision everywhere else in this spec). Do not describe this as "matching an existing convention" — it is new, security-relevant scope that must be its own explicit FR with its own test, not folded silently into FR-B8's phrasing as if no new code is needed.

---

#### [CRIT-002] CRITICAL-risk migration has no rollback plan, feature flag, or staged rollout

- **Lens**: Inoperability
- **Affected section**: Impact Assessment table ("Persisted `Policies`/`ToolPolicies` maps (migration) | CRITICAL"); FR-M1–FR-M3
- **Description**: The spec's own Impact Assessment table rates the tool-policy migration as **CRITICAL** risk with the note *"Compositor's default-`allow` fallback (the fail-open risk this migration exists to close)"* — i.e., the author already knows a migration bug here silently converts operator `deny` entries into `allow`. FR-M3 additionally deletes the legacy keys on the same boot ("no permanent dual-key backward compatibility"), meaning there is no way to inspect or recover the pre-migration state after the first post-upgrade boot completes. Nothing in the spec specifies: a feature flag to gate the new migration function separately from the rest of the `bash` rollout; a dry-run/audit mode; a backup of the pre-migration config; or any operator-facing rollback procedure if the migration is later found to have mis-resolved a conflicting-key case in production.
- **Impact**: If the strictest-wins logic (FR-M1) or the deletion step (FR-M3) has a bug that only manifests on a real operator's config shape not covered by the test dataset (e.g., a key with unexpected casing, a policy value spelled inconsistently, or a config that was hand-edited), the operator's original intent is unrecoverable from the running system — the legacy keys are gone, and the only rollback is restoring a pre-upgrade backup of `config.json` (which the spec never mentions and operators are not told to take).
- **Recommendation**: Add a functional requirement that the migration writes a timestamped backup of the pre-migration policy maps (e.g., `config.json.pre-bash-migration.bak`) before deleting legacy keys, and that this is mentioned in the boot log line FR-M1 already requires. Alternatively/additionally, specify that the migration is *additive-only for one release* (keep the legacy key present-but-unread, matching the "additive-then-cleanup" language ADR-036 §3.6 uses for the pattern before conceding to same-boot deletion) so operators have one upgrade cycle to notice a problem before the legacy key is gone.

---

### MAJOR Findings

#### [MAJ-001] `cwd` guard is defeated by a symlink pointing outside the workspace

- **Lens**: Insecurity (STRIDE: Elevation of Privilege)
- **Affected section**: User Story 3; FR-B2; Dataset: `cwd` validation
- **Description**: FR-B2 requires rejecting `cwd` containing `..` or an absolute path, and the resolved Clarification states the guard "checks only the final, fully-resolved (`filepath.Clean`ed) path." `filepath.Clean` is a purely lexical/string operation — it does not resolve symlinks. If a workspace directory contains a symlink (created by an earlier `bash` call, e.g. `ln -s /etc evil_link`), a subsequent call with `cwd: "evil_link"` is a syntactically valid relative path with no `..` and no leading `/`, passes `filepath.Clean`-based validation, and resolves (via the OS, at process-spawn time) to a directory entirely outside the workspace. This is a real, demonstrated class of path-traversal bypass (CWE-59) that the spec's own dataset (six rows, all pure string patterns — no symlink case) does not cover.
- **Impact**: An agent with `bash: allow` (the norm, given CRIT-001) can escape the workspace root via a two-call sequence: create a symlink, then `cd` into it — defeating the entire security premise of User Story 3 ("`cwd` cannot be used to escape the agent's workspace").
- **Recommendation**: Add an acceptance scenario and dataset row: *"Given a workspace containing a symlink to a path outside the workspace, when `bash` is called with `cwd` set to that symlink's relative name, then the call is rejected."* Specify the guard MUST resolve the final path with symlinks followed (`filepath.EvalSymlinks` or equivalent) before the inside/outside-workspace check, not merely `filepath.Clean`. Add `TestBash_CwdRejectsSymlinkEscape` to the Test Implementation Order.

---

#### [MAJ-002] Companion specs were drafted against a working tree two commits behind the named branch target — the dependency they build on may not exist at implementation time

- **Lens**: Incorrectness / Inoperability
- **Affected section**: Integration Boundaries → `pkg/gateway/ws_approval.go`; shared with `agent-delegation-spec.md`'s "Verified Behavior" section
- **Description**: This spec's Integration Boundaries section treats `ws_approval.go`'s grant-store consultation as already-shipped, unchanged infrastructure. Verified: commit `d0f65482` (which introduces the entire `ApprovalGrantStore`/session-scoped-grant mechanism this spec's `bash` "ask" flow relies on for parity with the delegation spec) is present on `origin/hotfix/v0.1.1` (the branch `tool-consolidation-spec.md` names as the target) but is **two commits ahead of the local working tree these three spec files were drafted in** (verified via `git merge-base`/`git log origin/hotfix/v0.1.1..HEAD`). `pkg/security/approvalgrants.go` does not exist in the local checkout at all.
- **Impact**: If an implementer creates a feature branch from the local checkout (rather than fetching and resetting to the actual tip of `origin/hotfix/v0.1.1`), the approval-grant infrastructure this spec's approval-flow consolidation (FR-A1, shared with `tool-consolidation-spec.md` §5) depends on will not exist, and nothing in this spec flags that as a precondition to verify. This project's own history records this exact failure mode (branching off a stale local base instead of the named remote tip) as a repeat problem.
- **Recommendation**: Add an explicit precondition to the "Input" header of all three specs: *"Implementation MUST branch from a checkout that includes commit `d0f65482` (verify via `git log --oneline | grep d0f65482` before starting) — this local working tree does not yet include it."*

---

#### [MAJ-003] No monitoring/metrics for the new cancel-cascade kill path

- **Lens**: Inoperability
- **Affected section**: User Story 5; FR-B10/FR-B11
- **Description**: `RequestCancel`'s existing cascade (turn cancellation, approval auto-deny) has established logging/audit conventions this spec extends into a new "PHASE A" hook (`KillBackgroundSessions`). The codebase already has a metrics-recorder convention in active use for exactly this class of decision (`pkg/tools/compositor.go`'s `IncFilterTotal`/`IncCollisionTotal`, wired at gateway boot). Nothing in FR-B10/FR-B11 or the BDD scenarios requires emitting a metric or structured log line recording how many background sessions were killed by a given cancel, which session(s), or how long the kill took.
- **Impact**: An operator who explicitly cancels a session with a long-running background build has no way to confirm — via logs, metrics, or any observable signal other than a subsequent `poll` call the operator may not think to make — that the kill cascade actually fired. A silent no-op bug in `KillAllForSession` (e.g., a filter predicate that never matches due to an `OwnerSessionID` wiring mistake) would be invisible in production until an operator notices a build still consuming CPU minutes after "cancel."
- **Recommendation**: Add a requirement that `SessionManager.KillAllForSession` logs one INFO line per session killed (session ID, PID, elapsed runtime) and increments an existing or new counter, and that `TestBash_CancelCascade_KillsOwnedBackgroundSessions` asserts on that log/metric emission, not only on the subsequent `poll` status.

---

#### [MAJ-004] `timeout_seconds` values above the stated max (3600) have unspecified behavior

- **Lens**: Ambiguity
- **Affected section**: FR-B1
- **Description**: FR-B1 states `timeout_seconds` is "optional, default 300, max 3600" but no acceptance scenario, BDD scenario, or dataset row specifies what happens when a caller passes a value greater than 3600 (or negative, or zero). Is it clamped to 3600? Rejected as a validation error (matching the `persistent` field's explicit reject-as-invalid convention in the same FR list)? Silently accepted and enforced as-is (defeating the stated "max")?
- **Impact**: Two implementers could build genuinely different behavior here — one clamping, one rejecting, one ignoring the cap entirely — with no test catching the divergence, since none exists.
- **Recommendation**: Add an explicit acceptance scenario: *"Given `timeout_seconds: 7200` (above the max), when `bash` is called, then the call is [rejected with a validation error | clamped to 3600] — pick one"* and a corresponding dataset row and unit test.

---

#### [MAJ-005] Migration dataset never covers a malformed/unrecognized legacy policy value

- **Lens**: Incompleteness
- **Affected section**: Dataset: migration key combinations; FR-M1
- **Description**: All eight dataset rows use well-formed `deny`/`ask`/`allow` values. Real, hand-edited or corrupted `config.json` files can contain an unrecognized string (`"Deny"` with wrong casing, `""`, `"disabled"`, a boolean, `null`) in a legacy `exec`/`workspace_shell`/`workspace_shell_bg` key. FR-M1's "strictest wins" rule (`deny > ask > allow`) has no defined behavior for a value that is none of the three.
- **Impact**: An implementer's strictest-wins comparison (likely an ordinal lookup table) will either panic, silently treat the unrecognized value as the loosest option (`allow` — the exact fail-open direction this whole spec exists to prevent), or produce an undefined migration result depending on map iteration order.
- **Recommendation**: Add a dataset row and acceptance criterion: an unrecognized legacy value MUST be treated as `deny` (fail-safe) with a WARN log line naming the offending key/value, not silently coerced to `allow` or panicking.

---

#### [MAJ-006] `SC-004`'s "byte-identical" idempotency claim is stricter than anything else in the spec requires, and untested against Go map-ordering nondeterminism

- **Lens**: Infeasibility
- **Affected section**: SC-004
- **Description**: SC-004 requires "running the full migration twice against the same config file produces byte-identical output on both runs." Go's `encoding/json` does marshal map keys in sorted order by default, so this is *achievable*, but only if the migration's write path uses the standard marshaler consistently and no other field (e.g., a timestamp, a "last migrated at" marker) is introduced anywhere in the migrated config structure. Nothing in FR-M1–M3 rules out a future "migrated at" audit field being added later, which would silently break SC-004 without any of this spec's own tests catching it, since idempotency here specifically means *byte-identical*, not merely *behaviorally equivalent*.
- **Impact**: Low-probability but a real trap for a future maintainer who innocently adds a migration timestamp for debugging and breaks a success criterion that was written for a stricter guarantee than any consumer actually needs (no consumer of `config.json` is shown to depend on byte-for-byte reproducibility — only on the resolved policy value).
- **Recommendation**: Relax SC-004 to "produces a config with identical resolved policy values on both runs, verified by structural comparison (not byte comparison)" unless there is a specific reason (e.g., a config-diffing tool elsewhere in the product) that requires literal byte identity.

---

### MINOR Findings

#### [MIN-001] "Every known way to run a shell command" (Independent Test, User Story 1) is not enumerated

- **Lens**: Ambiguity
- **Affected section**: User Story 1, "Independent Test"
- **Description**: "attempt every known way to run a shell command (direct call, background mode)" leaves "every known way" undefined beyond the two examples given. The Evaluation Scenario "Operator with an explicitly-denied agent tries every trick they can think of" (holdout) is more thorough in spirit but is explicitly not part of the required TDD plan.
- **Recommendation**: Enumerate the specific attack surface this closes (e.g., `run_in_background`, `persistent`, `action: poll/read/kill` against a session someone else started, PTY flag if somehow still reachable) as explicit dataset rows in the main TDD plan, not only the holdout section.

---

#### [MIN-002] Race window between `poll` and `kill`/process-natural-exit is unaddressed

- **Lens**: Incorrectness (race conditions)
- **Affected section**: Edge Cases; FR-B1 (`action: poll/read/kill`)
- **Description**: If a background process exits naturally in the instant between an agent's `poll` call returning "running" and a subsequent `kill` call, what does `kill` report — "already completed," "killed," an error? Not specified. `pkg/tools/session.go`'s `IsDone()` check exists but the spec's action semantics for this race aren't stated.
- **Recommendation**: Add an edge case and dataset row: "kill on an already-exited session returns [its actual final status], not an error and not a false 'killed.'"

---

#### [MIN-003] `send-keys`/PTY-adjacent session actions in existing `exec` are silently dropped without a migration note for in-flight sessions

- **Lens**: Incompleteness
- **Affected section**: Explicit Non-Behaviors (PTY dropped); FR-B1's `action` enum (`run|poll|read|kill` — no `send-keys`)
- **Description**: Today's `ExecTool` supports a `send-keys` action (confirmed in code: `case "send-keys": return t.executeSendKeys(args)`). The spec explicitly drops PTY and doesn't mention `send-keys` in the new `action` enum, which is consistent with dropping PTY (send-keys presumably only makes sense against a PTY session) — but nothing states what happens to a session created under the *old* tool that used `send-keys`, if the upgrade happens while such a session is live (e.g., mid-upgrade restart). This is a narrow edge case but the spec is otherwise careful about upgrade-time transitions (FR-M1–M3) and silent on this one.
- **Recommendation**: State explicitly that no in-flight PTY/send-keys sessions are expected to survive a binary upgrade (matching the "gateway restart" edge case already accepted as out-of-scope in the companion `async-notifier-spec.md`), for consistency.

---

#### [MIN-004] Dataset row 6 (`"subdir/../subdir"` → accepted) is a good positive control but has no adjacent negative control at the same complexity

- **Lens**: Incompleteness (test coverage)
- **Affected section**: Dataset: `cwd` validation, row 6
- **Description**: Row 6 proves a traversal-that-resolves-inward is accepted. There's no equivalent row proving a traversal-that-resolves-outward-then-back-inward-via-symlink or a deeply nested `"a/../../a"` (net one level up, still outside root if `a` is at workspace root) is still rejected — the existing rows 3/4 cover simple one-hop traversal only.
- **Recommendation**: Add a dataset row for `"a/../../b"` where the net effect is one level above workspace root, confirming the *final resolved path* check (not "contains `..`") correctly still rejects this class, not just the simple case.

---

### Observations

#### [OBS-001] `FR-B6`'s "resolved once per turn" god-mode check isn't tied to a cache-invalidation scenario

- **Lens**: Incompleteness
- **Suggestion**: If `godMode` can be toggled mid-turn (e.g., an operator flips a config setting while a long agent turn is executing multiple `bash` calls), "resolved once per turn" implies calls within the same turn use a stale value. Worth an explicit note confirming this is accepted behavior (likely is, given "resolved once per turn... exactly as workspace_shell/workspace_shell_bg already do" is presented as existing, accepted convention) rather than an oversight.

---

#### [OBS-002] The deny-pattern dataset's fork-bomb pattern is POSIX-shell-specific

- **Lens**: Incompleteness
- **Suggestion**: `:(){ :|:& };:` is a bash/POSIX-shell fork bomb; Windows has no equivalent shell syntax match. Since `bash` is stated to be "Cross-platform," worth a note (or an explicit non-behavior) on whether Windows-specific dangerous-command patterns (e.g., PowerShell fork-bomb equivalents) are in scope for a future pattern-list update, or explicitly out of scope for this spec.

---

#### [OBS-003] `ExecAllowlistSection.tsx`/`ExecProxyStatusCard.tsx` retained-but-repointed frontend components aren't given their own acceptance scenario

- **Lens**: Incompleteness
- **Suggestion**: ADR-036 §6 lists these as "retained, re-pointed at `bash`" but this spec has no BDD scenario or test confirming the re-pointing actually happened (e.g., a snapshot/label test asserting the UI no longer displays "exec" anywhere). Worth one lightweight frontend test given Constraint #8's cross-boundary rigor applies to this repo generally.

---

## Structural Integrity (Variant A: Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1 through US-5 each have 2-4 scenarios |
| Every acceptance scenario has BDD scenarios | PASS | |
| Every BDD scenario has `Traces to:` reference | PASS | |
| Every BDD scenario has a test in TDD plan | PASS | 28 tests enumerated, ordered |
| Every FR appears in traceability matrix | PASS | All 14 FRs (FR-B1–B11, FR-M1–M3) present |
| Every BDD scenario in traceability matrix | PARTIAL | FR-B5/FR-B6/FR-B7's "User Story" column names a section, not a story — self-flagged by the author as "cross-cutting, no dedicated scenario"; acceptable but slightly weakens traceability rigor |
| Test datasets cover boundaries/edges/errors | PARTIAL | See MAJ-001, MAJ-005, MIN-002, MIN-004 — symlinks, malformed migration values, race windows, and deep traversal are absent |
| Regression impact addressed | PASS | Dedicated table present, five existing behaviors mapped |
| Success criteria are measurable | PARTIAL | SC-004's "byte-identical" is measurable but arguably over-strict — see MAJ-006 |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Symlink-based path escape | No test for `cwd` resolving through a symlink to outside the workspace | User Story 3 |
| Malformed migration input | No test for a legacy policy value that isn't `deny`/`ask`/`allow` | User Story 4 |
| Timeout upper-bound | No test for `timeout_seconds` above the stated max | Behavioral Contract |
| Kill/exit race | No test for `kill` racing a natural process exit | User Story 2 |
| Fresh-custom-agent default (real, not assumed) | `TestBash_NewCustomAgentDeniedByDefault` needs a real seeding mechanism to test against — see CRIT-001 | User Story 1 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| `cwd` validation | Symlink escape, deep-nested net-outside traversal | Add rows per MAJ-001, MIN-004 |
| migration key combinations | Malformed/unrecognized value | Add row per MAJ-005 |
| (none exists) | `timeout_seconds` boundary (0, negative, > max) | Add new dataset per MAJ-004 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `bash` tool call path | ok | ok | ok | ok | risk | **risk** | Elevation via symlink-based `cwd` escape (MAJ-001); no rate limit / resource-exhaustion bound stated for concurrent background sessions per agent (unbounded `run_in_background` calls could exhaust process/FD limits — not addressed anywhere in the spec) |
| Migration pipeline | ok | risk | ok | ok | ok | ok | Tampering risk: a hand-edited config with a malformed legacy value has undefined resolution (MAJ-005); no repudiation concern (logged) |
| `SessionManager.KillAllForSession` | ok | ok | risk | ok | ok | ok | No audit/metric trail proving the kill fired (MAJ-003) — a silent no-op is unobservable |
| Deny-pattern baseline | ok | ok | ok | ok | ok | ok | Well-specified, floor cannot be disabled by config — good |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. What is the resource limit (max concurrent background sessions per agent, per workspace, globally) for `run_in_background`? Nothing bounds this — is unlimited background-session creation an accepted risk, or an oversight?
2. Does `bash: deny` also block an agent from seeing `bash` in its tool manifest at all, or only from executing it (mirroring the `load_tool`/manifest-visibility-vs-execution-gate distinction this codebase already has elsewhere per `ManifestInfra`)? The spec doesn't distinguish these two levels.
3. If the migration (FR-M1) runs but the subsequent config write (FR-M3's deletion step) fails partway (e.g., disk full), is the in-memory session left with the new `bash` policy while the persisted file still has the stale key, creating a state that will re-migrate (or double-migrate) on next boot? What guarantees atomicity here?
4. Who verifies, before FR-A1's approval-flow deletion, that `ToolApprovalModal` actually carries a live command preview equivalent to what `ExecApprovalBlock` showed? The spec says this "must be ported... before the dedicated flow is deleted" but assigns no owner, test, or acceptance scenario to that verification step — it's a prose caveat, not a requirement with a check.
5. For User Story 5's cancel cascade — does killing a background bash session also need to interact with `DevServerRegistry`, given `web_serve`'s dev-server mode uses the same registry and `bash`'s background mode is explicitly NOT this registry (per the Non-Behaviors)? Confirm these are genuinely disjoint session-tracking systems so a cancel doesn't need to reach into `DevServerRegistry` too.

---

## Verdict Rationale

CRIT-001 alone is grounds for BLOCK: it is a verified, concrete factual error about the codebase's current security posture, embedded directly in a P0 acceptance scenario and functional requirement, that — if implemented as literally written — reintroduces the exact fail-open pattern (a tool reachable without explicit operator opt-in) this entire ADR/spec cluster exists to eliminate. CRIT-002 compounds this: a CRITICAL-risk migration with no rollback path is exactly the kind of change Constraint #7's "fix everything, no excuses" release culture should refuse to ship without a recovery story. MAJ-001's symlink escape is a second, independent way the spec's own P0 security guarantee (workspace-escape prevention) fails as written.

None of these require re-architecting the spec — CRIT-001 needs one new explicit seeding requirement (with its own FR and test), CRIT-002 needs a backup step before deletion, MAJ-001 needs `EvalSymlinks` in the path guard. This is a REVISE-sized fix, but the security-relevant nature of the findings (a fresh custom agent unexpectedly getting shell access, and a workspace-escape bypass) is what pushes the verdict to BLOCK rather than REVISE — implementation must not proceed on the current text.

### Recommended Next Actions

- [ ] Add an explicit new-custom-agent `bash: deny` seeding requirement + test (CRIT-001)
- [ ] Add a pre-migration backup step to FR-M1/M3 (CRIT-002)
- [ ] Add `filepath.EvalSymlinks`-based resolution to the `cwd` guard + test (MAJ-001)
- [ ] Add the "must include `d0f65482`" precondition to the spec header (MAJ-002)
- [ ] Add kill-cascade logging/metrics (MAJ-003)
- [ ] Specify `timeout_seconds` over-max behavior (MAJ-004)
- [ ] Add malformed-legacy-value migration handling (MAJ-005)
- [ ] Relax or justify SC-004's byte-identical claim (MAJ-006)

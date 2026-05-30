# Adversarial Review: Central Tool Registry & Per-Agent Policy Filter

**Spec reviewed**: `docs/internal/specs/tool-registry-redesign-spec.md` (revision 5)
**Review date**: 2026-04-27
**Verdict**: REVISE

## Executive Summary

This is the third grill on a heavily revised spec (rev 5; H-01..H-22 already
addressed). The structural skeleton is mostly sound and the high-impact safety
properties (FR-061 admin-ask fence, FR-066 dedup invariant, FR-069 SIGKILL
recovery) are now present. However the revisions have introduced **direct
self-contradictions** about default values and MCP capabilities that will
silently produce divergent implementations, **the 78-FR scope has outpaced
the 38-BDD section** — at least seven revision-4/5 FRs reference "new BDD"
that was never actually written — and several **operational/security details
remain undefined** (args_hash algorithm, session_state event schema, mid-turn
TOCTOU on policy mutation, fence visibility in REST). The spec cannot ship
to implementation as-is because two implementers reading it would build two
different systems on the contradictions alone.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 9 |
| MINOR | 7 |
| OBSERVATION | 5 |
| **Total** | **24** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] FR-059 and FR-064 directly contradict each other on MCP `RequiresAdminAsk` capability

- **Lens**: Inconsistency
- **Affected section**: FR-059 (line ~1071) vs FR-064 (line ~1079)
- **Description**: FR-059 states: *"MCP tools cannot opt in (their adapter
  hardcodes `false`)."* FR-064 states: *"MCP server config (`mcp.servers.<name>`)
  MAY declare a per-tool `requires_admin_ask: [tool_a, tool_b]` array. The MCP
  adapter inspects this list at registration; tools listed there have their
  `RequiresAdminAsk()` return `true`."* These are mutually exclusive. The
  adapter cannot both hardcode `false` and honour an opt-in list. Phase A1
  (backend-lead) and Phase A2 (security-lead) will read different sentences
  and implement different things; the test `TestRegistry_AllSysagentToolsRequireAdminAsk`
  exercises only sysagent tools, so the contradiction will not surface in CI.
- **Impact**: Either (a) operators who configure `requires_admin_ask` on an
  MCP server discover at runtime that it silently does nothing — privileged
  MCP tools execute without the admin gate they configured for — or (b) the
  intended FR-059 invariant ("MCP cannot inherit admin protection by
  registering a system-prefixed name") is broken because some MCP tools now
  return `true`. Either way the security model documented in revision 4 (G-06)
  is undermined.
- **Recommendation**: Pick one. If MCP opt-in is desired, delete the "MCP
  tools cannot opt in" sentence in FR-059 and rewrite FR-015 to clarify that
  MCP servers can opt into admin-required, but cannot register `system.`-
  prefixed names (FR-060 covers the latter). If MCP opt-in is *not* desired,
  delete FR-064 wholesale and remove the `mcp.servers.<name>.requires_admin_ask`
  config key reference. Add a single explicit sentence to FR-059: "Only
  pkg/sysagent/tools/* MAY return true; MCP adapter always returns false."

---

#### [CRIT-002] Default value of `gateway.tool_approval_max_pending` stated three different ways

- **Lens**: Inconsistency
- **Affected section**: FR-016, FR-078, SC-006, Ambiguity Warnings Q2
- **Description**: Four different statements about the default:
  - FR-016 (line ~1022): *"default value is **64**"*; *"sentinel `0` means
    unlimited"*.
  - FR-078 (line ~1093): *"default `gateway.tool_approval_max_pending` is
    **64** for all variants until Cloud bring-up"*.
  - SC-006 (line ~1104): *"Default `gateway.tool_approval_max_pending` is
    **64** (open-source / desktop variants) and **256** (Cloud variant)"*.
  - Ambiguity Q2 (line ~1302): *"`gateway.tool_approval_max_pending` default
    | **Unlimited (no cap)**. Operator must set explicitly for multi-tenant."*
  Q2 is supposed to be the "locked-in" value but it directly contradicts
  FR-016/FR-078 (64) and SC-006 (64/256 by variant). The spec is internally
  inconsistent on a security-relevant default that determines DoS posture.
- **Impact**: Implementation will pick one value (likely whichever the dev
  reads first), tests will be written to a different one, the boot WARN log
  for "unlimited" (FR-016) will or will not fire depending on the choice,
  and the saturation test (test 42, `TestAgentLoop_AskSaturation_SyntheticDeny`)
  cannot be written because its setup ("32 pending — 33rd ask") is *also*
  inconsistent with the candidate defaults. Saturation guard is the
  primary DoS defence for ask-mode; landing it with the wrong default is a
  shippable security regression.
- **Recommendation**: Update the Ambiguity Warnings table to say "**64** (per
  FR-016/FR-078)" — Q2's "Unlimited" is a stale residue from revision 3
  before G-04 was fixed. Reconcile SC-006 to drop the "256 (Cloud variant)"
  clause and align with FR-078's "64 for all variants until Cloud bring-up";
  add a cross-reference note. Update test 42's BDD scenario to use the new
  default (64 pending → 65th call) instead of 32.

---

#### [CRIT-003] Approval timeout default stated as both 60s and 300s in same spec

- **Lens**: Inconsistency
- **Affected section**: US-5 AS-4 (line ~163) vs Approval State Table (line ~242) vs Q1 (line ~1301) vs SC-006 (line ~1104)
- **Description**:
  - US-5 acceptance scenario AS-4: *"the configured timeout (default **60s**)
    elapses"*.
  - Approval State Table `denied_timeout` row: *"after `tool_approval_timeout`
    (default **300s**)"*.
  - Ambiguity Q1: *"`gateway.tool_approval_timeout` default | **300 seconds**"*.
  - SC-006: *"Default `gateway.tool_approval_timeout` is **300 seconds**"*.
  - BDD "Approval timeout treated as deny" (line ~655): *"a 60-second timeout"*
    / *"61 seconds elapse"*.
  US-5 AS-4 and the BDD scenario both contradict the locked-in 300s. The BDD
  is what `TestAgentLoop_AskTimeout_TreatedAsDeny` (test 37) will be written
  against — the integration test will pass with timeout=60s while the
  production gateway runs with 300s, masking real-world timeout regressions.
- **Impact**: Test/runtime drift on a user-facing UX timer. A 300s default
  is fine for most flows but if the implementation lands at 60s "because the
  test said so", users will see a 5x more aggressive default-deny behaviour,
  burning real LLM cost on synthetic permission_denied retries. Conversely,
  a test written to 60s but production timer at 300s will pass quickly under
  artificial conditions but mask wall-clock regressions.
- **Recommendation**: Update US-5 AS-4 and the "Approval timeout treated as
  deny" BDD scenario to say "default **300 seconds**" and "301 seconds
  elapse" (or alternatively make the test use a configurable timeout with
  the default explicitly cited). All four locations (FR, BDD, AS, SC) must
  agree.

---

### MAJOR Findings

#### [MAJ-001] Eight revision-4/5 FRs reference BDD scenarios that do not exist

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: Traceability Matrix rows for FR-061, FR-065, FR-066, FR-068, FR-069, FR-070, FR-071 (tie-break row), FR-073
- **Description**: The traceability matrix says these FRs trace to:
  - FR-061: *"new 'AdminAsk fence' BDD"* — not present in BDD Scenarios.
  - FR-065: *"new 'Mixed-policy batch' BDD"* — not present.
  - FR-066: *"new 'Tools[] dedup invariant' BDD"* — not present.
  - FR-068: *"new 'MCP rename atomicity' BDD"* — not present.
  - FR-069: *"new 'SIGKILL recovery' BDD"* — not present.
  - FR-071: *"new tie-break BDD"* — not present.
  - FR-073: *"new BDD + integration test"* — not present.
  The structural-integrity rule "Every FR has at least one BDD" is failed
  for ≥7 FRs, and the spec's own "Completeness check" line (1198) — *"every
  FR appears with at least one BDD and one test"* — is a false statement
  given the actual BDD section content (lines 412–839, ending at
  "Stale policy entry...").
- **Impact**: Implementing subagents have no Given/When/Then to test against
  for the most subtle behaviours added in rev 5 (admin-ask fence, mixed-policy
  short-circuit, dedup-fail-call, MCP rename atomicity, ungraceful-shutdown
  recovery). QA-lead will write tests freehand; the test names exist (e.g.,
  `TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents`) but the *behaviour
  spec* the test should encode is delegated to the test author's interpretation.
  These are exactly the cases where revision 5 was supposed to add safety —
  the safety property cannot be unambiguously implemented if it isn't written
  as a scenario.
- **Recommendation**: Add the seven missing BDD scenarios. Minimum content
  for each: Given pre-state, When trigger, Then observable outcome with
  enough detail that a stranger could implement the test from the BDD alone.
  The "AdminAsk fence" BDD is the most critical and should cover at least
  three cases: (a) custom agent with `policies: {system.config.set: allow}`
  → effective `ask`; (b) core agent (Ava) with same policy → effective `allow`;
  (c) custom agent with non-RequiresAdminAsk tool `policies: {read_file:
  allow}` → effective `allow` (fence is scoped).

---

#### [MAJ-002] Mid-turn TOCTOU race between filter evaluation and tool execution unaddressed

- **Lens**: Incorrectness
- **Affected section**: FR-041 ("filter is per-LLM-call") + FR-020 (atomic
  policy pointer) + FR-058 (SPA write triggers recompute)
- **Description**: FR-041 says the filter runs per LLM call. The flow is
  (1) loop reads policy snapshot, (2) sends `tools[]`, (3) LLM emits
  `tool_calls[]`, (4) loop executes each tool. Between (3) and (4) the
  operator may PUT a config update via SPA (FR-058), swapping the policy
  pointer. The tool call was authorised under the old snapshot but executes
  under the new one — there is no re-check at execution time. A tool that
  was `allow` at LLM-call assembly but flipped to `deny` in step 3.5 still
  runs. The spec resolves Unasked Q1 ("mid-turn policy change applies on
  next LLM call") but is silent on the within-LLM-call window between filter
  output and tool execution.
- **Impact**: An operator who sees their custom agent doing something
  alarming and toggles the offending tool to `deny` from the SPA expects
  the in-flight tool call to be aborted. Today's spec executes the
  already-emitted call to completion. For destructive operations
  (`exec`, `system.config.set`, MCP-shell-tools) this is precisely the
  scenario where the operator wanted the `deny` to fire *now*, not on the
  next LLM round.
- **Recommendation**: Add an explicit FR specifying tool-execution-time
  policy re-check: before each tool's `Execute` runs, the loop loads the
  current policy pointer and re-resolves the tool's effective policy. If
  it is `deny`, synthesise `permission_denied` and skip execution. If it
  is `ask`, treat as a new ask call (re-pause, re-emit WS event). If still
  `allow`, run. Add a BDD scenario: *"Policy flipped to deny between LLM
  emission and tool execution"*.

---

#### [MAJ-003] FR-046 and FR-065 give overlapping but non-identical specifications for batch deny short-circuit

- **Lens**: Inconsistency
- **Affected section**: FR-046 (rev 4) and FR-065 (rev 5)
- **Description**: FR-046: *"When a user **denies** the K-th call in a
  sequential ask batch, the system MUST auto-deny calls K+1..N with synthetic
  results carrying `message: 'user denied an earlier call in this batch'`
  and audit each as `tool.policy.ask.denied` with `reason:
  'batch_short_circuit'`."* — note: per-call audit events.
  FR-065: *"the audit MUST emit a **single combined event** ...with a list
  of `cancelled_tool_call_ids` rather than one event per cancelled call."*
  — note: single combined audit. Also, FR-046 says "calls K+1..N" without
  policy-class qualification; FR-065 says "regardless of their individual
  policy (`allow`, `ask`, or `deny`)". These two FRs give conflicting
  audit-emission contracts (per-call vs combined) and slightly different
  scopes (ask-only vs all-policies).
- **Impact**: The audit log shape is part of the operator-observable
  contract (audit shipping pipelines parse it). One implementer emits N
  events; another emits one. SIEM rules written against one shape miss the
  other. Test 51 (`TestAudit_Events_Emitted`) cannot validate "the
  short-circuit audit is correct" because the contract is ambiguous.
- **Recommendation**: Mark FR-046 as *"superseded by FR-065"* with a
  cross-reference, or delete FR-046 wholesale and let FR-065 stand. Add an
  explicit sample audit-event payload to FR-065 showing the
  `cancelled_tool_call_ids: ["call_123", "call_124"]` field structure.

---

#### [MAJ-004] FR-074 `args_hash` defined without algorithm, encoding, or normalization

- **Lens**: Ambiguity / Insecurity
- **Affected section**: FR-074, audit table `tool.policy.ask.requested`
  (line ~313: "args_hash" in fields list)
- **Description**: FR-074 says audit events MUST carry `args_hash` so a
  single audit row answers "what arguments were approved/denied". The spec
  does not specify: (1) hash algorithm (SHA-256? SHA-512? Truncated?
  HMAC-with-which-key?), (2) input encoding (JSON canonical form? Go
  `%v`? `json.Marshal` is non-canonical for maps), (3) salting or
  per-deployment domain separation, (4) what to do for arguments that
  contain secrets (the hash leaks dictionary-attack surface for low-entropy
  args like API tokens passed inline).
- **Impact**: Two implementers will produce non-compatible hashes; a forensic
  query "did approval X cover arg payload Y" cannot be answered without
  pinning the algorithm. If the algorithm is `sha256(json.Marshal(args))`,
  Go's non-deterministic map iteration produces different hashes for
  semantically-equivalent inputs — the audit becomes unreliable for the
  exact use-case it exists for. Worse, low-entropy args (e.g.,
  `{"command":"rm -rf /"}` vs a 100-element dictionary of common shell
  commands) are trivially reversible from the hash.
- **Recommendation**: Specify exactly: algorithm = SHA-256 over
  `canonicaljson` (sorted keys, no whitespace, UTF-8) of the args object;
  output = lowercase hex (64 chars). Add a note that `args_hash` is an
  identity/correlation field, not a confidentiality protection — for
  sensitive args, the audit should additionally record a redacted
  preview (e.g., first 32 chars of each value with secrets masked) or a
  reference to a secret store, not rely on hashing.

---

#### [MAJ-005] `session_state` WS event payload schema is undefined

- **Lens**: Incompleteness
- **Affected section**: FR-052, FR-073, US-5 AS-7, edge case "ask-policy
  tool, no WS client connected"
- **Description**: The spec specifies that `session_state` is emitted on
  WS reconnect and is scoped to the authenticated user's sessions, but
  never defines the payload schema. By contrast `tool_approval_required`
  has a precise schema in FR-011: `{approval_id, tool_call_id, tool_name,
  args, agent_id, session_id, turn_id, expires_at}`. FR-052 says the payload
  "includes the current pending-approval set scoped to the session (empty
  after a restart)" — but the *shape* of an empty set vs a populated set,
  whether an `event_type` field discriminates, whether per-session or
  per-user keying — none of this is pinned.
- **Impact**: SPA (`A4 frontend lane`) and gateway (`A3 lane`) must agree
  on the wire format. Without a schema, the integration test 40
  (`TestAgentLoop_AskRestart_CancelsPending`) cannot assert payload
  correctness — only that *some* event fires. The SPA may discard malformed
  events silently and the user observes a "stuck modal" that the spec
  intended to dismiss.
- **Recommendation**: Add a payload schema to FR-052 of the form:
  ```
  {
    "type": "session_state",
    "user_id": "<uid>",
    "pending_approvals": [
      {"approval_id": "...", "session_id": "...", "tool_name": "..."}
    ],
    "emitted_at": "<iso8601>"
  }
  ```
  Add a BDD covering "empty pending_approvals after restart" vs "populated
  pending_approvals after a fresh user reconnect mid-turn".

---

#### [MAJ-006] FR-066 dedup-invariant violation fails the entire LLM call — no rollback specified

- **Lens**: Inoperability
- **Affected section**: FR-066
- **Description**: FR-066 says a duplicate-name in `tools[]` post-assembly
  is "treated as an internal invariant violation that emits HIGH audit ...
  and **fails the LLM call** with a `synthetic_error` returned to the
  loop". The spec does not define: what happens to the in-flight turn?
  Does the loop abort the turn and surface an error to the user, or retry
  with deduplicated tools, or fall back to deny-all-tools-for-this-call?
  What does the SPA show? Does the user's prompt cost a token charge?
  Does the audit fire once per call or once per turn?
- **Impact**: A bug anywhere in registry registration (e.g., a future
  sysagent tool added with a name collision) renders the agent unusable
  with no graceful path. Operators get a HIGH audit and the user sees a
  cryptic `synthetic_error`. This is a fail-loud-everywhere policy when
  fail-quietly-and-recover is plausible (drop the duplicate, audit, log
  WARN, continue).
- **Recommendation**: Specify the recovery: (a) on dedup violation, drop
  the colliding entry deterministically (keep the alphabetically earliest
  source-tag, e.g., `builtin` < `mcp:srv-A` < `mcp:srv-B`), emit HIGH
  audit, log WARN, and continue the LLM call with the deduplicated set;
  (b) the synthetic-error path is reserved for "no tools at all could be
  assembled" — i.e., the registry is fundamentally broken. Update the
  dedup test (`TestAssembly_RejectsDuplicateName`) accordingly.

---

#### [MAJ-007] Boot order step "3a" comes before step "3" — confusing and likely to be misimplemented

- **Lens**: Ambiguity
- **Affected section**: Boot Order section, lines 295–303
- **Description**: Steps in the Boot Order are numbered 1, 2, **3a**, **3**,
  4, 5, 6, 7. Conventionally "3a" is read as a sub-step *of* 3 (executed
  after 3 begins), not as a step *before* 3. Yet the prose explicitly says
  3a runs first ("Before step 3 reads any agent.json, step 3a builds an
  in-memory map..."). FR-062 reinforces "Before step 3". A reviewer
  skimming the numbered list will execute 3 before 3a.
- **Impact**: The corrupt-config disposition table (FR-023) consults the
  step-3a map. If implemented in step-number order, the validator runs
  before the disposition map exists and falls back to the wrong default
  (likely "core agent → abort"), causing every validation failure to abort
  boot — including for non-Ava core agents and custom agents. Boot will
  then reject configs that should be skipped.
- **Recommendation**: Renumber steps to 1, 2, 3 (was 3a — disposition map),
  4 (was 3 — config validation), 5 (was 4 — policy maps), … Keep the
  prose explanation but make the numbering match the execution order.

---

#### [MAJ-008] FR-061 fence visibility through `GET /api/v1/agents/{id}/tools` not specified

- **Lens**: Ambiguity / Inoperability
- **Affected section**: FR-061, FR-028, US-6 AS-2
- **Description**: FR-061 says effective `allow` is downgraded to `ask` for
  RequiresAdminAsk tools on custom agents. FR-028 says the agent-tools
  endpoint returns "the effective policy per tool". Two interpretations are
  consistent with the spec:
  - (i) The endpoint reports the *raw* policy (`allow`) — the SPA
    shows the operator what they configured. The fence is invisible at
    the API layer.
  - (ii) The endpoint reports the *post-fence* effective policy (`ask`) —
    the SPA shows the runtime truth, but operators are confused why
    `allow` they typed shows up as `ask`.
  Both are defensible. The spec picks neither. Phase A3 (REST) and Phase A4
  (SPA) must agree, and the answer determines the SPA UX (do we show a
  badge "downgraded to ask by admin-ask fence"? Do we error-tooltip the
  configured `allow`?).
- **Impact**: SPA/REST divergence. Operators cannot validate their security
  posture from the SPA if option (i) is implemented because they cannot
  see that `allow` actually means `ask`. If option (ii) is implemented
  without UX work, operators see a value they did not type and file
  bug reports. Either way, the fence's primary user-facing contract —
  "operator can't shoot themselves in the foot" — depends on the UI
  surfacing the fence, which the spec does not require.
- **Recommendation**: Pick option (ii) (post-fence effective) for the
  endpoint and add an explicit `fence_applied: true|false` field per tool
  so the SPA can render a badge. Add an FR-079: *"GET
  /api/v1/agents/{id}/tools MUST return both the configured policy
  (`configured_policy`) and the post-fence effective policy
  (`effective_policy`); the SPA MUST surface the difference visually
  when fence_applied=true."* Update FR-043 / preset confirmation dialog
  to mention the fence semantics.

---

#### [MAJ-009] Saturation guard skips emitting WS event but spec is silent on saturation feedback to user

- **Lens**: Inoperability
- **Affected section**: FR-016, US-5 AS-11, edge case
- **Description**: When the saturation cap (default 64) is hit, FR-016
  says the loop synthesises a deny and "no WS event is emitted for this
  call". The user (who sent the prompt that caused the LLM to attempt the
  ask) sees the agent respond as if denied without ever being asked, and
  never sees an approval modal. There is no `tool_saturation_warning` WS
  event, no system message in the chat ("system overloaded — try again
  later"), and no metric the SPA can subscribe to in order to surface
  the condition.
- **Impact**: Multi-tenant deployments hitting the cap have no user-visible
  feedback path. Users believe the agent decided not to call the tool
  ("the model is misbehaving") rather than understanding the system is at
  capacity. Operators see the metric but don't have a UX hook to relay
  the condition to their users.
- **Recommendation**: Add an FR specifying that saturation causes a
  one-line system message in the session transcript ("Agent action
  blocked: approval queue saturated. Retry later or contact your
  administrator.") so the user has visibility, and an audit/metric
  correlation. Optionally emit a `system_overload` WS event scoped to the
  affected session.

---

### MINOR Findings

#### [MIN-001] FR-072 silently coerces empty-string policy values to `allow` — security smell

- **Lens**: Insecurity
- **Affected section**: FR-072
- **Description**: FR-072 preserves existing behaviour: empty string `""`
  for `default_policy` or any per-tool entry is treated as `"allow"`. The
  audit emits `agent.config.empty_policy_value_coerced` (INFO). Operator
  pastes a config from a templating tool that emitted empty strings for
  unfilled fields — the gateway boots silently with default-allow on
  whatever was unfilled, including potentially `system.*` if the field name
  matched. INFO severity buries the signal in routine logs.
- **Recommendation**: Either elevate the coercion audit to WARN (audit log
  shippers commonly grep WARN+) or, preferably, **reject** empty strings at
  config-load time with `agent.config.invalid_policy_value` HIGH (FR-049
  already handles invalid values; an empty string should be classified as
  invalid for policy fields — the empty-coercion behaviour exists in
  `ResolvePolicy` for legacy reasons that no longer apply post-redesign).

---

#### [MIN-002] FR-070 "9 states" mixes FSM states and HTTP responses

- **Lens**: Incorrectness
- **Affected section**: Approval State Table, FR-070
- **Description**: The table lists `gone` as one of the "8 terminal states".
  But `gone` is described as "Any terminal-state approval receives a late
  incoming action → HTTP 410". That is an HTTP response code, not a state
  the approval reaches; the approval is already in `approved` /
  `denied_user` / etc. when the late action arrives. Modeling `gone` as a
  state implies a state transition `denied_user → gone` on late approve,
  which is conceptually wrong (the action does not change the approval —
  it generates a 410 response).
- **Recommendation**: Drop `gone` from the state list; describe it
  separately as "Late-action response: any terminal state + incoming
  action returns HTTP 410 Gone with no state transition." Update FR-070
  to "8 states (1 active, 7 terminal)".

---

#### [MIN-003] FR-051 MCP server rename detection trigger is undefined

- **Lens**: Ambiguity
- **Affected section**: FR-051, FR-068
- **Description**: Both FRs describe what happens "when an MCP server is
  renamed in config" but neither specifies how rename is detected. Is it
  diff between consecutive `mcp.servers` map keys on `ReloadProviderAndConfig`?
  Is there a stable identifier? If two changes happen in one reload — old
  `srv-A` removed, new `srv-B` added with different URL but identical tool
  set — is that a rename or an unrelated-add-and-remove?
- **Recommendation**: Define rename detection: rename = same server URL
  and process identity but different config name (or whatever durable
  field is available pre-#153). Two add+remove with different URLs is
  not a rename. Add this to FR-051.

---

#### [MIN-004] No FR governs LLM behaviour when synthetic errors saturate a turn

- **Lens**: Incompleteness
- **Affected section**: FR-013, FR-016, FR-046, FR-065, FR-069
- **Description**: Many error paths produce synthetic deny/error tool
  results that go back into the LLM context. If the LLM, faced with a
  synthetic deny, retries the same tool (e.g., loops `exec` 30 times,
  each denied for queue-saturation), the turn never converges. There is
  no per-turn error-floor (e.g., "after K consecutive synthetic denies,
  abort the turn with a system message").
- **Recommendation**: Add FR-080: *"After N consecutive synthetic-deny
  tool results within a single turn (default N=8), the loop terminates
  the turn with a system message and emits audit `turn.aborted_synthetic_loop`."*

---

#### [MIN-005] `gateway.tool_approval_max_pending` sentinel value semantics undefined for negatives

- **Lens**: Ambiguity
- **Affected section**: FR-016
- **Description**: FR-016 specifies sentinel `0` = unlimited. What about
  negative values? Does -1 mean "always saturated" or "config error" or
  "unlimited"? Operators editing config.json may type `-1` thinking it
  means "no override" (common in env-var conventions).
- **Recommendation**: FR-016 amend: "Negative values are a config error
  rejected at boot with HIGH audit `gateway.config.invalid_value`;
  gateway exits non-zero. Only `0` (unlimited) and positive integers
  are accepted."

---

#### [MIN-006] Test 42 BDD setup uses old default of 32, not new 64

- **Lens**: Inconsistency
- **Affected section**: BDD "Approval queue saturation — synthetic deny"
  (line ~726)
- **Description**: The BDD says "*`gateway.tool_approval_max_pending` is
  **explicitly configured** to 32 (default is unlimited; this scenario
  tests the optional cap)*". This is the rev-3-era setup. Post-rev-4
  default is 64 (FR-016) per the resolved G-04 finding, but the BDD wasn't
  updated.
- **Recommendation**: Update the BDD's Given line to "`...max_pending` is
  at its default value of 64; 64 approvals are currently pending; ...
  the LLM emits the 65th ask call".

---

#### [MIN-007] FR-009.5's worked example uses `system.agent.*` which is not a real tool prefix

- **Lens**: Ambiguity
- **Affected section**: FR-009 sub-rule 5 (worked example)
- **Description**: The example says: *"for tool `system.agent.create`
  against policy `{"system.agent.*": "ask", "system.*": "deny",
  "system.agent.create": "allow"}`"*. `system.agent.*` is a plausible
  multi-segment wildcard. But FR-009.1 said *"only a trailing `.*` is a
  wildcard"* — does `system.agent.*` count as trailing? Yes (last two
  chars are `.*`). The example is fine, but it would be clearer if the
  rule explicitly stated "the wildcard `.*` matches any one or more
  trailing segments" (or "any trailing string that doesn't reintroduce
  segment delimiters").
- **Recommendation**: Add to FR-009.1: "the trailing `.*` matches any
  non-empty suffix; e.g., `system.*` matches `system.foo.bar` and
  `system.foo`. Wildcards do not match the prefix alone (`system.*` does
  not match the literal name `system`)."

---

### Observations

#### [OBS-001] 78 functional requirements for one filter relocation suggests scope creep

- **Lens**: Overcomplexity
- **Affected section**: FR-001 through FR-078
- **Suggestion**: The original problem statement is "move the filter from
  the REST handler to the LLM-call assembly path". The spec has grown to
  78 FRs across approval state machines, audit pipelines, MCP rename
  semantics, RBAC, frontend regression tests, golden files, and stderr
  fallback formats. Many are good, but the spec is now harder to review
  than the implementation. Consider splitting: (a) core registry +
  filter relocation (FR-001..FR-010, FR-021..FR-040), (b) ask-mode
  approval protocol (FR-011..FR-018, FR-046..FR-048, FR-061..FR-070),
  (c) operability / observability (FR-038..FR-039, FR-053..FR-058,
  FR-063..FR-076). Each can be reviewed and shipped independently.

---

#### [OBS-002] No rollback / feature-flag for the redesign

- **Lens**: Inoperability
- **Affected section**: Phase A, Phase D
- **Suggestion**: Even pre-1.0 there are dev/CI/staging gateways. A failed
  rollout has no revert. Consider gating central-registry+filter behind a
  config flag (`tools.central_registry_enabled`, default true after Phase
  D2 passes) so a hotfix can flip back to per-agent registries in case of
  unanticipated regression.

---

#### [OBS-003] FR-069 SIGKILL-recovery synthetic entries may confuse LLM context on session resume

- **Lens**: Incorrectness
- **Affected section**: FR-069
- **Suggestion**: When a session is resumed (next user message), the
  transcript contains a synthetic `turn_cancelled_restart` system entry
  immediately after a `tool_call` with no result. The LLM sees this and
  may hallucinate a result, or apologise about a tool call it does not
  remember initiating. Consider whether the loop should also remove the
  orphaned `tool_call` on recovery (rather than just appending the cancel
  marker), or add a system instruction in the rebuild prompt: "The
  previous tool call was cancelled; please ignore it and respond to the
  current user message."

---

#### [OBS-004] Approval `expires_at` should be derivable but is sent over the wire

- **Lens**: Overcomplexity
- **Affected section**: FR-011
- **Suggestion**: The WS event payload includes `expires_at`. The SPA can
  compute this from `emitted_at + tool_approval_timeout` if the timeout
  is known. Sending `expires_at` doubles the truth source — clock skew
  between server and client now matters. Consider sending `expires_in_ms`
  instead (a duration relative to receipt, immune to clock skew).

---

#### [OBS-005] Real-LLM E2E tests in CI carry hidden cost and flake risk

- **Lens**: Inoperability
- **Affected section**: Phase A5, tests 53–58, Phase D2
- **Suggestion**: Tests 54, 55, 56, 57, 58 ("real LLM provider", "Real
  Anthropic / OpenRouter call") will incur per-CI-run API charges and
  fail randomly when the provider is rate-limited or the model retunes
  its behaviour. Consider gating real-LLM tests behind a separate CI job
  (nightly + pre-release only), with a recorded-fixture mode for the PR
  pipeline. The tests are valuable but their cost and flake-rate will
  push subsequent PRs to skip them, defeating the safety case.

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | All 7 stories have AS-1..AS-N. |
| Every acceptance scenario has BDD scenarios | PARTIAL | Most do; rev-5 additions on US-5 (admin-ask fence, mixed-policy batch, dedup invariant, MCP rename, SIGKILL recovery, tie-break, per-user session_state) are missing — see MAJ-001. |
| Every BDD scenario has `Traces to:` reference | PASS | All BDDs reference US-N AS-M. |
| Every BDD scenario has a test in TDD plan | PASS | Each BDD maps to ≥1 test in 1–58. |
| Every FR appears in traceability matrix | PASS | All FR-001..FR-078 present (some "reserved"). |
| Every BDD scenario in traceability matrix | PASS | Implicitly via FR mapping. |
| Test datasets cover boundaries/edges/errors | PASS | Datasets 1–16, 1–8, 1–8, 1–11 are reasonably exhaustive. |
| Regression impact addressed | PASS | Regression table + R1..R4 dataset present. |
| Success criteria are measurable | PARTIAL | SC-005 ("p95 within 200ms") and SC-011 ("zero double-executions across 100 trials") are measurable; SC-013 ("zero matches" via grep) is measurable. SC-006 contradicts FR-016/FR-078 on default values — see CRIT-002. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Mid-turn TOCTOU | No test for policy flipped between LLM emission and tool execution | (See MAJ-002) |
| Synthetic-error feedback loop | No test for LLM looping on saturation/deny synthetic errors | FR-016, FR-046, FR-065 |
| `args_hash` determinism | No test for stability of args_hash across map iteration | FR-074 |
| `session_state` payload schema | No test asserts payload shape vs schema | FR-052, FR-073 |
| Fence visibility via REST | No test confirms `GET /agents/{id}/tools` exposes fence-downgraded policy | FR-061, FR-028 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Per-agent + global policy resolution | Negative `tool_approval_max_pending` value | Add row 17 with `max_pending=-1` → boot reject |
| Boot-time config validation | Empty-string `default_policy: ""` | Add row 9: empty → coerced to allow + INFO audit (per FR-072) |
| Registry registration | MCP tool name = `system.foo` | Add row 9: rejected with `conflict_with: "reserved_prefix"` (per FR-060) |
| Ask-mode protocol | Custom agent + RequiresAdminAsk tool with `policies: {x: allow}` | Add row 12: effective policy is `ask` not `allow` (FR-061) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Central registries (builtin + MCP) | ok | ok | ok | ok | risk | ok | DoS via registry-thrash on rapid MCP reconnect not bounded |
| Filter / `FilterToolsByPolicy` | ok | ok | ok | ok | ok | risk | MAJ-002: TOCTOU between filter and tool exec on policy mutation |
| Approval endpoint | ok | ok | ok | ok | risk | ok | MAJ-009: saturation cap is the only bound; per-user fairness not specified |
| Audit subsystem | ok | risk | risk | risk | ok | ok | MAJ-004: args_hash leaks low-entropy args; MAJ-003: contract for batch short-circuit ambiguous (auditor non-repudiation impacted) |
| WS `tool_approval_required` / `session_state` | ok | ok | ok | risk | ok | ok | MAJ-005: payload schema undefined; FR-073 cross-user leakage avoided iff scoping is correctly implemented |
| Boot config validator | ok | ok | ok | ok | ok | risk | MAJ-007: step ordering bug could cause wrong abort/skip disposition |
| MCP adapter | risk | ok | ok | ok | ok | risk | CRIT-001: contradiction on RequiresAdminAsk capability is a capability-elevation hazard |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **Fence visibility in REST**: Does `GET /api/v1/agents/{id}/tools` return
   the configured policy or the post-fence effective policy? (MAJ-008)
2. **MCP `requires_admin_ask`**: Allowed (FR-064) or forbidden (FR-059)?
   The spec says both. (CRIT-001)
3. **Actual default for `tool_approval_max_pending`**: 64, 256, or
   unlimited? Three values stated. (CRIT-002)
4. **Actual default for `tool_approval_timeout`**: 60s or 300s? Two values
   stated. (CRIT-003)
5. **TOCTOU window**: should tool execution re-check policy, or honour the
   filter-time decision even after a deny was applied mid-turn? (MAJ-002)
6. **`args_hash` algorithm**: which hash, what canonicalization, what
   redaction strategy for sensitive args? (MAJ-004)
7. **`session_state` schema**: what fields, what type discriminator? (MAJ-005)
8. **Dedup-violation recovery**: fail the LLM call (current FR-066) or
   recover by deterministic deduplication? (MAJ-006)
9. **Boot order step numbering**: should "3a" execute before "3"?
   (MAJ-007 — fix by renumbering)
10. **User feedback on saturation**: how does the user know the agent's
    silence is due to system overload, not LLM choice? (MAJ-009)
11. **Synthetic-error loops**: at what point does the loop give up and
    abort the turn? (MIN-004)
12. **MCP rename detection trigger**: how is rename distinguished from
    add+remove? (MIN-003)
13. **Negative cap values**: rejected, treated as unlimited, or treated
    as "always saturated"? (MIN-005)
14. **Empty-string policy values**: silently coerced (current FR-072) or
    rejected as invalid? (MIN-001)
15. **Variant config mechanism**: how does the gateway know it's a "Cloud
    variant" at runtime? Referenced in FR-016 / SC-006 but not defined.

---

## Verdict Rationale

The spec resolves a real and well-scoped engineering problem (filter
relocation + central registry) and has been tightened across five revisions.
The structural skeleton is mostly sound and most prior grill findings are
genuinely closed.

It cannot ship as-is because **CRIT-001/002/003 are direct internal
contradictions** that will produce divergent implementations: two engineers
reading FR-059 and FR-064, or FR-016 and Q2, or AS-4 and Q1, will build two
different systems and only the contradiction will surface in production.
**MAJ-001 (eight missing BDDs)** undercuts the spec's own completeness
claim and means the rev-5 safety properties (admin-ask fence, dedup
invariant, mixed-policy short-circuit, SIGKILL recovery) are delegated to
test-author interpretation. **MAJ-002 (mid-turn TOCTOU)** is a security
hole the spec missed despite specifically resolving Unasked Q1. **MAJ-007
(boot-order numbering)** is a 30-second fix that will otherwise cause an
implementation defect with cascading impact on FR-023 disposition.

These must be reconciled before implementation begins.

### Recommended Next Actions

- [ ] Reconcile CRIT-001 by deleting either "MCP cannot opt in" (FR-059) or "MCP MAY opt in" (FR-064)
- [ ] Reconcile CRIT-002: pick a single default for `tool_approval_max_pending` and propagate to FR-016, FR-078, SC-006, and Q2
- [ ] Reconcile CRIT-003: update US-5 AS-4 and the timeout BDD to 300s to match Q1/SC-006
- [ ] Add the seven missing BDD scenarios (MAJ-001): admin-ask fence, mixed-policy batch, dedup invariant, MCP rename atomicity, SIGKILL recovery, wildcard tie-break, per-user `session_state`
- [ ] Add an FR for tool-execution-time policy re-check (MAJ-002)
- [ ] Mark FR-046 as superseded by FR-065 (MAJ-003)
- [ ] Specify `args_hash` algorithm and canonicalization (MAJ-004)
- [ ] Specify `session_state` payload schema (MAJ-005)
- [ ] Specify dedup-violation recovery behaviour (MAJ-006)
- [ ] Renumber Boot Order steps to match execution order (MAJ-007)
- [ ] Specify fence visibility through `GET /api/v1/agents/{id}/tools` (MAJ-008)
- [ ] Add user-visible saturation feedback (MAJ-009)
- [ ] Address MIN-001..MIN-007 in the same revision pass
- [ ] Consider OBS-001 (split spec) before Phase A begins

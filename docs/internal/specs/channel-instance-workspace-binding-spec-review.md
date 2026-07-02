# Adversarial Review: Channel-Instance ↔ Workspace Binding + Multi-Instance Routing

**Spec reviewed**: `docs/internal/specs/channel-instance-workspace-binding-spec.md`
**Review date**: 2026-07-02
**Verdict**: REVISE

## Executive Summary

This is a mature, well-grounded plan-spec that inherits a strong ADR (0 CRITICAL after 3 grill rounds), and most of its code citations verify against source. It has **no CRITICAL findings** — the drift-enforcement mechanism and the routing precedence are correctly specified. However, it carries several MAJOR gaps that will cause rework or incorrect behaviour if implemented as-is: the routing UI's actual interaction model (auto-save, no Save button) directly contradicts the spec's "Save is disabled + hint" model; the session-key fix targets a code path (`SessionKeyParams`) that the inbound routing does **not** use; the drift-observability/operator-alert infrastructure it depends on does not exist; and the `getChannelRouting`/`setChannelRouting` handlers read/write channel-wildcard **bindings**, not `cfg.Channels[].Identity`, so the "modify" framing understates the rewrite. Fix the MAJORs (especially MAJ-001, MAJ-002, MAJ-004) before `/taskify`.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 8 |
| MINOR | 6 |
| OBSERVATION | 4 |
| **Total** | **18** |

---

## Findings

### CRITICAL Findings

None. The two hot-path changes (drift drop branch, `resolveMessageRoute` Drop honoring) and the routing precedence are correctly and concretely specified, and the drop-and-alert mechanism is enforced at both fallback sites. The security-sensitive `InstanceID`-from-trusted-adapter requirement (FR-021) is present and tested.

---

### MAJOR Findings

#### [MAJ-001] The routing UI has no Save button — it auto-saves on select; US-1/US-3's "Save is disabled + hint" model does not exist and requires a UX redesign the spec doesn't scope

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: US-1 (AC-2, AC-3), US-3 (AC-1), FR-001, FR-004, FR-009; §3 symbol row `ChannelConfigPanel.tsx (routing section 678-724)` marked **modify**; BDD "Save blocked and hint shown when no agent selected".
- **Description**: The spec repeatedly asserts a "Save is disabled" / "Save stays disabled" interaction (US-1/AC-2, US-3/AC-1, US-2/AC-3, FR-004, FR-009, DS-1). The current `ChannelConfigPanel.tsx` routing section has **no Save button**: it uses a `SmartSelect` whose `onValueChange` calls `doSaveRoutingDebounced` (400 ms debounce → `setChannelRouting` PUT) — every selection auto-persists (`ChannelConfigPanel.tsx:699-704`, `doSaveRouting` mutation at `:345`). There is also no workspace selector and no `canSave` state. So "block Save" is not a small filter change on an existing control — it requires **introducing a Save-button interaction model** (or an explicit commit affordance) for the bound flow, plus deciding what happens to the unbound/legacy webchat auto-save path. The ADR's D3 gestures at "mirror `canSave` gating as in `ScheduleFormSheet.tsx`" but the spec's symbol table calls this a `modify` of an auto-save component, which understates it.
- **Impact**: The frontend workstream (WS-E) will discover mid-implementation that there is no Save button to disable, forcing an unplanned interaction-model change (add Save button; convert auto-save to explicit commit for bound instances; reconcile with the still-auto-saving unbound path). Rework, scope creep, and a likely inconsistency where bound instances commit on Save but unbound instances commit on change.
- **Recommendation**: Add an explicit user story / FR for the interaction-model change: "The bound routing flow MUST present an explicit commit control (Save button) gated by `canSave = workspaceSelected && agentSelected`; the unbound/webchat flow retains today's auto-save." Update the US-1/US-3 acceptance scenarios and the E2E spec (#24) to name the Save control, and update the §3 `ChannelConfigPanel.tsx` row to note the auto-save→explicit-commit conversion. Confirm whether the unbound path also gains a Save button or keeps debounced auto-save.

#### [MAJ-002] The session-key fix targets `SessionKeyParams`/`BuildAgentPeerSessionKey`, but the inbound routing path uses `agentSessionKey` which never calls it — FR-023/US-9 as specified will not prevent collisions

- **Lens**: Incorrectness
- **Affected section**: FR-023, US-9/AC-2, §3 symbol rows `agentSessionKey / BuildAgentPeerSessionKey / SessionKeyParams (loop.go:4624, route.go:68-75)` marked **modify**; TDD #12 `TestSessionKey_IncludesInstanceID`; ADR MAJ-006.
- **Description**: The spec (and ADR MAJ-006) says "add `InstanceID` to `SessionKeyParams`/`agentSessionKey` … so N same-type instances don't share a transcript namespace." But `agentSessionKey(agentID, msg)` (`loop.go:4624-4630`) builds the key as `agent:<id>:session:<SessionID>` or `agent:<id>:chat:<Channel>:<ChatID>` — it does **not** call `BuildAgentPeerSessionKey` or use `SessionKeyParams` at all. `BuildAgentPeerSessionKey(SessionKeyParams{...})` is only invoked inside `ResolveRoute`'s `choose()` closure (`route.go:68`), and its result (`route.SessionKey`) is discarded by `resolveMessageRoute`, which overwrites it with `agentSessionKey(...)` on the explicit/handoff paths and consumes `route.AgentID` only on the P0/binding path. So adding `InstanceID` to `SessionKeyParams` changes the route's `SessionKey` field that the inbound path largely ignores; the actual inbound transcript key (`agentSessionKey`) is keyed on `msg.Channel` (the TYPE) + `msg.ChatID`, or on `msg.SessionID`. Two `whatsapp:eu`/`whatsapp:us` instances messaging the same `ChatID` still collide unless `agentSessionKey` itself (or the `SessionID` minting upstream) is made instance-aware.
- **Impact**: US-9/AC-2 ("the session keys differ by `InstanceID` and do not collide") will pass a unit test on `BuildAgentPeerSessionKey` (#12) while the real inbound path still collides — a false-green exactly of the flavour the ADR's O-2 spike warns about. Transcript cross-contamination between two same-type instances.
- **Recommendation**: Re-target the fix to the code path actually used: make `agentSessionKey` (and/or the upstream `SessionID` minting for channel inbound) incorporate `InstanceID`. Trace where `msg.SessionID` is assigned for channel messages and confirm it is instance-scoped. Change TDD #12 to assert on `agentSessionKey`/the minted `SessionID`, not `BuildAgentPeerSessionKey`. If `BuildAgentPeerSessionKey` also needs it (for the P0/binding SessionKey used elsewhere), keep that change but do not rely on it for FR-023.

#### [MAJ-003] `bound_instance_drift` counter and the "operator alert" have no infrastructure; FR-027/US-5 assume a metrics + notification path that does not exist

- **Lens**: Incompleteness / Inoperability
- **Affected section**: FR-027, US-5 (AC-1 "an operator alert is emitted"), §1 In scope "Observability (`matched_by`, `bound_instance_drift` counter)", SC-004, D3 step 3 "operator notification and a `bound_instance_drift` counter", BDD drift outline "+ alert".
- **Description**: In the routing layer, `matched_by` exists only as a **structured-log field** on WARN calls (`route.go:280,292`) — there is no metric/counter registry the spec can "increment". A grep for `bound_instance_drift` / counter / metric in `pkg/routing` and `pkg/agent/loop.go` finds only unrelated atomic counters and log fields; there is no generic operator-notification channel for routing events (the `Notify*` methods in `loop.go` are task-specific). So FR-027's "`bound_instance_drift` counter" and US-5/AC-1's "operator alert is emitted" require **new** observability + notification plumbing that the spec neither designs nor scopes, and A-3 defers "UI for the drift operator alert" to "refine in frontend task" without deciding the emission mechanism.
- **Impact**: WS-A will implement the drop branch but have nowhere to emit the counter or the alert; SC-004 ("100% produce a `bound_instance_drift` event") is untestable because no event type exists; the "+ alert" in the BDD outline and H-5 holdout cannot be verified. Either the requirement is silently dropped or a metrics/notification subsystem is invented ad-hoc during implementation.
- **Recommendation**: Decide and specify the emission mechanism explicitly: (a) what "counter" means here (a structured audit-log event of a named type? an in-memory `atomic.Int64` exposed on `/health`? a Prometheus metric — noting the single-binary/no-new-deps constraint); (b) what "operator alert" means concretely (audit entry + which existing surface renders it — the ADR's A-3 suggests a channels-screen warning badge, which itself needs a data source). Add an FR for the audit-event schema and a success criterion that names the observable artifact. If a metrics registry doesn't exist, prefer an audit-log event type + a query the SPA badge reads.

#### [MAJ-004] `getChannelRouting` / `setChannelRouting` operate on channel-wildcard `cfg.Bindings`, not `cfg.Channels[].Identity`; "modify" understates a semantic rewrite, and `getChannelRouting` returning `workspace_id` requires reading a different store

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §3 symbol rows `setChannelRouting (rest.go:6366-6476)` and `getChannelRouting (rest.go:6352-6364)` marked **modify**; FR-005–FR-008; BDD "Valid bound routing persists and sets instance identity" (asserts `cfg.Channels["whatsapp:eu"].Identity == {kind:agent,id:ray}` **and** "any stale channel-wildcard binding … is removed"); TDD #14, #15.
- **Description**: Today `setChannelRouting` (`rest.go:6366+`) upserts/removes a **channel-wildcard `AgentBinding`** in `cfg.Bindings` (`match.channel`, `account_id:"*"`) and `getChannelRouting` (`rest.go:6352`) reads that binding via `channelWildcardIdx(cfg.Bindings, channelID)`. It never touches `cfg.Channels[id].Identity` or a `WorkspaceID`. The spec's target behaviour is a different persistence model entirely: write `cfg.Channels[id].WorkspaceID` + `cfg.Channels[id].Identity`, and *remove* the old wildcard binding. That is a **rewrite of both handlers' data model**, not a modification, and it has two consequences the spec under-specifies: (1) `getChannelRouting` must now read `WorkspaceID`+`Identity` from `cfg.Channels` (a `SmartSelect`-driven `default_agent_id` is no longer the source of truth) — the spec says "Return `workspace_id`" but not "stop reading the wildcard binding"; (2) the bound and unbound instances now persist routing in **two different places** (Channels.Identity vs Bindings wildcard), so `getChannelRouting` must return the right one per instance state, and a reader must disambiguate.
- **Impact**: If `getChannelRouting` keeps reading the wildcard binding while `setChannelRouting` writes `Identity`, the round-trip breaks (GET returns null after a bound PUT) — TDD #15 `TestGetChannelRouting_ReturnsWorkspaceID` would pass on `workspace_id` while `default_agent_id` regresses. Config ends up with two competing routing representations.
- **Recommendation**: Re-label both rows **rewrite**, and add an explicit requirement: for a bound instance, `getChannelRouting` reads `{WorkspaceID, Identity.ID}` from `cfg.Channels[id]`; for an unbound instance it reads the legacy wildcard binding; the response's `default_agent_id` reflects whichever applies. Add a BDD scenario for the GET round-trip of a bound instance (currently only the PUT is covered). Specify that the two representations are mutually exclusive per instance (bound → Identity, wildcard binding removed; unbound → wildcard binding only).

#### [MAJ-005] Workspace-delete cascade to bound instances is a new step spanning two stores (`workspaces/<id>.json` vs `config.json`) with undefined ordering/atomicity

- **Lens**: Incompleteness
- **Affected section**: US-10/AC-1, FR-025, TDD #21 `TestWorkspaceDelete_CascadesToInstances`; ADR MAJ-005.
- **Description**: `handleWorkspaceDelete` (`rest_workspaces.go:986`) has a defined 6-step cascade (heartbeat crons → heartbeat sessions → milestones → tasks → workspace file → dir) but **does not touch `cfg.Channels` or `cfg.Bindings`**. US-10/AC-1 adds a new cascade action (disable + unbind bound instances) that writes `config.json` — a **different persisted store** from the workspace file. The spec does not specify where in the existing sequence the config write lands, nor the failure semantics if the config write fails after the workspace file is already removed (orphaned dead binding pointing at a now-nonexistent workspace — precisely what US-10 aims to prevent). It also doesn't say whether "unbind" clears `WorkspaceID` only, or `WorkspaceID` + `Identity` (drift would otherwise fire on the next inbound if `Identity` remains but the workspace is gone).
- **Impact**: A partial cascade (workspace file deleted, config write fails) strands a bound instance routing to a deleted workspace — the exact orphan the requirement forbids. Non-deterministic recovery.
- **Recommendation**: Specify: (a) the config-channel disable+unbind runs **before** the workspace-file removal (so a failure aborts with the workspace still intact and consistent); (b) "unbind" clears both `WorkspaceID` and `Identity` and sets `Enabled=false`; (c) the failure mode (abort with 500, leaving workspace + bindings intact). Add a negative BDD/test for the partial-failure path.

#### [MAJ-006] Archived-workspace behaviour is contradictory across the spec: "404 on write" vs "existing binding routes stale"

- **Lens**: Inconsistency
- **Affected section**: §4 Edge Cases ("Workspace archived after binding → treated like unknown on write (404); existing binding routes stale until reconfigured"); DS-1 row 6 (`archived1 … 404`); FR-007 ("MUST return 404 when `workspace_id` is unknown **or archived**"); US-5/AC-3 (stale-but-functional routing).
- **Description**: FR-007 says an archived workspace is a 404 on the routing PUT. The edge-case table says the existing binding **still routes stale** until reconfigured. These describe two different subsystems (write-time validation vs route-time resolution) and are individually coherent — but the spec never states the route-time rule for an archived (not deleted) workspace as a first-class requirement, and DS-3 (drift trigger states) omits "workspace archived" entirely. So route-time behaviour when the *workspace* (not the agent) is archived is undefined: does the bound instance keep routing to its member agent (stale-but-functional, like agent-removed-from-CoreTeam), or drop? The drift policy (FR-013) is defined only in terms of the *agent's* state (deleted/disabled/worker/removed-from-team), never the *workspace's* state.
- **Impact**: An operator archives a workspace with a live bound WhatsApp number; implementers must guess whether inbound keeps flowing to the agent or drops. Divergent implementations; a real "does my number still answer?" ambiguity.
- **Recommendation**: Add a first-class route-time rule and a DS-3 row for "bound workspace archived (agent still valid)": recommended = route stale-but-functional (matches the CoreTeam-removal precedent), SPA warns; drop only if the agent itself drifts. State it in FR-013 or a new FR, and add the DS-3 row + a BDD scenario.

#### [MAJ-007] `ChannelId` enum member count is misstated and the "open the enum" decision (A-2, FR-024) is deferred to Gate 0 while BDD/tests assert concrete 404-vs-404 distinctions that depend on the undecided design

- **Lens**: Ambiguity / Incorrectness
- **Affected section**: A-2 (deferred to Gate 0), FR-024, US-11 (AC-1/AC-2), BDD "Unknown instance id is 404 (not enum-gate)", TDD #16; ADR §Current-state (claims "16 members … `validChannelIDs` allows 14").
- **Description**: The ADR states the `ChannelId` enum has 16 members and `validChannelIDs` allows 14. Actual: `ChannelId.yaml` has **15** members (webchat…email) and `validChannelIDs` (`rest.go:6310`) allows **14** (all but webchat). Minor, but the spec inherits the count. More importantly, US-11/AC-2 and BDD "Unknown instance id is 404 (not enum-gate)" require the system to **distinguish** an enum-gate 404 from an unknown-instance 404 — but whether that distinction is even possible depends on the A-2/FR-024 Gate-0 decision (open the enum to a pattern vs. add instance-CRUD). If the enum is opened to a permissive `string`/pattern, the "enum-gate 404" ceases to exist as a distinct outcome, making the BDD scenario's premise unsatisfiable as written. The spec asserts a testable behaviour that its own deferred design decision may invalidate.
- **Impact**: TDD #16 and the BDD "Unknown instance id is 404 (not enum-gate)" may be unimplementable depending on the Gate-0 choice; the "distinct from the enum-gate 404" wording bakes in an assumption (a closed enum still exists) that contradicts the "open the enum" option.
- **Recommendation**: Either resolve A-2/FR-024 in the spec (not defer to Gate 0) since two BDD scenarios and a TDD test depend on it, or rewrite US-11/AC-2 to be design-agnostic: "an unknown instance id returns 404 with a body distinguishing 'unknown instance' from a malformed id" — dropping the "enum-gate" framing. Correct the enum count (15/14) in the ADR and any inherited reference.

#### [MAJ-008] The instance-key grammar (A-1) is a one-way door deferred to Gate 0, yet DS-2 row 7 asserts a "max length" bound the spec never defines, and DS-1 row 7 leaves case-normalization outcome as "200 or 422 per normalization"

- **Lens**: Ambiguity / Infeasibility
- **Affected section**: A-1 (deferred to Gate 0), FR-017, DS-2 (rows 2, 4, 7), DS-1 row 7 ("200 or 422 per normalization"), §4 Edge Cases ("Instance key with illegal characters / uppercase / reserved delimiter → rejected").
- **Description**: FR-017 requires the grammar be "defined (`<type>:<slug>`, lowercase, bounded)" but leaves the actual bound, delimiter, and reserved-set to Gate 0 (A-1). Yet the test datasets already encode specific expected outcomes that presuppose decisions not yet made: DS-2 row 7 `verylongslug×256 → no (max length)` asserts a 256 max-length rule that no requirement states; DS-2 row 4 `whatsapp:eu:2 → no (extra delim)` presupposes `:` is the delimiter (which A-1/NEW-3 flags as possibly illegal in cred keys and may become `-`/`__`); DS-1 row 7 leaves the case-normalization result undecided ("200 or 422 per normalization"), so the test is non-deterministic. A test dataset with an undecided expected value is not a test.
- **Impact**: TDD #3 `TestInstanceKeyGrammar_Validation` cannot be written against DS-2 until the grammar is locked; DS-1 row 7 cannot assert a status. If Gate 0 picks `-` or `__` as the delimiter (per NEW-3), DS-2 rows 1/4/5 and every `whatsapp:eu` literal throughout the spec (BDD, DS-3, DS-4, SC, holdouts) become wrong.
- **Recommendation**: Lock the grammar **in the spec** (it is a one-way door and pervades every scenario): choose the delimiter after checking the cred-key charset now (not at Gate 0), define the max slug length as an explicit FR, and decide the case rule (recommend: reject uppercase at write time → DS-1 row 7 = 422, deterministic). If the delimiter turns out to be `-`/`__`, do a spec-wide find/replace of `whatsapp:eu` so the datasets and BDD literals are self-consistent. At minimum, replace the literal `:` with a named placeholder (`<DELIM>`) everywhere until locked.

---

### MINOR Findings

#### [MIN-001] `RouteInput.InstanceID` and `Identity` already exist — the spec lists `RouteInput` as "modify: add `BoundInstance bool`" but implies `InstanceID` is new elsewhere

- **Lens**: Incorrectness (scope accuracy)
- **Affected section**: §3 symbol row `RouteInput (route.go:11-28)` "**modify** — Add `BoundInstance bool`"; §1 In scope lists "`SessionKeyParams.InstanceID`" and "inbound `InstanceID` stamping" as if greenfield.
- **Description**: `RouteInput` already carries `InstanceID` and `Identity *config.ChannelIdentity` (`route.go:18-27`), and `resolveMessageRoute` already passes both (`loop.go:4577-4582`), and `inboundInstanceID`/`resolveInboundIdentity` already exist (`loop.go:8493-8525`). So the only *new* field on `RouteInput` is `BoundInstance bool`; the identity plumbing is present (the gap is that no adapter *stamps* `msg.InstanceID`, which FR-020 correctly addresses). The spec is mostly accurate here but the framing risks an implementer re-adding `InstanceID`.
- **Recommendation**: Add a one-line note in §3 that `RouteInput.InstanceID`, `RouteInput.Identity`, `inboundInstanceID`, and `resolveInboundIdentity` **already exist**; the net-new routing work is `BoundInstance` + the drift branch + adapter stamping (FR-020). This sharpens the "modify" scope.

#### [MIN-002] Existing worker 400 vs. new 422 for worker default is inconsistent and not reconciled

- **Lens**: Inconsistency
- **Affected section**: FR-008 (422 for worker), US-3/BDD outline (`worker1 → 422`), DS-1 row 4 (422); current `setChannelRouting` returns **400** for a worker (`rest.go`: "workers are not chat targets…").
- **Description**: The live handler returns `http.StatusBadRequest` (400) for a worker default. The spec standardizes on 422 (ADR D3 notes "existing 400 semantics may be kept, but standardize"). The spec picks 422 but doesn't flag that this **changes an existing status code**, which is a (minor) contract-behaviour change any client/test asserting 400 will notice.
- **Recommendation**: Note explicitly that the worker rejection status changes 400→422 and check for any existing test/SPA handling asserting 400 on this path.

#### [MIN-003] `SmartSelect` disabled-state capability is assumed but unverified

- **Lens**: Infeasibility (unverified dependency)
- **Affected section**: US-1/AC-1 ("the agent selector is disabled"), ADR D3 ("gate the agent `SmartSelect` on `workspaceId !== ''`").
- **Description**: The spec requires the agent `SmartSelect` to render disabled until a workspace is chosen. A grep for `disabled` in `SmartSelect.tsx` returned nothing (the component's disabled support was not confirmable in this review). If `SmartSelect` doesn't expose a `disabled` prop, the frontend must add one or wrap it.
- **Recommendation**: Verify `SmartSelect` supports a `disabled` prop; if not, add a small task to extend it, or specify a wrapper/overlay approach.

#### [MIN-004] Empty-CoreTeam picker state (FR-009) has no route-time counterpart specified

- **Lens**: Incompleteness
- **Affected section**: FR-009, US-2/AC-3.
- **Description**: FR-009 handles the *config-time* empty-CoreTeam case (disable Save + hint). But nothing states what happens if a workspace's CoreTeam is emptied *after* an instance is already bound to a member (that member is then removed) — this is partially covered by FR-013 (removed-from-CoreTeam → stale route) but the "CoreTeam emptied entirely" case isn't called out, and it's the natural extreme of the drift story.
- **Recommendation**: Confirm the emptied-CoreTeam-after-binding case resolves via FR-013's stale-route rule (agent still exists → routes stale) and add a one-line note, or a DS-3 variant.

#### [MIN-005] SC-001/SC-004 percentages ("100% of … route to …") are not tied to a defined sample size or harness

- **Lens**: Infeasibility (measurability)
- **Affected section**: SC-001, SC-004, SC-007.
- **Description**: "100% of `eu` inbound route to `ray`" and "0 cross-routes" are good targets but no sample count / duration / test harness is named, so "100%" is aspirational rather than measurable. SC-007 says "100% of sampled sessions" without defining the sample.
- **Recommendation**: Bind each percentage to the concrete test that proves it (e.g. "over the N inbound messages in `multi-instance-routing.spec.ts`") so the criterion is checkable.

#### [MIN-006] `normalizeChannelMap` currently `slog.Warn`s + drops; the "MUST NOT silently drop" phrasing is slightly off (it warns, not silently)

- **Lens**: Incorrectness (minor accuracy)
- **Affected section**: §5 Explicit Non-Behaviors ("must not **silently** drop a namespaced config key at load"); FR-016.
- **Description**: The current `normalizeChannelMap` (`config.go:1654-1668`) logs `slog.Warn("config: unknown channel type … — ignoring")` before `continue` — it drops but not *silently*. Minor, but the fix (match on effective `Type`) is correctly required by FR-016; only the word "silently" is imprecise.
- **Recommendation**: Drop "silently" or change to "must not drop a namespaced config key at load (it currently warns-and-drops)".

---

### Observations

#### [OBS-001] Consider stating the two-representations invariant as a config-repair rule

- **Lens**: Inoperability
- **Suggestion**: Given MAJ-004 (bound → `Identity`, unbound → wildcard binding), consider a `pkg/config` load-time repair that rejects/normalizes an instance carrying *both* a `WorkspaceID`/`Identity` and a stale wildcard binding, mirroring the existing multi-default repair. Prevents drift between the two stores over time.

#### [OBS-002] The critical-path lane (WS-B, 13-factory stamping) would benefit from the O-2 spike being a spec deliverable, not just an ADR next-step

- **Lens**: Incompleteness
- **Suggestion**: The ADR §9 spike (prove `msg.InstanceID` actually drives P0 end-to-end) is the single best de-risker for MAJ-002 and FR-020. Promote it into the TDD plan as an explicit integration checkpoint before the 13-factory fan-out, not just a holdout.

#### [OBS-003] `weixin.go` registers its factory outside `init.go` — call it out for the WS-B inventory

- **Lens**: Incompleteness
- **Suggestion**: 12 factories live in `*/init.go`; `weixin` registers in `weixin.go` (`pkg/channels/weixin/weixin.go:40`) and `googlechat` registers under the name `"google-chat"`. The spec's "13-factory" count is correct, but the WS-B task should enumerate these two irregular sites explicitly so a mechanical `init.go` sweep doesn't miss them.

#### [OBS-004] Consider whether `email` (mailbox) already models workspace binding and can inform the design

- **Lens**: Incompleteness
- **Suggestion**: `config.go:2251` already has a mailbox `WorkspaceID string` with a cap-1 "one mailbox per workspace" validator (`ValidateMailboxesCap1`). This is a near-identical binding pattern already in the codebase; the spec/ADR don't reference it. Reusing its validation idiom (and reconciling "one mailbox per workspace" vs. this feature's "many instances per workspace") could reduce design risk and surface an inconsistency early.

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-11 each have 2–3 acceptance scenarios. |
| Every acceptance scenario has BDD scenarios | PASS (mostly) | Well covered; US-10/AC-3 (rename=delete+create) has no BDD scenario, only the acceptance line. |
| Every BDD scenario has `Traces to:` reference | PASS | All scenarios carry `Traces to:`. |
| Every BDD scenario has a test in TDD plan | PARTIAL | Most map cleanly; the drift `+ alert` assertion (BDD drift outline) has no test that verifies the alert/counter (see MAJ-003). |
| Every FR appears in traceability matrix | PASS | FR-001..FR-027 all present in §8 matrix. |
| Every BDD scenario in traceability matrix | PARTIAL | The matrix maps FR→US→BDD→test but is FR-indexed; a few BDD scenarios (e.g. GET round-trip, unbound regression) are referenced only via tests, not as matrix rows. Acceptable for this format. |
| Test datasets cover boundaries/edges/errors | PARTIAL | Strong coverage, but DS-1 row 7 has a non-deterministic expected value and DS-2 row 7 asserts an undefined max-length (see MAJ-008); DS-3 omits the archived-workspace case (see MAJ-006). |
| Regression impact addressed | PASS | §7 names the exact suites to preserve (`route_explicit_priority_test.go`, handoff chat-scope test) and adds `TestResolveMessageRoute_Unbound_DefaultUnchanged`. Strong. |
| Success criteria are measurable | PARTIAL | Mostly measurable; SC-001/004/007 percentages lack a defined sample/harness (MIN-005); SC-004 depends on the non-existent drift event (MAJ-003). |

### Test Coverage Assessment

#### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Observability / alerting | No test verifies the `bound_instance_drift` counter or operator alert is emitted (infra undefined). | US-5/AC-1, SC-004, FR-027 |
| GET round-trip for bound instance | Only the PUT is BDD-covered; the GET reading `WorkspaceID`+`Identity` (not the wildcard binding) is untested. | MAJ-004, TDD #15 |
| Partial-cascade failure | Workspace-delete config-write-fails-after-file-delete path untested. | US-10/AC-1, MAJ-005 |
| Real inbound session-key collision | #12 tests `BuildAgentPeerSessionKey`, not `agentSessionKey`/`SessionID` (the path actually used). | US-9/AC-2, MAJ-002 |
| Archived-workspace route-time behaviour | No route-time test for a bound workspace being archived. | MAJ-006 |

#### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-1 (routing rejection) | Row 7 case-normalization outcome undecided | Lock case rule → deterministic 422 (or 200). |
| DS-2 (key grammar) | Max-length value undefined; delimiter assumed `:` | Define max length as an FR; parameterize delimiter until Gate 0 locks it. |
| DS-3 (drift triggers) | No "workspace archived" row | Add archived-workspace row (recommend: stale route). |
| DS-4 (InstanceID stamping) | Good; consider a row for a channel whose adapter has no per-instance concept yet | Confirm the legacy fallback row (#4) is the intended behaviour for un-migrated adapters. |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Inbound `InstanceID` stamping (adapters) | ok | ok | ok | ok | ok | ok | FR-021 correctly forbids sourcing `InstanceID` from message content; test #19 asserts it. Well handled. |
| `setChannelRouting` (membership validation) | ok | ok | risk | ok | ok | ok | FR-006 enforces membership server-side (good, EoP/tampering closed). **Repudiation**: config change to a channel's routing agent has no stated audit event — an operator silently re-binding a workspace's WhatsApp number to a different agent leaves no trail. Consider an audit entry. |
| Credential store (`channelCredKey` per-instance) | ok | ok | ok | risk | ok | ok | Per-`{id}` keying isolates secrets (FR-019). **Info-disclosure residual**: the delimiter-in-key safety (MAJ-008/NEW-3) is the open item — an illegal/collidable delimiter could let `whatsapp:eu` and a crafted key alias. Test #4 covers round-trip; add a collision test for adjacent keys. |
| Drift drop path | ok | ok | risk | ok | ok | ok | Correctly refuses global-default degrade (good, prevents unintended-agent EoP). **Repudiation/DoS**: an attacker who can delete/disable a bound agent can silently black-hole a channel (drop-and-alert) — the alert (MAJ-003) is the only mitigation and it's under-specified. Ensure the drop is auditable. |
| Workspace-delete cascade | ok | risk | risk | ok | ok | ok | Partial-failure (MAJ-005) can leave an orphaned binding (tampering-of-state via inconsistency); no audit of the cascade's channel-unbind step. |

**Legend**: risk = identified threat not fully mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **Is there a Save button, or does routing still auto-save?** The entire US-1/US-3 "block Save" model presumes a commit control the current UI lacks (MAJ-001). What is the interaction model for bound vs. unbound instances?
2. **What is the real session key for channel inbound?** Does `agentSessionKey`/`SessionID` become instance-scoped, or only `SessionKeyParams`? (MAJ-002) Which upstream code mints `msg.SessionID` for channel messages, and is it instance-aware?
3. **What is a `bound_instance_drift` "counter" and an "operator alert" concretely?** An audit event type? An in-memory counter on `/health`? Which UI surface renders the alert, and from what data source? (MAJ-003)
4. **When a bound instance is saved, does `getChannelRouting` read `Identity` or the wildcard binding?** How are the two persistence representations disambiguated per instance? (MAJ-004)
5. **What is the exact instance-key delimiter and max length, decided now (not at Gate 0)?** Every literal `whatsapp:eu` in the spec depends on this one-way door. (MAJ-008)
6. **At route time, does an *archived* (not deleted) workspace drop or route stale?** FR-013 only defines *agent* drift, never *workspace* state. (MAJ-006)
7. **Does the workspace-delete config-unbind run before or after the workspace-file removal, and what happens on partial failure?** (MAJ-005)
8. **Is routing-config change (re-binding an instance's agent) an audited action?** No repudiation trail is specified.
9. **Does the existing mailbox `WorkspaceID` binding (config.go:2251) conflict with or inform this design?** A workspace can own one mailbox but many channel instances — is that intentional and consistent? (OBS-004)

---

## Verdict Rationale

**REVISE.** The spec is architecturally sound and inherits a rigorously grilled ADR — there are no CRITICAL findings, the routing precedence and drift-drop mechanism are correctly specified, and the security-sensitive `InstanceID` provenance is properly required and tested. It is close to implementable. But three MAJOR findings would each cause real rework or incorrect behaviour if taskified as-is: **MAJ-001** (the "block Save" UX contradicts the actual auto-save UI, forcing an unscoped interaction-model change), **MAJ-002** (the session-key fix targets a code path the inbound router doesn't use, so FR-023/US-9 would false-green while transcripts still collide), and **MAJ-004** (the routing handlers are a data-model rewrite from wildcard-bindings to `Channels.Identity`, understated as "modify", with an unspecified GET round-trip). **MAJ-003** (drift observability/alert infrastructure doesn't exist) makes SC-004 and the "+ alert" assertions untestable. **MAJ-008** (instance-key grammar deferred to Gate 0 while datasets already assert specific outcomes) leaves the test datasets non-deterministic and every `whatsapp:eu` literal contingent on an undecided delimiter.

None of these change the chosen architecture (Option A holds); they are spec-accuracy and scoping gaps that must be closed so `/taskify` produces correct, implementable units rather than tasks that discover the mismatch mid-wave.

### Recommended Next Actions

- [ ] Resolve MAJ-001: add the Save-button/commit-model story and reconcile bound vs. unbound auto-save.
- [ ] Resolve MAJ-002: re-target the session-key fix to `agentSessionKey`/`SessionID`; re-point TDD #12.
- [ ] Resolve MAJ-003: specify the drift-event schema (audit event vs. counter vs. `/health`) and the alert surface; make SC-004 name the observable artifact.
- [ ] Resolve MAJ-004: re-label both routing handlers "rewrite"; specify the GET path and the mutually-exclusive two-representation rule; add a bound-GET round-trip BDD.
- [ ] Resolve MAJ-005: define cascade ordering (config unbind before file delete) + partial-failure semantics + what "unbind" clears.
- [ ] Resolve MAJ-006: add a route-time rule + DS-3 row + BDD for archived-workspace bound routing.
- [ ] Resolve MAJ-007: decide FR-024/A-2 in the spec (or make US-11/AC-2 design-agnostic); correct the enum count to 15/14.
- [ ] Resolve MAJ-008: lock the instance-key grammar (delimiter checked against cred-key charset now, max length as an FR, case rule) in the spec; parameterize/replace `whatsapp:eu` literals until locked.
- [ ] Address MINOR/OBS items (worker 400→422 note, `SmartSelect` disabled-prop check, sample-size binding for SC percentages, `weixin.go`/`google-chat` irregular factory sites, mailbox-binding precedent).

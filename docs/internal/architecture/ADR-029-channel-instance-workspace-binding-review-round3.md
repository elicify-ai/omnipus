# Adversarial Review: ADR-029 — Channel-Instance ↔ Workspace Binding (Round 3)

**Spec reviewed**: `docs/internal/architecture/ADR-029-channel-instance-workspace-binding.md`
**Review date**: 2026-07-02
**Verdict**: REVISE

## Executive Summary

Round 3 confirms the CRITICALs from rounds 1–2 are genuinely closed — the drift-enforcement mechanism (NEW-1), precedence (C-3), and endpoint-enum (C-1) are all correctly grounded against source. However this round surfaces a **factual error in a load-bearing scope estimate** (the "19 registered channel factories" claim is wrong — there are 13 factory registrations and the enum has 16 members; the number drives the WS-B critical-path sizing), **two silently-omitted multi-instance blockers** the fact-gathering missed (`normalizeChannelMap` drops non-type keys; the `webchat`/`email` enum members are not factories), and several MAJOR ambiguities/gaps in the drift mechanism and instance-lifecycle that would produce divergent implementations. No CRITICAL findings remain; the MAJORs must be resolved before `/plan-spec`.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 5 |
| OBSERVATION | 4 |
| **Total** | **15** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] The "19 registered channel factories" count is wrong — WS-B critical-path scope is mis-sized

- **Lens**: Incorrectness
- **Affected section**: §1 current-state facts (`[FACT: 19 `RegisterFactory(` calls in pkg/channels]`), NEW-4 in §8, WS-B in §9, "Critical path" in §9.
- **Description**: The ADR's round-2 "correction" states there are **19** registered channel factories (up from the draft's "11"/"~13"). The actual count is **13** `RegisterFactory` call sites: `grep -rn "RegisterFactory(" --include="*.go" . | grep -v _test | grep -v "func RegisterFactory"` returns exactly 13 — one per package across dingtalk, google-chat, line, whatsapp_native, telegram, irc, matrix, slack, discord, feishu, wecom, qq, weixin (note weixin registers in `weixin.go:40`, not an `init.go`; there are only 12 `init.go` files). The `ChannelId` **enum** has **16** members (`webchat`, `email`, `google-chat` + the 13 factory types) and `validChannelIDs` allows **14** of them. The "19" appears in no artifact I could reproduce.
- **Impact**: WS-B is declared the critical path ("the factory + inbound-stamping refactor across the type-keyed subset of the 19 registered factories is the longest single lane"). Sizing the longest lane against a phantom count of 19 mis-estimates the wave. Worse, the ADR itself admits the two sub-counts differ ("how many hardcode a type key … vs. how many *inbound* sites must stamp `InstanceID` … are enumerated in the Gate-0/spike inventory") but then never produces the inventory, leaving the single most load-bearing scope number both wrong and unexplained.
- **Recommendation**: Replace "19" with the verified count (13 factory registrations; enum has 16 members, 14 API-valid). Explicitly note that `webchat` and `email` are enum members but **not** factory-registered channels (webchat is the built-in WS surface at `manager.go:133,389,412`; email routes via a different path — no `RegisterFactory` for it), so they are out of WS-B's factory-plumbing scope. Produce the promised per-category inventory (config-selection sites vs. inbound-stamping sites) *in this ADR* or in the Gate-0 deliverable, not as a forward-reference.

#### [MAJ-002] `normalizeChannelMap` silently *drops* unknown keys — an unlisted multi-instance blocker

- **Lens**: Incompleteness
- **Affected section**: §1 (`normalizeChannelMap backfills Type from key at config.go:1663`), §3 Gap #5, WS-B scope.
- **Description**: The ADR characterizes `normalizeChannelMap` only as "backfills Type from key" and "must stop assuming key==type". But the code comment at `config.go:3166` states the cap-check runs on the RAW map "BEFORE normalizeChannelMap **drops unknown keys**". If `normalizeChannelMap` discards map entries whose key is not a recognized channel type, then a config with key `whatsapp:eu` (the very grammar the ADR proposes in M-4) would be **silently deleted at load** unless `normalizeChannelMap` is changed. This is a hard blocker for multi-instance, and it is not in the enumerated WS-B scope (which lists only "replace `ValidateChannelsCap1`/`normalizeChannelMap`'s key==type assumption" — dropping is a different behavior than the key==type inference).
- **Impact**: If WS-B changes only the `Type`-inference logic and not the key-drop logic, every non-type-keyed instance vanishes at config load with no error — the operator's "whatsapp:eu" instance disappears on restart. A "fix the key==type assumption" task scoped without reading the drop behavior will miss it.
- **Recommendation**: Read `normalizeChannelMap` (config.go:1654-1668) in full and enumerate its two distinct behaviors (Type backfill AND unknown-key drop). State explicitly in WS-B that the key-drop must be removed/relaxed for the instance-key grammar, and add a BDD/test scenario: "config with `<type>:<slug>` key survives a load→save→reload round-trip with Type intact."

#### [MAJ-003] Drift trigger "deleted or becomes a worker" is under-specified vs. the actual fallback surface

- **Lens**: Incompleteness
- **Affected section**: FR-6a, §6/D3 drift mechanism (steps 1–4), §7 membership drift.
- **Description**: The ADR bounds the drift-drop trigger to "when that agent is **deleted or becomes a worker**." But `pickAgentID` (route.go:256-292, verified) falls back to default in **two** distinct branches: (a) the agent resolves but `!a.IsChatTarget()` (worker), and (b) the agent ID is **not in the agent list at all** (deleted/renamed). The ADR's step-2 says "when … the P0 identity agent is unresolvable (deleted / worker)". However, Priority-0 in `route.go:87-100` calls `choose(input.Identity.ID, "identity.agent")` which routes through `pickAgentID`. There is a **third** state the ADR never addresses: an agent that is *disabled* (`Enabled==false`) but still present and still a chat-target. `IsChatTarget()`/`IsWorker()` semantics for a disabled agent are not stated. A disabled bound agent may pass the worker check, route "successfully" to a dead agent, and never trigger the drop path — the message goes to an agent that cannot answer.
- **Impact**: A bound instance whose agent is disabled (not deleted) may neither drop-and-alert nor route to a live agent — it silently routes to a disabled agent, which is exactly the "3am no-reply" failure the drift policy claims to prevent. The BDD scenario list (§9) only covers "agent leaves CoreTeam" and "deleted agent", not "disabled agent".
- **Recommendation**: Enumerate the full set of "unresolvable / non-answerable" states for a bound identity agent: {deleted, renamed, became-worker, **disabled**, archived}. Define which trigger drop-and-alert vs. route-stale-but-functional. Verify `IsChatTarget()` behavior for a disabled agent (`grep IsChatTarget pkg/config`) and state it. Add a "bound + disabled agent" BDD scenario.

#### [MAJ-004] `matched_by` / drift-drop is described but the drop-sentinel contract is not defined

- **Lens**: Ambiguity
- **Affected section**: §6/D3 step 1 ("Propagate a `Drop`/`MatchedBy:"bound.drift.drop"` marker onto `ResolvedRoute`"), step 2 ("return the **drop sentinel** (empty `AgentID` + drop marker)"), §7 observability.
- **Description**: The mechanism hinges on a "drop sentinel" and a "`MatchedBy:"bound.drift.drop"` marker" carried on `ResolvedRoute`, and on a new `RouteInput.BoundInstance bool`. But `ResolvedRoute` (verified: `pkg/routing` returns `routing.ResolvedRoute{AgentID, SessionKey}` — no `MatchedBy` or `Drop` field is present in the struct as used at loop.go:4518/4562/4595). `pickAgentID` returns a **plain string** agent ID, not a `ResolvedRoute` — so "return the drop sentinel (empty `AgentID` + drop marker)" is not expressible at that call site without changing `pickAgentID`'s signature and `choose`'s. The ADR treats `MatchedBy` and a `Drop` field as if they exist; they do not. `matched_by` is referenced ("Extend the existing `matched_by` route field") but no such field exists on the wire route today.
- **Impact**: An implementer following D3 literally will discover mid-wave that `pickAgentID` cannot return a marker (it returns `string`), forcing either a signature change that ripples through `choose`/`ResolveRoute` (the "zero routing-cascade change" claim breaks) or a side-channel. This is the exact class of "mechanism unenforceable as placed" that round-2's NEW-1 was supposed to fix — it fixed the *placement* but not the *carrier*.
- **Recommendation**: Specify the concrete carrier. Either (a) add fields to `ResolvedRoute` (`MatchedBy string`, `Drop bool`) and change `pickAgentID` to return a richer type — and then honestly re-score the "zero cascade change" claim, since this touches the shared return type every priority uses; or (b) keep `pickAgentID` string-returning and carry the bound-drift signal via a sentinel agent-ID + a second return value, spelled out exactly. State which, with the actual struct diff.

#### [MAJ-005] Instance lifecycle (create / rename / delete / disable) is entirely absent

- **Lens**: Incompleteness
- **Affected section**: FR-7, §6/D2, WS-F ("'Add instance' flow"), §9.
- **Description**: The ADR specifies how an instance *binds* and *routes*, and that N>1 instances are allowed, but never specifies the **lifecycle**: How is an instance created (the ADR mentions "`POST /channels`" once as a Gate-0 option but never decides it)? Can an instance be **deleted**, and what happens to its credential refs, its WhatsApp store dir (`.../whatsapp/<instanceID>/store.db`), its bound sessions, and its `workspace_id`-tagged history? Can it be **renamed** (the M-4 key is a one-way door — is rename even allowed, and if so does it re-key credentials + store dir)? What happens to a bound instance when its **workspace is deleted/archived** (D3 covers unknown workspace at *write* time with 404, but not deletion of a workspace that a live bound instance already points at)?
- **Impact**: WS-F ("Add instance UI") and WS-C (per-instance state) cannot be built without these decisions. Delete-without-cleanup leaks credential refs and orphans SQLite stores on disk. Workspace-deletion with a live bound instance re-opens the exact "bound instance reaches global default" failure the ADR spent two rounds closing — a bound instance whose workspace no longer exists has an undefined route.
- **Recommendation**: Add a §on instance lifecycle: create (decide `POST /channels` vs. enum-open, the Gate-0 item), delete (cascade: remove cred refs via `channelCredKey`, remove store dir, decide session-history disposition), rename (allow or forbid; if forbid, say so — simplest), and **workspace deletion with live bound instances** (block deletion? cascade-unbind? this is a drift trigger and must feed the same drop-and-alert path). Add the workspace-deletion case to the drift BDD set.

#### [MAJ-006] "Attach `workspace_id` at session creation" (FR-9) ignores that the session key has no instance dimension

- **Lens**: Incorrectness
- **Affected section**: FR-9, §1 (`session key = agent + channel(TYPE) + chat`, `agentSessionKey ~4624-4628`), WS-D.
- **Description**: FR-9/WS-D say to populate `Session.workspace_id` "at channel session creation, inherited from the bound instance." But the ADR's own §1 fact establishes the session key is `agent + channel(**TYPE**) + chat` — it has **no instance dimension**. With N>1 instances of the same type (e.g. `whatsapp:eu` and `whatsapp:us`) both bound to *different* workspaces but talking to the *same* peer/chat-id, the two produce the **same session key** (same agent could differ, but the chat-id namespace per channel-type is not guaranteed disjoint across instances). The session would then get a `workspace_id` from whichever instance wrote last — a cross-workspace data-mixing bug. The ADR notes `agentSessionKey` uses TYPE but never reconciles this with per-instance `workspace_id` attachment.
- **Impact**: Two instances of the same channel type bound to different workspaces can collide on one session record, and `workspace_id` becomes non-deterministic — a workspace-isolation breach (a v0.3 core invariant). This directly undercuts the "session→workspace linkage" side-benefit the ADR sells as free.
- **Recommendation**: Decide whether the session key must gain an instance dimension for multi-instance correctness (likely yes), and state it in WS-D/WS-B. If the key stays type-scoped, prove that two same-type instances cannot share a chat-id namespace (they can't for WhatsApp — different numbers — but state the guarantee per channel). Add a BDD: "two same-type instances, different workspaces, overlapping chat-id → distinct sessions with correct `workspace_id`."

---

### MINOR Findings

#### [MIN-001] `email` and `webchat` enum members conflated with factories throughout

- **Lens**: Ambiguity
- **Affected section**: §1 factory discussion, C-1, WS-B.
- **Description**: The ADR reasons about "the ~13 channels" and "19 factories" as if `ChannelId` enum membership == factory. `email` (enum) has no `RegisterFactory`; `webchat` is deliberately excluded from `validChannelIDs` ("always enabled and intentionally excluded", rest.go:6305). Multi-instance semantics for `email` and `webchat` are undefined — can there be N email channels? N webchats? The ADR never says.
- **Recommendation**: State that `webchat` and `email` are excluded from multi-instance/binding scope (or explicitly in scope with their own plumbing). Distinguish "enum member" from "factory-registered channel" wherever the count matters.

#### [MIN-002] Credential-delimiter risk (NEW-3) tested only for `:` — the chosen delimiter is undecided

- **Lens**: Ambiguity
- **Affected section**: §3 rows 12–13, §7 per-instance credential isolation, Gate-0.
- **Description**: The ADR flags that `:` in `channel_whatsapp:eu_token` may be an illegal cred-store key char and says "verify … pick a safe one if not." But it never checks the actual charset (`credentials.Store` key validation) nor the *other* embedding — the `_` delimiter in `channelCredKey` itself (`channel_<id>_<field>`). If the instance slug may contain `_` (M-4 says `[a-z0-9-]`, which excludes `_`, but this is stated as a candidate, not locked), a slug with `_` would make the `channel_<id>_<field>` parse ambiguous. The verification is deferred to Gate-0 but the candidate grammar and the key format interact and neither is locked.
- **Recommendation**: In Gate-0, actually read the credential-store key charset and lock the M-4 grammar against BOTH the enum/route lowercasing AND the `channelCredKey` `_`-delimited format. Prefer a delimiter that is neither `:` nor `_` (e.g. `-`) to avoid both hazards.

#### [MIN-003] "Empty CoreTeam → disabled Save" ignores worker-only workspaces already handled elsewhere

- **Lens**: Inconsistency
- **Affected section**: §6/D3 "Empty CoreTeam (M-1)".
- **Description**: The empty-state message is "add a member to this workspace first," but the eligible set is *non-worker* CoreTeam members. A workspace whose CoreTeam contains only workers is functionally empty for this picker but not literally empty — the message would mislead ("I did add members"). The distinction between "no members" and "no *eligible* (non-worker) members" is not made in the UX copy.
- **Recommendation**: Copy should read "add a chat-capable agent to this workspace" (or similar), and the empty-check must be on the filtered (non-worker) list, not raw CoreTeam length.

#### [MIN-004] `validChannelIDs` also gates a second call site (rest.go:6479) not mentioned

- **Lens**: Incompleteness
- **Affected section**: C-1, WS-A "open the `ChannelId` endpoint enum".
- **Description**: The ADR cites only `rest.go:5996-5999` as the enum gate. There is a **second** gate at `rest.go:6479` (`if !validChannelIDs[gen.ChannelId(channelID)]`). WS-A's "open the enum" must handle both, plus `channelWildcardIdx` and the drift-guard comment at rest.go:6304-6310 that ties `validChannelIDs` to the generated enum constants by build.
- **Recommendation**: Enumerate both gate sites and the build-time drift-guard in WS-A. Note that opening the enum to a free string removes the compile-time drift-guard's protection — decide the replacement invariant.

#### [MIN-005] Confidence blocks claim "zero routing-cascade change" but D3 adds a `RouteInput.BoundInstance` field + branch in `pickAgentID`

- **Lens**: Inconsistency
- **Affected section**: §6 primary confidence ("No routing-cascade change"), §8 ("zero routing-*cascade* change"), vs. §6/D3 steps 1–2 (new `RouteInput.BoundInstance`, new branch in `pickAgentID`).
- **Description**: "Zero routing-cascade change" is repeated as the load-bearing justification for Option A over B. But D3 adds a field to `RouteInput`, a conditional branch in `pickAgentID` (which is squarely in the routing package's hot path), and a new drop-sentinel return. That *is* a routing-cascade change — smaller than B's, but not zero. The hedged phrasing "cascade" vs. "hot path" is doing a lot of work to preserve a claim that the mechanism itself contradicts.
- **Recommendation**: Restate honestly: "Option A changes the routing package minimally — no new priority tier or match dimension (unlike B) — but does add a `BoundInstance` guard on the two default-fallback branches." Keep the A-over-B argument (still valid on *magnitude*), drop the "zero" absolute.

---

### Observations

#### [OBS-001] The ADR is exceptionally well-grounded; most `[FACT]` citations verified exactly

- **Lens**: Incorrectness (positive control)
- **Affected section**: throughout.
- **Suggestion**: Priority-0 (route.go:87-100), the two fallback sites (route.go:256-292 + loop.go:4583-4585), C-3 precedence (loop.go:4485-4596), `channelCredKey` (rest.go:2985-2987), `Session.yaml:85` wire field, the C-1 enum gate, `isChannelWildcardRaw`, and `ValidateChannelsCap1` counting-by-effective-type all verified as cited. The MAJORs above are gaps/ambiguities, not fabrications — the citation discipline is strong and should be preserved. Only the factory *count* (MAJ-001) and the `normalizeChannelMap` drop behavior (MAJ-002) were mis-stated.

#### [OBS-002] Consider whether `Identity`-reuse (O-1) should just be a new field now

- **Lens**: Overcomplexity (inverted)
- **Affected section**: §5 Option A, O-1.
- **Suggestion**: The ADR reuses `Identity{kind:agent}` to mean "routes TO" while its documented meaning is "acts AS", and stores `WorkspaceID` separately as the durable binding. This dual-meaning is a latent trap (a future reader will conflate them). Given v0.3 is fresh-build with no back-compat, introducing the `WorkspaceBinding{workspace_id, agent_id}` sub-struct *now* (rather than "if a future need separates them") may be cheaper than the semantic debt — the ADR already stores the pair independently. Weigh it.

#### [OBS-003] The `matched_by`/observability NFR should specify the metric name and type

- **Lens**: Inoperability
- **Affected section**: §7 observability (M-8).
- **Suggestion**: "Add a counter for drift drop-and-alert events" and "a metric/log distinguishing the two" are the right instinct but unnamed. Since Omnipus has no external metrics backend (file-based), specify the concrete carrier: is `bound_instance_drift` a structured-log field, an audit-log event type, or an in-memory counter surfaced via an endpoint? On-call at 3am needs to know where to look — the runbook line (d) helps but points at config, not at the counter.

#### [OBS-004] "5 workspaces = 5 WhatsApp numbers" — confirm WhatsApp multi-device / whatsmeow supports N stores in one process

- **Lens**: Infeasibility
- **Affected section**: FR-7, §7 WhatsApp store collision.
- **Suggestion**: The store-path fix (`.../whatsapp/<instanceID>/store.db`) assumes whatsmeow can run N independent device sessions in one process with N SQLite stores. This is almost certainly fine (whatsmeow is per-`Client`), but it is an untested assumption for the headline use case. The O-2 spike should wire **two** WhatsApp instances, not one, to prove N-device coexistence — a single-instance spike proves nothing about the collision the ADR is most worried about.

---

## Structural Integrity (Variant B — Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PASS | FR-1–FR-11 map to the operator's verbatim requirement; §9 lists BDD scenarios to write |
| Cross-references are consistent | FAIL | The "19 factories" (§1/NEW-4/§9) is internally repeated but wrong (MAJ-001); "matched_by field" (§7) references a field that doesn't exist (MAJ-004) |
| Scope boundaries are explicit | PARTIAL | In-scope FR-9 depth is bounded well; but instance lifecycle (create/delete/rename) and email/webchat multi-instance are undefined (MAJ-005, MIN-001) |
| Success criteria are measurable | PASS | §9 BDD list is concrete and testable; the drift "reject-not-default" assertion is measurable |
| Error/failure scenarios addressed | PARTIAL | Drift (deleted/worker) covered; disabled-agent and workspace-deletion drift NOT (MAJ-003, MAJ-005) |
| Dependencies between requirements identified | PASS | Gate-0 → 6-workstream fan-out with collision/ordering analysis is thorough and correct |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Config round-trip | `<type>:<slug>` key must survive load→save→reload (normalizeChannelMap drop) | MAJ-002 |
| Session isolation | Two same-type instances, different workspaces, overlapping chat-id → distinct sessions + correct `workspace_id` | MAJ-006 |
| Drift — disabled agent | Bound agent disabled (not deleted) → drop-and-alert, not silent route to dead agent | MAJ-003 |
| Drift — workspace deletion | Workspace deleted while a live bound instance points at it → defined behavior, not global default | MAJ-005 |
| Multi-device coexistence | TWO WhatsApp instances, N SQLite stores, one process | OBS-004 |
| Credential delimiter | Instance key with the chosen delimiter yields a legal, unambiguous `channelCredKey` | MIN-002 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Instance-ID grammar | max length, delimiter collision with `channelCredKey` `_`, uppercase input (lowercased by inboundInstanceID) | Lock charset/case/delimiter/length in Gate-0 against all three consumers |
| CoreTeam eligibility | worker-only workspace (non-empty but no eligible members) | Empty-state on filtered list, not raw CoreTeam (MIN-003) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Inbound `msg.InstanceID` stamping (FR-10) | risk-addressed | risk-addressed | ok | ok | ok | risk-addressed | ADR correctly mandates trusted-adapter source, never payload; reviewer-asserted per adapter. Sound. |
| `setChannelRouting` membership check | ok | risk-addressed | ok | ok | ok | risk-addressed | Server-side `∈ CoreTeam` validation (client filter UX-only) correctly specified — closes tamper/EoP |
| Per-instance credential refs | ok | ok | ok | risk | ok | ok | Isolation "nearly free" via `channel_<id>_<field>`; residual: delimiter safety unverified (MIN-002) |
| Workspace deletion w/ live bound instance | ok | ok | ok | ok | risk | risk | Undefined behavior (MAJ-005) — a deleted workspace's bound instance has no defined route; potential cross-workspace leak on re-bind |
| Session `workspace_id` attribution | ok | risk | ok | risk | ok | ok | Same-type multi-instance can collide on type-scoped session key → non-deterministic workspace tag (MAJ-006) |

**Legend**: risk = identified threat not mitigated in spec; risk-addressed = threat named and mitigated; ok = not applicable / adequately covered.

---

## Unasked Questions

1. **What is the actual channel-factory count and per-category inventory?** The ADR forward-references a Gate-0 inventory for the single most important scope number (WS-B critical path) but never produces it. (MAJ-001)
2. **Does `normalizeChannelMap` drop the new `<type>:<slug>` keys at load?** If yes (the code comment says it drops unknown keys), every multi-instance config silently vanishes on restart. (MAJ-002)
3. **What is the route for a bound instance whose agent is *disabled* (not deleted, not a worker)?** The drift trigger set omits this state. (MAJ-003)
4. **What concrete struct carries the drop sentinel?** `ResolvedRoute` has no `MatchedBy`/`Drop` field and `pickAgentID` returns a string — the mechanism's carrier is unspecified. (MAJ-004)
5. **What is an instance's full lifecycle** — create endpoint (POST vs enum-open, still undecided), delete (cred-ref + store-dir + session cleanup), rename (allowed?), and workspace-deletion cascade? (MAJ-005)
6. **Does the type-scoped session key stay correct with N same-type instances in different workspaces?** (MAJ-006)
7. **Are `email` and `webchat` in or out of multi-instance scope?** They are enum members but not factories. (MIN-001)
8. **Which delimiter is locked**, and is it legal in BOTH the credential-store key charset AND unambiguous against `channelCredKey`'s `_` delimiter? (MIN-002)

---

## Verdict Rationale

**REVISE.** The round-1/round-2 CRITICALs are genuinely closed — I re-verified the drift-enforcement placement (both fallback sites), the C-3 precedence chain, and the C-1 enum gate against source, and they hold. No CRITICAL remains. But this round surfaces one factual error in a load-bearing scope estimate (MAJ-001, the "19 factories" that sizes the critical path — the real count is 13), a silently-omitted hard blocker (MAJ-002, `normalizeChannelMap` dropping the very keys the ADR proposes), and an unspecified carrier for the drift mechanism the last round introduced (MAJ-004, `ResolvedRoute`/`pickAgentID` cannot carry the "drop marker" as written). Together with the missing instance-lifecycle (MAJ-005) and the session-key/workspace collision (MAJ-006), these would produce divergent, buggy implementations if handed to `/plan-spec` as-is. The decision (Option A) is still correct and does not change — these are execution-detail and scope-accuracy defects, not a redirection.

### Recommended Next Actions

- [ ] Correct the factory count to 13 and produce the promised per-category inventory in the ADR (MAJ-001)
- [ ] Read `normalizeChannelMap` fully; add unknown-key-drop removal to WS-B scope + a round-trip test (MAJ-002)
- [ ] Enumerate the full drift-trigger state set incl. disabled/archived agent; add BDD scenarios (MAJ-003)
- [ ] Specify the concrete drop-sentinel carrier (struct diff to `ResolvedRoute`/`pickAgentID`); re-score the "zero cascade change" claim honestly (MAJ-004, MIN-005)
- [ ] Add an instance-lifecycle section (create/delete/rename/workspace-deletion cascade) (MAJ-005)
- [ ] Resolve the type-scoped session-key vs. per-instance `workspace_id` collision (MAJ-006)
- [ ] Scope `email`/`webchat` explicitly in or out of multi-instance (MIN-001); lock the delimiter against all three consumers (MIN-002)

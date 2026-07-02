# Grill-Spec Review (ROUND 2) — ADR-029: Channel-Instance ↔ Workspace Binding

- **Reviewed document:** `docs/internal/architecture/ADR-029-channel-instance-workspace-binding.md` (revised after round 1)
- **Review date:** 2026-07-02
- **Input classification:** structured-spec mode (FR/NFR/Gap/WS IDs, decision criteria, option analysis; ADR prose, no BDD/traceability matrix)
- **Reviewer stance:** adversarial, read-only. Assumed to cause a 3am incident until proven otherwise.
- **Supersedes:** the round-1 review (this file previously held it). Round-1 raised 3 CRITICAL + 8 MAJOR + 5 MINOR + 3 OBS; the ADR folded all of them in. This round verifies those closures against source and hunts for residual/new flaws.

---

## 1. Executive Summary

The three round-1 CRITICALs are **genuinely closed as requirements** — C-1 → FR-11 (endpoint enum), C-2 → FR-10 (inbound `InstanceID` stamping), C-3 → FR-6a + explicit precedence in §6/D3 — and all are now in-scope with Gate-0 line items. The eight MAJORs are addressed. Grounding remains strong: I re-spot-checked ~20 `[FACT]` citations against source (route.go Priority-0/pickAgentID/default cascade, rest.go setChannelRouting + validChannelIDs + channelCredKey, loop.go resolveMessageRoute/inboundInstanceID, config.go ChannelInstanceConfig/normalizeChannelMap/ValidateChannelsCap1, workspace CoreTeam/MemberConfigs, ChannelId enum) — **all accurate**.

But the revision introduced/left **three substantive problems that block a clean spec hand-off**, plus a factual inconsistency and several second-order gaps:

1. **The drift-interception mechanism (FR-6a) is placed where it cannot see the signal it needs.** The ADR assigns "drift drop-and-alert" to WS-A in `route.go`, and asserts "P7 is unreachable for a bound instance … the only way to miss P0 is drift, which is intercepted." But `pickAgentID(agentID, matchedBy string)` — the single shared chokepoint every priority (including P0) funnels through — receives only two strings; it **cannot distinguish** a bound-instance P0 route from an ordinary binding route, so it cannot apply a bound-only drop-and-alert without new plumbing the ADR does not name. Worse, the two higher-precedence paths the ADR *preserves* (explicit `agent_id`, handoff pin) return a `ResolvedRoute` **before `ResolveRoute`/`pickAgentID` runs at all** (loop.go:4485-4527, 4529-4569) — so for those paths there is no P7 to suppress and no drift hook to fire. The invariant is asserted, not demonstrated. (NEW-1, CRITICAL.)

2. **Gap #11 / M-3 is stale: `Session.workspace_id` is already decided in the contract.** The ADR still frames "Is `Session.workspace_id` a wire field?" as an open Gate-0 question. But `contracts/components/schemas/Session.yaml` **already defines `workspace_id`** ("Associated workspace ID (optional, future v0.3 feature)") and the SPA already reads that wire Session (`WireSessionSchema`, api.ts:83). The ADR also conflates two different `Session` types — the internal transcript struct (`pkg/session/manager.go`, no workspace field) and the SPA wire Session (`Session.yaml`, has `workspace_id`) — without noting they differ. FR-9/WS-D is scoped against the wrong artifact. (NEW-2, MAJOR.)

3. **The instance-key grammar (M-4) collides with the credential-ref key charset the ADR relies on for M-5.** M-5 is actually *closer to free than the ADR claims* — `channelCredKey(channelID, field) = "channel_"+channelID+"_"+field` (rest.go:2985) already namespaces refs by the route `{id}`, so once FR-11 makes `{id}` the instance ID, refs auto-namespace (same "reuse the primitive" logic as Priority-0). **But** M-4's chosen grammar uses `:` as the delimiter (`whatsapp:eu`), which would produce credential-store keys like `channel_whatsapp:eu_token`. Whether `:` is a legal credential-store key char is unverified and unflagged; if not, M-4's "one-way-door" grammar choice breaks M-5 on day one. (NEW-3, MAJOR.)

- **Findings:** 1 CRITICAL, 4 MAJOR, 4 MINOR, 3 OBSERVATION (round-1 items not re-listed unless residual).
- **Verdict: REVISE.** No data-loss/security landmine (WhatsApp store + cred isolation are handled), so not a BLOCK. But NEW-1 is a correctness gap in the ADR's *central* claim (a bound instance cannot silently hit the global default), and it must be resolved before `/plan-spec`, or the spec will encode an unenforceable invariant.

---

## 2. Findings Table

| ID | Sev | Lens | Section | Description | Recommended fix |
|----|-----|------|---------|-------------|-----------------|
| **NEW-1** | CRITICAL | Infeasibility / Incorrectness | §2 FR-6a, §6 D3, §7 drift, §8 | The ADR's load-bearing invariant — "a bound instance can never silently reach P7 (global default); drift is intercepted with drop-and-alert" — is **asserted, not enforceable as scoped**. Three grounded facts break it: (a) **every** priority, including P0 `identity.agent`, routes through the shared `choose → pickAgentID(agentID, matchedBy)` chokepoint (route.go:66-67, 87-100), and `pickAgentID` degrades an unresolvable/worker agent to `resolveDefaultAgentID()` — the global default (route.go:255-293) — **for all callers indiscriminately**; it has no "is this a bound instance?" signal. (b) The precedence signals the ADR *preserves* (explicit `agent_id` at loop.go:4485-4527; handoff pin at 4529-4569) **return before `ResolveRoute` runs**, so their drift/worker degradation is handled by *different* code (4494, 4544) that also has no bound-instance awareness and no P7 for the ADR to suppress. (c) WS-A is told to add "drift drop-and-alert … in `route.go`" but `route.go` cannot tell a bound P0 route from an ordinary P6 channel-wildcard route without a new input flag. | Specify the enforcement mechanism concretely: (1) thread a "bound" signal into the resolver — e.g. `RouteInput.BoundInstance bool` (set when `resolveInboundIdentity` returns a kind=agent identity sourced from a `WorkspaceID`-carrying instance), and have `pickAgentID`/`choose` emit drop-and-alert **instead of** `resolveDefaultAgentID()` **only when that flag is set**; (2) state where the drop happens (before `choose` builds a `ResolvedRoute`? a sentinel `AgentID==""` the caller treats as drop?) and how the caller (`resolveMessageRoute`, loop.go:4571-4586) surfaces it (it currently falls back to `registry.GetDefaultAgent()` at 4585 — the *exact* forbidden path); (3) prove FR-6a for the two early-return paths too, or state they're out of the bound-instance guarantee. Add a BDD: "bound instance, member agent removed from CoreTeam, no handoff/explicit → message is dropped+alerted, NOT delivered to the global default." |
| **NEW-2** | MAJOR | Inconsistency / Incorrectness | §2 FR-9, §3 Gap #11 (M-3), §9 Gate 0 & WS-D | Gap #11 still asks "Does the SPA consume session workspace_id? If yes → Gate 0 contract item," and Gate 0 lists `Session.workspace_id` as a *conditional* ("in Gate 0 iff the SPA reads it"). **It's already decided in the committed contract:** `contracts/components/schemas/Session.yaml` defines `workspace_id` ("Associated workspace ID (optional, future v0.3 feature)"), consumed by the SPA via `WireSessionSchema` (api.ts:83, `fetchSessions`). The ADR also treats "the Session struct" as one thing while there are **two**: the internal transcript container `pkg/session/manager.go:18-24` `{Key,Messages,Summary,Created,Updated}` (no workspace field, the ADR's cited FACT) and the SPA wire `Session` (`Session.yaml`, has `workspace_id`). FR-9/WS-D is scoped against the internal struct, but the wire field already exists. | Resolve Gap #11: the wire contract already carries `workspace_id`, so FR-9's contract work is *populating an existing field*, not adding one — remove the "iff SPA reads it" conditional. Distinguish the two Session types explicitly in §1 facts and WS-D (transcript struct vs. `UnifiedMeta`/`SessionMeta` wire shape). State which layer actually needs the field set at creation (the metadata that feeds the wire Session, not necessarily `manager.go`'s struct). |
| **NEW-3** | MAJOR | Incompleteness / Insecurity | §3 Gap #12 (M-4) / Gap #13 (M-5), §7 | M-5 over-scopes the credential work: `channelCredKey(channelID, field)` (rest.go:2985) is `"channel_"+channelID+"_"+field`, keyed on the route `{id}`. Once FR-11 makes `{id}` the instance ID, refs are per-instance **for free** (parallel to the Priority-0 reuse argument) — the ADR should claim this saving, not design a fresh "namespace + collision guard." **However**, the *unflagged* real risk is the reverse interaction: M-4 picks `:` as the key delimiter (`whatsapp:eu`), which flows verbatim into `channel_whatsapp:eu_token` as a credential-store key. Whether the credential store accepts `:` in a key (charset / on-disk representation) is **unverified**. If it does not, M-4's one-way-door grammar silently breaks M-5. | (1) Re-scope M-5: state that per-instance cred isolation is *inherited from `channelCredKey` once `{id}` is the instance ID* (cite rest.go:2985), reducing WS-C to "verify + test," not "design." (2) Add to Gate-0 the **credential-store key-charset check** for the chosen delimiter: verify the store accepts the M-4 grammar in a `<field>_ref` key, or pick a delimiter that is safe across the config map key, `inboundInstanceID` lowercasing, the `ChannelId` enum/pattern, AND the credential key. This is the concrete "trace the grammar through ref-key derivation" the ADR promises but does not close. |
| **NEW-4** | MAJOR | Inconsistency | §1 blast radius, §6 D2, §9 WS-B | The factory count is internally inconsistent: §1/D2/WS-B repeatedly say "**11** channel factories" hardcode `cfg.Channels["<type>"]`, but the same ADR uses "**~13** channel adapters" for InstanceID stamping (FR-10, C-2, §1). Verified count of non-test factories hardcoding the type key is **13** (dingtalk, googlechat, line, whatsapp_native, telegram, irc, matrix, slack, discord, feishu, wecom, qq, weixin). WS-B — the **critical path** (§9 "the longest single lane") — is scoped at 11, undercounting the largest workstream by two adapters and blurring whether "factories" and "inbound-stamping adapters" are the same set (they are). | Correct "11" → "13" throughout (§1 blast radius, D2 multi-instance content, WS-B scope). State that the factory-plumbing set and the inbound-stamping set are the **same 13 adapters** (with 2 going through `config.InstanceTo*` converters — telegram.go:66, weixin.go:43 — vs. 11 reading `cfg.Channels[...]` directly), so the WS-B estimate reflects the true surface. |
| **R-M6** | MINOR | Incompleteness | §6 D3, §7 | Residual from round-1 M-6: the drift policy is now "drop-and-alert," but the **alert surface** is under-specified — "a surfaced operator notification" with no channel (WS frame? the existing notification system? audit-only?). Given NEW-1, the mechanism and the surfacing are coupled; without a concrete notification path, "alert" is aspirational. | Name the notification path (e.g. the existing operator-notification/WS mechanism used elsewhere) and the audit event shape; add the drift counter (M-8) as the *measurable* side so "alert" is testable. |
| **R-M2** | MINOR | Ambiguity | §6 D3 M-2, §3 Gap #2 | The `{workspace_id, agent_id}` independent-pair decision is stated, but the interaction with the **derived `Identity.ID`** is only half-specified: §5 Option A says `WorkspaceID` is stored "independently of `Identity.ID`" and `Identity` is "the derived routing mechanism." Nothing states what reconciles the two if they drift apart (e.g. `Identity.ID` edited out-of-band, or `agent_id` changed but `Identity` not re-derived). Two sources of truth for "who does this instance route to." | State that `Identity.ID` is **always re-derived from `agent_id` on save** (single write path), and that any read of "the bound agent" reads `agent_id` (the durable pair), never `Identity.ID` directly — or collapse to one field. |
| **R-N4** | MINOR | Incompleteness | §5 Option A, §9 WS-A | Residual N-4: WS-A is told to "drop stale wildcard binding via redefined `isChannelWildcardRaw` (N-4)." `isChannelWildcardRaw` (rest.go:6343-6350) matches on `channel == channelID` where the raw binding's `match.channel` keys on channel **TYPE** (route.go filterBindings lowercases and compares to type). Once `{id}` is a per-instance ID, `isChannelWildcardRaw(matchMap, "whatsapp:eu")` compares an instance ID against a type-keyed `match.channel` and will **never match** → the "drop stale binding" cleanup silently no-ops, leaving dead bindings (the exact confusion §7 wants to prevent). | Specify precisely how a per-instance binding is keyed in `BindingMatch` (a new `instance_id` match dimension? or `channel` now holds the instance ID and `filterBindings` changes?) so `isChannelWildcardRaw` can identify "this instance's wildcard binding." This is entangled with the Option-A-vs-B seam (Option A claims *no* binding is written for bound instances — if so, the only bindings to drop are pre-existing type-wildcard ones, so the cleanup targets `channel == <type>`, not the instance ID; say which). |
| **R-N1** | MINOR | Ambiguity | §2 FR-1, §3 Gap #3 | Round-1 N-1 partly addressed (Gap #3 assumes "mandatory for UI-configured; unbound = not-yet-configured"), but FR-1 still reads as an absolute ("MUST be associable with exactly one workspace") while §7 and the multi-instance UX (WS-F "add instance") depend on an instance existing **before** it's bound. The lifecycle "created unbound → bound on routing-save" is implied but never stated as a state machine. | Add one sentence: an instance MAY exist unbound (created via "add instance," pre-routing-config); it retains default routing until bound; binding is required to *complete* routing config, not to *exist*. Note the unbound state is what NEW-1's guarantee does NOT cover. |
| **O-1** | OBS | Overcomplexity | §9 Parallel Execution Plan | Residual round-1 N-3: the 6-workstream + "2→6→7-reviewer→fix→14-reviewer" wave choreography is still baked into a *Proposed* ADR. It's the operator's mandated pattern, but it couples the *decision* to a staffing model and reads as premature PM detail for a doc whose job is to justify Option A. The routing+UX slice (WS-A/E, High-confidence) and the 13-adapter multi-instance slice (WS-B/C/F, Medium) remain arguably separable epics sharing the Gate-0 seam. | (Non-blocking) Consider moving the wave/reviewer choreography to the taskify/epic artifact and keeping §9 to the dependency graph (Gate 0 → 6 WS + ordering). |
| **O-2** | OBS | — | §9 step 4 | The spike (O-2, correctly retained) must prove the inbound `msg.InstanceID` path — but given NEW-1 it should *also* prove the **drift path**: manually remove the bound agent from CoreTeam and assert the message is dropped+alerted, not delivered to the default. A spike that only proves the happy P0 path will green while NEW-1 remains unenforced. | (Non-blocking) Extend the spike's assertions to include the drift-drop case. |
| **O-3** | OBS | — | §9 WS-B | `ChannelInstanceConfig` is a single flat union struct (config.go:1504-1627) — all per-type fields, including `SessionStorePath` and every `*_ref`, live on one struct. This means most per-instance *state* isolation (FR-8) is automatic once the map key is distinct; the only genuine collisions are **fixed filesystem paths that ignore the key** (WhatsApp `store.db`, and whatever else the WS-B path audit finds) and shared in-memory singletons (`m.channels[name]`, O-3 round-1). Worth stating so WS-C's scope is "audit the key-ignoring paths," not "isolate every field." | (Non-blocking) Note the flat-union structure so FR-8 isn't over-scoped. |

---

## 3. Round-1 CRITICAL/MAJOR Closure Audit

| Round-1 ID | Status in revised ADR | Verdict |
|-----------|----------------------|---------|
| C-1 (ChannelId enum gates `/channels/{id}`) | Added as FR-11, §1 fact (C-1), §7 one-way-door #2, Gate-0 line item, WS-A scope | **CLOSED** (fact re-verified: rest.go:5997 `validChannelIDs[gen.ChannelId(...)]`, ChannelId.yaml is a closed 15-value enum) |
| C-2 (no adapter stamps `msg.InstanceID`) | Added as FR-10, §1 fact (C-2), STRIDE-spoofing caveat, WS-B scope, Gate-0 stamping-shape item | **CLOSED** (re-verified: only `pkg/bus/instanceid_test.go` sets `.InstanceID`; zero production producers) |
| C-3 (resolveMessageRoute precedence + P7 escape) | Added as FR-6/FR-6a, §1 fact (C-3), §6 D3 precedence para, Gap #10 | **PARTIALLY CLOSED** — precedence is now correctly stated, but the *enforcement* of "no P7 for bound instances" is asserted without a mechanism → **NEW-1** re-opens the enforcement half |
| M-1 (rejection set) | §6 D3 enumerates 422/404 set + empty-CoreTeam | CLOSED |
| M-2 (independent pair) | §6 D3 M-2 + Option A note | CLOSED (minor residual R-M2 on `Identity.ID` reconciliation) |
| M-3 (Session wire field) | Gap #11 + WS-D + Gate-0 conditional | **NOT CLOSED** → NEW-2 (contract already has the field; conditional is stale) |
| M-4 (key grammar) | Gap #12, §7 one-way-door #1, Gate-0 | PARTIALLY CLOSED → NEW-3 (delimiter×cred-key charset unclosed) |
| M-5 (cred-ref namespacing) | Gap #13, §7, WS-C | PARTIALLY CLOSED → NEW-3 (over-scoped; real risk is delimiter charset) |
| M-6 (drift = forbidden global-default) | §6 D3 drop-and-alert, §7, FR-6a | PARTIALLY CLOSED → NEW-1 (mechanism unenforceable as placed) + R-M6 (alert surface) |
| M-7 (mischaracterized "silent save") | §1 corrected (M-7 note, line 16) | CLOSED (re-verified: setChannelRouting rest.go:6404-6430 deliberately removes binding; UI `__none__` intentional) |
| M-8 (rollout/observability) | §7 "Operability, rollout & observability" block | CLOSED |

**Net:** 2 of 3 CRITICALs fully closed; C-3's enforcement half re-opens as NEW-1. Of 8 MAJORs, 5 closed, 3 partially closed and re-surface as NEW-2/NEW-3 + residual minors.

---

## 4. Structural Integrity Results (structured-spec mode)

| Check | Result |
|-------|--------|
| Every goal/objective has acceptance criteria | PARTIAL — FR-6a lacks an *enforceable* criterion (NEW-1); FR-9 scoped against wrong artifact (NEW-2) |
| Cross-references consistent (no dangling IDs) | PASS — FR/NFR/Gap/WS/C-/M- IDs resolve |
| Scope boundaries explicit (in/out) | PASS — Gap #4 (cross-workspace handoff), #7 (session-UI depth) explicitly out |
| Success/exit criteria measurable | PARTIAL — M-8 adds `matched_by`/drift counter (good), but the drift *alert* surface is unspecified (R-M6) |
| Requirements referencing each other consistent | FAIL — FR-6a "no P7" vs. the shared-`pickAgentID` reality (NEW-1); "11" vs "~13" adapters (NEW-4); Gap #11 open vs. Session.yaml decided (NEW-2) |
| Error/failure scenarios addressed per requirement | PARTIAL — rejection set + empty-CoreTeam good; drift enforcement + notification path thin (NEW-1, R-M6) |
| Dependencies identified | PASS — Gate 0 → 6-WS graph explicit; NEW-3 adds one missing Gate-0 dependency (cred-key charset) |
| One-way doors enumerated | PASS — both (key scheme, ChannelId enum) now listed; NEW-3 notes the key-scheme door has an unexamined credential-key facet |

**Grounding audit (round 2):** Re-verified ~20 citations. All accurate. New confirmations: `pickAgentID` shared chokepoint + global-default degrade (route.go:255-293) ✓; explicit-agent_id and handoff-pin early returns bypass `ResolveRoute` (loop.go:4485-4569) ✓; `resolveMessageRoute` falls back to `registry.GetDefaultAgent()` at 4585 ✓ (this is the concrete forbidden path NEW-1 must intercept); `channelCredKey` = `"channel_"+channelID+"_"+field` (rest.go:2985) ✓; `Session.yaml` already carries `workspace_id` ✓; factory count = 13 not 11 ✓; delegation graph SOLE-authority claim (delegation_context.go:16-18) ✓ (Gap #4's `[FACT]` is now citable — resolves round-1 N-5).

---

## 5. STRIDE Threat Summary (deltas from round 1)

| Component | Threat | ADR coverage |
|-----------|--------|--------------|
| Inbound `msg.InstanceID` | Spoofing (payload claims another instance → routes to privileged agent) | Covered — FR-10 + §7 "trusted adapter, never from content." Reviewer-assertable. |
| Per-instance credentials | Info disclosure (instance A reads instance B's token) | Covered in intent; **NEW-3**: the `channelCredKey` reuse actually *strengthens* isolation, but the `:` delimiter's validity as a cred-store key char is unverified — a bad delimiter could collide/normalize two instances' keys. |
| Membership validation | EoP / tampering (client-filtered picker bypassed via crafted PUT) | Covered — §6 D3 "membership check is server-side; client filter is UX only." |
| Drift → global default | EoP (message to an unintended, possibly higher-trust default agent) | **NEW-1**: the intended interception is unenforceable as placed → the forbidden global-default degrade can still fire. This is the highest-value STRIDE gap. |

---

## 6. Unasked Questions

1. **NEW-1 core:** What is the *actual data path* by which `route.go`/`pickAgentID` learns a route is bound, and where does the message get dropped (sentinel? error return? pre-`choose` guard)? How does `resolveMessageRoute` (loop.go:4571-4586, which today falls to `registry.GetDefaultAgent()` on miss) honor the drop instead of substituting the default?
2. Do the two preserved higher-precedence paths (explicit `agent_id`, handoff pin) fall under the FR-6a "no global default" guarantee, or are they explicitly exempt? (Their drift handling lives at loop.go:4494/4544, separate from `pickAgentID`.)
3. Is `:` a legal character in a credential-store key? If not, what delimiter satisfies map-key + `inboundInstanceID` lowercasing + `ChannelId` pattern + `channel_<id>_<field>` cred key simultaneously? (NEW-3.)
4. Given `Session.yaml` already has `workspace_id`, what precisely is Gate-0's Session work — populate at creation only? Which layer (wire meta vs. transcript struct) carries it? (NEW-2.)
5. When Option A writes *no* binding for a bound instance, what exactly does WS-A's "drop stale wildcard binding" target — the pre-existing type-wildcard binding keyed on `channel == <type>`? Then `isChannelWildcardRaw` needs the *type*, not the instance ID (R-N4).
6. Is the durable "who does this instance route to" answer read from `agent_id` or `Identity.ID`? What re-derives `Identity` when `agent_id` changes? (R-M2.)

---

## 7. Verdict

**Verdict: REVISE.**

Review written to: `docs/internal/architecture/ADR-029-channel-instance-workspace-binding-review.md`

The revision successfully closed 2 of 3 round-1 CRITICALs and 5 of 8 MAJORs. But the third CRITICAL's *enforcement* half (NEW-1) — the mechanism guaranteeing a bound instance never silently reaches the global default — is asserted against an architecture (`pickAgentID` as a signal-blind shared chokepoint, plus two `ResolveRoute`-bypassing early returns) that cannot deliver it without plumbing the ADR does not specify. That is the ADR's central promise; it must be made enforceable before speccing. NEW-2 (stale contract question), NEW-3 (grammar×cred-key charset one-way-door facet), and NEW-4 (11-vs-13 critical-path undercount) are lower but should ride along.

Address the findings above, then re-run:

```
/grill-spec docs/internal/architecture/ADR-029-channel-instance-workspace-binding.md
```

Once NEW-1's mechanism is specified (and NEW-2/3/4 corrected), the routing+UX slice (FR-1–FR-6a, FR-9, D3) is ready for `/plan-spec`; the 13-adapter multi-instance slice remains Medium-confidence pending the Gate-0 grammar + per-type-path inventory.

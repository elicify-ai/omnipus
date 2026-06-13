# Grill Review — ADR-019: v0.1.0 "Foundation" — Workspaces Redesign Structural Shapes

- **Reviewed file:** `docs/internal/architecture/ADR-019-v01-workspaces-foundation.md`
- **Review mode:** generic-markdown / ADR-review (ratifying ADR, first phase of an `/sdd` run)
- **Reviewer stance:** adversarial, read-only. The author is not trusted; the question is "how does this ADR, ratified as-is, force a re-cut or a 3 AM incident later?"
- **Grounding sources consulted:** `.preview-doc/decisions.html` (ledger), `.preview-doc/roadmap.html`, `docs/internal/design/memory-redesign-2026-05.md`, `docs/internal/design/tasks-redesign-2026-05.md`, and the cited `pkg/` files (verified directly — see Citation Audit).

---

## ROUND 2 — Re-review verdict (against ADR Rev 2)

**Verdict: PASS.**

All 5 MAJOR findings from round 1 are **closed**, each verified against the revised ADR text *and* re-grounded against the code:

| ID | Round-1 severity | Round-2 status | Evidence in Rev 2 |
|---|---|---|---|
| **F-1** | MAJOR | **CLOSED** | New **FR-4b** adds the optional bus `instance_id` field on `Peer`/`SenderInfo`/`InboundMessage`/`OutboundMessage` (all 4 structs verified present in `pkg/bus/types.go`), correctly tagged Go-field-not-contract, with the NFR-1 rationale. **FR-7** no longer says "start" — it now states the `sessions/` firehose record, `counters.jsonl` line, and `born_in`/`cited_in` schemas are **frozen/pinned in v0.1.0** so v0.2.0 reads them with no backfill. Shape row 7 updated to "frozen log-record formats"; row 4b added. Both unasked questions (log format, bus field) answered. |
| **F-2** | MAJOR | **CLOSED** | FR-12 now cites `pkg/tools/web.go:91 SearchProvider` (verified: `type SearchProvider interface` at line 91). The "(prior session grounding)" hedge is gone. Citation Audit row for this claim should be re-marked YES (see below). |
| **F-3** | MAJOR | **CLOSED** | FR-5 and shape row 5 now state the **opencode driver is net-new** ("there is no opencode runner in `pkg/`; `opencode-zen` is only a model-ref alias"). Verified: `pkg/providers/` has only `claude_cli` + `codex_cli`/`codex` providers, no opencode. The enum keeps `opencode` reserved; the "3 preinstalled drivers" overclaim is removed. |
| **F-7** | MAJOR | **CLOSED** | FR-3 / shape row 3 now ground agent classification to `AgentConfig.Type` / `AgentType` enum at `config.go:461-499` (verified: `Type AgentType` field at 461, enum system/core/custom). States classification is **reused, not new**; only `voice` is the new field. The wrong `config.go:590 kind` citation is gone. |
| **F-8** | MAJOR (was MINOR in r1) | **CLOSED** | FR-2 / shape row 2 / R2 now label `ChannelEntry`/`ChannelConfigureRequest` as an **existing-schema modification** (verified: both YAMLs present in `contracts/components/schemas/`), flagging the stricter `verify-contracts` blast radius (removals/renames break shipped TS Zod validators). *(Note: F-8 was MINOR in round 1; the operator promoted it into the MAJOR set for round-2 closure. Treated as such.)* |

**Additional round-1 MAJORs (F-4, F-5) — also closed:**

| ID | Round-1 severity | Round-2 status | Evidence in Rev 2 |
|---|---|---|---|
| **F-4** | MAJOR | **CLOSED** | New **NFR-7** ("Inert fields fully schema-pinned — the real NFR-1 guard") requires every inert field (`accept_from`, `budget`, `depth`, `voice`, plugin-manifest, frozen log records) to have its complete schema/type pinned now, and explicitly separates the two risks: UI-hiding mitigates user-confusion only; **only the pinned schema satisfies NFR-1**. R3 reframed to match ("UI-hiding addresses only user-confusion, not NFR-1"). The mitigation no longer answers the wrong risk. |
| **F-5** | MAJOR | **CLOSED** | FR-8 / shape row 8 now pin `blocked_by` delete/runtime semantics: (a) validator rejects cycle-creating edges at **write time** (so no runtime cycle exists); (b) **deleting a task cascade-cleans inbound + outbound edges** and surfaces affected dependents; (c) orphan edges (missing referenced task) are **dropped by the validator on load**. The previously-untestable "edges live, DAG runs" bar now has a defined, testable contract. |

**Round-1 MINOR / OBSERVATION re-check:**

| ID | Sev (r1) | Round-2 status |
|---|---|---|
| F-6 | MINOR (v0.3 vs v0.3.0 ambiguity) | **Partially addressed / acceptably deferred.** §0 still carries `[FACT: CLAUDE.md "v0.3 = Fresh-build"]` (correctly quoting CLAUDE.md's umbrella term) and R2 still says "v0.3 only lifts the cap." The vocabulary is not fully unified to `vX.Y.0`. Low blast radius for an ADR whose scope is explicitly v0.1.0; does not gate. Recommend the plan-specs adopt the roadmap's `vX.Y.0` labels. **Deferred — acceptable.** |
| F-7 | MINOR→MAJOR | **CLOSED** (see above; operator handled it as a MAJOR). |
| F-8 | MINOR→MAJOR | **CLOSED** (see above). |
| F-9 | MINOR (STRIDE for skill-write→execute + runner permission channel) | **Partially addressed / deferred.** R5/R6 still carry the mitigations (consent-gated/versioned/sandboxed skill writes; own-process/Landlock/worktree/egress/connection-test runners). No explicit per-entry-point STRIDE paragraph was added, but FR-5's bidirectional runner now explicitly includes the "control/permission-in" channel as a named shape, which is where the spoofing-frame concern lands. The threat-model detail is correctly plan-spec scope (Spec-4 pins the interface + conformance test). **Deferred to Spec-4 — acceptable**; recommend Spec-4 carry the explicit STRIDE note on the permission-in channel framing/auth and the skill author-then-execute re-sandbox/audit path. |
| F-10 | MINOR (overcomplexity: "Card-projectable identity") | **Not changed.** FR-11 still names "agent-reference target + Card-projectable identity = A2A-ready." This remains a thin speculative shape, but it is genuinely inert and adds **no field today** (an agent-reference is already an ID), so it costs nothing and cannot lock a contract (A2A wire format is external per the ADR). Low risk. **Acceptable as-is**; optionally demote to a roadmap note in Spec-6. |
| F-11 | OBSERVATION (operability surface) | **Partially addressed.** FR-4 now includes a per-runner **connection test** (health: spawn/auth/handshake) — a concrete observability surface for runners. No general health/audit-surface statement for skill-writes or channel-instances. Observation-level; acceptable. |
| F-12 | OBSERVATION (relaxed one-decision-per-ADR) | **Addressed.** §7 Caveat retained; the 6-spec decomposition keeps R1/R2 in their own specs (Spec-1, Spec-2). As predicted, fine. |
| F-13 | MINOR (rename "storage paths" scope) | **Acceptably deferred.** §0 and Constraints reaffirm **greenfield — no existing-install data migration**; DECISION 1 "Missing" still defers the exact inventory to Spec-1. The "no migrator needed" intent is implied by greenfield but not stated as a one-liner against the rename. Low risk; Spec-1's mechanical inventory pass covers it. **Acceptable.** |
| F-14 | OBSERVATION (Phase 3.5 gate not testable) | **Partially addressed.** §9 still names the cross-spec consistency pass and now ties it to "shared types … must agree" with a contract-regen experiment, but no single concrete exit-check assertion is written. Observation-level; recommend Spec set defines the one-shot `make verify-contracts`-across-merged-contracts gate. **Acceptable.** |

**Round-2 bottom line:** All 5 operator-nominated MAJOR findings (F-1, F-2, F-3, F-7, F-8) plus the two remaining round-1 MAJORs (F-4, F-5) are closed and code-verified. No new MAJOR or CRITICAL issues were introduced by the Rev 2 edits. The residual items are MINOR/OBSERVATION and are correctly deferred to the plan-specs (F-6 vocabulary, F-9 STRIDE detail → Spec-4, F-10 thin shape, F-13 rename inventory → Spec-1, F-14 cross-spec gate). The foundation-first thesis was sound in round 1 and the grounding is now rigorous. **The ADR may proceed to plan-spec (GATE A2 PASS).**

> The round-1 report below is retained verbatim for traceability.

---

## Executive Summary

ADR-019 ratifies a **foundation-first sequencing** thesis: land all 12 structural shapes additively in v0.1.0, do the one deliberate breaking migration (Connection-as-instance) up front, start the append-only logs, defer all behaviour. The thesis is sound, well-grounded in the committed concept, and the option analysis correctly eliminates B (defers the breaking migration into a feature release) and C (tunes behaviour blind). The two flagged one-way doors (R1 rename, R2 Connection-instance) are the right two, correctly rated Medium and gated behind plan-specs.

The defects are not in the **thesis** — they are in the **grounding rigor and shape-completeness**: several [FACT] tags are mis-cited or are assumptions wearing a fact label, two shapes that the ADR's own NFR-1 logic says must be front-loaded are silently under-specified (the append-only log schema, the in-process bus `instance_id` field the roadmap explicitly calls out), and the "inert shapes" mitigation ("keep them out of the UI") does not actually neutralize the re-cut risk it claims to.

| Severity | Count |
|---|---|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 6 |
| OBSERVATION | 4 |

**Verdict: REVISE.** No critical defects; the thesis can proceed. But five MAJOR findings must be closed before plan-spec, because each one, left as-is, either (a) re-introduces the exact re-cut the ADR exists to prevent, or (b) lets a mis-grounded [FACT] propagate unchecked into six downstream specs.

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| F-1 | MAJOR | Incompleteness | §6 shape inventory; roadmap | The roadmap explicitly lists **"optional `instance_id` on the in-process bus structs now"** and **"start the append-only event logs (`sessions/` firehose · `counters.jsonl` · `born_in/cited_in`)"** as v0.1.0 shapes. The bus `instance_id` field appears in **no FR** and no shape row. The log files are named in FR-7 ("START the append-only logs") but their **on-disk schema/format is never specified** anywhere in the ADR. By NFR-1's own logic these are persisted shapes — if their format moves later, that is a re-cut. | Add a shape row (or FR) for the bus `instance_id` field, and a shape row pinning the append-only log file **formats** (firehose record schema, `counters.jsonl` line schema, `born_in/cited_in` capture). State that the format is frozen in v0.1.0 even though ranking/graph consumers are v0.2.0. |
| F-2 | MAJOR | Incorrectness | §6 row 12; FR-12 | FR-12's [FACT] cites **`web.go SearchProvider`** "(prior session grounding)". The symbol exists but in **`pkg/tools/web.go`** (`type SearchProvider interface` at line 91), **not** `pkg/gateway/web.go` — there is no `web.go` in `pkg/gateway`. The "(prior session grounding)" parenthetical is an [ASSUMPTION] hedge embedded inside a [FACT] tag. | Correct the citation to `pkg/tools/web.go:91`. Remove the "(prior session grounding)" hedge — it is now verifiable [FACT]. Audit every other [FACT] for the same pattern (see F-3). |
| F-3 | MAJOR | Incorrectness | FR-4/FR-5 §6 row 4-5; roster | The executor enum is `external-cli{claude-code,opencode,codex}` and the ledger calls these "3 runner drivers (Claude Code · opencode · Codex)" **preinstalled**. Code has providers for **claude_cli and codex_cli only** — there is **no opencode provider anywhere in `pkg/`**. FR-5's evidence honestly cites only `{claude_cli,codex_cli}_provider.go`, but the roster/ledger imply opencode ships as a working driver. This is a shape that claims a [FACT] grounding it does not have. | Either (a) explicitly tag opencode as **net-new (no existing provider)** in FR-4/FR-5 so the implementing spec knows it is greenfield work, not "surface the existing", or (b) drop opencode from the v0.1.0 preinstalled set and reserve it in the enum only. Do not let "3 preinstalled drivers" pass as grounded when only 2 exist. |
| F-4 | MAJOR | Inconsistency | §6 Justification; R3; §0/business obj | The inert-shapes mitigation is stated as "keep inert fields **out of the UI** until enforced (so they don't mislead users)." Hiding a field from the UI does **nothing** for NFR-1 (no re-cut) — the re-cut risk is in the **wire/storage shape**, not the UI. The mitigation answers the wrong risk (R3 "mislead") while the stated justification implies it also addresses the shape risk. A field shaped wrong now and hidden in the UI still forces a contract change when it is later enforced. | Separate the two risks explicitly: (a) UI-hiding mitigates *user confusion* (R3); (b) the *re-cut* risk for inert fields (`accept_from`, `budget`, `voice`, `depth`, plugin manifest) is mitigated **only** by getting the schema right now — which requires each inert field's full value-space + validation rules to be pinned in the plan-spec **even though unenforced**. Add this as an explicit plan-spec obligation. |
| F-5 | MAJOR | Incompleteness / Infeasibility | §6 row 8; FR-8; tasks-redesign | FR-8 ships `blocked_by` **with its cycle/orphan validator** and asserts "edges live, DAG runs" run by Jim·Orchestrator (FR-6). But the ADR never states **what the Orchestrator does when it hits a cycle/orphan at run time** (vs at edit time), nor what happens to a task whose `blocked_by` references a **deleted** task (cascade-delete is a known v0.3 concept per MEMORY/tasks-redesign). "Edges live + DAG runs" without a defined cycle/dangling-reference runtime behaviour is an untestable acceptance bar and a live correctness hazard the moment a user deletes a blocking task. | Specify (or explicitly defer with a tracked decision) the runtime semantics: (1) edit-time validator rejects cycles/orphans; (2) what the Orchestrator does if a cycle is somehow present at run time; (3) what `blocked_by` resolution does when the referenced task is deleted (reject delete / null the edge / cascade). This is a v0.1.0 shape decision because the *validator contract* is shipping now. |
| F-6 | MINOR | Ambiguity | §0; FR-7; roadmap | The ADR uses "**v0.3**" (R2 "v0.3 only lifts the cap"; §0 "[FACT: CLAUDE.md v0.3 = Fresh-build]") interchangeably with the roadmap's **5-release scheme (v0.1.0…v1.0.0)** where the cap is lifted in **v0.3.0 Connections**. CLAUDE.md's "v0.3" is the *whole* Rooms/Workspaces redesign; the roadmap's "v0.3.0" is one release within it. A reader cannot tell whether "v0.3" means "the redesign umbrella" or "release v0.3.0". | Pick one vocabulary. Use the roadmap's `vX.Y.0` labels throughout the ADR and reserve "the redesign" for the umbrella. Restate R2 as "v0.3.0 lifts the cap." |
| F-7 | MINOR | Incorrectness | §0 / FR-3 citations | `[FACT: ...config.go:590 agent kind already exists]` and `config.go:775-778 typed fields`. Line 590 is `AgentDefaults.GetModelName` / `PeerMatch` — **not** an agent `kind` field. Line 775 is the **start** of `ChannelsConfig` (typed channel fields), which is approximately right but the `kind` claim at 590 is wrong. The ledger does say "agent `kind`" is added additively, so the *decision* is grounded — but the **line citation** is not. | Re-locate the actual `kind`/`AgentType` definition (grep `AgentType`/`Kind` in `pkg/config`) and fix the line number, or downgrade to "[FACT: ledger — agent `kind` additive]; [ASSUMPTION: extends existing AgentConfig]" if no field exists yet. |
| F-8 | MINOR | Incompleteness | FR-2; §6 row 2 | FR-2 says "regenerated `ChannelEntry`/`ChannelConfigureRequest` contract" implying these are new. Both already exist (`contracts/components/schemas/ChannelEntry.yaml`, `ChannelConfigureRequest.yaml`). The change is a **modification** of existing committed schemas — which has stricter Constraint #8 / `verify-contracts` blast radius than adding new ones (existing SPA consumers + generated TS Zod break on field changes). | Note in FR-2 / R2 that these are **existing** schemas being modified, and that the contract delta must enumerate removed/renamed fields (not just added), because removals break the generated TS validators the SPA already ships. |
| F-9 | MINOR | Insecurity (STRIDE) | FR-9; FR-5; R5/R6 | Threats are named (sandbox, consent, deny-by-default) but no STRIDE coverage for two new entry points: (1) **`system.skill.create/edit`** writing skill *code* that an agent later executes — an Elevation-of-Privilege / Tampering path (a skill the agent authors then runs); (2) the **`ExternalAgentRunner` control/permission-in channel** — a Spoofing/Tampering surface (can a malicious CLI subprocess forge permission-grant replies on stdin?). R5/R6 give mitigations but no threat model. | Add a one-paragraph STRIDE note per new entry point: skill-write → execute path (who can author, is the written skill re-sandboxed on execution, is there an audit record), and the runner permission channel (is the control channel authenticated/framed so the child can't spoof an approval). These are shape-influencing (audit-log fields, permission-frame format). |
| F-10 | MINOR | Overcomplexity | FR-11; §6 row 11 | "Protocol hooks (ACP/A2A shape-reserved)" ships **inert protocol-readiness** ("bidirectional runner = ACP-ready; agent-reference + Card-projectable identity = A2A-ready"). The bidirectional runner is justified by the CLI floor (DECISION 5, High confidence — good). But "Card-projectable identity" as an explicit A2A-readiness shape is speculative generality: nothing in v0.1.0…v0.4.0 consumes it, and A2A wire format is external (so it can't lock our contracts anyway). | Keep the bidirectional runner (independently justified). **Drop "Card-projectable identity" as a named v0.1.0 shape** — an agent-reference is already a string/ID; "Card-projectable" adds no field today and risks an unused abstraction. Reserve it as a roadmap note, not an FR-11 shape. |
| F-11 | OBSERVATION | Inoperability | whole ADR | No operability shape is mentioned for the foundation: the append-only logs are introduced for *future ranking* (F-1) but the ADR never says whether v0.1.0 ships any health/observability surface for the new subsystems (runner connection-test results, skill-write audit, channel-instance health). | Consider noting which observability/audit shapes are part of v0.1.0 vs deferred, so on-call has *something* when a runner or skill-write misbehaves. |
| F-12 | OBSERVATION | Overcomplexity | §7 Caveat; "one decision per ADR" | Relaxing "one decision per ADR" to 12 sub-decisions is defensible for a ratifying ADR, but it means the per-shape Medium/High confidence ratings carry the real weight — and two Medium items (R1/R2) gate the whole foundation. | Fine as-is; just ensure the 6-spec decomposition keeps each Medium item in its own spec (it does: Spec-1, Spec-2) so the gate is enforceable. |
| F-13 | MINOR | Ambiguity | FR-1; DECISION 1 | "rename `project→workspace` **end-to-end (code · contracts · storage paths)**" — but the ledger says the rename was "Pre-ADR: concept-doc only; **executed during implementation**" and DECISION 1's "Missing" admits the inventory is unknown. The scope of "storage paths" is load-bearing (existing sessions/tasks are written under `project_id`; greenfield means *fresh* installs, but a dev/test fixture or an in-flight branch with `project_id` data is not covered). | Clarify that "storage paths" rename applies to fresh installs only (greenfield) and that there is **no** on-disk data migration — any existing `project_id`-keyed data on a dev machine is discarded, not migrated. State this explicitly so an implementer doesn't write a needless migrator. |
| F-14 | OBSERVATION | Incompleteness | §9 next steps | The cross-spec consistency pass (Phase 3.5) is named but has no **acceptance gate** — "shared types must agree" is not testable as written. | Define the Phase 3.5 exit check concretely: e.g. "a single shared `contracts/` regen across all 6 specs produces no drift, and the `Workspace` key / `executor` enum / runner interface appear identically in every spec that references them." |

---

## Structural Integrity (generic-markdown / ADR mode)

| Dimension | Assessment |
|---|---|
| Scope clarity | **Strong.** Explicitly "*how to sequence*, not *what is the redesign*." In/out is clear (shapes + 1 migration + start logs IN; behaviour/derived data OUT). |
| Actors | **Adequate.** Single operator/owner, downstream plan-spec authors, implementation subagents, seeded Jim·Orchestrator. Missing: the **external CLI subprocess** as a (hostile-capable) actor in the threat model (F-9). |
| Success criteria | **Weak spot.** The governing NFR-1 ("no later release changes a v0.1.0 wire/scoping/storage shape") is the success criterion but is **not made measurable** — there is no stated check that proves a shape is "complete enough not to re-cut." F-1/F-4/F-5 are all instances of shapes that pass the prose bar but fail a "would this force a later contract change?" test. |
| Failure modes | **Partial.** R1–R6 cover the big ones. Gaps: `blocked_by` runtime/delete behaviour (F-5), skill-write→execute and runner-permission spoofing (F-9). |
| Implementation detail | **Appropriately deferred** to 6 plan-specs. The two Medium items correctly carry "Missing/Would improve" blocks. |
| Assumptions stated | **Mostly.** But F-2/F-3/F-7 show [ASSUMPTION]s ("prior session grounding", opencode driver, line 590 `kind`) presented inside [FACT] tags. |
| Constraints | **Strong.** Single-binary, pure-Go, contract-first, single-user, greenfield all cited to CLAUDE.md / decisions. |

---

## Citation Audit ([FACT] tag verification)

Every [FACT] was checked against the cited source.

| Claim | Cited | Verified? |
|---|---|---|
| Foundation-first thesis ("front-load shapes, defer behaviour") | roadmap.html | **YES** — verbatim in roadmap. |
| `project→workspace` rename, v0.1.0 migration | decisions.html | **YES** — ledger: "Code rename project → workspace end-to-end". |
| 4-base roster, Max retired, nullable `voice` | decisions.html; coreagent | **YES** — ledger + `pkg/coreagent/core.go` confirms 5 cores w/ Mia/Jim/Ray/Ava/Max locked identity. |
| Channels typed-singleton fields | config.go:775-778 | **YES** (≈) — `ChannelsConfig` typed fields start at 775; line is the struct head, close enough. |
| `initChannels` if-ladder | manager.go:582 | **YES** — `func (m *Manager) initChannels` at 582. |
| Skill tools are stubs | skill.go | **YES** — install/remove/search/list all `"status":"stub"`; no create/edit. |
| `RegistryManager` coordinates N registries; `RegistryConfig` single ClawHub | registry.go | **YES** — confirmed. |
| `RequireNotBypass` exists | (FR-12) | **YES** — `pkg/gateway/middleware/bypass_gate.go:35`. |
| MCP go-sdk v1.4.1 | go.mod | **YES** — `go.mod:27`. |
| `blocked_by` absent from boardtask | boardtask.go | **YES** — `Task` struct has no `blocked_by/start/due/recurrence`; only `ProjectID` (confirms rename surface). |
| hook_process JSON-RPC/stdio; cli providers; hardened_exec | various | **YES** — hook_process.go has jsonrpc+stdin/stdout; claude_cli + codex_cli providers exist; hardened_exec present. |
| **`web.go SearchProvider`** | "pkg/gateway… (prior session grounding)" | **NO (mis-cited)** — symbol is `pkg/tools/web.go:91`, not gateway; hedge inside [FACT]. → **F-2** |
| **opencode runner driver preinstalled** | implied by roster/ledger | **NO (ungrounded)** — no opencode provider in `pkg/`. → **F-3** |
| **agent `kind` at config.go:590** | config.go:590 | **NO (mis-cited line)** — 590 is `PeerMatch`/`AgentDefaults`, not a `kind` field. → **F-7** |
| `ChannelEntry`/`ChannelConfigureRequest` "regenerated" (new) | (FR-2) | **PARTIAL** — schemas already exist; this is a *modification*. → **F-8** |

Net: thesis + 11 of 14 [FACT]s verify cleanly; 3 are mis-cited/ungrounded and 1 is imprecise. None invalidate the decision, but each must be corrected so it doesn't propagate into six specs as gospel.

---

## Test Coverage Assessment

This is an ADR, so the bar is "is each shape verifiable and is the foundation provable as non-re-cut," not a TDD plan.

- **NFR-1 has no proving test.** The single most important property — "no later release reshapes this" — is asserted, never operationalized. Recommend a concrete gate (F-14): a one-shot `make verify-contracts` across the merged 6-spec contract set, plus a documented "shape completeness" checklist per inert field (full value-space pinned even if unenforced — F-4).
- **R1/R2 correctly demand tests** (mechanical rename inventory; Connection-instance migration test) — good, and pushed to Spec-1/Spec-2.
- **`blocked_by` validator** (FR-8) ships "with its validator" but the ADR specifies neither the cycle-detection contract nor the delete/orphan runtime behaviour (F-5) — so the validator's acceptance criteria are currently untestable.
- **Negative/security paths** for `skill.create/edit` and the runner permission channel are unaddressed (F-9).

---

## STRIDE Threat Summary

| Component (new in v0.1.0) | Threats identified by ADR | Gaps (this review) |
|---|---|---|
| `ExternalAgentRunner` (CLI + JSON, bidirectional) | DoS/EoP via powerful CLI → sandbox, worktree isolation, egress allowlist, connection-test-before-trust (R6) | **Spoofing/Tampering on the control/permission-in channel** unaddressed — can the child subprocess forge an approval reply? (F-9) |
| `system.skill.create/edit` | Self-modifying agent → consent-gated, versioned, sandboxed, deny-by-default (R5) | **EoP via author-then-execute** unaddressed — is an agent-authored skill re-sandboxed/audited on execution? (F-9) |
| Connection-as-instance + credential-ref keys | (none explicit) | Credential-ref key scheme is "Missing" in DECISION 2 — Information Disclosure surface (do refs leak across instances?) should be a Spec-2 obligation. |
| Append-only logs (firehose, counters, born_in/cited_in) | (none — shape unspecified, F-1) | Repudiation/Info-disclosure: log format unspecified means audit/PII handling unspecified. Tie to F-1. |
| Single-user / one-password / sensitive-settings re-type | maps to `RequireNotBypass` (verified) | Adequate for single-user; no gap. |

---

## Unasked Questions (the ADR should have answered)

1. **What is the on-disk format of the append-only logs, and is it frozen in v0.1.0?** The roadmap says they start now so v0.2 ranking "never needs a backfill" — that guarantee is void unless the format is pinned now. (F-1)
2. **Where is the in-process bus `instance_id` field?** The roadmap names it as a v0.1.0 additive shape; the ADR omits it entirely. (F-1)
3. **What happens to a `blocked_by` edge when the referenced task is deleted?** (F-5)
4. **Does opencode ship as a working runner driver in v0.1.0, or is it enum-reserved?** Code has no opencode provider. (F-3)
5. **For each inert field (`accept_from`, `budget`, `depth`, `voice`, plugin manifest): is its full value-space and validation pinned now, or only its presence?** Presence alone does not satisfy NFR-1. (F-4)
6. **When the cap is lifted to N instances later, does the cap-1 schema *already* allow N without a contract change** (i.e. is the cap a runtime guard over a map, not a schema constraint)? The ADR implies yes ("map keyed by instance") but never asserts the cap is enforcement-only, not shape. (relates to R2)
7. **Is "v0.3" the redesign umbrella or release v0.3.0?** (F-6)

---

## Verdict

**REVISE**

Review written to: `docs/internal/architecture/ADR-019-v01-workspaces-foundation-review.md`

The foundation-first thesis is correct and well-grounded — proceed with it. But the ADR has **5 MAJOR findings** that each either re-introduce the re-cut risk it exists to prevent (F-1 unspecified log/bus shapes, F-4 inert-field schema not actually pinned, F-5 `blocked_by` runtime/delete semantics) or let mis-grounded [FACT]s propagate into six specs (F-2 SearchProvider mis-cite, F-3 opencode ungrounded). Address F-1…F-5 (and ideally the MINORs F-6…F-10) in the ADR before opening the plan-specs.

Address the findings above, then re-run:

```
/grill-spec docs/internal/architecture/ADR-019-v01-workspaces-foundation.md
```

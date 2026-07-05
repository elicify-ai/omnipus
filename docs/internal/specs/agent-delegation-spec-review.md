# Adversarial Review: `delegate` — Unified Agent Delegation Tool + Tool-Approval Grant Inheritance

**Spec reviewed**: `docs/internal/specs/agent-delegation-spec.md`
**Review date**: 2026-07-04
**Verdict**: REVISE

## Executive Summary

This spec is well-grounded for the tool-merge half (User Story 1 — fixing the confirmed `check_spawn_status`/`spawn` disconnect), but its entire second half (User Story 2, grant inheritance) rests on a commit (`d0f65482`) that is verified absent from the local working tree these specs were drafted in, and its nested-delegation depth claim ("`SubTurn.MaxDepth`, default 3") does not match the actual code, where the default is 0/uncapped. Neither defect is unfixable, but both must be corrected before implementation — one is a precondition-verification gap, the other silently changes what the spec's own P0 security test (a bounded three-level chain) is supposed to prove.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **12** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] `SubTurn.MaxDepth`'s claimed default ("3") does not match the code — the actual default is 0 (uncapped)

- **Lens**: Incorrectness
- **Affected section**: Edge Cases ("the real limit is `SubTurn.MaxDepth`... default 3, operator-configurable"); Clarifications ("bounded by `SubTurn.MaxDepth` (default 3, operator-configurable)")
- **Description**: Verified against `pkg/config/config.go:1323` (`MaxDepth int json:"max_depth" env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH"`) and `pkg/agent/delegation_context.go:41`'s own comment: *"from `defaults.SubTurn.MaxDepth` (0 = uncapped)"*. No `GetMaxDepth()` accessor or default-assignment (`= 3`) exists anywhere in the codebase. The field is a plain Go `int`; its zero value (0) means uncapped per the code's own documentation, not "3."
- **Impact**: The spec repeatedly frames nested delegation as safely bounded by a small, fixed default depth, and uses that framing to justify testing exactly a three-level chain (FR-D8, `TestApprovalGrant_TransitiveAcrossThreeLevels`) as representative coverage of "the" real limit. If the actual out-of-the-box behavior is *uncapped* depth, then (a) the "bounded" framing is misleading — an operator who never touches `OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH` gets unlimited delegation nesting by default, a materially different resource/security posture than "capped at 3", and (b) the three-level test, while still useful as a positive proof of transitivity, is not actually validating "the real limit" the spec claims it validates — it is validating an arbitrary depth chosen without grounding in an enforced default.
- **Recommendation**: Correct the claim to state the actual default (0 = uncapped) and either (a) add a functional requirement to change the default to a genuinely enforced value if unbounded nesting was never intended, or (b) if unbounded-by-default is accepted/intended, drop the "bounded by default" framing and instead justify the three-level test purely as "sufficient to prove `Inherit`'s composition property holds transitively, independent of any specific depth limit."

---

#### [MAJ-002] User Story 2's entire foundation (`d0f65482`) is verified absent from the working tree these specs were drafted in

- **Lens**: Incorrectness / Inoperability
- **Affected section**: Header ("Input"); "Verified Behavior" section; FR-D5, FR-D6, FR-D8
- **Description**: The spec states `d0f65482` is "already on `origin/hotfix/v0.1.1`, independently verified in-session" and treats `ApprovalGrantStore`'s `Inherit`/`IsAllowed`/`Record`/`ClearSession` methods, and `ws_approval.go`'s policy-before-grant ordering, as pre-existing, already-tested infrastructure this spec merely adds regression coverage for. Verified: `d0f65482` **is** an ancestor of `origin/hotfix/v0.1.1`'s tip, confirming the claim is factually true of that remote branch — but it is **not** present in the local checkout at `/home/dev/omnipus2` where this spec file itself lives (`pkg/security/approvalgrants.go` does not exist there; `git merge-base --is-ancestor d0f65482... HEAD` returns false). The local HEAD is exactly two commits behind `origin/hotfix/v0.1.1`, and `d0f65482` is one of those two commits.
- **Impact**: FR-D6 ("MUST NOT modify `ApprovalGrantStore`'s... already-verified logic") and the "Verified Behavior" section's citation of `TestApproveTool_PolicyDenyOverridesGrant`/`TestApproveTool_PolicyAllowNeverPrompts` as existing tests are both statements about code that is not visible from the current checkout. An implementer who branches from local HEAD (a very easy mistake — it's the checked-out state where the spec itself was authored) rather than fetching and resetting to `origin/hotfix/v0.1.1`'s actual tip will find none of this infrastructure exists, making FR-D5/FR-D6/FR-D8 as written non-actionable (there is nothing to "not modify," and the "existing" tests FR-D5 says to chain don't exist to chain).
- **Recommendation**: Add an explicit implementation precondition to the spec header: *"Before starting, verify the working tree includes `d0f65482` (`git log --oneline | grep d0f65482`); if absent, fetch and check out `origin/hotfix/v0.1.1` — do not build on a local checkout that predates this commit."* This is a five-minute fix that prevents a very plausible and costly mistake given this exact class of stale-base error is a recorded recurring problem for this team.

---

#### [MAJ-003] Companion overview doc (`tool-consolidation-spec.md`) names this tool `subagent`, not `delegate` — an unreconciled naming conflict between two "ready for implementation" documents dated the same day

- **Lens**: Inconsistency
- **Affected section**: This spec's header ("Tool name resolved 2026-07-04: `delegate`"); `tool-consolidation-spec.md` §4 ("name TBD by implementer... this spec refers to it as `subagent` throughout")
- **Description**: `tool-consolidation-spec.md` is marked "Status: Draft — ready for implementation," dated 2026-07-04 (the same day this spec resolved the name to `delegate`), and its FR-S1/FR-S2 are the formal functional requirements an implementer starting from that umbrella document would read first — yet it still says the name is "TBD by implementer" and uses `subagent` throughout its own schema example and prose. Nothing in `tool-consolidation-spec.md` was updated to note the name was later locked to `delegate`, and this spec (`agent-delegation-spec.md`) does not flag the conflict either, despite listing `tool-consolidation-spec.md`'s governing ADR as its own "Input."
- **Impact**: An implementer working top-down from the umbrella spec (a reasonable, even expected, entry point given it's the one with "ready for implementation" in its status line and a `## 7. Traceability` section pointing at exact file paths) could reasonably implement the tool as `subagent` — schema field names, registry key, migration target key, frontend labels all following that name — while this more detailed companion spec, which they may read second or not at all, has already locked `delegate` as final. This is exactly the class of same-concept-different-name defect (CON-01) that causes real integration breakage (e.g., `pkg/config/migration.go`'s target key, frontend `humanizeToolName.ts` entries, and this spec's own test names all keyed on the wrong string relative to what a different part of the same change assumes).
- **Recommendation**: Update `tool-consolidation-spec.md` §4 to state the resolved name (`delegate`) and remove "name TBD" language, or at minimum add a one-line addendum at the top of that document pointing to this spec's Clarifications section as the name's authoritative resolution. Do this before implementation starts, not as a "the detailed spec wins" assumption left implicit.

---

#### [MAJ-004] FR-D8's "no new inheritance logic is required" claim is asserted, not verified against the actual `Inherit` implementation

- **Lens**: Incorrectness
- **Affected section**: Edge Cases; FR-D8; BDD Scenario "A grant flows transitively across a three-level delegation chain"
- **Description**: The spec asserts confidently that transitivity "already falls out of `Inherit`'s existing copy-at-spawn semantics" and that FR-D8 merely adds a missing test, not new code — but the "Existing Codebase Context" table for this spec (unlike every other symbol row) does not cite the actual `Inherit` implementation's line-level behavior for this specific claim; it only asserts it in prose ("each `spawnSubTurn` call copies whatever the immediate parent's bucket *currently* holds — including anything the parent itself inherited"). Given `pkg/security/approvalgrants.go` does not exist in the reviewable checkout (MAJ-002), this specific behavioral claim about `Inherit`'s implementation could not be independently verified for this review, unlike essentially every other cited symbol in the three specs (which were verified against real code in this session).
- **Impact**: If `Inherit`'s actual copy semantics are per-hop-only (copies only the immediate parent's *own* explicitly-granted set, not a set that itself may include inherited entries), transitivity would NOT automatically hold, and FR-D8 would need actual new code, not just a new test — a materially different and larger scope than "add the missing test."
- **Recommendation**: Before implementation, an engineer with access to `d0f65482`'s actual diff (once the branch precondition in MAJ-002 is resolved) must read `Inherit`'s implementation line-by-line and confirm the copy is transitively inclusive, not just re-assert the prose claim. If the assumption is wrong, FR-D8 needs to become a real code change, not a test-only addition.

---

#### [MAJ-005] No requirement bounds delegation-chain fan-out (breadth), only depth

- **Lens**: Incompleteness (resource limits, SEC-09)
- **Affected section**: Edge Cases; FR-D3 (delegation-policy gate: "trust set, modes, depth")
- **Description**: The spec discusses depth extensively (MaxDepth, three-level chains) but never addresses breadth — how many children a single parent can spawn concurrently via repeated `delegate` calls within one turn or one session. "Any agent... calls `delegate`" (FR-D4) with no per-turn or per-session call-count limit mentioned anywhere in this spec (the pre-existing delegation-policy gate's "modes" may or may not cover this — it's referenced as "FR-6.2, unchanged" without restating what it actually bounds).
- **Impact**: An agent (or a compromised/misbehaving one) could call `delegate` in a tight loop, spawning unbounded concurrent sub-turns, each consuming an LLM call, memory, and (per `bash-tool-spec.md`'s User Story 2) potentially its own background `bash` sessions — a resource-exhaustion vector (STRIDE: Denial of Service) this spec doesn't rule in or out.
- **Recommendation**: State explicitly whether fan-out is already bounded by the existing "FR-6.2" delegation-policy gate (and if so, name the specific limit/mechanism, mirroring this spec's otherwise-precise sourcing convention) or add a new requirement if it is not.

---

### MINOR Findings

#### [MIN-001] "Copy-at-spawn, not live reference" (Explicit Non-Behaviors) has no test for a grant *revoked* after spawn, only one for a grant *added* after spawn

- **Lens**: Incompleteness
- **Affected section**: Explicit Non-Behaviors; Acceptance Scenario 4 (User Story 2); Dataset row 4
- **Description**: The spec thoroughly covers "a grant recorded on the parent after a child has already been spawned... does not retroactively apply." It does not cover the mirror case: if an operator explicitly revokes/clears a grant on the parent (is there a revoke operation? `ClearSession` is mentioned only as clearing an entire session, not a single grant) after a child was spawned with that grant already inherited, does the already-running child keep the now-revoked capability indefinitely? This is the security-relevant direction (a revoked capability surviving in a live child) versus the already-covered direction (a new capability not retroactively granted), and the two are not symmetric in risk.
- **Recommendation**: Add an edge case and acceptance scenario for grant revocation post-spawn, or state explicitly that no revoke-a-single-grant operation exists today (only whole-session `ClearSession`) and that this asymmetry is accepted.

---

#### [MIN-002] "Any agent... including the main/orchestrating agent" (FR-D4) doesn't address self-delegation depth accounting

- **Lens**: Ambiguity
- **Affected section**: Edge Cases ("What happens when the SAME agent ID is spawned as its own child")
- **Description**: The self-delegation edge case addresses grant-inheritance idempotency but not whether self-delegation counts against `MaxDepth` the same as delegating to a different agent. If an agent can delegate to "a copy of itself" repeatedly, does each hop still increment depth (eventually hitting the — now-clarified-as-uncapped-by-default, per MAJ-001 — limit), or is self-delegation exempt?
- **Recommendation**: State explicitly that self-delegation increments depth identically to any other delegation (presumably true, but not stated).

---

#### [MIN-003] Evaluation Scenario "operator explicitly denies a tool... main agent... delegates to it" duplicates Acceptance Scenario 2 rather than adding new coverage

- **Lens**: Overcomplexity (test redundancy)
- **Affected section**: Evaluation Scenarios (Holdout), second scenario
- **Description**: This holdout scenario is structurally identical to User Story 2's Acceptance Scenario 2 (child `deny` policy overrides inherited grant) — it substitutes "the main agent" for "a parent agent" and "a specific worker agent" for "a child agent," but proves the same property. Given holdout scenarios are meant for genuinely independent post-implementation evaluation, a near-duplicate doesn't add coverage value.
- **Recommendation**: Either drop this holdout scenario or sharpen it to test something the acceptance scenario doesn't — e.g., a *chain* of specialized workers with mixed policies, not a single main→worker hop already covered.

---

#### [MIN-004] Traceability matrix's FR-D7 row correctly defers to `async-notifier-spec.md` but that spec's own FR-N9 doesn't reference `delegate` by its final name

- **Lens**: Inconsistency
- **Affected section**: Traceability Matrix, FR-D7 row; cross-reference to `async-notifier-spec.md`'s FR-N9
- **Description**: `async-notifier-spec.md`'s FR-N9 is scoped to `bash`'s completion path only ("`bash`'s background-completion path... MUST call `AsyncNotifier.Notify` with `SourceKind: "bash"`"). It never states the equivalent requirement for `delegate`'s async completion (i.e., no `FR-N-something` with `SourceKind: "delegate"` or `"subagent"`), even though FR-D7 here says async completion "MUST reuse `AsyncNotifier.Notify`... unchanged from how `spawn`'s existing callback already does" — implying the async-notifier spec should have a mirroring requirement, but it doesn't name one.
- **Recommendation**: Add an FR-N10 (or similar) to `async-notifier-spec.md` explicitly requiring `delegate`'s async path to call `Notify` with `SourceKind: "delegate"`, matching FR-N9's treatment of `bash`, so the traceability is genuinely bidirectional rather than asserted one-directionally from this spec alone.

---

### Observations

#### [OBS-001] FR-D1's schema doesn't state whether `agent_id` is validated against the delegation allowlist at schema-validation time or only at delegation-policy-gate time

- **Lens**: Ambiguity
- **Suggestion**: Minor implementation-detail gap — worth a one-line clarification for consistency with how the spec otherwise separates "schema validation" from "policy gate" concerns elsewhere (e.g., `cwd`'s validation-before-any-subprocess framing in the companion `bash-tool-spec.md`).

#### [OBS-002] The "grandparent → parent → grandchild" test naming doesn't clarify whether the *parent* itself must also be proven to still work normally (non-`ask` case) mid-chain

- **Lens**: Incompleteness
- **Suggestion**: FR-D8's test proves the grandchild's `ask`-policy tool is auto-approved. It doesn't explicitly assert the parent's own tool calls (using its own, non-inherited policy) are unaffected by carrying a passed-through inherited grant it never directly exercises. Likely fine, but worth an explicit assertion for completeness.

#### [OBS-003] Evaluation Scenario "A very long-running async delegation, checked on repeatedly over several minutes" has no defined poll interval floor/ceiling

- **Lens**: Incompleteness
- **Suggestion**: Consistent with `bash-tool-spec.md` not bounding concurrent background sessions (MAJ-005 there), this spec doesn't bound how frequently `action: status` can be polled — not a blocker, but worth cross-referencing if a rate limit is ever added to one tool, for consistency with the other.

---

## Structural Integrity (Variant A: Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1: 4 scenarios, US-2: 5 scenarios |
| Every acceptance scenario has BDD scenarios | PASS | |
| Every BDD scenario has `Traces to:` reference | PASS | |
| Every BDD scenario has a test in TDD plan | PASS | 11 tests enumerated |
| Every FR appears in traceability matrix | PASS | FR-D1–FR-D8 all present |
| Every BDD scenario in traceability matrix | PASS | |
| Test datasets cover boundaries/edges/errors | PARTIAL | Breadth/fan-out (MAJ-005) and revocation (MIN-001) absent |
| Regression impact addressed | PASS | Three existing behaviors mapped with explicit "no new regression test needed" reasoning |
| Success criteria are measurable | PASS | SC-001–SC-004 all quantified ("100%", "at least one", "two delegation hops") |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Fan-out/breadth limits | No test bounds concurrent delegation calls per turn/session | User Story 1 |
| Grant revocation post-spawn | No test for revoking (not just adding) a grant after a child is spawned | User Story 2 |
| Depth-limit enforcement (given MAJ-001) | No test proves *any* enforced ceiling exists if the real default is uncapped | Edge Cases |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| Child policy × parent grant presence matrix | Revoked-grant row | Add: parent revokes grant post-spawn → child (if still running) retains it or not — state which |
| (none exists) | Depth beyond 3 (e.g., 5 or 10 hops) if default is truly uncapped | Add per MAJ-001's resolution |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `delegate` tool call path | ok | ok | ok | ok | **risk** | ok | Unbounded fan-out/depth (MAJ-001, MAJ-005) is a DoS vector nothing in this spec bounds |
| `ApprovalGrantStore.Inherit` (as depended-upon, not modified) | ok | ok | ok | ok | ok | ok | Well-specified for the depend-on relationship, contingent on MAJ-002/MAJ-004 being resolved first |
| `task_id`/status scoping | ok | ok | ok | ok | ok | ok | Cross-conversation isolation explicitly tested (Edge Cases, Evaluation Scenarios) — good |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Is `SubTurn.MaxDepth`'s real-world default (0/uncapped, per MAJ-001) an intentional product decision, or an oversight that should be fixed as part of this change?
2. Does the delegation-policy gate's "modes" (FR-D3, "FR-6.2, unchanged") already bound concurrent fan-out, or is that a genuinely open gap (MAJ-005)?
3. Which of `tool-consolidation-spec.md` (says `subagent`) or this spec (says `delegate`) should an implementer trust if they only read one? (MAJ-003) — needs an explicit reconciliation, not an implicit "the newer one wins" assumption.
4. Is there a single-grant revoke operation today, or only whole-session `ClearSession`? If the former doesn't exist, is it in scope for a future spec, or is "no way to revoke a specific grant early" an accepted permanent limitation?
5. Given `d0f65482` isn't in the local tree (MAJ-002), has anyone actually re-run `TestApproveTool_PolicyDenyOverridesGrant`/`TestApproveTool_PolicyAllowNeverPrompts` against the code this spec will actually be implemented on top of, or is "already verified in-session" describing verification against a different checkout than the one implementation will start from?

---

## Verdict Rationale

No CRITICAL findings — this spec's core design (one tool, fixing the disconnected status-read bug, formalizing already-largely-correct grant-inheritance semantics) is sound in shape. But MAJ-001 (wrong depth-default claim, undermining the very test meant to validate the depth story) and MAJ-002 (the entire second half of the spec depends on a commit verified absent from the working tree it was written in) are not cosmetic — they mean an implementer following this spec exactly as written could build a "bounded to 3" mental model that's false, and could start work in an environment missing the foundation the spec assumes is already there. MAJ-003's naming conflict with the sibling `tool-consolidation-spec.md` is a straightforward but real integration-breaking risk given both documents are dated the same day and both claim implementation-readiness.

These are all REVISE-class: none require re-architecting the tool merge or the grant-inheritance design itself, but all three must be corrected in the text before an implementer picks this up, or the resulting implementation risks silently diverging from what the spec's own success criteria assume.

### Recommended Next Actions

- [ ] Correct the `SubTurn.MaxDepth` default claim and its downstream framing (MAJ-001)
- [ ] Add the `d0f65482` precondition check to the spec header (MAJ-002)
- [ ] Reconcile the tool name with `tool-consolidation-spec.md` (MAJ-003)
- [ ] Verify FR-D8's "no new logic required" claim against the actual `Inherit` implementation once accessible (MAJ-004)
- [ ] Add an explicit fan-out/breadth bound or confirm one already exists via FR-6.2 (MAJ-005)

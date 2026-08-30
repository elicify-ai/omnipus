# Adversarial Review: Tool Manifest Tier Redesign (Round 2)

**Spec reviewed**: `docs/internal/specs/tool-manifest-tier-redesign-spec.md` (Revision 2)
**Source ADR**: `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` (revision 5)
**Review date**: 2026-08-28
**Verdict**: BLOCK

## Executive Summary

This is Round 2. Round 1 (embedded in the spec's own "Clarifications" section, and in the superseded prior contents of this file) returned BLOCK on 1 CRITICAL / 6 MAJOR / 3 MINOR / 2 OBSERVATION; every one of those is verifiably resolved in Revision 2, and this pass independently re-confirmed the fix to the headline CRITICAL (the always-listed diff is now correctly stated as 6-out/5-in, matching ADR-071 §4.1 and the spec's own worked-example table). This round found **1 new CRITICAL and 1 new MAJOR finding**, both structural in the same way as Round 1's: the spec is exhaustively precise about facts it chose to state, and silent about a distinction the source ADR treats as its own single most important corrected error. The CRITICAL is a plain textual contradiction — `FR-037` and the "must not expire, evict, or withdraw" prohibition are worded as unconditional, yet the same requirement's own traceability row cites a test that requires exactly the eviction path the requirement forbids — and it is unresolvable from the spec text alone because the identifiers that would disambiguate it (`IsCore`, `PromoteTools`, `TickTTL`, `cfg.Tools.MCP.Discovery.TTL`) never appear anywhere in the document, despite ADR-071's own revision history naming the conflation between these two mechanisms as its worst defect (CRIT-101).

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 1 |
| MINOR | 6 |
| OBSERVATION | 4 |
| **Total** | **12** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] `FR-037` and the "no eviction" prohibition contradict the spec's own required decay test for external tools

- **Lens**: Inconsistency (cross-cutting with Infeasibility and Ambiguity)
- **Affected section**: `FR-037` ("A tool made usable MUST remain usable for the remainder of the conversation. The system MUST NOT build any expiry, eviction or unload path for it."); the Qualitative Prohibitions list ("The system **must not** expire, evict, or otherwise withdraw a tool that has been made usable during a conversation..."); the BDD scenario "A discovered external tool does decay with the discovery lifetime" (line 1039); Test Implementation Order row 65, `TestExternalPromotion_DecaysWithDiscoveryLifetime`; the `FR-037` traceability row, which cites that same test.
- **Description**: Both the requirement text and the qualitative prohibition are written as absolute and unconditional — no scope qualifier limits them to a subset of tools. Yet the very same requirement's own traceability row (line 2196) cites `TestExternalPromotion_DecaysWithDiscoveryLifetime`, and the BDD scenario it traces to states plainly: "an agent that made an externally-provided tool usable... more turns pass than the configured discovery lifetime... the tool is no longer usable." That is exactly the "expiry, eviction, or withdrawal" the same requirement forbids one sentence earlier.

  Reading ADR-071 directly resolves this: the ADR's own §1.1.1 (its own headline corrected defect, CRIT-101 in its revision history) explains that there are **two structurally independent promotion mechanisms** — the new `loadedTools` session map this spec builds, which is genuinely permanent and undecaying by design for static catalog tools (`IsCore` entries), and the **pre-existing, unrelated** MCP registry TTL (`cfg.Tools.MCP.Discovery.TTL`, default 5, via `PromoteTools`/`TickTTL`), which already decayed MCP-provided tools before this feature and continues to, untouched. So "MUST NOT build any expiry... path" is scoped, in the ADR's own reasoning, to *not building a new one* for the mechanism this spec introduces — it was never meant to reach into the pre-existing MCP TTL path this spec leaves alone.

  None of that scoping survives into the spec. The term "externally-provided tool" is used exactly twice (line 1044, and the Exposure-level dataset row 10) and is never defined. "Discovery lifetime" is used exactly twice (lines 1033, 1045) and is never pinned to a config key, a default value, or a mechanism. `IsCore`, `PromoteTools`, `TickTTL`, and `cfg.Tools.MCP.Discovery.TTL` do not appear anywhere in this document (confirmed by grep across the full file). This is precisely the class of omission the spec's own **Pinned Identifiers** table exists to prevent — "every load-bearing identifier this specification refers to by description, with its literal value... so no implementer has to infer one or re-derive it from the ADR" — and it is the one identifier pairing the ADR itself flags as the single most consequential correction in its own five-revision history.
- **Impact**: Two competent engineers reading only this spec build two different, both-defensible, both-wrong things. One reads `FR-037` literally, ships no decay path for anything, and either skips `TestExternalPromotion_DecaysWithDiscoveryLifetime` entirely or watches it fail — a requirement in the Test Implementation Order the spec elsewhere treats as unconditionally required (not gated, unlike the Story 7 cache work). The other builds *some* eviction/decay mechanism to satisfy that test, and — absent the missing identifiers telling them to reuse the existing MCP-only TTL path — risks wiring it against the new `loadedTools` map too, which is the exact static-tool eviction the Qualitative Prohibition (with no external-tool carve-out written down) forbids and which several other requirements and User Story 4's headline "permanent for the session" property depend on. Either path can pass every automated gate the spec defines, because nothing in the document actually asserts the *scope boundary* between the two mechanisms — only the ADR does, off-page.
- **Recommendation**: Add the missing scope qualifier to `FR-037` and to the Qualitative Prohibition — e.g., "...MUST NOT build any new expiry, eviction or unload path **for a tool promoted from the static catalog**; this does not extend to the pre-existing MCP-tool discovery TTL (`cfg.Tools.MCP.Discovery.TTL`, unchanged by this work), which continues to govern externally-provided tools exactly as it does today." Add one row to the Pinned Identifiers table naming `IsCore` / `PromoteTools` / `TickTTL` / `cfg.Tools.MCP.Discovery.TTL` as "the pre-existing, untouched decay mechanism that governs MCP-provided tools only — never confuse with the new, permanent `loadedTools` map this spec introduces for static tools," citing ADR-071 §1.1.1. Define "externally-provided tool" once, in the Pinned Identifiers table, as "a tool registered via `RegisterHidden` from a connected MCP server (non-core, `TTL 0` at registration)." Until this lands, `TestExternalPromotion_DecaysWithDiscoveryLifetime` and `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` cannot both be written against a self-consistent reading of `FR-037`.

---

### MAJOR Findings

#### [MAJ-001] `FR-031`'s traceability row cites a test that verifies only half of its own claimed scenario coverage — the exact failure mode the spec's own matrix caution warns about

- **Lens**: Inconsistency / Test Coverage Gap
- **Affected section**: `FR-031` traceability row (line 2190): *"FR-031 | US-4 | An unlisted tool is found by description; Every search-only tool remains findable | `TestVisibility_SearchOnlyToolsRemainInSearchIndex`"*; the BDD scenario "An unlisted tool is found by description" (line 1004); Test Implementation Order row 52, whose own row (line 1796) traces `TestVisibility_SearchOnlyToolsRemainInSearchIndex` only to the scenario "Every search-only tool remains findable."
- **Description**: `FR-031` bundles two distinct claims — "MUST remain present in the searchable index" (a static membership property) and, via the cited BDD scenario, "an unlisted tool is found by description and its schema is present in the next turn's callable set" (a dynamic promotion behaviour). The row cites exactly one test for both, and that test's own definition (row 52) traces only to the static-membership scenario. There is no test in the Test Implementation Order individually named or traced against "a search-only tool is found and made usable by a by-description search" as a Story-4-scoped behaviour; the closest candidates (`TestAmbiguity_DominantHitPromotesAlone`, and the pre-existing `pkg/tools/load_tool_test.go` regression test cited in the Regression Test Requirements table) are not cited from `FR-031`'s own row.

  The spec explicitly warns readers about exactly this pattern in its own "A caution about this matrix specifically" paragraph (line 2250): *"A test name that mentions the same nouns as a requirement is not evidence that it exercises that requirement... read each cited test's own row... and confirm the path and the outcome match, not just the subject."* Applying that instruction to `FR-031`'s own row surfaces this gap — the spec did not apply its own stated discipline to every row it added.
- **Impact**: Likely a documentation-only gap rather than a missing behaviour — the promotion mechanism is almost certainly exercised as a side effect of `TestAmbiguity_DominantHitPromotesAlone` and the existing `pkg/tools/load_tool_test.go` regression suite. But as written, an implementer or reviewer checking `FR-031` off the traceability matrix (which is the explicit purpose of that matrix) would conclude the dynamic promotion behaviour is covered by a test that in fact only asserts static index membership, and would not know to look at the Regression Test Requirements table for the actual coverage.
- **Recommendation**: Split `FR-031`'s traceability row citation: keep `TestVisibility_SearchOnlyToolsRemainInSearchIndex` for the static-membership half, and add an explicit second citation — either a new, small integration test or an explicit pointer to `TestAmbiguity_DominantHitPromotesAlone` plus the `pkg/tools/load_tool_test.go` regression row — for "found and made usable by description." While auditing this row, also tighten the adjacent `FR-037` row: it cites `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` for the plain scenario "A tool made usable stays usable for the conversation," which is a *stronger* claim than that scenario needs (surviving past the discovery-lifetime boundary implies surviving the simple multi-turn case) — probably fine as a subset relationship, but the row should say so explicitly rather than relying on the reader to notice.

---

### MINOR Findings

#### [MIN-001] `FR-051`'s "strongly recommended when handing off" does not state whether it also applies to the return-to-default direction

- **Lens**: Ambiguity
- **Affected section**: `FR-051`: *"The explanatory note MUST be declared optional, and its description MUST state both the forward-looking and the backward-looking obligation and that it is strongly recommended when handing off."*
- **Description**: The sentence requires the note's description to state both obligations (forward-looking brief, backward-looking report) but then qualifies "strongly recommended" with "when handing off" — which reads naturally as scoping the recommendation to the forward direction only. Nothing elsewhere resolves whether the backward-looking (return-to-default) note is equally "strongly recommended" or merely optional-with-no-particular-encouragement. Acceptance Scenario 9 and dataset row 11 confirm omission succeeds on *some* branch but do not distinguish the two.
- **Recommendation**: Reword to state explicitly whether "strongly recommended" governs both destination branches or only the named-target one, e.g.: "...and that providing it is strongly recommended on both branches" or "...and that it is strongly recommended only when handing off to a named agent."

---

#### [MIN-002] No scenario or dataset row ties a timed-out switch to the "failed switch → no signal" rule

- **Lens**: Incompleteness
- **Affected section**: `FR-052` ("The operation MUST be bounded by a 10-second ceiling, on both destination branches"); `FR-061` ("A failed switch MUST produce no active-agent signal"); Test #20, `TestSwitchAgent_TimeoutCeilingOnBothBranches`.
- **Description**: Every other failure mode in User Story 5 (unknown identifier, worker target, unresolvable active agent) gets its own dedicated BDD scenario and dataset row explicitly stating the observable outcome. A timeout does not: there is no scenario titled anything like "A switch that exceeds its ceiling produces no signal," and no dataset row exercises it. It is reasonably inferable that a timeout counts as "a switch that fails" under `FR-061`, but this is inferred, not stated — unlike every comparable failure path in this story.
- **Recommendation**: Add one BDD scenario ("A switch that exceeds the 10-second ceiling emits no active-agent signal") and one dataset row to the Agent-switch destination resolution table, or add an explicit sentence to `FR-052` stating that a timeout is treated as a failed switch for the purposes of `FR-061`.

---

#### [MIN-003] The "22 lines" / "~101 lines" render arithmetic is not marked as independently verified, unlike every comparable count in the spec

- **Lens**: Infeasibility
- **Affected section**: `FR-033`, `SC-001` ("exactly 22 lines"); Exposure-level classification dataset rows 6–7 ("2 header + 6 categories × 2 + 8 entries" = 22; "2 + 14 × 2 + 71" = 101).
- **Description**: Every other numeric fact this spec treats as load-bearing (18-entry `fullManifestToolNames`, 90/92/89-name catalog counts, 90-line `allStaticToolNames` range) is explicitly annotated "Verified in this session" with the counting methodology stated, precisely because the spec's own Round-1 CRITICAL showed that plausible-looking arithmetic can be wrong while still passing a count-only check. The 22-line and 101-line breakdowns carry no such annotation — they appear only as a parenthetical formula in a dataset "Notes" column, with no citation of the actual category-grouping logic in `BuildCompressedManifest` that would confirm "6 categories" (for the 8 previewed tools) and "14 categories" (for the reverted 71-tool listing) are the real category counts rather than an assumption carried over from the ADR.
- **Recommendation**: Either mark this arithmetic "Verified in this session" with the method (e.g., a dry run of the renderer against the pinned 8-name and 71-name sets), or explicitly flag it as an ADR-sourced estimate not independently re-derived — consistent with how the spec treats every other precise, gate-defining number.

---

#### [MIN-004] `FR-038`'s "5-turn" horizon is not related to the pre-existing, operator-configurable MCP discovery TTL that shares the same default

- **Lens**: Incorrectness
- **Affected section**: `FR-038` ("...goes unused for more than 5 turns..."); ADR-071 (per direct review of the ADR text): "5 keeps r3's stated intent... on the same number the old TTL claim used" — i.e., `cfg.Tools.MCP.Discovery.TTL` also defaults to 5.
- **Description**: The spec pins "5" as a bare literal with no cross-reference to the existing, already-operator-configurable `cfg.Tools.MCP.Discovery.TTL`, which the ADR itself notes shares the same default value. The spec never states whether this is a coincidence, a deliberate independence (two separately-tunable constants that happen to start equal), or an implicit coupling an operator would reasonably expect (if they have already tuned their MCP TTL away from 5, does the unused-discovery horizon track that value or stay fixed at the literal 5?). This is the same missing-identifier problem as CRIT-001, in a second location.
- **Recommendation**: State explicitly, next to `FR-038`, whether the 5-turn horizon is intentionally decoupled from `cfg.Tools.MCP.Discovery.TTL` (two independent constants, coincidentally equal today) or intentionally derived from it. If decoupled, say so in one sentence so a future reader does not "fix" the apparent duplication by merging them.

---

#### [MIN-005] The audit-trail asymmetry between the two switch destinations is repudiation-relevant and worth naming as a tracked item now that the tools merge

- **Lens**: Insecurity (STRIDE — Repudiation)
- **Affected section**: `FR-055` ("The recorded-entry identity stamping MUST remain asymmetric between the two branches, and the recorded content prefix for a named destination MUST remain exactly `Handoff: `"); Scenario "A return to default transfers no brief."
- **Description**: Only the named-target switch branch is guaranteed a recorded, frozen-prefix conversation entry (`Handoff: `). The spec correctly identifies this asymmetry and requires it to survive unchanged (this is a pre-existing characteristic, not something this work introduces, and the spec is right not to silently "fix" it mid-rename per its own scope-creep prohibition). But merging two previously-separate tools into one capability changes how visible that asymmetry is: two tools with two different audit behaviours reads as an artifact of history; one capability with an asymmetric audit trail on its two branches reads, to a future reader, like a bug. Given Story 5's own framing ("The merge is security-relevant"), this is worth a one-line acknowledgement.
- **Recommendation**: No behavioural change needed. Add one sentence near `FR-055` (or as a new line item alongside `FR-066`/`FR-067`, which already track two other known-and-deliberately-deferred defects from this same surface) recording that the audit asymmetry is known, pre-existing, and intentionally preserved rather than silently carried forward — so a future reader doesn't file it as a fresh defect, and so it's visible as a candidate for its own follow-up if the product ever wants symmetric audit coverage.

---

#### [MIN-006] The cache-boundary story has no ongoing production signal after its one-off pre-merge measurement

- **Lens**: Inoperability
- **Affected section**: `FR-085` ("The measurement is manual, operator-owned... It MUST NOT be added to continuous integration"); Development Setup / Integration Boundaries (Anthropic API, conditional cache story only).
- **Description**: The gate for shipping Story 7 is a single manual measurement, taken once, pre-merge. The offline structural checks (`TestContextBlocks_CatalogAtIndexOne` and siblings) guard against a *code* regression moving the cached block, but nothing in the spec provides ongoing production visibility into whether the cache boundary is still delivering the claimed benefit — e.g., if a future Anthropic API change alters the minimum cacheable prefix, cache lifetime, or tokenizer behaviour, there is no live signal that would surface a silent regression to zero benefit. This is explicitly and reasonably out of CI by design; the gap is the absence of any *production* signal at all, not the absence of a CI gate.
- **Recommendation**: Either accept this explicitly as "measured once at merge, never rechecked" (a one-sentence statement to that effect would remove the ambiguity), or note a lightweight follow-up: since `providers.Usage.CacheReadTokens` is already read for the one-off measurement, the same figure could be sampled into an existing metrics/logging path for passive ongoing visibility without any new CI requirement.

---

### Observations

#### [OBS-001] The bash/navigate/create_task/update_task tier move is a bigger round-trip-latency change than "moves down a tier" suggests on first read

- **Lens**: Ambiguity (informational)
- **Affected section**: "Reclassified tools land at the intended level" table; User Story 4 narrative.
- **Suggestion**: Moving from "always-listed" to "previewed" is not merely a cosmetic visibility change — per `FR-030`, "previewed" is a subdivision of the pre-existing *Lazy* manifest tier, meaning these four tools go from zero-round-trip, full-schema-every-turn callable to requiring a `ToolSearch` round trip before first use in a fresh conversation (persisting for the rest of that conversation per `FR-037`). The spec correctly defers the underlying product judgement to ADR-071 §4.1 by design ("it does not re-argue them"), so this is not a defect — but a reviewer skimming the reclassification table alone could underestimate the friction this adds to routine, high-frequency task-management workflows on the first tool call of every new conversation. Worth a one-line callout at the point of the reclassification table, purely for reader calibration.

---

#### [OBS-002] The reserved word `"default"` is only checked for collision on the `switch_agent` and agent-CRUD paths

- **Lens**: Insecurity (STRIDE — Elevation of Privilege, informational)
- **Affected section**: `FR-056`–`FR-058`.
- **Suggestion**: The spec thoroughly covers collision resolution and (gated) creation-time rejection for the `switch_agent` destination parameter and the agent-identity CRUD path. It does not state whether any *other* pre-existing agent-identifier lookup path in the codebase (e.g., a REST route addressing an agent literally by ID) could also collide with the literal string `"default"`. This is very likely a pre-existing condition unrelated to this feature (the word was already reserved in the return-to-default sense before this merge) rather than something newly introduced — flagging only so it gets a one-line confirmation at ratification rather than being assumed away.

---

#### [OBS-003] `FR-039`'s two-structure requirement is a documentation-fidelity choice, not a technical necessity

- **Lens**: Overcomplexity
- **Affected section**: `FR-039` ("The pending-discovery record and the usable-set record MAY remain two structures, matching the source ADR's own naming of them as separate maps... **both MUST be swept by one function**...").
- **Suggestion**: The spec's own text acknowledges the two-map design exists "matching the source ADR's own naming," not because a single map keyed by `(agent, conversation)` with a richer per-tool value (usable flag + promotion turn + counted flag) couldn't do the same job with one fewer moving part and no two-structure-sweep discipline to maintain. This is a reasonable, low-cost choice (and the one-sweep-function requirement correctly closes the desync risk either way), but it is worth being explicit that it's a fidelity-to-source-document choice rather than a constraint implementers are bound to preserve if a future refactor wants to collapse the two maps.

---

#### [OBS-004] Traceability-matrix precision worth a second pass beyond the two rows this review flagged

- **Lens**: Inconsistency (informational)
- **Affected section**: The full "Traceability Matrix" section.
- **Suggestion**: MAJ-001 found one row (`FR-031`) where the cited test's own definition traces to a narrower scenario than the FR row claims coverage for. Given the matrix has ~85 rows and this review sampled the pattern rather than exhaustively cross-checking every row against every cited test's own Test-Implementation-Order definition, a mechanical pass (script-assisted, as this review did for structural completeness) cross-referencing each row's cited test against that test's own "Traces to BDD Scenario" column would be cheap insurance against further instances of the exact failure mode the spec's own "caution about this matrix" paragraph describes.

---

## Structural Integrity (Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | All 7 stories carry an Acceptance Scenarios section. |
| Every acceptance scenario has BDD scenarios | PASS | Spot-checked across all 7 stories; outline scenarios cover multiple acceptance criteria explicitly via their Examples tables. |
| Every BDD scenario has a `Traces to:` reference | PASS | Confirmed for all 88 scenario headings. |
| Every BDD scenario has a corresponding test in the TDD plan | PASS, with caveats | See MAJ-001 and OBS-004 — coverage exists but two rows cite a test narrower than the claim, rather than the correct or a more precise test. |
| Every FR appears in the traceability matrix | PASS | Verified programmatically: all 84 defined `FR-xxx`/`FR-xxxa` ids appear in the matrix. |
| Every BDD scenario appears in the traceability matrix | PASS | Matches the spec's own completeness check; the two named exceptions (cache-breakpoint count, provider-without-markers) are correctly cross-referenced to `SC-016`/`FR-082` instead. |
| Test datasets cover boundary conditions, edge cases, error scenarios | PASS | Zero/negative limits, empty rankings, ties, cross-agent isolation, concurrency, and Unicode/long-string inputs are all present. |
| Regression impact is explicitly addressed | PASS | Dedicated Regression Test Requirements and Regression Dataset sections, including two explicitly-preserved pre-existing defects. |
| Success criteria are measurable with no subjective language | PASS | All 21 `SC-xxx` entries carry concrete counts, thresholds, or byte/token-identical comparisons. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|------------------|---------------------|
| Switch timeout outcome | No scenario/dataset row ties a `switch_agent` timeout to the "failed switch → no signal" rule | `FR-052` / `FR-061` (MIN-002) |
| Static-vs-MCP decay boundary | No test or FR text distinguishes which tools the "no eviction" rule covers vs. the pre-existing MCP TTL path | `FR-037` (CRIT-001) |
| Cache-boundary production health | Only a one-off pre-merge manual measurement; no recurring or soak-level check | `FR-085` (MIN-006) |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|------------------------|-----------------|
| Agent-switch destination resolution | Timeout on either branch | Add a row: target resolves normally but the operation exceeds 10s → failure, no signal |
| Exposure-level classification and drift | Independent verification of the 22-line / 101-line render breakdown | Add a row (or an annotation on existing rows 6–7) marked "Verified in this session" with method, matching the rigor applied to every other count in the spec |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `ToolSearch` (discovery, US1/US2) | ok | ok | ok | ok (US1 closes the pre-existing leak) | ok (50-check cap, FR-006) | ok | The "did-you-mean" loss for externally-provided tools is a deliberate, recorded fail-closed trade (Ambiguity Warning #10). |
| `switch_agent` (US5) | ok | ok | **risk** (MIN-005) | ok | ok (10s ceiling, both branches) | ok (worker rejection, delegation exclusion, FR-053/FR-059) | Repudiation asymmetry between the two destination branches is pre-existing and explicitly preserved, not introduced here — flagged for visibility only. |
| Upgrade conversion (US6) | ok | ok (atomic replace) | ok | ok | ok | ok (strictest-wins fold, FR-071) | Concurrent-start convergence is explicitly dataset-tested (row 13–14); one-way rollback risk is the review's top-flagged item from Round 1 and remains correctly documented as OPERATOR. |
| Metrics endpoint | n/a | n/a | n/a | ok (counters only, no payload data) | n/a | n/a | Reuses the existing authenticated endpoint; no new attack surface. |
| Manifest cache boundary (US7, gated) | n/a | ok (position + marker structurally asserted) | n/a | ok | n/a | n/a | **risk** (MIN-006) — no ongoing production signal after the one-off manual gate. |

**Legend**: risk = identified threat not (or only partially) mitigated in the spec; ok = adequately addressed or not applicable.

---

## Unasked Questions

1. Is the `FR-038` 5-turn unused-discovery horizon intentionally independent of the pre-existing, operator-configurable `cfg.Tools.MCP.Discovery.TTL` (which shares the same default value today), or should the two move together? (CRIT-001, MIN-004)
2. Now that `hand_off` and `return_to_default` merge into one `switch_agent` capability, should the long-standing audit-trail asymmetry between the two destination branches be filed as its own tracked follow-up — the way `FR-066` and `FR-067` already track two other known, deliberately-deferred defects on this same surface? (MIN-005)
3. What is the operational plan if a future provider-side change (tokenizer, minimum cacheable prefix, cache lifetime) silently degrades the cache-boundary benefit after the one-off pre-merge measurement passes? Is any recheck ever expected, or is "measured once, trusted indefinitely" the accepted posture? (MIN-006)
4. Does `FR-051`'s "strongly recommended when handing off" apply to the return-to-default direction too, or only to a named-target switch? (MIN-001)
5. Does a `switch_agent` timeout observably behave as "a failed switch" for every purpose `FR-061` cares about (no signal, and — per Scenario "A failed switch emits no signal" — presumably an error surfaced to the caller), or does a timeout need its own distinct outcome? (MIN-002)

---

## Verdict Rationale

BLOCK, on CRIT-001 alone: `FR-037` and the Qualitative Prohibition against tool eviction are worded as absolute, yet the same requirement's own cited test requires the exact eviction behaviour the requirement forbids, and nothing in the document supplies the scope boundary (static-catalog tools vs. pre-existing MCP-TTL-governed tools) that would resolve the contradiction — that boundary exists only in ADR-071 §1.1.1, which this spec elsewhere goes out of its way to translate into concrete, pinned identifiers for every other load-bearing concept it touches. This is not a hypothetical risk: it is a textual self-contradiction on a requirement ID that both a "build no decay, ever" implementation and a "build decay, and risk it touching the wrong map" implementation can each satisfy exactly half of. MAJ-001 is lower stakes (very likely a documentation gap over a real gap in behaviour) but is worth fixing before this ships, given it is a live instance of the exact traceability-matrix failure mode the spec's own text warns readers to check for.

Everything else here is MINOR or OBSERVATION — this spec is materially stronger than Round 1, and every Round 1 finding independently re-checked as resolved.

### Recommended Next Actions

- [ ] Resolve CRIT-001: add the scope qualifier to `FR-037` and the Qualitative Prohibition, and add the missing `IsCore`/`PromoteTools`/`TickTTL`/`cfg.Tools.MCP.Discovery.TTL` row to Pinned Identifiers, citing ADR-071 §1.1.1.
- [ ] Resolve MAJ-001: split or correct `FR-031`'s traceability citation so the "found by description" half of its claim is covered by a test whose own definition actually traces to that scenario.
- [ ] Address MIN-001 through MIN-006 opportunistically; none block implementation on their own, but MIN-004 shares CRIT-001's root cause and is cheap to fix in the same edit.
- [ ] Consider OBS-004's suggestion of a mechanical traceability-matrix cross-check pass before this spec is next re-reviewed, given the matrix's size (~85 rows) and the two instances this pass found by sampling rather than exhaustive checking.

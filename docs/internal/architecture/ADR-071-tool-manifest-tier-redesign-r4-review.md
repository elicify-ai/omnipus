# Adversarial Review: ADR-071 — Tool manifest tier redesign (revision 4)

**Spec reviewed**: `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` (revision 4, 2026-08-27)
**Review date**: 2026-08-27
**Verdict**: REVISE

## Executive Summary

This is the fourth adversarial pass on this ADR (three prior reviews already produced 1 CRITICAL/6 MAJOR/5 MINOR/3 OBS, 0/4/3/3, and 2/2/1/1 respectively, all resolved in-document). Independent re-verification of this revision's highest-stakes claims — the `IsCore`/TTL gate in `pkg/tools/registry.go`, the complete 89-name tier partition against the live `allStaticToolNames` catalog, the `toolVisibility.ts` default-arm regression, and the migration-ordering hazard in `pkg/gateway/gateway.go` — found every one of them accurate against source at the current `release/v0.1.1` checkout. This pass's job was to find what three prior reviews and the operator's own re-derivations missed, and it found one genuinely new **CRITICAL**: the `ToolSearch` discovery mechanism this entire ADR is built on **discloses the name and full description of every BM25-ranked tool, including denied Tier 3 tools, with no policy filter at all** — a gap that directly undercuts §4.3's and §4.6's "invisible by default" risk-acceptance framing, which the document never states, checks, or bounds. Two MAJOR and one MINOR round out the new findings, all in mechanisms this revision itself introduced.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 2 |
| MINOR | 1 |
| OBSERVATION | 1 |
| **Total** | **5** |

---

## Findings

### CRITICAL Findings

#### [CRIT-201] `ToolSearch`'s match list is not policy-filtered — the discovery channel itself discloses denied Tier 3 tool names and descriptions

- **Lens**: Insecurity (Information Disclosure) / Incompleteness
- **Affected section**: §3.2 ("Schema-exposure widening: considered, bounded, and accepted"), §4.3 ("71% invisible by default"), §4.6 (bucket scoping to (agent, session))
- **Description**: `pkg/tools/tools_tool.go::execSearchAndLoad` (lines ~156–163) builds the `matches` array — `{Name, Description}` for **every** BM25-ranked result up to `maxSearchResults` (default 5) — directly from `ranked`, **before** any `canLoad`/policy check runs. `canLoad` is applied only when deciding which single (or, post-D2, up to three) candidate(s) get **auto-loaded** with a full schema; it is never applied to the `matches` list itself, which is always returned in full. Verified against source: when every ranked result is policy-denied, the code's own comment says so explicitly (`// No resolver or all results denied by policy.`) and still marshals and returns the full `matches` array with every name and description. `SnapshotSearchableTools` (`pkg/tools/registry.go`) confirms the BM25 corpus is built purely from `ToolManifestTier(name) == ManifestLazy` on the calling agent's own registry — with **no policy filtering at all** — so a tool the agent's policy denies is in the searchable corpus and can rank in `matches` on any query that scores it, intentional or not.

  §3.2 discusses "schema-exposure widening" at length and concludes "every promoted candidate passes `canLoad` first, so nothing enters the callable set... The delta is reachability of the schema, not of the capability." That reasoning is correct for the *loaded schema* set but does not apply to the *match list* — a different, unfiltered channel in the same tool result. The document never distinguishes these two disclosure surfaces, and only bounds one of them.
- **Impact**: Tier 3 (§4.1) is 63 tools, deliberately including destructive/administrative verbs (`delete_agent`, `delete_workspace`, `delete_task`, `disable_channel`, `remove_mcp_server`, `set_config`, and others). D3's whole accepted-risk framing (§4.3: "an agent that does not already suspect a capability exists has no zero-cost way to discover it") and §4.6's careful re-keying of the loaded-tools bucket to `(agent, session)` both assume that reaching a Tier 3 tool's name and purpose costs a deliberate, targeted `ToolSearch` call. In fact, **any** `ToolSearch` call on an unrelated topic can return the name and full description of a policy-denied destructive tool merely because it ranked in the top 5 BM25 results — no targeting required, no policy check applied, and the agent (or a prompt-injected instruction reading the tool result) now knows the tool exists and what it does, even though it can never load or call it. This is a real information-disclosure gap under STRIDE, and it is more severe after D3 than before: pre-D3, all 71 lazy tools are already named in the manifest block every turn, so this pre-existing gap in `execSearchAndLoad` was moot. Post-D3, 63 of those tools are deliberately hidden from the manifest specifically to reduce their visibility — and the discovery mechanism the ADR relies on to gate that visibility does not itself respect policy.
- **Recommendation**: Filter `matches` through `canLoad` (or a lighter existence/policy check that avoids the loadable-schema fetch) before it is returned, for both the `query` and any listing path in `execLoad`. At minimum: state explicitly in §3.2 or a new subsection that the match list is unfiltered today, decide whether that is accepted risk (and adjust §4.3's "invisible by default" framing accordingly, since it currently overstates the property) or fixed as a prerequisite of D3 shipping (the same "hard prerequisite" treatment §4.6 gave the bucket-scoping gap). Add a required test asserting that a `ToolSearch(query)` call from an agent whose policy denies a matching tool does **not** return that tool's name or description in `matches`.

---

### MAJOR Findings

#### [MAJ-201] §3.2's "administrative" cross-category exclusion has no canonical source and no drift test

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: §3.2, "One narrowing is adopted, cheaply"
- **Description**: The narrowing excludes a candidate from the speculative cross-category promotion band when "its category is administrative (`delete_*`/`remove_*`/`disable_*`/`set_config`)." The document does not say whether "category" here means `Tool.Category()` (the same mechanism `BuildCompressedManifest` uses to group tools under `## <category>` headings — which almost certainly returns values like `"workspaces"`/`"channels"`/`"tasks"`, not `"administrative"`) or a new, standalone name-prefix predicate invented for this narrowing alone. Whichever it is, no drift test or canonical list backs it. Contrast this with every other magic set in the document: `previewedLazyToolNames` gets a required drift test (§4.4, "Adding a tool must force a tier decision"); `allStaticToolNames` panics at boot on an unlisted override key (§5.3). This one has neither.
- **Impact**: A future destructive or administrative tool that does not begin with `delete_`, `remove_`, `disable_`, or literally equal `set_config` (e.g. a hypothetical `revoke_credential`, `purge_workspace_data`, or `wipe_session`) silently falls outside the narrowing the moment it is added, with no build-time or test-time signal — the exact "silent default" failure shape §4.4 built a drift test to prevent elsewhere in this same document.
- **Recommendation**: Specify the classification mechanism precisely (name it as either a `Tool.Category()` value or an explicit, exported name-prefix/set constant), and add a drift test mirroring §4.4's pattern — e.g., assert every Tier 3 tool name matching a documented "destructive verb" heuristic is covered, or maintain an explicit `administrativeToolNames` set alongside `previewedLazyToolNames` with the same coverage discipline.

---

#### [MAJ-202] `pendingSearchPromotions`'s outer key is unspecified relative to §4.6's bucket rekey

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: §4.3.1(a) ("State... One new map on `AgentLoop`... `pendingSearchPromotions map[string]map[string]int` — session bucket → tool name → the turn index"), cross-referenced against §4.6 ("Replace `manifestSessionID(transcriptID, sessionKey)` with `manifestBucketKey(agentID, transcriptID, sessionKey)`")
- **Description**: §4.6 replaces the session-only key (`manifestSessionID`) used by `loadedTools` with an `(agent, session)` composite key (`manifestBucketKey`), and is explicit that this must land with D3 (W3) because D3's "invisible by default" property is false without it. §4.3.1(a) introduces a second, parallel side table — `pendingSearchPromotions` — in the **same workstream (W3)**, written from the **same `markLoaded` closure** and swept at the **same call sites** (`forgetSession`, the per-turn `TickTTL` point) that §4.6 also touches. Yet §4.3.1(a) calls its outer key "session bucket" without saying whether that means the pre-§4.6 `manifestSessionID` or the post-§4.6 `manifestBucketKey`. Given both mechanisms are being built in the same pass and interact at identical call sites, an implementer has to guess — and the two plausible guesses have different behavior across a `switch_agent` handoff (a session-only key lets Agent B's promotion silently overwrite or extend Agent A's still-pending entry for a same-named tool; an (agent,session) key keeps them separate, consistent with §4.6's own reasoning for why the loaded-tools bucket needed the same fix).
- **Impact**: Left unresolved, whichever engineer implements W3 picks one interpretation without a specification to check against, and the two interpretations produce different, silently-diverging behavior for the no-followup counter across agent handoffs — the same class of "looks right, quietly disagrees with a sibling mechanism" defect this document has now found and corrected in itself three times (§1.1.1, §4.6, §6.4).
- **Recommendation**: State explicitly that `pendingSearchPromotions` is keyed by `manifestBucketKey(agentID, transcriptID, sessionKey)` (the same key `loadedTools` uses post-§4.6), not the legacy `manifestSessionID`. Add this as a fourth required test alongside §4.6's three: cross-agent isolation for the no-followup counter, mirroring the cross-agent isolation test already required for `loadedTools`.

---

### MINOR Findings

#### [MIN-201] The `no_followup` counter's revisit action is unspecified, unlike its sibling

- **Lens**: Infeasibility / Inoperability
- **Affected section**: §4.3.1(b), "Together these make the revisit trigger falsifiable and readable"
- **Description**: The stated action for `omnipus_toolsearch_zero_result_total` is concrete: "Tier 2 is too narrow and should widen." The stated action for `omnipus_toolsearch_no_followup_total` is only "means the promotions themselves are missing" — which restates the symptom, not a response. It is not said whether a rising no-followup count should tighten the §3.2 ambiguity ratios (fewer speculative promotions), shrink Tier 3 (fewer things to mis-promote), or something else.
- **Recommendation**: State the intended operator response to a rising `no_followup` count explicitly, the same way the zero-result counter's response is stated — even a provisional "tighten `searchAmbiguityRatio`/`searchCrossCategoryRatio` first; only widen Tier 2 if that alone does not bring it down" would close the gap.

---

### Observations

#### [OBS-201] The observability apparatus for a provisional, reversible risk is substantial

- **Lens**: Overcomplexity
- **Affected section**: §4.3.1 as a whole
- **Suggestion**: Two atomic counters, a `/metrics` wiring extension, a new per-turn side table with its own sweep logic, and a time-boxed config flag are all built to service one provisional, self-acknowledged-reversible risk (§4.3). The document defends this at length and each piece individually survives scrutiny (this review did not find grounds to cut any one of them), so this is recorded as an observation rather than a finding: worth a final gut-check at ratification on whether the full apparatus is proportionate, now that MAJ-202 adds one more piece of state to it.

---

## Structural Integrity (Variant C — Generic Markdown)

**Scope clarity**: Clear and explicit. §"Release-phase routing" states what this ADR is and is not blocked by; "Related, explicitly OUT of scope" names two adjacent issues and confirms neither is touched.

**Actors identified**: Yes — the operator (all tier/identity decisions), the architect (mechanism/thresholds), and every downstream consumer (Anthropic/OpenAI/Bedrock providers, the SPA, CI) are named with per-actor consequences throughout.

**Success criteria**: Mostly measurable. §6.6's D5 gate has a precise, numeric pass/fail threshold (`ΔC ≥ 0.8 × B`). §4.3.1's revisit trigger is falsifiable for the zero-result counter and only partially so for the no-follow-up counter (MIN-201).

**Failure modes**: Exceptionally well covered for the mechanisms this document already scrutinized four times over (TTL/reclamation, cross-agent leak, migration ordering, frame-emission regression). Not covered for the mechanism this review's CRIT-201 identifies — the document's failure-mode analysis for D2/D3 never considers the unfiltered match-list channel.

**Implementation detail**: High. Code-cited to `file::symbol`, with explicit reconciliation tables (§5.1.2) and normative parameter descriptions (§5.1.1) precisely where past revisions left gaps.

**Assumptions & constraints**: Explicit and repeatedly corrected against source (§1.1.1's TTL/`IsCore` correction, §12's resolved unverifieds). The one implicit assumption this review found — that `canLoad`-bounding the loaded set also bounds what a search discloses — is false and undocumented (CRIT-201).

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Policy-filtered search results | No test anywhere asserts that `ToolSearch`'s `matches` list excludes tools the calling agent's policy denies | CRIT-201 |
| Cross-agent isolation for the no-followup counter | §4.6 requires this test for `loadedTools`; no equivalent is required for `pendingSearchPromotions` | MAJ-202 |
| Drift coverage for the "administrative" exclusion set | No test fails when a new destructive tool bypasses the §3.2 narrowing | MAJ-201 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| `ToolSearch(query)` corpus | A query that BM25-ranks a policy-`deny` Tier 3 tool inside the top `maxSearchResults` without ranking it #1 | Add as the CRIT-201 regression test — assert the denied tool's name/description is absent from `matches` |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `ToolSearch` (query path) | ok | ok | ok | **risk** | ok | ok | CRIT-201 — unfiltered `matches` list discloses denied-tool names/descriptions |
| `ToolSearch` (loaded schema set) | ok | ok | ok | ok (bounded by `canLoad`, per §3.2) | ok | ok | Already analyzed and bounded in-document |
| `switch_agent` (D4) | ok | ok | ok | ok | ok | ok | Exclusion mechanism and its blast radius already exhaustively covered (§5.2–§5.2.3) |
| Legacy policy-key migration (§5.3 5a) | n/a | n/a | n/a | n/a | ok (fail-closed by design) | n/a | Already the document's own highest-severity finding; independently re-verified accurate against `pkg/gateway/gateway.go` in this pass |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Should `ToolSearch`'s `matches` list be policy-filtered, and if the operator decides not to (e.g., on the grounds that name+description disclosure is an acceptable cost), should §4.3's "71% invisible by default" language be corrected to reflect that visibility is a manifest-block property, not a true discovery-channel property?
2. Is the §3.2 "administrative" exclusion meant to track `Tool.Category()`, or is it a deliberately separate, narrower classification — and if the latter, who owns keeping its prefix list current as new tools are added?
3. Does `pendingSearchPromotions` use `manifestBucketKey` or `manifestSessionID`, and — if `manifestBucketKey` — does the `forgetSession` suffix-sweep specified in §4.6 point 4 also need to sweep this second map, or does it already, implicitly, by virtue of sharing `loadedToolsMu`?

---

## Verdict Rationale

REVISE, not BLOCK: unlike the r3 review's two CRITICALs (which invalidated the reasoning behind three whole sections), CRIT-201 is a gap the document never engaged with rather than a claim it got wrong, and it is fixable with a bounded, well-understood change (filter one array through an already-existing predicate) plus a decision about how to restate §4.3's risk framing. The two MAJOR findings are both specification gaps in mechanisms this revision itself introduced (§3.2's narrowing, §4.3.1(a)'s new side table) and are cheap to close before implementation starts, in the same spirit as this ADR's own repeated "state it explicitly so an implementer doesn't have to guess" discipline. Everything independently re-verified in this pass — the tier arithmetic against the live 89-name catalog, the `IsCore`/TTL semantics, the `toolVisibility.ts` regression, and the migration-ordering hazard — held up exactly as described, which is itself informative: three prior reviews already drove the document's factual accuracy very high, and this pass's yield came from asking a structurally different question ("what does the mechanism do that the document doesn't discuss?") rather than re-deriving what it already discusses.

### Recommended Next Actions

- [ ] Resolve CRIT-201: decide whether `ToolSearch`'s match list is filtered by policy or the risk is explicitly accepted and §4.3's framing is corrected to match; add the required test either way.
- [ ] Resolve MAJ-201: specify the "administrative" classification mechanism and add drift coverage.
- [ ] Resolve MAJ-202: state `pendingSearchPromotions`'s key scheme explicitly and add the cross-agent isolation test.
- [ ] Resolve MIN-201: state the intended operator response to a rising `no_followup` count.
- [ ] Re-run the §10 blanket-grep discipline this document already mandates for its own mechanism claims, now including `matches\[`, `execSearchAndLoad`, and `SnapshotSearchableTools` in the grep set, since CRIT-201 shows that discipline has not yet been pointed at the discovery-channel code itself.

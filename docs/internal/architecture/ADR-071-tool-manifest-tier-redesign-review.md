# Adversarial Review: ADR-071 — Tool manifest tier redesign

**Spec reviewed**: `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md`
**Review date**: 2026-08-27
**Verdict**: REVISE

## Executive Summary

ADR-071's own code citations were spot-checked against `release/v0.1.1` and hold up well — the
90/89-tool arithmetic, the tier partition, the TTL/promotion mechanics, and the `SnapshotSearchableTools`
break-risk in D3 are all verified correct. The ADR is unusually self-auditing (it flags its own risks
in §7 "Negative/accepted" and §12 "Unverified"). But the D4 tool merge (`switch_agent`) is under-specified
in ways that matter: it silently changes a required parameter to optional, silently collapses two
parameters with different semantics into one, doesn't say how two Execute bodies with materially
different logic get reconciled, and — most importantly — its own root-cause narrative for the
`websocket.go` blast radius only describes half of the actual bug. There is a second, untested,
unmentioned `if p.Tool == "hand_off"` branch three lines from the one the ADR does discuss; as written,
the rename breaks the active-agent UI indicator on every successful hand-off, not just on
return-to-default. 1 CRITICAL, 6 MAJOR, 5 MINOR, 3 OBSERVATION findings.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 6 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **16** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] §5.2's websocket.go blast radius describes only half the bug

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: §5.2 "Blast radius — this rename is security-relevant, not cosmetic"
- **Description**: §5.2 says: *"`pkg/gateway/websocket.go` decides whether the emitted
  `AgentSwitchedFrame` clears the active agent by testing `p.Tool == "return_to_default"` ...
  The condition must be re-expressed as an inspection of `switch_agent`'s `target`."* This is true
  but incomplete. `pkg/gateway/websocket.go:3878` has a **second, sibling** branch three lines above
  the one cited:
  ```go
  if p.Tool == "hand_off" && status == "success" {
      if activeAgent, ok := h.agentLoop.GetSessionActiveAgent(evtSID); ok {
          // ... emits AgentSwitchedFrame WITH the new agent set
      }
  }
  if p.Tool == "return_to_default" && status == "success" {
      // ... emits AgentSwitchedFrame with AgentId omitted (nil) = "returned to default"
  }
  ```
  Both branches gate on an exact tool-name string. After D1+D4, the tool name is `switch_agent` for
  both cases, so **both** conditions become permanently false, not just the one the ADR names. I
  grepped the entire gateway test suite (`pkg/gateway/*_test.go`) for `"hand_off"` and for
  `AgentSwitchedFrame`-adjacent assertions on the success path: there is no test file. `tests/e2e/handoff.spec.ts`
  mentions `hand_off` only in an unrelated prose comment (line 288, about `SubagentBlock`).
  `tests/integration/handoff_agent_id_test.go` drives a `hand_off` tool-call stream but does not
  assert on `agent_switched`/`AgentSwitchedFrame` at all. This branch has **zero** test coverage
  anywhere in the repository today, and it is absent from §5.2's "Tests" row.
- **Impact**: If an implementer fixes exactly what the ADR narrates (re-express the
  `return_to_default` branch), the `hand_off`-success branch keeps testing a string that no longer
  exists. Every successful `switch_agent(target: <agent>)` call — the common case, far more frequent
  than return-to-default — stops emitting `AgentSwitchedFrame` at all. The SPA's active-agent
  indicator freezes on whatever agent was active before the switch, silently, with no error and no
  failing test anywhere in the suite. This is a worse regression than the one the ADR explicitly
  worries about (which only affects the *clearing* case), and it is exactly the "looks right,
  measures as a no-op" failure mode §6.2 warns about elsewhere in this same document — just missed
  by the author in the one place it was already looking.
- **Recommendation**: Rewrite §5.2 to name **both** branches at `websocket.go:3878` and `:3899`
  explicitly by line reference, not just the second. Add an explicit acceptance test (integration or
  gateway-level) asserting `AgentSwitchedFrame` is emitted with the correct `AgentId` (set vs. nil)
  for both `switch_agent(target: "<agent-id>")` and `switch_agent(target: "default")`, keyed off
  `p.Tool == "switch_agent"` plus an inspection of the result/args — not tool name alone. Add this
  test to the required-changes list in §5.3 and to the "Tests" row of the blast-radius table.

---

### MAJOR Findings

#### [MAJ-001] `context` silently changes from required to optional, contradicting the ADR's own "preserved unchanged" claim

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: §5.1 — `switch_agent(target: string, context?: string)`, and the claim
  "`context` — optional handoff note, preserved unchanged from `hand_off`'s existing parameter."
- **Description**: I read `HandoffTool.Parameters()` (`pkg/tools/handoff.go:145-159`):
  `"required": []string{"agent_id", "context"}`. `context` is **required** today. The ADR's proposed
  signature marks it optional (`context?: string`) and explicitly claims this is "preserved unchanged."
  It is not unchanged — it is a behavior relaxation the ADR asserts didn't happen while its own
  proposed type signature shows that it did.
- **Impact**: An implementer trusting the "preserved unchanged" claim at face value could go either
  way — keep it required (contradicting the stated signature) or make it optional (contradicting the
  stated claim) — and there is no way to tell from the ADR which was intended, or whether the
  relaxation was a deliberate simplification or a drafting slip.
- **Recommendation**: State explicitly whether `context` becomes optional as a deliberate decision
  (and why — e.g., "a return-to-default has no natural addressee for instructions, so requiring a
  note doesn't make sense for that branch") or keep it required for the hand-off case and only
  optional for the `target: "default"` case (a param that's conditionally required depending on
  another param's value — call this out explicitly, since JSON Schema's `required` array can't
  express that conditional on its own and it needs either an `oneOf`/`if`-`then` construct or
  application-level validation).

---

#### [MAJ-002] The merged `context` parameter conflates two different semantics with no reconciling description

- **Lens**: Ambiguity / Incorrectness
- **Affected section**: §5.1
- **Description**: `HandoffTool`'s `context` parameter is described as *"Context or instructions to
  give the target agent about this conversation"* — forward-looking, addressed to the agent about to
  take over. `ReturnToDefaultTool`'s equivalent parameter is not called `context` at all — it's
  `summary`, described as *"Optional summary of what was accomplished before returning"*
  (`pkg/tools/handoff.go:379-388`) — backward-looking, a report of what already happened. The ADR
  merges these into one `context` field and states no new description for it, and never acknowledges
  that it is asking one parameter to serve two different communicative purposes depending on whether
  `target` names an agent or is the literal `"default"`.
- **Impact**: Without a description that covers both cases, the model has no guidance on what to put
  in `context` when returning to default vs. handing off — it will either follow the old `hand_off`
  framing (forward-looking instructions, nonsensical for a return) or invent something. This is a
  live UX regression risk buried in what §5.1 presents as a mechanical parameter rename.
- **Recommendation**: Either keep two logically distinct concepts under one field with an explicit
  dual description ("Optional: instructions for the target agent when switching to a specific agent,
  or a summary of work done when returning to default"), or reconsider whether `summary`'s intent is
  actually well-served by folding into `context` at all versus keeping a semantically neutral name
  like `note`.

---

#### [MAJ-003] D4 doesn't specify how two materially different `Execute` bodies get reconciled into one

- **Lens**: Incompleteness
- **Affected section**: §5.1, §5.2 (Tool impl row: "→ one `SwitchAgentTool`")
- **Description**: I read both `Execute` methods in full (`pkg/tools/handoff.go:162-260` and
  `:392-446`). They are not the same shape with different string constants swapped in — they diverge
  substantively:
  - `HandoffTool.Execute` performs an agent-existence check, a **worker-rejection check**
    (`reg.IsWorker(agentID)` — a worker must never become a live chat target), then a
    **token-budget-aware transcript context transfer**: it reads the full transcript, computes a
    per-agent context-window budget, splits recent/older messages by token budget
    (`splitByTokenBudget`), and builds a truncation summary line for the older portion.
  - `ReturnToDefaultTool.Execute` does none of that — no worker check (not needed, target is always
    the default agent), no transcript read, no token-budget split.

  The ADR frames this merge as following "the precedent this codebase has now set twice" (ADR-036's
  `bash`/`delegate` consolidations) and describes it purely as a parameter-shape unification. It
  never states whether `switch_agent(target: "default")` should also do the worker-check (moot,
  since default is never a worker, but worth stating so an implementer doesn't wonder) and — more
  importantly — whether it should **also** get the token-budget context transfer `hand_off` does, or
  keep the simpler no-transfer behavior `return_to_default` has today.
- **Impact**: This is exactly the kind of implementation decision `/plan-spec` and `/taskify` need
  answered before decomposition, and it's silent. Two equally plausible implementations (transfer
  context on every switch vs. only on agent-to-agent handoff) produce different runtime behavior and
  different token costs, and nothing in the ADR picks one.
- **Recommendation**: Add a subsection stating explicitly which of `HandoffTool`'s five execution
  steps (agent lookup, worker rejection, token-budget transfer, audit log, frontend notify) apply
  unconditionally in `switch_agent` and which are conditional on `target != "default"`.

---

#### [MAJ-004] No documentation-update workstream — three reference docs go stale with no plan to fix them

- **Lens**: Incompleteness / Inoperability
- **Affected section**: §5.2 (blast radius table), §10 (implementation workstreams)
- **Description**: I grepped the whole repo for `hand_off`/`return_to_default` outside code and
  found user/developer-facing reference documentation that the ADR never mentions:
  `docs/tools-reference.md:68` ("`return_to_default` | Return control to the default agent after a
  handoff. | `pkg/tools/handoff.go:312`"), `docs/routing.md:143,155` (two prose references to
  `hand-off`/`return_to_default` as the mechanism), and `docs/protocol/websocket-protocol.md:484`
  (documents the `agent_switched` frame as triggered by "handoff or return_to_default"). None of
  these three files appear anywhere in §5.2's blast-radius table or in any of the four workstreams
  in §10.
- **Impact**: Post-merge, these three docs describe tools that no longer exist (`hand_off`,
  `return_to_default`) and omit the tools that replaced them (`switch_agent`) and the entire D3 tier
  concept, with no mechanism (unlike `make verify-contracts` for the wire schemas) to catch the drift.
  Given this project's own repeated emphasis (CLAUDE.md is full of "this line went stale" callouts)
  on exactly this failure mode, shipping a rename ADR with zero doc-update workstream is a
  self-inflicted version of the problem CLAUDE.md warns about elsewhere.
- **Recommendation**: Add a fifth, small workstream (or fold into W1) that updates
  `docs/tools-reference.md`, `docs/routing.md`, and `docs/protocol/websocket-protocol.md` in the same
  commit as the rename.

---

#### [MAJ-005] No rollback or kill-switch for a change the ADR itself calls a "genuine tradeoff" with an unmeasured risk

- **Lens**: Inoperability
- **Affected section**: §4.3 "The accepted risk, stated plainly", §11 Q3
- **Description**: §4.3 states, in its own words: *"71% of the catalog become[s] invisible by
  default... This is a genuine tradeoff... Two things mitigate it and neither is a full answer."*
  §11 Q3 asks whether the revisit trigger gets instrumentation and says *"if the answer is no, the
  ADR should say the risk is accepted permanently rather than provisionally"* — but the ADR ships
  without resolving that question either way. Nowhere in the document is there a config flag,
  feature flag, or staged-rollout mechanism that would let this ship, be observed, and be reverted
  in minutes if agents start failing at Tier-3-only work — the only stated deployment plan is "ship
  on its own branch and merge whenever it is green" (front matter, "Release-phase routing").
- **Impact**: This is not a cosmetic or reversible-by-code-revert-only change — it changes what a
  running agent can see and do, on every install that upgrades, all at once, with a self-acknowledged
  unmeasured behavioral risk and (per §11 Q3) no committed telemetry to detect it. A code revert is
  the only rollback path, and that requires noticing the regression first — which the ADR admits
  there's currently no instrumentation to do.
- **Recommendation**: Either (a) commit to the §11 Q3 instrumentation (a zero-result-query counter is
  cheap, per the ADR's own suggestion) as a hard prerequisite of D3 shipping, not an optional nice-to-
  have, or (b) gate the Tier 2/Tier 3 split behind the existing `cfg.Tools.Manifest.Compressed`-style
  config surface so it can be dialed back per-install without a binary rollback. Given CLAUDE.md's
  Lens-8-style aversion to gratuitous flags, (a) is the cheaper and more consistent choice — but the
  ADR needs to pick one, not leave the question open at ratification time.

---

#### [MAJ-006] Pre-existing agents literally named "default" aren't covered by the reserved-name fix

- **Lens**: Incompleteness
- **Affected section**: §5.1 "Open design gap", §11 Q1
- **Description**: The recommended fix — "the literal `'default'` always wins, and agent
  create/update rejects `default` as a reserved id" — only prevents a *new* agent from being created
  with that name going forward. It says nothing about an install that, prior to this ADR shipping,
  already has an agent whose id or name is literally `default` (nothing in the current codebase
  appears to reserve that string today). This is a real, if narrow, upgrade-time data case that a
  migration-strategy check (INC-10) should cover: what happens to that agent, and to any user
  currently trying to reach it via `switch_agent(target: "default")`, on the day this ships?
- **Impact**: On the (rare, but not impossible) install where this collision pre-exists, the fix as
  written silently makes that agent permanently unreachable via `switch_agent`'s literal path with no
  migration, warning, or rename prompt.
- **Recommendation**: Add a boot-time check (mirroring the pattern already used elsewhere in this
  codebase for coverage gaps) that warns or blocks if any existing agent's id/name is exactly
  `default`, rather than silently shadowing it.

---

### MINOR Findings

#### [MIN-001] §12's "unverified" `SanitizeToolName` item was a two-minute check the ADR itself said it needed and didn't do

- **Lens**: Ambiguity / Process
- **Affected section**: §12, first bullet
- **Description**: The ADR flags this as unverified and says "this is a two-minute check that
  invalidates a whole workstream if it goes the wrong way" — but doesn't do it before drafting a
  document that's "awaiting ratification." I did the check: `SanitizeToolName`
  (`pkg/tools/registry.go:690-692`) is `strings.ReplaceAll(name, ".", "_")` — it only touches dots,
  never case. `registry.go:689`'s own comment confirms mixed case is allowed: *"Anthropic/Azure
  require `^[a-zA-Z0-9_-]{1,128}$`."* `pkg/tools/fuzzy.go`'s lowercasing is confined to fuzzy-match
  suggestion text, not the canonical registry lookup. So D1's rename is safe on this axis — but the
  ADR shouldn't have left a self-identified two-minute check undone at the point of asking for
  ratification.
- **Recommendation**: Fold this verification into the ADR text (mark it resolved, not open) before
  ratification, and apply the same discipline to the Bedrock item in §12's second bullet if it's
  similarly cheap to check.

---

#### [MIN-002] Stale-comment sites outside the D4 blast-radius table

- **Lens**: Incompleteness
- **Affected section**: §5.2 blast radius table
- **Description**: `pkg/agent/loop.go` has a comment — *"Inject session key so handoff/return_to_default
  tools can address the session"* — that isn't in any row of the table. It's non-functional (a
  comment, not a string match), so it won't break behavior, but it's exactly the kind of drift
  CLAUDE.md repeatedly calls out as a recurring problem in this codebase.
- **Recommendation**: Include a blanket "grep for `hand_off`/`return_to_default` in comments across
  `pkg/` and update" step in the mechanical rename pass (§10 already says D1 "is a mechanical pass" —
  make it explicit that comments are in scope, not just identifiers).

---

#### [MIN-003] TS test files for `toolVisibility.ts`/`humanizeToolName.ts` aren't in the Tests row, despite the ADR's own fail-open reasoning applying to them too

- **Lens**: Inconsistency
- **Affected section**: §5.2 Tests row; §5.2's core argument about `sprint_h_registry_test.go` and
  `delegate_grandchild_test.go` asserting on literal strings
- **Description**: `src/lib/toolVisibility.test.ts` (lines 60-61) and
  `src/lib/humanizeToolName.test.ts` (line 64) both assert against the literal strings `'hand_off'`
  and `'return_to_default'`/`'system.return_to_default'`. The ADR applies real rigor to the analogous
  Go-side risk (tests asserting on the old string would keep passing against a registry that no
  longer has it) but doesn't extend that same scrutiny to these TS tests, which aren't listed in the
  Tests row at all. I checked the actual runtime impact and it's lower-severity than the Go case:
  `toolVisibility.ts`'s `default:` case (`src/lib/toolVisibility.ts:178-184`) returns `true`
  (visible) for any unrecognized name, so an unrenamed switch statement fails open into the *correct*
  outcome for `switch_agent` by accident, not the dangerous direction. Still, the tests themselves
  would go stale (testing tool names that no longer exist, silently failing to test the tool that
  replaced them).
- **Recommendation**: Add both TS test files to the Tests row, and add an explicit
  `switch_agent`-is-visible case (not just rely on the accidental default-true behavior).

---

#### [MIN-004] The ambiguity test doesn't state the single-candidate degenerate case explicitly

- **Lens**: Ambiguity
- **Affected section**: §3.2
- **Description**: The test is defined for `i ≥ 2`. When the policy-loadable ranked result set has
  exactly one entry, there's no `i = 2` to evaluate, and the section doesn't explicitly say "in that
  case, tool₁ is promoted alone" (it's implied by "if no candidate qualifies, promote tool₁ alone,"
  but a reader has to infer that an empty candidate set counts as "no candidate qualifies").
- **Recommendation**: One sentence: "If the ranked set has fewer than 2 entries after the `canLoad`
  filter, no ambiguity test runs; `tool₁` (if any) is promoted alone."

---

#### [MIN-005] No tie-breaking rule when more than 2 candidates qualify and the cap is hit

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: §3.2, "capped at 3 total"
- **Description**: If 3+ candidates (beyond `tool₁`) qualify under either clause, the ADR doesn't say
  which 2 get promoted alongside `tool₁`. It's very likely intended to be "the 2 highest-scoring
  qualifying candidates, in ranked order" (since the input is already rank-sorted), but that's not
  stated.
- **Recommendation**: Add "ties are broken by rank order (the ranked-list order already establishes
  this; state it explicitly so an implementer doesn't need to infer it)."

---

### Observations

#### [OBS-001] §6.4's own finding undercuts D5's ROI case for shipping now rather than with #654

- **Lens**: Overcomplexity
- **Affected section**: §6.4, §6.3's closing paragraph
- **Suggestion**: §6.4 shows that *any* tool-array change invalidates the whole cached prefix
  (including `staticPrompt`, which has been cached since #607), and that `ToolSearch` promotion and
  TTL expiry both change the array — meaning D5's benefit accrues only on turns where nothing gets
  promoted or expires. §6.3 already concedes D5 "is worth far less after D3 than it would have been
  before it." Given that D5 also introduces the single riskiest implementation detail in the whole
  ADR (§6.2's "one detail an implementer can get wrong while producing something that looks right and
  measures as a no-op" — the index-1 ordering constraint) for a benefit the ADR's own analysis says is
  now marginal, consider explicitly deferring D5 into #654 (which already needs to answer "how often
  does the tool array actually churn per session" to size its own investment) rather than shipping a
  fragile ordering-dependent mechanism now for an ROI the document itself questions.

---

#### [OBS-002] Tier placement of individual tools is reasoned, not measured — correctly flagged, worth reinforcing

- **Lens**: Infeasibility
- **Affected section**: §11 Q4
- **Suggestion**: The ADR already asks for sign-off on the constants as "reasoned starting values...
  with tuning deferred until there is data" — good practice. Consider explicitly logging the initial
  tier assignment decision date so a future revisit (§4.3's trigger) has a clean "as of" baseline to
  diff against.

---

#### [OBS-003] `pkg/gateway/inboundschemas/AgentSwitchedFrame.yaml` is a generated copy, not a second manually-maintained file — worth naming to preempt confusion

- **Lens**: Incompleteness (mitigated)
- **Affected section**: §9 "Contract impact"
- **Suggestion**: I confirmed `pkg/gateway/inboundschemas/*.yaml` is mechanically synced from
  `contracts/components/schemas/*.yaml` by `scripts/gen-contracts.sh` step 5 (and diffed to be
  identical), and `make verify-contracts` checks git diff on that directory too — so this is not an
  additional manual-edit site, and the existing 5-step process already covers it. Since it appeared
  in a plain grep for `return_to_default` alongside the canonical schema file, a future reader doing
  the same grep this review did is likely to briefly worry it's a second hand-maintained copy.
  Naming it explicitly in §9 ("the sync target `pkg/gateway/inboundschemas/` picks this up
  automatically via `make gen-contracts`") costs one sentence and removes that ambiguity.

---

## Structural Integrity (Variant B — Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | D1-D5 each have a rationale and blast radius, but no explicit "done when" acceptance criteria beyond the implicit "tests pass, gates green." §6.5 is the one exception — it states a concrete acceptance test for D5's caching win. |
| Cross-references are consistent | PASS | Internal §-references and `file::symbol` citations checked against source and are accurate everywhere spot-checked. |
| Scope boundaries are explicit | PASS | "Related, explicitly OUT of scope" names #653/#654 by number; §1's release-phase routing is explicit about not touching v0.1/v0.2/v0.3. |
| Success criteria are measurable | PARTIAL | §6.5's cache-hit-rate acceptance test is measurable. §4.3's "revisit trigger" is explicitly *not* instrumented yet (see MAJ-005) — unfalsifiable as written. |
| Error/failure scenarios addressed | PARTIAL | D2's ambiguity fallback and D4's security blast radius are addressed with real rigor; the websocket.go dual-branch bug (CRIT-001) shows that rigor didn't reach every failure mode. |
| Dependencies between requirements identified | PASS | §10's workstream dependency graph (W1-W4, ordering constraints) is unusually explicit and internally consistent with the decisions it orders. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Gateway/WS regression | No test exists (before or proposed) for the `hand_off`-success → `AgentSwitchedFrame`-with-agent-set branch at `websocket.go:3878` | CRIT-001 |
| SPA visibility regression | `toolVisibility.test.ts`/`humanizeToolName.test.ts` not scoped into the rename's required test updates | MIN-003 |
| Migration / upgrade-time data | No test for an install with a pre-existing agent literally named `default` | MAJ-006 |
| Execute-body reconciliation | No test scenario distinguishes "does `switch_agent(target: default)` get the token-budget transcript transfer or not" because the ADR doesn't decide the question | MAJ-003 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| BM25 ambiguity ratio (§3.2) | Degenerate single-candidate and zero-scoring-term cases | State explicitly per MIN-004 |
| BM25 ambiguity ratio (§3.2) | 3+ qualifying candidates beyond `tool₁` | State tie-break rule per MIN-005 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `switch_agent` tool (delegated sub-turn context) | ok | ok | ok | ok | ok | **risk** | `ExcludedHandoff`/`CloneExcept` exclusion is correctly identified and the fix is specified in §5.2/§5.3 — but see CRIT-001 for a second, unrelated privilege-adjacent surface (a delegated child could also silently regain visibility into the parent's active-agent switch notification if the frame emission logic is patched incompletely; not itself an elevation, but the same code path). |
| `AgentSwitchedFrame` WS frame | ok | ok | ok | ok | ok | ok | Semantics unchanged per §9; description-only contract diff, correctly scoped to the 5-step regen process. |
| `ToolSearch` promotion (D2/D3) | ok | ok | ok | ok | **risk** | ok | §3.3 bounds the worst case at "2 extra schemas for ≤5 turns" — adequately addressed, no action needed, flagged only because unbounded-DOS-via-repeated-ambiguous-queries wasn't explicitly ruled out (each `ToolSearch` call is still one call, so this is bounded by normal turn-count limits elsewhere in the system, not a new surface). |
| Tool-policy migration (§5.3 step 5) | ok | ok | ok | ok | ok | ok | Strictest-value-wins migration correctly mirrors the ADR-036 §3.6 precedent I verified exists in that document. |
| Reserved `default` agent name (§5.1) | ok | ok | ok | ok | ok | ok | Forward-looking fix is sound; see MAJ-006 for the upgrade-time gap it leaves. |

**Legend**: risk = identified threat not fully mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Does `switch_agent(target: "default")` perform the same token-budget-aware transcript context
   transfer that `hand_off` does today, or the simpler no-transfer behavior `return_to_default` has
   today? (MAJ-003)
2. Is `context` required, optional, or conditionally required depending on `target`? (MAJ-001)
3. What should `context`'s description say, given it must now serve both a forward-looking
   ("instructions for the agent taking over") and a backward-looking ("summary of what was
   accomplished") purpose? (MAJ-002)
4. What happens on an install that already has an agent named exactly `default` at upgrade time?
   (MAJ-006)
5. Is there a committed decision on §11 Q3 (revisit-trigger instrumentation), or does this ADR ship
   with that question genuinely unresolved? The document itself says it should say one or the other —
   it currently does neither. (MAJ-005)
6. Given §6.4's finding that tool-array churn already invalidates the cached prefix on a probably-
   large fraction of turns, has anyone estimated what fraction of real sessions D5 actually helps,
   before committing to its ordering-fragile implementation now instead of folding it into #654?
   (OBS-001)

---

## Verdict Rationale

CRIT-001 alone is enough to block: the ADR's central security-relevant section under-diagnosed the
exact bug class it was written to catch, and the missing branch has no test coverage anywhere in the
repository to catch the gap after the fact — this is the single most concrete, evidence-backed finding
in this review and it must be fixed in the ADR text (not just left for the implementer to discover)
before `/plan-spec` runs against it. MAJ-001 through MAJ-003 are a cluster around the same root cause:
D4 is described as a "mechanical" parameter-shape merge in the spirit of ADR-036, but the two tools
being merged have meaningfully different required-ness, semantics, and execution logic that the ADR
doesn't reconcile — an implementer following the ADR text as written has to make three consequential
decisions the ADR should have made. MAJ-004 through MAJ-006 are smaller but real gaps (documentation,
rollback posture, upgrade-time migration) that are individually MINOR-adjacent but collectively point
at the same pattern: strong rigor where the operator's own review (§1.2) already looked, less rigor
in the surrounding areas the review didn't cover.

None of this invalidates the ADR's core decisions (D1-D5 are all reasonable, well-argued, and the
tier arithmetic and mechanism design in D3 hold up under independent verification). This is a REVISE,
not a BLOCK-the-whole-approach: the fixes are localized edits to §5.1, §5.2, and §10, plus resolving
the two genuinely open questions (§11 Q1, Q3) the ADR itself already flagged as needing the operator's
yes.

### Recommended Next Actions

- [ ] Fix CRIT-001: name both `websocket.go` branches (`:3878` and `:3899`) in §5.2, and add the
      missing acceptance test to §5.2/§5.3.
- [ ] Resolve MAJ-001/MAJ-002: decide and state `context`'s required-ness and write a merged
      description covering both use cases.
- [ ] Resolve MAJ-003: state which `HandoffTool.Execute` steps apply unconditionally in the merged
      `switch_agent`.
- [ ] Resolve MAJ-004: add a documentation-update step to §10 covering the three stale-reference docs.
- [ ] Resolve MAJ-005 and MAJ-006 as part of answering §11 Q1 and Q3 — both are already open questions
      the ADR flagged; this review adds the concrete gap each leaves if answered "no action."
- [ ] Fold MIN-001's resolution (SanitizeToolName is confirmed safe) into §12 as closed, not open.
- [ ] Address MIN-002 through MIN-005 in the same pass as the above — all are small text additions.

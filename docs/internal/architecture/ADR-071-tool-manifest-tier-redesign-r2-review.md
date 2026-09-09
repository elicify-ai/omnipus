# Adversarial Review: ADR-071 — Tool manifest tier redesign (revision 2)

**Spec reviewed**: `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` (r2, 2026-08-27)
**Review date**: 2026-08-27
**Verdict**: REVISE

> **Note on filename**: this review targets **r2**, the revision written in response to
> [`ADR-071-tool-manifest-tier-redesign-review.md`](ADR-071-tool-manifest-tier-redesign-review.md)
> (the r1 review, 1 CRITICAL / 6 MAJOR / 5 MINOR / 3 OBSERVATION). That file is explicitly preserved
> in r2's own front matter as "retained unmodified as a historical record... not superseded" — this
> review is written to a **separate file** (`-r2-review.md`) rather than overwriting it, to honor
> that. r2's traceability table (§13) claims all 16 of the r1 review's findings are resolved; this
> review independently spot-checked that claim (see "Verification of r2's own claims" below) and
> found it accurate everywhere checked — the findings below are net-new, found by re-running the
* eight-lens process against r2's actual (changed and unchanged) text and against the current
  codebase at `release/v0.1.1` @ `aa97bcea` (verified — this is the exact commit the working tree is
  checked out to).

## Executive Summary

r2's own code citations hold up: every claim independently re-verified below — the 89-tool
post-D4 arithmetic (cross-checked name-by-name against `pkg/coreagent/core.go::allStaticToolNames`,
not just the totals), both `websocket.go` line numbers, `ToolExecEndPayload`'s field list,
`HandoffTool`/`ReturnToDefaultTool`'s actual required-ness and execution steps, `SnapshotSearchableTools`,
`sortedToolNames`, `SanitizeToolName`, and the Bedrock/OpenAI-compatible `CacheControl` handling — was
correct. But r2 repeats, in a new form, the exact failure pattern that produced r1's CRITICAL finding:
a blast-radius/completeness claim that a plain repository search shows is not actually complete. §5.2.2
says "Full surface, all confirmed by search in session" — a grep for the literal strings `hand_off` /
`return_to_default` across the repository turns up a **production file** (`pkg/agent/subturn.go`, the
actual `CloneExcept` call site) and **three Go test files** (`pkg/agent/sprint_h_subturn_test.go`,
`pkg/agent/sprint_h_scenario_test.go`, `pkg/agent/subturn_delegate_nesting_test.go`) that hardcode the
old tool name and are absent from the table — plus two "related, still in force" ADRs (057, 065) that
describe `hand_off` as an active mechanism, outside W5's stated scope with no principled reason given.
Separately, the two detection counters r2 adds as a **hard prerequisite of D3 shipping** (§4.3.1) are
never wired to this codebase's actual operator-facing `/metrics` endpoint (`pkg/gateway/metrics.go`) —
which already exists, in the same tool-registry domain, with an established zero-import-cycle wiring
pattern r2 doesn't use or mention — so the mechanism r2 calls "falsifiable" produces data nobody can
currently see. 4 MAJOR, 3 MINOR, 3 OBSERVATION findings; 0 CRITICAL.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 4 |
| MINOR | 3 |
| OBSERVATION | 3 |
| **Total** | **10** |

---

## Verification of r2's own claims (context for the findings below)

Before hunting for new gaps, r2's central factual claims were independently re-derived from source,
not re-read from the ADR's own prose, to check whether r1's "spot-checked and held up" verdict still
applies to r2's *changed* material:

- **89-tool arithmetic**: extracted `allStaticToolNames` from `pkg/coreagent/core.go` (90 names,
  confirmed), built the ADR's Tier 1/2/3 lists (17/8/63) as flat files, and diffed. Zero names in the
  ADR's tier lists are absent from the catalog; the **only** catalog name not covered by Tier 1/2/3 is
  `load_tool` — which is exactly correct, since it becomes the Infra tool (`ToolSearch`). Arithmetic
  holds at the name level, not just the totals.
- **`websocket.go` both branches**: `grep -n 'p.Tool == "hand_off"\|p.Tool == "return_to_default"'`
  returns exactly `:3878` and `:3899`, matching §5.2.1 exactly.
- **`ToolExecEndPayload`**: read in full — confirmed no tool-arguments field exists, supporting §5.2.2's
  claim that r1's prescribed fix was not implementable.
- **Option (A)'s premise**: read `ReturnToDefaultTool.Execute` and `onHandoffFrontend`
  (`pkg/agent/loop.go`) in full — confirmed `onHandoff` is called with `AgentID: defaultAgentID`
  (non-empty) on the default-return path, so `GetSessionActiveAgent` does return the default agent's
  own id afterward, not an empty/cleared value. The closure `getDefaultAgent` in `loop.go` falls back to
  the *same* `liveRegistry.GetDefaultAgent()` the websocket.go fix would compare against — no hidden
  third resolution ladder.
- **`HandoffTool`/`ReturnToDefaultTool` required-ness and steps**: read both `Execute` bodies in full;
  §5.1.1's "required" claim (`"required": []string{"agent_id", "context"}`) and §5.1.2's reconciliation
  table both check out against source, with one omission — see MIN-002 below.
- **`SnapshotSearchableTools`, `sortedToolNames`, `SanitizeToolName`, Bedrock/OpenAI-compatible
  `CacheControl` handling**: all read in full, all match the ADR's claims exactly.

None of this is repeated as a finding — it is recorded so the findings below are read as *additional*
to a document that is, on the whole, unusually well-grounded in source.

---

## Findings

### MAJOR Findings

#### [MAJ-001] The blast-radius table's own completeness claim doesn't survive a plain grep: `pkg/agent/subturn.go` is absent

- **Lens**: Incompleteness
- **Affected section**: §5.2.2, "Full surface, all confirmed by search in session" and the surface
  table beneath it
- **Description**: `grep -rln '"hand_off"\|"return_to_default"'` across `pkg/`, `src/`, and `tests/`
  turns up `pkg/agent/subturn.go` — a production file, not listed anywhere in the surface table (which
  cites `pkg/tools/registry.go` for the `ExcludedHandoff` constant, but not the file that actually
  *calls* `CloneExcept` with it). Two distinct problems live in that file:
  1. **Line 1075**: `slog.Info("subturn: child registry constructed", "excluded", []string{"hand_off"}, ...)`
     — a hardcoded string literal, independent of the `tools.ExcludedHandoff` constant, used for the
     stated purpose "so operators can debug 'my subagent has no tools' issues." After D4 renames the
     constant's value, this log line keeps printing `"excluded": ["hand_off"]` forever — wrong, and
     specifically wrong in the one log line an operator would consult to understand *why* a subagent
     lacks a tool. This is not the `ExcludedHandoff` constant that the ADR does correctly plan to
     rename; it is a second, independent literal the rename does not touch by construction.
  2. **Lines 1061-1071**: a substantive, currently-live bug comment: *"unlike 'delegate' above,
     'hand_off' is unconditionally excluded from EVERY child sub-turn's registry, and the SAME
     `load_tool` fabricated-success-then-`permission_denied` bug just cured for 'delegate' is still
     live for 'hand_off' — `canLoad`/`markLoaded` (`pkg/tools/tools_tool.go`) resolve the caller via
     `al.registry.GetAgent(callerID)`, the PERSISTENT top-level agent, not this ephemeral child's own
     registry, so `load_tool` can still report a fabricated success for 'hand_off' here... Root-caused
     but out of scope for this fix."* This is a documented, acknowledged, still-open defect that sits
     directly in the intersection of **D1** (renames the exact tool, `load_tool`→`ToolSearch`, where
     `canLoad`/`markLoaded` live) and **D4** (renames the exact excluded tool, `hand_off`→`switch_agent`)
     — and the ADR never mentions it, despite §5.2's whole thesis being "this rename is
     security-relevant, not cosmetic" in exactly this file's neighborhood.
- **Impact**: (1) is a confusing-but-harmless stale log string — low severity on its own, but it is
  precisely the class of drift CLAUDE.md and this ADR's own §10 mechanical-rename-pass instruction
  warn about, in a file the rename pass has no reason to visit because it isn't in the table. (2) is
  higher-value: an implementer following the ADR's blast-radius table has no signal that this comment
  exists, so the mechanical rename risks either (a) leaving the comment referencing a tool name that no
  longer exists, further obscuring an already-acknowledged bug, or (b) a rename tool/find-replace
  touching the string inside the comment without anyone re-reading and re-validating whether the bug
  description still holds true for `ToolSearch`/`switch_agent` post-rename (it should — the mechanism
  described doesn't depend on the tool's name — but nothing forces that re-validation to happen).
- **Recommendation**: Add `pkg/agent/subturn.go` to §5.2.2's surface table, with two explicit sub-items:
  (a) update the log literal at line 1075 to read the excluded name dynamically (e.g.
  `[]string{tools.ExcludedHandoff.String()}` or equivalent) so it can never drift from the constant
  again, not just fix it once; (b) update the bug-comment's tool names to `ToolSearch`/`switch_agent`
  in the same commit, and add one sentence confirming the described defect is still live post-rename
  (it is architecturally unrelated to the rename, but a future reader should not have to re-derive that).

---

#### [MAJ-002] The Tests row is also incomplete: three `pkg/agent` test files hardcode the literal `"hand_off"` and aren't listed

- **Lens**: Incompleteness
- **Affected section**: §5.2.2 surface table, "Tests (existing, must be updated)" row
- **Description**: The same grep that found MAJ-001 also surfaces three test files not in the row:
  `pkg/agent/sprint_h_subturn_test.go`, `pkg/agent/sprint_h_scenario_test.go`, and
  `pkg/agent/subturn_delegate_nesting_test.go`. Read in full:
  - `sprint_h_subturn_test.go:124`: `childRegistry := parentRegistry.CloneExcept("hand_off")` — the
    **raw string literal**, not `tools.ExcludedHandoff`. Line 116-117 asserts the precondition
    `hasHandoffBefore, _ := parentRegistry.Get("hand_off"); require.True(t, hasHandoffBefore, ...)`.
  - `sprint_h_scenario_test.go`: `CloneExcept("delegate", "hand_off")` (three call sites, lines 81,
    135, 266), `forbiddenTools := []string{"delegate", "hand_off"}` (line 83), and an assertion text
    "child must have exactly parent_count-2 tools after excluding delegate+hand_off" (line 275).
  - `subturn_delegate_nesting_test.go:66`: a comment referencing "hand_off remains excluded."
- **Impact**: Unlike r1's CRIT-001 (a silently-passing test that would ship a regression undetected),
  these tests use `require.True`/literal-string lookups that will **fail loudly** post-rename — so this
  is not a silent-regression risk. But it is a real gap against the ADR's own stated bar: §5.2.2 states
  plainly "Full surface, all confirmed by search in session," and this is demonstrably not so — a
  one-line grep (the same one the ADR's own methodology relies on throughout §5.2/§10) finds three more
  files in under a second. Left unlisted, `/taskify`'s task breakdown for W1 will most likely miss these
  files, producing a rename PR whose CI fails on compile/test errors the author has to discover and
  patch in a follow-up commit — avoidable friction, and a second instance of exactly the pattern r1's
  review was written to catch.
- **Recommendation**: Add all three files to the Tests row. While auditing them, also flag
  `sprint_h_subturn_test.go:124`'s use of the raw string `"hand_off"` instead of `tools.ExcludedHandoff`
  as a latent test-hygiene issue independent of this ADR — the constant exists precisely so call sites
  don't hardcode the literal, and this test bypasses it.

---

#### [MAJ-003] W5 doesn't address two "still in force" ADRs that describe `hand_off` as a live mechanism, not a dated record

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: §10 W5 (documentation workstream) and its scoping note: *"the many
  `docs/internal/{specs,uat,reviews}/` hits are deliberately out of scope: those are dated
  point-in-time records... whose value is that they describe the system as it was on their date...
  Only live reference documentation is in scope."*
- **Description**: `grep -ln 'hand_off\|return_to_default' docs/internal/architecture/ADR-*.md` finds,
  beyond ADR-040 (already cited elsewhere in this ADR as needing its `CloneExcept` exclusion renamed),
  two more architecture decision records that describe `hand_off` as **currently active behavior**, not
  historical narrative:
  - `ADR-057-session-parent-child-parity.md:462`: *"`sessionActiveAgent` resolver | `loop.go:6653-6656`
    | **FIXED** — `hand_off` is structurally excluded from a child registry (`subturn.go:988` →
    `registry.go:667-669`), so a child can never hand off; the resolver correctly returns `""`..."* —
    stated as a present-tense, currently-true invariant with file:line citations, in a document that
    functions as reference material for the session-identity subsystem elsewhere in this codebase
    (CLAUDE.md itself cites ADR-057 repeatedly as authoritative for how `pkg/session` works today).
  - `ADR-065-channel-ownership-per-agent-workspace.md:46`: *"The bypass: `hand_off` pins another agent
    to a live conversation, and that pin is consulted..."* — describing an active security-relevant
    bypass mechanism, present tense.
  Neither file is a UAT run, a spec review, or a superseded design draft — the three categories §10's
  scoping note names as the reason for excluding `docs/internal/{specs,uat,reviews}/`. ADRs are this
  project's own accepted-decision record type (CLAUDE.md: "`docs/internal/architecture/ADR-*.md` —
  accepted decisions"), and both are on this very ADR's own "Related, all still in force" list in
  spirit (ADR-040 is; 057/065 are the same genre and equally still-cited elsewhere) — yet the ADR draws
  no line explaining why 057/065 are excluded from W5 while ADR-040's exclusion constant gets an
  explicit fix instruction.
- **Impact**: Post-rename, ADR-057's table row will assert a "FIXED, currently true" invariant using a
  tool name that no longer exists, in a document other engineers consult as live reference for session
  mechanics — exactly the confusion §10 W5 was written to prevent for `docs/tools-reference.md`,
  `docs/routing.md`, and `docs/protocol/websocket-protocol.md`, just in two files the ADR didn't grep
  for by directory.
- **Recommendation**: Either (a) add ADR-057:462/465 and ADR-065:46 to W5's file list with a one-line
  update each (name `switch_agent`, note the exclusion mechanism is unchanged), or (b) if the project's
  convention is that ADRs are genuinely immutable once accepted (matching the specs/uat/reviews
  precedent), say so explicitly here and add a one-line pointer note to each ("superseded name, see
  ADR-071") instead of full rewriting — but pick one and state it, rather than leaving the boundary
  between "live reference doc" and "dated record" silently undrawn for the ADR family.

---

#### [MAJ-004] The two new detection counters — a stated hard prerequisite of D3 shipping — are never wired to this codebase's actual operator-facing metrics surface

- **Lens**: Inoperability
- **Affected section**: §4.3.1(a), "Detection — a zero-result / abandoned-search counter"
- **Description**: §4.3.1(a) specifies `toolSearchZeroResultQueries` and `toolSearchNoFollowUpCalls` as
  "two `atomic.Uint64` counters in `pkg/tools`... a package-level counter plus an exported accessor, no
  new dependency, no new config, **no metrics backend**" — modeled explicitly on
  `handoffTranscriptWriteFailures` (`pkg/tools/handoff.go`). Checked where that precedent actually
  surfaces: `grep -rln 'HandoffTranscriptWriteFailures'` finds exactly one consumer,
  `pkg/tools/handoff_adr057_test.go` — a test file. Despite the accessor's own doc comment naming a
  Prometheus-style metric (`omnipus_handoff_transcript_write_failures_total`), it is never registered
  with, or read by, any actual metrics exporter. This codebase does have one:
  `pkg/gateway/metrics.go` implements a hand-rolled `/metrics` Prometheus-text endpoint specifically
  for **tool-registry** counters (`omnipus_tool_filter_total`, `omnipus_tool_mcp_collision_total`,
  `omnipus_tool_approval_latency_seconds`) — the exact domain these two new counters belong to. It is
  reached from `pkg/tools` via an already-established, zero-import-cycle pattern: `pkg/tools/compositor.go`
  defines a small recorder interface (`IncFilterTotal`, `IncCollisionTotal`) with a `nopToolMetrics`
  no-op default and a package-level `activeToolMetricsRecorder`, and `pkg/gateway/gateway.go:4103` wires
  the real implementation in at boot via `tools.SetToolMetricsRecorder(globalToolMetrics)`. This is a
  proven, working, few-line pattern in the same package the ADR proposes to add the new counters to —
  and the ADR neither uses it nor mentions `pkg/gateway/metrics.go` exists.
- **Impact**: §4.3.1 states its purpose in the strongest possible terms — "**Both mechanisms below are
  hard prerequisites of D3 shipping**," and frames the counters as what makes the revisit trigger
  "falsifiable" rather than "unfalsifiable... which is worse... exactly the failure shape
  `docs/internal/false-green-patterns.md` catalogues." As specified, the counters are visible only to
  whoever writes a throwaway Go program (or attaches a debugger) to call the exported accessor — no log
  line, no `/metrics` row, no admin endpoint is named anywhere in the ADR. That is not a materially
  better position than "no telemetry," which is the exact posture r1's MAJ-005 already objected to; r2
  has replaced an unfalsifiable trigger with a falsifiable-in-principle, unobservable-in-practice one,
  and calls that resolved. Given this ADR is unusually careful about distinguishing a detector that
  works from a detector that only looks like it works (§6.6's whole argument for shipping D5 turns on
  exactly this distinction), leaving its own detection mechanism at "exists but nobody can see it" is a
  real gap, not a stylistic one.
- **Recommendation**: Extend the existing `pkg/tools/compositor.go` recorder interface with
  `IncToolSearchZeroResult()` / `IncToolSearchNoFollowUp()`, add matching counters/exposition rows to
  `pkg/gateway/metrics.go` (`omnipus_toolsearch_zero_result_total`, `omnipus_toolsearch_no_followup_total`),
  and wire them the same way `IncFilterTotal`/`IncCollisionTotal` already are. This costs a handful of
  lines, reuses an established pattern instead of inventing a second, weaker one, and is what actually
  makes §4.3.1(a) "committed, not recommended" in practice, not just in the ADR's prose.

---

### MINOR Findings

#### [MIN-001] `toolSearchNoFollowUpCalls` conflates "wrong guess" with "correct guess acted on later"

- **Lens**: Infeasibility / Ambiguity
- **Affected section**: §4.3.1(a), second bullet
- **Description**: The counter increments "when a `ToolSearch` promotes one or more tools and the turn
  ends without any of them being called" — this is stated to be the "found something, it was wrong"
  signal. But nothing distinguishes that case from an agent that promotes the *correct* tool, then
  spends the current turn on a prerequisite step (reading a file, asking a clarifying question,
  finishing a different sub-task) before calling the promoted tool one or more turns later — which is
  well within the default 5-turn TTL window the promotion survives for (§3.3). Both cases increment the
  same counter.
- **Recommendation**: Either narrow the condition to "...and the promoted tool's TTL expires (or the
  session ends) without ever being called" (aligning the signal to the thing that actually costs
  something — a wasted schema, not a same-turn delay), or explicitly acknowledge the same-turn
  granularity is a deliberate simplification and that some rate of false positives is expected and
  should be discounted when reading the counter.

---

#### [MIN-002] §5.1.2's reconciliation table, despite reading "both bodies... in full," omits the timeout wrapper difference

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: §5.1.2, the step-by-step table and its introduction ("Both bodies were read in
  full")
- **Description**: `HandoffTool.Execute` opens with `ctx, cancel := context.WithTimeout(ctx, 10*time.Second)`
  — an unconditional 10-second timeout wrapping the entire operation (present because of the
  `ReadTranscript`/token-budget-split work it does). `ReturnToDefaultTool.Execute` has **no** timeout
  wrapper at all. The table lists seven steps (target resolution, worker rejection, session-key
  resolution, atomic switch, transcript transfer, audit entry, frontend notify, result string) and
  marks each as conditional or unconditional, but the timeout wrapper — a real behavioral difference
  between the two bodies — isn't one of the rows.
- **Recommendation**: Add a row (or a sentence) stating whether `switch_agent`'s `Execute` wraps the
  whole call in the 10-second timeout unconditionally (harmless for the fast `target: "default"` path,
  since it does no transcript I/O, but still a stated behavior change worth naming rather than leaving
  implicit) or only for the agent-target branch.

---

#### [MIN-003] D2's cross-category promotion widens per-query schema exposure with no STRIDE pass of its own

- **Lens**: Insecurity (Elevation of Privilege / Information Disclosure, bounded)
- **Affected section**: §3.2, the cross-category near-miss clause
- **Description**: Today, one `ToolSearch(query)` call exposes at most one tool's full schema. After
  D2, an ambiguous query can expose up to three — and the cross-category clause is specifically designed
  to fire when a query plausibly matches two *different kinds* of tool. Tier 3 (§4.1) includes several
  destructive/high-blast-radius tools (`delete_agent`, `delete_workspace`, `disable_channel`,
  `set_config`, `delete_task`, `remove_mcp_server`). Every promoted candidate must still pass `canLoad`
  (policy) first, so this is not a privilege-escalation path — but it does mean a single, seemingly
  benign query can surface a destructive tool's callable schema into context without the model (or an
  injected instruction later in the same context) needing to name that tool explicitly. §3.5 already
  shows the ADR is alert to adjacent risk (rejecting execute-on-search partly because "arguments
  invented inside a free-text search call get none of [schema-constrained generation]"), but this
  specific angle — promotion breadth as an indirect exposure surface for prompt-injection-adjacent
  attacks — isn't addressed anywhere in §3 or §11.
- **Recommendation**: State explicitly whether high-risk Tier-3 tools should be excluded from
  multi-promotion (i.e., capped at confident-single-hit-only regardless of ambiguity band), or record
  that the risk was considered and accepted because `canLoad` already bounds it to what the calling
  agent's own policy allows. Either is fine; the silence is the gap.

---

### Observations

#### [OBS-001] `ManifestVisibility`'s enum-vs-bool justification partly leans on a deferred, undecided future feature

- **Lens**: Overcomplexity
- **Affected section**: §4.4, "An enum rather than a `ManifestVisible bool`"
- **Suggestion**: The design gives three reasons for choosing an enum: it mirrors the existing
  `ToolManifestTier` lookup, it reads correctly at call sites, and it "leaves room for a third value if
  the per-agent idea in §4.5 is ever taken up." §4.5 is explicitly "recorded as a future consideration;
  not designed, not scoped, not implemented here." The first two reasons already fully justify the enum
  on their own; the third is the "someday we might need..." pattern Lens 8's own test names as a smell,
  even though the enum choice itself is cheap and not wrong. Consider dropping the third clause — it
  costs nothing to remove and keeps the precedent clean for future ADRs that might reach for the same
  kind of justification in a costlier spot.

---

#### [OBS-002] D2's ambiguity test requires evaluating `canLoad` against the full ranked list, not stopping at the first passing hit — an implementation-flow change §3 doesn't flag

- **Lens**: Incompleteness
- **Affected section**: §3.2
- **Suggestion**: `execSearchAndLoad` today walks `matches` (built from the raw ranked list, with no
  `Score` field carried — confirming F2's premise that `ToolSearchResult` drops the BM25 score) and
  `break`s on the first candidate that passes `canLoad`. Building "the ranked, policy-loadable result
  set" the ambiguity test needs (§3.2) requires evaluating `canLoad` against every ranked candidate up
  to `maxSearchResults` (default 5) rather than stopping at the first success — a control-flow change
  from short-circuit to full-scan. `canLoad` itself appears side-effect-free (a policy lookup), so this
  is low risk, but it's a real change to today's loop that the algorithm description doesn't call out
  as an implementation consequence.

---

#### [OBS-003] `PreviewAllLazy` (§4.3.1b) and D5's `BuildStaticToolCatalog`/`BuildLoadedToolsDelta` split (§6.1) don't say which function ends up owning the `ManifestVisibility` filter

- **Lens**: Inconsistency
- **Affected section**: §4.4 ("`BuildCompressedManifest` gains one filter line") vs. §6.1 (D5 splits
  `BuildCompressedManifest` into two new functions)
- **Suggestion**: §4.4 places the `ManifestSearchOnly` filter inside `BuildCompressedManifest`. §6.1
  later retires that function's manifest-block role in favor of `BuildStaticToolCatalog(lazyTools
  []Tool)` + `BuildLoadedToolsDelta`. §10's sequencing (W3 before W2) makes this low-risk in practice,
  but the ADR never states explicitly whether `BuildStaticToolCatalog` itself applies the visibility
  filter or expects an already-filtered `lazyTools` slice from its caller — worth one sentence so an
  implementer doing W2 doesn't have to infer it from the sequencing note alone.

---

## Structural Integrity (Variant B — Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | D4/D5 get explicit acceptance tests (§5.2.3, §6.5/§6.6). D3's own accepted risk (§4.3, "71% invisible by default") has no acceptance test of its own — only the two detection counters, and MAJ-004 shows those aren't yet operator-observable. |
| Cross-references are consistent | PARTIAL | Every internal `§`-reference and `file::symbol` citation spot-checked resolves correctly. But the "Full surface, all confirmed by search in session" claim (§5.2.2) is the one cross-reference-completeness claim that doesn't hold under an independent grep — see MAJ-001/MAJ-002. |
| Scope boundaries are explicit | PASS | #653/#654 named and excluded; release-phase routing explicit; W5's specs/uat/reviews exclusion is well-reasoned, just not extended consistently to ADR-057/065 (MAJ-003). |
| Success criteria are measurable | PARTIAL | §6.5/§6.6's `CacheReadTokens` gate is genuinely measurable in principle, but its pass condition ("approximately the catalog block's token count") has no stated tolerance — see MIN-001's sibling issue at MAJ-004's neighbor, §6.6 (folded into the Ambiguity discussion above; not separately numbered to avoid double-counting with MAJ-004's operability angle). |
| Error/failure scenarios addressed | PASS | D2's degenerate cases (MIN-004/MIN-005 from r1) are now explicit; D4's worker-rejection and reserved-name cases are thorough. |
| Dependencies between requirements identified | PASS | §10's workstream graph remains unusually explicit and, per this review's OBS-003, only slightly under-specified at the W2/W3 boundary. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Sub-turn registry construction (production log + comment) | `pkg/agent/subturn.go`'s hardcoded `"hand_off"` log literal and bug-comment aren't covered by any test asserting the log field's value, so a stale post-rename literal would ship with no test to catch it | MAJ-001 |
| Sub-turn exclusion tests using raw literals | Three `pkg/agent` test files assert against the literal `"hand_off"` string rather than `tools.ExcludedHandoff`; they'll fail hard post-rename (good) but aren't in the required-updates list (bad, for planning purposes) | MAJ-002 |
| Metrics/observability | No test proposed anywhere that asserts `toolSearchZeroResultQueries`/`toolSearchNoFollowUpCalls` are reachable via `/metrics` or any other operator surface, because no such wiring is specified | MAJ-004 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| D5 cache-gate acceptance test (§6.6) | No stated tolerance band for "approximately the catalog block's token count" | Define a numeric tolerance (e.g., within ±20%, or "at least half the estimated block size") so the gate has one unambiguous pass/fail reading |
| `toolSearchNoFollowUpCalls` | No boundary distinguishing "never called" from "called in a later turn within TTL" | See MIN-001 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `switch_agent` / `ExcludedHandoff` exclusion (delegated sub-turn) | ok | ok | ok | ok | ok | ok | The runtime enforcement is correctly identified and renamed (`tools.ExcludedHandoff` in `pkg/tools/registry.go`); MAJ-001 is about a stale *log line* and *bug comment* in the file that consumes the constant (`pkg/agent/subturn.go`), not the enforcement mechanism itself, which is unaffected. |
| `ToolSearch` D2 multi-promotion | ok | ok | ok | **risk** | ok | ok | MIN-003 — policy-bounded, but the exposure-surface widening for high-risk Tier-3 tools isn't discussed anywhere in the document. |
| `toolSearchZeroResultQueries` / `toolSearchNoFollowUpCalls` counters | ok | ok | ok | ok | ok | ok | Not a STRIDE risk — MAJ-004's finding is that the mechanism is inert/unobservable, not insecure. |
| `AgentSwitchedFrame` emission (Option A, §5.2.2) | ok | ok | ok | ok | ok | ok | Independently re-verified against `websocket.go:3878-3919` and `onHandoffFrontend` in this review; the derivation logic is sound and matches the ADR's description exactly. |

**Legend**: risk = identified threat not fully mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Now that `pkg/agent/subturn.go`'s known, documented `load_tool`/`hand_off` fabricated-success bug
   sits at the exact intersection of D1 and D4, should this ADR at minimum re-confirm (in one sentence)
   that the bug's mechanism is unaffected by either rename — or does the rename change anything about
   how `canLoad`/`markLoaded` resolve the caller that would make this worth re-scoping into this ADR
   rather than leaving it "out of scope" a second time? (MAJ-001)
2. Is there a reason `pkg/tools/compositor.go`'s existing metrics-recorder interface and
   `pkg/gateway/metrics.go`'s `/metrics` endpoint were not considered for the two new §4.3.1 counters —
   or was their existence simply not known at drafting time? (MAJ-004)
3. Are `docs/internal/architecture/ADR-*.md` files, as a genre, meant to be living reference
   documentation (updated when the mechanism they describe changes) or immutable point-in-time decision
   records (like the specs/uat/reviews directories W5 already excludes)? This ADR's own front matter
   both treats ADR-040 as needing an update (its `CloneExcept` exclusion "must rename") and is silent on
   ADR-057/065, which describe the identical mechanism — the document doesn't currently answer this for
   itself. (MAJ-003)
4. What tolerance turns §6.6's D5 merge gate from a judgment call back into an actual measurement — is
   "approximately the catalog block's token count" ±10%, ±50%, "any nonzero rise," or something else?
   (folded into MAJ-004's neighboring discussion; not separately scored to avoid double-counting)

---

## Verdict Rationale

No CRITICAL findings — r2 successfully closed r1's CRIT-001 and the surrounding MAJOR cluster, and
every code claim independently re-checked in this pass held up exactly as stated. But two of this
review's four MAJOR findings (MAJ-001, MAJ-002) show the *same failure shape* r1's CRIT-001 caught,
recurring in the *same section* (§5.2.2) that r2 rewrote specifically to fix it: a stated
"full/complete" surface claim that a plain grep — the exact technique both this review and r1's
methodology already relied on — shows is incomplete. That is not a new category of risk, but it is
evidence the completeness claim in §5.2.2 should not be taken at face value without re-verification, and
it should be re-verified again after the fixes below are applied, not assumed fixed by inspection alone.
MAJ-003 and MAJ-004 are a different pattern: both are cases where r2's stated "hard prerequisite" /
"committed, not recommended" language is stronger than what the mechanism actually delivers today
(MAJ-004 especially — a "falsifiable" detector nobody can currently observe is functionally close to
the "unfalsifiable trigger... which is worse" posture r1's MAJ-005 already objected to, just one layer
removed). None of the four MAJOR findings blocks the *design* — D1 through D5 remain sound, and the
fixes required are all localized: add files to two table rows, add two counter-registration calls to an
existing metrics struct, and resolve one scope question about the ADR-057/065 boundary. This is a
REVISE, not a BLOCK.

### Recommended Next Actions

- [ ] MAJ-001: add `pkg/agent/subturn.go` to §5.2.2's surface table; fix the hardcoded log literal to
      derive from `tools.ExcludedHandoff` rather than a second hand-copied string; update and
      re-validate the bug-comment's tool names.
- [ ] MAJ-002: add `pkg/agent/sprint_h_subturn_test.go`, `pkg/agent/sprint_h_scenario_test.go`, and
      `pkg/agent/subturn_delegate_nesting_test.go` to the Tests row.
- [ ] MAJ-003: decide and state whether ADR-057/ADR-065 are in W5's scope; if yes, add their two cited
      lines; if no, state the principled distinction from ADR-040's inclusion.
- [ ] MAJ-004: wire `toolSearchZeroResultQueries`/`toolSearchNoFollowUpCalls` through
      `pkg/tools/compositor.go`'s existing recorder interface into `pkg/gateway/metrics.go`, following
      the `IncFilterTotal`/`IncCollisionTotal` pattern exactly.
- [ ] MIN-001 through MIN-003 and OBS-001 through OBS-003: small text additions, same pass as the above.

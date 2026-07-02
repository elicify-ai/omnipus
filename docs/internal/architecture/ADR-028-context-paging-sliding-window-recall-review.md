# Adversarial Review: ADR-028 — Context paging (sliding-window + recall)

**Spec reviewed**: docs/internal/architecture/ADR-028-context-paging-sliding-window-recall.md
**Review date**: 2026-07-01
**Review mode**: structured-spec (ADR with FR-/NFR-/D- identifiers; no BDD/traceability)
**Verdict**: REVISE

> This supersedes the prior (rev. 1) review that BLOCKED on the `transcript.jsonl`-vs-`context.jsonl`
> archive error. That correction (C-1/C-2/C-3) is confirmed cleared below. Rev. 2 introduces new
> claims (append-only `context.jsonl`, `.context/` retention, agent-scoped recall) that were not in
> rev. 1 and are graded fresh here. No CRITICAL remains; the verdict is REVISE, driven by MAJOR
> findings on the append-only mechanism, agent-scoping feasibility, and the retention-liveness gap.

## Executive Summary

The archive correction (`context.jsonl`, not `transcript.jsonl`) is verified correct against the
code — the prior BLOCK is cleared. However rev. 2 mis-characterises the load-bearing change: the
JSONL store **already has a non-destructive eviction primitive** (`meta.Skip` + append-only
`addMsg`), so "make `context.jsonl` append-only" is a *smaller and different* change than the ADR
frames it, and the ADR's stated highest-uncertainty item ("the exact SetHistory/Save write path")
is answered by code the ADR never cites. Separately, the agent-scoping requirement (NFR-6/M-2) is
**not implementable as written** because on-disk messages carry no agent identity, and the D14
"never touch an active session" retention guarantee is contradicted by the actual `ModTime`-based
sweep. These are correctness/feasibility gaps that must be resolved before plan-spec.

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

#### [MAJ-001] "Append-only" mis-frames an existing, simpler mechanism as a novel invasive change

- **Lens**: Incorrectness / Overcomplexity
- **Affected section**: FR-2, D14, D11, §6 ("Missing: the exact SetHistory/Save → context.jsonl write path to convert to append-only"), §9 step 4.
- **Description**: The ADR treats "make `context.jsonl` append-only" as an unknown, invasive
  persistence change and lists the write path to convert as *Missing* / high-uncertainty. In
  fact `pkg/memory/jsonl.go` already appends non-destructively: `addMsg`/`AddFullMessage`
  (`jsonl.go:213`) use `O_APPEND` and never rewrite; `GetHistory` (`jsonl.go:266`) reads
  `readMessages(path, meta.Skip)` — it **skips** the first `meta.Skip` lines rather than
  deleting them; `TruncateHistory` (`jsonl.go:323`) evicts from the live window purely by
  advancing `meta.Skip`, touching **zero bytes** of the log. The *only* operations that
  physically rewrite/lose data are `SetHistory` → `rewriteJSONL` and `Compact` → `rewriteJSONL`.
  `forceCompression`'s data loss (`loop.go:7452` `SetHistory(keptHistory)` + `loop.go:7453`
  `Save`→`Compact`) comes precisely from calling those two. So "append-only" reduces to:
  windowTrim advances `Skip` (à la `TruncateHistory`) instead of calling `SetHistory`, and
  windowTrim/Save must **not** call `Compact` on `.context/`. The archive is then already whole
  (`readMessages(path, 0)`), and the live window is `readMessages(path, Skip)`.
- **Impact**: plan-spec will scope a large, risky "convert the write path to append-only" change
  and possibly reinvent a mutable log format, when the primitive already exists. The design's
  central confidence claim ("append-only makes verbatim paging + no-loss real: High") rests on an
  unexamined mechanism the ADR did not identify.
- **Recommendation**: Rewrite FR-2/D11/D14 to state the actual change: (1) windowTrim evicts by
  advancing `meta.Skip` (reuse/rename `TruncateHistory` semantics), never `SetHistory`; (2) neither
  windowTrim nor its Save path calls `Compact` for `.context/` sessions (today `UnifiedStore.Save`
  → `backend.Compact`, `unified.go:756`, which would collapse skipped lines and destroy the
  archive — this must be gated off for context logs); (3) recall reads the full log via
  `readMessages(path, 0)` / a `Skip=0` read. Move the "Missing: write path" note to Resolved and
  cite `jsonl.go:213/266/323/398`.

#### [MAJ-002] `Save`→`Compact` still destroys the archive; the ADR does not disable it

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-2 ("windowTrim MUST NOT rewrite/truncate it (today `SetHistory`+`Save` does)"), §6 D14.
- **Description**: The ADR says removing the `SetHistory` call makes the log append-only, but the
  companion `Save` call is equally destructive and is **not** removed. `UnifiedStore.Save`
  (`unified.go:756`) calls `backend.Compact`, and `Compact` (`jsonl.go:398`) physically rewrites
  the file dropping all `Skip`-skipped lines — i.e. exactly the evicted turns recall needs. Any
  code path that still calls `Save`/`Compact` on a `.context/` session (session close, periodic
  flush, other callers of `Sessions.Save`) silently truncates the archive to the live window.
- **Impact**: recall returns nothing for evicted turns after the next `Compact`; the "no data loss
  within the retention window" guarantee (§7) is false wherever `Compact` runs. This is the same
  class of silent loss the ADR claims to eliminate.
- **Recommendation**: Add an explicit requirement: `Compact` MUST be a no-op (or forbidden) for
  `.context/` session logs once paging is enabled; audit every caller of `Sessions.Save` /
  `UnifiedStore.Save` / `backend.Compact` for context sessions and enumerate them in the ADR.
  Alternatively state that `.context/` uses a distinct store instance that never compacts.

#### [MAJ-003] Agent-scoped recall (NFR-6/M-2) is not implementable — messages carry no agent identity

- **Lens**: Infeasibility
- **Affected section**: NFR-6, M-2, FR-6 ("Output is scoped to the current agent"), §7 "Cross-agent bleed".
- **Description**: `context.jsonl` is one file **per session** (`.context/{sessionID}.jsonl`,
  `unified.go:787`), shared across agents on handoff/subturn (`SwitchAgent` flips
  `ActiveAgentID` on the *same* session, `unified.go:406-424`). The persisted line type is
  `providers.Message` (`protocoltypes/types.go:85`), whose fields are
  `Role/Content/Media/ReasoningContent/SystemParts/ToolCalls/ToolCallID` — **no agent id, no
  author tag**. So there is no field on which to filter recall to "the current agent." The ADR
  asserts scoping as a hard requirement but the data model cannot express it.
- **Impact**: NFR-6 cannot be satisfied without a schema change; plan-spec will discover this late.
  Cross-agent bleed (the exact risk the ADR flags) is unpreventable on the current record.
- **Recommendation**: Decide and state one of: (a) add an `AgentID`/author field to the persisted
  context record (a wire/persistence-format change — note Constraint #8 if it crosses the boundary,
  and a migration/back-compat story for existing logs with no tag); (b) partition `.context/` per
  agent (`{sessionID}.{agentID}.jsonl`) — changes the "single provider-valid replay log" premise;
  or (c) downgrade NFR-6 to "recall may include turns authored while another agent was active"
  and remove the "MUST NOT surface another agent's turns" guarantee. As written, the requirement
  and the data model contradict each other.

#### [MAJ-004] D14 "never touch an active session's log" contradicts the actual `ModTime`-based sweep

- **Lens**: Incorrectness / Inoperability
- **Affected section**: FR-9, D14, §6 ("sweeps inactive sessions' logs … never active sessions"), §7.
- **Description**: The existing retention sweep decides deletion purely by file `ModTime` vs a
  `cutoff` (`retention_sweep.go:30,84`). There is no notion of "active" (in-RAM / has-a-live-loop)
  vs "inactive." A long-lived heartbeat session that is genuinely active but has not appended in
  `retentionDays` would be swept; conversely a cold session appended-to recently is "active" by
  ModTime. The ADR repeatedly promises the sweep will "never touch an active session's log," but
  no liveness signal is defined or referenced.
- **Impact**: The safety property ("recall recovers data within the retention window even under a
  windowTrim bug", §7) can delete the live archive of an actually-active session, or the sweep
  fails to bound a quiet-but-live session — either way the guarantee is unmet.
- **Recommendation**: Define "active" concretely and how the sweep learns it (e.g. a live-session
  registry the sweep consults; a `meta.json` `LastActiveAt`/`Closed` flag; or a lock the loop holds).
  State the retention parameter for `.context/` explicitly (a new key, or reuse `session_days`) and
  whether it can differ from transcript retention. Until then FR-9/D14 are underspecified for
  implementation.

#### [MAJ-005] "No material p50 regression" and eviction-thrash bound are asserted without measurable thresholds

- **Lens**: Infeasibility (untestable) / Incompleteness
- **Affected section**: NFR-2, NFR-5, D8, D13, §9 step 3.
- **Description**: NFR-2 ("No material p50 turn-assembly regression") has no number — "material"
  is subjective and untestable. NFR-5 requires `window + recalled-chunk` be "budgeted together so
  a paged-in chunk cannot cause an eviction↔recall thrash," but gives no rule: what happens when a
  requested `turn_range`/recall result *cannot* fit alongside the floor window? The ADR defers
  D8/D13 numbers to plan-spec, but the *anti-thrash invariant itself* (not just the numbers) is a
  correctness property that must be stated here, since it is the core one-way-door safety argument.
- **Impact**: "budget together" is unimplementable as written — an engineer cannot tell whether to
  reject the recall, truncate the chunk, or shrink the window, and there is no pass/fail for the
  benchmark gate the ADR calls mandatory.
- **Recommendation**: State the invariant precisely, e.g.: "recall output is capped so that
  `pinned + floor-window + breadcrumb + recalled-chunk ≤ contextWindow − MaxTokens`; if a requested
  range exceeds that, recall truncates to the newest turns that fit and returns an explicit
  'N turns omitted' notice — it never evicts window turns to make room." Give NFR-2 a concrete
  budget (e.g. "≤ X ms p50 delta at N-turn history on the bench harness").

#### [MAJ-006] Turn-boundary eviction over an append-only log with `tool_call_id` pairs is asserted, not shown safe

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: FR-1, D9, D10, §7 "Provider-validity on eviction."
- **Description**: The design evicts "oldest whole Turn(s)" by (per MAJ-001) advancing `Skip`. But
  `Skip` is a **line count** from the top of the file; `splitHistoryAtTurnMidpoint`/`parseTurnBoundaries`
  operate on an in-memory `[]providers.Message` slice. The ADR does not show that a Turn boundary in
  the parsed history maps cleanly to a line offset in the append-only file — especially once the
  file also contains previously-evicted lines that `GetHistory` skips. If the window read
  (`Skip`-based) can begin mid-Turn (e.g. on an `assistant` message whose matching `tool` result
  precedes the Skip point, or a bare `tool` line whose call was skipped), the replayed window is
  provider-invalid (orphaned `tool_call_id`), which is the exact failure §7 says the design prevents.
- **Impact**: A `Skip` value that lands inside a tool-call/tool-result pair yields a 400 from the
  provider on the very next turn — a hot-path break with no summary fallback (hard-replace).
- **Recommendation**: Require that the evicted `Skip` offset is computed from Turn boundaries in
  file-line space (not slice space) and always lands on a Turn boundary, and that the floor is "keep
  the last complete Turn." Add an explicit invariant/test: the `Skip`-read window never begins on a
  `tool` role or an `assistant` message with unresolved `tool_call_id`. Note that today's
  `TruncateHistory(keepLast)` counts raw lines, not Turns — so it cannot be reused unchanged.

---

### MINOR Findings

#### [MIN-001] Stale line-number citation for `handleModelSwitch`

- **Lens**: Incorrectness
- **Affected section**: FR-8, "handleModelSwitch (`loop.go:5020`)".
- **Description**: `handleModelSwitch` is defined at `loop.go:7659`, not `5020`. Line 5020 is the
  *caller* (the switch-detection site). The rev.1 review already noted the definition is at 7659, so
  this citation slipped through the revision. The other cited lines check out (`forceCompression`
  7411, `splitHistoryAtTurnMidpoint` `context_budget.go:187`, trigger sites 5075/5846/5981, and the
  D11 archive facts), so this one stale citation undermines the "code-cited" credibility.
- **Recommendation**: Correct to `loop.go:7659` (definition); if the caller was intended, say so.

#### [MIN-002] Breadcrumb "prominent" is subjective and untestable

- **Lens**: Ambiguity
- **Affected section**: FR-5, D5, D12 ("It MUST be prominent").
- **Description**: "Prominent" has no operational definition — position in the system prompt, a
  required marker string, a minimum token budget? Two engineers will place it differently, and the
  M-4 mitigation ("prominent breadcrumb naming `recall_conversation`") — described as the single
  biggest product-risk mitigation — hinges on this undefined word.
- **Recommendation**: Specify placement and form: e.g. "rendered as a dedicated `## Evicted context`
  section immediately after pinned core, always naming the `recall_conversation` tool and the
  turn-range, never folded into the summary." Give plan-spec a concrete template.

#### [MIN-003] Tool-count claim inconsistent (83 vs project record of 78)

- **Lens**: Inconsistency
- **Affected section**: §4 ("Roster ~83 tools"), §5 A/C ("Zero new surface" vs "+1 tool marginal").
- **Description**: The ADR states ~83 tools; the project's own tool-refactor record says 78 tools /
  16 categories. Minor, but the "+1 marginal" argument for a new tool should cite the real current
  count, and §5 Option C says "Zero new surface" while §6 says "+1 tool is marginal" — pick one.
- **Recommendation**: Reconcile against the live registry count; state the exact number and make the
  Option C surface-cost wording consistent with §6.

#### [MIN-004] Provider-validity of a *partial* recall not specified

- **Lens**: Incompleteness
- **Affected section**: FR-6, NFR-3, D13.
- **Description**: NFR-3 requires recall return provider-valid turns (tool_call_id + results
  present). But D13 caps output at "≤ ~8 turns"/token budget and a BM25/turn-range/time query may
  select a *fragment* of a Turn. It is unstated whether recall snaps its selection to whole Turns
  (to stay provider-valid) or may return a lone tool-result line. If the recalled chunk is injected
  back into the live window (implied by NFR-5's "paged-in chunk"), a fragment breaks validity.
- **Recommendation**: State that recall always returns whole Turns and, if injected into context,
  the injected form is provider-valid (or is inserted as read-only `user`-role quoted text, not as
  replayable `assistant`/`tool` messages). Clarify whether recall output re-enters the window or is
  surfaced only as tool output.

#### [MIN-005] BM25 recall corpus — live window vs full archive — unstated

- **Lens**: Ambiguity
- **Affected section**: FR-6, M-1.
- **Description**: The recall corpus is "this session's `context.jsonl`," but with the `Skip`
  model there are two readings — the live window (skipped read) or the whole archive (Skip=0 read).
  Recall's entire purpose is to reach evicted turns, so it must read the **full** file (Skip=0), yet
  the ADR never says which read recall uses, and reusing `GetHistory` (which honours `Skip`) would
  make recall unable to see what was evicted — defeating the feature.
- **Recommendation**: State that recall reads the full archive (`readMessages(path, 0)` / a
  Skip-ignoring read), explicitly bypassing the live-window `Skip`.

---

### Observations

#### [OBS-001] BM25 generalization (M-1) — consider a typed shared core, not in-place generics

- **Lens**: Overcomplexity
- **Affected section**: M-1, Constraints.
- **Suggestion**: `rankRetrosBM25`/`retroTokenize` (`retro_bm25.go:29,105`) are `Retro`-typed.
  Rather than making them generic over an interface, a small shared `bm25Score(tokens []string, …)`
  core with two thin typed callers (Retro, Message) is likely simpler and keeps the hot retro path
  monomorphic. Worth a note so plan-spec doesn't over-abstract.

#### [OBS-002] Consider whether recall needs BM25 at all for v1

- **Lens**: Overcomplexity
- **Affected section**: FR-6 (three retrieval modes: BM25, turn-range, time).
- **Suggestion**: turn-range + time are cheap and deterministic; BM25 adds the M-1 generalization
  cost. If the breadcrumb already names a turn-range, a v1 that ships turn-range/time only (BM25
  fast-follow) would cut scope on the hot new path. Not a defect — a scoping question for the ADR to
  acknowledge.

#### [OBS-003] Migration honesty for in-flight sessions with pre-existing rewritten logs

- **Lens**: Incompleteness
- **Affected section**: NFR-4 ("no migration").
- **Suggestion**: Existing sessions' `context.jsonl` files were already truncated by historical
  `forceCompression`/`Compact` runs — their evicted turns are *gone* and unrecoverable by recall.
  NFR-4 is correct that no migration is needed, but the ADR should state the honest limitation:
  recall only recovers turns evicted *after* the append-only switch; older evicted content stays
  lost. Users may expect otherwise.

#### [OBS-004] Observability for the new eviction/recall path unspecified

- **Lens**: Inoperability
- **Affected section**: whole ADR (no monitoring/metrics section).
- **Suggestion**: For a hard-replace change on the hottest path, on-call will want signals: eviction
  count per session, recall call rate + hit/empty rate (the M-4 "model never pages" detector),
  archive size growth, and `Skip` advancement. The ADR mentions a benchmark harness but no runtime
  metrics/logs. Recommend a one-line requirement for structured counters so the M-4 product risk is
  observable in production, not just in the pre-merge eval.

---

## Structural Integrity (Variant B: Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | Direction (D1–D6) clear; NFR-2/NFR-5 lack measurable criteria (MAJ-005); M-4 mitigation success has no pass condition. |
| Cross-references are consistent | PARTIAL | FR↔D↔§ mapping is coherent; one stale code citation (MIN-001); tool count / "zero new surface" inconsistent (MIN-003). |
| Scope boundaries are explicit | PASS | Hard-replace/no-flag, v0.3-on-hotfix note, "not `transcript.jsonl`" all explicit. |
| Success criteria are measurable | FAIL | "No material regression" / "prominent" / "budgeted together" are unmeasurable as written (MAJ-005, MIN-002). |
| Error/failure scenarios addressed | PARTIAL | §7 covers torn-pair, re-overflow, bleed, unbounded growth; but the `Compact`-still-destroys path (MAJ-002) and the mid-Turn `Skip` case (MAJ-006) are not covered. |
| Dependencies between requirements identified | PARTIAL | FR-2↔FR-6 (append-only enables recall) is clear; the FR-6↔data-model dependency (needs an agent tag, MAJ-003) is unidentified. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Requirement |
|----------|-----------------|----------------------|
| Provider-validity (eviction) | No test that a `Skip`-based window never begins mid-Turn / orphans a `tool_call_id` | FR-1, D9, D10, MAJ-006 |
| Archive durability | No test that `Compact`/`Save` on a `.context/` session preserves evicted lines | FR-2, MAJ-002 |
| Agent isolation | No test that recall excludes another agent's turns (blocked by MAJ-003 — nothing to assert on yet) | NFR-6, MAJ-003 |
| Retention/liveness | No test that an active session's log survives the sweep, or that a quiet-but-live heartbeat session is not deleted | FR-9, D14, MAJ-004 |
| Anti-thrash | No test that a recall exceeding budget truncates rather than evicting window turns | NFR-5, MAJ-005 |
| Model-paging (M-4) | ADR calls for the eval but sets no threshold (what page-rate = pass?) | §7, §9 |
| Recall corpus | No test that recall reads the full archive (Skip=0), not the live window | FR-6, MIN-005 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| append-only `context.jsonl` | ok | risk | ok | risk | risk | ok | T: `Compact`/`Save` still rewrites (MAJ-002); I: cross-agent read of shared log (MAJ-003); D: unbounded growth on active heartbeat sessions until liveness-aware sweep exists (MAJ-004). |
| `recall_conversation` tool | ok | ok | ok | risk | risk | risk | I/E: no agent scoping possible on current record → a subagent could page in another agent's turns (MAJ-003); D: unbounded recall could re-overflow if anti-thrash invariant not enforced (MAJ-005). Reads local session file only — no new network surface. |
| `.context/` retention sweep | ok | risk | ok | ok | risk | ok | T/D: deletes by ModTime with no liveness check → can drop an active session's archive or fail to bound a quiet-live one (MAJ-004). |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable.

Note: this is a local-file, in-process feature (no new auth boundary, no network), so classic
S/R/E network threats are largely N/A; the real STRIDE weight is on Tampering (archive integrity),
Information Disclosure (cross-agent), and DoS (unbounded growth / recall re-overflow).

---

## Unasked Questions

1. Given `meta.Skip` + append-only `addMsg` already exist, what specifically about the write path is
   still unknown — and does windowTrim just advance `Skip` by Turn instead of calling `SetHistory`?
   (MAJ-001)
2. Every caller of `Sessions.Save`/`UnifiedStore.Save`/`backend.Compact` on a `.context/` session
   will collapse the archive — which callers exist, and how is each gated off? (MAJ-002)
3. On what field does recall filter "to the current agent" when `providers.Message` has no agent id
   and the log is one shared file per session? (MAJ-003)
4. What makes a session "active" for the retention sweep, and where does the ModTime-based sweep get
   that signal? (MAJ-004)
5. When a requested recall range does not fit the residual budget, does recall reject, truncate, or
   shrink the window — and what is the exact fit inequality? (MAJ-005)
6. Does the `Skip` offset for eviction land on Turn boundaries in *file-line* space, and how is a
   mid-pair `Skip` prevented? (MAJ-006)
7. Does recall read the full archive (Skip=0) or the live window, and does its output re-enter the
   provider window or stay tool-output-only? (MIN-004, MIN-005)
8. What is the retention parameter for `.context/` — a new config key, or `session_days`? Can it
   differ from transcript retention? (MAJ-004)
9. What page-rate / recall-hit threshold constitutes a PASS for the mandatory M-4 model-paging eval?

---

## Verdict Rationale

The rev. 1 BLOCK is cleared: the archive-identity correction (`context.jsonl` over `transcript.jsonl`,
C-1/C-2/C-3) is verified against `unified.go:83`, `daypartition.go:141` (transcript has no
`tool_call_id`/tool-result type), and `retention_sweep.go:62-91` (`.context/` skipped). The
direction (D1–D6) is sound and well-grounded.

The verdict is **REVISE**, not PASS, because rev. 2's new load-bearing claims are inconsistent with
the code they depend on. Most importantly, the ADR's own stated highest-uncertainty item — the
append-only write-path conversion — is answered by the existing `meta.Skip`/`addMsg`/`TruncateHistory`
machinery the ADR never cites (MAJ-001), and the companion `Compact`/`Save` destructor is left
active (MAJ-002); together these mean the design as written does *not* yet guarantee the no-loss
property it claims. Two further requirements are infeasible/incorrect against the data model:
agent-scoped recall has no field to filter on (MAJ-003) and the "never touch an active session"
retention promise contradicts the ModTime sweep (MAJ-004). None of these are fatal to the direction
— they are gaps a corrected ADR can close — but they must be resolved before plan-spec so the spec
scopes the *actual* (smaller, `Skip`-based) change and does not commit to unimplementable
guarantees.

### Recommended Next Actions

- [ ] Rewrite FR-2/D11/D14 around the existing `Skip`/append-only primitive; cite `jsonl.go:213/266/323/398` (MAJ-001).
- [ ] Add a requirement that `Compact`/`Save` is disabled for `.context/` sessions; enumerate all callers (MAJ-002).
- [ ] Decide and specify how recall achieves agent scoping (add field / per-agent partition / downgrade NFR-6) (MAJ-003).
- [ ] Define "active" and the liveness signal the retention sweep uses; name the `.context/` retention parameter (MAJ-004).
- [ ] State the anti-thrash fit inequality and give NFR-2 a numeric threshold (MAJ-005).
- [ ] Specify Turn-boundary-safe `Skip` offsets and a "window never begins mid-pair" invariant (MAJ-006).
- [ ] Fix the `handleModelSwitch` citation (7659), reconcile the tool count / surface wording, define "prominent," and clarify recall's corpus read (Skip=0) and re-entry semantics (MIN-001..005).
```

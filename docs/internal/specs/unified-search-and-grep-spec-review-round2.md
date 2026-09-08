# Adversarial Review: Unified Library Search & Grep Engine — Implementation Spec (Round 2 of 2, FINAL)

**Spec reviewed**: `docs/internal/specs/unified-search-and-grep-spec.md` (@ `a9f8b38a5`, worktree `wt-integrate`, branch `integrate/library-improvements-v0.1.1`)
**Round-1 review**: `docs/internal/specs/unified-search-and-grep-spec-review-round1.md` (27 findings, verdict BLOCK)
**Review date**: 2026-09-07
**Review mode**: plan-spec (BDD + FR/SC IDs + traceability matrix detected)
**Verdict**: **REVISE** (0 CRITICAL / 6 MAJOR / 12 MINOR / 4 OBSERVATION)

Every NEW code-fact claim the revision added was re-verified against the live worktree
(see the re-verification table at the end); all check out, with one framing problem
(R2-MAJ-002) and two engine caveats the spec does not yet know about (R2-MIN-001,
R2-MAJ-004). All 27 round-1 findings were audited for genuine resolution: **24
RESOLVED, 3 PARTIAL, 0 UNRESOLVED**.

## Executive Summary

The revision is substantively real: the three round-1 CRITICALs are closed in the ways
that matter (attachments are implementable end-to-end against the engine as verified in
code; the walk is rate-limited, capped, and cancellable; output is bounded at three
layers). No finding in this round is shipping-incident class. But three round-1
findings are only partially resolved — the policy roster still omits the seeded
specialist tier that `denyAllThenOverride` will stamp to explicit **deny** the moment
grep enters the catalog, directly contradicting the founder's ALL-agents ruling while
the spec's own test 25 would pass; the concurrency regime never says whether the
2-walk cap and any rate limiter cover the **agent tool path**; and smart-case's
interaction with the literal fast path makes test 1 unimplementable as written for
lowercase patterns. On top of that, the revision introduced one structural defect of
its own: it is a **delta overlay**, not a spec — FR-001..015, most BDD bodies, the
dataset matrices and H1–H7 now exist only in git history. All six MAJORs are cheap,
text-level fixes. Fix them and this goes to build.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 12 |
| OBSERVATION | 4 |
| **Total** | **22** |

---

## Part 1 — Round-1 resolution audit (all 27 findings)

Evidence cites the revised spec by line (`spec:N`) and the worktree by `file:line`.

| R1 finding | Status | Evidence |
|---|---|---|
| CRIT-001 attachment search deleted | **RESOLVED** | US-1 rewritten with Attachments group (spec:93-117); MV-9 `attachments[]` (spec:267-272); SC-009 parity check (spec:505-507); tests 26/28; §2 rows corrected (spec:59-61); staging note (spec:27). Implementability verified in code — see re-verification table rows 1–3 and R2-MIN-001 for the one platform caveat. |
| CRIT-002 no limiter/cancel/cap | **PARTIAL** | REST surface fully closed: MV-11 (spec:274-278), FR-017 (spec:482-484), tests 6/18, BDD cancel+busy scenarios (spec:332-342), edge case (spec:204-205). NOT closed: the round-1 recommendation's second half — a limiter for the **tool path** (`knowledgeToolLimiter` treatment) — was dropped with no recorded ruling, and MV-11 never says whether the 2-walk cap counts tool walks. → R2-MAJ-003. |
| CRIT-003 unbounded output | **RESOLVED** | MV-3 1 MiB payload bound (spec:250-251), MV-6 512-byte excerpts (spec:258), MV-12 64,000-char tool cap (spec:279-281), FR-018 (spec:485-486), US-3 AS-8 (spec:168-170), test 22, BDD (spec:350-355), windowTrim rationale in integration boundaries (spec:290-291). Accepted deviation: round-1's suggested lower tool-default match cap was not adopted — acceptable, the 64k serialization cap bounds the context damage regardless. Two residue items → R2-MIN-004, R2-MIN-009. |
| MAJ-001 honesty parity (6 signals vs 3) | **RESOLVED** | MV-9 now carries all six: X-of-Y + unknown-total, server-authored `statement`, capped boolean, `limit_clamped`+`limit_requested`, per-hit `excerpt_unavailable`, attachments group with filename-match kind (spec:267-272); DS-4 extended (spec:455-456); tests 26/28; regression checklist demands assert-level port-diff (spec:458-462). Caveat: the reason ENUM became a boolean → R2-MIN-010 (acceptable, record it). |
| MAJ-002 Worker/roster/upgrade hole | **PARTIAL** | Worker ruled explicit allow with founder rationale (spec:10-14, 261-266, 476-479); system agents ruled (spec:263-264); upgrade path pinned via drift backfill + test 25 assert (spec:265-266, 441). NOT closed: the three seeded **specialists** (Planner/Explorer/Researcher, `pkg/coreagent/core.go:42,98-109`) appear in NO roster enumeration, and the backfill value for pre-existing **custom** agents (deny, by verified mechanism) is unstated. → R2-MAJ-002. |
| MAJ-003 pattern semantics undefined | **PARTIAL** | Literal-vs-regex per surface ruled: FR-016 (spec:480-481), US-2 AS-7 (spec:139-141), BDD metacharacters scenario (spec:318-323), non-behavior (spec:232-233), behavioral contract (spec:219-220). NOT closed: **name-match case semantics** still appear nowhere; DS-1 has no regex-flag column so row 7 still contradicts FR-016 as written; smart-case's Unicode definition is absent. → R2-MAJ-004, R2-MIN-002. |
| MAJ-004 wire fields unenumerated | **RESOLVED** | Sketch with fields/defaults (spec:402-413); complete 7-token reason enum (spec:251-253); per-file-cap-as-skip ruled with its own BDD scenario (spec:248-249, 378-384); flat hit + `match_kind` avoids the ADR-034 discriminator trap (spec:409). New gaps the sketch itself exposes are filed separately (R2-MAJ-005, R2-MIN-005/006/007/009). |
| MAJ-005 bar at Library root | **RESOLVED** | US-4 AS-2 disabled-with-hint (spec:172-180); §2 cites the existing null-workspace disabled state (`LibrarySearchBar.tsx:81`, verified); test 29. |
| MAJ-006 grep workspace scoping | **RESOLVED** | FR-020 (spec:488-489), US-3 AS-7 (spec:166-167), non-behavior (spec:234), BDD (spec:357-361), test 21; the sketch has a `path` scope and no workspace field (spec:403). |
| MAJ-007 chmod-000 void under root CI | **RESOLVED** | Test 11 now "via injectable FS-error seam (and a dangling-symlink variant) — never chmod-000" (spec:427). |
| MAJ-008 DS-2 50k-file trees | **RESOLVED** | Test 5 "bounds injected via an options struct" (spec:421); DS-2 "all via injected options" (spec:453). |
| MAJ-009 SC-003 flake gate | **RESOLVED** | SC-003 non-gating, recorded, solo-measured (spec:499-501); A6 updated (spec:534). |
| MIN-001 blast-radius inventory | **RESOLVED** | (a) inboundschemas via regen Step 5 (spec:65, 189-193); (b) `api.knowledge.test.ts` (spec:64, 188-189); (c) `KnowledgePanel.searchFn` retype (spec:63, test 30); (d) the two gateway wiring sites (spec:68, 396-400). |
| MIN-002 pin-test graft | **RESOLVED** | Own sibling pin test 23 (spec:439, 67). |
| MIN-003 gitignore scope ×3 | **RESOLVED** | FR-007 single statement, both name+content, no toggle (spec:473-475); edge case aligned (spec:206-209); A2 kept as deferral record (spec:530). |
| MIN-004 gitignore semantics/library | **RESOLVED** | `sabhiram/go-gitignore` chosen with rationale (spec:45-48); nested files, negations, git-ness-irrelevant, pruned-count-observable (spec:206-209, 473-475); test 8 (spec:424). |
| MIN-005 MV-5 untested; 401 missing | **RESOLVED** | 401 in MV-1 + test 17 (spec:240-243, 433); zero-hit `[]` assert in test 16 (spec:432); MV-5 names the handler path (spec:256-257). |
| MIN-006 pathological scenario orphan | **RESOLVED** | Test 14 (spec:430). Residual matrix nit → R2-MIN-012. |
| MIN-007 root lost mid-walk | **RESOLVED** | US-2 AS-6 (spec:135-138), `root_lost` in the enum (spec:253), FR-021 (spec:492-493), test 12, BDD (spec:325-330), UI wording in test 31 (spec:447). |
| MIN-008 hidden-file wire default | **RESOLVED** | `include_hidden` request flag, default false, `.git`-pruning stated as a property (spec:210-212, 405). Follow-on question → R2-MIN-006. |
| MIN-009 §2 reference drift | **RESOLVED** | `:151` case label / `:156` dispatch / `:461` handler (spec:61, verified); pin-lineage sentence (spec:5-7). |
| MIN-010 one deadline, two callers | **RESOLVED** | SPA 3 s interactive override in MV-3 (spec:249-250); no-streaming recorded as a v1 choice (spec:38-39, 237). |
| OBS-001 tool name "grep" | **RESOLVED** | Name kept (spec:397). |
| OBS-002 stage A first | **RESOLVED** | Staging rule in scope line (spec:27). |
| OBS-003 ranking parity | **RESOLVED** | Non-behavior (spec:235-236), A3 (spec:531), regression ordering assert (spec:461-462). |
| OBS-004 bounds-config non-decision | **RESOLVED** | Out-of-scope + MV-3 + non-behavior all state "not operator-configurable in v1" (spec:39-40, 246, 230-231). |
| OBS-005 founder ruling needed | **RESOLVED** | Ruling recorded in header + A4 CLOSED (spec:10-14, 51-53, 532). |

---

## Part 2 — Findings (new material introduced by the revision)

### MAJOR findings

#### [R2-MAJ-001] The revision is a delta overlay, not a self-contained spec — half its normative content exists only in git history

- **Lens**: Incompleteness (structural)
- **Affected section**: §4 non-behaviors ("unchanged from draft, plus:", spec:229), §5 ("unchanged scenarios kept as in draft", spec:297; the name-only list, spec:386-390), §6 datasets ("DS-1 … adds", spec:452-456), §6 regression ("as draft, plus", spec:458), §7 ("FR-001..FR-015 as draft, with amendments", spec:469; "SC-001, SC-002, SC-004..SC-007 as draft", spec:498; "Full matrix = draft matrix + these rows", spec:521), §9 ("H1–H7 as draft, plus:", spec:538)
- **Description**: The revised file **replaced** the draft in the tree (`git show
  6a4492fb3:docs/internal/specs/unified-search-and-grep-spec.md` is the only copy of
  the referenced content). An implementer, `/taskify`, or a reviewer reading the spec
  at HEAD cannot see the text of FR-001 through FR-015, eleven of the BDD scenario
  bodies, the DS-1..DS-4 base matrices, the draft traceability matrix, SC-001/002/004..007,
  the integration-boundaries table, or H1–H7 — yet the revision's own deltas amend and
  trace into exactly that content. This is the spec that "goes to implementation"
  after this round; a spec whose requirements require `git archaeology` to read will
  be implemented from memory and diffs.
- **Impact**: Guaranteed divergence between what was ratified and what gets built;
  the traceability matrix is unverifiable from the document itself.
- **Recommendation**: In the fix pass, inline every retained section verbatim (with
  the revision's amendments applied in place) so the file at HEAD is the complete,
  single-document spec. Keep the "changed in revision" markers if useful, but the
  document must stand alone.

#### [R2-MAJ-002] The ALL-agents-allow roster omits the seeded specialist tier — which will be stamped explicit DENY by the very mechanism the spec relies on

- **Lens**: Inconsistency + Insecurity (governance) — completes round-1 MAJ-002
- **Affected section**: Header ruling (b) (spec:12-14), §1 A4 (spec:51-53), MV-8 (spec:261-266), FR-009 (spec:476-479), test 25 (spec:441), §8 A4 (spec:532), §2 policy row (spec:70)
- **Description**: Three defects, one root:
  1. **Specialists.** Every roster enumeration reads "Jim/Mia/Ava/Ray, the Worker,
     and system agents". The seeded roster also contains the three delegation-only
     **specialists** — Planner, Explorer, Researcher (`pkg/coreagent/core.go:42`,
     `specialistIDs` at `:98-109`) — seeded via `denyAllThenOverride` (`core.go:570-582`),
     which "returns a fully-enumerated policy map covering every [catalog tool]" and
     stamps **explicit deny** for any tool not in the role's override list
     (`core.go:861-867`: "Deny-by-default; only the tools each role plausibly needs
     are allowed"). The moment `grep` enters the catalog, all three specialists get
     `grep: deny` **unless each override map is amended** — contradicting the
     founder's "available to ALL agents" ruling — and test 25, written from the
     spec's own roster line, would pass green over that contradiction. This is not
     silent ceiling inheritance (the entry is explicit), so no drift guard fires
     either.
  2. **Custom agents on upgrade.** MV-8 says "the drift backfill writes an explicit
     per-agent `grep` entry on pre-existing agents (test asserts it)" without saying
     the **value**. The verified mechanism (`pkg/coreagent/tool_policy_catalog_drift.go:56-72`):
     seeded agents take their own seed's value (allow, post-ruling); System Agents
     are skipped because `seedSystemAgents` re-enforces their exact map each boot;
     **any other agent takes "deny"** — fail-closed by design. So on an upgraded
     install, every operator-created agent resolves `grep: deny` until the operator
     grants it. That is probably the right call — but it means "grep is available to
     ALL agents" is true of the *seeded* roster only, and the spec never says so. An
     implementer writing test 25's backfill assert has to guess between allow
     (matching the ruling's phrasing) and deny (matching the mechanism).
  3. **Precedent framing.** The §2 table row (spec:70) still cites "(Worker deny
     precedent `core.go:854`)" with no note that grep **deliberately diverges** from
     that knowledge-family precedent per the founder's ruling. As the only
     precedent named in the codebase-context table, it points the implementer at
     exactly the posture the ruling overturned.
- **Impact**: A fresh install ships three seeded agents whose grep posture
  contradicts the founder's recorded ruling, with the spec's own test passing; the
  upgrade-path test is unwritable without guessing.
- **Recommendation**: (1) Extend the roster in MV-8, FR-009, and test 25 to name
  Planner/Explorer/Researcher with explicit `allow` (and require test 25 to iterate
  the FULL seeded roster from the seed functions, not a hand-list). (2) Add one
  sentence to MV-8/FR-009: "pre-existing seeded agents backfill to their seed's
  `allow`; pre-existing custom agents backfill to `deny` (standard fail-closed rule)
  — the ALL-agents ruling governs the seeded roster; operators grant custom agents
  themselves." (3) Reword spec:70 to "knowledge-family Worker deny precedent
  (`core.go:846-859`) — grep deliberately diverges, founder 2026-09-07".

#### [R2-MAJ-003] The concurrency regime never says whether the 2-walk cap and rate limiting cover the agent tool path — and the busy contract is half-specified

- **Lens**: Ambiguity + Insecurity (DoS) — completes round-1 CRIT-002
- **Affected section**: MV-11 (spec:274-278), FR-017 (spec:482-484), integration boundaries (spec:288-291), test 18 (spec:434), edge case (spec:204-205)
- **Description**:
  1. **Scope of the semaphore.** MV-11 says "at most 2 concurrent file walks per
     gateway"; FR-017 scopes every clause to "the endpoint". If the cap covers only
     REST walks, the grep **tool** path can run unlimited concurrent walks (delegated
     sub-turns parallelize agent work) — the residual half of round-1 CRIT-002. If it
     covers both, two agent greps starve the human bar: the person typing sees
     429-busy while Jim works. Neither reading is stated, and test 18 tests only the
     REST side.
  2. **Tool-path rate limiting.** Round-1's recommendation (a) explicitly included
     "give the tool path the `knowledgeToolLimiter` treatment" (the knowledge tools'
     own limiter, `pkg/gateway/rest_knowledge.go:94`). The revision adopted the REST
     half and silently dropped the tool half — no ruling recorded either way.
  3. **The busy contract.** The sibling 429 contract always carries `Retry-After`
     (`rest_knowledge.go:748-753`); MV-11's busy-429 names no `Retry-After` and the
     spec never says what the **SPA does** on either 429 flavor. The retrieval
     limiter is 60 calls/minute per workspace (`pkg/knowledge/tools.go:210-215`) —
     a fast typist with a short debounce CAN trip it, and a person mid-word must not
     get an error banner. Likewise the **agent** experience of "busy" (structured
     tool error with a retry hint? blocking wait?) is unstated.
- **Impact**: The DoS bound either has a hole (tool walks uncounted) or a UX trap
  (agents starve humans); two implementers ship two different systems; the busy path
  reaches users with undefined behavior on both surfaces.
- **Recommendation**: Add to MV-11/FR-017: "The 2-walk semaphore covers ALL engine
  walks — REST and tool alike. On saturation: REST answers 429 with
  `Retry-After: 1` (sibling shape); the tool returns a structured busy error naming
  the retry hint (never a hang). The tool path additionally sits behind its own
  retrieval-class limiter (`knowledgeToolLimiter` precedent). The SPA treats both
  429 flavors as a transient still-searching state — keep the pending indicator,
  auto-retry once after `Retry-After` — never an error banner." Add a vitest row for
  the 429 UI state (extend test 31) and a tool-busy case to test 20 or 22.

#### [R2-MAJ-004] Smart-case collides with the literal fast path: test 1 is unimplementable as written for lowercase patterns, and Unicode folding is undefined

- **Lens**: Infeasibility + Ambiguity — completes round-1 MAJ-003
- **Affected section**: Test 1 (spec:417, "asserts the regex engine is NOT invoked for plain strings"), MV-13 (spec:282-285), FR-016 (spec:480-481), FR-022 (spec:494-495), DS-1 smart-case + CJK rows, test 2
- **Description**: Smart-case makes every all-lowercase pattern **case-insensitive**.
  Case-insensitive literal matching without the regex engine requires hand-rolled
  case folding; ASCII folding is easy, but **Unicode** folding (`café`/`CAFÉ`,
  dotted/dotless I, ligatures) is a bug farm — ripgrep itself routes
  non-ASCII-insensitive literals through the regex engine. As written, test 1's
  "regex engine NOT invoked for plain strings" either fails against any sane
  implementation (which will use the regex engine for insensitive non-ASCII
  literals) or forces the team to hand-roll a Unicode case folder — precisely the
  class of hand-rolling MIN-004 banned for gitignore. Related residue from round-1
  MAJ-003: **name-match case semantics** (US-2 AS-1's "report" → `Q3 report.md`)
  still appear nowhere — substring yes (implied), case rule absent.
- **Impact**: The flagship best-in-class test is red on day one or drives a
  correctness-risky hand-rolled folder; name matching ships with accidental
  semantics.
- **Recommendation**: Define in §4 + FR-016: "Smart-case: a pattern containing an
  uppercase letter is case-sensitive; otherwise insensitive. The literal fast path
  handles case-sensitive literals and ASCII-insensitive literals without the regex
  engine; a non-ASCII insensitive literal MAY fall back to the regex engine
  (correctness over the lever)." Scope test 1's no-regex assert to exactly those two
  fast-path classes and add a non-ASCII-lowercase row to DS-1 documenting the
  permitted fallback. Add one sentence: "Name matching is case-insensitive substring
  on both surfaces" (or smart-case — pick one and say it).

#### [R2-MAJ-005] FR-019 promises a per-file match cap that no bound, wire field, or test carries

- **Lens**: Inconsistency
- **Affected section**: FR-019 (spec:487, "per-file and total match caps"), US-3 (spec:146-147, same phrase), MV-3 (spec:245-251), wire sketch `limits` (spec:406-407: files, bytes, matches, depth, deadline_ms), DS-2, tests 5/22
- **Description**: MV-3's per-file bound is **bytes** (4 MiB); its match cap is
  **total** (1,000). Nowhere is there a per-file **match** cap: not in MV-3, not in
  the `limits` sketch, not in any DS-2 row, not in any test. FR-019 (and US-3's
  ripgrep-feel list) promise it to the agent twice. One of the two is wrong, and an
  implementer will discover this mid-build — the exact ad-hoc-wire-design failure
  MAJ-004 was raised to prevent.
- **Impact**: Either an unspecified lever gets invented during implementation
  (field name, default, truncation semantics all improvised) or the tool ships
  without a promised capability and test 33's "best-in-class" e2e can't exercise it.
- **Recommendation**: Rule it: add `per_file_matches` to `limits` (suggested default
  unlimited-but-clampable, or a small default like 50 for the tool surface),
  define behavior as a per-file skip-remainder counted in a stats field (mirror the
  per-file byte cap's ruling), add a DS-2 row + test 5 case — OR strike "per-file"
  from FR-019 and US-3 and record the deferral. Either is fine; pick one in the spec.

#### [R2-MAJ-006] A best-in-class grep with no case-mode control: case-sensitive lowercase search is inexpressible

- **Lens**: Incompleteness (against the founder's own bar)
- **Affected section**: US-3 (spec:144-147), wire sketch `FileSearchRequest` (spec:403-407), FR-019, DS-1
- **Description**: Smart-case is the ONLY case mode. An agent that wants to match
  `todo` but not `TODO` cannot: the all-lowercase pattern is auto-insensitive, and
  `regex:true` doesn't escape it (smart-case applies to regex too — US-3/test 2).
  Every tool in the class the founder benchmarked against (`rg -s/-i/-S`, GNU grep
  `-i`) exposes the mode. The sketch has no field for it.
- **Impact**: A stated-MUST "feels like ripgrep" surface ships unable to express one
  of grep's basic queries; agents will get wrong (over-broad) results with no
  workaround, and the gap is a wire-contract change to fix later (Constraint #8
  makes post-hoc additions expensive).
- **Recommendation**: Add `case: "auto" | "sensitive" | "insensitive"` (default
  `auto` = smart-case) to `FileSearchRequest` (tool + REST; the bar always sends
  `auto`), one DS-1 row per non-default mode, and a line in FR-019. Cheap now, a
  contract bump later.

---

### MINOR findings

#### [R2-MIN-001] Attachment parity has a platform hole the spec doesn't know about
- **Lens**: Incorrectness / Incompleteness
- **Affected section**: SC-009 (spec:505-507), MV-9, US-1
- **Description**: A `words + kind=attachment` query is NOT text-only servable —
  `textOnlyServable` whitelists `kind == KindNote` only
  (`pkg/records/knowledgefind/find.go:1066-1077`), so attachment queries need the
  properties index, which does not exist under
  `records_no_sqlite || mipsle || netbsd || (freebsd && arm)`
  (`pkg/records/propindex_stub_unavailable.go:1`). The RETIRED endpoint answered
  attachments from the text index alone (`pkg/knowledge/index.go::indexAttachment`
  — "records an attachment by filename and path ONLY"). On those (niche, whatsmeow-
  less) builds, SC-009's absolute claim — "every filename findable via the retired
  surface is findable via the surviving bar" — is false: the group comes back empty/
  refused.
- **Recommendation**: Add the carve-out to SC-009 and MV-9: on propindex-less
  platforms the Attachments group is empty with `complete:false` + the engine's
  refusal reason surfaced (never a bare empty group). Alternatively extend
  `textOnlyServable` to `kind=attachment` (it consults only the kind column — but
  that is an engine-gate change; if chosen, record it as its own line item, since
  the gate's comment block explicitly claims the kind column is properties-owned).

#### [R2-MIN-002] DS-1 has no regex-flag column; row 7 contradicts FR-016 as written
- **Affected section**: DS-1 (draft rows 1-7 + revision deltas, spec:452), FR-016, MV-1
- **Description**: Draft row 7 — pattern `(` ⇒ "400 with parse error message" — is
  only correct with `regex:true`; FR-016 makes the same input a literal match from
  the bar, and the revision's new `f(x)` row asserts exactly that. The matrix
  records neither which rows set `regex` nor which surface they run against.
- **Recommendation**: Add a `regex` column to DS-1; mark row 7 `regex:true`; mark
  the `f(x)` and smart-case rows `regex:false`.

#### [R2-MIN-003] "All 6 request-level reasons" vs a 7-token enum
- **Affected section**: Test 5 note (spec:421), MV-3 enum (spec:251-253)
- **Description**: The enum has seven tokens; `root_lost` is request-level too but is
  covered by test 12, not test 5. The phrase invites an implementer to treat the
  enum as 6-membered.
- **Recommendation**: Reword test 5's note: "the 6 engineered-bound reasons
  (`root_lost` is test 12's)".

#### [R2-MIN-004] Two truncation layers share one reason field; the 1 MiB cap's enforcement layer is unstated
- **Affected section**: MV-3 `max_output` (spec:250-253), MV-12 (spec:279-281), test 5 vs test 22
- **Description**: An engine-truncated result (`max_matches`) can ALSO exceed the
  64,000-char tool serialization cap — which reason wins in the single
  `truncated_reason`? And is the 1 MiB REST payload bound enforced inside the engine
  (test 5 injects it as a bound, implying an engine-side byte budget over
  accumulated hit content) or at the handler on serialized JSON? The two layers
  measure different things.
- **Recommendation**: One paragraph: the engine enforces an accumulated-output byte
  budget (reason `max_output`); the tool's 64,000-char serialization cap is applied
  after, appending the marker and OVERRIDING the reason to `max_output` if not
  already set (engine reason otherwise preserved in the structured body). REST's
  1 MiB is the engine budget, not a post-hoc serialization check.

#### [R2-MIN-005] REST `context_lines` has no consumer in v1
- **Affected section**: Wire sketch (spec:407: "`context_lines` (0–5, tool/REST both)")
- **Description**: The bar never sends it (no UI is specified to render context
  lines) and the tool reaches the engine in-process, not via REST. A request field
  with zero consumers is speculative generality (Lens 8).
- **Recommendation**: Either drop it from the REST contract for v1, or add one line
  recording the deliberate choice ("REST carries it for API parity with the tool;
  the SPA does not use it in v1") so it's a decision, not drift.

#### [R2-MIN-006] `include_hidden:true` exposes Omnipus-internal directories to content scan
- **Affected section**: Edge case (spec:210-212), wire sketch (spec:405)
- **Description**: The stated property — default-false keeps `.git/`, `.library/`,
  `.omnipus-vault/` out — inverts when a caller (any agent, any REST client) sets
  the flag: index internals, vault bookkeeping, and git object noise become
  content-scannable and can dominate results and the file/byte budgets.
- **Recommendation**: Rule it: `.library/` and `.omnipus-vault/` (and `.git/`) are
  ALWAYS pruned regardless of `include_hidden` (the flag governs user dotfiles), or
  record the exposure as intended. Add a DS-3 row either way. Note the walker must
  still read `.gitignore` files (themselves dot-named) when include_hidden=false.

#### [R2-MIN-007] File-search limit clamps are silent — the vault surface just got clamp disclosure, the new surface doesn't
- **Affected section**: MV-3 ("clamped upward", spec:245-247), `FileSearchResponse` sketch (spec:410-412), MV-9 (spec:270)
- **Description**: A `limits` override above a server cap is clamped with no
  disclosure field, in the same revision that ports `limit_clamped`/`limit_requested`
  to vault search citing FR-037's "the clamp is REPORTED, never silent".
- **Recommendation**: Add a `limits_applied` echo (or a `limits_clamped` boolean) to
  `FileSearchResponse` and one assert in test 16.

#### [R2-MIN-008] FR-022's zero-alloc MUST has no gating verification
- **Affected section**: FR-022 (spec:494-495), MV-13 (spec:282-285), test 34 (non-gating), SC-008 (no alloc threshold)
- **Description**: "Zero-alloc line scanning on the hot path" is a MUST verified
  only by recorded, non-gating benchmarks — a MUST nothing enforces (the
  false-green doc's exact pattern).
- **Recommendation**: Add a unit-level `testing.AllocsPerRun` gate (≤ a small
  constant per scanned line, on the literal path) to test 1 or a sibling, or soften
  the fourth lever to SHOULD + recorded bench. Either close the gap or stop calling
  it a MUST.

#### [R2-MIN-009] Hit granularity is undefined
- **Affected section**: `FileSearchHit` sketch (spec:409), MV-3 max matches, MV-12
- **Description**: Is a hit one matching LINE (multiple hits per file), one FILE
  (first match + count), or one match (two matches on one line = two hits)? The
  match cap, the 64k budget, context-line attachment, and the SPA rendering all
  depend on the answer.
- **Recommendation**: One line: "a hit is one matching line (first match position
  reported); `max matches` counts hits; a name match is one hit with
  `match_kind:name`."

#### [R2-MIN-010] `excerpt_unavailable` shrank from a 5-reason enum to a boolean — fine, but record it
- **Affected section**: MV-9 (spec:271), vs `KnowledgeSearchHit.yaml:57-64`
- **Description**: The retired contract's machine-readable reasons
  (`file_unreadable|file_missing|match_moved|budget_exhausted|attachment_not_read`)
  become a bare boolean. Round-1 allowed "or a reduced enum"; a boolean is the
  maximal reduction and is defensible (the find path cannot attribute the old
  path's re-read reasons), but the retirement of the reasons is currently implicit.
  Also implicit: WHO composes MV-9's server-authored `statement` (the handler,
  mirroring `knowledgeStatement`) and that FR-036's never-invent-a-denominator rule
  rides along with the port.
- **Recommendation**: One sentence in MV-9 recording the enum→boolean reduction and
  its reason, and one naming the handler as the statement's author with the FR-036
  rule restated.

#### [R2-MIN-011] The Worker's explicit `allow` is the sparse map's first at-ceiling entry — two doc comments will go stale
- **Affected section**: MV-8 (spec:262-263), impact table (spec:83)
- **Description**: The Worker seed map is documented — twice — as holding ONLY
  below-ceiling entries ("the same seed spells out an explicit 'deny' for each name
  it wants below that ceiling", `pkg/coreagent/tool_policy_catalog_drift.go:76-83`;
  the seed's own comment at `core.go:840-852`). MV-8's explicit `grep: allow` breaks
  that invariant deliberately (founder ruling). Harmless mechanically, but both
  comments become false the day it lands.
- **Recommendation**: Add "update the Worker sparse-map doc comments
  (`core.go`, `tool_policy_catalog_drift.go`)" to the impact table's policy row so
  the implementing PR carries it.

#### [R2-MIN-012] Traceability nits carried or introduced
- **Affected section**: BDD traces (spec:333, 339: "Traces to: MV-11"), matrix (spec:508-521)
- **Description**: (a) The cancel and busy scenarios trace to an MV, not a US/AS —
  same class as round-1's structural nit, now twice more. (b) The round-1 structural
  FAIL "unreadable-file scenario absent from the matrix" is still unfixed (test 11
  traces to "edge"; no matrix row). (c) FR-006/MV-4's matrix row was not updated to
  include test 14.
- **Recommendation**: Trace cancel/busy to US-2 AS-3-adjacent behavior or add an AS
  under US-2 for cancellation; add the unreadable-file and test-14 rows when
  inlining the full matrix (R2-MAJ-001).

---

### Observations

#### [R2-OBS-001] All four O1 dependencies are NEW to go.mod — verified absent today
`charlievieth/fastwalk`, `bmatcuk/doublestar/v4`, `sabhiram/go-gitignore`,
`grafana/regexp`: none appear in `go.mod` at `a9f8b38a5`. All are pure-Go,
MIT/BSD-licensed — compatible with Constraints #1/#2. FR-015's pin-and-justify
gate applies to all four in the implementing PR.

#### [R2-OBS-002] MV-9's additive-compat claim verified on the client side
The generated zod client uses non-strict `z.object` (`src/lib/api/generated/schemas.ts:4408`),
so unknown new fields pass old validation — MV-9's "old zod clients stay valid"
holds. Server-side, `VaultSearchResponse.yaml` is `additionalProperties: false`, so
the schema change + regen is mandatory before the handler emits a single new field
(Constraint #8 step order already covers this).

#### [R2-OBS-003] The global walk semaphore is a (tiny) cross-workspace side channel
429-busy is observable from any workspace while another workspace's walk runs.
Single-operator product — accept; noted for the STRIDE table.

#### [R2-OBS-004] SC-008's 5×/2× ratios are corpus-dependent
"Recorded, thresholds revisited with real numbers" is the right posture. Pin the
benchmark corpus (checked-in generator seed) in `tests/perf` so numbers are
comparable release-to-release; A7's go-re2 trigger depends on exactly that
comparability.

---

## Structural Integrity (plan-spec mode)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-5, all expanded |
| Every acceptance scenario has BDD scenarios | PASS | new AS-6/7/8s all covered by ★ scenarios |
| Every BDD scenario has `Traces to:` | PASS (nit) | cancel/busy trace to MV-11, not a US/AS (R2-MIN-012a) |
| Every BDD scenario has a TDD test | PASS | round-1's FAIL fixed (test 14); all ★ scenarios mapped |
| Every FR appears in traceability matrix | **CONDITIONAL** | delta rows present; the full matrix is unverifiable from the document (R2-MAJ-001) |
| Every BDD scenario in matrix | **FAIL (carried)** | unreadable-file scenario still absent (R2-MIN-012b) |
| Test datasets cover boundaries/edges/errors | PASS (gaps) | injected bounds fix MAJ-008; missing: regex column (R2-MIN-002), per-file-match row (R2-MAJ-005), non-ASCII-insensitive row (R2-MAJ-004), always-pruned-internals row (R2-MIN-006) |
| Regression impact addressed | PASS | assert-level port-diff checklist is a genuine strengthening |
| Success criteria measurable | PASS | SC-003 de-flaked (non-gating); SC-008 recorded; SC-009 needs the platform carve-out (R2-MIN-001) |

## Test Coverage Assessment

| Category | Gap | Finding |
|----------|-----|---------|
| Tool-path concurrency | No test for tool-vs-semaphore interaction or tool busy behavior | R2-MAJ-003 |
| 429 UI behavior | No vitest row renders the busy/limited state | R2-MAJ-003 |
| Smart-case fast path | Test 1's assert unimplementable as scoped | R2-MAJ-004 |
| Per-file match cap | No bound, no row, no test behind FR-019's promise | R2-MAJ-005 |
| Case modes | No DS-1 rows for sensitive/insensitive overrides | R2-MAJ-006 |
| Zero-alloc MUST | No gating alloc assertion | R2-MIN-008 |
| Clamp disclosure (files) | No assert that a clamped override is reported | R2-MIN-007 |
| Policy roster | Test 25 enumerates a hand-list that omits specialists | R2-MAJ-002 |

## STRIDE Delta (new material only)

| Component | Threat class | Status |
|-----------|-------------|--------|
| 2-walk semaphore | DoS | mitigated for REST; **tool-path scope unstated** (R2-MAJ-003) |
| 2-walk semaphore | Info disclosure | cross-workspace busy signal — accepted (R2-OBS-003) |
| `attachments[]` group | Info disclosure | none new — filenames already visible in the tree |
| `include_hidden` flag | Info disclosure | internal dirs scannable when true (R2-MIN-006) |
| Policy roster | Elevation | specialists' posture decided by omission (R2-MAJ-002) |
| Output caps (3 layers) | DoS (context) | mitigated; layering ambiguity only (R2-MIN-004) |

## Code-fact re-verification (claims NEW in the revision)

All verified against worktree `wt-integrate` @ `a9f8b38a5` on 2026-09-07.

| # | Claim | Verdict |
|---|-------|---------|
| 1 | `KindAttachment` at `pkg/records/knowledgefind/request.go:70` | **CONFIRMED** (const block :67-70; accepted in requests :181-188; mapped to `propindex.KindAttachment` :589-592) |
| 2 | `KnowledgeSearchHit.yaml` hit kinds include `attachment` | **CONFIRMED** (:40-42; excerpt_unavailable 5-reason enum incl. `attachment_not_read` :57-64) |
| 3 | Attachment query implementable through the find path | **CONFIRMED with caveats** — generated `VaultFindRequestKindAttachment` exists (`pkg/api/generated/openapi_types.gen.go:8038`), the shared text index indexes attachment names (`pkg/knowledge/index.go::indexAttachment`, "filename and path ONLY (FR-039a)"), and both endpoints ride the same index (`pkg/vaultprops/find_env.go:81,166` wraps the same `knowledge.OpenIndex`). Caveats: propindex platform gate (R2-MIN-001); note the RETIRED endpoint's engine is `pkg/knowledge`'s `knowledge_search` tool (`rest_knowledge.go:733-740`), not `knowledgefind` — parity (SC-009) is well-posed because the two share one text index with tokenized name matching |
| 4 | `LibrarySearchBar.tsx:81` disabled null-workspace state | **CONFIRMED** (props doc :81-83: "null = the Library virtual root — the bar renders disabled") |
| 5 | `shell.go::truncateOutput` `:589,1728`, 64,000 chars | **CONFIRMED** (:589 description "keeps up to 64,000"; applied :1728; definition :1859; constant `maxForegroundSuccessOutputLen = config.DefaultBuiltinSuccessCap // 64,000 chars` :1855. Note: FAILING commands keep only 10,000 — MV-12's flat 64,000 is a deliberate simplification worth one word) |
| 6 | `allowKnowledgeRetrieval` limiter + 429 contract exists | **CONFIRMED** (`rest_knowledge.go:743-753`, Retry-After always set; `knowledgeRESTLimiter`/`knowledgeToolLimiter` :93-94; 60/min sliding window per key, `pkg/knowledge/tools.go:210-215`) |
| 7 | `KnowledgePanel.tsx:198` searchFn typed off the component | **CONFIRMED** (:198 `React.ComponentProps<typeof KnowledgeSearch>['searchFn']`; mount :333) |
| 8 | `gateway.go:1261-1286` builder + `:1321` knowledge union | **CONFIRMED** (`buildKnownBuiltinToolNames` :1261; six-name union loop ~:1318-1325) |
| 9 | `knowledge_tools_wire.go:112` metadata registration | **CONFIRMED** (`registerKnowledgeBuiltinMetadata` :112) |
| 10 | Worker deny precedent `core.go:854` | **CONFIRMED as fact, wrong as framing** (rationale :846-852, denies :853-858) — see R2-MAJ-002(3) |
| 11 | `catalogSizeToday` = 101 | **CONFIRMED** (`catalog_count_test.go:114`) |
| 12 | Drift backfill writes explicit per-agent entries on upgrade | **CONFIRMED, value matters** (`tool_policy_catalog_drift.go:56-72`: seeded → seed's value; System Agents skipped/re-enforced; custom → **deny**) — see R2-MAJ-002(2) |
| 13 | Upgrade-pin precedent tests at `seed_upgrade_catalog_drift_test.go:225,289` | **CONFIRMED** (both test funcs at those lines) |
| 14 | e2e shards 65/65 today | **CONFIRMED** (65 specs across 15 shards in `tests/e2e/shards.json`) |
| 15 | `rest_library.go` case-ladder fits `case "files"` | **CONFIRMED** (case ladder at :91-145) |

## Unasked Questions

1. Does the 2-walk semaphore count agent grep walks — and what does an agent see when it's full? (R2-MAJ-003)
2. What posture do Planner/Explorer/Researcher get, and will test 25 iterate the real seeded roster rather than a hand-list? (R2-MAJ-002)
3. What value does the backfill write for a pre-existing CUSTOM agent — and is "available to ALL agents" scoped to the seeded roster? (R2-MAJ-002)
4. Is smart-case folding ASCII-only in the fast path? What happens to `café`? (R2-MAJ-004)
5. How does an agent search for `todo` but not `TODO`? (R2-MAJ-006)
6. Per-file match cap: bound, field, and test — or delete the promise? (R2-MAJ-005)
7. Which layer enforces the 1 MiB — and which reason wins when engine truncation and the 64k tool cap both fire? (R2-MIN-004)
8. Does `include_hidden:true` let an agent content-scan `.omnipus-vault/` index internals? (R2-MIN-006)
9. Where does an implementer read FR-001..FR-015 once this revision is the only spec in the tree? (R2-MAJ-001)
10. What does the Attachments group show on a propindex-less platform where the engine refuses the kind? (R2-MIN-001)

## Verdict Rationale

**REVISE.** Zero CRITICALs: the round-1 incident-class risks are genuinely closed,
and the revision's grounding held up under re-verification (15/15 new code-fact
claims confirmed, two with caveats the findings absorb). What remains is exactly the
kind of defect a final round exists to catch before build: a roster enumeration that
the seeding mechanism will contradict silently (R2-MAJ-002), a concurrency contract
with its most important scoping question unanswered (R2-MAJ-003), a flagship test
that cannot pass as written (R2-MAJ-004), two promise/contract mismatches
(R2-MAJ-005/006), and a spec file that is no longer self-contained (R2-MAJ-001).
All six are text-level fixes — no design rework, one optional founder touch-point
(confirming specialists are inside "ALL agents", which the ruling's own wording
already implies).

**Must not survive into build**: R2-MAJ-001 (inline the full spec), R2-MAJ-002
(roster + backfill values), R2-MAJ-003 (semaphore scope + busy contract),
R2-MAJ-004 (scope test 1 + define folding). R2-MAJ-005/006 need a one-line ruling
each, either direction. The MINORs are one-sentence fixes and should ride the same
edit.

### Recommended Next Actions

- [ ] R2-MAJ-001: inline all "as draft" content — ship one self-contained spec file
- [ ] R2-MAJ-002: name Planner/Explorer/Researcher (allow) everywhere the roster appears; state the custom-agent backfill value; reframe spec:70's precedent line
- [ ] R2-MAJ-003: rule semaphore scope (both surfaces), tool limiter, Retry-After on busy, SPA + agent busy behavior; extend tests 18/31
- [ ] R2-MAJ-004: define smart-case folding + fast-path scope; re-scope test 1; rule name-match case semantics
- [ ] R2-MAJ-005: per-file match cap — add the lever or strike the promise
- [ ] R2-MAJ-006: add the `case` mode field (or record its explicit deferral against the best-in-class ruling)
- [ ] R2-MIN-001..012 per finding recommendations (each ≤ 2 sentences of spec text)

```
Verdict: REVISE

Review written to: docs/internal/specs/unified-search-and-grep-spec-review-round2.md

To address these findings, run:
  /plan-spec --revise docs/internal/specs/unified-search-and-grep-spec.md docs/internal/specs/unified-search-and-grep-spec-review-round2.md
```

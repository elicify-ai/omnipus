# Adversarial Review: Skill activation and loading (ADR-072) — rev 3

**Spec reviewed**: `docs/internal/specs/skill-activation-and-loading-spec.md` (Draft, rev 3, verified
against `release/v0.1.1` @ `f101a9b4`)
**Review date**: 2026-09-01
**Verdict**: BLOCK

## Executive Summary

This spec is a genuine second draft — it closed all 14 findings from the rev-2 review with real
design changes (ADR-072 r4's D10.2, D6.1.1, D1.2), and every fact independently checked against the
live `f101a9b4` checkout in this pass (`skillAllowed`, `SecretEntriesAlways`'s 8 entries and its
coupling to `buildSecretGuardPatterns`, the 4 embedded builtin skills, `MaxSearchResults: 5`,
`ForcedSkills`, `tools.ResolvePath`) held up. But the rebuild has two new problems severe enough to
block: the traceability index built specifically to fix the *last* review's "stale completeness
claim" finding (CRIT-003) has fresh, mechanically-verifiable errors in its own final four rows, and
the interaction between the just-added uncapped menu (D1.1) and the count-bounded prompt cache (D8)
is never reconciled against CLAUDE.md's own 10MB RAM ceiling for security-feature overhead — a
ceiling the spec's own §6 table claims is satisfied. Both are concrete, both are cheap to fix, and
both undercut a self-verification claim the spec asks the reader to trust. **2 CRITICAL, 4 MAJOR, 3
MINOR, 2 OBSERVATION** findings.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 4 |
| MINOR | 3 |
| OBSERVATION | 2 |
| **Total** | **11** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The rebuilt traceability index has fresh, verifiable errors in its own last four rows

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: §12.1 "Scenario coverage index (exact titles)", rows 53–56; cross-checked
  against §12's FR traceability matrix and §9.3's test list.
- **Description**: §12.1 was built specifically to close the previous review's CRIT-003 ("traceability
  self-check stale... could not be checked even in principle"), and the spec claims it is "regenerated
  mechanically on 2026-09-01, not hand-edited... 0 defined-but-unmatrixed, 0 matrixed-but-undefined."
  It is not internally consistent. Comparing §12.1's Test(s) column against §12's own FR-matrix Test(s)
  column and §9.3's test names:
  - Row 53 ("Every skill call produces an audit record", FR-018) lists test **51**. §12's FR matrix
    says FR-018 → test **50** (`TestAudit_EverySkillCallRecorded`, whose name is a verbatim match for
    this scenario title). §9.3's test #51 is `TestAudit_HiddenDenialStillRecorded` — a different
    scenario entirely.
  - Row 54 ("A hidden denial is still audited", FR-019) lists tests **51c, 51d**. §12's FR matrix says
    FR-019 → test **51** (`TestAudit_HiddenDenialStillRecorded`). Tests 51c/51d are
    `TestAudit_WriteFileToSkillPathIsAudited` / `TestAudit_RecordNamesPerformingTool` — the CRIT-002
    write-audit tests, unrelated to "hidden denial."
  - Row 55 ("A write through any tool is audited", FR-071/071a) lists test **54**. §12's FR matrix says
    FR-071a → test **51d**. §9.3's test #54 is `TestSkillsScreen_LastInvokedSurfaced` — unrelated.
  - Row 56 ("A never-invoked granted skill is visibly unused", FR-020) has **no test listed at all**.
    §12's FR matrix says FR-020 → test **54** (`TestSkillsScreen_LastInvokedSurfaced`, whose name is a
    verbatim match for this scenario).

  The pattern is a one-row downward shift: row 53 got a value belonging near row 51/52, row 54 got
  row 55's value, row 55 got row 56's value, and row 56 was left empty. This is exactly the class of
  defect §12.1 exists to make impossible, still present in its newest four rows.
- **Impact**: SC-004 ("All 54 tests in §9.3 pass") and the FR/scenario completeness claim are both
  gated on this index being trustworthy. A reader or an implementer following §12.1 to find the test
  for "never-invoked visibly unused" finds nothing, and would have to independently re-derive the
  mapping from §12's FR matrix — the exact manual cross-checking §12.1 was introduced to eliminate.
  Because the error is confined to rows that didn't exist before this revision, it also means the
  "regenerated mechanically" claim was not actually re-run after the D1.2/D6.1.1/symlink additions, or
  the generation script itself has an off-by-one in how it appends new scenario rows.
- **Recommendation**: Fix the four cells directly — row 53 → `50`, row 54 → `51`, row 55 → `51c, 51d`,
  row 56 → `54` — and then actually re-run whatever produced this table (script or otherwise) end to
  end, diffing its output against §12's FR matrix and §9.3's test list before claiming "0 orphans"
  again. If the table is genuinely hand-assembled rather than scripted, say so plainly instead of
  asserting mechanical generation — the current wording is a specific, falsifiable claim that this
  review just falsified.

---

#### [CRIT-002] Uncapped menu × count-bounded (not byte-bounded) cache is never reconciled against the project's 10MB RAM ceiling

- **Lens**: Infeasibility / Inconsistency
- **Affected section**: FR-005 / D1.1 (menu has no count limit, no truncation), FR-046 / D8 (cache
  bounds "the per-agent cached variant **count**... with LRU eviction"), §6 "Resource limits:
  security-feature overhead <10MB (Constraint #3); the new cache is bounded (D8)."
- **Description**: D1.1 (§14.2, Q2) deliberately removed the only size limit the menu ever had, and
  the spec is explicit that this is unbounded: "the per-message menu is now proportional to how many
  skills the workspace offers, unbounded for a large mounted collection... **the mount's contribution
  is the one number nobody chose**." Dataset C row 13 and BDD "A mount contributing an implausible
  number of skills warns at creation" both treat a 5000-skill mount as a real, anticipated case — D1.2
  was added specifically because of it. Meanwhile D8's cache bound (FR-046) caps the **number of
  cached prompt variants** per agent, not their **size**. A single cached variant for a workspace with
  a 5000-skill mount is not a few kilobytes — at even 150 bytes/entry (slug + display name +
  description + location-free footer) that's ~750KB for one variant, before accounting for every other
  agent×workspace combination the LRU is still holding. §6's deployment table asserts Constraint #3
  ("security-feature RAM overhead <10MB") is satisfied by citing "the new cache is bounded (D8)" —
  but D8 bounds count, not bytes, so this is not actually a demonstration that the constraint holds;
  it's a citation of a different property.
- **Impact**: CLAUDE.md Constraint #3 is a **Hard Constraint**, and this spec is the only place that
  constraint gets checked before implementation starts. As written, an operator who mounts one large
  real-world monorepo with an unusually large `.claude/skills/` tree (exactly what Evaluation Scenario
  H2 invites — "Clone a public repository... mount it... no configuration was needed at any point")
  can push a single agent's cached prompt variants for that workspace well past the entire security
  feature's RAM budget, and D1.2's warning fires at mount-creation time on a **different** metric
  (skill count, not resulting cache footprint) so it doesn't actually warn about the thing that
  matters here.
- **Recommendation**: Either (a) add a byte-size cap to the cache alongside the count cap — evict by
  LRU **and** by aggregate byte budget, whichever is hit first — and state the byte budget as a new FR
  next to FR-046; or (b) explicitly accept the unbounded-per-variant risk with the same "stated rather
  than overlooked" treatment the spec already gives the Windows read gate (G7) and the enumeration
  oracle (MIN-003), including revising §6's Constraint #3 line so it no longer implies D8 alone
  satisfies it. Either is acceptable; leaving the current wording, which reads as a passed check, is
  not.

---

### MAJOR Findings

#### [MAJ-001] FR-046's cache bound ships with no default, contradicting the project's own convention and blocking SC-015

- **Lens**: Infeasibility
- **Affected section**: FR-046 ("bound the per-agent cached variant count and evict
  least-recently-used, with the bound exposed as an operator-tunable config value (default deferred to
  measurement)"); SC-015 ("Per-agent cached prompt variants never exceed the configured bound under a
  3× workspace-churn loop").
- **Description**: Every other operator-tunable value in this codebase is verified to ship with a
  concrete seeded default in `pkg/config/defaults.go` — the directly analogous precedent,
  `Tools.MCP.Discovery.MaxSearchResults`, is set to `5` there (`pkg/config/defaults.go:725`), not left
  for post-ship measurement, even though FR-008 explicitly inherits that exact value for the new
  `Skill` search mode. "Default deferred to measurement" is not a value; it's a decision not yet made.
- **Impact**: SC-015 cannot be run — "never exceeds the configured bound" is untestable without a
  bound — and the first build genuinely has no cap until someone picks one ad hoc during
  implementation, at which point it stops being a spec decision and becomes an unreviewed
  implementation detail, which is exactly the failure mode the spec's own "conservative type design"
  and "no default-policy fallback" framing (CLAUDE.md Constraint #6) argues against elsewhere.
- **Recommendation**: Pick a concrete conservative default now (a number in the same 5–50 range as
  `MaxSearchResults` or the D1.2 mount-skill-count threshold is defensible) and state it in FR-046 and
  `pkg/config/defaults.go`'s planned entry. Keep it operator-tunable — that part is fine — but ship a
  number.

---

#### [MAJ-002] The audit record's "mode" and "outcome" vocabulary is never enumerated in the spec, and two divergent audit-record shapes are never reconciled

- **Lens**: Ambiguity / Infeasibility
- **Affected section**: FR-018 ("carrying slug, mode, outcome, shelf, agent id and workspace id"),
  FR-071a ("shelf, resolved path, acting agent, workspace, and the tool that performed the write"),
  SC-006, SC-011, and every test/scenario that asserts "a permission-denied classification," "a
  distinct not-found classification," or "N distinct classifications observed."
- **Description**: Nowhere in this spec are the literal values of `mode` or `outcome` stated, even
  though the spec's own cited source has already settled them: ADR-072 lines 380–381 state `mode` is
  `load` / `search` and `outcome` is `loaded` / `denied` / `not_found`. The spec cites this ADR as
  authoritative throughout §1.1 and reproduces far more granular detail from it elsewhere (e.g. FR-029's
  exact sort algorithm), but drops this specific, already-decided enumeration. Separately, FR-018's
  call-audit shape (`slug, mode, outcome, shelf, agent, workspace`) and FR-071a's write-audit shape
  (`shelf, resolved path, agent, workspace, tool`) share three fields and diverge on the rest, with no
  statement of whether these are the same audit-record type with optional fields, two record types
  under one event kind, or genuinely separate `pkg/audit` entries — and no statement of whether a D9
  delegate-preload counts as `mode: load` for audit purposes, which is never tested either way.
- **Impact**: "N distinct classifications observed" (SC-011, SC-006, and the Dataset A/B/E "…
  classification" cells) is satisfiable by any three-way, five-way, etc. arbitrary strings as long as
  they differ from each other — a test can pass while asserting nothing about what the actual wire
  values are, which is not what "machine-verifiable constraint" is supposed to mean in §5.2. An
  implementer without independent access to ADR-072's exact line numbers has to invent this vocabulary
  from scratch, and there is a real risk of drifting from the existing `pkg/tools/result.go` wire
  vocabulary (`PermissionDeniedCode = "permission_denied"`, `DelegationDeniedCode =
  "delegation_denied"` already exist; no `not_found`-equivalent constant exists yet).
- **Recommendation**: Copy ADR-072's `mode`/`outcome` enumeration verbatim into FR-018 (or a new
  FR-018a) as a closed set, state explicitly whether a D9 preload audits as `mode: load`, and state
  whether FR-018 and FR-071a describe one audit-record type or two. While doing so, name (or mint) the
  actual string constants to reuse — `PermissionDeniedCode`/`DelegationDeniedCode` already exist for
  the denial cases; a `NotFoundCode` (or equivalent) needs to be introduced and named here rather than
  left to be invented ad hoc during implementation.

---

#### [MAJ-003] D4.2's "granted registry slug wins over a same-slug project skill" carve-out is unspecified when the registry grant is dangling

- **Lens**: Incompleteness
- **Affected section**: FR-028 ("resolve a registry slug the agent holds in preference to a project
  skill of the same slug"), US-4 AS-4, the "A project skill cannot shadow a granted registry slug"
  scenario, and the separately-stated edge case "Agent granted a slug that is no longer installed →
  menu omits it; a direct load returns not-found, distinct from refused."
- **Description**: The spec covers the dangling-grant case in isolation (grant references an
  uninstalled skill, nothing else competes for the slug) and covers the shadowing case in isolation
  (grant references an *installed* registry skill that collides with a project skill). It never covers
  the intersection: an agent's grant list names a registry slug that has since been **uninstalled**
  from the central library, and a mount in the agent's current workspace happens to carry a
  **project** skill with that same slug. Does the dangling grant still "count" as "a registry slug the
  agent holds" for D4.2's purposes (blocking the project skill from resolving, and now returning
  not-found instead of the working project skill), or does an uninstalled registry skill stop
  competing, letting the project skill resolve through? No FR, BDD scenario, or dataset row answers
  this, and it is a realistic production sequence — skill packages get removed from a shared library
  on a cadence that doesn't track individual agents' grant lists.
- **Impact**: Depending on which reading an implementer picks, an operator could either (a) lose access
  to a perfectly good, present, mount-granted project skill because a stale grant entry silently
  shadows it with nothing behind it, or (b) get inconsistent behavior between two otherwise-identical
  agents that differ only in whether their now-dangling grant list still technically names the slug.
  Either way it's the kind of silent-never-fires failure the ADR itself names as the single biggest
  accepted risk (§1.1's Failure modes row), and this is a gap in exactly that mechanism.
- **Recommendation**: Add one line to FR-028 stating the rule (recommendation: an uninstalled slug
  cannot compete — resolution should fall through to the project skill, since "the agent holds it" is
  meaningless for a skill that no longer exists to be held), one dataset row to Dataset A or C
  exercising it, and one BDD scenario tracing to it.

---

#### [MAJ-004] Mounted project skills auto-activate as unreviewed agent instructions purely by directory presence, with no STRIDE analysis and no content-trust disclosure

- **Lens**: Insecurity
- **Affected section**: D4.1 ("the mount is the grant" for project skills), US-4 AS-1 ("no per-slug
  configuration and no setting to discover"), Evaluation Scenario H2 ("Clone a public repository that
  genuinely carries `.claude/skills/`. Mount it... no configuration was needed at any point" — framed
  as the success condition), and §5's Explicit non-behaviors, which never discusses skill-content
  trust.
- **Description**: A registry skill requires an operator to individually grant a specific slug — a
  deliberate, per-skill allow-listing act, and the actual security control D4/US-2 exists to make real.
  A project skill requires none of that: by design, the instant a mount contains a recognised skills
  directory, every `SKILL.md` under it becomes loadable and menu-listed for every agent acting in that
  workspace, with zero individual review. `SKILL.md` content is not passive data — once loaded it
  becomes literal instructions injected into the agent's context (exactly the mechanism this whole spec
  exists to make more selective for registry skills). D1.2 already establishes the precedent that a
  mount can warrant an operator-facing disclosure at creation time — but its warning is scoped
  entirely to skill **count** ("stating the count and its per-turn consequence"), not skill **content
  or provenance**. Nothing in the spec surfaces to the operator that mounting a folder — including,
  per H2, an arbitrary cloned repository nobody at the org wrote — hands every collaborator on that
  repository (or a compromised dependency, or a malicious PR that slipped past review before the clone
  was made) a standing channel to inject instructions into every agent working in that workspace, with
  no per-skill grant step to catch it.
- **Impact**: This is a real elevation-of-privilege / instruction-injection vector that the spec's own
  Insecurity lens (§5) never names, in a spec that is otherwise unusually careful about naming accepted
  risks explicitly (Windows read-gate gap, enumeration oracle, `bash`-mediated write-audit gap are all
  stated plainly elsewhere). Silence here reads as an oversight, not a considered trade-off — which
  matters, because every other accepted risk in this spec earns that status by being written down.
- **Recommendation**: This does not require re-litigating D4.1 (mounting a folder already implies
  substantial trust in its contents via filesystem access). It requires two cheap additions: (1) a
  §5.1 line stating plainly that project-skill content is not reviewed or sandboxed relative to its
  instructions and is trusted at the same level as the mount itself, and (2) extending — or explicitly
  declining to extend, and saying why — the D1.2 mount-creation warning to also name *what a skills
  directory grants* (auto-loadable agent instructions, not just files) the first time a mount is found
  to contain one, distinct from the count threshold.

---

### MINOR Findings

#### [MIN-001] §2.5a's stated shelf precedence reads as contradicting D4.2 without saying it inverts

- **Lens**: Ambiguity
- **Affected section**: §2.5a: "Precedence for resolution is `project` → `global` → `builtin`, with
  D4.2's carve-out."
- **Description**: Read plainly, this sentence says project wins first in general. D4.2 (FR-028) says
  the opposite for the specific case of a slug collision the agent's own grant already covers: the
  granted registry (global/builtin) skill wins over the project skill, not the other way around. The
  word "carve-out" is doing a lot of work — a reader skimming §2.5a alone, without independently
  reaching FR-028, would reasonably conclude project always wins, which is precisely backwards for the
  one case D4.2 was written to nail down (US-4 AS-4's whole point).
- **Recommendation**: Restate explicitly: "Precedence for resolution is `project` → `global` →
  `builtin` **except** when the agent's own grant list already includes a same-slug global or builtin
  skill, in which case that registry skill wins over any project skill of the same slug (D4.2)."

---

#### [MIN-002] Three overlapping agent-facing skill-discovery tools now coexist with no in-spec disambiguation

- **Lens**: Overcomplexity / Ambiguity
- **Affected section**: FR-001 (new `Skill` tool: load-by-slug, search-by-query), FR-025 (existing
  `list_skills`, now grant-filtered and location-free), and `find_skills` (marketplace search,
  explicitly out of scope but left unchanged and still agent-reachable).
- **Description**: After this change, an agent has three separate tools whose names and purposes are
  easy to confuse at call time: `Skill` (search installed, granted skills by description), `list_skills`
  (enumerate installed, granted skills), and `find_skills` (marketplace search for skills not yet
  installed). The spec never states, anywhere an agent would see it (the per-request reminder, the
  `Skill` tool's own description, or a footer note), which of the three to reach for in which
  situation. This is exactly the kind of trigger-condition precision the spec insists on for skill
  *descriptions* themselves (D2/US-9) but doesn't apply to its own new tool relative to its neighbors.
- **Recommendation**: Add one disambiguating sentence to the `Skill` tool's description (or FR-001)
  distinguishing it from `list_skills` and `find_skills` in trigger terms, mirroring the discipline
  D2 already requires of skill authors.

---

#### [MIN-003] Denial/not-found "classification" strings aren't tied to the project's existing wire vocabulary

- **Lens**: Inconsistency
- **Affected section**: FR-021 ("a permission-denied classification, naming the skill"), FR-054 ("a
  distinct not-found classification"), §5.2's "structured failure carrying a permission-denied
  classification."
- **Description**: `pkg/tools/result.go` already defines `PermissionDeniedCode = "permission_denied"`
  and `DelegationDeniedCode = "delegation_denied"`, an established convention this feature's denial
  paths should plausibly reuse (FR-021's load-door denial and FR-053's delegation denial map naturally
  onto these). No equivalent `not_found`-style constant exists today, so FR-054 implicitly asks for a
  new one without naming it. This is a narrower instance of MAJ-002 but worth calling out separately
  because the fix is nearly free: the constants already half-exist.
- **Recommendation**: State in FR-021/FR-053 that the existing `PermissionDeniedCode` /
  `DelegationDeniedCode` constants are reused, and name the new not-found constant FR-054 requires
  (e.g. `SkillNotFoundCode = "skill_not_found"`).

---

### Observations

#### [OBS-001] FR-071's audit-append guarantee for project-skill writes could be stated locally instead of only inherited from §7's generic clause

- **Lens**: Inoperability
- **Affected section**: FR-071/FR-071a (audit every skill-path write regardless of tool); §7's
  Audit trail integration boundary ("On failure: logging failure must not fail the turn, but must
  itself be logged").
- **Suggestion**: Given how much weight FR-071 carries (it's the direct fix for the previous review's
  CRIT-002), a one-line restatement next to FR-071 itself — "the write and its audit record are not
  transactional; a write that succeeds with a subsequently-failed audit append is still logged as a
  logging failure per §7" — would make the guarantee locally verifiable without requiring the reader
  to already know §7 covers it.

---

#### [OBS-002] G9's concurrent-write gap should explicitly name the read-during-write case too

- **Lens**: Incompleteness
- **Affected section**: §15 Gap G9 ("Concurrent writes to one project skill through a shared mount...
  this spec does not specify it").
- **Suggestion**: As stated, G9 covers writer-vs-writer races on `SkillWriter`'s project path. It's
  silent on a reader-vs-writer race: an agent's `Skill` load reading a `SKILL.md` concurrently with
  another agent's authoring write to the same file could observe a torn/partial file, which is a
  distinct failure mode from "two writers stomp each other" (a malformed set of instructions gets
  loaded rather than a lost update). Worth folding into G9's scope explicitly rather than leaving it
  implied.

---

## Structural Integrity (plan-spec format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has ≥1 acceptance scenario | PASS | US-1..US-11 each carry 3–7 |
| Every acceptance scenario has ≥1 BDD scenario | PASS | Spot-checked across US-1, US-4, US-6, US-11 |
| Every BDD scenario has a `Traces to:` back-reference | PASS | All 56 scenarios in §8 carry one |
| Every BDD scenario has a corresponding test in the TDD plan | **FAIL** | See CRIT-001 — §12.1's own index misstates 4 of 56 mappings |
| Every FR appears in the traceability matrix | PASS (as claimed) | §12's row-for-row FR list was spot-checked against §10 and is internally consistent; not independently re-diffed in full |
| Every BDD scenario appears in the traceability matrix | **FAIL** | Same as above — present, but with wrong test references in 4 rows |
| Test datasets cover boundary conditions, edge cases, error scenarios | PASS | Datasets A–F are unusually thorough, including near-miss and Unicode rows |
| Regression impact is explicitly addressed | PASS | §9.5 names 8 existing behaviours with old/new coverage and a 7-row regression dataset |
| Success criteria are measurable with no subjective language | PARTIAL | SC-001–017 are numeric and falsifiable, but SC-015 is not runnable without MAJ-001's missing default, and SC-006/SC-011's "classification" language is unverifiable without MAJ-002's missing enumeration |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Cache memory footprint | No test asserts a byte/size bound on cached menu variants, only a count bound (FR-046) | "Cache stays bounded across many workspaces" (CRIT-002) |
| Audit vocabulary conformance | No test asserts audit `mode`/`outcome` values against a named closed set, because none is defined in-spec | "Every skill call produces an audit record", "A hidden denial is still audited" (MAJ-002) |
| Dangling-grant × project-skill collision | No test exercises an uninstalled registry grant colliding with an installed project skill of the same slug | "A project skill cannot shadow a granted registry slug" (MAJ-003) |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| Dataset A (Grant list values) | Grant slug that is both installed-then-uninstalled AND colliding with a project skill | Add a row combining #8 (dangling reference) with a same-slug project skill present, asserting the MAJ-003 resolution |
| Dataset C (Mount layouts) | A `SKILL.md` written concurrently with a `Skill` load of the same path | Add a row for OBS-002's torn-read case, even if the expected result is "unspecified — locking TBD" |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Skill tool (load/search) | ok | ok | ok | ok (MIN-003's enumeration trade-off is stated) | ok | ok | Grant enforcement at all five doors is well covered |
| Project-skill discovery (mounts) | ok | ok | ok | ok | ok | **risk** | MAJ-004 — content auto-activates as instructions with no individual review or disclosure, unlike registry skills |
| Read gate (`pkg/tools`, sandbox) | ok | ok | ok | ok | ok | ok | Windows gap is explicitly stated and tested (G7/T5c-windows), not silently accepted |
| Audit trail (skill calls + writes) | ok | ok | **risk** | ok | ok | ok | MAJ-002 — undefined `mode`/`outcome` vocabulary means "distinct classification" tests can pass without asserting real values; `bash`-mediated writes explicitly and correctly left unaudited (FR-071b) rather than silently gapped |
| Prompt cache (D8) | ok | ok | ok | ok | **risk** | ok | CRIT-002 — unbounded per-variant size under a count-only bound is a resource-exhaustion path, self-inflicted by a large mount rather than adversarial, but unaddressed either way |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Does an uninstalled registry grant still "count" as held for D4.2's shadowing carve-out, or does
   resolution fall through to the project skill? (MAJ-003)
2. Is a D9 delegate-preload audited with `mode: load`, or a third mode value nowhere else defined?
   (MAJ-002)
3. Are FR-018's call-audit records and FR-071a's write-audit records the same `pkg/audit` entry type
   with optional fields, or two distinct types? (MAJ-002)
4. What actual byte or count ceiling keeps one giant mount's cached menu variant from being the whole
   RAM budget by itself — and if none is planned, is that an accepted trade-off worth stating as
   plainly as the Windows gap is? (CRIT-002)
5. What number does FR-046's cache-bound default actually ship with on day one? (MAJ-001)
6. Should the operator ever be told, at the moment a mount's skills first become loadable, that this
   grants standing instruction-injection surface — separately from D1.2's per-turn-cost warning?
   (MAJ-004)

---

## Verdict Rationale

Two CRITICAL findings block this spec. CRIT-001 is the more damning of the two precisely because of
what it sits inside: §12.1 exists *only* because the previous review round found the spec's
completeness claim unverifiable, and the fix — "regenerate mechanically, diff both directions, 0
orphans" — is directly falsified by re-checking its own last four rows against the very tables it
claims to have been diffed against. That doesn't just mean four cells are wrong; it means the
regeneration process this spec now leans on for its central credibility claim either wasn't actually
re-run after the newest FRs (D1.2, D6.1.1, the symlink findings) were added, or has a bug that
produces exactly this kind of shift. Either way, the reader can no longer take "0 orphans, mechanically
verified" at face value anywhere else in the document either. CRIT-002 is a distinct, structural gap:
D1.1 (uncap the menu) and D8 (bound the cache by count) were each reasoned through carefully in
isolation, but their product — an unbounded-size item held in a count-bounded cache — was never run
against CLAUDE.md's own Hard Constraint #3, which the spec's §6 table asserts is satisfied without
demonstrating it.

The four MAJOR findings are all cheap, concrete fixes that don't require new design work: pin a
default (MAJ-001), copy three already-decided enum values out of the ADR (MAJ-002), add one sentence
plus one dataset row for the dangling-grant intersection (MAJ-003), and extend an existing disclosure
mechanism or explicitly decline to (MAJ-004). None of the four require re-litigating an ADR decision.

### Recommended Next Actions

- [ ] Fix §12.1 rows 53–56's Test(s) values and re-verify the whole table end to end (CRIT-001)
- [ ] Reconcile D1.1's uncapped menu against D8's count-only cache bound and CLAUDE.md Constraint #3,
      either with a byte-size cap or an explicit accepted-risk statement (CRIT-002)
- [ ] Pick and state a concrete default for FR-046's cache bound (MAJ-001)
- [ ] Copy ADR-072's `mode`/`outcome` enumeration into FR-018, state the D9-preload mode and the
      relationship between FR-018 and FR-071a's audit shapes (MAJ-002)
- [ ] Add the dangling-registry-grant × installed-project-skill collision case to FR-028, Dataset A,
      and §8 (MAJ-003)
- [ ] Add a §5.1 statement on project-skill content trust and decide whether to extend the D1.2 mount
      warning to cover it (MAJ-004)
- [ ] Address the three MINOR findings (shelf-precedence phrasing, tool disambiguation, wire-vocabulary
      reuse) at the author's discretion — none block implementation on their own

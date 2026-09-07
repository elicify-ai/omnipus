# Vault records — adversarial review, round 3 (final)

- **Reviewed:** `docs/internal/specs/vault-records-spec-2026-08-25.md` (draft revision 2) together with
  `docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` (claimed revision 3)
- **Date:** 2026-08-25
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`, branch `feat/library-improvements`
- **Mode:** plan-spec (FR/SC identifiers, traceability matrix, Given/When/Then scenarios, test plan)
- **Round-2 verdict:** BLOCK — 8 critical, 21 major

---

## Executive summary

Of the eight round-2 critical fixes, **three are real and complete** (the `author.go` partial-reuse
correction, the four unfailable tests, the removal of the per-property-field design). **Five were
applied to one document or one line and not to the others that assert the same thing**, leaving the
two documents contradicting each other and, in three cases, contradicting themselves. The descope of
D11/D12 was executed by a broken text substitution that left two literal `%s — %s` headings in the
ADR and did not touch any of the seven places that still promise, sequence, cost or test the
descoped features.

The new one-field `Props` design carries a defect that both previous framings hid: **the rebuild
mechanism it depends on does not exist in the code, and the nearest mechanism that does exist would
silently drop the field rather than rebuild the index.** A second, independent defect follows from
the same change: with `Props` stored-but-not-indexed and `indexDoc` otherwise unchanged, nothing in
the index can narrow candidates by record type or by any property, so the 10,000-record candidate
cap is breached by every query against a corpus of the stated supported size.

**Findings: 10 CRITICAL, 17 MAJOR, 6 MINOR, 3 OBSERVATION. Verdict: BLOCK.**

---

## Part A — Round-2 fix verification

| # | Round-2 fix claimed | Real? | Evidence |
|---|---|---|---|
| 1 | D16 uses ONE stored JSON field, with an index-format version bump forcing rebuild | **Partly — the field is right, the rebuild is fiction** | C-3, C-4 |
| 2 | IDs use a never-lowered high-water mark instead of reconcile-to-max | **No — the old mechanism is still cited in three places and the new one contradicts its own AC** | C-7 |
| 3 | D11 sub-records and D12 temporal facts descoped | **No — headings corrupted; seven live references remain** | C-1, C-2 |
| 4 | Nine tools, not eight | **Partly — corrected in D15 only; D18 and the seed table still carry eight** | C-6 |
| 5 | `author.go` reuse stated as partial, list-splice new work, guard against scalar-over-multiline | **Yes — and grounded in the code** | verified below |
| 6 | Four unfailable tests replaced | **Yes** | verified below |
| 7 | Bounds restated against the candidate cap; RSS inside ADR-067's budget | **No — applied to one of three rows and one of two documents; SC-008 still says 96 MB** | C-5 |
| 8 | (round-2 item, carried) scope resolved before unknown-property rejection | **Yes — FR-024** | — |

**Fix 5 is sound and I confirmed it against the tree.** `SetProperty` (`pkg/knowledge/author.go:766`)
takes `(key, value string)`; `authorValidatePropertyKey` (`:1015`) rejects a key containing a colon or
line break; and the function's own doc comment states the hazard FR-040b guards, verbatim: *"any
continuation lines belonging to it (an indented block, a block sequence) are removed, because they
were part of the old value."* FR-040b is therefore a real, code-grounded requirement, not a
defensive nicety.

**Fix 6 is sound.** The replacement tests name the mechanism that would fail
(`TestIndex_StaleFormatIsRebuiltNotOpened`, `TestKnowledgeSearch_ScoringUnchangedByPropsField`,
`TestIndex_PropsFieldRoundTripsExactDecimal`, `TestTools_RecordToolsRegisteredAndDotFree`), and each
carries an explicit note on why the weaker form could not fail. That is the right shape. Two of the
four are nonetheless unreachable as specified — see C-3 and M-5.

---

## Part B — Findings

### CRITICAL

**C-1 — The D11/D12 descope was executed by a broken template and left two unnamed headings.**
*Lens: Inconsistency. ADR §2, lines 342 and 350.*

Both headings read literally:

```
### %s — %s — **DEFERRED, not in scope**
```

The format placeholders were never substituted. The consequence is not cosmetic: the two decisions
no longer contain the strings "D11" or "D12" anywhere, so every surviving cross-reference to them
(ADR §4.2, ADR D20 W6, spec §10 A-4/A-5) points at a heading that cannot be found by searching for
its identifier. A reader following "D11" from §4.2 lands nowhere.

**Fix:** restore the headings as `### D11 — Sub-records — **DEFERRED, not in scope**` and
`### D12 — Temporal facts — **DEFERRED, not in scope**`, then grep both documents for `D11` and
`D12` and reconcile every hit (see C-2).

---

**C-2 — The descoped features are still delivered, sequenced, claimed as a gain, costed, and tested.**
*Lens: Inconsistency. ADR D20 line 583, ADR §4.1 line 670, ADR §4.2 lines 685–686, ADR D14/AC-14.1
line 414; spec §2 edge-case table, spec §7 DS-3, spec §10 A-4/A-5.*

Seven live references survive the descope:

1. **ADR D20, W6** — *"Delivers: Sub-records (D11), temporal facts (D12), saved views (D10)"*, with
   the exit criterion *"'who did we know at Acme in 2023' is answerable"*. **The wave's only exit
   criterion is the descoped feature.** W6 as written cannot be exited.
2. **ADR §4.1 (Gained)** — *"Time-bounded facts are queryable, which no surveyed file-based system
   offers."* A descoped feature is listed as a benefit of the decision.
3. **ADR §4.2 (Cost)** — *"Sub-records (D11) and temporal facts (D12) are both departures…"*
4. **ADR D14, AC-14.1** — the byte-preservation fixture corpus is required to include *"nested
   sub-records"*.
5. **Spec §2 edge cases** — three rows contract behaviour: *"Sub-record list containing one malformed
   entry → that entry reported; the rest of the record remains valid"*, *"Temporal property queried
   with no `as of` → the currently-valid value"*, *"Temporal property with overlapping validity
   ranges → reported as a validation error"*. These are exactly the "promise with nothing behind it"
   the descope note says it removed.
6. **Spec §7 DS-3** — the write corpus must contain *"nested sub-records"*.
7. **Spec §10 A-4/A-5** — still say the likely assumption is *"in scope"*, still say *"D11 specifies
   them; no FR covers them in this draft — **a real gap**"*, and the closing paragraph still asserts
   *"A-4 and A-5 are specification defects"*. **After a descope these are not defects; they are the
   decision.** A reader of §10 will conclude the opposite of what the ADR now says.

**Fix:** delete W6's D11/D12 line and replace its exit criterion with the saved-views one; delete
§4.1's temporal claim; rewrite §4.2's bullet to name only what remains in scope; remove "nested
sub-records" from AC-14.1 and DS-3; delete the three edge-case rows; replace §10 A-4/A-5 with a
single line recording that D11/D12 are deferred and no FR is expected.

---

**C-3 — FR-020a's rebuild mechanism does not exist, and the mechanism that does exist would silently
drop the `Props` field instead of rebuilding.**
*Lens: Infeasibility / Incorrectness. Spec FR-020a, §0 table, §7 regression; ADR D16, AC-16.1.*

Both documents require *"the index format version MUST be bumped; an index written by an earlier
version MUST be rebuilt, never opened and queried for a field it does not contain."* I checked the
tree. **There is no index format version.** `grep -n "Version" pkg/knowledge/index.go` returns
nothing. The only version in the package is `manifestVersion = 1` in `pkg/knowledge/manifest.go:48`
— a file the spec's §0 reuse table does not list at all.

Bumping `manifestVersion` does **not** do what FR-020a needs. The path is:

- `LoadManifest` (`manifest.go:113`) on a version mismatch returns `NewManifest(root)` plus an error.
- `Index.Sync` (`index.go:751-757`) treats that error as non-fatal: *"an unusable manifest costs a
  full rebuild, never a wrong answer"*, logs a WARN, and re-indexes every file.
- But it re-indexes **into the already-open bleve index**. `openOrCreateBleve` (`index.go:446-455`)
  calls `bleve.NewUsing(path, buildIndexMapping(), …)` **only when the directory does not exist**;
  for an existing directory it calls `bleve.OpenUsing`, which never re-applies the mapping. The
  bleve directory is removed and recreated in exactly one circumstance: `openOrCreateBleve` returning
  an **error** (`index.go:417-434`, "index open failed; removing and rebuilding"). A version mismatch
  is not an open error.

So on every upgraded install, `Props` is written into a document whose mapping has
`doc.Dynamic = false`, `m.IndexDynamic = false`, `m.StoreDynamic = false` (`index.go:574-578`). An
undeclared field against a closed mapping is **silently ignored** — no error, no warning. Every
record query then reads a document with no properties, finds nothing to exclude, and reports
`complete: true` with an empty `problems` array.

**That is the exact failure this ADR exists to prevent, reintroduced by its own upgrade path, and it
is the defect the previous two framings hid.** The per-property-field design made the mapping
problem unmissable. Collapsing to one field made it look like an ordinary struct-field addition, and
the mapping question stopped being asked.

`TestIndex_StaleFormatIsRebuiltNotOpened` cannot be written against a version that does not exist, so
the seam test the spec calls *"the seam that matters"* is currently unimplementable.

**Fix:** specify the real mechanism. An on-disk format stamp (its own file under the index dir, or a
new field on the manifest read **before** `openOrCreateBleve`) whose mismatch causes
`os.RemoveAll(ix.blevePath)` **and** removal of the manifest, before the bleve open — i.e. the same
two removals `index.go:420-430` already performs on corruption. Add an FR for it, cite
`manifest.go::manifestVersion` and `index.go::openOrCreateBleve` by symbol, and add a second seam
test asserting that a `Props` value written against a stale mapping is **detected**, not silently
absent — the current test asserts a rebuild happened, not that the field is readable afterwards.

---

**C-4 — Nothing in the index can narrow candidates, so the 10,000-record cap refuses every query at
the stated supported corpus size.**
*Lens: Infeasibility. Spec FR-020, FR-021, FR-064, SC-007, SC-001; ADR D16, D15.1b.*

`Props` is *"stored-but-not-analysed"* and *"does not participate in scoring"* (ADR D16). The rest of
`indexDoc` is unchanged: `Path`, `Name`, `Kind`, `Offset`, `Body` (`index.go:583-589`). I checked
what `Kind` holds — it is only ever `ScanKindNote` or `ScanKindAttachment` (`index.go:876, 954, 995,
1157`). **There is no indexed field that identifies a record type, and no indexed field carrying any
record property.**

Neither document says how the candidate set is produced. FR-021 says evaluation happens *"in Go over
the retrieved candidate set"* and FR-064 caps that set at 10,000 — but retrieval has no filter to
apply. The only available candidate set for a structured query with no free-text term is **every
document in the index**.

The arithmetic is fatal. §3 states the supported bound as 50,000 records, and bleve documents here
are *segments* of notes (`segmentDocID`, `index.go:595`), so the document count is strictly higher
than the record count. Every `record_query` therefore materialises >50,000 candidates, breaches the
10,000 cap, and is **refused** under FR-064 with a narrowing instruction — for a filter the caller
already narrowed as far as the schema permits. SC-001 ("the two-hop question is answered by one
`record_query` call") and SC-007 (p95 for filter + group + count at 50,000 records) are both
unreachable, and FR-066 then forbids returning any aggregate at all.

This is not a performance concern that measurement will settle. It is a structural consequence of
storing every property in one unindexed field.

**Fix:** decide and specify candidate selection explicitly. The minimum viable answer is one **indexed**
keyword field carrying the record type (a sixth `indexDoc` field, e.g. `RecordType`, indexed
not-analysed), so a type-scoped query retrieves only that type's documents; then restate the
candidate cap as a per-type bound and re-derive whether 10,000 is the right number against a realistic
type distribution. If the answer is that Go-side evaluation cannot meet the bound without more
indexed fields, that is the §3.6 fallback trigger and must be said now, not discovered at W2.

---

**C-5 — The bounds fix was applied to one of three rows and one of two documents; the spec now
contradicts itself on RSS.**
*Lens: Inconsistency. Spec §3 constraints table vs spec SC-008; spec §3 vs ADR D20 performance table.*

| Claim | Spec §3 | Spec §5 | ADR D20 |
|---|---|---|---|
| Peak RSS during aggregation | **< 48 MB** | **< 96 MB** (SC-008) | **< 96 MB** |
| p95, filter only | < 150 ms **at a 10,000-record candidate set** | — | < 150 ms **at 50,000 records** |
| p95, filter + 2-level group + count | < 400 ms **at 50,000 records** | at 50,000 records (SC-007) | < 400 ms at 50,000 records |
| p95, one relation hop | < 600 ms **at 50,000 records** | — | < 600 ms at 50,000 records |

Three separate defects here:

1. **SC-008 was not updated.** The success criterion the implementation will be measured against
   still permits 96 MB — twice the §3 bound, and 1.5× ADR-067's entire 64 MB steady-state budget,
   which is the precise problem round 2 said was fixed. Whichever number a subagent reads first wins.
2. **ADR D20 was not updated at all** — neither the RSS row nor the filter-only row. The spec claims
   to implement ADR revision 3; on this table the ADR still states revision 1's numbers, and ADR D20
   line 597 hangs a decision on the stale one: *"Exceeding the RSS target moves the index decision to
   §3.6"*. Under the 48 MB bound that trigger fires 48 MB earlier than the ADR says.
3. **The correction's own reasoning was not carried to rows 2 and 3.** The spec's note explains why
   "at 50,000 records" measured nothing — *"the cap bounds work regardless of corpus size"*. That
   argument applies identically to group+count and to the relation hop. Worse, per C-4 those two rows
   describe queries that FR-064 **refuses**, so they are not merely mis-scoped, they are unmeasurable.

**Fix:** pick one RSS number (48 MB, per the stated reconciliation with ADR-067), write it in all
three places, restate rows 2 and 3 against the candidate cap as row 1 now is, and update ADR D20 and
its §3.6 trigger sentence to match.

---

**C-6 — The nine-tool fix reached D15 only; `record_view_import` has no seed policy and therefore
ships silently denied.**
*Lens: Inconsistency / Insecurity. ADR D15 line 418 vs D18 AC-18.1 line 545 and the seed table lines
558-561; spec FR-070, FR-080, FR-081, SC-009.*

D15 says *"Nine tools"* and even annotates the correction: *"(Revision 2 said 'eight' while O-1's
resolution added `record_view_import` — corrected.)"* D18 was not corrected. AC-18.1 still reads
*"a test enumerates the **eight** record tools"*, and the seed posture table lists exactly eight:

- row 1: `record_schema`, `record_query`, `record_explain`, `record_validate`
- row 2: `record_write`, `record_relate`, `record_log`, `record_view_write`

`record_view_import` appears in neither row. D18's own analysis then tells you the consequence:
`repairAndValidateToolPolicyCoverage` backfills the gap to `deny` and logs one WARN, so **the
importer ships dead on every install with a log line as the only signal** — the exact failure D18
was written to prevent, committed in D18's own table.

It also breaks the spec's gates: FR-080 requires an explicit entry for every record tool for every
seeded agent, and FR-081/SC-009 assert **zero repaired pairs** on a fresh install. With the seed as
written, ten seeded agents × one missing tool = ten repaired pairs, so
`TestToolPolicy_ZeroRepairedPairsOnFreshInstall` fails on day one. (I verified the ten-agent roster:
`mia`, `jim`, `ava`, `ray`, `worker`, `planner`, `explorer`, `researcher`, `judge`, `plansupervisor`
all exist in `pkg/coreagent/core.go`.)

**Fix:** change AC-18.1 to "nine", add a `record_view_import` row to the seed table with an argued
posture (it writes a view file, so it belongs with the write group: `ask` for Mia and Ray, `allow`
for Jim and Ava, explicit `deny` elsewhere), and add an FR requiring the seed table and FR-070's list
to be asserted against each other by a test so this cannot drift again.

---

**C-7 — The high-water-mark fix contradicts its own acceptance criterion, and the removed
reconcile-to-max is still cited as the Windows mitigation.**
*Lens: Inconsistency / Incorrectness. ADR D7 line 258, D7.1 lines 281–289, AC-7.1; spec FR-038,
US-5.4, SC-005, §2 edge table last row.*

Four mutually incompatible statements:

- **ADR D7:** *"the sequence is monotonic per type and **never reused**, so ID count is not record
  count"* — i.e. gaps are expected and are the whole point.
- **ADR D7.1:** *"If the file is lost, the reconcile floor is the max over existing records **plus a
  recorded gap allowance**"* — deliberately introduces a gap of unspecified size. "Gap allowance" is
  never defined: no value, no derivation, no bound.
- **ADR AC-7.1 and spec SC-005:** *"1,000 distinct IDs and **zero gaps** in the sequence."*

A design whose stated property is "never reused" cannot be accepted by a criterion demanding zero
gaps; under contention with a persisted counter, a process that reserves and dies leaves a gap by
construction. The acceptance criterion as written will fail a correct implementation and pass an
incorrect one that reuses identifiers.

Second, the fix left the recovery path pointing back at the mechanism it removed. FR-038's own
rationale says reconciling *to* the maximum *"guarantees reuse after the highest record is deleted,
which makes an existing relation resolve to a different record."* Then **US-5.4** specifies exactly
that as the recovery behaviour: *"Given the sequence file is deleted, When the vault is opened, Then
allocation resumes above the highest existing identifier."* The never-lowered guarantee is only as
durable as `.seq`; the spec's own scenario defines the loss path as the banned behaviour.

Third, **ADR D7.1 line 281** says of the Windows collision case: *"the mitigation is the reconcile
below"* — the reconcile no longer exists, and a high-water mark does not heal a duplicate ID
regardless. The spec's edge table repeats it: *"Windows, two processes allocating IDs → collision
possible (flock is a no-op); **healed by reconcile**, reported by validation."* Two dangling
references to a removed mechanism, both stating a mitigation that is not real. (The flock no-op is
correctly inherited from ADR-054 §5 — that part is accurate.)

**Fix:** (a) delete "zero gaps" from AC-7.1 and SC-005; assert *zero duplicates and monotonic
allocation* instead. (b) Define "gap allowance" with a number and a rationale, or delete it and
specify that `.seq` loss flags the vault and requires operator acknowledgement before further
allocation. (c) Reconcile US-5.4 with FR-038 — say explicitly that resuming above the max is a
degraded recovery that may reuse identifiers of deleted records, and that the vault is flagged.
(d) Replace both "healed by reconcile" claims with the truth: on Windows a collision is **detected by
`record_validate` and not healed**.

---

**C-8 — The named highest-risk deliverable has no oracle. `TestComparisonTruthTable` cannot be
written from this specification.**
*Lens: Infeasibility. Spec §8, §7 row 6; ADR §4.2.*

§8 makes the truth table a first-class deliverable and states its oracle rule: *"expected result
derived from **this document**, never from running the implementation."* The document does not
contain the semantics.

The table must enumerate *"left type × right type across all seven property types plus absent and
wrong-typed"* × eight operators — 81 type pairs × 8 operators ≈ 648 cells. The specification defines
the expected value of, at most, three families of them:

- absent-handling (FR-007, FR-008, D3.2);
- enum ordering by declared position (FR-010, D4);
- cross-currency refusal (FR-014).

Everything else is undefined. What does `text < text` return — lexical order, or an error? Is
`money > number` a type error, a refusal, or a comparison of the bare amount? Does `date = text`
coerce? Does `contains` apply to a scalar, to `person`, to `money`? Is `enum < text` false or a
rejection? An engineer writing the table has no source for any of these and will do the one thing §8
forbids: derive them from the comparator being written.

This matters more than an ordinary coverage gap, because §8 also states the change-control rule —
*"one that requires editing the table is a specification change"*. A table derived from the
implementation makes that rule vacuous and locks in whatever the first comparator happened to do,
which is precisely how `3 > 2` became `false` in the research incident §8 cites.

**Fix:** add a section to the spec defining comparison semantics as a 9×9 disposition matrix at the
*type-pair* level — for each pair, one of {ordered comparison, equality only, `contains` only,
rejected as a type error} — plus the rule for absent on each side and the rule for a value that does
not parse as its declared type. 81 cells of prose-free table. The truth table is then mechanically
derivable from it, and a comparator change genuinely does require editing a specification.

---

**C-9 — `Scope.Truncated()` is reused but no requirement consumes it, so scope truncation produces
silent incompleteness reported as `complete: true`.**
*Lens: Incompleteness / Incorrectness. Spec §0 table, FR-060, FR-062, FR-025; ADR D15.1a.*

The spec's §0 reuse table explicitly names `.Truncated` among the `pkg/knowledge/scope.go` surfaces
being reused. I verified it: `Scope.Truncated()` (`scope.go:117-120`) reports *"that enumeration hit
`ScopeMaxDirs`, so Collections may be"* incomplete.

No FR consumes it. FR-060 requires resolution through scope; FR-061/FR-062 require an out-of-scope
record to yield an empty result indistinguishable from an empty vault. Compose the two: when scope
enumeration truncates, some of the caller's **own** vaults are silently absent, the records in them
are silently absent, and FR-025's completeness verdict — which knows nothing about scope enumeration
— reports `complete: true` with an empty `problems` array.

This is the central behavioural contract of the document failing through a primitive the document
lists as reused. It is also indistinguishable at the tool surface from C-3's failure mode, which
makes it hard to diagnose in the field.

**Fix:** add an FR: *a truncated scope enumeration MUST set `complete: false` and add a problem
naming scope truncation and the remedy.* Note the tension with FR-062 and resolve it explicitly —
reporting "your scope enumeration truncated" leaks nothing about other workspaces, so the two are
compatible, but that argument must be written down or an implementer will suppress the problem to
satisfy FR-062. Add a test to §7.

---

**C-10 — A P0 security requirement depends on an explicitly open question (O-6).**
*Lens: Insecurity / Incompleteness. ADR O-6; spec FR-024, FR-062, §10 A-2.*

FR-024 requires that *"Scope MUST be resolved before this rejection, so the valid-names list never
reveals schemas outside the caller's workspace — otherwise the error channel defeats FR-062."* That
requirement presupposes schemas are workspace-scoped. ADR **O-6 is open**: *"Does a record type's
schema apply vault-wide, or per workspace?"* — and the spec's §10 A-2 records the likely
implementation assumption as **vault-wide**.

If schemas are vault-wide (the assumption), then one vault mounted into two workspaces exposes the
same schema set to both, and FR-024's guarantee is either meaningless or unimplementable — there is
nothing to filter. FR-062's indistinguishability then holds for records but not for schemas: an agent
in workspace A can enumerate the property names of a `company` type it can retrieve zero records
from, which is a real information-disclosure channel about another workspace's data model.

An open question is acceptable in an ADR. An open question that a P0 security requirement resolves in
one direction while the implementation assumption resolves it in the other is a blocker.

**Fix:** resolve O-6 before W1. If vault-wide, weaken or delete FR-024's scope clause and state
plainly that schema names are not workspace-confidential. If per-workspace, specify how a schema file
inside a shared vault is attributed to a workspace, and add a negative test alongside
`TestScope_CrossWorkspaceReturnsEmpty`.

---

### MAJOR

**M-1 — Money has two incompatible representations across the two documents.** *Lens: Inconsistency.*
ADR **O-2** resolves money as *"amount (**integer minor units**) + ISO-4217 currency + declared
scale"*. ADR **D16** and spec **FR-020b** say *"Money is a **decimal string** inside `Props`"*, and
spec FR-012 says *"amount, ISO-4217 currency and declared scale"*. Integer minor units + scale and a
decimal string are different wire shapes with different round-trip and comparison semantics, and
AC-16.2's *"a value requiring more precision than float64 provides survives unchanged"* is trivially
true for one and a real constraint for the other. Pick one, state it in FR-012, FR-020b and O-2, and
make `TestIndex_PropsFieldRoundTripsExactDecimal` assert that exact shape.

**M-2 — The identity field is named two different things, and one of them violates D8.** *Lens:
Inconsistency.* D8 requires system fields to carry an `omni_` prefix and names `omni_id` as the
example. D7 then shows `id: CO-0142` in frontmatter, D5.1 says the index resolves a wikilink by
*"reading its `id`"*, and the spec never mentions `omni_` at all. `id` is also a field operators and
other plugins commonly use, so an unprefixed `id` is exactly the collision D8 exists to avoid. Decide,
and make D7's example match D8's rule.

**M-3 — Six functional requirements are absent from the traceability matrix, including three of the
round-2 fixes.** *Lens: Structural / Inconsistency.* §6 covers FR-001..003, 004/009, 006, 007/008,
010/011, 012..014, 020/021, 022..024, 025/026, 027..029, 030..035, 036..039, 040..042, 043/044, 045,
046, 050..053, 060..062, 063..066, 070..073, 080..082, 090/091, 100..102. Orphaned: **FR-005**
(unrecognised type is not an error — a stated P0 behaviour), **FR-015** (schema change triggers
revalidation), **FR-020a** (the index version bump), **FR-020b** (no float in the index path),
**FR-040a** (list splice), **FR-040b** (multi-line guard). The last three are the requirements round 2
added; adding a requirement without a matrix row is how it reaches implementation untested.

**M-4 — Four tests named in the traceability matrix are absent from the test plan.** *Lens:
Structural.* §6 names `TestSchema_TypesAreScopedToRecordType`, `TestIndex_PropsFieldRoundTripsExactDecimal`,
`TestRelate_ReplaceMustBeNamed`, `TestDerived_NeverWrittenToFrontmatter`. §7's numbered plan (rows
1–21) contains none of them. A test in the matrix and not in the plan has no level, no ordering and
no owner.

**M-5 — Test-name mismatch, and FR-072/FR-073 are traced to a test that covers neither.** *Lens:
Inconsistency / Incompleteness.* §6 row: `FR-070..073 → TestTools_NamesHaveNoDots`. §7 row 18:
`TestTools_RecordToolsRegisteredAndDotFree | FR-070, 071`. Two names for one test, and FR-072 (compact
textual schema, not JSON) and FR-073 (`record_explain`) have **no test anywhere**. FR-072 carries a
concrete, falsifiable claim (Notion's ~91% token reduction) and deserves an assertion.

**M-6 — `record_explain` is specified to do something it cannot do.** *Lens: Infeasibility.* FR-073:
*"MUST report what a query would return and which properties it could not evaluate, **without
evaluating it**."* With all filtering, grouping and aggregation evaluated in Go over decoded `Props`
(FR-021), "what a query would return" is knowable only by evaluating it. What `record_explain` can
honestly report is static: the properties named, whether each exists and type-checks, the estimated
candidate count, and whether the bounds would be breached. Rewrite FR-073 to that, or the
implementation will either run the query (making the tool a duplicate of `record_query` at full cost)
or fabricate an estimate.

**M-7 — `record_validate` is an unbounded resource sink with no bound of its own.** *Lens: Insecurity
(DoS) / Incompleteness.* §3 budgets it at *"< 30 s at 50,000 records"*. It is by definition exempt
from the 10,000-record candidate cap (FR-064), no RSS bound covers it (the 48 MB figure is scoped to
*"aggregation at the candidate cap"*), and its only protection is ADR-067's shared
`knowledgeRESTLimiter`. An agent that calls it in a loop burns 30 s of CPU and an unbounded working
set per call, on the same process that serves chat token streaming. Add an explicit peak-RSS bound
for a full validation pass, specify whether it streams or materialises, and either give it its own
tighter rate limit or make it cancellable and single-flight per vault.

**M-8 — `record_view_import`'s verbatim reporting is an unbounded echo of file content with no path
confinement stated.** *Lens: Insecurity (information disclosure).* FR-101: *"An expression it cannot
translate MUST be reported verbatim."* The tool takes a caller-supplied path. FR-060 says every record
tool resolves through workspace scope, but nothing says the *import source path* is confined to the
vault, that the file must actually be a `.base` file, or that verbatim output is size-bounded. Point
it at a file whose contents parse as no expression and "reported verbatim" becomes a read primitive
that returns file content through the tool result. Add: the source path MUST resolve inside the
caller's scoped vault roots; the file MUST be validated as a `.base` document before any content is
echoed; verbatim output MUST be truncated at a stated byte cap with the truncation reported.

**M-9 — The index rebuild every install must perform is specified nowhere as an operational event.**
*Lens: Inoperability.* FR-020a/AC-16.1 force a full rebuild of every existing ADR-067 index on
upgrade. Nothing states: whether `knowledge_search` is available during the rebuild, how long it takes
for a vault at the supported bound, whether the gateway blocks boot on it, whether progress is
surfaced (the code has a `progressCoalescer`, so it can be), or what happens if the rebuild is
interrupted. §1's own note — *"an impact analysis on `pkg/knowledge/index.go` before W2 would be
prudent"* — is recorded and then not acted on. This is the single largest blast-radius item in the
spec and it has no operability requirement, no SC and no runbook line.

**M-10 — There is no rollback story for the index format change.** *Lens: Inoperability.* Once an
index is rebuilt at the new version, downgrading the binary is not specified. Given the mechanism in
C-3 does not exist yet, whatever replaces it should state the downgrade behaviour explicitly
(old binary sees a newer stamp → rebuild, or refuse and log). Silence here means a downgrade reads a
new-format index with old code, which is the mirror of the failure AC-16.1 guards.

**M-11 — The supported-corpus figure is undefined pending a measurement, and the reason given for
that is wrong.** *Lens: Ambiguity / Incorrectness.* §3: *"Supported records per vault: 50,000 records.
**Note:** the index counts segments, not records, so this is an unknown larger document count; the
segment ratio MUST be measured at W2 and recorded here."* A headline capacity bound that resolves to
"unknown, measure later" cannot be tested and cannot be refused against — and every performance row
below it is stated "at 50,000 records". The framing is also imprecise: bleve documents here are
*note segments* (`segmentDocID`, `index.go:595`), a deterministic function of note size and the
segmenter, not an unknown. State the assumed notes-per-record and segments-per-note, derive the
document count, and put a falsifiable number in the table.

**M-12 — Record identifiers are unique only within a type, but relations resolve to a bare
identifier.** *Lens: Ambiguity / Incorrectness.* FR-036: *"unique within its type"*. FR-031: *"The
index MUST resolve a relation to the target's record identifier."* Nothing says the join key is
type-qualified. With per-type sequences and per-type uniqueness, `0142` is ambiguous and even the
prefixed form is only unique if prefixes are themselves globally unique — which D2 leaves to the
schema author (`identity.prefix: CO`), with no requirement that two schemas cannot both choose `CO`.
Add: prefixes MUST be unique across schemas in a vault (rejected at load, both paths named, mirroring
FR-003), and the join key is the full prefixed identifier.

**M-13 — `person` is a distinct property type with no behaviour distinguishing it from a relation.**
*Lens: Overcomplexity.* D3 defines `person` as *"a relation to a person record, distinct from a name
typed as text"* — that is `{type: relation, to: person}`. No FR gives `person` any behaviour a
relation does not have. Its cost is not zero: it is one of the seven types in the closed set FR-004
enumerates, so it multiplies into the C-8 truth table (two rows and two columns, ~30 cells) and into
every validation and comparison branch. Remove it and declare person-ness with `to: person`, or state
the behaviour that justifies it.

**M-14 — "Dated note" is undefined, and the undated case has no scenario.** *Lens: Ambiguity /
Incompleteness.* FR-050: *"A mention of a record in a **dated note** MUST be treated as an
interaction."* Frontmatter `date`? A daily-note filename? File mtime? Three implementers, three
answers, and mtime in particular would make a `git clone` rewrite everyone's interaction history.
US-6 also has no scenario for a mention in a note with **no** date, which is the common case in a
real vault. Define the date source in priority order and add a scenario for the undated note.

**M-15 — The shape of a refusal is unspecified, and it collides with FR-025.** *Lens: Incompleteness.*
FR-025 makes the completeness verdict and problem list **required fields of every query response**
(FR-091, D19). FR-064/FR-065 refuse. A refusal is presumably an error, not a response — but D19 lists
no error schema among the nine contract types, and nothing says whether a refusal carries `problems`.
An implementer will reasonably return HTTP 400 with a bare message, and the narrowing instruction
FR-064 promises has nowhere to live. Add the error type to D19 and state which fields a refusal
carries.

**M-16 — FR-015 (schema change → revalidation) has no test, no bound and no mechanism.** *Lens:
Incompleteness.* The requirement correctly identifies that schemas live where the scanner does not
walk, so no mtime or manifest entry exists — and then stops. Nothing says how the change is detected
(fsnotify? poll? on-tool-call stat?), what "invalidate affected records" costs at the supported bound,
or whether a query racing a schema edit sees old or new rules. No matrix row (M-3), no test.

**M-17 — The ADR's own status line says revision 2 while the spec implements revision 3, and §6
disclaims performance targets the ADR now states.** *Lens: Inconsistency.* ADR line 3:
*"**revision 2**, after adversarial review"*. Spec line 3: *"Implements ADR-068 **revision 3**"*.
Separately, ADR §6 still says *"It does not claim performance targets… and **that is a gap**"* while
D20 carries a full performance table and the spec carries SC-007/SC-008. A reviewer cannot tell which
statement is current.

---

### MINOR

**m-1 — §3.6 appears before §3.5 in the ADR's alternatives** (lines 638 and 652).

**m-2 — The spec's §0 reuse table attributes the version bump to `index.go`**, but the only version
mechanism lives in `manifest.go`, which the table omits entirely. Add the row (192 lines) so the file
being changed is visible.

**m-3 — SC-002's fixture (63 records, 22 malformed) is named nowhere else.** DS-1 does not build it
and §7 does not reference it. Either name the fixture or derive the numbers from DS-1.

**m-4 — Nothing asserts that FR-070's list and ADR D15's table agree.** Given C-6 is exactly that
drift, add the assertion.

**m-5 — §1 records "GitNexus was not consulted" as a gap and leaves it open** for the one change with
a cross-package blast radius (`pkg/knowledge/index.go`). Run `impact({target: "buildIndexMapping"})`
and `impact({target: "openOrCreateBleve"})` before W2 and record the result.

**m-6 — Holdout scenario 6 asserts behaviour no FR provides.** *"Run two agents writing different
properties of the same record concurrently; confirm both land and neither file is damaged."* FR-043's
version token guarantees the second write is **refused**, not that it lands. Either the holdout is
wrong or a retry/merge requirement is missing.

---

### OBSERVATIONS

**O-A — Nine tools before any of them has a user.** `record_explain` (M-6), `record_view_import`
(M-8) and `record_log` (whose note-explosion risk the spec itself flags as A-3) are the three with the
least evidence behind them and the most surface. W1–W4 need five tools. Consider shipping five and
letting the remaining four earn their place.

**O-B — The two-level grouping cap is asserted from one anecdote** (*"a published CRM design specified
group by company, then jurisdiction"*). It is a reasonable default; it does not need the justification
it is given, and if a third level is cheap in Go the cap is arbitrary.

**O-C — `record_view_write` and the `.omnipus-vault/views/` file format are specified in D10 but have
no FR, no contract type in D19's list, and no test.** Saved views are the W6 deliverable that
*survives* the D11/D12 descope, so they are now W6's entire content — and they are the least specified
thing in the document.

---

## Part C — Structural integrity results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has at least one acceptance scenario | **PASS** — US-1..US-8 all carry numbered scenarios |
| Every acceptance scenario has a BDD scenario | **PASS** — scenarios are Given/When/Then in prose |
| Every BDD scenario has a `Traces to:` back-reference | **FAIL** — scenarios carry no back-references; the matrix maps FR→scenario in one direction only |
| Every BDD scenario has a corresponding test | **FAIL** — US-5.4 (sequence-file loss), US-7.3 (property absent from schema), US-2.1 (all-valid corpus reports complete) have no test |
| Every functional requirement appears in the matrix | **FAIL** — six orphans (M-3) |
| Every test in the matrix appears in the plan | **FAIL** — four orphans (M-4) |
| Test datasets cover boundaries, edges, errors | **PARTIAL** — DS-1/DS-2 are strong; DS-3 still requires a descoped construct (C-2); no dataset covers comparison semantics (C-8) |
| Regression impact explicitly addressed | **PARTIAL** — three named seam tests, one of which is unimplementable as specified (C-3); no coverage of rebuild operability (M-9) |
| Success criteria measurable, no subjective language | **FAIL** — SC-008 contradicts §3 (C-5); SC-007 measures a query FR-064 refuses (C-4); SC-005 asserts "zero gaps" against a design that produces them (C-7) |

---

## Part D — Test coverage assessment

- **Unfailable tests: none found in this revision.** Round 2's four were genuinely replaced, and each
  replacement carries a note explaining what the weaker form could not catch. This is the strongest
  part of the document.
- **Unimplementable tests: two.** `TestIndex_StaleFormatIsRebuiltNotOpened` (no version exists to be
  stale — C-3) and `TestComparisonTruthTable` (no oracle — C-8). Both are the *named* highest-risk
  guards in their sections.
- **Missing negative tests:** no test for FR-005 (unrecognised `type` is not an error), FR-011's
  case-exactness at the *write* path (DS-1 covers validation only), or FR-046 at the point of writing.
- **Missing concurrency tests:** `TestID_ConcurrentAllocationIsCollisionFree` covers ID allocation.
  Nothing covers two concurrent `record_write` calls to the same file, which holdout 6 assumes works
  (m-6), or a query racing a schema edit (M-16).
- **Missing idempotency tests:** `record_relate add` of an existing relation, `record_log` of the same
  interaction twice — both plausible agent retries, neither specified.
- **Level assignment looks right.** One e2e test (`TestRecords_PerfAtFiftyThousand`) for the two
  performance SCs, everything else unit or integration. No E2E gold-plating.

---

## Part E — STRIDE summary

| Component | Threat | Status |
|---|---|---|
| `record_query` | **Information disclosure** — valid-names error channel leaking schemas across workspaces | FR-024 addresses records; unresolved for schemas (**C-10**) |
| `record_query` | **Information disclosure** — scope truncation silently narrowing results while reporting complete | **C-9**, unaddressed |
| `record_validate` | **DoS** — 30 s CPU + unbounded working set per call, cap-exempt, shared limiter | **M-7**, unaddressed |
| `record_view_import` | **Information disclosure** — verbatim echo of a caller-named file, no path confinement or size cap | **M-8**, unaddressed |
| `record_write` / `record_relate` | **Tampering** — stale-token refusal, byte-preserving splice | FR-041/FR-043 adequate; multi-line guard FR-040b is code-grounded and correct |
| `record_write` / `record_relate` | **Repudiation** — audit on mutating tools and on stale-token refusal | FR-044 adequate. **Gap:** no audit for a denied or out-of-scope *read*, so probing is invisible |
| All record tools | **Elevation of privilege** — per-agent tool policy | FR-080/FR-081 sound in intent, defeated by the seed gap (**C-6**) |
| ID allocator | **Tampering** — duplicate ID silently merging two records | POSIX sound; Windows knowingly unprotected with a **false** mitigation claim (**C-7**) |
| Index | **Spoofing / integrity** — a stale-mapping index returning empty results as authoritative | **C-3**, unaddressed |

---

## Part F — Unasked questions

1. **How is the candidate set produced?** Neither document says. Everything in §3's bounds table
   depends on the answer (C-4).
2. **What happens to a record when its schema is deleted?** FR-015 covers change, not removal. Do its
   records revert to ordinary notes (FR-005), or become validation errors?
3. **Can a note declare two types?** `type:` is shown as a scalar throughout, but D3.1 says a scalar
   silently becomes a list the moment a second value is added — the failure this ADR is built around.
   What does the system do when `type` itself becomes a list?
4. **What is the migration path for a vault that already uses `type:` for something else?** Real vaults
   use `type: meeting` as a plain tag. On first schema load those notes become records and start
   failing validation.
5. **Is `record_query` reachable from the REST/SPA surface, or agent-tools only?** D19 defines nine wire
   types, implying REST; no requirement covers auth, rate limiting or CSRF for those endpoints
   distinct from the tool path.
6. **What does a `.seq` write cost per record creation?** Every `record_write` that mints an ID takes a
   flock, an fsync and a rewrite. At bulk-import rates this is the dominant cost and nothing bounds it.
7. **Who owns O-5 (deleted vs relocated relation targets) and by when?** It is the one open question
   the ADR itself calls *"the most important unanswered question in that product"*, and it is still
   assigned to "Architect" with no date.

---

## Verdict

**BLOCK.**

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/specs/vault-records-spec-2026-08-25-review-round3.md`

Both documents must be revised together — five of the ten critical findings exist only because a fix
was applied to one document and not the other. To address them:

```
/plan-spec --revise docs/internal/specs/vault-records-spec-2026-08-25.md docs/internal/specs/vault-records-spec-2026-08-25-review-round3.md
```

Three items should be settled **before** that revision, because the rest of the document depends on
their answers: **C-4** (how candidates are selected), **C-3** (the real rebuild mechanism), and
**C-10/O-6** (schema scope). The first two determine whether the D16 Go-side design survives at all
or whether §3.6's SQLite fallback is triggered now rather than at W2.

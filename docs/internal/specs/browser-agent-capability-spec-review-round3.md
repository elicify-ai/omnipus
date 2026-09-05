# Grill — round 3: `browser-agent-capability-spec.md` (revision 3)

- **Spec under review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-agent-capability-spec.md` (1515 lines, revision 3, landed at `dfa9dd4e9`)
- **Source ADR:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` (read at HEAD `028abcfcb`)
- **Sibling spec:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec.md` (D1, read at HEAD)
- **Prior rounds:** `browser-agent-capability-spec-review.md` (30 findings), `browser-agent-capability-spec-review-round2.md` (26 findings)
- **Mode:** `plan-spec`
- **Focus:** drift against ADR sections and the sibling D1 spec that landed *after* revision 3, plus verification that the round-2 fixes held.

---

## Executive summary

Revision 3's round-2 fixes held — FR-007's counting seam, FR-029's packaging, FR-028's named
surface, the underscore event names and the lease tombstones are all genuinely in place, and
the §11/§12 A-16 position on `browser_upload_file` matches ADR D2.9's erratum exactly, with S-25
and S-48 now consistent. The problem is everything that moved underneath it: **three ADR sections
(D1.1b, D1.1c, D1.1d) landed after this revision and none is absorbed**, the sibling D1 spec
**replaced the lease-membership rule this document restates**, and the ADR and D1 are now in
**direct opposition** on whether the browser pool evicts or refuses — a contradiction D2 sits
inside and covers on neither side. Separately, the operator-mandated audit mitigation (FR-028)
rests on a viewer the spec's own §12 B-6 documents as already broken on a typical install.

**19 findings: 3 CRITICAL · 11 MAJOR · 7 MINOR · 3 OBSERVATION.**

**Verdict: BLOCK.**

---

## Findings

| ID | Sev | Lens | Section | Finding | Fix |
|---|---|---|---|---|---|
| C1 | CRITICAL | Inconsistency | §6, FR-033, edit site 15 | ADR D1.1c rules **LRU eviction with no error surface**; D1 at HEAD rules **refusal, never eviction**, with an explicit close action and a Settings remedy. D2 covers neither outcome. | Route the ADR↔D1 contradiction to the ADR owner before Stream C lands; add an FR for "browser unavailable for a pool reason"; extend edit site 15. |
| C2 | CRITICAL | Incorrectness | FR-028, §2.3, §6.1, §12 B-6 | The operator-mandated audit mitigation's named surface is documented by this spec as already blank on any install with a paired channel (#667, OPEN). | Make #667 a stated prerequisite of FR-028 the way #659 is of FR-029, or name a working primary surface. |
| C3 | CRITICAL | Incompleteness | FR-028(a), §6 D1.2 bullet, §2.3 | The chat-render half of the mitigation is unverified for **delegated sub-turns** — the population the D1.2 supersession just widened the risk to. | Verify and state whether a child sub-turn's tool calls render in the parent thread; if not, the mitigation does not cover the D1.2 case and needs a different one. |
| M1 | MAJOR | Inconsistency | §3 interface contract, Stream F | D2 restates D1 §14.2 rule 3 as *"mutates page or tab state … enforced against the REGISTRY"* — the rule D1 **explicitly discarded** as self-contradictory and replaced with *"lease **iff** `controlledResult`-gated"*. | Replace the parenthetical with the biconditional and cite D1 §14.2 rule 3's table. |
| M2 | MAJOR | Incompleteness | §5, §9, §10 | D1's new rule makes D2's `controlledResult` wiring on its four action tools **load-bearing for D1's test 18**, and D2 has no FR, scenario or test asserting it. | Number the obligation, give it a structural scenario mirroring S-60, record it in §6 and §15 item 2. |
| M3 | MAJOR | Inconsistency | §15 item 2, §0a row 7 | Defects (a) "the set is five, being fixed uncommitted" and (b) "D1's MAJ-008 still reads three" are **fixed at HEAD**. Only (c), the `D2 FR-018` citation, survives. | Reduce item 2 to (c); delete the in-flight status note. |
| M4 | MAJOR | Inconsistency | §12 A-16, §15 item 3, edit site 21 | The ADR D2.9 erratum this spec asks for **landed in the same commit as this revision**. A-16's disposition should be RULED, not DECIDED-overrulable, and §15 item 3 asks the operator to rule on a settled question. | Re-verify against the ADR at HEAD; relabel and close §15 item 3. |
| M5 | MAJOR | Incompleteness | §0a, §0b row 13, §6 | D1.1b, D1.1c and D1.1d all post-date revision 3 and none is absorbed; §0b claims only D1.1a and D1.2 were. FR-035's provenance is now an operator ruling (D1.1d), not a grill finding. | Add a revision-4 absorption row; rework §6's D1.1a bullet to cover D1.1a–D1.1d. |
| M6 | MAJOR | Incorrectness | FR-016, §12 A-8, §13 holdout 5 | Under D1.1c a workspace's browser can be evicted **between two tool calls in one turn**; every D2 verb assumes the tab persists, and the snapshot `index` handle then resolves against a reloaded page — plausibly and wrongly. | State the assumption; require a turn-scoped pin from D1, or add a named "browsing context replaced" error. |
| M7 | MAJOR | Incompleteness | edit site 15, §2.1 | Whole-Chrome teardown (idle close, FR-046 explicit close, crash containment, eviction) is a teardown site for D2's own `sessionEntry` dialog state and is absent from the "exhaustive" list. | Add it, or obtain a written D1 guarantee that `sessionEntry` dies with the Chrome. |
| M8 | MAJOR | Incompleteness | US-7, S-22, FR-011 | `browser_hover`'s only use case — act on the revealed item — is neither specified nor tested. Whether `:hover` survives the next tool call is an unpinned dependency behaviour. | Add an AC + scenario for hover→click on a pure-CSS `:hover` fixture; pin the behaviour the way order 0c pins `page.Enable`. |
| M9 | MAJOR | Inconsistency | §14.1 C4 row | Still asserts `_AskWithNoApprover_Terminates` *"fails while #659 is open, by design"* and calls 0a/0b "gates" — the design revision 3 replaced. | Rewrite the cell to match order 0b, S-27 and §14.2 C3. |
| M10 | MAJOR | Insecurity | §2.3, §4 | The accepted-risk enumeration omits the only destination outside the operator's machine: every snapshot ships form values **to the model provider's API**. This voids ADR D2.9's "every other browser tool moves data inward" asymmetry. | Add the destination to §2.3 and §4; note the asymmetry argument no longer holds for a values-emitting snapshot. |
| M11 | MAJOR | Insecurity | §6, FR-019, FR-030 | ADR §6's open question — whether an agent-minted `/preview/<agent>/<token>/` should be reachable from a tab another agent drives — is **created** by FR-019 + FR-030 and appears nowhere in D2. | Add it to §6's D1-boundary list and §12; the token is the credential (ADR-044 FR-023). |
| m1 | MINOR | Inconsistency | §3, Stream F, §12 B-3 | Three dangling `§15 item 5` cross-references; the intended targets are item 2 and item 6(b). | Repoint. |
| m2 | MINOR | Incompleteness | §12 B-6, §2.1 | #667 is never cited by number, though the commit landing revision 3 names it and the spec demands issue numbers elsewhere. | Cite #667. |
| m3 | MINOR | Inconsistency | §2.2, FR-007 | §2.2 lists `dom.GetBoxModel` as the stability **fallback** while FR-007/S-15 assert **zero** `getBoxModel` in the gate; the fallback's trigger and cost are unspecified. | Delete the row, or scope FR-007 to the fast path in §9 as §3 already does. |
| m4 | MINOR | Ambiguity | FR-010, §3 | "plus modifiers" — no set, no syntax, no scenario. | Enumerate the modifier set and separator; add a positive scenario. |
| m5 | MINOR | Ambiguity | FR-009, §3, S-18 | Multi-select is specified for **labels** only; `{value: [...]}` legality and partial-`value` behaviour undefined. | Specify or forbid; add a dataset row either way. |
| m6 | MINOR | Inoperability | §6 (iii) | Fourteen per-workspace profile directories on a disk-constrained worker, with no creation/cleanup requirement. | State the disk cost and the cleanup obligation. |
| m7 | MINOR | Infeasibility | §12 A-13 / A-18 | The FR-034 removal decision reads FR-032 counters that A-18 concedes are **zero in `visible_only`** — blank on precisely the install that flipped the switch. | Give the removal issue a rule that works when the switch is on, or require the measurement window at `full`. |
| O1 | OBSERVATION | Insecurity | §6, §11(b) | D1.1b's `--renderer-process-limit` weakens site isolation past the cap and the ADR says it is "not acceptable if agents are pointed at arbitrary hostile URLs"; §11(b)(i) argues Ray/Explorer/Researcher "browse arbitrary public sites". | One line in §6 recording the tension. |
| O2 | OBSERVATION | Incompleteness | §6 D1.2 bullet | ADR D1.2 names `browser_upload_file`'s `ask` as "the one place this is still gated"; FR-029 means it does not exist in v1. | State it so nobody reads D1.2 as gated. |
| O3 | OBSERVATION | — | §9 | Counts, tombstones and the FR↔US↔BDD↔test mapping all check out row by row. The structural discipline is not in question. | — |

---

## The findings in full

### C1 (CRITICAL) — the ADR and the D1 spec now rule *opposite* pool behaviour, and D2 covers neither

**ADR §D1.1c, "The cap manages itself — no error, no button, no UI" (operator ruling, 2026-08-31):**

> **Policy.** When a workspace needs a browser and the cap is reached, evict the **least recently
> used** instance and start the new one. … there is no "pool full" error surface and no UI change.
> An earlier draft proposed a REST path and a close button; both are withdrawn.

**The D1 spec at HEAD says the exact reverse**, in five places:

| D1 site | Text |
|---|---|
| §1 in-scope, line 135 | "a cap, **a refusal (not an eviction)** at the cap" |
| §3.1, line 330 | "The pool **NEVER evicts a live browser** to make room — a silent eviction logs someone out mid-task" |
| §5 non-behaviors, line 633 | "The system must **not** evict a live workspace browser to satisfy a new request. Refuse at the cap (FR-039)." |
| FR-039, line 1097 | "At the cap the pool **refuses** … it never evicts a live browser" |
| §3.1 line 350-353, FR-046, US-15/AC4 | The refusal message names "**the close action**" and tells the operator to raise `max_browsers` "**in Settings**" — the REST path and button D1.1c withdrew. |

The two documents even contradict each other's *reasoning*. D1's stated reason for refusing is
"a silent eviction logs someone out mid-task". D1.1c's stated reason for evicting is "**closing a
browser is not destructive** — the logins live in the profile directory on disk, not in the
process. A closed browser reopens signed in." D1's premise was true under CDP contexts (ADR D1.0a:
*Survives reload: no*) and is **false under D1.1a's profile directories**, which is precisely why
the operator ruled the other way after D1's spec was written.

**Why this is D2's problem and not only D1's.** D2 must know which of two behaviours a browser tool
call can produce:

- Under **refusal**, a browser tool can now fail for a reason that is not "no Chromium" — and
  FR-033/US-16 is D2's *only* requirement about an unavailable browser. D2's error taxonomy
  (`ErrNotActionable`'s closed four, the dialog wording rules, FR-033) has no slot for it, and the
  spec's own governing principle is that a failure must name its cause.
- Under **eviction**, D2 acquires a new teardown site for its own state (M7) and a new
  between-calls hazard (M6), neither of which appears anywhere in this document.

§6's "D1 boundary — four items this spec does not decide" lists the write lease, the
browsing-context audit event, the team-editing disclosure and `CaptureSharedContext`. **The pool is
not one of them**, and it is the item with the largest observable effect on D2's tools.

**Recommended fix.** (i) Raise the ADR↔D1 contradiction to the ADR's owner as a blocking cross-spec
item in §15 — this is not D2's to resolve, but it is D2's to refuse to build over. (ii) Add a
requirement alongside FR-033: *any* browser-unavailable outcome (no Chromium, pool refusal, launch
failure, evicted-and-relaunch-failed) returns an error naming the cause and the remedy, with a
scenario per cause. (iii) Extend edit site 15 per M7.

---

### C2 (CRITICAL) — FR-028's mandated audit surface is, by this spec's own evidence, blank on a typical install

ADR D2.11 makes two mitigations **non-optional**. FR-028 delivers the second as *"a metadata-only
`browser_snapshot` audit event … read at **Settings → Security → Audit Log**"*, and §2.3 calls it
"the **durable** half — the record that survives and that an on-call operator greps". §6.1 gives it
its own row. §4's observable contract promises it. Holdout 11 is built on it.

§12 B-6, in the same document, establishes that this viewer does not work:

> `AuditLogResponse.entries` is `z.array(AuditEntry)`; `src/lib/api.ts::performRequest` **throws
> `ApiSchemaError`** on a `safeParse` failure rather than dropping the offending row. **So one
> dotted entry blanks the entire Audit Log viewer.** Two shipped emitters already write dotted
> names into the same `audit.jsonl`: `audit.EventChannelPairing = "channel.pairing"` … and
> `audit.EventCliValidate = "cli.validate"` … **so on any install where a channel has been paired
> or a CLI validated, Settings → Security → Audit Log is already broken.**

Omnipus ships ~14 channels and its headline use is chat over them. "A channel has been paired" is
not an edge case; it is the product. So on a typical install, the surface FR-028 rests on renders
nothing at all — **including D2's own correctly-named `browser_snapshot` records.**

The spec treats this as a scoping question ("D2's obligation is narrow and absolute: use
underscore-form names … File it; do not fold it in") and never asks the consequential question:
*does the mitigation the operator ruled non-optional actually work?* It does not. Using an
underscore name means D2 does not make the viewer worse; it does not make D2's records readable.

Confirmed against the tracker: **#667 is OPEN**, titled *"Audit Log screen blanks entirely when
history contains channel.pairing or cli.validate (dotted event names violate AuditEntry pattern)"*.
It was filed by the very commit that landed this revision (`dfa9dd4e9`, subject: *"…audit-log
defect filed as #667"*) and the spec never cites the number (see m2).

**Recommended fix.** Treat #667 exactly as FR-029 treats #659: a **stated prerequisite** of FR-028,
because without it the requirement is satisfied in code and unobservable in fact. If the operator
prefers not to gate on it, then FR-028 must name a surface that works today — the raw
`$OMNIPUS_HOME/system/audit.jsonl` path is greppable and durable — as the primary surface, with the
viewer as a secondary convenience, and the runbook in §6.1 must say so. What it must not do is
present a broken surface as the durable half of a mandated security control; that is the shape
`docs/internal/false-green-patterns.md` exists for, applied to a control rather than a test.

---

### C3 (CRITICAL) — the chat-render half is unverified for delegated sub-turns, which D1.2 just made the risk's main population

FR-028(a) is the mitigation that shows *what* was captured:

> The full snapshot result renders inline, **by default, for every viewer of the conversation** —
> true today because `src/lib/toolVisibility.ts` contains zero `browser` references, pinned by a
> regression assertion (S-43).

S-43 asserts `shouldRenderToolCall(name, …, verboseChatEnabled=false)` returns `true` for the six
names. **That is an assertion about a predicate, not about whether the parent conversation ever
reaches it.** A delegated sub-turn runs as its own session with its own `transcriptSessionID`
(ADR-057 D2/FR-011, cited in root `CLAUDE.md`), and root `CLAUDE.md`'s tool-visibility section
records that `delegate`'s `run` sub-case *"plus its whole SubagentBlock delegation card in the
thread"* is **hidden by default**, while the ActivityPanel *"only ever shows subagent spans and
background `bash` sessions"*. If a child's individual tool calls do not surface in the parent
thread, then for a delegated snapshot the render mitigation is not merely weaker — it is absent,
and `shouldRenderToolCall` returning `true` is true and irrelevant.

This matters now specifically because of D1.2's supersession. §6 handles it in one sentence:

> The supersession does, however, **sharpen** §2.3's accepted risk: a snapshot taken by any agent
> on the workspace sees the same signed-in state as every other, so "a snapshot of a signed-in page
> can carry a card number into the transcript" now applies to delegated work too, with no
> attended-operator discriminator to soften it.

The paragraph identifies the widened population and then does not re-check the mitigation against
it. §6's D1.2 bullet says *"Checked explicitly: the only places this document discusses unattended
agents are §11(a) and §12 A-4, and both are about who can answer an `ask` approval prompt"* — which
is exactly the check that would have caught this, run over the wrong requirement. The `ask`
question was checked; the **render** question was not.

**Recommended fix.** Establish, from code, whether a delegated sub-turn's `browser_snapshot` tool
call renders in the parent conversation's thread with `verboseChatEnabled=false`. If it does, say so
in §2.3 with the evidence and extend S-43 to assert it at that level rather than at the predicate.
If it does not, FR-028(a) does not cover the delegated case, and either the audit half must carry
the whole mitigation for it (which C2 shows is currently non-functional) or the ruling needs
re-opening for unattended snapshots specifically.

---

### M1 (MAJOR) — §3 restates the lease-membership rule D1 discarded

§3's shared interface contract (lines 309-316) and Stream F (line 500):

> D2's FOUR new ACTION tools are automatically in scope via that annex's membership **RULE**
> (§14.2 rule 3: **every tool in `pkg/tools/browser` that mutates page or tab state** takes the
> lease, enforced against the REGISTRY, not a hand-written list)

D1 §14.2 rule 3 at HEAD says the opposite, under a heading that names this exact rule as the defect:

> **Why this rule and not "does it mutate".** The previous draft's rule was *"every tool that
> mutates page or tab state acquires the lease; the exemption list is exactly the five"* — and
> `browser_handle_dialog` **mutates page state** while being on the exemption list. The rule
> contradicted its own exemption, so no test could classify by it …
>
> **The rule:** a `browser_*` tool takes the write lease **if and only if** it is gated by the
> ADR-038 D6 human-control lock — that is, if and only if its `Execute` calls `controlledResult`.

The **count** D2 states (four leased, two exempt) is still correct and agrees with D1's normative
block. The **derivation** is not: under the biconditional, D2's four tools are leased because they
call `controlledResult`, not because they mutate. That is not pedantry — it is the difference
between a property D2 controls and one it does not, and it is what M2 turns on.

**Fix.** Replace the parenthetical with the biconditional, cite D1 §14.2 rule 3's per-tool table,
and drop "enforced against the REGISTRY, not a hand-written list" (D1's test enumerates the registry
to compare *two gates*, not to read a membership list).

---

### M2 (MAJOR) — D1's new rule places a testable obligation on D2 that D2 has no requirement for

D1 §14.2 rule 3 closes with:

> **The one cross-spec obligation.** D2 must register `browser_handle_dialog` ungated by
> `controlledResult`, **and must gate its four action tools.** If D2 declines the first, the
> biconditional acquires its first exception, this table becomes a list again, and that is an
> operator decision rather than a spec's.

D1's `TestWriteLease_EveryActionToolIsLeased` (its test 18) enumerates the registry and exercises
every registered `browser_*` tool under a held control lock and a held write lease, asserting the
two answers **agree**. So if `browser_hover` ships without `controlledResult`, **another
document's test goes red** and D2 has no requirement anyone can point at.

D2 satisfies the first half explicitly and well: FR-035 has a number, an argument, two scenarios
(S-30, S-31a/b), a §5 non-behavior and a test. FR-038 does the same for the snapshot. **The second
half — the four action tools must call `controlledResult` — exists only as prose**: §2.1's
`controlledResult` row ("Every new *action* tool must call it, except …") and §3's call-order line.
There is no FR, no BDD scenario, no dataset row and no TDD entry. §5's non-behaviors forbid the two
exemptions and never mandate the four inclusions. §9's traceability has no row for it.

This is a straightforward asymmetry: the exemptions, which are cheap to get right, have full
coverage; the inclusions, which are the ones a refactor silently loses, have none.

**Fix.** Give it a number (FR-039), a scenario asserting structurally — as S-60 already does for
the snapshot's *absence* of the calls — that each of `browser_select_option`, `browser_press_key`
(both the locator and no-locator paths), `browser_hover` and `browser_upload_file` calls
`controlledResult`; add the TDD entry; and record the reciprocal obligation in §6's D1 boundary and
in §15 item 2, which currently lists only what D2 requires **of** D1.

---

### M3 (MAJOR) — §15 item 2's defects (a) and (b) are fixed at HEAD

Verified in `browser-workspace-ownership-spec.md` at HEAD:

| D2's claim | Reality at HEAD |
|---|---|
| (a) "The set is five and should be six … **Status: being fixed concurrently, uncommitted.**" | §14.2 rule 3 opens with **"THE NORMATIVE COUNTS … the exempt set is SIX"**, includes `browser_list_tabs` with per-tool evidence (`tabs.go:20`), and US-9/AC4 + AC4a carry it. Committed. |
| (b) "D1's own **MAJ-008** disposition row still reads 'three'." | §15 MAJ-008 now reads *"**The exemption is a closed set of SIX, not three** … This row previously said 'three' while §14 said 'five'"*. Fixed. |
| (c) "D1 cites the snapshot's exemption as **D2 FR-018** … **NOT yet fixed**." | **Still true.** `browser-workspace-ownership-spec.md` lines 718 and 1481 both read `D2 FR-018`. |

D2's own diagnosis of round-2 M1 was that quoting superseded D1 text "would have cost a round trip
and invited a duplicate amendment". Revision 3 corrected the quote and left the status; the same
failure has recurred one revision later against newer text. **(c) alone is the live dependency**,
and it is a one-token edit in D1.

---

### M4 (MAJOR) — the ADR D2.9 erratum this spec asks for already exists, filed by this spec's own commit

**The substance is correct and this finding does not disturb it.** ADR D2.9's table at HEAD:

| Tool | Jim | Ray | Mia | Ava |
|---|---|---|---|---|
| `browser_upload_file` | **ask** | **ask** | deny¹ | deny¹ |

> ¹ **Erratum, 2026-08-31.** … But Mia and Ava resolve **`deny`** regardless, and this table
> previously showed `ask` for them, which was wrong. `denyAllThenOverride` writes an *explicit
> agent-level* `deny` … and `compositor.go::resolveEffectivePolicyWith` merges **deny > ask >
> allow** …

That is §11 footnote ³ and §12 A-16, verbatim in mechanism and conclusion. **S-25 and S-48 are
consistent with it and with each other**, US-8/AC3 and US-12/AC3 agree, order 4 is split correctly,
and edit site 6a's "NO EDIT — and that is the decision" is right. The primary drift check on this
axis passes.

What is stale is the **status**, in three places, all of which route work that is done:

- §12 A-16: *"**The ADR erratum this raises, and it needs the ADR's owner:** ADR D2.9's table lists
  Mia = Ava = `ask` … Recommended amendment: the table's Mia/Ava cell reads `deny`, with a footnote
  that the global `ask` is overridden downward"* — that is a description of text that now exists.
- §15 item 3: *"**The ADR's D2.9 table says `ask` for them** and its own next paragraph implies
  `deny` — that contradiction predates this spec and needs an ADR erratum either way."*
- §11 edit site 21 lists the ADR-072 D2.11 errata as outstanding and omits D2.9 entirely, so a
  reader reconciling the two cannot tell which errata landed.

There is also a **disposition-class error**. §12's legend: *RULED = decided by the operator,
recorded, not re-litigable here. DECIDED = decided by this spec on the evidence; overrulable.*
ADR D1.1a names Daniel Piatkowski as decider for every ruling in the ADR, and the erratum is an
operator-authority correction inside it. A-16 is therefore **RULED**, and §15 item 3 — whose whole
purpose is to route a decision to the operator — is asking him to rule on something already ruled.
Revision 3 diagnosed exactly this in round-2 M1 for §15 item 2 and reproduced it in item 3.

**Fix.** Re-verify A-16 against the ADR at HEAD, relabel it RULED with the erratum quoted, close
§15 item 3 (keeping the two-edit-site reversal path as a note, since it remains true), and add the
D2.9 erratum to edit site 21's ledger as **landed**, with its commit.

---

### M5 (MAJOR) — three ADR sections post-date this revision and none is absorbed

Commit order, verified:

```
5dbc8c41b  ADR D1.1a
3667c06ae  D1 spec rewritten
69652622d  lease relocated to D1
335d56fe6  D2 spec rewritten; D1 exemption widened to five
da6bb665d  ADR §8 corrections log
dfa9dd4e9  D2 spec REVISION 3  <-- this document
6447bc4ee  ADR D1.1b + D1.1d      <-- after
028abcfcb  ADR D1.1c thrash       <-- after
```

`git show 6447bc4ee` and `028abcfcb` are **pure insertions** — no D2-relevant text was deleted, so
nothing this spec quotes was invalidated by them; what happened is that three new rulings arrived
that it has never read. §0b row 13 says *"Two ADR changes absorbed: D1.2 superseded and D1.1a"* and
§6 carries exactly those two. The unabsorbed material, in order of D2 relevance:

**D1.1d — the dialog ruling is now an operator ruling, not a grill finding.**

> A stuck dialog may be cleared even while a human holds control. `browser_handle_dialog` is exempt
> from the write lease **and** from `controlledResult`. … Gating the one tool that can clear it
> leaves both parties frozen. This narrows ADR-038 D6's exclusivity by exactly one tool.

That is FR-035, ratified upstream, with the same reasoning D2 derived independently (§3 Stream C's
"lease deadlock" + "human-viewer lockout"). D2 still attributes it to "grill C5" in §0b row 5, §9's
FR-035 ADR column and §14.1's C5 row, has **no §12 row** for it at all, and §15 item 2 still frames
"D1 §14.2 rule 3 must carry it" as an open ask. It is carried, by both D1 and the ADR. A
requirement whose provenance is an operator ruling should say so — this document is careful about
that distinction everywhere else (A-2, A-4).

**D1.1b — the pool's sizing decisions.** `--renderer-process-limit=N` (start N=4); a `max_browsers`
formula; a **cgroup memory-pressure admission gate** ("Refuse to grow the pool when
`memory.current / memory.max > 0.85`"); and the explicit statement that the renderer cap "**is not
acceptable if agents are pointed at arbitrary hostile URLs**" (see O1). Neither this spec nor the D1
spec mentions `--renderer-process-limit` anywhere.

**D1.1c — eviction, the thrash detector, and "the cap is a soft target".** See C1, M6, M7.

**Fix.** Add a revision-4 absorption row to §0a; rework §6's D1.1a bullet into a D1.1a–D1.1d bullet
that states, per section, what changes for D2 and what does not; and re-source FR-035.

---

### M6 (MAJOR) — the workspace browser can now vanish between two tool calls, and every D2 verb assumes it does not

D1.1c's guards are *"never evict an instance with a viewer attached"* and *"never evict an instance
with a tool call in flight"*. Neither holds in the gap **between** two tool calls of one turn, and
the first does not exist at all for the unattended delegated work D1.2 just made ordinary. So under
the ADR's ruling, this sequence is legal:

1. Agent calls `browser_snapshot` on a half-filled signed-in form; gets an outline with handles.
2. Another workspace needs a browser; the pool evicts this one (LRU); its Chrome dies.
3. Agent calls `browser_click{role:"button", name:"Continue", index:3}` — the pool relaunches,
   the profile restores the login, the tab reopens at whatever the start URL is.

§12 A-8 chose the `index` handle over an opaque node id on this reasoning:

> An opaque node id would be a second identity scheme that **goes stale on the next DOM mutation
> with no way for the agent to tell.**

An `index` into a re-derived role+name ordering has the same property under eviction, and worse: it
does not fail, it silently resolves a *different* element on a page whose state the agent's plan
assumed. §13 holdout 5 covers the intra-page SPA re-render and correctly demands "a *named*
resolution error"; the eviction case produces no error at all. Every entered form value, every
navigation, every dialog the agent had cleared, is gone with no signal.

**Fix.** Either obtain from D1 a guarantee that an instance is pinned for the duration of a **turn**
rather than a **call** — which is the granularity D2's tools actually compose at — or add a named
error: the browsing context carries a generation/epoch, tools capture it, and a mismatch returns
"the browsing context was replaced since your last call; re-read the page" rather than acting.
State the choice in §5 either way.

---

### M7 (MAJOR) — whole-Chrome teardown is an unnamed teardown site for D2's own dialog state

Edit site 15 names the teardown sites **exhaustively**, and revision 3 added `ReapIdleSessions`
after round-2 unasked-Q7 with the right reasoning:

> Evict all three pieces of state at every teardown site, named exhaustively: explicit
> `browser_close_tab`, the `Session()` ctx-recreation path, AND `ReapIdleSessions` … a stale
> `target.ID` surviving a reap makes the re-arm a silent no-op and the ADR-041-F3 wedge returns.

D1 adds four more ways a tab ceases to exist that are not tab-level at all: **whole-Chrome idle
close** (D1.1a item 3, "Closing a whole Chrome is a new operation"), **FR-046's explicit close**,
**per-Chrome crash containment** (D1.1a item 4), and **eviction** (D1.1c). If `sessionEntry` — or
its `dialogListeners` / pending-dialog / `lastActivation` fields — survives the death of the process
those `target.ID`s belonged to, the per-tab listener re-arm on the next launch is a lookup hit and a
return, and the tab is wedged with no record. That is verbatim the failure this edit site exists to
prevent, reachable through a path it does not name.

**Fix.** Either add whole-Chrome teardown to edit site 15 and a scenario (`alert()` on a tab whose
Chrome is closed and relaunched → the listener is armed exactly once), or obtain and cite a written
D1 guarantee that `sessionEntry` is destroyed with its Chrome and cannot outlive it. An assumption
is fine; an unstated one is what ADR-041 F3 was.

---

### M8 (MAJOR) — `browser_hover` is specified only up to the point where it stops being useful

- **US-7:** *"As an agent, I want to reach a menu that only opens on hover."*
- **US-7/AC1 and S-22:** the submenu becomes visible and `window.__clicked` is `undefined`.
- **Dataset:** "hover target with a click counter → menu visible, counter `0`".
- **§3 Stream B:** "scroll into view, then `Input.dispatchMouseEvent{type:"mouseMoved"}` at the box
  centre. Gate applies. **Must not** click."

Nothing anywhere states whether the hover state survives the **next tool call** — and reaching the
menu item is the entire user story. §3's own post-gate table (added in revision 3 for FR-007)
records what the next call does:

| chromedp issues after the gate | Source |
|---|---|
| `DOM.scrollIntoViewIfNeeded`, `DOM.getContentQuads`, 2 × `Input.dispatchMouseEvent` | `MouseClickNode` (`input.go:57-92`) |

So a `browser_click` on the revealed item scrolls, re-measures and dispatches press/release at new
coordinates, with no intervening `mouseMoved` — and whether Chrome's `:hover` chain is preserved
across that, for a pure-CSS `#nav:hover > .submenu` menu whose parent is no longer under the
synthetic pointer, is **an untested dependency behaviour**. It is precisely the class of question
§2.2a resolved by reading chromedp's source and pinned with `TestChromedpEnablesPageDomainPerTarget`
(order 0c). Here it is not asked.

Note the ADR asks only for "An agent can open a hover-triggered menu" (criterion 10), so the spec
satisfies the ADR literally while possibly not satisfying its own user story. That gap is the
finding.

**Fix.** Add US-7/AC2 and a scenario: a pure-CSS `:hover` submenu; `browser_hover` on the parent,
then `browser_click` on a submenu item; assert the item's handler fired. If it cannot be made to
work through `chromedp.Click`, say so in §5 as a stated limitation and tell the agent what to do
instead — an unstated one will be found by an agent on a real navigation bar.

---

### M9 (MAJOR) — §14.1's C4 row still asserts the design revision 3 replaced

§14.1, C4:

> `_AskWithNoApprover_Terminates` **fails while #659 is open**, by design

§10 order 0b, §8 S-27 and §14.2's C3 row all say the opposite, and give the reason:

> **Lands `t.Skip`-ped** … **a permanently-red committed test is not a gate** — it contradicts Hard
> Constraint #7 and reds `pr.yml` forever, and a gate that can never go green blocks forever.

The same cell also calls orders 0a and 0b "gates", where order 0b's own row reads *"**Oracle,
carried by #659 — not a gate in this repo**"*.

Revision 3 relabelled §14.1's M2 and M10 for exactly this failure mode (round-2 m8: *"a disposition
table that overstates is worse than one with open rows"*) and left C4 carrying a retracted design in
the same table. A reader auditing the round-1 sheet finds the wrong answer.

**Fix.** Rewrite the C4 cell to match order 0b, and state that the round-1 disposition was itself
superseded by round-2 C3 — the sheet is now a record of two rounds and needs to say which round
each cell reflects.

---

### M10 (MAJOR) — the accepted-risk statement omits the destination outside the operator's machine

§2.3, "The accepted risk, stated plainly rather than softened":

> A `browser_snapshot` of a signed-in page can carry a **card number, a partially typed password, or
> an account identifier** into the model's context, into the conversation the operator reads, and
> into the stored transcript at `sessions/<id>/<YYYY-MM-DD>.jsonl` — which has a **90-day default
> retention**.

Three destinations, all local. The fourth is the one that leaves: **the model provider's API.**
"Into the model's context" is where it goes *logically*; physically it is serialised into a request
body and sent to OpenRouter, Anthropic, Google or whoever serves that agent's model, retained under
that vendor's policy, not this one's. For a project whose positioning is "community-facing, no
telemetry" and whose credentials are AES-256-GCM at rest, that is the material sentence, and it is
the one missing.

It also undoes the argument the ADR used to set the policy postures. ADR D2.9:

> `browser_upload_file` … is **the only browser tool that carries a local file across the boundary
> into a remote site; every other browser tool moves data inward.** That asymmetry is worth one
> confirmation from whoever is driving.

That asymmetry justified `ask` on upload and `allow` on the other five. A values-emitting snapshot
of a signed-in page **moves data outward** — to a third party — and under D1.2 it does so from
unattended delegated work with no operator watching. The spec is not obliged to re-open the
operator's ruling; it is obliged to state the risk completely, and §2.3 exists for exactly that.

**Fix.** Add the provider destination to §2.3's enumeration and to §4's observable line; add one
sentence to §12 A-2 recording that D2.9's inward/outward asymmetry does not survive a
values-emitting snapshot, so the operator can see the argument he ruled on has moved.

---

### M11 (MAJOR) — D2 creates the ADR's open preview-token question and never names it

ADR §6, "Genuinely open":

> **Does the preview URL need re-keying?** `serve_web` mints `/preview/<agent>/<token>/` per
> **agent** while the browsing context is per **workspace**. Whether a preview minted by one agent
> should be reachable from a tab another agent drives is not decided; **the token is the credential**
> (ADR-044 FR-023), so this is a **security question**, not a routing one.

D2 is the document that makes this happen. FR-019 tells the agent, in an error message, to mint one.
FR-030 grants `serve_web: allow` to three more agents specifically so they can. The workspace
browser is shared by every agent on the team (D1.2). The result is a tab whose URL is another
agent's bearer credential, readable by any co-workspace agent through `browser_list_tabs`,
`browser_get_text`, or — now with structure and values — `browser_snapshot`.

§6's D1-boundary list names four things D2 does not decide and this is not among them; §12 has no
row; §11(c)'s argument for the `serve_web` grant covers write reach, preview-listener confinement
and `gateway.preview_enabled`, and does not reach cross-agent token visibility in a shared browser.

**Fix.** Add it to §6 as a fifth D1/ADR boundary item and to §12 as an assumption, citing ADR §6 and
ADR-044 FR-023. D2 need not solve it — but a spec that creates a security question the ADR has
flagged as open should not be silent about it.

---

## Minor findings

**m1 — three dangling `§15 item 5` references.** Line 447 (§3 Stream C, FR-035's preamble: *"D1
§14.2 rule 3 must carry it — see §15 item 5"*), line 494 (Stream F: *"the lease was relocated …
see … §15 item 5"*) and line 1365 (§12 B-3: *"Listed in §15 item 5"*). §15 item 5 is A-12
(`browser_handle_dialog: allow` with `accept:false`). The intended targets are item **2** for the
first two and item **6(b)** for the third — §9's FR-035 and FR-038 rows already cite item 2
correctly, so the document contradicts itself on the same reference.

**m2 — #667 is never cited by number.** §2.1's `AuditEntry` row and §12 B-6 describe the defect in
full and end with *"File it; do not fold it in"* — while `dfa9dd4e9`, the commit that landed this
revision, is titled *"docs(spec,adr): D2 spec revision 3; audit-log defect filed as **#667**"*, and
#667 is OPEN with a title that matches B-6 word for word. The spec requires an issue number in the
FR-034 config key's doc comment (SC-009) and does not apply the rule to itself. Given C2, the number
is load-bearing, not bookkeeping.

**m3 — §2.2's `GetBoxModel` stability fallback contradicts FR-007.** §2.2's primitives table:
`| Stability (fallback) | dom.GetBoxModel() / dom.GetContentQuads() |`. FR-007, S-15, US-4/AC1 and
the dataset row all assert **zero** `DOM.getBoxModel` inside `waitActionable`. §3 scopes the claim
to "the fast path"; §9's FR-007 row does not. No requirement says when the fallback fires, what
triggers it, what it costs, or whether the FR-007 counter tolerates it. Either the row is a
leftover from the pre-rescope design and should go, or the fallback is real and FR-007 needs the
fast-path qualifier everywhere plus a dataset row for the slow path.

**m4 — `browser_press_key`'s modifiers are unspecified.** §3 and FR-010 enumerate the accepted key
names exactly — `Enter`, `Tab`, `Escape`, `ArrowUp/Down/Left/Right`, `Backspace`, `Delete`, `Home`,
`End`, `PageUp`, `PageDown` — and then say "**plus modifiers**" with no set, no syntax and no
positive scenario. The single modifier-adjacent artefact is the negative `key:"Ctrl+Banana"`, which
establishes that `+` separates and `Ctrl` is plausible, and settles neither. Two engineers build
different parsers; `Ctrl` vs `Control` vs `ctrl`, `Cmd` vs `Meta`, and combination order are all
open. Given "An unrecognised name is an error listing the accepted set", the accepted set must exist.

**m5 — multi-select is specified for labels only.** §3: *"Multi-select accepts an array."* The
partial-match rule, S-18 and the dataset row are all written in terms of labels ("errors naming the
**unresolved labels**", "error naming `Gamma`"). Whether `{value: ["a","b"]}` is legal, and what a
partial `value` array does, is undefined — one revision after `value` was promoted from prose to a
tested parameter (S-61) precisely because "nothing distinguished a shipped parameter from a
sentence".

**m6 — the CI process budget ignores per-workspace profile directories.** §6 (iii) concludes *"one
Chrome at a time, torn down between orders"* and budgets *"one headless Chrome plus the Go test
binary"*. Under D1.1a each workspace's Chrome carries its own `--user-data-dir`; §6 (iii) requires
the fourteen real-Chrome orders to "each stand up their own workspace", i.e. fourteen profile
directories on a worker whose disk root `CLAUDE.md` names as the binding constraint. Neither
creation cost nor cleanup is stated. A leaked profile dir per failed run is a slow disk leak on the
same worker the repo already had to protect from OOM.

**m7 — the FR-034 time-box's decision rule is blank in the case it was written for.** §12 A-13
requires the removal issue to carry *"a **decision rule** over the FR-032 counters: if
`gate_failure_total{condition="stable"|"hit-testable"}` over the window is a **negligible fraction**
of total gated actions … the switch is deleted"*. §12 A-18 concedes: *"`gate_failure_total` is
necessarily zero in `visible_only`, which the runbook must not read as 'no failures'."* So on the
install that flipped the switch — the only install with evidence the gate misbehaves — the counters
read zero and the decision rule says "negligible: delete the switch", which is the opposite of the
right answer. The rule is satisfiable only where it is not needed.

---

## Observations

**O1 — the renderer cap's stated precondition and D2's stated agent behaviour are in tension.**
ADR D1.1b: *"`--renderer-process-limit` means over-limit navigations reuse same-site processes,
weakening site isolation for the pages beyond the cap. That is acceptable for agent-driven browsing
of semi-trusted destinations and **is not acceptable if agents are pointed at arbitrary hostile
URLs**."* §11(b)(i) argues the dialog grant partly on the grounds that *"Ray, Explorer and
Researcher are among the agents most likely to hit a dialog, **because they browse arbitrary public
sites**"*. Setting the cap is D1's; noticing that D2's own agent model is the input to D1's
acceptability judgement is worth one line in §6.

**O2 — ADR D1.2's sole named mitigation does not exist in v1.** D1.2: *"The accepted risk, stated
once: an unattended agent can act as the operator on any site the workspace is signed into … 
`browser_upload_file`'s global `ask` seed (D2.9) is **the one place this is still gated**, and issue
#659 is its prerequisite."* Under FR-029 that tool is not registered until #659 lands, so for v1 the
ADR's one named gate on the shared-login risk is absent. Not D2's to change — the hold is correct —
but worth stating so D1.2 is not read as gated.

**O3 — the structural machinery is sound.** §9's arithmetic checks out (38 FR identifiers − 1
tombstone = 37; 62 scenarios − 3 = 59; 18 US − 1 = 17); every live scenario appears once in the
scenario→FR list; every live FR carries a TDD order; the two build-gate FRs are marked as such
rather than left blank; the tombstone convention resolves all cross-references from both prior
grills and from D1. The `file::symbol` discipline holds and the line numbers that remain are
fixtures. The problems above are drift and completeness, not craft.

---

## Structural integrity results (`plan-spec` mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | US-14 tombstoned with its ACs relocated and the tombstone labelled. |
| Every acceptance scenario has ≥1 BDD scenario | **PASS (2 documented exceptions)** | US-4/AC2 and US-17/AC3 marked non-automated by design in §9. |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** | Each S-xx header carries US + FR. |
| Every BDD scenario has a TDD test | **PASS** | §9's test column, §10's orders. |
| Every FR appears in the traceability matrix | **PASS** | Including the FR-023 tombstone. |
| Every BDD scenario appears in the matrix | **PASS** | §9's scenario→FR list, verified against the §8 headers. |
| Test datasets cover boundary / edge / error | **PARTIAL** | See Phase-3 table; hover, modifiers, multi-select-by-value and the pool-unavailable case have no rows. |
| Regression impact explicitly addressed | **PASS** | §10's named regression list with `file:line`. |
| Success criteria measurable, no subjective language | **PARTIAL** | SC-009 rests on a surface C2 shows is non-functional; A-13's decision rule is unsatisfiable (m7). |
| **Cross-document consistency (added for this round)** | **FAIL** | M1, M3, M4, M5, C1 — the spec is consistent with an ADR and a sibling spec that have both moved. |

---

## Test coverage assessment

| Gap | Level | Finding |
|---|---|---|
| The four action tools call `controlledResult` | Unit/structural | **M2** — no test; another spec's test is the only assertion. |
| Hover → act on the revealed item | Integration | **M8** — S-22 stops at "visible". |
| A browser unavailable for a **pool** reason | Integration | **C1** — order 25 covers no-Chromium only. |
| Whole-Chrome teardown → dialog-listener re-arm | Integration | **M7** — order 16 covers ctx recreation, not process death. |
| Browsing context replaced between two calls | Integration | **M6** — no scenario, no dataset row. |
| A delegated sub-turn's snapshot renders in the parent thread | vitest / integration | **C3** — S-43 asserts the predicate, not the surface. |
| `browser_press_key` with a modifier | Integration | **m4** — only the negative case exists. |
| `browser_select_option` multi-select by `value` | Integration | **m5** — labels only. |
| FR-028's audit record is readable end-to-end | Holdout 11 | **C2** — the holdout will fail on any install with a paired channel, and the spec predicts this without connecting it. |

Strong points worth keeping: S-59's CDP-seam concurrency assertion (the sequential form genuinely
could not test the invariant), S-62's seam-not-timing assertion for the one sanctioned gate bypass,
S-24's "the tool contains no `AllowedRoots` comparison" structural check, order 0c's dependency pin,
and FR-036's oracle, which the round-2 reviewer actually ran.

---

## STRIDE summary

| Component | Threat | Status in the spec |
|---|---|---|
| `browser_snapshot` output | **Information disclosure** — field values from a signed-in page | Ruled and accepted; §2.3 states it but **omits transmission to the model provider** (M10) and the mitigation is unverified for delegated turns (C3). |
| Audit records | **Repudiation** — "which agent captured what" | FR-028/FR-031 emit it; the reading surface is broken (C2). |
| `browser_upload_file` | **Information disclosure / EoP** — local file to a remote origin | Best-handled item in the spec: `FSOpWrite` at the chokepoint, `ask`, an audit event, registration held on #659. `RealPath()` TOCTOU stated. |
| `serve_web` preview token in a shared browser tab | **Spoofing / EoP** — one agent's bearer credential visible to co-workspace agents | **Unaddressed** (M11); ADR §6 flags it as open. |
| `data-omnipus-tsel` marker | **Tampering** — a hostile page stamps the marker | Inherited, stated, mitigated by the random per-resolution token. |
| Browser pool | **DoS** — memory exhaustion, thrash, or refusal | D1/ADR contradict each other (C1); D2 has no error path for either outcome. |
| `browser_handle_dialog` | **EoP** — `accept:true` on a destructive `confirm()` | Argued in §11(b); `accept` defaults to `false`; visible in the chat thread — subject to C3 for delegated turns. |
| Chrome profile directories | **Information disclosure** — site cookies at rest, outside the credential vault | D1's, and out of D2's scope; noted so it is not lost between the two documents. |

---

## Unasked questions

1. **If the pool evicts (ADR D1.1c), what does an agent mid-task observe?** Nothing in either spec
   tells the agent its browsing context was replaced. "Silently starts over" is not an outcome D2's
   error-naming discipline would accept anywhere else.
2. **Does a delegated sub-turn's `browser_snapshot` reach the operator's chat thread at all?** The
   whole render mitigation turns on it (C3) and nothing in the spec asks.
3. **Where does the D1.1b cgroup pressure gate's refusal surface to the agent?** D1.1c says there is
   no pool-full error surface; D1.1b says admission is refused under pressure. One of them is wrong,
   and D2 receives whatever falls out.
4. **Which of `browser_snapshot`'s 64,000 bytes actually survive `windowTrim`?** §12 B-2 states the
   per-tool cap and the turn budget are separate and stops there. Two capped tools in one turn is
   128 KB; ADR-066's trim then evicts whole turns. Whether an agent can reliably act on a snapshot
   it took two turns ago is unexamined, and A-8's handle contract implicitly assumes it can.
5. **Who owns the ADR↔D1 eviction contradiction, and does D2 wait for it?** §15 routes one open
   ruling (A-3) and two cross-spec items. This is a third and larger one, and it is not listed.
6. **Is `TestVisibility_TierArithmetic`'s union literal still 87 after D1's own tool changes?**
   D1 adds no tools, so probably — but D2's E2 literals (`68`/`93`) were computed against a tree
   that has since taken four ADR commits, and the spec's own standing rule is that a claim marked
   verified in an earlier revision is not evidence.
7. **What happens to a pending dialog when its Chrome is evicted?** §2.1's reaper row answers the
   tab-level version ("a dialog open at reap time simply dies with the tab, which is correct"). The
   process-level version, where the *workspace* rather than the tab disappears, is not asked.

---

## Verdict

**BLOCK.**

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-agent-capability-spec-review-round3.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/browser-agent-capability-spec.md docs/internal/specs/browser-agent-capability-spec-review-round3.md
```

**Suggested order of work**, because two of the three CRITICALs are not this spec's to close alone:

1. **C1** — route the ADR D1.1c ↔ D1 FR-039 contradiction to the ADR's owner. Nothing in D2's pool
   handling can be written until it is settled, and both M6 and M7 fall out of the answer.
2. **C3** — a one-afternoon code question with a large blast radius on an operator-mandated control.
3. **C2** — decide whether #667 gates FR-028 or FR-028 names a different primary surface.
4. **M1–M5** — the drift edits, all mechanical once C1 is answered.
5. Everything else.

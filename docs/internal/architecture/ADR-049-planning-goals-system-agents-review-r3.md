# Adversarial Re-Review (r3, targeted): ADR-049 — Planning & Goals (amended r2)

**Spec reviewed**: `docs/internal/architecture/ADR-049-planning-goals-system-agents.md` (amended 2026-07-19 post grill-review r2, commit `01cc8741` on `feature/planning-goals`)
**Prior reviews**: r1 (`…-review.md`, verdict BLOCK) · r2 (`…-review-r2.md`, verdict REVISE: 1 MAJ / 5 MIN / 3 OBS)
**Review date**: 2026-07-19
**Review mode**: generic-markdown (ADR), **targeted re-check of the r2-amended paragraphs** per the r2 review's own fast-path recommendation — closure verification + contradiction sweep of the edited text, not a fourth full eight-lens pass
**Verdict**: **PASS**

## Executive Summary

All nine r2 closures verify as present, substantive, and codebase-accurate: the new
D7 "Judge unavailability ≠ verdict absent" paragraph resolves R2-MAJ-001 exactly as
recommended (and its new `[FACT]` citation checks out — `defaultRetryBackoffMs =
{60000, 120000, 300000}`, `pkg/cron/service.go:67-71`); the five MINORs and the
observation-level items (grandfather clause, both citation fixes, Gap #8
origin-based enforcement) are all closed in the current text. The contradiction
sweep of the amended paragraphs found no new conflict — the sharpest candidate
(Gap #8's "cron-injected `/goal` is inert" vs D6's cron-driven `/loop`
implementation) is coherent on inspection. Residue: 1 MINOR (a truncation-boundary
edge inside the adopted R2-MIN-002 fix) and 4 OBSERVATIONS (stale r1-only header,
§9 test-list carry, unpushed branch, Judge disable-ability unstated). No CRITICAL,
no MAJOR: **PASS**.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 0 |
| MINOR | 1 |
| OBSERVATION | 4 |
| **Total** | **5** |

---

## Part A — r2 Closure Verification Matrix

Each r2 finding, what the current ADR text says, and the verification result.
Code citations re-checked on `feature/planning-goals` @ `01cc8741`.

| r2 ID | Claimed closure | Verification in current ADR text | Status |
|---|---|---|---|
| R2-MAJ-001 | D7 paragraph "Judge unavailability ≠ verdict absent" | Present in D7: NFR-2 fail-closed explicitly scoped to "a judge that RAN and produced no/negative verdict"; unavailability (SEC-26 throttle/cost-cap, provider error, timeout) → loop **pauses and retries with backoff**, reusing the cron transient schedule 60s/120s/300s — **citation verified**: `defaultRetryBackoffMs = []int64{60000, 120000, 300000}` (`pkg/cron/service.go:67-71`); **attempt not consumed**, no verdict recorded, pause surfaces in loop/plan status; **idle-expiry clock keeps running** so a permanently unavailable judge ends via calendar brake, "not via a correlated attempt-burn cascade". All four elements of the r2 recommendation adopted, in the recommended home (D7) | **CLOSED** |
| R2-MIN-001 | D2 rule 5 rejects deny OR ask | Present: "effective `bash` policy is deny **or ask** (rule 2 resolves `ask` to deny unattended, so both are structurally unsatisfiable)"; reject on agent paths, UI warns — the exact reject/warn split retained | **CLOSED** |
| R2-MIN-002 | Migration: normalize → truncate → disambiguate | Present in D1: "normalized first (lowercase, trim), then truncated to the 64-char cap (including the `milestone:` prefix), then collisions disambiguated … keyed by milestone ID order — so post-normalization/truncation collisions ('Q3'/'q3') are caught". Ordering and the post-normalization collision check match the recommendation | **CLOSED** — one boundary residual, see R3-MIN-001 |
| R2-MIN-003 | Empty milestones preserved | Present in D1: "milestones with no member tasks are preserved as migration-log entries (name + due_date) since a tag cannot exist unattached" — option (a) of the r2 recommendation, decision recorded with rationale | **CLOSED** |
| R2-MIN-004 | `system` type seed-only | Present in D3 Lifecycle: "type `system` is not creatable via REST or agent tools (400, mirroring the ADR-035/037 raw-body-sniff precedent) and seeded System Agents are not deletable — seeding is the only creation path". This also bounds the MAJ-001 exclusion surface to the single seeded Judge, as r2 noted it would | **CLOSED** |
| R2-MIN-005 | D4 owner-lifecycle | Present in D4: delete of an agent owning active plans/goal-loops **rejected (400)** until reassigned/stopped; disable **pauses** (blocked state on board, resumes on re-enable); "no silent week-long stall path exists". §3 Gap #6 narrowed to notification detail only. Note: the ADR chose reject-deletion rather than r2's suggested terminal wind-down — a valid, strictly stronger alternative that equally eliminates the silent-stall path | **CLOSED** |
| R2-OBS-001 | `summarizeSession` grandfather clause | Present in D3 (cross-referencing FR-7): goroutine-spawned, genuinely out-of-turn, "explicitly grandfathered un-attributed … the sole exception to the FR-7 standing rule", re-homed in v0.3 memory work. Placed in D3 rather than FR-7 itself — acceptable, since it names FR-7 explicitly | **CLOSED** |
| R2-OBS-002 | Citation fixes | (1) D3 now cites `applyMemoryCommandPrompt loop.go:9770` — **verified**: func at `pkg/agent/loop.go:9770`. (2) D3 exclusions re-grounded on `resolveDefaultAgentID` picking the first enabled **chat-target** via `IsChatTarget()` (= `!IsWorker()`) — **verified**: `pkg/config/config.go:919`; the text also adopts r2's central-predicate enforcement suggestion ("type `system` must be excluded from chat-target status") | **CLOSED** |
| Gap #8 (Part C) | Origin-based enforcement | Present in §3 Gap #8: enforcement "discriminates on **message origin** (system/cron/async-injected messages cannot start goals or loops — a cron-injected prompt beginning `/goal` is inert), not merely on surface"; surface exclusion (task runs, delegated sub-turns) and `DelegationDepth` bound retained | **CLOSED** |
| R2-OBS-003 | Change set committed | ADR + r1 + r2 reviews + CLAUDE.md OBS-001 correction committed as one change set (`01cc8741`) on `feature/planning-goals`, tree clean. **But the branch has no upstream — nothing is pushed** (`git ls-remote --heads origin feature/planning-goals` is empty) | **PARTIALLY CLOSED** — see R3-OBS-003 |

---

## Part B — Findings (r3)

### MINOR

#### [R3-MIN-001] D1 truncation-then-suffix ordering can overflow the 64-char cap at the boundary

- **Lens**: Incorrectness
- **Affected section**: D1 ratified migration requirements × D1 tag rules.
- **Description**: The adopted ordering is normalize → "truncated to the 64-char
  cap (including the `milestone:` prefix)" → disambiguate with `-2`, `-3`, …
  suffixes. A name truncated to exactly the cap that then collides receives a
  suffix and lands at 66+ chars — violating the separately-ratified tag rule
  (max 64). The two rules read together make the overflow tag illegal, but the
  reconciliation method is unstated, and naive re-truncation after suffixing can
  collide with a different already-emitted tag (the residual sliver of the
  "new collision class" r2 warned about). Two implementers will resolve this
  differently.
- **Impact**: Migration-only, boundary-length names with collisions — narrow, but
  it is exactly the class of silent re-merge R2-MIN-002 existed to prevent.
- **Recommendation**: One clause in D1: truncation reserves headroom for the
  disambiguation suffix, and the final tag (prefix + name + suffix) MUST be
  ≤ 64 chars with uniqueness guaranteed after re-truncation (re-run the
  collision check on the final string). Alternatively, explicitly delegate the
  exact algorithm to plan-spec with the invariant stated ("final tags unique
  and ≤ 64").

### OBSERVATIONS

#### [R3-OBS-001] Header and preamble still describe the r1-only amendment state

- **Lens**: Incorrectness (hygiene)
- **Description**: The Status line says "amended 2026-07-19 post grill-review
  r1" and the Ratification-mode note enumerates only the r1 findings
  (CRIT-001..003, MAJ-001..009, MIN-001..004, OBS-001..003), while the body now
  carries nine `(r2)` amendments. §9 step 1 likewise still instructs verifying
  "CRIT-001..003 and MAJ-001..009". A reader of the header alone would conclude
  the r2 findings were never incorporated.
- **Suggestion**: Update Status to "amended r2; grill-spec r3 targeted re-check
  PASS", add the R2-* IDs to the ratification note, and retire or restate §9
  step 1.

#### [R3-OBS-002] §9 step 4 test list does not carry the three tests the r2 review demanded

- **Lens**: Test coverage
- **Description**: r2 Part D named three tests missing from §9 step 4:
  (1) judge-unavailability ⇒ attempt NOT consumed + loop paused (now implied by
  the D7 paragraph); (2) normalized-tag collision test for the migration
  ("Q3"/"q3"); (3) `type: system` REST-create rejected 400 + seeded Judge
  non-deletable. The ratified text now implies all three, but §9 step 4 is the
  explicit carry-into-plan-spec list and was not extended.
- **Suggestion**: Append the three tests to §9 step 4 so plan-spec inherits them
  mechanically rather than by re-derivation.

#### [R3-OBS-003] Change set committed but unpushed on an ephemeral pod

- **Lens**: Inoperability
- **Description**: `feature/planning-goals` @ `01cc8741` contains the ADR, both
  prior reviews, and the CLAUDE.md correction — but the branch does not exist on
  `origin`. Per the environment's own rules, git-pushed is the only durable
  state; a pod recycle still destroys everything R2-OBS-003 warned about.
- **Suggestion**: Push the branch (this r3 review included) before plan-spec
  work begins.

#### [R3-OBS-004] D3 lifecycle decides create/delete for the seeded Judge but not disable

- **Lens**: Ambiguity
- **Description**: D3 ratifies "not creatable … not deletable"; FR-7 says
  "seeded, locked, visible, editable model + rubric prompt". Whether the seeded
  Judge can be **disabled** is unstated. If it can, a disabled Judge is
  functionally the permanent form of D7's judge-unavailability (every active
  loop pauses until calendar wind-down); if it cannot, "locked" should say so.
  Either answer is fine; the D7 pause path means neither produces the
  attempt-burn cascade — this is a one-clause completeness point, not a defect.
- **Suggestion**: One clause in D3 Lifecycle: either "seeded System Agents
  cannot be disabled" or "disabling the Judge is treated as D7
  judge-unavailability for every active loop".

---

## Part C — Contradiction Sweep of the Amended Paragraphs

Checked pairings, all coherent:

1. **New D7 paragraph × NFR-2**: no conflict — the paragraph scopes NFR-2's
   fail-closed rule to "a judge that RAN and produced no/negative verdict";
   pausing without a verdict is not defaulting to success.
2. **New D7 paragraph × D7's own idle definition (MIN-001)**: "idle = no attempt,
   state transition, or user interaction" could be read so that pause-entry (a
   state transition) resets the clock once; the paragraph's explicit stipulation
   ("the idle-expiry clock keeps running") governs thereafter, and backoff
   retries are neither attempts (not consumed) nor transitions — so wind-down is
   bounded at ≤ 7 days from pause entry in the worst reading. Coherent; plan-spec
   should encode the stipulation as the tiebreaker.
3. **Gap #8 origin rule × D6 `/loop` implementation**: `/loop` interval mode rides
   cron `every` + `continue` sessions — but those injections carry the loop's
   *work* prompts for an already-started loop; they are not `/goal`//`/loop`
   command text, and the origin rule only blocks *starting* goals/loops from
   synthesized messages. The user-originated `/loop` command remains the sole
   entry point. No conflict.
4. **D3 "seeded System Agents not deletable" × D4 "deleting an owner agent
   rejected while owning active loops"**: disjoint populations (the Judge owns no
   plans; owner agents are roster agents) — the two rules compose rather than
   conflict.
5. **D3 lifecycle × FR-7 "editable model + rubric prompt"**: consistent — edit is
   neither create nor delete.
6. **§3 Gap #6 row × D4 owner-lifecycle text**: the gap row's summary matches D4
   verbatim in substance; residue correctly narrowed to notification detail.
7. **§1 corrected facts, D2 rules 1–4, FR texts**: untouched by the r2
   amendments; spot-checked citations still hold (`IsPrivilegedAgent`
   `pkg/security/ratelimit.go:17-21`; `applyMemoryCommandPrompt`
   `loop.go:9770`; `IsChatTarget` `config.go:919`).

No new contradiction was introduced by the r2 edits.

## Unasked Questions (r3)

1. Can the seeded Judge be disabled, and if so is that D7 judge-unavailability?
   (R3-OBS-004 — one clause.)
2. What is the exact re-truncation rule when a disambiguation suffix would push a
   migrated tag past 64 chars? (R3-MIN-001 — one clause or explicit plan-spec
   delegation with the invariant stated.)

---

## Verdict

**PASS.**

All r2 findings — the single MAJOR and all five MINORs — are verifiably closed in
the current ADR text, the closures are substantive (not cosmetic), every new
`[FACT]` citation introduced by the amendments verifies against the code, and the
amended paragraphs introduce no new contradiction. The remaining 1 MINOR + 4
OBSERVATIONs are one-clause hygiene items that plan-spec can absorb; none is
direction-changing and none blocks the pipeline.

Per ADR §9 step 2, proceed to:

```
/plan-spec docs/internal/architecture/ADR-049-planning-goals-system-agents.md
```

Recommended before plan-spec begins: push `feature/planning-goals` (R3-OBS-003)
and optionally fold R3-MIN-001 and the four observations into the ADR in the same
commit as the header refresh (R3-OBS-001).

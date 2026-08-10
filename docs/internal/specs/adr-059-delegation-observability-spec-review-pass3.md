# Spec review — ADR-059 work items W1–W5 (adversarial grill, pass #3 / VERIFICATION PASS)

- **Reviewed:** `docs/internal/specs/adr-059-delegation-observability-spec.md` (510 lines, carrying
  amendments A1 and A2)
- **Prior passes:** [pass #1](adr-059-delegation-observability-spec-review.md) (BLOCK, 4C/8M),
  [pass #2](adr-059-delegation-observability-spec-review-pass2.md) (BLOCK, 5C/9M)
- **Branch:** `fix/uat-delegation-rootcauses` @ `e37f9389`
- **Authority:** ADR-059 (Accepted), ADR-058 §3/§7, `CLAUDE.md` Constraints #7 and #8
- **Reviewer mode:** `plan-spec`
- **Date:** 2026-08-10
- **Verification:** every `file:line`, symbol, count and render path below was read on this branch in
  this session. Pass #2's numbers were re-derived, not inherited.

---

## Executive summary

**Four CRITICAL, seven MAJOR, ten MINOR, two OBSERVATION. Verdict: BLOCK.**

**A2's central claim is true.** All nine edits the review was asked to verify physically exist in the
body. §2.2 row 4 names `GenericToolCall.tsx` (:180); §6 item 3 is retargeted (:381); §4's first
bullet is scoped to "**streaming-capable**" providers (:293); SC-001 is reclassified as a review
obligation (:410); SC-002 is restated off the withdrawn FR-009 (:413); FR-009 is struck through with
its reason (:404); §9.1 maps AC-01…AC-07 (:433); §10.5, §10.6 and §10.7 exist with substantive
content (:457, :468, :482); and A1-4's count is corrected to three implementers (:41). Pass #2's
headline finding — an amendment that described edits nobody made — is **closed**. This document does
not repeat it.

**And the corrected count is wrong again — because the commit that fixed the bug pass #2 found created
a fourth implementer that nobody counted.** At `e37f9389` there are **four** `ChatStream` methods, not
three: `ClaudeProvider.ChatStream` was added at `pkg/providers/claude_provider.go:64` in the
immediately preceding commit `45b01b14` — the fix A2-3 celebrates. The spec was amended one commit
later and still says three, in §2.1's symbol table (which omits `ClaudeProvider` entirely), in §2.2
row 1, in A1-4/A2-2, and in **§10.6's definition of done item 1** — *"all three implementers accept
it"*, a completion criterion that is false as written. ADR-059 §3, §4 and W6 say three as well, and
the ADR wins on disagreement. The count has now been wrong in three consecutive passes (two → three →
four), each time in the document that claims to have verified it by enumeration.

**The W5 gate is not a gate yet, and pass #2's diagnosis of it was itself incomplete.** Two facts
neither prior pass established:

1. **`write_file` is a *registered* tool UI.** `OmnipusRuntimeProvider.tsx:313` mounts
   `<FileWriteConfirmUI />`, which registers by tool name (`FileWriteConfirm.tsx:91`). AssistantUI
   dispatches registered names to their own component; `GenericToolCall` is only the **Fallback** for
   *unregistered* tools (`ChatScreen.tsx:303-320, 1396-1400`). So on the **live** path a `write_file`
   refusal never reaches `GenericToolCall` at all. The human sees `write_file · a.svg · 1.2 KB ·
   Failed` and **no reason whatsoever**. `GenericToolCall` renders `write_file` only on the
   **replay/historical** path, where `ChatScreen.tsx:983-993` renders every stored call through it
   with `error={tc.error}`. §6 item 3 is therefore right about one path and silent about the other,
   and US-5 AC-1 ("a human sees a readable message") is **already false live, today, before W5**.
2. **§6's "known precedent" paragraph is factually inverted.** It states that ADR-058's structured
   JSON ships today and *"no SPA component appears to special-case it — suggesting such payloads
   already render as text."* `GenericToolCall.tsx:76-90` **does** special-case it, and :507 renders it
   through a bespoke `DelegationFailureDisplay` whose own comment reads *"render a distinct,
   human-readable block instead of a raw JSON blob."* The real precedent is the opposite of the one
   §6 records: a structured payload required a dedicated SPA component to avoid becoming a blob. And
   that precedent lives in `result` (an object), not in `error` (a string) — so it does not transfer
   to W5's landing field. `error` is rendered raw at :523-524.

**Answering the two direct questions.** **W1/W2/W4 are not implementable without further questions** —
the implementer count is wrong, the compile-break sites are still asserted as "~13" and never
enumerated (now stale in the other direction), §9 row 2 still tells the implementer the wiring test
"must pass unchanged" while §2.3 and TDD #1 tell them to rewrite it, and §10.7 #2 leaves a live
either/or ("keep it as documentation, or delete it and say why") that ADR AC-02 constrains. **The W5
gate is still not a real gate** — four questions where A1-8 claims one, two of them answered only in
amendment prose and never recorded in §6, no alternative shape, and now a lead that points the
implementer at the wrong conclusion.

| Severity | Count |
|---|---|
| CRITICAL | 4 |
| MAJOR | 7 |
| MINOR | 10 |
| OBSERVATION | 2 |
| **Total** | **23** |

---

## CRITICAL

### C3-01 — There are four `ChatStream` implementers, not three; the definition of done names three, and the fourth is the one A2-3 created

- **Lens:** Incorrectness / Infeasibility
- **Affected:** §2.1, §2.2 row 1, A1-4, A2-2, §10.6 item 1, ADR-059 §3/§4/§6 W1

`grep -rn "func .*ChatStream" --include='*.go'` at `e37f9389`, non-test:

| Type | File:line | In §2.1? |
|---|---|---|
| `*openai_compat.Provider` | `pkg/providers/openai_compat/provider.go:206` | yes |
| `*anthropicprovider.Provider` | `pkg/providers/anthropic/provider.go:118` | yes |
| `*providers.HTTPProvider` | `pkg/providers/http_provider.go:51` | yes |
| **`*providers.ClaudeProvider`** | **`pkg/providers/claude_provider.go:64`** | **no** |

`ClaudeProvider.ChatStream` was added by commit `45b01b14` — *"fix(providers): forward ChatStream on
ClaudeProvider"* — the fix A2-3 records as its own discovery. The spec was amended by `e37f9389`, the
very next commit, and did not re-derive the count the amendment exists to correct.

It is not a bookkeeping error. It is load-bearing four ways:

- **§10.6 item 1** — *"`ChatStream` carries the progress parameter and **all three implementers**
  accept it"* — is a **completion criterion that is false**. An implementer who satisfies it literally
  has left `claude_provider.go:64` and its forwarding call at `:72` on the old signature.
- **§2.1's symbol table**, the spec's map of what W1 touches, has no `ClaudeProvider` row. §2.2 row 1
  says "3 in-tree implementers + 1 caller".
- **`streaming_compliance_test.go` is FROZEN in §2.3** ("must pass without edits"). It now asserts
  `_ StreamingProvider = (*ClaudeProvider)(nil)` at :35. It passes without edits only if **four**
  implementers are migrated. §2.3 and §10.6 disagree about how many that is.
- **ADR-059 wins on disagreement** (spec header) and says three in §3, §4 and W6. So the count the
  implementer is bound to is the wrong one, and correcting the spec alone does not fix it.

**Impact.** The identical failure mode as C2-02, one iteration later: pass #1 said two, A1 said two,
A2 corrected to three, HEAD is four. Each correction was published as "verified by enumeration". The
enumeration is cheap and reproducible; what keeps failing is re-running it after the tree changes.

**Fix.**
1. Add a `ClaudeProvider.ChatStream` row to §2.1 (`pkg/providers/claude_provider.go:64`, role: "W1:
   implementer (forwarder — the type the factory returns on the Anthropic OAuth/token path,
   `factory_provider.go:32`)").
2. Change every "three" to "four" in §2.2 row 1, A1-4, A2-2 and §10.6 item 1.
3. **Amend ADR-059 §3, §4 and §6 W1 in the same commit**, since the ADR is authoritative. Cite the
   commit in both documents.
4. Add a standing line to §10.6: *"the implementer count is re-derived by grep at implementation
   time, not read from this document"* — three passes of stale counts earn it.

---

### C3-02 — §6's "known precedent" is inverted: the SPA *does* special-case ADR-058's payload, with a bespoke component, and the precedent does not apply to W5's landing field

- **Lens:** Incorrectness
- **Affected:** §6 closing paragraph, §6 item 3, US-5, FR-008, ADR-059 Q1

§6 currently reads:

> **Known precedent, to be confirmed rather than assumed:** ADR-058 already ships structured JSON
> inside denial text today, and no SPA component appears to special-case it — suggesting such payloads
> already render as text. That is a lead, not an answer.

Both halves are false, and the second is false in the direction that matters.

- `GenericToolCall.tsx:76-90` declares `isDelegationFailure`, which tests
  `(value as Record<string, unknown>)['error'] === 'delegation_denied'` **on the result object**.
- `:505-507` renders the match through `DelegationFailureDisplay`, under the comment *"Structured
  delegation-denied sentinel — render a distinct, human-readable block instead of a raw JSON blob."*
- The component is significant enough that its branch was **ported into a sibling** —
  `ToolCallBadge.test.tsx:51-69`, *"delegation-denied branch (gate 3) — ported from
  GenericToolCall.tsx"*.

So the precedent is: **when ADR-058 shipped a structured payload into a surface a human reads, the SPA
needed a purpose-built renderer to stop it looking like a blob.** §6 tells the implementer the
opposite — that payloads already render as text and W5 is probably free.

Compounding it: the ADR-058 payload lives in `result` (`type: object`,
`additionalProperties: true`, `contracts/components/schemas/ToolCall.yaml:69-72`). W5's discriminator,
emitted through `ErrorResult` at `pkg/tools/filesystem.go:830`, lands in `ToolCall.error` — a plain
string, rendered **verbatim and unconditionally** at `GenericToolCall.tsx:523-524`:

```tsx
{error && (
  <div className="text-[var(--color-error)] text-[10px] font-sans">{error}</div>
)}
```

No sentinel detection, no formatting. A JSON payload placed there renders as JSON. The precedent §6
cites is real but sits on the *other* field, and moving W5 onto that field is exactly pass #2's
unadopted "shape B".

**Impact.** SC-005 lets W5 merge once §6 is "answered". An implementer answering it from §6's own lead
concludes "precedent says it renders as text, ship it" and ships a JSON blob into the replay surface.
The gate does not merely fail to help — it argues for the wrong answer.

**Fix.** Replace the paragraph with the verified finding: *"ADR-058's payload is carried in
`ToolCall.result` (object, `additionalProperties: true`) and is special-cased by
`GenericToolCall.tsx:76-90` → `DelegationFailureDisplay` (:505-507). `ToolCall.error` (string) has no
such handling and is rendered verbatim at :523-524. The precedent therefore argues that a structured
payload in a human-visible surface needs either the result field or a renderer — not that it is
free."* Then pin the decision: shape B (`ToolCall.result`, no contract change, discriminator out of
the prose) or shape A plus a `write_file` renderer, with the frontend work named.

---

### C3-03 — The gate covers the replay path only: `write_file` is a registered tool UI, so live chat renders `FileWriteConfirm` and shows the human no refusal text at all

- **Lens:** Incompleteness / Incorrectness
- **Affected:** §6 item 3, §2.2 row 4, US-5 AC-1, FR-008, ADR-059 R5/Q1

A1-3's correction was half a correction. It established, correctly, that `FileWriteConfirm.tsx` never
reads `result` — so it cannot show a blob. It concluded that `GenericToolCall.tsx` is therefore the
component to gate on. The dispatch says both are, on different paths:

| Path | Renderer | Source | Shows refusal text? |
|---|---|---|---|
| **Live chat** | `FileWriteConfirm` | registered by name at `FileWriteConfirm.tsx:91`, mounted `OmnipusRuntimeProvider.tsx:313`; registered names win, `GenericToolCall` is only `Fallback` (`ChatScreen.tsx:303-320`, `:1396-1400`) | **No — nothing at all** |
| **Replay / historical** | `GenericToolCall` | `ChatScreen.tsx:983-993` renders every stored call through it, `error={tc.error}` | **Yes — verbatim (:523-524)** |

`FileWriteConfirm`'s entire live output for a refusal is
`{status dot} write_file  a.svg  1.2 KB  Failed` — status word from
`getToolBadgeStatusConfig`, `isError={status.type === 'incomplete'}`. The sentence
*"file: a.svg already exists. Set overwrite=true to replace."* is **never displayed live**.

Three consequences the spec states or implies the opposite of:

1. **US-5 AC-1 is already false.** *"Given the changed refusal text, When it renders in the chat
   surface, Then a human sees a readable message"* — on the live path a human sees a status word.
   The AC cannot pass on the surface it names, before or after W5.
2. **FR-008 is vacuous where the spec puts the risk.** *"A refusal MUST remain human-readable where it
   is displayed"* is trivially satisfied live (it isn't displayed) and load-bearing only on replay.
3. **The real W5 risk is live/replay divergence, and the spec never names it.** Live would keep
   showing "Failed"; replay would start showing a JSON blob — in a codebase that maintains this parity
   deliberately (`ChatScreen.tsx:236-238`, *"Mirrors MessageItem.tsx's cap (live render path) so live
   and replay show the same…"*, and the whole of `GenericToolCall.replay-humanize.test.tsx`, a
   regression test born from exactly this class of divergence).

**Impact.** §6 item 3 asks *"does `GenericToolCall.tsx` render the refusal as a sentence today"* — on
replay, yes. Answering it truthfully still leaves the live path unexamined, and the live path is where
the spec's only P0 behavioural story (US-5) is aimed. The gate can be passed while the product
question is unasked.

**Fix.** Rewrite §6 item 3 as two questions, one per path, with the dispatch rule stated so the next
reader does not re-derive it. Correct §2.2 row 4 to list **both** components with their paths — the
current parenthetical (*"`FileWriteConfirm.tsx` never reads the result"*) is true and now
actively misleading, because it reads as "not a dependent" when the correct reading is "shows the
human nothing, which is its own finding". Re-scope US-5 AC-1 to the replay surface, or restate it as
*"neither surface regresses, and the live surface's existing silence is recorded as a separate
defect"*.

---

### C3-04 — §9 still contradicts §2.3, SC-002 and TDD #1 on the one test all three describe, and still carries a row for the struck FR-009

- **Lens:** Inconsistency
- **Affected:** §9 rows 2 and 9, §2.3 REWRITTEN, SC-002, §10.7 #1

§9 :424, verbatim at `e37f9389`:

| FR-003 | US-1 | Hook replacing options cannot silence progress | `toolcall_progress_wiring_test.go` (existing, **must pass unchanged**) |

Against the same document:

- **§2.3** classifies that exact file as **REWRITTEN**, because it *"stubs `ChatStream` (W1) and
  asserts through `ToolCallProgressFromOptions` (W2 deletes it)"*.
- **SC-002** (:413, newly restated by A2) requires FROZEN tests to pass unchanged and REWRITTEN tests
  to carry their named replacement — so §9 row 2 asserts a property SC-002 explicitly does not.
- **§10.7 #1** (:486) calls it *"Rewrite of the wiring test; replaces the options-map assertion"*.

Verified in source: the file cannot pass unchanged. `toolcall_progress_wiring_test.go:63` declares a
stub `ChatStream` whose signature W1 changes, and `:145` / `:219` call
`protocoltypes.ToolCallProgressFromOptions`, which W2 deletes.

And §9 :431 still reads `FR-009 | US-2 | — | SC-002` — a matrix row for a requirement struck through
three sections earlier, pointing at a criterion that was rewritten specifically to stop depending on
it.

**Impact.** This is the third pass on the same cell. Pass #1 raised it as C-01, pass #2 as C2-04, and
A2 fixed the two things pass #2 named (FR-009's text, SC-002's wording) while leaving the two §9 rows
pass #2 also named. An implementer reading §9 — the section whose job is to tell them what to run —
is told to leave a file alone that will not compile.

**Fix.** §9 row 2's Test cell becomes *"`toolcall_progress_wiring_test.go` — REWRITTEN per §2.3;
asserts the stub receives a non-nil `onProgress` parameter"*. Delete §9 row 9 outright; FR-009 is
withdrawn and a struck requirement needs no trace. Then add a one-line consistency rule to §10.6:
*"§2.3, §8, §9 and §10.7 name the same test files; a change to one requires a change to all four."*

---

## MAJOR

### M3-01 — The definition of done cannot detect the three gaps §9.1 exists to expose

- **Lens:** Infeasibility
- **Affected:** §10.6 item 5, §9.1, §10.7 #2/#3/#4/#5

§10.6 item 5: *"ADR-059's AC-01…AC-07 are **each mapped in §9** to a test, or carry an explicit
written waiver."*

§9.1 already maps all seven. Every row has a populated "Covered by" cell — including the three the
same table flags as defects: AC-03 → "TDD #3" / **GAP — no Anthropic test exists**; AC-05 → "TDD #4" /
**PARTIAL**; AC-06 → "TDD #5" / **GAP — no test, no waiver**. The criterion is satisfied by the table's
existence, today, with zero tests written. It measures the document, not the tree.

Worse for AC-02: §9.1 maps it to TDD #2 and annotates *"after W1 it cannot fail"*, and §10.7 #2 says
*"Do not present it as coverage."* So the spec maps an ADR acceptance criterion to a test it declares
non-coverage, and the DoD accepts the mapping.

Verified that all three gaps are real at `e37f9389`: `pkg/providers/anthropic/` contains
`ToolCallProgress` in `provider.go` only, no test file; `toolcall_progress_race_test.go:33` is
`ts := &turnState{}`, one turn; no panic test exists anywhere.

**Fix.** Restate item 5 as *"TDD #3, #4 and #5 exist, exercise a production caller, and are green — or
each carries a written waiver naming the risk accepted and the person accepting it."* Add FR-010
(per-provider emission), FR-011 (cross-turn isolation) and FR-012 (handler panic containment) to §7 so
the gaps have normative force, with §9 rows. For AC-02, adopt pass #2's structural resolution: assert
the `ChatStream` call site passes non-nil `onProgress` **while** the `BeforeLLM` block returns
`HookActionModify` with an empty `Options` map — which pins ordering and can fail if someone re-routes
progress through options later — and say in one line that this is what AC-02 means post-W1.

### M3-02 — The compile-break sites are still a number, not a list, and the number is now stale in the other direction

- **Lens:** Incompleteness / Infeasibility
- **Affected:** A1-2, §2.2 row 1, §2.3 closing line, SC-004

§2.3 still closes *"Anything not listed in either table must be discovered by compiling, not by
assumption — A1-2 puts that at roughly 13 sites, two in `pkg/gateway`."* No list. Pass #2 supplied one
and it was not adopted.

Re-derived at `e37f9389` — and `claude_provider.go` adds two sites nobody has counted:

| File:line | Kind |
|---|---|
| `pkg/providers/types.go:44` | interface declaration |
| `pkg/providers/openai_compat/provider.go:206` (+ `:259` options accessor) | emitter |
| `pkg/providers/anthropic/provider.go:118` (+ `:144` accessor) | emitter |
| `pkg/providers/http_provider.go:51` + `:59` | forwarder decl **and** call |
| **`pkg/providers/claude_provider.go:64` + `:72`** | **forwarder decl and call — new, uncounted** |
| `pkg/agent/loop.go:8412` (+ `:8288` injection W2 deletes) | production call site |
| `pkg/agent/toolcall_progress_wiring_test.go:63` | stub |
| `pkg/agent/async_result_persistence_test.go:320` | stub |
| `pkg/gateway/cancel_transcript_order_test.go:56` | stub — **CI only** |
| `pkg/gateway/websocket_multisession_test.go:56` | stub — **CI only** |
| `pkg/providers/openai_compat/chatstream_usage_test.go:50, 134, 204, 213` | 4 positional calls |
| `pkg/providers/openai_compat/cache_tokens_test.go:85, 162` | 2 positional calls |
| `pkg/providers/anthropic/provider_test.go:265` | streaming round-trip |
| `pkg/providers/streaming_compliance_test.go:34-38` | four compile assertions |

The instruction remains unexecutable as written: `CLAUDE.md` forbids building or running the
`pkg/gateway` test binary in this environment, and two break sites are there. SC-004 budgets one green
run at the final commit, not the discovery round-trips.

**Fix.** Put the table in §2.2 as a "mechanically edited, no behaviour change" row. Add the criterion
pass #1 drafted: *"every stub `ChatStream` in the tree accepts and ignores `onProgress`; a stub that
silently drops it makes the wiring test vacuous."* State in §10.6 that W1 is expected to need at least
one `runci.sh <ref> go-build` round-trip before review.

### M3-03 — A1-5's binding prefix rule still has not reached FR-007, and the truncation edge case still has no scenario

- **Lens:** Incompleteness
- **Affected:** A1-5, FR-007, §3 edge cases, §5, §9

A1-5 is BINDING and says the structured payload *"MUST begin the string … so truncation can never
remove it."* FR-007 (:401-402) is unchanged: *"A precondition refusal MUST be machine-distinguishable
from an I/O failure in the result the calling agent reads."* No position, no bound.

The rule survives only in §10.7 #9's Notes column — a test note, not a requirement. A test can be
deleted for being awkward; a requirement cannot.

The hazard re-verified: `pkg/agent/loop.go:10372` is
`tcRecord.Error = truncateRunes(contentForLLM, maxFailClosedOutputChars)`, and pass #2's live-frame
bound (`maxLiveErrorChars = 2000`, `truncateRunes ForFrame` appending `"\n... (truncated…)"`) still
stands. A JSON payload cut mid-string and suffixed is unparseable.

§3 lists *"A very long refusal (the persisted side truncates at 2000 runes)"* as an edge case that
appears in no requirement, no scenario and no matrix row. So does *"A refusal whose path contains
characters requiring escaping"*.

**Fix.** Amend FR-007 in place: *"…and the discriminator MUST occupy the leading runes of the result
text so that neither the persisted (`maxFailClosedOutputChars`) nor the live (`maxLiveErrorChars`)
2000-rune truncation can remove or sever it."* Add the BDD scenario and a §9 row. Add
`config.FilterSensitiveData` (`pkg/agent/session_messaging_wire.go:283`) as a second edge case with
the same mitigation — it is a `strings.Replacer` doing substring substitution and can redact *inside*
a payload, breaking parseability without touching the prefix.

### M3-04 — The five artefacts carrying the refusal sentence are still unlisted, and §6 item 4's answer exists only in amendment prose

- **Lens:** Incompleteness
- **Affected:** A1-8, §2.2 row 4, §6 items 1 and 4, Constraint #8

`grep -rn "already exists. Set overwrite"` at `e37f9389`:

| Artefact | Line |
|---|---|
| `pkg/tools/filesystem.go` | `:830` — the emitter |
| `contracts/components/schemas/ToolCall.yaml` | `:62` — `example:` **is** the refusal sentence |
| `pkg/gateway/inboundschemas/ToolCall.yaml` | `:62` — mirrored copy, same example |
| `src/lib/api/generated/openapi-types.ts` | `:3416` — generated `@example` |
| `pkg/gateway/toolexecend_error_bound_test.go` | `:58` — fixture |

§2.2 row 4 lists none. ADR-059 §7 item 1 names the mirror and the generated file explicitly; §6 item 1
drops both. A1-8's answer — *"`ToolCall.error` is `type: string`, so JSON-inside-a-string always
validates"* — is correct (`ToolCall.yaml:49-50`) and incomplete: if the now-stale `example:` is
refreshed, that touches `contracts/`, and `make verify-contracts` fails on generated drift. A1-8 gives
one half and presents it as the answer, and **neither half is recorded in §6's body**, which still
lists items 1 and 4 as open questions SC-005 requires answering.

**Fix.** Add the five artefacts to §2.2 row 4. Write A1-8's answers **into §6 items 1 and 4** with the
missing half: *"no pipeline run is needed for schema validity; yes if the stale `example:` is
refreshed, and the refresh must ride the same commit per Constraint #8."*

### M3-05 — W2's comment debt is still unlisted: deleting the symbol orphans four live doc references

- **Lens:** Incompleteness
- **Affected:** §2.1, W2, §10.6

`OnToolCallProgressKey`'s doc comment (`pkg/providers/protocoltypes/progress.go:3-19`) is the canonical
narrative of the incident. Four live comments send readers to it, verified at `e37f9389`:

- `pkg/agent/loop.go:8261`
- `pkg/agent/turn.go:417`
- `pkg/tools/delegate.go:333`
- `pkg/providers/openai_compat/provider.go:348`

W2 deletes the target. §2.1 lists neither the comments nor a destination. Raised as m-04 in pass #1
and M2-08 in pass #2; unaddressed in both amendments.

**Fix.** Add a §2.1 row naming the four sites, and state the destination —
`protocoltypes.ToolCallProgress`'s own doc comment is the obvious home, and §1.2's own precedent (put
the standing rule where the next person will read it) argues for it. Add to §10.6: *"no comment
references a deleted symbol."*

### M3-06 — Nothing is tracked: the spec contains no issue reference, and §10.5 says "tracked separately" about an untracked gap

- **Lens:** Inoperability
- **Affected:** §10.5, A1-4 bullet 3, `CLAUDE.md` issue conventions

`grep -n "#[0-9]\{3\}\|issue"` over the spec returns **nothing**. §10.5 defers two items — *"Teaching
`delegate status` to distinguish 'quiet' from 'cannot report' … Real gap, **tracked separately**"* and
*"Wiring `tokens_in` — unrelated, **separately tracked**"* — naming no tracker for either. ADR-059 Q4
already records that no GitHub issue is linked to the ADR at all.

`CLAUDE.md` requires every PR to close its issues by keyword, repeated per issue, in the PR
description. A spec whose deferrals name no issue produces a PR that can close nothing, and the
deferral becomes invisible the moment this branch merges.

**Fix.** File two issues (Type: Task, `area:` labels per the conventions doc) and put the numbers in
§10.5. Put the ADR-059 tracking issue number in both document headers. This is a five-minute action
that has now survived two BLOCK verdicts.

### M3-07 — A1-8's claim that §6 was reduced to one question is still false, and SC-005 still requires all four

- **Lens:** Inconsistency
- **Affected:** A1-8, §6, SC-005

A1-8 (:72-74): *"§6's four gate questions are also reduced to the one that is genuinely open: items 1
and 4 were answerable from the tree."* §6 (:378-385) still lists **four** numbered questions. A2-1
enumerated six A1 claims and repaired them; this seventh claim was not on the list and survives as a
false statement of document state — the residue of pass #2's C2-01.

It matters because SC-005 reads *"W5 is not merged until §6 is answered in an appended amendment"* and
§6's preamble reads *"blocked until **each** of these is answered in writing"*. So the binding text
requires four answers while the binding amendment says one is open, and item 2 (does the live
`tool_call_result` frame change shape?) has **no** answer anywhere in either document.

**Fix.** Either reduce §6 to the open question and record the answers to 1, 2 and 4 inline as
resolved, or delete A1-8's sentence. Answer item 2 in the body — it is a grep, and it is the last
unanswered one.

---

## MINOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **m3-01** | Incorrectness | ADR-059 §4 | The scope limit **is** now recorded (verified, added by `e37f9389`) — but its enumeration says *"`anthropic`, `openai_compat` and `HTTPProvider` implement `ChatStream`"*, which C3-01 refutes. Since the ADR wins on disagreement, the authoritative document now carries the wrong count. | Amend with C3-01's fix, same commit. |
| **m3-02** | Ambiguity | §10.5 | *"Named, accepted, and **to be recorded** in ADR-059 §4"* is stale — it was recorded in the same commit that wrote this line. | Change to "recorded in ADR-059 §4 (`e37f9389`)". |
| **m3-03** | Incompleteness | §2.3 REWRITTEN row 2 | `openai_compat/toolcall_progress_test.go` holds three tests, not two — the nil-callback case (`:120`) is unnamed. It is the **only** cover for §4's *"given no progress callback, the system reports nothing and does not fail."* Pass #2 m2-01, unaddressed. | Name all three; mark the nil-callback case FROZEN. |
| **m3-04** | Incompleteness | §1 table | W6 and W7 still absent, though §6 executes W6 and §2.3 protects W7. Pass #1 m-06, pass #2 m2-03. | Add both rows. |
| **m3-05** | Inconsistency | §3 | US-3 and US-5 still lack the *"Independent test:"* line US-1/US-2/US-4 carry; US-5 is P0. Pass #1 m-01, pass #2 m2-04. | Add both, or drop the convention. |
| **m3-06** | Incompleteness | §5, §9 | Four acceptance criteria still have no BDD scenario: US-2 AC-2, US-3 AC-1, US-4 AC-3 (the persisted-transcript case — this **is** ADR AC-07), US-5 AC-2. Pass #1 m-02, pass #2 m2-05. | Add US-4 AC-3 and US-5 AC-2 at minimum; annotate deliberate `—`s. |
| **m3-07** | Ambiguity | §10 | The ambiguity audit is still unrevised. Row 1 poses W1-vs-`Chat` as open, which §4 and §10.5 now answer. Row 4 is answerable from the tree: `ToolCallProgress` and `OnToolCallProgress` are read by both emitters, so only the key and the accessor go. Rows 2 and 3 defer W5's field names and `IsError` a third time. | Answer rows 1 and 4 in place; fold row 2 into the §6 shape decision (C3-02). |
| **m3-08** | Incompleteness | §1.2 | The standing rule — *"any future removal of a `ToolResult` field after a release is a hook-contract break"* — is still only in this spec. It remains the document's strongest passage and the least likely to be found by the person about to break it. | Promote to a doc comment on `tools.ToolResult` as part of W3's cleanup. |
| **m3-09** | Overcomplexity | §10.7 #6, #7 | Both wrong-level tests survive. #6 (grep for the options key) is redundant — deleting `OnToolCallProgressKey` makes every surviving reference a compile error — and its scenario's *"no matches outside historical records"* is undefined, since the literal appears in ADR-059, in this spec, and in the four comments of M3-05. #7 is a test policing a comment, contradicting §2.2's own "Risk: NONE". Pass #2 M2-09. | Drop both; state that W2 is verified by the compiler plus the FROZEN table and W4 by reviewed diff. |
| **m3-10** | Ambiguity | §2.1 | The table lists `write_file`'s guard as `pkg/tools/filesystem.go` with no line. It is `:830`, and its in-code comment already names W5, ADR-059 D4 and the §7 gate — better contract documentation than §6 currently carries. | Add the line number and reconcile §6 against that comment. |

---

## OBSERVATIONS

| ID | Section | Note |
|---|---|---|
| **O3-01** | A2 / whole doc | **All nine edits A2 claims are physically present**, verified individually against the body at `e37f9389`, not against A2's description of it. §2.2 :180, §6 :381, §4 :293, SC-001 :410, SC-002 :413, FR-009 :404, §9.1 :433, §10.5 :457, §10.6 :468, §10.7 :482, A1-4 :41. Pass #2's central finding is closed and does not recur. The amendment discipline A2-1 adopted — "A1 should be read as the rationale, not as a claim that they exist" — is the right model and should be kept. |
| **O3-02** | A2-3 / `45b01b14` | The `ClaudeProvider` fix is the most valuable thing on this branch and is correctly done: the forwarding method carries a comment explaining *why* a non-embedded delegate promotes nothing, `streaming_compliance_test.go:24-38` now asserts the factory-returned type first, and the rule — *"if a wrapper is what the factory hands the agent loop, the wrapper is what gets asserted here"* — is written where the next person will hit it. That the same commit invalidated the spec's implementer count (C3-01) is a bookkeeping failure, not a criticism of the fix. |

---

## Structural integrity results (plan-spec mode)

| Check | Result | Δ vs pass #2 |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | unchanged |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** | unchanged — 4 uncovered (m3-06) |
| Every BDD scenario has a `Traces to:` | **PASS** | unchanged |
| Every BDD scenario has a corresponding test in the TDD plan | **PARTIAL** | **improved** — §10.7 now exists; scenarios still not mapped row-by-row, and §9's Test column still names no file for 4 of 9 rows |
| Every functional requirement appears in the traceability matrix | **PASS**, with defect | FR-009 is struck yet still holds a §9 row (C3-04) |
| Every BDD scenario appears in the traceability matrix | **PASS** | unchanged |
| Test datasets cover boundaries, edge cases, error scenarios | **FAIL** | unchanged — no datasets section; §3's five edge cases still appear in no requirement, scenario or matrix row (M3-03) |
| Regression impact explicitly addressed | **PARTIAL** | §2.3 remains sound; §2.2 now undercounts implementers as well as break sites (C3-01, M3-02) |
| Success criteria measurable, no subjective language | **PARTIAL** | **improved** — SC-001 and SC-002 fixed; SC-005 still satisfiable against an inverted lead and a half-covered surface (C3-02, C3-03) |
| Definition of done detects the defects the spec names | **FAIL** | **new** — item 1 names the wrong count, item 5 is satisfied by the table's existence (C3-01, M3-01) |
| Amendment consistent with the body it amends | **PASS**, one residue | **improved** — nine of nine A2 edits verified; A1-8's "§6 reduced to one" remains false (M3-07) |

---

## Test coverage assessment

**The production-caller bar is now partly satisfied by accident.** ADR-059 §8's inherited rule — *"a
green test that does not exercise a production caller does not satisfy this ADR"* — is finally
restated, in §10.7's "Stub resistance" paragraph, with the right sharpening (*"Test 3 must exercise
the type the **factory returns**"*). That is a real improvement over both prior passes. But §2.1 does
not list `ClaudeProvider`, so an implementer writing Test 3 has no pointer to the factory-returned
type and will most naturally reach for `anthropicprovider.Provider` — repeating precisely the mistake
the paragraph warns about.

**Now-legitimate coverage.** Pass #2's C2-03 argued an Anthropic emission test could not exercise a
production caller. `45b01b14` changed that: `factory_provider.go:32` returns `ClaudeProvider`, which
forwards to the emitter. TDD #3 is now a valid test. The spec should say so — it currently reads as
though the gap were merely unfilled rather than newly fillable.

**Still missing.** AC-05's cross-turn half (`toolcall_progress_race_test.go:33` is one `turnState`,
and §2.3 freezes the file, locking the gap in). AC-06's panic case (no test, no waiver). The
truncation scenario (M3-03). The negative cases pass #2 named for US-4's Scenario Outline — a path
that exists as a **directory**, a symlink to an existing file, a sandbox denial firing *before* the
existence check — all three must **not** carry the discriminator, and all three are cheap.

**Vacuous by construction.** TDD #2, which the spec itself labels as such and then maps to ADR AC-02
in §9.1 (M3-01).

---

## STRIDE summary

| Component | Threats considered | Assessment |
|---|---|---|
| `ChatStream` `onProgress` parameter | Information disclosure | Unchanged: FR-005 and D2 bound the payload to byte counts. No finding. |
| Progress record on `turnState` | Info disclosure (cross-turn leakage), DoS (hot-path cost) | Cross-turn isolation still asserted and untested (M3-01). Hot-path cost still unmeasured. |
| `write_file` refusal payload | Information disclosure | The path already reaches LLM, transcript, WS frame and replay surface, so W5 adds no new exposure — but `config.FilterSensitiveData` (`session_messaging_wire.go:283`) is a substring `strings.Replacer` that can redact **inside** a JSON payload and break parseability. Still not an edge case (M3-03). |
| `PostToolUse` hook payload | Tampering / contract break | §1.2 unchanged and still the best-handled surface in the document. |
| Provider factory → streaming selection | Availability of the safety signal | **Resolved.** Pass #2's finding that OAuth/token Anthropic installs silently lost streaming is fixed at `claude_provider.go:64`. The spec has not absorbed the consequence (C3-01). |
| SPA render of a structured refusal | Integrity of what the operator reads | **New.** Live and replay diverge: live shows a status word and no reason, replay renders `error` verbatim. W5 would widen that divergence in a codebase that deliberately maintains parity (C3-03). |

No spoofing, elevation-of-privilege or authentication surface is touched by W1–W5.

---

## Unasked questions

1. **Why is W5 still bundled with W1–W4?** Asked in pass #1, asked again in pass #2, unanswered in
   both amendments. W1/W2/W4 are a typed-transport migration with zero behavioural surface; W5 is the
   only behavioural change, is gated on unanswered contract analysis, and shares no code with the
   others. Three grills have now been spent partly on W5's gate while W1's implementer count went
   stale twice. Splitting would let W1/W2/W4 ship.
2. **Does the live path's silence get fixed?** C3-03 establishes that a human watching chat sees no
   refusal reason at all today. That is arguably a larger defect than the one W5 addresses, it is
   adjacent to the incident narrative (a worker's outcome being unreadable), and no document names
   it.
3. **What does W1 revert to if AC-03 fails in UAT?** Asked in both prior passes. Still unanswered.
   W2 deletes the fallback, so reverting W1 alone leaves a tree that does not compile.
4. **Does anything teach the orchestrator to read the progress line?** Asked twice. §10.5 now names
   the hazard — an orchestrator on a non-reporting install still sees silence — and defers it with no
   tracker (M3-06).
5. **What re-derives the implementer count next time?** Two → three → four across three passes. The
   count is one `grep` and it has been wrong in every version of this document. Nothing in the process
   catches it; `streaming_compliance_test.go` would, but only after the migration is already written.

---

## Next action

**Verdict: BLOCK** — 4 CRITICAL.

This is a materially better document than pass #2 reviewed. The amendment now tells the truth about
itself, the missing house sections are real and substantive, and the production bug the last grill
surfaced is fixed properly. The blockers are narrower and every one of them is a factual correction
rather than a rewrite.

Address in this order:

1. **C3-01** — four implementers. Fix §2.1, §2.2, A1-4/A2-2, §10.6 item 1 **and ADR-059 §3/§4/§6 W1**
   in one commit. Nothing about W1 is safe to start until the target list is right.
2. **C3-04** — §9 rows 2 and 9. Two cells, third time of asking.
3. **C3-02 + C3-03** — rewrite §6's precedent paragraph against the verified render paths, split item
   3 into live and replay, and pin the payload shape now (default to `ToolCall.result`,
   `additionalProperties: true`, no contract change).
4. **M3-01** — make §10.6 item 5 measure the tree, not the table; add FR-010/011/012 or written
   waivers.
5. **M3-02, M3-03, M3-04, M3-05** — adopt the enumerations in this report verbatim; they are
   re-derived at `e37f9389` and need no further verification.
6. **M3-06, M3-07** — file the two issues, and reconcile §6's question count with A1-8.
7. Then the MINORs. m3-01 through m3-07 are corrections, not additions.

Re-run after revision:

```
/grill-spec docs/internal/specs/adr-059-delegation-observability-spec.md
```

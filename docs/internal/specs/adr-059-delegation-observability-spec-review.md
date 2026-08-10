# Spec review — ADR-059 work items W1–W5 (adversarial grill, pass #1)

- **Reviewed:** `docs/internal/specs/adr-059-delegation-observability-spec.md` (Draft, 2026-08-10, 336 lines)
- **Branch:** `fix/uat-delegation-rootcauses` @ `febf21e2` (spec file untracked; tree otherwise clean)
- **Authority:** [ADR-059](../architecture/ADR-059-delegation-observability.md) (Accepted) and its committed review; [ADR-058](../architecture/ADR-058-tool-denial-semantics.md) §3/§7
- **Reviewer mode:** `plan-spec` (FR-/SC- ids, BDD block, traceability matrix all present)
- **Structural comparators:** `docs/internal/specs/adr-058-tool-denial-semantics-spec.md` (the sibling ADR-implementing spec) and `docs/internal/specs/workspace-heartbeat-memory-config-spec.md` (post-grill amendment convention)
- **Date:** 2026-08-10
- **Verification:** every `file:line` and symbol below was read on this branch in this session and can be re-run.

---

## Executive summary

Four CRITICAL, eight MAJOR, ten MINOR, three OBSERVATION.

The spec's diagnosis of its own risk profile is right — this is a migration, and regression is the
dominant hazard — but the artefact that carries that framing, §2.3's "must pass unchanged" list, is
**arithmetically impossible**: two of its six entries cannot survive W1/W2, because one of them stubs
`ChatStream` and calls the very function W2 deletes. FR-009 and SC-002 therefore block FR-001 and
FR-004. Separately, the §6 gate that guards the only behavioural change points at a React component
that renders no result text at all, so the gate as written would clear W5 on a false premise. And the
spec's §4 behavioural contract is stated unconditionally while being false for most in-tree provider
configurations — a coverage boundary the spec touches once, in an ambiguity table, and never scopes.

The §2.1 symbol table, by contrast, is accurate line for line, and §1.2's hook-contract rule is the
strongest passage in the document.

| Severity | Count |
|---|---|
| CRITICAL | 4 |
| MAJOR | 8 |
| MINOR | 10 |
| OBSERVATION | 3 |
| **Total** | **25** |

**Verdict: BLOCK.**

---

## CRITICAL

### C-01 — §2.3's "must pass unchanged" list is self-contradictory; FR-009 blocks FR-001/FR-004

- **Lens:** Infeasibility / Inconsistency
- **Affected:** §2.3, FR-009, SC-002, §9 row 2

**Two of the six listed tests cannot survive the migration they are supposed to police.**

`pkg/agent/toolcall_progress_wiring_test.go` (listed as *"the producer exists and survives `BeforeLLM`"*):

- defines `progressCapturingStreamProvider.ChatStream` at `:63-72` with the **current** six-parameter
  signature and `var _ providers.StreamingProvider = (*progressCapturingStreamProvider)(nil)` at
  `:74` — W1 breaks its compile;
- asserts through `protocoltypes.ToolCallProgressFromOptions(opts)` at **`:145` and `:219`** — the
  function W2 **deletes**, so after W2 the file does not compile at all;
- its entire premise (*"loop.go actually wires the callback into the options map"*, `:17-18`) is the
  mechanism W1/W2 abolish.

`pkg/providers/openai_compat/toolcall_progress_test.go` (listed as *"tool-args-only stream emits
progress"*) contains a **second** test, `TestToolCallProgressFromOptions` at `:133-155`, exercising
`ToolCallProgressFromOptions` and `OnToolCallProgressKey` directly. §2.3 names the **file**, not the
test, so the file also cannot pass unchanged; that test must be deleted with W2.

FR-009 — *"No test listed in §2.3 may change to accommodate the migration"* — and SC-002 —
*"Every test in §2.3 passes unchanged after W1 and W2"* — are therefore unsatisfiable simultaneously
with FR-001 and FR-004. §9 compounds it by mapping FR-003 to *"`toolcall_progress_wiring_test.go`
(existing, must pass unchanged)"*.

**Impact:** an implementer following the spec literally cannot land W1 or W2. An implementer who
ignores FR-009 to make progress has lost the spec's only regression control, silently.

**Fix.** Split §2.3 into two lists, and make the distinction the load-bearing one:

- **Frozen (behaviour pinned; any diff is a defect):** `toolcall_progress_race_test.go`,
  `toolcall_progress_lifetime_test.go`, `delegate_toolcall_progress_test.go`,
  `streaming_compliance_test.go`, and `TestParseStreamResponse_EmitsProgressForToolCallArguments`
  (the single test in `openai_compat/toolcall_progress_test.go` that survives).
- **Must be rewritten, with the replacement assertion named in advance:**
  `toolcall_progress_wiring_test.go` — its post-W1 job is *"a real turn passes a non-nil `onProgress`
  argument to `ChatStream`"*, asserted on a captured parameter rather than a captured map; and
  `TestToolCallProgressFromOptions` — deleted, as its subject is deleted.

Restate FR-009 as: *"The behaviour asserted by each frozen test in §2.3 must hold unchanged; the two
rewritten tests must assert the same property through the new transport, and the rewrite must be in
the same commit as W1/W2."*

---

### C-02 — The impact assessment undercounts the compile break by at least six files, two of them in `pkg/gateway`

- **Lens:** Incompleteness
- **Affected:** §2.2 row 1 (*"3 in-tree implementers + 1 caller, all compile-enforced"*)

Verified on this branch, every one of these breaks the moment `ChatStream`'s signature changes, and
**none appears anywhere in the spec**:

| File | Why it breaks |
|---|---|
| `pkg/agent/toolcall_progress_wiring_test.go:63` | stub `ChatStream` (also C-01) |
| `pkg/agent/async_result_persistence_test.go:320` | stub `ChatStream` + `var _ StreamingProvider` at `:328` |
| `pkg/gateway/cancel_transcript_order_test.go:56` | stub `ChatStream` |
| `pkg/gateway/websocket_multisession_test.go:56` | stub `ChatStream` |
| `pkg/providers/openai_compat/chatstream_usage_test.go:50, :134, :204, :213` | positional call sites |
| `pkg/providers/openai_compat/cache_tokens_test.go:85, :162` | positional call sites |
| `pkg/providers/anthropic/provider_test.go:265` | streaming round-trip test |

Two of them are in `pkg/gateway`. Per `CLAUDE.md`'s "Testing & building — CI is the authority", the
gateway test binary must not be built or run in the dev pod; W1 therefore **forces** a CI-worker
round-trip that the spec's §8 does not plan for beyond a bare SC-004.

**Impact:** the spec presents W1 as a four-site change with a six-test safety net. It is a
thirteen-site change, and the person doing it will discover that from a compiler error rather than
from the spec — exactly the "migration regressed something" failure §1 says is the dominant risk.

**Fix.** Add the seven files to §2.2 as a *"mechanically edited, no behaviour change"* row, and add a
success criterion: *"SC-00x: after W1, `go build` and `go vet` are clean with the tags
`goolm,stdjson`, and every stub `ChatStream` in the tree accepts and ignores `onProgress` — no stub
may silently drop it, since a stub that drops it makes the wiring test above vacuous."* Route
verification through the CI worker gates named in `deploy/ci-worker/CLAUDE.md` (`go-build`, then
`go-test`), not locally.

---

### C-03 — The W5 gate's decisive question targets a component that renders no result text

- **Lens:** Incorrectness / Infeasibility
- **Affected:** §6 item 3, §2.2 row 4, US-5, FR-008, SC-005

§6 item 3 is flagged by the spec itself as *"the user-visible question and the most likely reason to
choose a different shape"*:

> **Does the SPA's `FileWriteConfirm.tsx` render the refusal as a sentence today, and would it show a
> JSON blob after the change?**

`src/components/chat/tools/FileWriteConfirm.tsx` renders **`args.path`, a byte count of
`args.content`, and a status label** (`FileOpBlock`, `:42-76`; `makeWriteFileUI`, `:78-92`). Its
`makeAssistantToolUI` result type is `unknown` and the result is never read. It shows the word
`Failed` and nothing else. It **cannot** render a sentence today and **cannot** render a JSON blob
tomorrow.

So the gate, answered honestly against the component it names, returns "no user-visible change" — and
W5 ships on that clearance while the surfaces that *do* carry the text go unexamined:

- `src/components/chat/tools/GenericToolCall.tsx:523-524` renders `error` verbatim
  (`<div …>{error}</div>`) — this is the generic path, and whether `write_file` reaches it in replay
  vs. live is the question that actually needed asking;
- the persisted `ToolCall.error`, whose contract example (`contracts/components/schemas/ToolCall.yaml:62`)
  is **literally the current refusal sentence**, `"file: a.svg already exists. Set overwrite=true to replace."`;
- the calling agent's own narration, which is the only surface a delegation denial reaches at all
  (per `CLAUDE.md`'s tool-visibility rules).

§2.2 repeats the error, listing *"the SPA's `FileWriteConfirm.tsx`"* as a direct dependent of the
`write_file` result text. It is not a dependent at all.

**Impact:** the one gate protecting the one behavioural change in this spec is aimed at the wrong
target and produces a false all-clear. That is worse than having no gate, because SC-005 will be
recorded as satisfied.

**Fix.** Rewrite §6 item 3 as: *"Enumerate every surface that renders a `write_file` failure's text —
live WS `tool_call_result`, replayed transcript, `GenericToolCall.tsx`'s `error` branch, the
ActivityPanel, and `delegate status` / `inspect_session` output — and state, per surface, what a
human sees before and after."* Name `src/components/chat/tools/FileWriteConfirm.tsx` in §2.2 as
explicitly **not** a dependent, with the reason, so the next reader does not re-derive it.

---

### C-04 — §4's behavioural contract is false for most in-tree provider configurations, and the spec never scopes it

- **Lens:** Incorrectness / Incompleteness
- **Affected:** §4 bullet 1, §10 ambiguity 1, FR-002, holdout scenario 4

§4 states, without qualification:

> When a provider streams tool-call arguments, the system reports forward progress to the caller.

Two independent holes make that false, and the spec names only half of one of them.

**(a) Fallback candidates bypass streaming entirely.** Confirmed at `pkg/agent/loop.go:8358-8377`:
when `len(activeCandidates) > 1 && al.fallback != nil`, the closure calls `p.Chat(...)` and
**returns** at `:8375` / `:8392` — the `activeProvider.(providers.StreamingProvider)` branch at
`:8396` is never reached. §10 flags this correctly.

**(b) Seven of the ten in-tree `LLMProvider` implementations have no `ChatStream` at all.** Only
three exist tree-wide (`openai_compat/provider.go:206`, `anthropic/provider.go:118`,
`http_provider.go:51`). `pkg/providers/azure`, `pkg/providers/bedrock`,
`pkg/providers/anthropic_messages`, `antigravity_provider.go`, `claude_provider.go`,
`codex_provider.go` and the two CLI providers implement `Chat` only, and none embeds a streaming
provider (structs verified: `azure/provider.go:34-38`, `anthropic_messages/provider.go:42-46`,
`bedrock/provider_bedrock.go:44-48` — all plain fields, no promotion). Bedrock is a **first-class**
provider per ADR-053. The spec never mentions this.

**Impact — and why this is CRITICAL, not MAJOR.** The product outcome of ADR-059 is that an
orchestrator learns to read a progress line and stops killing quiet workers. On a Bedrock, Azure or
fallback-configured install, the progress line never appears — and an orchestrator taught "silence
means hung" now has *more* confidence in exactly the inference that caused the original incident.
The spec ships a signal that is absent on a large share of installs while stating a contract that
says it is present.

**Fix.** Add an **§Out of scope / known coverage limits** section (the spec has none; both
comparators do) stating, in the reader's terms:

1. Progress exists only on the streaming path; an agent with more than one fallback candidate takes
   the non-streaming `Chat` path (`loop.go:8358`) and gets no progress *and no text streaming*.
2. Progress exists only for the three providers implementing `ChatStream`. Named list, with the
   consequence spelled out for Bedrock/Azure/`anthropic_messages`.
3. External-CLI (`Is3P`) children never reach the progress path at all —
   `delegateStatusExtra` returns `delegate3PStatusNote` at `pkg/tools/delegate.go:2561-2563` before
   the reader is consulted. (This is the ADR review's unasked question 2, still unanswered.)

Then qualify §4 bullet 1 to *"When a **streaming** provider streams tool-call arguments **on a
single-candidate turn**, …"*, and file one tracked issue for the gap so §3's "Positive" claim in the
ADR is not read as a completed outcome.

---

## MAJOR

### M-01 — SC-001 and US-1's "independent test" are not tests

- **Lens:** Infeasibility
- **Affected:** SC-001, US-1 *"Independent test: delete the `onProgress` parameter from one implementer; the build must fail."*

Nothing in CI deletes a parameter. This is a restatement of Go's type system, and the ADR-059 review
already ruled on this class for ADR-058's *"must fail on pre-fix code"*: it is a **review
obligation**, not a CI-enforceable assertion. As a "success criterion" it is unfalsifiable — it will
be marked satisfied by inspection every time.

Note the honest half is still worth keeping: ADR-059 D1's own correction records that
`streaming_compliance_test.go`'s `var _ StreamingProvider = …` block lives in a **test** file and is
therefore *not* a `make build` error. A parameter on the interface **is** — which is D1's real
argument.

**Fix.** Demote SC-001 to a review obligation under a named heading, and replace it with something a
gate can run: *"SC-001: `pkg/providers` declares the three production compliance assertions in a
non-test file, so a non-implementing provider fails `make build`, not `make test`."* If that is
rejected, say so and record SC-001 as a review obligation explicitly.

### M-02 — The ADR's acceptance criteria are never mapped; three are silently dropped

- **Lens:** Incompleteness
- **Affected:** whole spec vs. ADR-059 §8 (AC-01…AC-07)

The spec claims to deliver W1–W5 of an Accepted ADR, and never once cites that ADR's ACs. Checked
individually against the tree:

| ADR AC | Covered by the spec? | Reality on this branch |
|---|---|---|
| AC-01 loop supplies non-nil handler | Yes, via §2.3 (rewritten — C-01) | `toolcall_progress_wiring_test.go` |
| AC-02 survives `BeforeLLM` | Yes, but becomes vacuous — see M-04 | ditto |
| AC-03 **per implementing provider**, >1 event, strictly increasing | **No** | **No Anthropic progress test exists** — see M-03 |
| AC-04 `delegate status` shows progressing vs idle | Assumed delivered | `delegate_toolcall_progress_test.go` ✓ |
| AC-05 race-free **including two concurrent sub-turns on the same target agent** | **Half** | `toolcall_progress_race_test.go:32` drives 8 writers against **one** `turnState`; the cross-turn case — the exact hazard review finding C-02 raised, and the reason D1 was rewritten — is untested |
| AC-06 panicking handler does not kill the turn, **or** propagation documented as deliberate | **No** | no such test anywhere; no such statement in the spec |
| AC-07 discriminator survives into the persisted transcript | Partly | US-4 AC-3 has no BDD scenario and no test row |

§2.3 then **freezes** the incomplete race test as "must pass unchanged", locking AC-05's gap in.

**Fix.** Add a §"ADR acceptance criteria — inherited, satisfied, or deferred" table with one row per
AC-01…AC-07, each naming the test file that discharges it or the reason it is deferred with a tracked
issue. Add the two missing tests: a cross-turn progress-isolation test (two `turnState`s under two
sub-turns on one agent instance, asserting neither observes the other's bytes) and either an AC-06
panic test or one paragraph documenting the propagation behaviour as deliberate.

### M-03 — FR-002 has no Anthropic emission test, and §9 papers over it

- **Lens:** Incompleteness / Infeasibility
- **Affected:** FR-002, §2.3, §9 row 1

FR-002: *"All in-tree streaming providers MUST accept it and emit progress on argument deltas."*
§2.3 lists only `pkg/providers/openai_compat/toolcall_progress_test.go`. `grep -rn ToolCallProgress
pkg/providers/anthropic/` returns **production code only** (`provider.go:144, :167, :233`) — there is
no test, in `provider_test.go` or anywhere else, that the Anthropic emitter produces more than one
progress event with increasing `ArgsBytes`.

§9's FR-002 row offers *"provider progress tests (§2.3) + new per-provider parameter test"*. A
*parameter* test proves the signature compiles. It does not prove emission — which is precisely the
failure `streaming_compliance_test.go`'s own comment and ADR-059 D1's correction warn about: the
Anthropic emitter *"was green against an interface-compliance test"* while emitting nothing.

**Fix.** Add `pkg/providers/anthropic/toolcall_progress_test.go` to the deliverables, mirroring
`openai_compat`'s shape (a synthetic content-block stream carrying tool-input deltas, asserting
`len(progress) > 1` and strictly increasing `ArgsBytes`), and replace §9's FR-002 test cell with the
two real file names. This is ADR AC-03 and it is not optional.

### M-04 — The `BeforeLLM` scenario becomes a test that cannot fail

- **Lens:** Infeasibility
- **Affected:** US-1 AC-3, FR-003, BDD *"A hook replacing request options cannot silence progress"*, §9 row 2

After W1, `onProgress` is a parameter. Nothing in the loop derives it from `llmOpts`, so no hook can
touch it — by construction. The scenario has no failure mode; it is a green assertion that proves
nothing, the exact class ADR-058 §8's inherited bar exists to exclude (*"a green test that does not
exercise a production caller does not satisfy this ADR"*).

The spec half-notices this — §1.1 lists hook-replacement as one of four costs a parameter "removes at
once" — and then keeps FR-003 as a live requirement with a test row anyway.

**Fix.** Pick one, explicitly: (a) retire FR-003 with a one-line note that W1 satisfies it
structurally and delete the scenario; or (b) keep it as a **structural** assertion — e.g. a test
that the `ChatStream` call site passes a non-nil `onProgress` *while* a hook returns
`HookActionModify` with a nil `Options` map, which at least pins the call-site ordering. Do not keep
a behavioural scenario whose failure is unreachable.

### M-05 — §2.2's W5 dependency list omits the contract artefacts, which changes §6 item 4's answer

- **Lens:** Incompleteness / Incorrectness
- **Affected:** §2.2 row 4, §6 items 1 and 4

Verified dependents of the `write_file` refusal string that the spec does not list:

| Artefact | Why it matters |
|---|---|
| `contracts/components/schemas/ToolCall.yaml:62` | its `example:` **is** the current sentence, verbatim |
| `pkg/gateway/inboundschemas/ToolCall.yaml:62` | mirrored copy, same example |
| `src/lib/api/generated/openapi-types.ts:3416` | generated `@example` — regenerated only by the pipeline |
| `pkg/gateway/toolexecend_error_bound_test.go:58` | uses the sentence as a short-error fixture (passes either way, but must be re-read) |

This settles two of §6's four questions and re-frames a third:

- **Item 1** asks whether the text *"still validates"* against a schema with
  `additionalProperties: false`. `ToolCall.error` is `type: string` (`ToolCall.yaml:53-63`) —
  JSON-inside-a-string always validates. `additionalProperties: false` bites when you add a **field**,
  which W5 does not. The question as posed mis-frames the risk and invites a reassuring answer to the
  wrong question.
- **Item 4** ("is the 5-step pipeline required?") has a two-part answer the spec should state:
  **no** for schema validity, **yes** if the now-stale `example:` is refreshed — because that touches
  `contracts/` and `make verify-contracts` fails on generated drift.

**Fix.** Add the four artefacts to §2.2. Replace §6 item 1 with *"`ToolCall.error` is `type: string`,
so structured text validates without a schema change — confirm no consumer parses it as prose"* and
answer item 4 in-spec now, including whether the stale example is updated in the same commit.

### M-06 — The spec is missing four sections that both structural comparators carry

- **Lens:** Incompleteness (structural)
- **Affected:** whole document

Against `adr-058-tool-denial-semantics-spec.md` (the sibling ADR-implementing spec, 616 lines) and
`workspace-heartbeat-memory-config-spec.md` (534 lines):

| Element | ADR-058 spec | heartbeat spec | this spec |
|---|---|---|---|
| TDD plan | §8 | §7 | **absent** |
| Verification bar restated | §8.1 (explicitly inherits ADR-057 §10) | — | **absent** |
| Stub-resistance — per-AC false-pass excluded | §8.2 | — | **absent** |
| Test datasets | — | §7 "Test Datasets" | **absent** |
| Regression requirements | §8.3 order | §7 "Regression Requirements" | §2.3 (broken — C-01) |
| Work units / wave graph | §9 | — | **absent** |
| Definition of done | §10 | — | **absent** |
| Pinned strings (single source) | §4.2 | — | **absent** (W5's shape deferred twice — §6 and §10) |
| Integration boundaries | — | §5 | **absent** |
| Relevant execution flows | — | §3 | **absent** |
| Out of scope / non-goals | §11 carried-forward | — | **absent** (see C-04) |
| Issue link | ADR links #594/#595 | — | **none** |
| Length | 616 | 534 | 336 |

The most consequential absence is the **verification bar**. ADR-059 §8 opens by inheriting ADR-058's
rule verbatim — *"a green test that does not exercise a production caller does not satisfy this
ADR"* — and this spec never restates it, never operationalises it, and (M-04) proposes at least one
test that violates it.

**Fix.** Add §"TDD plan" with a restated verification bar and a stub-resistance sub-section naming,
per success criterion, the false-pass it excludes (ADR-058 spec §8.2 is the model); add
§"Out of scope"; add §"Definition of done". A wave graph is optional at this size — say so rather
than omitting it silently.

### M-07 — The §6 W5 gate is real but toothless: three of four questions are answerable today, and there is no branch for a bad answer

- **Lens:** Inoperability / Overcomplexity
- **Affected:** §6, SC-005, §10 ambiguity 2

Asked directly: is this gate discipline or procrastination? **Both, in separable parts.**

*Discipline, genuinely:* ADR-058 §7 item 4 documents a real silent-Zod-drop failure mode for this
class, ADR-059 §7/W6 makes the gate an ADR obligation, and SC-005 blocks the merge. That is not
theatre.

*Procrastination, in three specific ways:*

1. **Items 1, 2 and 4 are answerable from the tree in minutes.** Item 1 and item 4 are answered in
   M-05 above; item 2 is one grep of `pkg/gateway/websocket.go`. Deferring a ten-minute lookup into a
   formal amendment cycle costs more than doing it, and it dilutes the one question that is actually
   open.
2. **Item 3 — the only open question — is aimed at the wrong component** (C-03), so the gate as
   written cannot answer it.
3. **There is no decision branch.** The gate says "answer these in writing" and stops. It never says
   *what happens if the answer is "yes, a human would see a JSON blob"*. No alternative shape is
   pre-agreed, no fallback (prose sentence + a trailing machine-readable suffix; a `result` map key
   rather than error text — `ToolCall.result` is `additionalProperties: true`, so that route needs no
   contract change at all) is even named. A gate with no branch is a pause, not a decision point.

Compounding it, §10 ambiguity 2 defers the **JSON field names** to a second confirmation. W5's shape
is thus deferred twice, in two places, with no owner named for either.

**Fix.** Answer items 1, 2 and 4 in the spec body now (M-05 supplies most of it). Keep item 3 as the
gate, rewritten per C-03. Add the missing branch: *"If any human-visible surface would render a raw
payload, W5 takes shape B — prose sentence unchanged, discriminator carried in `ToolCall.result`
(`additionalProperties: true`, no contract change) — and FR-007 is re-scoped to the result map."*
Pin the field names in a §"Pinned strings" block per the ADR-058 spec §4.2 precedent, defaulting to
ADR-058 D1's `{"error":…,"message":…}` shape, so §10 ambiguity 2 closes here rather than deferring.

### M-08 — FR-007's discriminator has no truncation bound, and truncation destroys machine-parseability

- **Lens:** Incompleteness
- **Affected:** FR-007, §3 edge case *"A very long refusal (the persisted side truncates at 2000 runes)"*

Both sides truncate, and the spec names only one:

- persisted: `tcRecord.Error = truncateRunes(contentForLLM, maxFailClosedOutputChars)`
  (`pkg/agent/loop.go:10372`; `maxFailClosedOutputChars = 2000`,
  `pkg/agent/task_completion_signal.go:340`);
- live frame: `truncateRunesForFrame(s, maxLiveErrorChars)` with the same bound of 2000 and an
  appended marker `"\n... (truncated, output continues)"` (`pkg/gateway/toolexecend_error_bound_test.go:24-52`).

A JSON payload cut at 2000 runes with a suffix appended is **invalid JSON**. FR-007 — *"machine
distinguishable"* — then fails silently for long paths, which is the one input the caller controls.
ADR-058's own marker survives because its payload **begins** `{"error":…}`.

**Fix.** State in FR-007 that the discriminator MUST be prefix-positioned (within the first N runes,
N stated) so no truncation path can remove it, and add a scenario: *"Given a refusal whose rendered
text exceeds the truncation bound, Then the discriminator is still present in both the live frame and
the persisted record."* Also note that `write_file` is a **trusted** tool
(`isUntrustedToolResult`, `loop.go:10146`) so `promptGuard.Sanitize` does not apply — saying so
removes a reviewer question (but see m-09 for the filter that *does* apply).

---

## MINOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **m-01** | Inconsistency | §3 | US-1, US-2 and US-4 carry an *"Independent test:"* line; **US-3 and US-5 do not.** US-5 is a P0 gate. | Add both, or drop the convention. |
| **m-02** | Incompleteness | §5, §9 | Four acceptance criteria have no BDD scenario: US-2 AC-2, US-3 AC-1, US-4 AC-3 (persisted transcript — this is ADR AC-07), US-5 AC-2 (wire validation). §9 shows this as `—` in three rows without comment. | Add scenarios for US-4 AC-3 and US-5 AC-2 at minimum; annotate the deliberate `—`s. |
| **m-03** | Ambiguity | §9 | The Test column names **no files and no test names** — *"new grep-assertion test"*, *"doc assertion test on the field comment"*, *"new per-provider parameter test"*, *"SPA render check (§6 item 3)"*. The sibling ADR-058 spec pins strings and test placement (§4.2, §8.3). | Replace each cell with a package-qualified file name and test name. |
| **m-04** | Overcomplexity | FR-004, §5 scenario 4 | The *"grep-assertion test"* is redundant: deleting `OnToolCallProgressKey` makes every surviving reference a **compile error**. The scenario's *"no matches outside historical records"* is also undefined — the literal appears in the ADR, in this spec, and in three live comments (`loop.go:8261`, `turn.go:417`, `delegate.go:333`) that W2 must also update. | Drop the grep test; rely on the compiler. Define "historical records", and add the three comment sites to §2.1. |
| **m-05** | Overcomplexity / Inconsistency | FR-006, §9 row 6 | A *"doc assertion test on the field comment"* (a `go/ast` test policing a comment) contradicts §1's own *"Documentation only"* and §2.2's *"Risk: NONE — no production consumer exists"*. | Drop the test; make W4 a reviewed diff. If a guard is genuinely wanted, say why the two prior statements were wrong. |
| **m-06** | Incompleteness | §1 table | The work-item table lists W1–W5. **W6 is executed by §6** and **W7 is what §2.3 protects**, and neither is named — a reader cannot tell whether they are in scope. | Add W6 and W7 rows ("gate, §6" / "delivered, frozen by §2.3"). |
| **m-07** | Ambiguity | §2.2, §6 | `FileWriteConfirm.tsx` is cited with no path. Its real path is `src/components/chat/tools/FileWriteConfirm.tsx`. | Use the full repo-relative path (and see C-03 — it is the wrong component regardless). |
| **m-08** | Incompleteness | whole doc | **No issue link.** ADR-059 Q4 records *"No GitHub issue is linked to this ADR yet"* and the spec inherits that silently. `CLAUDE.md` requires every PR to close its issues by keyword, per issue. | File the issue (Type: Task, `area:` labels) and link it in the header before the PR exists. |
| **m-09** | Incompleteness | §3 edge cases | `cfg.FilterSensitiveData(contentForLLM)` (`loop.go:10179`) runs over the result text unconditionally when enabled. A path containing a token-shaped substring would be redacted **inside** the JSON, producing an unparseable payload — a second way FR-007 can fail silently. Not in the edge-case list. | Add as an edge case, with the same prefix-positioning mitigation as M-08. |
| **m-10** | Ambiguity | §2.2 | *"3 in-tree implementers"* is true but misleading: `HTTPProvider.ChatStream` (`http_provider.go:51-59`) is a **pass-through** to an `openai_compat.Provider`. There are two real emitters and one forwarder — which matters because ADR AC-03's *"each implementing provider"* would otherwise demand a third emission test that can only re-test `openai_compat`. | Say "2 emitters + 1 forwarder" and scope AC-03 to the two emitters. |

---

## OBSERVATIONS

| ID | Section | Note |
|---|---|---|
| **O-01** | §1.2 | The best passage in the spec, and it should survive any revision verbatim. It converts a one-off deletion into a standing rule, and the rule is **correct**: `ToolResultHookResponse.Result *tools.ToolResult \`json:"result,omitempty"\`` (`pkg/agent/hooks.go:207`) does serialise the struct into the hook payload, and the safety claim holds — `0fb79b19` is contained by **no tag and no other branch**, so `ToolResult.Reason` never shipped. Consider promoting the rule itself to a comment on `tools.ToolResult`, where the next person to delete a field will actually read it. |
| **O-02** | §2.1 | The symbol table is **accurate line for line** on this branch — all ten rows verified, including `delegateStatusExtra` at `delegate.go:2557`, `ProgressForSession` at `turn.go:880`, and the `write_file` guard at `filesystem.go:830`. Worth stating in a re-grill note, because the reviewer's default posture on an asserted symbol table is disbelief, and this one earns the opposite. |
| **O-03** | §2.3 | The *concept* of a frozen-test list is the right instrument for a migration spec and is not present in either comparator. Once C-01 splits it into frozen vs. rewritten, it is the most reusable idea in the document — worth proposing as a standing convention for migration specs generally. |

---

## Structural integrity results (plan-spec mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | US-1 (3), US-2 (2), US-3 (1), US-4 (3), US-5 (2) |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** | 4 uncovered — m-02 |
| Every BDD scenario has a `Traces to:` | **PASS** | all 6 |
| Every BDD scenario has a corresponding test in the TDD plan | **FAIL** | there is no TDD plan — M-06 |
| Every functional requirement appears in the traceability matrix | **PASS** | FR-001…FR-009 all present |
| Every BDD scenario appears in the traceability matrix | **PASS** | all 6 |
| Test datasets cover boundaries, edge cases, error scenarios | **FAIL** | no datasets section; §3's five edge cases appear in no scenario, requirement or matrix row |
| Regression impact explicitly addressed | **FAIL** | §2.3 exists but is self-contradictory (C-01) and incomplete (C-02) |
| Success criteria measurable, no subjective language | **PARTIAL** | SC-001 not CI-measurable (M-01); SC-005 is a process gate, not a criterion; FR-008's *"remain human-readable"* has no defined check |

---

## Test coverage assessment

**Missing negative tests.** US-4's Scenario Outline covers "existing file" vs. "unwritable
directory", which is good. There is no case for: a path that exists as a **directory** (not a file);
a symlink to an existing file; a refusal on a path the sandbox denies *before* the existence check —
all of which produce error text that must **not** carry the discriminator.

**Missing concurrency tests.** ADR AC-05's cross-turn half is untested and frozen (M-02). This is the
single hazard that rewrote D1 after the ADR review's C-02 finding, and it is the one case the
delivered race test does not cover.

**Missing failure-mode test.** ADR AC-06 (panicking handler) has no test and no documented waiver
(M-02). D3 calls a panic here *"strictly worse than the blindness being fixed"* — an ungoverned
hazard the spec does not carry forward.

**Missing per-provider emission test.** Anthropic (M-03).

**Test level appropriateness.** Two proposed tests are at the wrong level for their subject: the
grep-assertion test (m-04, the compiler already does it) and the doc-comment assertion test (m-05,
disproportionate to a comment). No E2E is proposed where none is needed — that part is right.

**Regression baseline.** §2.3 is the right idea and the wrong list (C-01, C-02).

---

## STRIDE summary

| Component | Threats considered | Assessment |
|---|---|---|
| `ChatStream` `onProgress` parameter | Information disclosure | Handled and stated: FR-005 and D2 restrict the payload to byte counts. `ArgsBytes` is a length, not a digest — it discloses size only. **No new finding.** |
| Progress record on `turnState` | Information disclosure (cross-turn leakage), DoS (hot-path cost) | Cross-turn isolation is asserted by design but **not tested** (M-02 / AC-05). Hot-path cost is bounded by D3's discipline; the spec sets no rate and no measurement, inheriting the ADR review's unasked question 4 without answering it. |
| `write_file` refusal payload | Information disclosure | The refusal embeds a **filesystem path** into text that reaches the LLM, the persisted transcript, the WS frame and the SPA. That is already true today, so W5 adds no new exposure — but m-09's sensitive-data filter interacts with it, and the spec should say the exposure is unchanged rather than leaving a reviewer to derive it. |
| `PostToolUse` hook payload | Tampering / contract break | §1.2 identifies the hazard and states the standing rule. **Best-handled surface in the spec.** |
| Delegation status surface | Repudiation | Unchanged by this spec (W7 delivered). No finding. |

No spoofing, elevation-of-privilege or authentication surface is touched by W1–W5.

---

## Unasked questions

1. **Who owns the §6 amendment, and where does it live?** SC-005 says W5 is not merged until §6 is
   *"answered in an appended amendment"*. The heartbeat spec's convention is a numbered
   `## 1.x Amendment An — post spec-grill (BINDING; overrides any conflict below)` block at the top.
   This spec names no location, no author role, and no ratification step.
2. **What is the rollback story for W1?** It is a signature change across two packages and seven test
   files. If AC-03 fails on Anthropic in UAT, is the revert W1 alone, or W1+W2 (which would resurrect
   deleted code)? The spec's own §1 framing — migration, regression is the risk — implies a revert
   plan and does not provide one.
3. **Does anything teach the orchestrator to read the progress line?** ADR-059 §3 claims the fix
   *"removes one of the four inputs"* to the cancel/respawn loop, but that only happens if Jim's
   prompt or the `delegate status` help text tells him what the line means. Nothing in W1–W5 touches
   either. Is that in scope, deferred, or assumed?
4. **What does a stale progress record look like to a reader?** `clearToolCallProgress` is deferred
   at the top of `callLLM` (`loop.go:8327`), so the record clears at round end — but
   `formatToolCallProgressLine` renders a *"last progress Ns ago"* freshness figure with no stated
   staleness ceiling. At what age does the line stop being reassuring and start being misleading?
   (ADR review unasked question 1, still open.)
5. **If W5's discriminator lands, does anything ever read it?** FR-007's consumer is "the calling
   agent", i.e. a model. ADR AC-07 concedes the real outcome is model behaviour and is untestable.
   Is there any plan to observe, post-release, whether a worker actually reports *"already done"* —
   or is W5 shipped on judgement with no feedback loop at all? Say which.
6. **Why is W5 in this spec?** W1–W4 are a typed-transport migration with zero behavioural surface.
   W5 is the only behavioural change, carries the only real risk, is blocked on a gate, and shares no
   code with the other four. Splitting it out would let W1–W4 ship immediately and give W5 the
   contract analysis it needs without holding the migration hostage. The spec never justifies the
   bundle.

---

## Next action

**Verdict: BLOCK** — 4 CRITICAL.

Address in this order:

1. **C-01** — split §2.3 and restate FR-009/SC-002. Nothing can be implemented until this is
   coherent.
2. **C-02** — enumerate the full compile break and plan the CI-worker verification.
3. **C-04** — add the out-of-scope / coverage-limits section and qualify §4.
4. **C-03 + M-07** — rewrite the §6 gate at the right surfaces, answer items 1/2/4 in-spec, and add
   the "what if the answer is bad" branch.
5. **M-02, M-03** — map the ADR's ACs and add the two missing tests (Anthropic emission, cross-turn
   isolation).
6. **M-06** — add the TDD plan with the inherited verification bar, out-of-scope, definition of done.
7. Then the MINORs, of which m-04, m-05 and m-06 are quick deletions rather than additions.

Re-run after revision:

```
/grill-spec docs/internal/specs/adr-059-delegation-observability-spec.md
```

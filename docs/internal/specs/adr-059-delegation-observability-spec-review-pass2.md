# Spec review — ADR-059 work items W1–W5 (adversarial grill, pass #2 / RE-GRILL)

- **Reviewed:** `docs/internal/specs/adr-059-delegation-observability-spec.md` (407 lines, now carrying
  `## 1.0 Amendment A1 — post spec-grill #1 (BINDING)`)
- **Pass #1 report:** `docs/internal/specs/adr-059-delegation-observability-spec-review.md` (BLOCK,
  4 CRITICAL / 8 MAJOR / 10 MINOR / 3 OBSERVATION)
- **Branch:** `fix/uat-delegation-rootcauses` (both spec and review still untracked; tree otherwise clean)
- **Authority:** ADR-059 (Accepted), ADR-058 §3/§7, `CLAUDE.md` Constraints #7 and #8
- **Reviewer mode:** `plan-spec`
- **Conformance comparator:** `docs/internal/specs/workspace-heartbeat-memory-config-spec.md`
- **Date:** 2026-08-10
- **Verification:** every `file:line`, symbol and count below was read on this branch in this session.
  Where this report contradicts A1, the contradiction is evidenced, not asserted.

---

## Executive summary

**Five CRITICAL, nine MAJOR, eight MINOR, two OBSERVATION. Verdict: BLOCK.**

The amendment is not an amendment. A1 reads as a changelog of edits that were made — "§2.2's
dependent list is corrected likewise", "§6 item 3 now targets `GenericToolCall.tsx`", "§4's first
behavioural bullet is scoped", "SC-001 … Reclassified", "AC-01…AC-07 are now mapped in §9",
"missing house sections added: out-of-scope, definition of done, and a TDD plan". **None of those
six edits exists in the document.** §2.2 still names `FileWriteConfirm.tsx`; §6 item 3 still names
`FileWriteConfirm.tsx` and still has four questions; §4 bullet 1 is verbatim unchanged; SC-001 is
verbatim unchanged; §9 has no AC row; and the document's own section list (`grep -n "^#"`) ends at
§11 with no out-of-scope, no definition of done and no TDD plan. Exactly one body edit was
executed — §2.3. The result is a spec whose top section states, as fact, six things about its own
body that a reader can falsify in thirty seconds.

Worse, the one item A1 got substantively right in direction it got **factually wrong in
enumeration**, and that error is load-bearing. A1-4 declares, as BINDING scope, that "only
`anthropic` and `openai_compat` implement `ChatStream`". Verified: **three** types implement it —
`HTTPProvider` is the third (`pkg/providers/http_provider.go:51`), it is asserted as such in
`streaming_compliance_test.go:27`, it is named as an implementer by the spec's own §2.1, and ADR-059
W6's table says "update all **three** implementers". A1 therefore contradicts the ADR it is bound to
and the section three inches below it.

And chasing that error surfaced what neither pass found: **`HTTPProvider` is not a side path, it is
the only production streaming path.** `openai_compat.Provider` is constructed in exactly one place in
the tree — inside `NewHTTPProviderWithMaxTokensFieldAndRequestTimeout` (`http_provider.go:25`).
`anthropicprovider.Provider` is constructed in exactly one place — inside `ClaudeProvider`
(`claude_provider.go:16,25`), which implements `Chat` and **not** `ChatStream` and holds its delegate
as an unexported field, so no method is promoted. The native Anthropic emitter that ADR-059 D1 was
written to add is **unreachable from the provider factory**. A1-7 mandates an Anthropic emission test
as "required work"; that test would exercise no production caller, which is the precise class
ADR-059 §8's inherited bar exists to exclude.

Against the direct questions asked of this pass: **A1-3's `FileWriteConfirm.tsx` claim is confirmed**
(the component destructures `{ args, status }` and never `result`), **A1-4's scope claim is refuted**,
**the A1-8 sections were promised and not added**, **the W5 gate is still aimed at the wrong
component and still has no decision branch**, and **W1/W2/W4 are not executable as written** — W1
because the compile break is asserted as "~13 sites" but never enumerated while the spec tells the
implementer to "discover by compiling" in a package `CLAUDE.md` forbids compiling locally.

| Severity | Count |
|---|---|
| CRITICAL | 5 |
| MAJOR | 9 |
| MINOR | 8 |
| OBSERVATION | 2 |
| **Total** | **24** |

---

## CRITICAL

### C2-01 — A1 describes six body edits that were never made; the amendment is a promise ledger, not an amendment

- **Lens:** Incorrectness / Inconsistency
- **Affected:** A1-3, A1-4, A1-6, A1-7, A1-8 vs. §2.2, §4, §6, §8, §9

Each row below is A1's own wording against what `grep`/`sed` returns from the same file.

| A1 says | Document actually contains | Line |
|---|---|---|
| A1-3: "§6 item 3 **now targets** `GenericToolCall.tsx`" | "**Does the SPA's `FileWriteConfirm.tsx` render the refusal as a sentence today**…" | §6 item 3, :343-345 |
| A1-3: "§2.2's dependent list is **corrected** likewise" | "…the SPA's `FileWriteConfirm.tsx`; the persisted `ToolCall.error`" | §2.2 row 4, :144 |
| A1-4: "§4's first behavioural bullet **is scoped** to streaming-capable providers only" | "When a provider streams tool-call arguments, the system reports forward progress to the caller." — unqualified | §4, :257 |
| A1-6: "SC-001 is not a test. **Reclassified**" | "**SC-001**: Removing the progress parameter from any in-tree provider produces a build failure." — verbatim as pass #1 found it | §8, :369 |
| A1-7: "ADR-059's AC-01…AC-07 **are now mapped in §9**" | §9 has nine rows, all keyed `FR-00x`. **No AC appears anywhere in §9**, or anywhere else in the spec. | §9, :377-387 |
| A1-8: "**missing house sections added**: out-of-scope, definition of done, and a TDD plan" | Section list runs §1, §1.1, §1.2, §2, §2.1–2.3, §3, §4, §5, §6, §7, §8, §9, §10, §11. **None of the three exists.** | whole doc |
| A1-8: "§6's four gate questions are **also reduced to the one** that is genuinely open" | §6 still lists four numbered questions. | §6, :339-346 |

**Impact.** This is worse than an unrevised spec, because it is an unrevised spec wearing a
certificate of revision. The header says A1 is BINDING and "overrides any conflict below", so a
reader who trusts it will believe an out-of-scope section, a definition of done, a TDD plan and an
AC map exist and will look for them; a reader who reads top-to-bottom will hit the un-amended text
and cannot tell which of the two documents is the spec. Worst case is the middle: an implementer
reads A1-6, records SC-001 as "reclassified to a review obligation", and §8 still carries it as a
success criterion for whoever signs off the release. Pass #1's C-03 (the wrong gate component),
M-01 (SC-001), M-02 (AC map) and M-06 (missing sections) are all still **open**, now with a top
section claiming they are closed.

**Fix.** Either do the edits, or convert every A1 sentence from a claim about the document into an
instruction on the document. Concretely: A1 should say "*§6 item 3 MUST be rewritten to target …*"
and the body then carries the rewrite, exactly as the comparator does — `workspace-heartbeat-memory-config-spec.md`
amends A1/A2 at the top **and** stamps the affected body section (`US-7 … *(amended A1/F-02+F-04)*`,
:152). No section of this spec bears such a stamp. Until the body matches, the spec cannot be
implemented from and cannot be reviewed against.

---

### C2-02 — A1-4's binding scope statement is factually wrong: three types implement `ChatStream`, and it contradicts both §2.1 and the ADR

- **Lens:** Incorrectness
- **Affected:** A1-4, §2.1 row 4, §2.2 row 1, ADR-059 §6 W1

A1-4, stated as verified-by-enumeration and BINDING:

> Verified by enumeration: **only `anthropic` and `openai_compat` implement `ChatStream`.**

Refuted. `grep -rn "func .*ChatStream" --include='*.go'` returns three method declarations:

| Type | File:line |
|---|---|
| `*openai_compat.Provider` | `pkg/providers/openai_compat/provider.go:206` |
| `*anthropicprovider.Provider` | `pkg/providers/anthropic/provider.go:118` |
| `*providers.HTTPProvider` | `pkg/providers/http_provider.go:51` |

`pkg/providers/streaming_compliance_test.go:24-28` asserts all three at compile time
(`_ StreamingProvider = (*HTTPProvider)(nil)`), and its runnable twin lists `http_provider` as a
third case (:39). ADR-059 §6 W1 reads "update **all three** implementers". The spec's own §2.1 lists
`HTTPProvider.ChatStream` as "W1: implementer (delegates through)" and §2.2 says "3 in-tree
implementers".

So A1-4 — the section headed "overrides any conflict below" — conflicts with the ADR (which per the
spec header **wins**), with §2.1, and with §2.2, and no reconciliation is offered.

**Impact.** A1-4 is not a stray sentence; it is the premise of the scope carve-out that A1 asks
ADR-059 §4 to inherit, and the premise of A1-7's "required work" list. An implementer executing W1
against A1-4's enumeration updates two files and leaves `http_provider.go:51-60` on the old
signature — a compile break at the one call site (`:59`) that actually runs in production (see
C2-03).

**Fix.** Restate A1-4 as *"three types implement `ChatStream`: two emitters (`openai_compat`,
`anthropic`) and one forwarder (`HTTPProvider`, `http_provider.go:51`, which delegates to an
`openai_compat.Provider`)"* — which is pass #1's m-10, adopted in substance and mis-stated in fact.
Then re-derive A1-4's coverage conclusion from the corrected enumeration; it does not survive
unchanged (C2-03).

---

### C2-03 — The production streaming surface is one emitter behind one forwarder; the native Anthropic emitter is unreachable from the factory, and A1-7 mandates a test that cannot exercise a production caller

- **Lens:** Incorrectness / Infeasibility
- **Affected:** A1-4, A1-7, FR-002, ADR-059 AC-03, §4 bullet 1

Constructor sites, tree-wide, verified by grep:

- `openai_compat.NewProvider` is called in **exactly one non-test location**:
  `pkg/providers/http_provider.go:25`, inside `NewHTTPProviderWithMaxTokensFieldAndRequestTimeout`.
- `anthropicprovider.NewProvider` / `NewProviderWithTokenSource` are called in **exactly one
  non-test location each**: `pkg/providers/claude_provider.go:16` and `:25`, inside `ClaudeProvider`.

And `ClaudeProvider` (`claude_provider.go:10-47`) holds `delegate *anthropicprovider.Provider` as an
**unexported field, not an embedded type** — no promotion — and declares `Chat` (:32) and
`GetDefaultModel` (:46) only. **It has no `ChatStream`.**

Trace the factory (`pkg/providers/factory_provider.go`):

| Configured protocol | Provider handed to the loop | Streams? |
|---|---|---|
| `anthropic` + API key (:308-338) | `HTTPProvider` → `openai_compat` | yes |
| `anthropic` + `oauth`/`token` (:309-315 → `createClaudeAuthProvider`, :22-33) | `ClaudeProvider` | **no** |
| `openai`, `openrouter`, `litellm`+~20 aliases, `minimax` (:112, :203, :263, :295) | `HTTPProvider` → `openai_compat` | yes |
| `azure`, `bedrock`, `anthropic-messages`, `antigravity`, `claude-cli`, `codex-cli` | own types | no |

Two consequences the spec states the opposite of:

1. **`HTTPProvider` is not a peripheral forwarder — it is the sole production entry point to
   streaming.** A1-4 dismisses it by omission; §2.1 files it as "delegates through".
2. **`anthropicprovider.Provider.ChatStream` is reachable from no factory branch.** A1-7 records as
   "required work": *"AC-03 has no Anthropic emission test at all"*. Adding one satisfies the letter
   of ADR AC-03 and violates ADR-059 §8's opening bar — *"a green test that does not exercise a
   production caller does not satisfy this ADR"* — which the spec never restates (pass #1 M-06,
   still open per C2-01).

**Impact.** ADR-059 D1's narrative is that the Anthropic provider "had no `ChatStream` method at all,
so every Anthropic-backed call took the non-streaming path" (`streaming_compliance_test.go:20-22`).
The method now exists; on the OAuth/token path **the call still takes the non-streaming path**,
because the wrapper in front of it does not forward streaming. The delivered fix does not reach the
configuration the incident narrative describes, and this spec — whose whole framing is "the behaviour
is already delivered and green" (§1) — never checks that claim against the factory.

**Fix.** Three things, none optional:

1. Correct A1-4's enumeration per C2-02 and re-scope §4 bullet 1 to name the **reachable** path:
   *"a single-candidate turn on a provider constructed through `HTTPProvider`"*.
2. State explicitly whether `ClaudeProvider` gains a forwarding `ChatStream` **in this spec** or is
   filed as a named coverage gap with a tracked issue. It is a five-line method
   (`http_provider.go:51-60` is the template); leaving it undecided means an operator on OAuth
   Anthropic gets neither text streaming nor progress and nothing in the spec says so.
3. Re-scope AC-03 to the emitters that a production path reaches. If `ClaudeProvider` is fixed under
   (2), the Anthropic emission test A1-7 demands becomes legitimate; if not, say in writing that
   AC-03 is discharged for `openai_compat` only and waived for `anthropic` with the reason, rather
   than adding a green test against dead code.

---

### C2-04 — FR-009 is "WITHDRAWN" in A1 and still live in §7, §8 and §9 — and §9 still contradicts the rewritten §2.3 directly

- **Lens:** Inconsistency
- **Affected:** A1-1, FR-009, SC-002, §9 rows 2 and 9

A1-1: *"FR-009 is **WITHDRAWN**."* The body:

- **§7 :365** — `**FR-009**: No test listed in §2.3 may change to accommodate the migration.`
  Present, unmarked, indistinguishable from the eight live requirements around it.
- **§8 :371** — `**SC-002**: Every test in §2.3 passes unchanged after W1 and W2.` A1 **never
  mentions SC-002**. It is now strictly false against the rewritten §2.3, which contains a table
  headed "REWRITTEN — mechanism changes, so the test must too". SC-002 and §2.3 are in the same
  document, three sections apart, asserting opposite things about the same list.
- **§9 :387** — a traceability row `FR-009 | US-2 | — | SC-002`, mapping a withdrawn requirement to a
  false criterion.
- **§9 :380** — `FR-003 | US-1 | Hook replacing options cannot silence progress |`
  **`toolcall_progress_wiring_test.go` (existing, must pass unchanged)`** — the exact cell pass #1
  C-01 called out, still there, still saying "must pass unchanged" about the one file §2.3 now
  classifies as REWRITTEN.

**Impact.** "A1 overrides any conflict below" does not resolve this, because A1-1 withdraws FR-009
and says nothing about SC-002 or §9 row 2. The override rule tells a reader which document wins on a
**stated** conflict; it cannot dispose of a criterion the amendment never addressed. A release
reviewer checking §8 will find SC-002 unsatisfiable and have no textual basis to waive it.

**Fix.** Delete FR-009 and its §9 row, or restate it as pass #1 proposed (*"the behaviour asserted by
each FROZEN test must hold unchanged; each REWRITTEN test must assert the same property through the
new transport, in the same commit as W1/W2"*). Replace SC-002 with *"every test in §2.3's FROZEN
table passes with a zero-line diff; each REWRITTEN test asserts the replacement assertion named in
§2.3"*. Fix §9 row 2's test cell to name the rewritten assertion.

---

### C2-05 — The W5 gate is unchanged: still aimed at a component that reads no result, still four questions, still no branch for a bad answer

- **Lens:** Incorrectness / Inoperability
- **Affected:** §6, SC-005, §2.2 row 4, US-5, FR-008

**A1-3's diagnosis is confirmed.** `src/components/chat/tools/FileWriteConfirm.tsx` declares
`makeAssistantToolUI<WriteFileArgs, unknown>` and renders `({ args, status })` — `result` is never
destructured and never referenced anywhere in the file. It renders `basename(args.path)`, a byte
count of `args.content`, and a status word from `getToolBadgeStatusConfig`. It cannot show a sentence
today and cannot show a JSON blob tomorrow. A1-3 is right.

**A1-3's corrective is confirmed too.** `src/components/chat/tools/GenericToolCall.tsx` takes
`result?: unknown` (:35) and `error?: string` (:38), renders `{error}` verbatim at :523-524, and
renders result content with sentinel special-casing at :459-520 — including an
`error === 'delegation_denied'` discriminator branch (:90) that is the exact ADR-058 precedent §6
calls "a lead, not an answer".

**And none of it reached the document.** §6 item 3 is byte-identical to the version pass #1 reviewed,
still naming `FileWriteConfirm.tsx` as "the user-visible question and the most likely reason to
choose a different shape". §2.2 row 4 still lists it as a direct dependent. §6 still has four items,
not the one A1-8 says it was reduced to. And **there is still no decision branch** — the gate says
"answered in writing and appended as an amendment" and stops; nothing states what W5 becomes if a
human-visible surface would render a payload. Pass #1's M-07 fix (fall back to `ToolCall.result`,
which is `additionalProperties: true` at `contracts/components/schemas/ToolCall.yaml:72`, needing no
contract change) is not present, and §10 ambiguity 2 still defers the JSON field names a second time.

**Impact.** SC-005 — "W5 is not merged until §6 is answered" — is satisfiable by answering the
question §6 asks, which is about a component that cannot change. The gate returns a false all-clear
and records it as a passed criterion. That is the finding pass #1 rated CRITICAL, and A1 addressed it
in prose only.

**Fix.** Apply A1-3's own text to §6 item 3 and §2.2 row 4. Name `FileWriteConfirm.tsx` in §2.2 as
explicitly **not** a dependent, with the reason, so the next reader does not re-derive it. Answer
items 1, 2 and 4 in the body (M-05 below supplies items 1 and 4). Add the branch: *"If any
human-visible surface would render a raw payload, W5 takes shape B — prose sentence unchanged,
discriminator in `ToolCall.result` — and FR-007 is re-scoped to the result map."* Pin the field names
now, defaulting to ADR-058 D1's shape at
`docs/internal/architecture/ADR-058-tool-denial-semantics.md:49`
(`{"error":"…","message":"…","tool":…,"reason":…}`).

---

## MAJOR

### M2-01 — A1-2 asserts a number instead of a list, and instructs discovery by a compile the project forbids locally

- **Lens:** Incompleteness / Infeasibility
- **Affected:** A1-2, §2.2 row 1, §2.3 closing line

A1-2 says "the compile break is ~13 sites, not 4, two of them in `pkg/gateway`". It names none of
them. §2.2 row 1 still reads "3 in-tree implementers + 1 caller". §2.3 closes: *"Anything not listed
in either table must be discovered by compiling, not by assumption."*

That instruction is unexecutable as written on this project. Per `CLAUDE.md` ("Testing & building —
CI is the authority"), the `pkg/gateway` test binary must not be built or run in the dev pod, and
two of the break sites are there. "Discover by compiling" therefore means one CI-worker round-trip
per discovery, on a spec that budgets for it only as a bare SC-004 ("`go-test` and `go-race` are
green on the worker at the final commit").

Enumerated on this branch — this is the list A1-2 should carry:

| File:line | Kind |
|---|---|
| `pkg/providers/types.go:44` | interface declaration |
| `pkg/providers/openai_compat/provider.go:206` | emitter; also `:259` derives `onProgress` from options |
| `pkg/providers/anthropic/provider.go:118` | emitter; also `:144` derives from options |
| `pkg/providers/http_provider.go:51` + `:59` | forwarder decl **and** call site |
| `pkg/agent/loop.go:8412` | production call site; `:8288` is the options injection W2 deletes |
| `pkg/agent/toolcall_progress_wiring_test.go:63` | stub `ChatStream` + `var _` at `:74` |
| `pkg/agent/async_result_persistence_test.go:320` | stub `ChatStream` |
| `pkg/gateway/cancel_transcript_order_test.go:56` | stub — **CI only** |
| `pkg/gateway/websocket_multisession_test.go:56` | stub — **CI only** |
| `pkg/providers/openai_compat/chatstream_usage_test.go:50, 134, 204, 213` | 4 positional call sites |
| `pkg/providers/openai_compat/cache_tokens_test.go:85, 162` | 2 positional call sites |
| `pkg/providers/anthropic/provider_test.go:265` | streaming round-trip |
| `pkg/providers/streaming_compliance_test.go:24-28` | compliance block — passes only if all three are updated |

**Fix.** Put the table in §2.2 as a "mechanically edited, no behaviour change" row. Add a success
criterion pass #1 already drafted: *"every stub `ChatStream` in the tree accepts and ignores
`onProgress`; no stub may silently drop it, since a stub that drops it makes the wiring test
vacuous."* Name the gate sequence from `deploy/ci-worker/CLAUDE.md` (`go-build`, then `go-test`) and
state that W1 is expected to need at least one worker round-trip before it can be reviewed.

### M2-02 — A1-1's named replacement assertion hardens the one test pass #1 proved cannot fail

- **Lens:** Infeasibility
- **Affected:** §2.3 REWRITTEN row 1, FR-003, US-1 AC-3, BDD scenario 2, §9 row 2

§2.3's replacement assertion for `toolcall_progress_wiring_test.go`:

> the stub receives a **non-nil `onProgress` parameter**, and still does so after a `BeforeLLM` hook
> replaces the options

After W1/W2, `onProgress` is a positional parameter and nothing derives it from `llmOpts` — the
injection at `loop.go:8288` is deleted by W2. A hook replacing options therefore cannot affect it
**by construction**. The second clause has no reachable failure mode. Pass #1 raised this (M-04);
A1 does not mention it and has now written the vacuous half into the binding test disposition.

There is a genuine tension here the spec must resolve rather than skip: **ADR-059 AC-02 requires it**
("The handler survives a `BeforeLLM` hook replacing the options/parameters wholesale"), and the ADR
wins on disagreement. So "delete FR-003" is not available without an ADR amendment.

**Fix.** State the resolution explicitly. The defensible version is structural, not behavioural:
assert that the `ChatStream` call site passes a non-nil `onProgress` **while** the hook block returns
`HookActionModify` with a nil/empty `Options` map — which pins call-site ordering (the hook block must
not run after the argument is computed in a way that could reorder) and can fail if someone
re-routes `onProgress` through options in future. Say in one line that this is what AC-02 means
post-W1, so a reviewer does not read a green test as evidence of a property no longer at risk.

### M2-03 — A1-7 names three real gaps and converts none of them into a deliverable

- **Lens:** Incompleteness
- **Affected:** A1-7, §2.3, §7, §8, §9

A1-7 says AC-03 (Anthropic emission), AC-05 (cross-turn isolation) and AC-06 (panicking handler)
"are recorded as required work, not deferred silently". Verified that all three gaps are real:

- **AC-03** — `grep -rn ToolCallProgress pkg/providers/anthropic/` returns production code only
  (`provider.go:144, :167, :233`). No test. (And see C2-03 — the test may be untestable in the
  ADR's own terms.)
- **AC-05** — `pkg/agent/toolcall_progress_race_test.go:33` is `ts := &turnState{}`, a single
  `turnState`. The cross-turn half is untested, and §2.3 lists this file as **FROZEN**, locking the
  gap in.
- **AC-06** — no panic test anywhere; no waiver paragraph in the spec.

But "recorded as required work" is the whole of the treatment. No test file is named, no FR is added,
no SC is added, no §9 row is added, and §2.3 does not move the race test out of FROZEN or add a
sibling. A requirement that appears only inside an amendment's prose, with no matrix row, is not
tracked — it is mentioned.

**Fix.** Add FR-010/FR-011/FR-012 (or explicit waivers), one §9 row each with a package-qualified
file and test name, and either add a cross-turn case to `toolcall_progress_race_test.go` — which
means it leaves the FROZEN table — or add a new file and say so.

### M2-04 — A1-5's prefix-positioning rule never reached FR-007, and the truncation edge case has no scenario

- **Lens:** Incompleteness
- **Affected:** A1-5, FR-007, §3 edge cases, §5, §9

A1-5's facts check out precisely: persisted bound `maxFailClosedOutputChars = 2000`
(`pkg/agent/task_completion_signal.go:340`); live bound `maxLiveErrorChars = 2000`
(`pkg/gateway/websocket_live_error_bound.go:12`), applied at `websocket.go:3820`, and
`truncateRunesForFrame` returns `string(runes[:maxRunes]) + "\n... (truncated, output continues)"`
(:26) — a JSON payload cut there and suffixed is invalid JSON. ADR-058's payload survives only
because it **begins** `{"error":…`.

The rule, however, exists only in A1-5. **FR-007 is verbatim unchanged** (:366-367) and states
machine-distinguishability with no positional constraint and no bound. There is no BDD scenario for
the long-refusal case; §3's edge case *"A very long refusal (the persisted side truncates at 2000
runes)"* is listed and appears in no requirement, scenario or matrix row. Pass #1's m-09 (the
`FilterSensitiveData` pass at `loop.go:10179` redacting **inside** the payload) is also unaddressed —
a second silent-failure route with the same mitigation.

**Fix.** Amend FR-007 in place: *"…and the discriminator MUST occupy the first N runes of the result
text, N ≤ 2000, so neither the persisted nor the live truncation path can remove or sever it."* Add
the scenario A1-5 implies (*"Given a refusal whose text exceeds 2000 runes, Then the discriminator is
present in both the live frame and the persisted record"*) and a §9 row. Add the sensitive-data
filter as an edge case with the same mitigation.

### M2-05 — The contract artefacts carrying the refusal string are still unlisted, and A1-8's answer to §6 item 4 is incomplete because of it

- **Lens:** Incompleteness / Incorrectness
- **Affected:** A1-8, §2.2 row 4, §6 items 1 and 4

A1-8 answers §6 items 1 and 4 with: *"`ToolCall.error` is `type: string`, so JSON-inside-a-string
always validates, and `additionalProperties: false` only bites on added fields, which W5 does not
add."* Both halves are correct — `ToolCall.yaml:9` is `additionalProperties: false`, `:49-50` is
`error: type: string`. The conclusion drawn from them is not.

`grep -rn "already exists. Set overwrite"` returns four artefacts that carry the **current sentence
verbatim**:

| Artefact | Line |
|---|---|
| `contracts/components/schemas/ToolCall.yaml` | `:62` — `example:` **is** the refusal sentence |
| `pkg/gateway/inboundschemas/ToolCall.yaml` | `:62` — mirrored copy, same example |
| `src/lib/api/generated/openapi-types.ts` | `:3416` — generated `@example`, regenerated only by the pipeline |
| `pkg/gateway/toolexecend_error_bound_test.go` | `:58` — the sentence as a short-error fixture |

ADR-059 §7 item 1 names the mirror and the generated file explicitly; the spec's §6 item 1 drops
both. So §6 item 4's real answer is two-part — **no** pipeline run is needed for schema validity,
**yes** if the now-stale `example:` is refreshed, because that touches `contracts/` and
`make verify-contracts` fails on generated drift (Constraint #8). A1-8 gives only the first half and
presents it as the answer.

**Fix.** Add the four artefacts to §2.2 row 4. State in §6 item 4 whether the stale example is
refreshed in the same commit as W5, and if so that the 5-step pipeline runs.

### M2-06 — No TDD plan means six BDD scenarios have no tests, and §9's Test column still names no file

- **Lens:** Incompleteness (structural)
- **Affected:** A1-8, §5, §9

A1-8 promises a TDD plan; there is none (C2-01). Consequently all six §5 scenarios are unbacked, and
§9's Test column contains only descriptions — *"new grep-assertion test"*, *"doc assertion test on
the field comment"*, *"new per-provider parameter test"*, *"SPA render check (§6 item 3)"* — with no
package, no file, no test name. Pass #1's m-03 is unaddressed.

The comparator carries §7 "TDD Plan" plus "Test Datasets" and "Regression Requirements"
(`workspace-heartbeat-memory-config-spec.md:352, :381, :412`). This spec has none of the three; §2.3
is the closest analogue and covers regression only.

**Fix.** Add the TDD plan, restating ADR-059 §8's inherited bar verbatim at its head, with one row
per scenario naming a package-qualified file and test name.

### M2-07 — A1-4's third bullet imposes an obligation on ADR-059 that ADR-059 does not carry, and names no tracker

- **Lens:** Inconsistency / Inoperability
- **Affected:** A1-4 bullet 3

A1-4: *"the gap … MUST be recorded in ADR-059 §4 (out of scope) rather than left implicit. Tracked,
not fixed here."*

`ADR-059 §4` currently reads, in full: *"Whether `steer` should be deliverable mid-round; whether
unbounded worker output should be bounded by mechanism rather than prompt guidance; whether
`write_file` should expose 'who wrote this and when' to a racing sibling; whether `inspect_session`
should surface persisted failure reasons (not yet tracked — see §8)."* The progress-coverage gap is
absent.

"Tracked" names no issue. The spec has no issue link at all (pass #1's m-08, unaddressed); ADR-059 Q4
already records that no GitHub issue is linked to the ADR. `CLAUDE.md` requires every PR to close its
issues by keyword, per issue — there is nothing to close.

**Fix.** Either amend ADR-059 §4 in the same change and cite the commit, or restate A1-4 bullet 3 as
an open action with an owner. File the issue (Type: Task, `area:` labels) and put the number in both
headers.

### M2-08 — W2's comment debt is still unlisted; deleting the symbol orphans four live doc references

- **Lens:** Incompleteness
- **Affected:** §2.1, W2

`OnToolCallProgressKey`'s doc comment (`protocoltypes/progress.go:3-19`) is the canonical narrative of
the incident, and four live comments point readers at it:

- `pkg/agent/loop.go:8261`
- `pkg/agent/turn.go:417`
- `pkg/tools/delegate.go:333`
- `pkg/providers/openai_compat/provider.go:348`

W2 deletes the target. §2.1 lists neither the comments nor a destination for the narrative. Pass #1
raised three of the four (m-04); A1 does not mention it.

**Fix.** Add a §2.1 row naming the four sites and state where the incident narrative lands after W2 —
`ToolCallProgress`'s own doc comment is the obvious home, and §1.2's precedent (put the standing rule
where the next person will read it) argues for it.

### M2-09 — Two tests specified at the wrong level survive unamended

- **Lens:** Overcomplexity
- **Affected:** FR-004 / §5 scenario 4 / §9 row 4; FR-006 / §9 row 6

- The **grep-assertion test** for FR-004 is redundant: deleting `OnToolCallProgressKey` makes every
  surviving reference a compile error. Its scenario's *"no matches outside historical records"* is
  also undefined — the literal appears in ADR-059, in this spec, and in the four comments above.
- The **doc-assertion test** on `Index`'s comment (a test policing a comment) contradicts §1's own
  "Documentation only" and §2.2's "Risk: NONE — no production consumer exists".

Both were pass #1 MINORs (m-04, m-05); A1 addressed neither. They are grouped here because together
they are the only new test work §9 names for W2 and W4, so removing them leaves those two work items
with **no** verification story at all — which must then be stated rather than left blank.

**Fix.** Drop both. State that W2 is verified by the compiler plus the FROZEN table, and W4 by
reviewed diff, in one line each.

---

## MINOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **m2-01** | Incompleteness | §2.3 REWRITTEN row 2 | `openai_compat/toolcall_progress_test.go` has **three** tests, not two: `TestParseStreamResponse_EmitsProgressForToolCallArguments` (:41), `TestParseStreamResponse_NilProgressCallbackIsSafe` (:120), `TestToolCallProgressFromOptions` (:133). The row names only "the tool-args-only stream case" as surviving. | Name all three; the nil-callback case is FROZEN too and is the only cover for §4's "given no callback, reports nothing and does not fail". |
| **m2-02** | Ambiguity | §1 header | Status still reads **"Draft (pre-grill)"** after two grills and a binding amendment. | Update to "Draft — post-grill #2" with the review links. |
| **m2-03** | Incompleteness | §1 table | W6 and W7 still unnamed in the work-item table, though §6 executes W6 and §2.3 protects W7. Pass #1 m-06, unaddressed. | Add both rows. |
| **m2-04** | Inconsistency | §3 | US-3 and US-5 still lack the *"Independent test:"* line the other three carry; US-5 is P0. Pass #1 m-01, unaddressed. | Add both, or drop the convention. |
| **m2-05** | Incompleteness | §5, §9 | Four acceptance criteria still have no BDD scenario: US-2 AC-2, US-3 AC-1, US-4 AC-3 (the persisted-transcript case — this is ADR AC-07), US-5 AC-2. Pass #1 m-02, unaddressed. | Add US-4 AC-3 and US-5 AC-2 at minimum; annotate deliberate `—`s. |
| **m2-06** | Ambiguity | §2.2 row 4 | `FileWriteConfirm.tsx` still cited without a path. Full path: `src/components/chat/tools/FileWriteConfirm.tsx`. Pass #1 m-07, unaddressed — and per C2-05 it should be removed from the row entirely. | Remove, with a "not a dependent, because…" note. |
| **m2-07** | Ambiguity | §10 | The ambiguity audit is unrevised: row 1 still poses W1-vs-`Chat` as an open question that A1-4 answers; rows 2–4 still defer W5's field names, `IsError`, and whether `protocoltypes` survives. Row 4 is answerable from the tree — `ToolCallProgress` and `OnToolCallProgress` are used by both emitters, so only the key and the accessor go. | Fold row 1 into A1-4; answer row 4 in place; move row 2 into the §6 branch (C2-05). |
| **m2-08** | Incompleteness | §1.2 | The standing rule ("any future removal of a `ToolResult` field after a release is a hook-contract break") is still only in this spec. It survives A1 intact and remains the document's strongest passage — but a spec is not where the next person deleting a field will look. | Promote it to a doc comment on `tools.ToolResult` as part of W3's cleanup. |

---

## OBSERVATIONS

| ID | Section | Note |
|---|---|---|
| **O2-01** | §2.3 | The FROZEN/REWRITTEN split is the one A1 edit that landed, and it is correct on the facts: all three FROZEN `pkg/agent`/`pkg/tools` entries contain no `ChatStream`, `StreamingProvider` or `ToolCallProgressFromOptions` reference (verified by grep — clean), and `streaming_compliance_test.go` declares no stub, so it survives W1 provided all three implementers are updated. The instrument is sound; the document around it is not. Worth keeping verbatim through the next revision. |
| **O2-02** | §2.1 | Re-verified on this branch and still accurate line for line, including the `write_file` guard at `pkg/tools/filesystem.go:830` — whose in-code comment already names W5, ADR-059 D4 and the §7 gate. That comment is better contract documentation than §6 currently is; the gate's rewrite (C2-05) should be reconciled against it. |

---

## Structural integrity results (plan-spec mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | US-1 (3), US-2 (2), US-3 (1), US-4 (3), US-5 (2) |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** | 4 uncovered — m2-05 (unchanged from pass #1) |
| Every BDD scenario has a `Traces to:` | **PASS** | all 6 |
| Every BDD scenario has a corresponding test in the TDD plan | **FAIL** | still no TDD plan, despite A1-8 — C2-01, M2-06 |
| Every functional requirement appears in the traceability matrix | **PASS** | FR-001…FR-009 — including FR-009, which A1 withdrew (C2-04) |
| Every BDD scenario appears in the traceability matrix | **PASS** | all 6 |
| Test datasets cover boundaries, edge cases, error scenarios | **FAIL** | no datasets section; §3's five edge cases still appear in no scenario, requirement or matrix row |
| Regression impact explicitly addressed | **PARTIAL** | §2.3 is now coherent (O2-01) but §2.2 still undercounts the break (M2-01) |
| Success criteria measurable, no subjective language | **FAIL** | SC-001 unchanged despite A1-6; SC-002 now contradicts §2.3; SC-005 satisfiable against the wrong component (C2-05) |
| Amendment consistent with the body it amends | **FAIL** | six false statements of document state — C2-01 |

---

## Test coverage assessment

**The production-caller bar is the headline.** ADR-059 §8 opens by inheriting ADR-058's rule — *"a
green test that does not exercise a production caller does not satisfy this ADR"* — and the spec
still never restates it. Two of the tests currently on the plan violate it: the `BeforeLLM` half of
§2.3's replacement assertion (M2-02, unfalsifiable post-W1) and A1-7's Anthropic emission test
(C2-03, against a constructor no factory branch reaches).

**Missing negative tests.** US-4's Scenario Outline still covers only "existing file" vs. "unwritable
directory". Absent: a path that exists as a **directory**; a symlink to an existing file; a sandbox
denial that fires *before* the existence check. All three produce error text that must **not** carry
the discriminator, and all three are cheap.

**Missing concurrency test.** AC-05's cross-turn half, unchanged and still frozen (M2-03).

**Missing failure-mode test.** AC-06, still neither tested nor waived (M2-03).

**Missing truncation test.** A1-5 identifies the failure precisely and adds no scenario (M2-04).

**Test level.** The two wrong-level tests survive (M2-09).

---

## STRIDE summary

| Component | Threats considered | Assessment |
|---|---|---|
| `ChatStream` `onProgress` parameter | Information disclosure | Unchanged from pass #1: FR-005 and D2 bound the payload to byte counts. No new finding. |
| Progress record on `turnState` | Info disclosure (cross-turn leakage), DoS (hot-path cost) | Cross-turn isolation still asserted and untested (M2-03). Hot-path cost still unmeasured and unrated. |
| `write_file` refusal payload | Information disclosure | The path already reaches LLM, transcript, WS frame and SPA, so W5 adds no exposure — but `FilterSensitiveData` (`loop.go:10179`) can redact **inside** a JSON payload, breaking parseability. Still not an edge case (M2-04). |
| `PostToolUse` hook payload | Tampering / contract break | §1.2 unchanged and still the best-handled surface. |
| Provider factory → streaming selection | Availability of the safety signal | **New.** OAuth/token Anthropic installs get `ClaudeProvider`, which has no `ChatStream`; the progress signal ADR-059 exists to provide is absent there and the spec asserts the opposite (C2-03). |

No spoofing, elevation-of-privilege or authentication surface is touched by W1–W5.

---

## Unasked questions

1. **Was A1 written against a revision that was then lost?** Six of its statements describe edits with
   enough specificity (naming `GenericToolCall.tsx`, naming the three added sections) that they read
   like a summary of work done, not work planned. If a revised body exists somewhere, this review is
   against the wrong file and that should be established before anything else.
2. **Does `ClaudeProvider` get a `ChatStream`, or does OAuth-Anthropic ship without progress?** This
   is a five-line forwarding method and a product decision, and nobody has made it (C2-03).
3. **What does W1 revert to if AC-03 fails in UAT?** Pass #1 asked; still unanswered. It is a
   signature change across two packages and nine test files, and W2 deletes the fallback mechanism.
   Reverting W1 alone leaves a tree that does not compile.
4. **Does anything teach the orchestrator to read the progress line?** Pass #1 asked; still
   unanswered, and A1-4's third bullet now sharpens it into a hazard — an orchestrator taught that
   silence means "hung" is *more* dangerous on a non-reporting install than one taught nothing.
5. **Why is W5 still in this spec?** Pass #1 asked; A1 does not respond. W1–W4 are a typed-transport
   migration with zero behavioural surface; W5 is the only behavioural change, is gated, and shares no
   code with the other four. Splitting it would let W1–W4 ship and give W5 the contract analysis it
   needs. The spec still never justifies the bundle.

---

## Next action

**Verdict: BLOCK** — 5 CRITICAL.

The document cannot be implemented from, because the reader cannot tell which of the two documents
inside it is authoritative. Address in this order:

1. **C2-01** — resolve the amendment. Either execute the six edits A1 claims, or rewrite A1 as
   instructions. Nothing else can be assessed until the body and the amendment agree.
2. **C2-02 + C2-03** — correct the provider enumeration and decide the `ClaudeProvider` question.
   These change what W1 must touch and what AC-03 can honestly assert.
3. **C2-04** — dispose of FR-009, SC-002 and §9 rows 2 and 9 together.
4. **C2-05** — repoint the W5 gate, answer items 1/2/4 in the body, add the bad-answer branch, pin
   the payload shape.
5. **M2-01** — put the compile-break table in §2.2 and plan the CI round-trips.
6. **M2-03, M2-04, M2-06** — turn A1-5's and A1-7's prose into FRs, scenarios and matrix rows, and
   add the TDD plan with the inherited verification bar at its head.
7. Then the MINORs; m2-01, m2-02, m2-03, m2-06 and m2-07 are corrections, not additions.

Re-run after revision:

```
/grill-spec docs/internal/specs/adr-059-delegation-observability-spec.md
```

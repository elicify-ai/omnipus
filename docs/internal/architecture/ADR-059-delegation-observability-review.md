# ADR-059 review — adversarial grill

- **Reviewed:** `docs/internal/architecture/ADR-059-delegation-observability.md` (Proposed, 2026-08-10, 144 lines)
- **Branch:** `fix/uat-delegation-rootcauses` @ `c9715f5a` (+ uncommitted work in `pkg/agent/loop.go`, `pkg/agent/turn.go`, `pkg/tools/delegate.go`)
- **Baseline the ADR cites:** `release/v0.1.1` @ `ae93a45e`
- **Reviewer mode:** generic-markdown (ADR) — structural checks adapted to this repo's ADR standard, as set by ADR-057 and ADR-058
- **Date:** 2026-08-10

---

## Executive summary

Four CRITICAL, nine MAJOR, ten MINOR, three OBSERVATION. The document's diagnosis (§1) is
strong and its control case is exemplary, but two of its four decisions are contradicted by
code already committed on the branch the ADR sits on: D1 rejects the options-map mechanism
that `pkg/providers/protocoltypes/progress.go` already implements, and D4 rejects a
`ToolResult.Reason` field that is committed, wired into `write_file`, and covered by its own
test file. D1's proposed interface would additionally break under the exact concurrency the
ADR's own evidence describes. The ADR carries none of the work-items / acceptance-criteria /
contract-impact apparatus that ADR-057 and ADR-058 established, so nothing converts these
un-executed rejections into reviewable work.

**Verdict: BLOCK.**

---

## Findings

### CRITICAL

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **C-01** | Incorrectness / Inconsistency | D1 | **D1's decided mechanism is contradicted by committed code on this branch.** `ToolCallProgressCapable` and `SetToolCallProgressHandler` appear nowhere in the repo except inside ADR-059 itself. `pkg/providers/protocoltypes/progress.go` (committed `c36c28a1`) implements the **options-map** mechanism D1 rejects: `const OnToolCallProgressKey = "on_tool_call_progress"`, carried through `options map[string]any`. Its `ToolCallProgressFromOptions` does verbatim what D1 calls disqualifying — "defensively accept two type shapes and return `nil` on anything else" (`case OnToolCallProgress` / `case func(ToolCallProgress)` / `default: return nil`). The file's own doc comment states the opposite rationale to D1: *"passed through the existing `options map[string]any` parameter so no provider interface signature has to change."* The uncommitted `loop.go` change writes `llmOpts[protocoltypes.OnToolCallProgressKey] = …`. | Either withdraw D1 and record the options-map design as decided (with the mitigation the code already implements — see C-02), or keep D1 and add a work item that deletes `OnToolCallProgressKey`, `ToolCallProgressFromOptions`, and the `llmOpts` injection. The ADR cannot stand as a record of this branch in its current form. |
| **C-02** | Incorrectness / Infeasibility | D1, D3 | **D1's setter-based interface is unsafe for the exact scenario the ADR exists to fix.** `SetToolCallProgressHandler` mutates state on a **provider instance**, but provider instances are per-`AgentInstance` and shared across every concurrent turn on that agent: `AgentInstance.Provider` is returned directly by `GetProviderForCandidate` for the unpinned path (`pkg/agent/instance.go:437-447`), and `providerPool` is one `atomic.Pointer[map[string]providers.LLMProvider]` (`instance.go:69`) whose entries are shared pointers built once by `buildProviderPool`. Per ADR-032, parallel delegations to the same target agent all run on that agent's own instance — i.e. the ADR's own wave-1 pair (`b5a76216`, `4259f3c6`) would share one provider. A per-instance setter is last-writer-wins: worker A's progress lands in worker B's liveness record, or is silently dropped. D3's *"a progress handler scoped to the child's spawn call"* is **unachievable** with D1's interface, which offers no per-call scoping. The delivered per-call `options` entry is correctly scoped by construction (the in-flight `turn.go` comment: *"concurrent turns each get their own callback writing into their own state, never into another turn's"*). | If the capability-interface direction is kept, it must be a **per-call** shape — e.g. extend `StreamingProvider.ChatStream`'s signature (which already carries `onChunk func(accumulated string)` as a parameter, `pkg/providers/types.go:43-52`) or introduce a `StreamCallbacks` struct parameter. D1 must state the concurrency requirement explicitly and show the chosen shape satisfies it. |
| **C-03** | Incorrectness | §3 Positive, D1 | **"The capability interface makes a missing implementation a build error rather than silent degradation" is false, and §4 concedes it.** Optional capability interfaces in this codebase are consulted by a runtime type assertion — `if sp, ok := activeProvider.(providers.StreamingProvider); ok` (`loop.go:8335`, and `ThinkingCapable` at `:8207`). A provider that does not implement the interface fails that assertion **silently**; there is no compile error. The only compile-time signal is a hand-written `var _ I = (*T)(nil)`, and on `release/v0.1.1` **no such assertion exists in production code for `StreamingProvider` at all** — the ones that exist are in `_test.go` files. The branch adds `pkg/providers/streaming_compliance_test.go`, i.e. a **test**-compile error, added per provider by exactly the same manual discipline the options-map alternative would require. §4 clause 3 then demolishes the claim outright: the broken Anthropic emitter *"was green against an interface-compliance test."* | Delete or restate the §3 claim. The honest version: an interface plus a deliberately-maintained compliance assertion catches *non-implementation*; it catches nothing about *correct* implementation, which is why §4 clause 3 exists. Note that a compliance assertion in a test file is not a build error for `make build`. |
| **C-04** | Incompleteness / Insecurity | whole doc, D4 | **No contract-impact analysis (Constraint #8), against an explicit precedent that demanded one.** ADR-058 §7 is titled *"Contract impact (Constraint #8) — investigated, not assumed"*, and its item 4 is a direct warning about this exact class: results returned as `*ToolResult` from tool execution, where adding a field without the contract pipeline causes a **silent SPA-edge Zod drop**. ADR-058 §3's last bullet deliberately scoped **out** `PermissionDeniedResult` / `DelegationDeniedResult`, calling the extension *"not free"*. D4 extends the ADR-058 convention into precisely that scoped-out class. On this branch, `write_file`'s failure text is now persisted into `ToolCall.error` — a top-level field on a contract type with `additionalProperties: false` (`contracts/components/schemas/ToolCall.yaml`), mirrored in `pkg/gateway/inboundschemas/ToolCall.yaml`, generated into `src/lib/api/generated/schemas.ts`. Turning `ForLLM` into structured JSON changes what is persisted there and what the SPA renders. ADR-059 contains no §7-equivalent, no `make verify-contracts` statement, and no decision on the SPA rendering change. | Add a contract-impact section modelled on ADR-058 §7. Trace, individually: (a) `ForLLM` → `contentForLLM` → `tcRecord.Error` → `ToolCall.error`; (b) the live `tool_call_result` WS frame; (c) whether the SPA chat surface would begin rendering a JSON blob where it rendered a sentence. State explicitly whether the 5-step pipeline is required. |

### MAJOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **M-01** | Inconsistency | D4 | **D4's rejection is unexecuted; its replacement is unbuilt.** `ToolResult.Reason`, `ResultReason`, `ReasonAlreadyExists` and `WithReason` are committed (`pkg/tools/result.go:35-39, 210-258, 414-424`, commit `0fb79b19`) and in **live use**: `pkg/tools/filesystem.go` returns `ErrorResult(…).WithReason(ReasonAlreadyExists)` at the no-overwrite guard, with a 114-line dedicated test file `pkg/tools/write_file_reason_test.go`. D4 says it was *"superseded here before use"* — it is in use. Meanwhile D4's mandated structured JSON does not exist: `ForLLM` is still the plain sentence `"file: %s already exists. Set overwrite=true to replace."`. | Add a work item that either removes `Reason`/`ResultReason`/`WithReason` and its call site and tests, or amends D4 to keep them. As written the ADR describes a state the branch is not in and provides no path to it. |
| **M-02** | Incorrectness | D4 | **D4's premise is overstated.** D4 asserts the worker *"receives a result it can unambiguously read as precondition already satisfied, not write failed"* is impossible today. But the existing prose already names both the condition and the remedy: `"file: X already exists. Set overwrite=true to replace."` The genuine ambiguity in the incident is different and unaddressed — was the file written by *a sibling doing my job* (task complete) or by something unrelated (task not complete)? A `already_exists` tag does not answer that. D4 never states what the JSON discriminator tells the worker that the sentence does not. | State the discrimination D4 actually buys, in the worker's terms, and show why prose cannot carry it. If the real need is "who wrote it and when", say so — the `clobberNote` audit lookup two branches below in `filesystem.go` is closer to that need than a reason tag. |
| **M-03** | Infeasibility | §4, D4 | **D4 has zero verification clauses and cannot satisfy §4's own bar.** §4's four clauses are entirely about D1/D2/D3 progress reporting. D4's stated outcome — *"the worker concludes its task is effectively done and reports that back to its parent in its own words"* — is a model-behaviour outcome that no deterministic test can assert. §4 opens *"This ADR is only satisfied when a test proves the **outcome**, not the plumbing"* and closes *"A green test that does not exercise a production caller does not satisfy this ADR."* D4 is structurally excluded from both. The ADR's [INFERRED] paragraph concedes the evidence never reached the step, but never concedes the verification gap. | Either add a D4 verification clause that is honestly a *plumbing* test (the result payload contains the discriminator, `write_file`'s guard emits it, the persisted transcript carries it) and amend §4's opening to allow it, or state plainly that D4 is unverifiable by test and is accepted on judgement. Do not leave §4 asserting a bar D4 cannot clear. |
| **M-04** | Incompleteness | §1.4 | **§1.4 omits the amplification cause this branch actually fixed.** The committed `pkg/tools/delegate.go` RC-3 comment reads: *"in one real UAT session 20 of 28 cancel calls hit this branch, and the caller re-issued cancels and re-spawned workers in a loop instead of treating 'already done' as success"* — `executeCancel` returned `ErrorResult` for an already-terminal session. That is 20 of the 28 cancels §1.1 counts, and it is a second, independent driver of the respawn loop. §1.4 lists only steer-ineffectiveness, `write_file` collisions, and dead `bash`. | Add the cancel-error-shape cause to §1.4 with its `delegate.go` citation, and either record the RC-3 fix as a decision in this ADR or name the ADR that owns it. Right now the fix exists only as a code comment with no decision record. |
| **M-05** | Incorrectness / Inconsistency | §3 Positive | **"removes the cause of the cancel/respawn amplification" overclaims and contradicts the ADR's own caveats.** By §1.4's own account there are at least three inputs to the loop (ineffective steer, `write_file` collisions read as this worker's failure, dead `bash`), plus M-04's fourth. Progress visibility removes one input to the operator's cancel decision. D4's [INFERRED] paragraph and §1.4 both undercut the singular "the cause". | Downgrade to "removes one of the inputs", enumerate the others, and cross-reference which decision (or which other change) addresses each. |
| **M-06** | Incompleteness / Inconsistency | header, §1.1 | **The entire evidence base is unreproducible and uncorroborated, and is not tagged as such.** Session `session_01KZJSVAX1QWYFFRA29HEHYGYP` appears nowhere in the repo; no UAT report was committed; the transcripts were read *"off the running machine"*. ADR-058 set the standard for exactly this: its operator-supplied `gateway.log` lines are explicitly marked *"not re-read by the author"* **and** cross-checked against a committed report (`docs/internal/uat/plan-32-task-parallelism-2026-08-05.md`) quoted by line number. ADR-059's header instead asserts *"Every behavioural claim below carries either a transcript figure or a `file:line`"* — silently equating a figure only the author can reach with a citation any reviewer can re-run. | Commit a UAT report under `docs/internal/uat/` carrying the counts, the timeline, and the quoted worker narration, and cite it by line the way ADR-058 does. Failing that, tag every §1.1 figure as operator/author-supplied and not independently verifiable, and stop claiming parity with the `file:line` class. |
| **M-07** | Incorrectness | D3 | **D3 mis-describes the mechanism that exists.** D3: *"The handler updates a liveness record … on `DelegateTaskState`, which `DelegateTool` already owns and mutex-protects."* The in-flight implementation puts the record on **`turnState`** as `atomicToolCallProgress` — plain atomics, with an explicit comment that it deliberately does **not** use `ts.mu` — and gives `DelegateTool` a pull-side `SetProgressReader(al)` wired in `registerSharedTools`. `DelegateTaskState`'s mutex (`delegate.go:365`) is real but irrelevant to this design. D3's stated ownership split (*"the loop owns the signal, `DelegateTool` owns the state"*) is inverted relative to the code: the loop owns both the signal and the state; the tool reads it. | Rewrite D3 to describe the turn-scoped atomic record and the session-keyed pull interface, and drop the `DelegateTaskState` mutex justification. If the ADR genuinely intends the push-into-`DelegateTaskState` design, say so and add the work item that changes the code. |
| **M-08** | Incorrectness | §1.3 Layer 3 | **A load-bearing citation is mis-attributed by ~48 lines.** §1.3 cites *"`recentActivityLines` (`:2484-2569`)"*. On `release/v0.1.1`, `recentActivityLines` is `pkg/tools/delegate.go:2532-2569`; **line 2484 is `delegateStatusExtra`** — the other function the ADR names, separately, in D3. One of the four citations that carry §1.3 points at the wrong function. | Correct to `delegate.go:2532-2569`. Re-check the remaining §1.3 citations (see m-02, m-03, m-04). |
| **M-09** | Incompleteness (structural) | whole doc | **No work items, no acceptance criteria, no issue link, no spec handoff — a clean break from this repo's ADR standard.** ADR-058 ships §5 out-of-scope, §6 work items W1–W6, §7 contract impact, §8 AC-01…AC-09 under an explicitly inherited verification bar, §9 open questions, §10 amendments. ADR-057 ships §5 risk register, §6 work items, §7 alternatives, §10 verification requirements, and a per-finding disposition appendix. ADR-056/055/054 all carry Risks, Confidence, Validation/Next-steps sections. ADR-059 has §1–§4 and 144 lines against ADR-058's 366 and ADR-057's 737. It links **no** GitHub issue (ADR-058 links #594 and resolves #595) and names no follow-on spec (ADR-058: *"A spec follows separately."*). | Add §Work items (one row per change, mapped to a decision), §Acceptance criteria (numbered, each asserting an outcome against a real caller — inherit ADR-058 §8's bar explicitly), §Contract impact (C-04), §Risks, and an issue link. Without work items there is nowhere to record C-01/M-01's un-executed rejections. |

### MINOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **m-01** | Incorrectness | §1.1 | The tool-call breakdown does not sum: 75 `status` + 7 `peek` + 7 `inbox` + 4 `steer` + 28 `cancel` = **121**, against the stated **132** `delegate` calls. The 11 `run` spawns close the gap but are left for the reader to infer. | Add the `run` row, or say "the remaining 11 were spawns". |
| **m-02** | Incorrectness | §1.3 Layer 1 | `loop.go:8334` is off by one — the `StreamingProvider` assertion is at **`loop.go:8335`** on `release/v0.1.1`. | Correct. |
| **m-03** | Infeasibility | §1.3 Layer 1 | `pkg/providers/anthropic/provider.go:31-38` is cited to prove `ChatStream`'s **absence**; those lines are the `Provider` struct and `SupportsThinking`. A line range cannot evidence a negative. (The claim itself is true — `git grep ChatStream` over `pkg/providers/anthropic/` on `release/v0.1.1` returns only a test name.) | Cite the package and the search, not a line range: "no `ChatStream` method exists in `pkg/providers/anthropic/` on `release/v0.1.1`". |
| **m-04** | Incorrectness | §1.3 Layer 3 | `inbox` cited as `:2768-2840`; **2840 is the first line of `executeInboxAck`**. `executeInbox` ends at 2838. | Correct to `:2768-2838`. |
| **m-05** | Inconsistency | D2 | D2's `Index` contract contradicts the committed doc comment. D2: *"provider-defined and stable only within one stream … Consumers must not treat it as a tool-call ordinal."* `protocoltypes/progress.go`: *"Index is the tool call's position in the response (OpenAI's delta index)."* One says ordinal; the other forbids it. | Pick one and make the code comment match. D2's reading is the safer contract. |
| **m-06** | Infeasibility | §4 clause 3 | *"progress fires **between** block start and block stop"* is Anthropic content-block vocabulary applied to *"per implementing provider"*. `openai_compat` has no block start/stop — it has delta accumulation (`provider.go:340-358`). The clause is unsatisfiable as literally written for the provider carrying most traffic. | Restate provider-neutrally: "progress fires more than once, with strictly increasing `ArgsBytes`, before the tool call is complete", then give the Anthropic block-boundary case as the worked example. |
| **m-07** | Incompleteness | §1.4 | *"`bash` was 100% dead (a separate defect, fixed on `fix/uat-delegation-rootcauses`)"* carries neither a `file:line` nor a commit sha, in a document whose header promises one per behavioural claim. (`a79976c4` "size RLIMIT_NPROC in tasks, not processes" and `f7d0f4bf` appear to be it, but the reader must guess.) | Cite the commit and the file. |
| **m-08** | Incompleteness | §3 Negative | The [INFERRED] Anthropic-streamer consequence is **statically determinable** and should have been resolved. `WSHandler.GetStreamer` (`pkg/gateway/websocket.go:476-489`) gates existence on `h.sessions[chatID]` alone and uses `sessionID` only to pick the transcript store and tag the agent — so a child passing `parentTS.chatID` does acquire the parent's connection, and the ADR's conclusion is right. But its stated reasoning (*"`GetStreamer` keying on `chatID`"*) elides that the signature takes `sessionID` too (`pkg/bus/bus.go:144`), which is the first thing a reviewer checks. `SSEHandler.GetStreamer` discards it outright (`sse.go:87`). | Replace the inference with the two citations. The consequence stands; the tag does not need to. |
| **m-09** | Incompleteness | §3 Not decided | *"whether `inspect_session` should surface persisted failure reasons to a parent agent (it currently drops them — a real gap, tracked separately)"* names no tracker. Verified still true (`pkg/tools/inspect_session.go` never reads `ToolCall.Error`, even though the branch now persists it). ADR-058 named #595 for its equivalent. | File the issue and cite its number, or say "not yet tracked". |
| **m-10** | Ambiguity | §1.1, §1.4 | The 11-subagent roster is not reconstructible: §1.1 says 11 subagents for 3 files; §1.4 says 6 replacements for 2 remaining files; §1.2 references "wave-2 SVG workers" (plural) and one Markdown worker. 3 + 6 = 9. | Add a one-line roster: wave-1 (n, ids), replacements (n), wave-2 (n). |

### OBSERVATIONS

| ID | Section | Note |
|---|---|---|
| **O-01** | §1.2 | The control case is the strongest passage in the document and lands ADR-058 §1.2's move exactly: same session, same model, same worker agent, one variable (output length). Keep it verbatim through any revision. |
| **O-02** | D2 | Worth stating in the follow-on spec that `ArgsBytes` is a length and not a digest, so it discloses size only. D2 implies this; saying it removes a reviewer question. |
| **O-03** | — | Consider promoting the RC-3 cancel-idempotency fix (M-04) to a numbered decision here, or to its own ADR. It is the second half of the same root cause and currently has no decision record at all — only a code comment that ends *"do not re-fix this back to `ErrorResult`"*, which is exactly the kind of standing constraint an ADR is for. |

---

## Structural assessment against this repo's ADR standard

Compared against ADR-057, ADR-058 (most recent accepted/proposed) and the ADR-054/055/056 set.

**Conforms:**

- Title form `ADR-NNN: <subject> — <the sharp claim>` — matches 057/058.
- Header block (`Status` / `Date` / `Related` / `Deciders` / `Evidence level`) — field-for-field identical to 057/058, including the `Evidence level: 1` framing and the `[INFERRED]` convention.
- The `> **Scope note.**` blockquote — lifted from ADR-058's, correctly.
- §1 shape — `1.1 The evidence` → `1.2 The control case — this is not <X> indiscipline` → `1.3 Root cause, read in source` → `1.4 Why the existing controls did not help` mirrors ADR-058 §1.1–§1.5 deliberately and well.
- Decisions as `### Dn — <imperative claim>`, with inline `**Rejected: …**` blocks — matches 058 D5's precedent.
- Tone — declarative, evidence-first, willing to state losses. This is on-register for the set.

**Does not conform (see M-09):**

| Element | ADR-057 | ADR-058 | ADR-059 |
|---|---|---|---|
| Work items | §6 W1–W24 | §6 W1–W6 | **absent** |
| Acceptance criteria | §10 (non-negotiable) | §8 AC-01…AC-09 | **absent** (§4 is 4 prose clauses, unnumbered) |
| Contract impact (Constraint #8) | — (no wire change) | §7, explicit | **absent** (C-04: there is a wire change) |
| Risk register | §5 | — | absent |
| Alternatives considered | §7 | D5 + inline | inline only (acceptable) |
| Open questions | §9 | §9 | §3 "Not decided here" (acceptable) |
| Out of scope, named | §6 "Removed from scope" | §5 | absent |
| "What this does NOT fix" | §4 Lost/changed | §3 | §3 Negative (partial) |
| Issue link | specs | #594 / #595 | **none** |
| Follow-on spec named | yes | yes | **none** |
| Review artefact | `-review.md` exists | — | this file |
| Length | 737 | 366 | 144 |

**Tense problem.** The ADR is `Status: Proposed`, but §4 clause 3 narrates implementation
history in the past tense (*"The Anthropic emitter **initially read** `AsToolUse()` … **It was
green** against an interface-compliance test"*), and D4 describes a rejected field as *"Implemented
earlier on this branch."* This is a retrospective record wearing a prospective status. ADR-058
handles the same situation with an explicit `Amended` marker and a preserved-original-text
convention (§10). ADR-059 should either adopt that, or move the retrospective material into §1
and keep §2/§4 forward-looking.

---

## Consistency with ADR-032 / ADR-036 / ADR-057 / ADR-058

| ADR | Verdict | Notes |
|---|---|---|
| **ADR-032** (delegation identity) | **No contradiction, but an unstated dependency.** Nothing in ADR-059 alters identity sourcing. However ADR-032's guarantee — a sub-turn runs as the *target* agent's own instance — is precisely what makes C-02 bite: parallel delegations to one target share that agent's provider instance, so D1's per-instance setter cannot be per-child. ADR-059 should cite ADR-032 for the concurrency requirement rather than only for identity. |
| **ADR-036** (`bash` consolidation) | **Related-link is thin.** ADR-036 is cited in the header but the only contact point is §1.4's one-line "`bash` was 100% dead", which itself carries no citation (m-07). Either strengthen the link or drop it. |
| **ADR-057** (parent/child parity, FR-043) | **No contradiction.** D3 extends `delegateStatusExtra`'s empty branch, which is ADR-057 FR-043's own surface, and the "read the child's own `DelegateSessionID`" re-point is respected. One check for the spec: `recentActivityLines` emits an `slog.Info` on the genuinely-empty path per ADR-057 BDD-51 (*"a genuinely empty activity path must leave a trace"*); D3 changes what an operator sees when that path fires, so confirm BDD-51's assertion still holds. §3's Anthropic-streamer consequence also runs through ADR-057's FR-007 (`subturn.go:1274`, `TranscriptSessionID: childID`) — see m-08. |
| **ADR-058** (denial semantics) | **D4 exceeds ADR-058's scope without doing ADR-058's homework.** D4 invokes ADR-058 as *"the governing precedent"*, but ADR-058 §3's final bullet deliberately scoped **out** the `*ToolResult`-returned refusal class D4 now extends into, and §7 item 4 priced that extension as a contract-pipeline cost with a silent-Zod-drop failure mode. Invoking the precedent while skipping its §7 is the inconsistency. See C-04. Note also that ADR-058's D1 marker (`"permanent":true\|false`) lives in a payload that was *already* JSON; `write_file`'s `ForLLM` is prose today, so D4 is a format change, not a field addition. |

---

## Testability of §4's verification bar

| Clause | Testable? | Assessment |
|---|---|---|
| 1 — status distinguishes generating from idle; must fail on pre-fix code | **Yes**, with a caveat | Drivable with a fake streaming provider that emits argument deltas without completing. The *"must fail on the pre-fix code"* half is a review discipline, not a CI-enforceable assertion — no mechanism runs the test against the pre-fix tree. State it as a review obligation. Positive lower bound is implicitly covered by *"distinguishably from an idle child"* — make it explicit, since a change that always reports "generating" would otherwise pass. |
| 2 — the loop actually installs the handler | **Yes** | Straightforward; the in-flight `toolcall_progress_wiring_test.go` appears to target it. Should additionally assert installation survives a `BeforeLLM` hook returning `HookActionModify` with a nil `Options` map — the ordering hazard the implementation comment calls out. |
| 3 — per-provider, progress fires between block start and stop | **Partially** | Testable for Anthropic; unsatisfiable as literally worded for `openai_compat` (m-06). |
| 4 — race coverage | **Yes** | `-race` with a writer goroutine and a polling reader. Should additionally cover the *cross-turn* case from C-02 — two concurrent turns must not observe each other's progress. As written, clause 4 only covers write/read on one record. |
| **D4** | **No clause exists** | See M-03. |

**Overall:** the bar is real and better than most, but it verifies three of four decisions,
omits the cross-turn concurrency case that C-02 identifies as the live hazard, and is stated as
prose rather than the numbered, individually-citable ACs that ADR-058 established.

---

## Unasked questions

1. What happens to the progress record when the child's turn ends? Is a stale
   `lastActivityUnixNano` distinguishable from a live one, or does a completed child keep
   reporting its last byte count? D3's example string (*"last progress 1.3s ago"*) implies
   freshness matters but nothing bounds staleness.
2. What does `status` show for an `Is3P` (external-CLI) child? `delegateStatusExtra` returns a
   fixed note before it ever reaches the activity path, so external-CLI workers keep the exact
   blindness this ADR describes. Deliberate, or an unnoticed gap?
3. Does progress reporting change the *orchestrator's* prompt guidance at all? The evidence
   shows an agent polling 75 times in 46 seconds. Better data with the same polling discipline
   is still 75 polls. Is anything expected to change on that side, and is that in scope?
4. What is the throttle? D3 mandates the handler be "cheap, non-blocking" and says "the
   implementation is responsible for its own throttling", but names no rate and no measurement.
   §3 says "measured before merge" — measured how, against what threshold?
5. Should `steer` delivery mid-round be reconsidered now? §3 defers it, but §1.4 shows steer was
   the operator's first attempt and it silently did nothing — a worse failure than the one being
   fixed, since the orchestrator got no indication its steer was un-deliverable.
6. Does the ADR intend `bash`'s background-dispatch and `write_file` to get the same liveness
   treatment, or is this delegation-only? A long `bash` and a long tool argument are the same
   blindness from the reader's side.

---

## Next action

Verdict: **BLOCK** — 4 CRITICAL.

Address C-01 through C-04 first: they determine whether D1 and D4 describe the system that is
being built. Then M-01/M-07 (bring D3/D4 into line with the code), M-06 (commit the evidence),
M-09 (work items, ACs, contract impact, issue link), and the citation corrections
(M-08, m-01…m-04).

Re-run after revision:

```
/grill-spec docs/internal/architecture/ADR-059-delegation-observability.md
```

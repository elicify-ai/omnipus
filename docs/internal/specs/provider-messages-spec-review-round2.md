# Adversarial Review: Provider Messages (provider/LLM error presentation, #711) — Round 2

**Spec reviewed**: `docs/internal/specs/provider-messages-spec.md` (commit `93d7e7c13`, after the round-1 fix and the D9 follow-up)
**Round-1 review**: `docs/internal/specs/provider-messages-spec-review.md` (finding IDs CRIT-001…OBS-003 there; this round uses IDs from 101 upward to avoid collisions)
**Mode**: plan-spec (BDD scenarios, FR-xxx, SC-x, traceability matrix present)
**Round**: Round 2 of 2 (the last grill round — per the feature flow, any finding still blocking after the fix goes to the founder)
**Review date**: 2026-09-26
**Verdict**: **REVISE**

Certainty labels: **Verified** = read in code on `feat/provider-messages` @ `93d7e7c13` during this review (symbol cited). **Inferred** = reasoned from code or from known provider/SDK behaviour, not executed. GitNexus MCP tools were not connected in this session. The exploration used Read/Grep only, so blast-radius statements are Inferred.

## Executive Summary

The round-1 fix is real: D1–D9 are applied, the assembly points (MAJ-001) are now correct, and the billing lockout is gone. But the spec's central P0 mechanism, "retry the chosen model 3 times, then fall back", is placed where the code cannot deliver it (MAJ-101), and the event it forwards to the browser has seven other retry reasons behind it that would all be shown as "{provider} is busy" (MAJ-102). The new `model_retired` template also turns an existing copy-rule test red (MAJ-103). The widened SPA trust rule would show internal own-limiter text on replay (MAJ-104), and the #711 oracle, as written, can never pass (MAJ-105). None of these is a production-incident or data-loss defect, so there is no CRITICAL. Ten MAJOR findings mean the spec cannot go to RED unchanged.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 10 |
| MINOR | 6 |
| OBSERVATION | 3 |
| **Total** | **19** |

---

## Findings

### CRITICAL Findings

None. The defects below produce wrong behaviour or a test plan that cannot pass. They do not leak secrets or corrupt data. D5 (Verbose raw text) is a recorded founder risk acceptance, so it is not re-raised.

---

### MAJOR Findings

#### [MAJ-101] Retry-then-fallback cannot be built where §7.4 puts it

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: §7.4 "Policy … generalise the existing delegated pattern (`loop_provider_retry.go::callProvider`) to root turns"; §7.4 "within one turn the retry loop owns the current candidate and does not consult the chain; a candidate's retries exhaust (or are skipped) before `FallbackChain.Execute` moves to the next candidate"; C-8, C-9; FR-005; TDD row 8 chain rows.
- **Description** (all Verified):
  1. `callProvider` wraps `callProviderOnce`, and `callProviderOnce` wraps the **whole** chain: `rt.al.fallback.Execute(...)` when `len(rt.activeCandidates) > 1` (`pkg/agent/loop_run_turn.go::callProviderOnce`). A retry loop at the `callProvider` level therefore re-runs the entire chain. It never re-calls one candidate.
  2. Inside `pkg/providers/fallback.go::FallbackChain.Execute`, a 429 is `FailoverRateLimit`, which `IsRetriable()`. So the chain calls `fc.cooldown.MarkFailure(...)` and moves to the next candidate **at once**. By the time the outer loop could retry, the primary is already in cooldown and the next pass skips it (`!fc.cooldown.IsAvailable(cooldownKey)` → `Skipped: true`).
  3. The only other place for the loop is inside the per-candidate `run` closure. But each candidate gets `fc.candidateBudget(...)`, capped at `perCandidateTimeout`, and `defaultPerCandidateTimeout = 120 * time.Second`. One honoured `retry-after: 120` wait uses up the candidate's whole budget. The next call then fails with `DeadlineExceeded`, which is classified `FailoverTimeout`: wrong code, wrong cooldown, wrong terminal line.
- **Impact**: For every agent with a Fallback model (the case US-6 and AU-1 are built around), the spec as written yields one of two outcomes. Either (a) no per-model retry happens at all and the chain jumps straight to the fallback (today's behaviour; the countdown never shows), or (b) the countdown shows for up to 2:00 and the turn then dies as a timeout. Unit tests written to the §7.4 text can pass against a single-candidate harness while the real multi-candidate path does neither. This is the "documented but unwired" pattern.
- **Recommendation**: Name the real placement and its budget rule. Recommended:
  - Put the per-candidate retry loop **inside `FallbackChain.Execute`**, around `run(...)` for a `FailoverRateLimit` verdict, before `MarkFailure`. Alternatively, put it in the closure, with the chain told not to mark failure until the loop gives up.
  - Exclude the wait time from the candidate budget. Create a fresh `candidateBudget` per call, not per candidate, and keep the sleep context-cancellable on the **turn** context (C-12).
  - Keep the single-candidate path in `callProvider` (it already works there).
  - Add TDD rows that run through the real `FallbackChain.Execute`, not a stub: "2 candidates, primary 429 `retry-after: 3` then success → the primary answers on call 2, no fallback, no cooldown mark", and "primary 429 × 3 → exactly one `MarkFailure`, then the fallback".
  - State the new worst-case turn duration (see OBS-101).

---

#### [MAJ-102] Forwarding `EventKindLLMRetry` as `provider_retry` would announce seven non-provider retries as "{provider} is busy"

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: §7.4 "`hubSyncTap` gains a handler forwarding `EventKindLLMRetry` to the new `provider_retry` frame (the kind moves off the explicitly-not-forwarded list)"; §4 non-behavior "Transport/timeout inline retries … remain unannounced (MIN-001)"; PM-5 / C-23 ("no extra note" for context length); §13 item 6; C-8; F4 (which lists "streaming resets" as a problem).
- **Description** (Verified): `EventKindLLMRetry` has **eight** emitters with seven `Reason` values. Only one of them is a provider rate limit:

  | Emitter | `Reason` | `Attempt` / `MaxRetries` semantics |
  |---|---|---|
  | `loop_provider_retry.go::callProvider` | `rate_limit` | `retry+1` (1..2) / `delegatedRateLimitMaxRetries` = 2 |
  | `loop_run_turn_response.go` (~273) | `streaming_reset` | `retry+1` / `cr.maxRetries` |
  | `loop_run_turn_response.go` (~503) | `timeout` | `retry+1` / `cr.maxRetries` |
  | `loop_run_turn_response.go` (~560) | `context_limit` | `retry+1` / `cr.maxRetries` |
  | `loop_run_turn_iterations.go` (~145) | `empty_response` | counter / `maxEmptyResponseRetries` |
  | `loop_run_turn_iterations.go` (~489) | orphan-tool-markup repair | counter / max |
  | `loop_truncation.go` (~209, ~292) | truncated tool call / truncation continue | 1/1; rounds / max |

  The spec forwards the event **kind** with no reason filter. It also defines `attempt` as "the call about to be made (2..3)" (C-8), while the existing delegated emitter sends 1..2 of 2.
- **Impact**: A context-overflow recovery would show "OpenRouter is busy. Retrying automatically…", which breaks the founder's PM-5 ruling ("no extra note") and C-23. A timeout retry would be announced, contradicting the spec's own §4 non-behavior. An empty-response or truncation repair (the model's fault, not the provider's) would be blamed on the provider. A delegated child would show "attempt 1 of 2" and violate C-8.
- **Recommendation**: The hub handler must forward **only** `Reason == "rate_limit"` from the rate-limit retry path. Every other reason stays unforwarded. Better: give the provider rate-limit retry its own event kind (e.g. `EventKindProviderRetry`) so the filter cannot drift. State the delegated path's attempt mapping explicitly: either convert it to C-8 semantics or stop forwarding delegated retries. Add a TDD row that emits each of the seven other reasons and asserts no `provider_retry` frame. Remove "streaming resets" from F4, or scope it in explicitly.

---

#### [MAJ-103] The `model_retired` template breaks the existing `config` copy rule, and its fallback sentence is unspecified

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: D9 (attribution `config`; wording "…— choose another model."); §6 "`model_retired` is `config` (D9), so its template advises a config change … and never retry advice"; §7.1 item 1; §11 note "copy-rule tests — extended to 25 codes"; SC-6.
- **Description** (Verified): `pkg/agent/translate_error_test.go::TestUserMessages_OurFaultsDoNotSendUsersModelShopping` bans `"another model"` (plus "different model", "pick a model", …) for **every** `product` and `config` code. `src/lib/llm-error.test.ts` has the same list ("never tells the user to switch models when the fault is ours or the operator's"). The founder's D9 wording contains "choose another model", and D9 attributes it `config`. §6 claims the template satisfies the copy rules; it does not. Separately, the spec defines only the `provider_message` template for `model_retired`. It never gives the plain catalogue `message` used when facts are absent, and the generator's bijection (C-3) requires one.
- **Impact**: Either the copy-rule test goes red, or someone "fixes" it by exempting the templated variant from copy rules. That quietly creates a loophole for every future `provider_message`. The facts-absent path also has no defined text to render.
- **Recommendation**: This is a founder-visible wording conflict, so do not resolve it silently. Options:
  - **A**: keep D9's wording and add a narrow, named exception in both copy-rule tests for `model_retired` ("switching IS the fix when the model is retired"), with a comment.
  - **B**: reword to avoid the banned phrase while keeping the meaning, e.g. "This model is no longer offered by {provider}. Pick a new model in the agent's settings." ("pick a new model" is not on the banned list; this mirrors `model_unassigned`).
  - **C**: attribute `provider` instead of `config`.

  Recommend **B** (it mirrors existing copy) and confirm it with the founder, since D9 fixed the exact wording. Also specify the catalogue `message` for the facts-absent case, and state that the copy-rule tests run over the `provider_message` variants too.

---

#### [MAJ-104] The SPA's code-keyed allow-list would render internal own-limiter text on replay

- **Lens**: Insecurity (trust boundary) / Inconsistency
- **Affected section**: C-14; §7.3 step 3 ("renders `le.message` for the templated codes only … a stated trust-boundary change"); §8 item 6; RG-2 ("Then the text is today's own-limiter copy, never '{provider} is busy…'"); §13 item 5.
- **Description** (Verified):
  1. The own-limiter (SEC-26) persists its denial with `Code: CodeRateLimited`, `Message: "rate limit: <PolicyRule> (retry after Ns)"` (`pkg/agent/loop.go`, the `appendClassifiedError(EventKindRateLimit.String(), "rate_limit", …)` call). The writer keeps that text: `{"rate_limit","rate_limit"}` is in `trustedInternalStageSet`, and `writeErrorTranscriptWithAbandonment` sets `written = message` for `CodeRateLimited`.
  2. Replay sends `entry.Content` as `LLMErrorReplay.Message` (`pkg/gateway/replay.go`, the `ReplayErrorPayload` build).
  3. Today `getLLMErrorDisplay` ignores `le.message` for `rate_limited`, so replay shows catalogue copy. Once `rate_limited` joins the allow-list (it is a templated code: the terminal line), a reloaded session shows "rate limit: llm_calls_per_minute (retry after 30s)". That is an internal policy identifier the SPA deliberately hid until now.
- **Impact**: RG-2 as written contradicts C-14. "Today's own-limiter copy" on replay is the catalogue sentence, and after the change it becomes the internal string. More generally, the allow-list trusts **every producer** of those four codes, not just the new assembly path. Any future producer that writes a raw string under one of those codes goes straight to the screen, and the SPA's defence-in-depth for those codes is gone.
- **Recommendation**: Do not key trust on the code alone. Pick one:
  - (a) Persist a flag or subtype with the assembled sentence (e.g. `TranscriptEntry.SystemSubtype = "provider_message"`, carried on `LLMErrorReplay`). The SPA renders `le.message` only when that flag is set.
  - (b) Change the own-limiter to persist catalogue copy (or its own code) so `rate_limited` rows are always provider-assembled.

  Rewrite RG-2 with an exact oracle: "a replayed own-limiter denial renders <exact string>".

---

#### [MAJ-105] The #711 oracle (DG-2, SC-2, TDD row 15) can never pass, because D1 puts the body on the wire

- **Lens**: Infeasibility / Inconsistency
- **Affected section**: DG-2 ("no frame payload of ANY type contains the sentinel substring"); SC-2 ("A scan over ALL frame payloads in the e2e WS log … finds zero provider-body sentinel substrings"); TDD row 15; C-1/C-2.
- **Description** (Verified): D1 keeps `detail` on every error frame. `pkg/agent/translate_error.go::buildDetail` builds `"status=<n> body=<scrubbed preview, 512 chars>"`, and `pkg/gateway/websocket_forward_hub.go::hubError` sets `Detail: &detail`. A sentinel placed in the provider body **will** appear in the error frame's payload by design.
- **Impact**: The oracle is red on correct code. The predictable "fix" is to weaken the scan (exclude error frames, or drop the sentinel from the body). That is exactly the silent weakening the false-green rules warn about, and it could also blind the check to a real regression on the new frames.
- **Recommendation**: Restate the oracle to match D1:
  - Scan 1: the sentinel appears in **no `provider_retry`/`provider_fallback` payload** and in **no error-frame field other than `payload.llm_error.detail`**.
  - Scan 2: the sentinel appears nowhere in the **non-Verbose DOM**.
  - Scan 3 (positive control): the sentinel **does** appear in the error frame's `detail`. This proves the scan can see the body at all.

  Keep the DG-3 mutation check. Update SC-2 and TDD row 15 to the same wording.

---

#### [MAJ-106] Billing detection is internally contradictory, misses Anthropic's wording, and leaves the two classifiers diverging

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: C-5; B-1 Examples (rows `400 | "credit balance too low"` and `500 | "payment required"`); §12 D2 closing note ("billing-shaped prose on a non-402 status … no longer classifies `quota_billing` — the 402 status and the structured `insufficient_quota` field are the account-state signals"); §7.3 user side and providers side.
- **Description**:
  1. **Self-contradiction.** C-5 and B-1 say three prose phrases classify `quota_billing` on any status. The §12 note says non-402 prose never does. The D2 table itself has `429 | prose "insufficient credits" → quota_billing`, which is non-402 prose. RED will write whichever version it reads first. (Verified in spec.)
  2. **Unreachable rows.** `classifyByHTTPStatus` is status-first. `status >= 500` returns `CodeNetwork` before any body check, and a 400 reaches the body detectors only inside the 4xx branch (Verified, `pkg/agent/translate_error.go::classifyByHTTPStatus`). §7.3 places the new detector "before `rateLimitSubstrings` on body-bearing paths, and on the 429 path". Neither placement covers B-1's 400 or 500 rows. On the routing side, `classifyByStatus` maps 5xx to `FailoverTimeout` without reading the body (Verified), so even a fixed user side would disagree with routing on the 500 row.
  3. **Anthropic missed.** Anthropic's out-of-credit error reads "Your credit balance is too low to access the Anthropic API" (Inferred, high confidence, known wording; round 1 proposed "credit balance **is** too low"). The C-5 phrase "credit balance too low" is **not** a substring of it. Anthropic's billing failure therefore stays `CodeUnknown`. It even qualifies for the media strip-retry gate, since it is 4xx, not 401/403/413, and `CodeUnknown`.
  4. **Divergent classifiers.** The routing side keeps `billingPatterns` = `\b402\b` (a regex on **text**, which matches any "402" token), "payment required", "insufficient credits", "credit balance", "plans & billing", "insufficient balance" (Verified). §7.3 moves "the C-5 phrases" ahead of rate-limit, but never says the routing list is narrowed to C-5. The two classifiers then give different verdicts on the same error. The spec also never says **which** classifier's verdict decides "retry this candidate" (the existing delegated gate uses the routing one: `shouldRetryDelegatedRateLimit` → `providers.ClassifyError`).
- **Impact**: The most common billing failure for Anthropic users gets "we can't tell why" instead of the out-of-credit line. A 500 with "payment required" in an error page may become a non-retryable billing verdict. And one error can be retried as a rate limit by one classifier while the other shows "out of credit".
- **Recommendation**:
  - Pick one rule and delete the other text. Recommended: "402, or the structured `insufficient_quota` field, or one of the C-5 phrases on a **4xx** status; never on 5xx."
  - Fix the phrase to "credit balance is too low" (keep "credit balance too low" as well if wanted) and add the Anthropic wording as a D2 row (`400 → quota_billing`).
  - State where in `classifyByHTTPStatus` the detector runs (inside the 4xx branch, before the media detectors and the residual `CodeUnknown`), and delete the 500 B-1 row.
  - Make the routing `billingPatterns` equal to C-5 (drop the `\b402\b` text regex, "plans & billing" and bare "credit balance").
  - State that the retry decision reads the user-side code (C-8/C-9 speak in `rate_limited` terms) or the routing reason, and add a D2 test asserting both classifiers agree on every row.

---

#### [MAJ-107] The new non-retryable codes are missing from the operator-only set, so task runs and the Judge keep retrying them

- **Lens**: Incompleteness
- **Affected section**: §2 symbols table and impact table (no mention); §7.3 (`isRetryable` returns false for `quota_billing` and `model_retired`); D9 (attribution `config`).
- **Description** (Verified): `pkg/agent/operator_only_turn_error.go::classifyOperatorOnlyTurnError` is "the ONE classification of turn errors that only an operator can clear". It is read by the Judge (`judgeDispatchNeedsOperator`, withheld at once instead of retried on `judgeRetryBackoff`) and by task runs (`task_run_loop.go::finishRunTurn`: "ends the task Failed at once, with the reason and how to fix it — no task attempt used, no restart"). Its header states the membership rule: codes attributed `config` (or `user` for expired sign-in) that `isRetryable` reports false for. `model_retired` meets that rule exactly, and `quota_billing` meets it if Q4 lands on `config`. Neither appears in the spec.
- **Impact**: A scheduled task whose model was retired, or whose account ran dry, burns its attempts and restarts on a failure that no wait can clear. The operator sees "failed after N attempts" rather than "choose another model" or "out of credit". The impact table omits this consumer entirely.
- **Recommendation**: Add `model_retired` to `classifyOperatorOnlyTurnError` (new `operatorFixModelRetired` cause, with task/Judge wording). Tie `quota_billing`'s membership to the Q4 ruling: in the set if `config`. Add both to §2's symbol table and impact table, with a unit row per consumer.

---

#### [MAJ-108] A newly attached tab cannot show the countdown (A-8), and `retry_at` assumes browser and server clocks agree

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: US-1/8, A-8 ("a second tab attaches (or the WS reconnects) … the frame replays"); C-11; FR-006; §7.4 "a tab attaching mid-wait renders the remaining time".
- **Description**:
  1. (Verified) Attach has two paths (`pkg/gateway/ws_session_hub.go` header): an **incremental** catch-up from the per-session journal (reconnect with a servable cursor), and a **snapshot** built from the persisted transcript plus the active-turn **projection** (`pkg/gateway/ws_hub_projection.go`). The projection holds only tokens, running tools and turn-level "items" (`hubKindToken/ToolStart/ToolResult/Item`). A fresh second tab gets the snapshot. A `provider_retry` frame is neither persisted nor a projection item, so that tab shows no indicator at all. The spec never mentions the projection.
  2. (Verified mechanism; skew size Inferred) C-11 has the client compute `retry_at − now` with the **browser's** clock against the **server's** wall clock. No clock-offset handling exists in `src/` (grep for skew/server-time: none). The existing `RateLimitIndicator` counts relative seconds. On a self-hosted install reached from another machine, a skew of tens of seconds shows "0:00 / Retrying now…" too early or a countdown longer than the wait.
- **Impact**: A-8 passes on reconnect-with-cursor and fails on a real second tab, the more common case. The countdown can be visibly wrong without anyone noticing in CI, where both clocks are the same.
- **Recommendation**:
  - Make `provider_retry` a projection item (`hubKindItem`), keyed per turn and **replaced** by each newer retry frame, then cleared by the terminal/done frame. Add an A-8 variant for "fresh tab, snapshot path".
  - For skew, have the frame carry `retry_after_seconds` **and** a server `sent_at`, and have the client derive the offset. Alternatively, stamp server time on attach/hello and correct once per connection.
  - Add a component test with a fake client clock offset of ±30 s.

---

#### [MAJ-109] The `model_retired` trigger is broad enough to make false claims and to pull 404s off the strip-retry gate

- **Lens**: Incorrectness
- **Affected section**: C-24 ("404 AND a body that names the requested model id verbatim, or matches … 'model not found', 'does not exist', 'has been decommissioned', 'no longer supported'"); MR-1; D2 model-retired rows; §13 item 10.
- **Description** (provider wordings Inferred, medium-high; code paths Verified):
  - OpenAI answers 404 "The model `X` does not exist **or you do not have access to it**" for an org without access to a live model.
  - Ollama answers 404 "model 'llama3' not found, **try pulling it first**" for a model that was never downloaded.
  - Gateways answer 404 bodies that echo the model id for region or plan restrictions.

  All of them name the model and/or contain a C-24 phrase, so all would render "This model is no longer offered by {provider}", a confident false statement. C-24 also says the residual gate "is NOT re-pointed". But any media-bearing 404 whose body happens to echo the model id moves from `CodeUnknown` to `model_retired`, and `outcomeFallbackEligible` (Verified: fires only on `CodeUnknown`) stops firing for it. MR-2 only tests a body with **no** model id, so it cannot see this. Finally (Verified), the routing classifier maps 404 to no reason (`classifyByStatus` has no 404 case), so `FallbackChain.Execute` returns "unclassified error … do not fallback". A retired primary therefore never reaches the configured Fallback model; the spec is silent on this.
- **Impact**: Users are told a model is retired when it is merely not pulled or not permitted. The media strip-retry recovery silently stops for a class of 404s. And the one situation where a Fallback model would help is the one that never falls back.
- **Recommendation**:
  - Drop "names the requested model id" and "does not exist" as sufficient triggers. Keep only explicit retirement phrases ("has been decommissioned", "no longer supported", "deprecated and removed", plus provider-pinned codes like `model_not_found` **with** a retirement phrase).
  - Add D2 near-miss rows for the OpenAI access-denied 404 and the Ollama not-pulled 404 (expected: not `model_retired`).
  - Add an MR-3 row: "media-bearing 404 whose body echoes the model id, no retirement phrase → `CodeUnknown`, strip-retry gate fires".
  - Decide (founder-visible) whether a retired primary should fall back to the Fallback model. If yes, map the C-24 verdict to a retriable routing reason.

---

#### [MAJ-110] Fact capture at `HandleErrorResponse` does not reach every adapter, and it is the harder of two places

- **Lens**: Incompleteness / Overcomplexity
- **Affected section**: §7.2 ("parsed at `HandleErrorResponse` and `WrapHTMLResponseError`"; "MAJ-006: the producing provider id + model are stamped onto `ProviderError` at the boundary (`HandleErrorResponse`)"); C-16; FR-001.
- **Description** (Verified unless marked):
  - `common.HandleErrorResponse(resp *http.Response, apiBase string)` has no provider-id or model parameter.
  - Only three adapters call it or `WrapHTMLResponseError`: `openai_compat`, `azure`, `anthropic_messages`. The SDK-backed `pkg/providers/anthropic/provider.go` wraps errors with `fmt.Errorf("claude API call: %w", err)`, and the CLI-backed and codex adapters have their own paths. For those, no `retry-after` is captured and no identity is stamped.
  - Meanwhile the chain already knows the identity of every attempt: `Execute` calls `ClassifyError(err, candidate.Provider, candidate.Model)`, and `FailoverError` carries `Provider`/`Model` into `FallbackExhaustedError.Attempts`, which `errorToProviderError` already walks.
  - (Inferred) anthropic-sdk-go retries 429s internally by default (2 retries, honouring `retry-after`), and the adapter does not set `option.WithMaxRetries(0)`. On that path the feature's 3 calls become up to 9, with silent SDK waits outside the countdown.
- **Impact**: Every signature change to `HandleErrorResponse` ripples through its callers, and the SDK adapters still end up with the catalogue fallback. The spec then reports provider naming as delivered while one adapter family never gets it.
- **Recommendation**:
  - Take identity from the per-candidate attempt (`FailoverError.Provider/Model`, or the `run` closure in `callProviderOnce`, which has `provider, model`), not from `HandleErrorResponse`.
  - Keep header parsing in `HandleErrorResponse` for the HTTP adapters. List the adapters that will **not** get `retry-after` facts and accept the backoff schedule for them explicitly.
  - Decide whether to set `WithMaxRetries(0)` on the SDK adapter so the countdown is the only retry.
  - Add a D3 row: "SDK-adapter 429 → provider named from the attempt, backoff schedule".

---

### MINOR Findings

#### [MIN-101] `retry-after: 0` is handled three different ways

- **Lens**: Inconsistency / Overcomplexity
- **Affected section**: §7.2 ("presence-distinguishable — a pointer or explicit flag, because `retry-after: 0` is legal and means 'immediate'"); C-9 ("`retry-after: 0` and a missing header both take the backoff schedule (immediate first backoff step)"); §7.4 ("exponential backoff 2 s × 2"); D1 row `zero → fact absent; backoff schedule`.
- **Description**: §7.2 builds a pointer to tell 0 apart from absent, but C-9 and D1 then treat them identically, so the pointer changes nothing. C-9's "immediate first backoff step" contradicts §7.4's 2 s first step.
- **Recommendation**: Treat 0 as absent (the D1 row), drop the pointer requirement, and delete "(immediate first backoff step)" from C-9.

#### [MIN-102] "Cooldown only matters across turns" is false: a turn makes many provider calls

- **Lens**: Incorrectness
- **Affected section**: §7.4 "The ≥ 60 s standard cooldown a failed candidate picks up only matters across turns".
- **Description** (Verified): a turn runs several iterations (tool loop), and each calls `callProviderOnce`. After the primary fails in iteration 1, iteration 2 of the **same** turn skips it for cooldown. It is not stated whether each iteration can start a fresh 3-call retry cycle on a candidate that becomes available again mid-turn, or whether retry frames and fallback notes repeat per iteration.
- **Recommendation**: Replace the sentence with the per-iteration rule. Recommended: retry state and the fallback note are per turn, and later iterations in the same turn neither re-announce nor re-note the same pair. Add a unit row with 3 iterations.

#### [MIN-103] Fallback-note edge cases are undefined

- **Lens**: Ambiguity
- **Affected section**: §6 fallback note row; C-17, C-18; §7.4 fallback note bullet; US-6.
- **Description**: The spec does not say:
  - which `{unavailable_model}` a multi-hop chain names (Y failed, X1 failed, X2 answered);
  - where the "once per session per pair" state lives (memory, lost on restart, or derived from the transcript);
  - where the persisted note sits relative to the assistant message on replay ("under the reply" live; the ordering of a `TranscriptEntry` written before or after the answer is not stated).
- **Recommendation**:
  - Name the **primary** (first candidate) as `{unavailable_model}`.
  - Derive the once-per-pair check from the session transcript: the last persisted note's pair. It then survives restarts with no new state.
  - Persist the note after the assistant entry, and pin the order in FB-2.

#### [MIN-104] The indicator copy hard-codes "of 3" and hides model switches within one provider

- **Lens**: Ambiguity / Incorrectness
- **Affected section**: §8 item 2 state table ("(attempt {n} of 3)"); §7.4 "a candidate switch issues a new frame with that candidate's identity and a reset attempt counter".
- **Description**: The literal 3 ignores the frame's `max_attempts`. When the chain moves from `openrouter/model-a` to `openrouter/model-b`, the line reads "OpenRouter is busy … (attempt 2 of 3)" again with no visible change, and the counter appears to go backwards.
- **Recommendation**: Render `{max}` from the frame. Decide whether the live line names the model when the provider is unchanged (e.g. "OpenRouter (model-b) is busy…"), and add a D3 row for it.

#### [MIN-105] Structural gaps in traceability

- **Lens**: Incompleteness (structural)
- **Affected section**: §10, §11, §15.
- **Description** (Verified by grep of the spec): acceptance scenarios US-5/2 (registered credential scrubbed) and US-4/3 (landing order) have no BDD scenario. BDD scenarios AU-3, RG-1 and RG-2 are cited by no numbered TDD row (the FR-009 matrix cell points AU-3 at row 15, but row 15 traces only DG-1…DG-3). §15 claims every TDD row cites only defined IDs and every scenario is covered; the first claim holds, the second does not.
- **Recommendation**: Add a BDD scenario for US-5/2, or mark it "existing test, unchanged" with the test name. Mark US-4/3 as a process item outside BDD. Add TDD rows for AU-3 (a key-shaped fragment in an auth body is absent from message and facts), RG-1 (byte-identical catalogue) and RG-2 (own-limiter replay string, see MAJ-104).

#### [MIN-106] Q4 is open, but the contract wave that depends on it is first in the order

- **Lens**: Inoperability (process)
- **Affected section**: §7.1 item 1 ("attribution per Q4 — placeholder `provider` until ruled"); §16 Q4; FR-015 (contract first, one atomic commit).
- **Description**: The contract commit is the first thing to land, and it fixes `quota_billing`'s attribution. With `provider` as the placeholder, nothing forbids retry advice, and the operator-only membership (MAJ-107) changes with the answer. The contract would have to be re-cut after the founder answers.
- **Recommendation**: Get Q4 answered before RED starts (it gates the contract, the copy rules and MAJ-107). Otherwise state that the recommendation (A, `config`) ships unless overruled.

---

### Observations

#### [OBS-101] No bound on total turn time under retry-then-fallback

- **Lens**: Inoperability
- **Affected section**: §7.4; D3.
- **Suggestion**: The worst case per candidate is 2 waits of up to 120 s plus 3 calls. With 3 candidates, a user watches countdowns for over 12 minutes before the terminal line. State the accepted worst case, or cap total wait per turn (e.g. 4 minutes across all candidates), and put the number in C-9.

#### [OBS-102] C-21 describes a fix that is already in place

- **Lens**: Overcomplexity
- **Affected section**: C-21; FR-010; §8 item 4 ("The disclosure's content rendering is tightened").
- **Suggestion**: `src/components/chat/MessageItem.tsx` already renders `{message.errorDetail.slice(0, ERROR_DETAIL_MAX_CHARS)}` as a React text node, which is inert (Verified). Only the JSON pretty-print is new. Call DG-5 a regression guard, not a fix, so the gate does not count it as delivered hardening.

#### [OBS-103] Wire fields with no consumer

- **Lens**: Overcomplexity
- **Affected section**: §7.1 items 1 and 3 (`facts.retry_after_seconds`, `retry_at`, `attempts`, `max_attempts` on the **error** frame; `reason` on `provider_fallback`).
- **Suggestion**: No SPA surface reads these on the error frame (the Verbose facts lines are provider, model, request id), and the note text is identical for both `reason` values. Drop them, or name the consumer. Each extra wire field is a contract to keep in lockstep across `LLMError.yaml` and the inline asyncapi copies.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1…US-8 all have numbered scenarios |
| Every acceptance scenario has BDD scenarios | FAIL | US-5/2 and US-4/3 have none (MIN-105) |
| Every BDD scenario has `Traces to:` reference | PASS | All 32 scenarios carry one |
| Every BDD scenario has a test in TDD plan | FAIL | AU-3, RG-1, RG-2 have no numbered TDD row (MIN-105) |
| Every FR appears in traceability matrix | PASS | FR-001…FR-016 |
| Every BDD scenario in traceability matrix | PASS | |
| Test datasets cover boundaries/edges/errors | FAIL | D2 lacks Anthropic "credit balance is too low" (MAJ-106), OpenAI access-denied 404 and Ollama not-pulled 404 near-misses, media-404 echoing the model id (MAJ-109); D3 lacks an SDK-adapter row (MAJ-110) and a same-provider model switch row (MIN-104) |
| Regression impact addressed | FAIL | Misses the operator-only set (MAJ-107), the seven non-rate-limit `EventKindLLMRetry` reasons (MAJ-102), and own-limiter replay text (MAJ-104) |
| Success criteria are measurable | FAIL | SC-2 is measurable but unachievable on correct code (MAJ-105) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Integration through the real chain | Retry-then-fallback tested only as "unit rows"; must run through `FallbackChain.Execute` with its per-candidate budget | A-1, A-4, A-5, TDD row 8 (MAJ-101) |
| Negative: event filtering | No test that `streaming_reset`/`timeout`/`context_limit`/`empty_response`/truncation retries produce no `provider_retry` frame | A set, RG-1 (MAJ-102) |
| Snapshot attach | Fresh-tab snapshot path, not only journal replay | A-8 (MAJ-108) |
| Clock skew | Countdown with a client clock offset | A-2, A-8 (MAJ-108) |
| Positive control for the sentinel scan | Prove the scan sees the body in `detail` | DG-2, SC-2 (MAJ-105) |
| Consumer regression | Task run / Judge handling of `model_retired` and `quota_billing` | new (MAJ-107) |
| Classifier agreement | Both classifiers agree on every D2 row | B-1, B-2 (MAJ-106) |
| Per-iteration behaviour | Multi-iteration turn after a fallback: no repeated frames or notes | FB-4 (MIN-102) |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D2 billing | Anthropic out-of-credit wording | `400 | "Your credit balance is too low to access the Anthropic API" → quota_billing` |
| D2 billing | Status class | Delete the `500 | payment required` B-1 row, or add a `5xx → network` negative row |
| D2 model_retired | Access-denied and not-pulled 404s | OpenAI "does not exist or you do not have access to it" → not `model_retired`; Ollama "not found, try pulling it first" → not `model_retired` |
| D2 model_retired | Media-bearing 404 echoing the model id | → `CodeUnknown`; strip-retry gate fires |
| D1 retry-after | Value above the per-candidate budget | `retry-after: 120` with a 2-candidate chain → the primary is retried, not timed out |
| D3 assembly | SDK-backed adapter; same-provider model switch | Provider named from the attempt; live line distinguishes models |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Agent-side assembly (`ErrorPayload.Message`) | ok | ok | ok | ok | ok | ok | Slots are catalog names and ids; no body text |
| SPA allow-list (C-14) | ok | ok | ok | risk | ok | ok | Code-keyed trust shows any producer's text for four codes; own-limiter internal string on replay (MAJ-104) |
| `provider_retry` / `provider_fallback` frames | ok | ok | ok | risk | ok | ok | Kind-level forwarding exposes non-provider retries under a provider label (MAJ-102); fields are facts only (C-2 holds if the filter is added) |
| Retry loop | ok | ok | ok | ok | risk | ok | No per-turn wait cap (OBS-101); SDK internal retries multiply calls (MAJ-110, Inferred) |
| Verbose disclosure | ok | ok | ok | accepted | ok | ok | D5 risk acceptance for fragments; rendering already inert (OBS-102) |
| Classifiers | ok | ok | ok | ok | ok | ok | Correctness issues only (MAJ-106, MAJ-109), no security surface |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable, accepted = founder risk acceptance recorded.

---

## Unasked Questions

1. Where exactly does the per-candidate retry loop live relative to `FallbackChain.Execute`, and how does its wait interact with the 120 s per-candidate budget? (MAJ-101)
2. Is the provider rate-limit retry a new event kind, or a `Reason` filter on `EventKindLLMRetry`? And what happens to delegated-turn retry frames? (MAJ-102)
3. Founder: the D9 wording collides with the `config` copy rule. Is option B's rewording acceptable, or is a named test exception preferred? (MAJ-103)
4. How does the SPA know a `rate_limited` message was provider-assembled rather than written by the own-limiter? (MAJ-104)
5. Should a retired primary model fall back to the configured Fallback model, or end the turn? Today the chain aborts on a 404. (MAJ-109)
6. Which classifier decides "retry this candidate" when the two disagree? (MAJ-106)
7. What is the longest a user may watch countdowns in one turn? (OBS-101)
8. Should the SDK adapter's own automatic retries be turned off so the countdown is the only retry? (MAJ-110)

---

## Verdict Rationale

**REVISE.** There is no CRITICAL finding: nothing here leaks key material beyond the D5 acceptance or corrupts data. But MAJ-101 means the P0 story (US-1) cannot be built as written for any agent with a Fallback model, which is exactly the D3 scenario. MAJ-102 would break the founder's PM-5 ruling and the spec's own non-behaviors the day it ships. MAJ-103 and MAJ-105 would turn tests red on correct code, which invites test weakening. MAJ-104 widens a trust boundary further than the spec states. MAJ-106–MAJ-110 are narrower, but each lets a real provider error get the wrong line or the wrong retry behaviour.

This is the last grill round. Per the feature flow, the fix round runs next. MAJ-103 (wording versus copy rule), MAJ-109's fallback question and Q4 (MIN-106) are founder decisions and should be put to the founder before the fix, not decided by the author.

### Recommended Next Actions

- [ ] MAJ-101: move the retry loop into or around `FallbackChain.Execute` with a per-call budget; add real-chain TDD rows
- [ ] MAJ-102: forward only provider rate-limit retries (new event kind or `Reason` filter); add negative rows for the seven other reasons
- [ ] MAJ-103: founder picks A/B/C for the `model_retired` wording; specify the facts-absent catalogue sentence
- [ ] MAJ-104: persist a provider-message flag and gate the SPA on it; rewrite RG-2 with an exact string
- [ ] MAJ-105: restate DG-2/SC-2/row 15 as three scans (new frames, non-`detail` fields plus non-Verbose DOM, positive control)
- [ ] MAJ-106: one billing rule, 4xx only, Anthropic phrase fixed, routing list equal to C-5, classifier-agreement test
- [ ] MAJ-107: add `model_retired` (and `quota_billing` per Q4) to `classifyOperatorOnlyTurnError`
- [ ] MAJ-108: make `provider_retry` a projection item; handle clock skew
- [ ] MAJ-109: narrow C-24 to explicit retirement phrases; add near-miss and media-404 rows; founder rules on fallback for a retired primary
- [ ] MAJ-110: take identity from the chain attempt; list adapters without header facts; decide on SDK retries
- [ ] MIN-101…MIN-106, OBS-101…OBS-103 as listed

---

## Questions for the founder

New open points from this round only (D1–D9 are settled and not repeated; Q4 is repeated only because this round changes its framing). A question here is not a finding; team-lead turns this list into the founder interview before the fix round.

- **FQ-101 (from MAJ-103) — `model_retired` wording vs the existing copy rule.** D9's "choose another model" fails `pkg/agent/translate_error_test.go::TestUserMessages_OurFaultsDoNotSendUsersModelShopping`, which bans "another model" in any `config`-attributed message. Options: **A** keep D9's words and add a named exception to the test; **B (recommended)** reword to "Pick a new model in the agent's settings." (names the property that helps, matches the test's intent); **C** attribute `model_retired` to `provider` instead of `config`.
- **FQ-102 (from MAJ-109) — does a retired primary model fall back?** Today a 404 aborts the chain (`pkg/providers/error_classifier.go::classifyByStatus` has no 404 case), so the configured Fallback model is never tried. Options: **A (recommended)** fall back to the Fallback model and show the retired line in the fallback note; **B** end the turn with the retired line.
- **FQ-103 (from MAJ-110) — the Anthropic SDK's own automatic retries.** The SDK likely retries 429s itself (Inferred), so "3 attempts" could become up to 9 hidden ones. Options: **A (recommended)** turn SDK retries off so the visible countdown is the only retry; **B** keep them and count only Omnipus-level attempts.
- **FQ-104 (from OBS-101) — the longest a user may watch countdowns in one turn.** 3 attempts × 2-minute ceiling per model, times each Fallback model, has no stated total cap. Options: **A (recommended)** one per-turn retry-wait cap (e.g. 5 minutes total), after which the turn ends with the rate-limit line; **B** no total cap (per-model ceiling only).
- **Q4 (reframed, still open)** — `quota_billing` attribution (`config` vs `provider`) now also decides whether it joins the operator-only set (MAJ-107), i.e. whether task runs and the Judge fail at once or keep retrying an out-of-credit account. Recommendation unchanged from round 1's framing, but the founder should rule knowing it has this second effect.

## Escalation to the founder

None. This round found **no CRITICAL finding**. The ten MAJOR findings go to the one fix round; any that remain blocking after that fix go to the founder for disposition — there is no third grill round.

## Next action

```
Verdict: REVISE

Review written to: docs/internal/specs/provider-messages-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
```

---

## Evidence (this review)

| Claim | Evidence | Certainty |
|---|---|---|
| Retry loop sits outside the chain; chain falls through on 429 | `pkg/agent/loop_provider_retry.go::callProvider` wraps `callProviderOnce`; `pkg/agent/loop_run_turn.go::callProviderOnce` calls `rt.al.fallback.Execute`; `pkg/providers/fallback.go::Execute` `MarkFailure` + continue on retriable; `pkg/providers/types.go::IsRetriable` | Verified |
| Per-candidate budget 120 s | `pkg/providers/fallback.go::defaultPerCandidateTimeout`, `candidateBudget` | Verified |
| Eight `EventKindLLMRetry` emitters, seven reasons | `grep -rn EventKindLLMRetry pkg` plus a read of each call site | Verified |
| Copy rule bans "another model" for `config` | `pkg/agent/translate_error_test.go::TestUserMessages_OurFaultsDoNotSendUsersModelShopping`; `src/lib/llm-error.test.ts` same list | Verified |
| Own-limiter persists internal text under `rate_limited`; replay sends Content | `pkg/agent/loop.go` (`appendClassifiedError(EventKindRateLimit…)`); `pkg/agent/turn_transcript.go::writeErrorTranscriptWithAbandonment` (`written = message` for `CodeRateLimited`); `pkg/gateway/replay.go` (`Message: entry.Content`) | Verified |
| SPA ignores `le.message` today except `delegated_task_limit` | `src/lib/llm-error.ts::getLLMErrorDisplay` | Verified |
| `detail` carries the body preview on the wire | `pkg/agent/translate_error.go::buildDetail` (`body=`+preview); `pkg/gateway/websocket_forward_hub.go::hubError` (`Detail: &detail`) | Verified |
| User classifier is status-first (5xx → network before body) | `pkg/agent/translate_error.go::classifyByHTTPStatus` | Verified |
| Routing billing patterns broader than C-5; 5xx status-first; 404 unmapped | `pkg/providers/error_classifier.go::billingPatterns`, `classifyByStatus`, `ClassifyError` | Verified |
| Operator-only set and its consumers | `pkg/agent/operator_only_turn_error.go::classifyOperatorOnlyTurnError` (header names Judge and task-run consumers) | Verified |
| Snapshot attach uses transcript plus projection; projection kinds | `pkg/gateway/ws_session_hub.go` header; `pkg/gateway/ws_hub_projection.go::hubFrameKind` | Verified |
| No clock-skew handling in the SPA; existing indicator uses relative seconds | grep `src/lib`, `src/store` for skew/server time (none); `src/components/chat/RateLimitIndicator.tsx` | Verified |
| Strip-retry gate fires only on `CodeUnknown` 4xx | `pkg/agent/media_downgrade.go::outcomeFallbackEligible` | Verified |
| `HandleErrorResponse` has no identity params; 3 adapters use it; SDK adapter wraps with `fmt.Errorf` | `pkg/providers/common/common.go::HandleErrorResponse`; grep of callers; `pkg/providers/anthropic/provider.go` | Verified |
| Disclosure already inert | `src/components/chat/MessageItem.tsx` (`{message.errorDetail.slice(…)}`) | Verified |
| Anthropic / OpenAI / Ollama / Gemini wordings | Known provider behaviour, not probed live | Inferred (medium-high) |
| anthropic-sdk-go retries 429 by default | Known SDK default; no `WithMaxRetries` in the adapter (grep) | Inferred (medium) |
| Traceability gaps | grep of the spec for AU-3, RG-1, RG-2, "US-5 / 2", "US-4 / 3" | Verified |
| Self-check (addendum) | Re-read the review against grill-spec Output: file path correct, verdict stated, CRITICAL/MAJOR listed, separate "Questions for the founder", "Escalation to the founder" and the round-2 next-action block now present; MAJ-101 re-verified (`pkg/providers/fallback.go::defaultPerCandidateTimeout` = 120 s, `MarkFailure` after `IsRetriable`), MAJ-103 re-verified (`bannedSwitch` contains "another model"; spec §6 row uses "choose another model") | Verified |

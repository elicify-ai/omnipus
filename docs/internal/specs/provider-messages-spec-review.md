# Adversarial Review: Provider Messages (provider/LLM error presentation, #711)

**Document reviewed**: `docs/internal/specs/provider-messages-spec.md` (commit `3e2b4e1c2`)
**Mode**: Spec (plan-spec format: BDD scenarios, FR-xxx, SC-x, traceability matrix present)
**Round**: Round 1 of 2
**Review date**: 2026-09-26
**Verdict**: **BLOCK**

Certainty labels: **Verified** = read in code on `feat/provider-messages` @ `3e2b4e1c2` this review (symbol cited); **Inferred** = reasoned from code or from known provider behaviour, not executed. GitNexus MCP tools were not connected in this review session (Read/Grep only), so blast-radius claims are Inferred.

> **Review-internal correction (before publication):** the first draft rated the provider-detail route's authorization (now MIN-008) as MAJOR on the premise of a multi-account gateway. That premise is wrong: `pkg/config/config.go::Config.Users` is documented "single-user model: holds at most one entry" and `pkg/gateway/rest.go::adminWrap` describes "the single-account model". The finding is kept at MINOR as defence in depth; the matching founder question was dropped.

## Executive Summary

The direction (facts, not raw text; on-demand raw detail) is sound, but the spec's model of *where the error sentence is produced and shown* is wrong in three places, so the headline feature (provider-named sentences that survive persistence and replay) would not work as written. Two critical defects: the billing-detector seed vocabulary will reclassify real per-minute rate limits as "out of credit" **and** disable the model for 5–24 hours through the provider cooldown; and the raw-JSON accordion cannot meet the founder's "key fragments: never" (PM-2) with the scrubber the spec itself says cannot recognise fragments. The spec also reverses a locked ADR-051 decision without amending it (MAJ-014), and its #711 oracle scans error frames only, while the retry event it newly forwards carries the raw error string (MAJ-015).

Frontend coverage (lenses 5, 8, 9, 10) was run with the same depth as backend: four frontend MAJOR findings (MAJ-004, MAJ-016, MAJ-017, MAJ-018) and two MINOR (MIN-009, MIN-010). All 22 sampled `file::symbol` citations from the spec resolve (table in "Citation verification").

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 17 |
| MINOR | 10 |
| OBSERVATION | 3 |
| **Total** | **32** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Billing seed vocabulary reclassifies genuine rate limits and triggers a 5–24 h model disable

- **Lens**: Incorrectness / Inoperability
- **Affected section**: §7.3 (seed list "insufficient_quota", "insufficient credits", "insufficient balance", "out of credit", "exceeded your current quota", "usage limit reached", "billing"); §7.3 providers-side "429 with a billing-shaped body → `FailoverBilling`"; D2 row `429 | exceeded your current quota | quota_billing`; FR-002.
- **Description**:
  1. Google's Gemini API answers a **per-minute** free-tier rate limit with 429 `RESOURCE_EXHAUSTED` and the text "You exceeded your current quota, please check your plan and billing details…" — it matches both "exceeded your current quota" and "billing". (Inferred, high confidence — known Gemini wording; not reproduced here.)
  2. "usage limit reached" is the wording of time-windowed subscription limits (e.g. ChatGPT/Codex "You've hit your usage limit… try again in …"), which reset on their own — not an empty account. `pkg/providers/error_classifier.go::rateLimitPatterns` today deliberately lists `usage limit` and `exceeded your current quota` as **rate-limit** patterns (Verified).
  3. The bare substring "billing" matches any error that links to a billing page.
  4. The consequence is not just wrong copy. `pkg/providers/cooldown.go::MarkFailure` sets `DisabledUntil = now + calculateBillingCooldown(n)` for `FailoverBilling` — **5 h, 10 h, 20 h, then 24 h** (Verified). `FallbackChain.Execute` then skips that provider/model (Verified, `fallback.go::Execute` cooldown check). One misread per-minute throttle takes the model out of the fallback chain for five hours, and the user is told their account is empty and to pay.
- **Impact**: A Gemini free-tier user who sends messages quickly is told "Google says your account is out of credit", gets no auto-retry (`retryable: false`), and their Gemini candidate is silently skipped for 5 h on every fallback-enabled agent.
- **Recommendation**:
  - Drop "billing", "usage limit reached" and "exceeded your current quota" from the seed. Keep only unambiguous account-state markers: HTTP 402, `insufficient_quota` (OpenAI's error **code**, match on the JSON `code`/`type` field, not free text), "insufficient credits", "insufficient balance", "credit balance is too low", "payment required".
  - For 429 specifically, require a structured marker (402-equivalent code field such as `insufficient_quota`) rather than prose; a 429 with prose-only quota wording stays `rate_limited`.
  - Add D2 rows that must stay `rate_limited`: the Gemini per-minute message verbatim; a Codex "usage limit… try again in 3 days" message; an OpenAI 429 whose message links to a billing page.
  - Decide explicitly (founder) whether a billing verdict should still use the 5 h `calculateBillingCooldown` curve, and add a test pinning the cooldown chosen for each D2 row.

---

#### [CRIT-002] The raw-JSON accordion cannot guarantee "key fragments: never" (PM-2), yet FR-009 / C-11 claim it

- **Lens**: Insecurity / Inconsistency
- **Affected section**: FR-009 ("MUST NOT name key labels, key material, or fragments anywhere in messages, facts, or the raw view"); C-11 ("No message, fact, **or accordion content** contains … key material/fragments"); §5 Option 1; C-8; problem row F3.
- **Description**: The spec's own F3 evidence says `logger.ScrubSensitiveValues` "cannot recognise never-registered or masked/truncated values". Option 1 then serves the *scrubbed raw provider body* to the Verbose viewer. Providers commonly echo a masked key in 401 bodies (e.g. "Incorrect API key provided: sk-proj-****abcd"). That fragment is not a registered value and passes the scrubber. FR-009 and C-11 are therefore not achievable by the specified mechanism, and C-11 has no test that could catch it (TDD row 9 only asserts *registered* values are scrubbed). PM-2 says fragments are "never" shown.
- **Impact**: #711's leak class moves from "every frame" to "every Verbose expand". The spec reports the leak closed while the founder's "never" is still violated, and the test suite stays green.
- **Recommendation**: Pick one and state it in FR-009:
  - (a) Serve a **parsed allow-list view** rather than the raw body: `error.type`, `error.code`, `error.message`, `status`, request id. Run `error.message` through a key-shape masker (patterns such as `sk-[A-Za-z0-9_-]{4,}`, `\*{3,}[A-Za-z0-9]{2,}`, long base64/hex runs ≥ 24 chars). Add a D-set scenario with an OpenAI-style masked-key 401 body as the oracle.
  - (b) Keep the raw body, and have the founder explicitly accept the residual-fragment risk. Then rewrite FR-009 and C-11 so they no longer claim "never" for the raw view.
  - Either way, add a TDD row asserting an *unregistered* key-shaped fragment is absent from the provider-detail response.

---

### MAJOR Findings

#### [MAJ-001] "Gateway assembles the line" (D1) is wrong about the architecture, so provider naming is lost on the live bubble, on persistence and on replay

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: §6 D1; §7.3 "Loop identity … so the gateway can assemble"; §8.3 "server-assembled message as the one line"; §8.6 "replay path unchanged"; C-7; US-6/2.
- **Description** (all Verified):
  1. For provider failures the agent sets `ErrorPayload.Code` (`loop_run_turn_response.go`, the `rf.err != nil` block; `loop.go::emitErrorEvent`). `hubError` then **skips** translation and forwards `p.Message` verbatim (`if p.Code != "" { message = p.Message }`). The sentence is therefore produced in `pkg/agent` at the emit site, not in the gateway.
  2. The transcript writer `turn_transcript.go::writeErrorTranscriptWithAbandonment` overwrites the message with `defaultUserMessage(code)` for every coded, non-trusted, non-`rate_limited` error. A provider-named `quota_billing` or `provider_auth_failed` sentence is therefore **persisted as the generic catalogue sentence**.
  3. `src/lib/llm-error.ts::getLLMErrorDisplay` deliberately ignores `le.message` and renders `codeToMessage(le.code)` for every code except `delegated_task_limit`. Its doc comment says provider-originated message text is NOT shown in the bubble. This applies to live frames and replay alike, so no server-assembled sentence would reach the screen.
  4. Channel replies (`session_worker.go`, `loop.go`: `TranslateTurnError(err).Message`) classify the error chain with no loop identity, so they would not get the provider name either.
- **Impact**: As written, the build passes its unit tests (the Go translator produces the right string), but the user sees today's generic sentence live, after reload, and on Telegram/Discord. This is the "documented but unwired" failure mode.
- **Recommendation**: Rewrite D1 to name the real assembly points and the required changes:
  - (i) The agent emit sites assemble the sentence from facts carried on the error chain (see MAJ-006 for where the facts come from).
  - (ii) `writeErrorTranscriptWithAbandonment` persists the assembled sentence for the templated codes (extend its trusted set, or pass the assembled `LLMError` through `appendClassifiedError` without re-deriving).
  - (iii) `getLLMErrorDisplay` renders `le.message` for the templated codes only, as an explicit allow-list mirroring the `delegated_task_limit` exception, with a test proving other codes still render catalogue copy.
  - (iv) Channel replies use the same facts-aware translator.
  - Record this as a trust-boundary change: the SPA starts trusting server message text for these codes. Add BDD scenarios for replay text and channel text.

#### [MAJ-002] Root-turn auto-retry interacts with the fallback chain and cooldown in undefined ways

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: §7.4 "Generalise the existing delegated pattern to root turns"; FR-005; Q3.
- **Description** (Verified): `callProviderOnce` takes the `FallbackChain.Execute` path whenever `len(rt.activeCandidates) > 1`. A 429 there already fails over to the next candidate and calls `cooldown.MarkFailure`, which sets a standard cooldown of **at least 60 s** (`calculateStandardCooldown`). If the root retry loop wraps this and sleeps for a provider `retry-after` shorter than 60 s (common: 1–30 s), the next `Execute` finds every candidate in cooldown. It returns `FallbackExhaustedError` without calling any provider, so attempts 2..5 burn instantly while the UI shows countdowns. The spec also does not say:
  - which attempt's `retry-after` and provider drive the countdown when the chain holds several 429s;
  - whether retry applies at all when a fallback candidate exists (the delegated gate `shouldRetryDelegatedRateLimit` explicitly requires `len(activeCandidates) == 1`);
  - how a retry frame relates to a fallback note in the same turn.
- **Impact**: Fake retries, a countdown that lies, and a terminal line after ~0 s of real waiting on every multi-candidate root agent.
- **Recommendation**: State the rule. Suggested: root auto-retry applies only when the chain is exhausted and every attempt failed `rate_limited`. The wait is `max(retry-after across attempts, remaining cooldown of the earliest-available candidate)`. Alternatively, the retry bypasses the cooldown for the candidate whose `retry-after` elapsed. Add unit rows for 1-candidate, 2-candidate-both-429, and 2-candidate-one-billing.

#### [MAJ-003] No ceiling on how long a turn may sit in auto-retry

- **Lens**: Incompleteness / Inoperability
- **Affected section**: C-4 (clamp 86400 s); FR-005 (cap 5); §7.4.
- **Description**: With `retry-after` clamped only at 24 h and five attempts, a root turn may legally sleep for days, holding the session's turn (queued user messages wait behind it). Delegated sessions have a 30-minute default timeout (`config.go`, `DelegationTimeoutMinutes` comment), so a long wait there ends as `turn_timed_out` mid-countdown. OpenAI daily-limit 429s commonly carry `retry-after` values in the thousands of seconds.
- **Impact**: A turn that "Retrying automatically in 5:59:12" blocks the chat for six hours. A delegated child times out with the wrong code.
- **Recommendation**: Add a maximum auto-wait (suggested 60–120 s per attempt, founder to confirm under Q3). Above it, do not auto-retry. Emit the terminal line with the reset time as a fact ("OpenRouter is busy until 14:05. You can retry the turn then."), or the plain terminal line. Add BDD and dataset rows for `retry-after: 3600`.

#### [MAJ-004] The countdown is relative-only, so a second tab or a reattach restarts it

- **Lens**: Incorrectness
- **Affected section**: §7.1 item 3 (`ProviderRetryFrame` carries `retry_after_seconds`); US-1/2; holdout 6.
- **Description**: The hub keeps turn-scoped frames in the active-turn projection and replays them to a tab that attaches mid-turn (see `hubError`'s `hubKindItem` meta; #823 design). A tab attaching 100 s into a 120 s wait receives `retry_after_seconds: 120` and shows 2:00 while the server retries 20 s later. The same happens after a WebSocket reconnect.
- **Impact**: Two tabs show different countdowns. Holdout 6 ("both show the same line") fails.
- **Recommendation**: Add `retry_at` (server wall-clock time, RFC 3339) to the frame and render `retry_at − now`, clamped at 0. Keep `retry_after_seconds` for display of the original wait only if needed. Add a BDD scenario: "tab attaches mid-countdown → remaining time, not original."

#### [MAJ-005] Deleting `detail` (Q2 A) removes the only Verbose content for six codes whose copy promises it

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: Q2; §7.5; §8.3; FR-004.
- **Description** (Verified): The catalogue sentences for `provider_stalled`, `tool_args` ("Verbose chat shows which tool and what went wrong"), `schema`, `turn_timed_out`, `context_unrecoverable` and `unknown` all tell the user to open Verbose chat for details. Those details today come from `detail`, including non-provider text such as the stall duration built in `TranslateTurnError` (`buildDetail(nil, "…stopped responding for %s")`). Option 1's retention holds **only the raw provider body**, captured at `hubError` from `p.ProviderError`. Typed exits (`emitErrorEvent`) carry no `ProviderError` at all.
- **Impact**: After this ships, six error sentences point the user at a Verbose view that is empty for them, which is a user-facing lie.
- **Recommendation**: Either:
  - (a) keep a curated, provider-text-free Verbose fact (for example `facts.diagnostic`: stall limit, tool name, stage) for non-provider codes; or
  - (b) rewrite those six catalogue sentences in the same contract wave. The copy-rule tests then pin the new text.
  - Add a TDD row per code.

#### [MAJ-006] The named provider can be the wrong provider

- **Lens**: Incorrectness
- **Affected section**: §7.3 "Loop identity: the loop threads provider display name + model into `ErrorPayload`"; §6 templates.
- **Description** (Verified): `errorToProviderError` classifies from the **most recent** fallback attempt (`FallbackExhaustedError` walked in reverse). The loop's identity (`rt.activeCandidates[0]`, `rt.llmModel`) is the **primary**. If the primary (OpenRouter) was skipped for cooldown and the fallback (Anthropic) returned 401, the message says "OpenRouter rejected the API key". The last attempt may also be a cooldown *skip* with no provider response at all.
- **Impact**: The operator is sent to fix the wrong key, and the sentence is confidently wrong.
- **Recommendation**: Source `{provider}`/`model` from the attempt that produced the classified `ProviderError` (`FallbackAttempt.Provider/Model`, or stamp the provider id onto `common.ProviderError` at `HandleErrorResponse`). Specify that a skip-only chain has no provider fact, so the catalogue fallback applies. Add a BDD scenario for primary-skipped / fallback-401.

#### [MAJ-007] "Attempt N of 5" is ambiguous: calls or retries?

- **Lens**: Ambiguity
- **Affected section**: US-1/1 (`attempt=1` after the first 429); C-5; FR-005 "cap 5"; Scenario "Terminal line after exhaustion" ("Given 5 failed rate-limited attempts"); Q3; PM-1 example "attempt 2 of 5".
- **Description**: After the first call fails, the next call is the **second** call, yet US-1/1 shows "attempt 1 of 5". The existing delegated code counts *retries* (`delegatedRateLimitMaxRetries = 2`, `Attempt: retry+1`). It is not stated whether "5" means 5 calls total (4 retries) or 5 retries (6 calls). The terminal scenario's "5 failed attempts" fits either reading.
- **Impact**: RED and GREEN authors will pick different readings. Tests pass or fail depending on who wrote them.
- **Recommendation**: Define it once: "`max_attempts` = total provider calls including the first (5). The retry frame's `attempt` is the number of the call about to be made (2..5)." That matches PM-1's "attempt 2 of 5". Update US-1/1, the dataset D3 row and C-5.

#### [MAJ-008] Rate-limit reset headers are not in one format

- **Lens**: Incorrectness
- **Affected section**: §7.2; dataset D1 row "fallback `x-ratelimit-reset: 1729…` (epoch)".
- **Description** (Inferred, medium-high confidence — from provider documentation, not probed):
  - OpenAI sends `x-ratelimit-reset-requests` / `-tokens` as Go-style durations ("6m0s", "20ms").
  - Anthropic uses `anthropic-ratelimit-*-reset` as RFC 3339 timestamps.
  - OpenRouter's `X-RateLimit-Reset` is epoch **milliseconds**.
  - Gemini puts `retryDelay` in the JSON body (`RetryInfo`), not in a header.
  - The spec assumes epoch seconds. D1 also lacks a past HTTP-date (negative delta), malformed values, and duplicate headers.
- **Impact**: A parse of "6m0s" as epoch fails silently or yields garbage. A millisecond epoch parsed as seconds clamps to 86400, giving a 24-hour countdown.
- **Recommendation**: Either:
  - limit the secondary source to an explicit table of (header name → format) per provider family, with a parse test per row; or
  - drop `x-ratelimit-reset*` from scope and use `retry-after`/`retry-after-ms` only.
  - Add D1 rows: past HTTP-date → absent; malformed → absent; ms-epoch; duration string.

#### [MAJ-009] (withdrawn to MINOR — see MIN-008 and the review-internal correction at the top)

#### [MAJ-010] The persisted fallback note has no transcript shape or replay carrier

- **Lens**: Incompleteness
- **Affected section**: §7.1 (only a live `ProviderFallbackFrame`); §7.4 "persisted fallback notice"; US-6/2; C-7 "ReplayErrorFrame unchanged".
- **Description**: A persisted note needs a `session.TranscriptEntry` type or status, and a replay frame or entry shape in `asyncapi.yaml`. Neither is in the contract wave. It is also unspecified whether the note appears when the primary was merely *skipped for cooldown* (Verified: `Execute` records skipped attempts). In that case every turn during a cooldown of up to 1 h (standard) or 24 h (billing) gets a note.
- **Impact**: A contract-first violation (Hard Constraint #8) discovered mid-build, and a chat full of repeated notes.
- **Recommendation**: Add the replay carrier to §7.1. State whether skip-caused fallbacks produce the note: suggested yes, but at most once per session per (unavailable, answered) pair until the pair changes. Add a BDD scenario.

#### [MAJ-011] PM-1's fourth founder example is silently dropped

- **Lens**: Inconsistency
- **Affected section**: §6 "All other codes keep today's catalogue copy unchanged"; §3 PM-1 row.
- **Description**: PM-1 lists four example sentences. The fourth — "OpenRouter no longer offers z-ai/glm-4. Choose another model for this agent." — has no template, no code mapping (is it `model_unavailable`? a 404 residual `unknown`?) and no founder question.
- **Impact**: The founder believes model-gone errors are covered. They ship as today's generic sentence.
- **Recommendation**: Either add a template and classification (404 / "model not found" bodies → `model_unavailable`, with a check that this does not re-point the residual-4xx `CodeUnknown` media gate), or add Q7 asking the founder to defer it explicitly.

#### [MAJ-012] `billing_url` in the provider catalog is a cross-repo, schema-versioned, unsigned change

- **Lens**: Infeasibility / Insecurity
- **Affected section**: §7.6; Q5; C-10.
- **Description** (Verified):
  - The catalog is pulled daily from `elicify-ai/omnipus-provider-catalog` (`catalog/puller.go`). It is gated by `schema_version` (`parse.go`) and, per ADR-067, is checksum-only with no signature (an accepted risk).
  - "Embedded snapshot gains values" is not enough: the next daily pull replaces the document, so a field the assembly repo does not emit disappears and the button vanishes.
  - A catalog-supplied URL rendered as a clickable button also needs the same load-time URL validation ADR-067 applies to endpoints (https only, no `javascript:`).
- **Impact**: The button works on day 1 and disappears on day 2. Or, with a tampered catalog, it becomes a phishing link inside the product's own chrome.
- **Recommendation**: Scope the assembly-repo change and the `schema_version` bump explicitly (or source `billing_url` from a Go-side table with a note on why that does not violate ADR-067's "no Go-side provider knowledge"). Require https-only validation on load, and render with `rel="noopener noreferrer"`, `target="_blank"`.

#### [MAJ-013] PM-7 sequencing with the security branch is unmanaged

- **Lens**: Inoperability
- **Affected section**: header; US-4/3.
- **Description** (Verified): `c13c4d279` ("error frame detail stays off the wire") is on `feature/gateway-security-fixes` (local and origin). The spec says it is "removed from that branch" but gives no ordering.
- **Impact**: If the security branch lands with the commit reverted before this feature lands, #711's leak (detail on every frame) is live again on `main`. If it lands with the commit kept, it conflicts with §7.5's `hubError` rewrite.
- **Recommendation**: State the rule: revert `c13c4d279` only in the same landing window as this feature's `hubError` change, or keep it until this feature lands and resolve the conflict in favour of this design. Name the owner. US-4/3 is not testable as written — replace it with that rule.

#### [MAJ-014] The spec reverses a locked ADR-051 decision and invariant without amending the ADR

- **Lens**: Inconsistency with existing ADRs
- **Affected section**: header ("governing ADR: ADR-051"); §5 Option 1; §7.1 item 1 (remove `detail`); Q2.
- **Description** (Verified, `docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md`): Revision 3 records operator decision Q2 — "raw `detail` → ships on the wire but the SPA renders it only under Verbose Chat" — and RD7 repeats it ("`detail` **ships on the wire**"). Invariant (3) reads "no raw provider text crosses to the SPA in production or persists". The spec (a) deletes the on-wire `detail` that RD7 mandates and (b) adds a REST route whose whole purpose is to carry raw provider text to the SPA, which contradicts invariant (3) as literally written. The spec's Q2 asks the founder only about the field, not about reversing an ADR decision; no ADR amendment or superseding note is listed as a deliverable.
- **Impact**: After landing, the governing ADR describes the opposite of the code. The next reviewer cites ADR-051 RD7 and flags the new design as a regression, or re-adds `detail` "per the ADR". Stale ADRs have to be corrected by the architect with a dated note; the spec must name that step.
- **Recommendation**: Add a deliverable: a dated ADR-051 amendment (architect) that supersedes RD7's "ships on the wire", rewrites invariant (3) to "no raw provider text rides a broadcast frame or persists; raw text reaches the SPA only via the on-demand, session-scoped provider-detail fetch under Verbose Chat", and records PM-6/PM-7 as the authority. Reword Q2 so the founder sees they are reversing ADR-051's locked Q2 (added as a founder question below).

#### [MAJ-015] The #711 oracle covers error frames only, but the newly forwarded retry event carries the raw error string

- **Lens**: Insecurity / Testability (false green)
- **Affected section**: §7.4 ("`hubSyncTap` gains a handler forwarding `EventKindLLMRetry`"; "`LLMRetryPayload` extended additively"); C-1; TDD 11; SC-2; Scenario "Error frames never carry provider body text".
- **Description** (Verified): `pkg/agent/loop_provider_retry.go::callProvider` emits `LLMRetryPayload{…, Error: err.Error(), …}`. For a provider failure `err.Error()` is `pkg/providers/common/common.go::ProviderError.Error()`, which embeds `BodyPreview` (or `Body`) — the provider's response text. Today that is harmless only because `EventKindLLMRetry` is on the not-forwarded list in `pkg/gateway/websocket_forward.go`. The spec moves it off that list and extends the payload "additively", i.e. `Error` stays. The spec's `ProviderRetryFrame` field list omits `error`, but nothing forbids a handler that marshals the payload, and C-1, TDD 11 and SC-2 all scan **`ErrorFrame`** only. The same applies to the new `provider_fallback` frame if it is built from `FallbackAttempt.Error`.
- **Impact**: A handler that copies `payload.Error` into any retry-frame field (or a later "add reason text" change) re-opens #711 on every 429 retry, and the #711 oracle stays green because it never looks at `provider_retry` frames.
- **Recommendation**: Extend C-1 to "no frame of **any** type (error, provider_retry, provider_fallback, and every frame in the wire log) contains a provider-body substring". Make TDD 11 / SC-2 scan the whole WS log, and make the dataset body a unique sentinel string so the scan cannot pass by accident. State in §7.4 that the hub handler builds `ProviderRetryFrame` from named fields only and never reads `LLMRetryPayload.Error`. Add a mutation check to the test plan: forwarding `Error` must turn TDD 11 red.

#### [MAJ-016] The retry indicator's live region would announce every second to screen readers

- **Lens**: Accessibility / keyboard
- **Affected section**: §8 item 2 ("reuses the `RateLimitIndicator` pattern — per-second interval, warning→success transition, `role="status"`, `aria-live`"); TDD 13.
- **Description** (Verified, `src/components/chat/RateLimitIndicator.tsx`): the pattern to be reused puts `role="status" aria-live="polite"` on the container whose text includes the per-second `formatSeconds(remaining)` countdown. Copied as specified, a 120 s wait produces up to 120 polite announcements per attempt, times up to five attempts, each one interrupting whatever the screen-reader user is reading. The spec specifies neither what is announced nor when.
- **Impact**: The feature built to reduce confusion makes the chat unusable with a screen reader for minutes at a time.
- **Recommendation**: Specify the announcement contract: the live region announces once per attempt ("OpenRouter is busy. Retrying automatically in 2 minutes, attempt 2 of 5."), and once on the terminal line; the ticking mm:ss text sits outside the live region (or is `aria-hidden` with a visually-hidden coarse remainder updated at most every 30 s). Add a component test asserting the live-region text does not change on a 1 s tick.

#### [MAJ-017] The retry-wait journey has undefined states: Stop, zero, dismiss, and a misleading "success" colour

- **Lens**: UI states / journey gaps
- **Affected section**: §8 item 2; US-1/2 and US-1/4; §9 (no scenario for user action during a wait).
- **Description**:
  1. **Stop during the wait.** A root retry sleeps inside the turn. The spec does not say that the Stop control stays enabled during the wait and that Stop cancels the sleep at once. The existing delegated sleep uses `sleepWithContext(rt.turnCtx, …)` (Verified, `loop_provider_retry.go::callProvider`), so cancellation is feasible, but it is not a stated requirement or tested. Per ADR-082 only an explicit Stop/cancel ends a turn early, so Stop is the user's only way out of a long wait.
  2. **At 0.** The copied pattern flips to a success colour and the text "Retry available — rate limit cleared" (Verified, `RateLimitIndicator.tsx`) — right for the own-limiter (the user must retry by hand), wrong here (the system retries on its own). The state between 0 and the next frame ("Retrying now…") is not specified.
  3. **Dismiss.** The own-limiter indicator has a dismiss button; the spec does not say whether the provider-retry indicator has one, or what happens to the countdown when it is dismissed.
  4. **Typing during the wait.** Whether a new user message queues behind the waiting turn or steers it is not stated (it will queue — inferred from the turn model — and the user deserves to be told).
- **Impact**: A user who wants out of a 5 × 2-minute wait cannot tell whether Stop works; screen text claims "cleared" while the system is still waiting on the provider.
- **Recommendation**: Add a state table for `ProviderRetryIndicator` (waiting / retrying-now / cleared-by-next-frame / stopped-by-user), no success colour at 0, "Retrying now…" text, no dismiss button (or specify one), and a BDD scenario "Stop during a retry wait ends the turn within 1 s and no further attempt is made", with a Go unit test on the sleep's context cancellation.

#### [MAJ-018] The Verbose accordion lacks a fetch-failure state and a safe rendering rule

- **Lens**: UI states / Insecurity
- **Affected section**: §8 item 4 ("loading / raw / expired"); US-5; TDD 15; holdout 4.
- **Description**:
  1. Only loading, raw and expired (404) are specified. A network failure, a 401 after the session token lapsed, or a 5xx during the fetch has no state; the natural implementation maps any non-200 to "available live only", which is untrue and hides real faults.
  2. The raw body is not always JSON: holdout 4 itself expects an HTML page from a wrong `api_base` (`WrapHTMLResponseError` exists for that). The spec says "raw JSON" and gives no rendering rule. The body must render as inert text (never as HTML, never via `dangerouslySetInnerHTML`, never markdown-rendered), pretty-printed only when it parses as JSON.
  3. Whether the fetched body is cached per `error_id` for the tab (re-open without re-fetch) and what happens when the retention expires between two opens is unspecified.
- **Impact**: Misleading "available live only" on transient failures; and, with a markdown or HTML renderer reused from the chat bubble, provider-supplied markup injected into the chat.
- **Recommendation**: Add states "Couldn't load the provider response. Retry" (with a keyboard-reachable retry control) and keep "available live only" for 404 only; add the inert-text rendering rule with a component test that feeds `<img src=x onerror=…>` and asserts it renders as text; state the per-tab cache rule.

---

### MINOR Findings

#### [MIN-001] F4 lists timeout/stream-reset retries as silent, but only `rate_limited` retries are surfaced
- **Lens**: Inconsistency
- **Affected section**: §1 F4; §7.4.
- **Description**: `loop_run_turn_response.go::retryTimeout` inline retries (Verified) stay invisible.
- **Recommendation**: Add a non-behavior line, "transport/timeout retries remain unannounced (out of scope)", or include them.

#### [MIN-002] Retention parameters are placeholders
- **Lens**: Ambiguity
- **Affected section**: §5 "e.g. last N errors per session, TTL ~5 minutes".
- **Recommendation**: Fix the numbers (for example N = 20 per session, TTL = 10 min, global cap 1,000 entries × 8 KiB ≈ 8 MiB) and test eviction.

#### [MIN-003] Undefined and contradictory dataset terms
- **Lens**: Ambiguity
- **Affected section**: D2 "200-less"; C-4 "`retry-after: 0` is treated as 'immediate retry allowed'" vs D1 "absent fact → exponential backoff".
- **Recommendation**: Define "status 0 / non-HTTP". Say explicitly that `retry-after: 0` uses the backoff schedule.

#### [MIN-004] "23-code copy-rule tests pass unchanged" contradicts adding a 24th code
- **Lens**: Inconsistency
- **Affected section**: §11 closing paragraph; §13 item 1.
- **Recommendation**: Say "extended to 24 codes, existing assertions unchanged".

#### [MIN-005] SC-1 costs real minutes in Playwright
- **Lens**: Infeasibility
- **Affected section**: SC-1 (120 s countdown, ±2 s), test 18.
- **Description**: With 5 attempts this is several minutes per spec run, on a suite already sharded for time.
- **Recommendation**: Use `retry-after: 3` in e2e and prove the 120 s formatting at component level.

#### [MIN-006] The replay scenario's oracle is wrong
- **Lens**: Incorrectness
- **Affected section**: Scenario "Replay never carries or serves raw detail" ("the fetch returns 404").
- **Description**: A replayed error has no `error_id` (C-7), so there is nothing to fetch.
- **Recommendation**: The oracle should be "no request is made; UI shows 'available live only'", plus a separate REST test for an expired id → 404.

#### [MIN-007] Own-limiter `rate_limited` must never pick up a provider template
- **Lens**: Incompleteness
- **Affected section**: §6; §13 item 5.
- **Description**: SEC-26 in-process denials share `CodeRateLimited` (`turn_transcript.go` belt, Verified).
- **Recommendation**: Add a test that an own-limiter denial never renders "{provider} is busy".

#### [MIN-008] The provider-detail route's session binding and `error_id` shape are unspecified (defence in depth)
- **Lens**: Insecurity
- **Affected section**: C-13; §7.1 item 4; §7.5.
- **Description** (Verified): the install is single-account (`pkg/config/config.go::Config.Users`, "holds at most one entry"), so a cross-account read is not the threat. But "authenticated" is all the spec says. The existing session reads under `withAuth(HandleSessions)` (`pkg/gateway/gateway_boot.go`) do no per-session check (`rest_sessions.go::getSession` ignores the request), whereas the precedent the spec should copy, `rest_tool_results.go::HandleToolResults`, validates `session_id` and has a cross-session negative test (`TestHandleToolResults_CrossSessionForbidden`). The `error_id` format is unspecified.
- **Recommendation**: Key the retention by `(session_id, error_id)`, `error_id` random (UUIDv4), mismatched session → 404; validate both ids with `validateEntityID`; add a cross-session negative test to TDD 10 mirroring the tool-results one.

#### [MIN-009] "Open billing page" is a link, not a button
- **Lens**: Accessibility / design-system reuse
- **Affected section**: §8 item 3; US-2/2; Scenario "Billing button only when the URL is known".
- **Description**: The control navigates to an external site in a new tab (holdout 2). Specified as a "button", it risks a `<button onClick={window.open}>`, which screen readers announce as an action, not a link, loses the URL on hover/long-press, and needs manual `noopener`.
- **Recommendation**: Specify an anchor (`<a href target="_blank" rel="noopener noreferrer">`) styled through the catalogued Button's link/`asChild` form per the design-system skill, with an accessible name that says it opens the provider's site in a new tab. Test: role `link`, exact `href`.

#### [MIN-010] Design-system deliverables are missing and the accordion choice is punted
- **Lens**: Design-system reuse / brand
- **Affected section**: §8 items 2 and 4; §11.
- **Description**: `ProviderRetryIndicator` is declared "a new component per the design-system skill", but the TDD plan lists no design-system publication items (manifest, story, lock-script run) that the blocking `design-system` CI gate enforces for new components. For the accordion the spec leaves "native `<details>` … or the catalogued collapsible" to frontend-lead, although PM-6 says "reuse the tool-call accordion pattern" and both a catalogued `src/components/ui/accordion.tsx` and native `<details>` tool UIs (`src/components/chat/tools/WebServeUI.tsx`) exist (Verified). Two plausible implementations of one founder decision is an open design choice, not an implementation detail.
- **Recommendation**: Name the one component to reuse (and the tool-call UI it mirrors), and add the design-system publication steps for `ProviderRetryIndicator` to §11 (or reuse/extend the existing indicator instead of adding a sibling, per skill rule 14).

---

### Observations

#### [OBS-001] Reuse the existing lazy-fetch precedent
- **Lens**: Overcomplexity
- **Affected section**: §5 Option 1; §7.1 item 4.
- **Suggestion**: `GET /sessions/{session_id}/tool-results/{ref}` (`rest_tool_results.go::HandleToolResults`, `openapi.yaml`) is already the session-scoped lazy fetch behind the tool-call accordion that PM-6 refers to. Mirror its path shape, auth and 404 semantics, or reuse its store, instead of designing a parallel one.

#### [OBS-002] Template machinery is heavy for four sentences
- **Lens**: Overcomplexity
- **Affected section**: §6 "Contract mechanics".
- **Suggestion**: A second YAML block, a closed slot vocabulary and generator extensions in two generators serve four sentences with one slot. Consider adding a `provider_message` sibling string to the three existing `x-user-messages` entries. The generator then enforces only the single `{provider}` token.

#### [OBS-003] `cf-ray` is not a provider request ID
- **Lens**: Incorrectness
- **Affected section**: §7.2.
- **Suggestion**: It is Cloudflare's edge ID. Use it only as a last resort and label it as such in the Verbose facts.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-7 all have numbered scenarios |
| Every acceptance scenario has BDD scenarios | FAIL | No BDD for US-1/2 (countdown ticks, next attempt at 0), US-1/3 (no-retry-after backoff), US-4/3 (PM-7), US-5/4 (Verbose off → no accordion, no fetch) |
| Every BDD scenario has `Traces to:` | PASS | All present |
| Every BDD scenario has a test in TDD plan | FAIL | Scenarios are unnumbered, but the TDD plan and matrix cite IDs like "A-5", "D-4". Set A has 4 scenarios, so "A-1…A-5" (FR-005) dangles. "Keys are never named" (C set) maps only indirectly via tests 9/11, which do not cover auth messages |
| Every FR appears in traceability matrix | PASS | FR-001..FR-012 present |
| Every BDD scenario in traceability matrix | FAIL | "Identity absent falls back to catalogue copy" (US-3/2) is in no FR row; no request-ID Verbose-only scenario exists although test 2 traces C-12 |
| Test datasets cover boundaries/edges/errors | FAIL | D1 lacks a past HTTP-date, malformed values, ms-epoch, duration strings (MAJ-008). D2 lacks the Gemini per-minute and Codex usage-window negatives (CRIT-001). No dataset for provider identity under fallback (MAJ-006) |
| Regression impact addressed | PASS (with gaps) | §13 exists; misses the cooldown-curve regression (CRIT-001) and the Verbose-promise regression (MAJ-005) |
| Success criteria are measurable | PASS | SC-1..SC-6 have thresholds; SC-1 is costly (MIN-005) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Integration (persist/replay text) | Nothing asserts the provider-named sentence survives `writeErrorTranscriptWithAbandonment` and renders on replay (MAJ-001) | US-2, US-3, US-6/2 |
| Component (SPA trusts message) | No test that `getLLMErrorDisplay` shows `le.message` for templated codes only | US-1..3 |
| Concurrency / multi-tab | A tab attaching mid-countdown (MAJ-004); two tabs expanding the accordion concurrently | US-1, holdout 6 |
| Fallback × retry | Multi-candidate root turn with 429s, with cooldown (MAJ-002) | US-1 |
| Security negative | Cross-session provider-detail fetch (MIN-008); unregistered key fragment in raw body (CRIT-002); provider body in `provider_retry` frames (MAJ-015); HTML/script body rendered inert (MAJ-018) | US-1, US-5 |
| Accessibility | Live-region announcement rate (MAJ-016); billing control has link role (MIN-009); accordion and fetch-retry keyboard-reachable (MAJ-018) | US-1, US-2, US-5 |
| Journey | Stop during a retry wait cancels it (MAJ-017) | US-1 |
| Cooldown regression | Which cooldown curve each D2 row triggers (CRIT-001) | US-2 |
| Channel surface | Telegram/Discord reply text for templated codes | FR-003 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D1 retry-after | past date, malformed, ms-epoch, duration string, duplicate header | Add one row each; expected = absent fact except ms-epoch/duration |
| D1 | wait above the auto-retry ceiling | `retry-after: 3600` → no auto-retry, terminal line (MAJ-003) |
| D2 billing | real-world false positives | Gemini per-minute 429 text, Codex "usage limit… try again in 3 days", OpenAI 429 with a billing link → `rate_limited` |
| D3 assembly | fallback-attempt provider differs from primary | primary OpenRouter skipped, Anthropic 401 → "Anthropic rejected…" |
| D3 | provider id not in catalog | `DisplayName` returns the id verbatim; assert the sentence uses the id, not a blank |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| provider-detail REST route | ok | ok | risk | risk | risk | ok | Single-account install, so elevation across accounts is N/A; session binding and `error_id` entropy unspecified (MIN-008); no audit of who fetched raw bodies; no rate limit on fetches; fragment leak (CRIT-002); non-JSON bodies need inert rendering (MAJ-018) |
| Retry / fallback frames (new) | ok | ok | ok | risk | ok | ok | `LLMRetryPayload.Error` carries the provider body preview; the #711 oracle does not scan these frames (MAJ-015) |
| In-memory retention | ok | ok | ok | risk | risk | ok | Unregistered fragments stored; size bounds are placeholders (MIN-002) |
| Error / retry / fallback frames | ok | ok | ok | ok | ok | ok | Facts-only frames close the #711 broadcast leak by construction, assuming MAJ-001 does not reintroduce `p.Message` pass-through of raw text |
| Catalog `billing_url` | risk | risk | ok | ok | ok | ok | Unsigned catalog supplies a clickable URL; https-only validation needed (MAJ-012) |
| Billing classifier → cooldown | ok | risk | ok | ok | risk | ok | Provider body text chooses a 5–24 h disable; a misclassification amounts to self-inflicted denial of service (CRIT-001) |

---

## Consistency with existing ADRs and designs

| Source | Relation to this spec | Result |
|---|---|---|
| ADR-051 — Media handling and provider error translation | Governing ADR. Rev 3 Q2 / RD7 lock "`detail` ships on the wire, rendered only under Verbose Chat"; invariant (3) "no raw provider text crosses to the SPA". The spec reverses both. | **Conflict — MAJ-014** (amendment required) |
| #823 uniform-frame session hub (`ErrorFrame.yaml` / `RateLimitFrame.yaml` `seq` comments; `hubSyncTap`) | Option 1 keeps frames uniform per session. Good. But new turn-scoped frames replayed to a mid-turn tab carry a relative countdown. | Consistent in structure; **MAJ-004** on countdown |
| ADR-082 (turn ends only on explicit Stop/cancel, per root `CLAUDE.md` "Retired surfaces") | A multi-minute root retry wait must stay cancellable by Stop. | Not violated, but unstated — **MAJ-017** |
| ADR-067 (provider catalog: checksum-only, no Go-side provider knowledge) | `billing_url` needs the assembly repo, a `schema_version` bump and URL validation. | **MAJ-012** |
| ADR-092 — Shell permission modes | No interaction: this spec touches no tool policy, approval or shell path. | No conflict |
| ADR-090 — Built-in agents, skills, and visual reading | No interaction: no new tool, no role or document-runtime change (the spec's own §17 reachability note holds — no tool registration needed). | No conflict |
| ADR-094 (preview isolation, #798) | **Not on this integration branch**: its commits (`2158b4f37` … `97f13c249`) are only on `feature/gateway-security` (Verified, `git branch -a --contains`). Subject is preview-surface isolation, not error frames. | No conflict; nothing to reconcile on `release/v0.1.1` |
| Security branch commit `c13c4d279` ("error frame detail stays off the wire (#711)") | Only on `feature/gateway-security-fixes` (local + origin, Verified). | **MAJ-013** (landing order) |
| Hard Constraint #8 (contract-first) | Contract wave ordered first — good; gaps: fallback-note replay carrier (MAJ-010), `ProviderErrorDetail` body type and error responses unspecified, retry frame must not inherit `LLMRetryPayload.Error` (MAJ-015). | Partial |

## Citation verification (sample of the spec's `file::symbol` claims)

All re-checked by Grep in this review on `3e2b4e1c2`; 22 of 22 resolve.

| Citation | Result |
|---|---|
| `pkg/providers/common/common.go::HandleErrorResponse`, `WrapHTMLResponseError`, `ProviderError.Error` | Found |
| `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus`, `buildDetail`, `BuildDetail`, `ProviderErrorFromFailover`, `providerErrorFromChain`, `defaultUserMessage` | Found |
| `pkg/agent/events.go::LLMRetryPayload`, `EventKindLLMRetry` | Found |
| `pkg/gateway/websocket_forward.go` — `EventKindLLMRetry` in the not-forwarded case list | Found |
| `pkg/gateway/websocket_forward_hub.go::hubError` (`if p.Code != "" { message = p.Message }`, `Detail: &detail`), `hubSyncTap` | Found |
| `pkg/agent/loop_provider_retry.go::callProvider`, `shouldRetryDelegatedRateLimit` | Found |
| `pkg/agent/loop_run_turn.go::callProviderOnce` — "Fallback: succeeded with …" `logger.InfoCF` | Found |
| `pkg/logger/sensitive.go::ScrubSensitiveValues` | Found |
| `pkg/agent/media_downgrade.go::outcomeFallbackEligible` (spec implies `translate_error.go`; the symbol is referenced there and defined in `media_downgrade.go`) | Found (location nuance) |
| `src/store/chatPreferences.ts::verboseChatEnabled`, `src/store/chat/store.ts::setRateLimitEvent`, `src/components/chat/RateLimitIndicator.tsx::formatSeconds`, `src/components/chat/ChatScreen.tsx::VirtualAssistantMessageRow`, `src/components/chat/MessageItem.tsx::ERROR_DETAIL_MAX_CHARS` | Found |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| Agent-facing tool: registered + policy entry for every agent? | N/A | No new tool (spec §17) |
| User-facing: named screen/component renders it? | Yes, but broken as written | `MessageItem.tsx` bubble + new `ProviderRetryIndicator`; the bubble currently discards server text (`src/lib/llm-error.ts::getLLMErrorDisplay`) — MAJ-001 |
| Test plan describes execution, not just authorship? | Partly | TDD 18 is a Playwright run; the #711 oracle under-scans (MAJ-015) |

---

## Unasked Questions

Prompts for the spec author (not for the founder interview):

1. Which attempt's provider, model and `retry-after` drive the line when a fallback chain holds several failures? (MAJ-002, MAJ-006)
2. What exactly does "attempt N of M" count? (MAJ-007)
3. Which reset-header formats are parsed, per provider family? (MAJ-008)
4. What is the transcript entry shape and replay carrier of the fallback note, and does a cooldown *skip* produce it? (MAJ-010) — note `callProviderOnce` logs the fallback whenever `len(fbResult.Attempts) > 0`, and skips are recorded as attempts.
5. What states does `ProviderRetryIndicator` have, and what does the live region announce? (MAJ-016, MAJ-017)
6. What does the accordion show on a non-404 fetch failure, and how is a non-JSON body rendered? (MAJ-018)
7. What are the retention numbers (N per session, TTL, global cap)? (MIN-002)

---

## Questions for the founder

New points only; the spec's own Q1–Q6 (§16) still stand and are not repeated. Each needs a founder decision before the round-1 fix.

1. **FQ-1 — Billing lockout (CRIT-001).** Today a billing verdict takes the model out of the fallback chain for 5 h, then 10 h, 20 h, 24 h. Should an "out of credit" verdict keep that lockout, or only change the message? **A:** keep the lockout, but only for unambiguous markers (HTTP 402, `insufficient_quota` code) — *recommended*; **B:** message only, normal short cooldown; **C:** keep as the spec has it.
2. **FQ-2 — Key fragments in the raw view (CRIT-002).** Providers echo masked keys ("sk-proj-****abcd") that the scrubber cannot recognise. **A:** show a filtered view (type, code, message with key-shaped text masked, status, request id) instead of the raw body — *recommended*, keeps your "never"; **B:** show the raw body and accept that masked fragments may appear.
3. **FQ-3 — Longest automatic wait (MAJ-003).** Providers sometimes say "retry in 6 hours". **A:** auto-retry only when the wait is ≤ 2 minutes; longer waits end the turn with "busy until 14:05" — *recommended*; **B:** no ceiling; **C:** another number.
4. **FQ-4 — Retry when backup models exist (MAJ-002).** **A:** try backups first; auto-retry with countdown only when every model is rate-limited — *recommended*; **B:** retry the primary with a countdown before touching backups.
5. **FQ-5 — Reversing ADR-051's locked decision (MAJ-014).** On 2026-07-21 you locked "raw detail ships on the wire, shown only under Verbose Chat". This spec removes it from the wire and fetches it on demand. **A:** confirm the reversal and have the architect amend ADR-051 — *recommended*; **B:** keep ADR-051 as is (then the spec's Option 1 cannot stand).
6. **FQ-6 — Six messages that say "open Verbose chat for details" (MAJ-005).** Once `detail` goes, Verbose has nothing for them. **A:** keep a short, provider-text-free diagnostic line in Verbose for those codes — *recommended*; **B:** rewrite the six sentences to drop the Verbose promise.
7. **FQ-7 — "Model no longer offered" (MAJ-011).** Your fourth PM-1 example has no template. **A:** in scope now — *recommended*; **B:** defer to a tracked issue.
8. **FQ-8 — Landing order with the security branch (MAJ-013).** **A:** keep `c13c4d279` on the security branch until this feature lands, then this design replaces it — *recommended*, the leak is never reopened; **B:** revert it now and land both together.
9. **FQ-9 — Billing links need a change in the provider-catalog repository (MAJ-012).** **A:** do the catalog-repo change and format version bump as part of this feature — *recommended*; **B:** ship without the billing button now, add it later.
10. **FQ-10 — Stop during a wait (MAJ-017).** **A:** the Stop button stays active and ends the wait at once — *recommended*; **B:** the wait cannot be interrupted.

---

## Verdict Rationale

**BLOCK.**
- CRIT-001 would ship a classifier change that tells free-tier Gemini users their account is empty and disables their model for five hours. That is a production incident, not a copy nit.
- CRIT-002 means the spec claims compliance with the founder's "never show key fragments" through a mechanism it itself documents as unable to deliver it.
- MAJ-001 means the central user-visible promise (the provider-named sentence) would be invisible live, after reload and on channels, while unit tests pass — the documented-but-unwired pattern.
- MAJ-015 means the #711 fix could be undone by the very frame this spec adds, with the #711 oracle still green.
- MAJ-014 leaves the governing ADR contradicting the design.
- MAJ-002 to MAJ-004 and MAJ-016/017 make the retry countdown unreliable, unstoppable-looking, or hostile to screen readers.

### Recommended Next Actions

- [ ] Replace the billing seed with structured, unambiguous markers; add false-positive D2 rows; pin the cooldown per row (CRIT-001, FQ-1)
- [ ] Choose a filtered raw view with a key-shape masker, or record the founder's risk acceptance; fix FR-009/C-11 (CRIT-002, FQ-2)
- [ ] Rewrite D1 around the real assembly points: agent emit sites, `writeErrorTranscriptWithAbandonment`, the `getLLMErrorDisplay` allow-list, channel replies (MAJ-001)
- [ ] Specify retry × fallback × cooldown, a maximum auto-wait, `retry_at`, attempt numbering, and Stop-cancels-wait (MAJ-002, MAJ-003, MAJ-004, MAJ-007, MAJ-017)
- [ ] Widen C-1 / TDD 11 / SC-2 to every frame type with a sentinel body; forbid forwarding `LLMRetryPayload.Error` (MAJ-015)
- [ ] Add the ADR-051 amendment as a deliverable (MAJ-014, FQ-5)
- [ ] Source provider facts from the failing attempt (MAJ-006); restrict reset-header parsing to known formats (MAJ-008)
- [ ] Add the fallback-note replay carrier to the contract wave (MAJ-010); session-scope the detail route like tool-results (MIN-008)
- [ ] Specify the indicator's states and live-region contract, the accordion's failure state and inert rendering, the billing link semantics, and the design-system deliverables (MAJ-016, MAJ-017, MAJ-018, MIN-009, MIN-010)
- [ ] Resolve the Verbose-promise copy (MAJ-005), PM-1 example 4 (MAJ-011), the catalog cross-repo plan (MAJ-012) and landing order (MAJ-013)
- [ ] Fill the structural gaps: number the BDD scenarios; add the missing US-1/2, US-1/3, US-4/3 and US-5/4 scenarios

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/provider-messages-spec-review.md

This is grill round 1 of 2 (fixed). Next: team-lead interviews the
founder on "Questions for the founder", then the spec author fixes
round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the
corrected spec at docs/internal/specs/provider-messages-spec.md — regardless of
this round's verdict.
```

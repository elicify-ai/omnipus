# Provider Messages — Specification (provider/LLM error presentation, #711)

**Status:** Draft (round-1 fix applied — findings from `provider-messages-spec-review.md` resolved or explicitly left open; awaiting grill round 2. Not yet approved.)

- **Source briefs:** `docs/internal/specs/spec-provider-messages.md` (founder decisions PM-1…PM-7, 2026-09-26 — the interview output); `docs/internal/specs/provider-messages-research.md` (OpenCode vs Omnipus, 2026-09-26); GitHub issue #711; governing ADR: [ADR-051 — Media handling and provider error translation](../architecture/ADR-051-media-handling-and-provider-error-translation.md).
- **Discovery status:** Phase 1 is satisfied by the recorded founder decisions PM-1…PM-7 (interview output, confirmed 2026-09-26). PM-6's delegated point — the delivery mechanism for raw provider text — was settled by founder decision **D1** after grill round 1: the wire already carries `detail` (ADR-051 Rev 3 Q2 / RD7) and #711 is a **display** problem, not a wire problem. See §5. The remaining open points are listed in §16 — nothing else waits on the founder before grill round 2.
- **Premise correction (round-1 fix, verified in code):** the round-0 spec assumed #711 required stripping raw detail off the wire and built §5 (REST fetch), §7.5 (retention) and the `detail` deletion on that premise. That premise was wrong. ADR-051 already decided `detail` ships on the wire and is **display-gated** (`src/components/chat/MessageItem.tsx` — the Verbose-gated "Technical details" disclosure, mounted only when `verboseChatEnabled && message.errorDetail`; `src/lib/llm-error.ts::getLLMErrorDisplay` — `detail` returned only when Verbose chat is on; replay strips `detail` before persisting). That mechanism is not broken and is not replaced. This spec now extends it and scopes the remaining #711 risk to this feature's **new** frames (§5, US-4).
- **Codebase intelligence note:** the GitNexus MCP tools are not connected in this session; per `omnipus-shared-rules` rule 9 the exploration below is Read/Grep-based, and every impact row is labelled **Inferred**. Every cited symbol was read in this task on `feat/provider-messages` @ `de5a0cef3` (re-verified during the round-1 fix).
- **Change size:** feature (structural, cross-tree: contracts, `pkg/providers`, `pkg/agent`, `pkg/gateway`, `src/`). Gate: RED/GREEN/CHECK → 8-reviewer gate → founder's yes.
- **Closes #711 by design.** Not by the gateway-security squad's blunt "drop detail" commit (`c13c4d279`), which stays on that branch until this feature lands and is then replaced by this design (working assumption per **FQ-8**, §16 — MAJ-013).

---

## Founder decisions — round 1 (resolves grill round 1's Questions for the founder)

Recorded verbatim-in-meaning from the founder's answers, 2026-09-26. These decisions
supersede the corresponding options in §16 and in `provider-messages-spec-review.md`'s
"Questions for the founder"; the fix round below applies them.

**D1 — ADR-051 stays; #711 is closed by *display*, not by stripping the wire (resolves
Q1, Q2, FQ-5, FQ-6).** Founder: *"the raw message can be sent as well, but not
displayed — the question is not what is sent from our server but how it is displayed;
verbose chat is not sending more messages to the UI, it only displays the session
events differently, it hides less."* Consequence: the wire keeps carrying the raw
detail exactly as ADR-051 already decided. Normal chat **displays** only the clean,
fact-assembled one-line sentence. Verbose chat **displays** more — the raw detail too
— from the same frame; it is not a second, richer message from the server. No REST
fetch (Option 1 is withdrawn), no per-tab Verbose flag at attach, no deletion of the
`detail` field. §5's "Delivery mechanism" options are moot — replace with: one frame,
two displays. The grill's delivery-path findings still apply and must be fixed:
MAJ-001 (the assembled sentence must actually reach the live bubble, the persisted
transcript, and replay — today it doesn't), MAJ-015 (the #711 oracle must be re-scoped
from "nothing raw on the wire" to "nothing raw *rendered* to a non-Verbose viewer",
and must additionally cover the forwarded retry event, not only the error frame).

**D2 — No billing/quota lockout at all (resolves FQ-1; CRIT-001 is moot).** Founder:
*"we should not block a model at all, simplify — if the user loads more usage credits
he cannot continue for 5 hours would be bad."* Remove the billing-cooldown path
(`pkg/providers/cooldown.go::calculateBillingCooldown`) from the `quota_billing`
classification outcome entirely — no extended lockout, no special cooldown curve for
this code. (Whatever normal, non-billing retry/fallback behavior otherwise applies is
unaffected; this decision only removes the billing-specific lockout.)

**D3 — Retry-then-fallback policy (resolves FQ-4, Q3, FQ-3).** Retry the chosen model
**3 times**. Honor the provider's `retry-after` **only when it is ≤ 2 minutes**
(showing the countdown); when the provider asks for longer than 2 minutes, skip the
remaining retries and switch to the fallback model(s) immediately. If no fallback is
configured, end the turn with the message instead.

**D4 — Stop ends the wait (resolves FQ-10).** The Stop control ends both the countdown
wait and the turn — the wait is always interruptible.

**D5 — Verbose shows the raw provider text as-is (resolves FQ-2).** No filtered/masked
view. The founder accepts the key-fragment risk explicitly (masked keys the scrubber
cannot recognise may appear in the raw view).

**D6 — Billing button descoped entirely (resolves Q5, FQ-9).** Founder: build it only
if the provider list data already has billing links. Team-lead checked: the provider
catalog (`pkg/providers/catalog`, sourced from models.dev + litellm + local overrides)
carries no billing/top-up link field today. Remove `billing_url` and the "Open billing
page" button from this spec's scope entirely — not "ship without it now", removed.

**D7 — Fallback note persists; naming (resolves Q6).** The fallback note persists in
history (unchanged from the spec's recommendation). Call the answering model the
**"Fallback model"** in copy — never "backup".

**D8 — Provider messages get their own visual format (NEW).** Provider-message
presentation (rate-limit countdown, fallback-model note, out-of-credit, Verbose
facts+raw) must not look like a regular assistant response. Frontend produces 2-3
visual options as a clickable static demo (static Storybook build or static HTML
snapshot — never a live dev server) covering all four cases; squad-lead real-browser-
checks it before hand-off. Runs in parallel with the spec fix round, not gating it.

**Still open — do not block the fix round on these; team-lead brings them to the
founder next round:**

- **FQ-7** — add the "model no longer offered" template now, or defer to a tracked
  issue.
- **Q4** — `quota_billing` attribution (`config` vs `provider`) — now that the lockout
  (D2) and the billing button (D6) are both gone, is this attribution still
  meaningful, or is there nothing left for it to gate?
- **FQ-8** — landing order with `gateway-security`'s `c13c4d279`: team-lead is
  defaulting to the review's recommendation (keep it on that branch until this feature
  lands, then this design replaces it) absent a correction.

**Fix-round clarifications (appended by the round-1 fix; the D1–D8 text above is
unchanged — these only cross-reference where the fix landed each decision):**

- **D1** → §5 rewritten to "one frame, two displays"; §7.5 and §7.6 withdrawn; the
  old C-1 wire-scan oracle re-scoped to display gating (C-1/C-2 in §4); MAJ-001 fixed
  in §7.3/§7.4 (assembly points named); CRIT-002 resolved by D5's risk acceptance
  (C-19 in §4 reworded — "never" is now claimed only for the assembled message and
  facts, never for the raw view).
- **D2** → `calculateBillingCooldown` deleted (§7.3); CRIT-001's classifier-accuracy
  half is still fixed — the billing detector's seed vocabulary is narrowed and the
  false-positive dataset rows added (§7.3, §12 D2, C-5/C-6/C-7).
- **D3** → the exact retry policy is specified in §7.4, including MAJ-007's attempt
  semantics (3 total provider calls including the first; a retry frame's `attempt` is
  the call about to be made, 2..3) and the 2-minute ceiling as MAJ-003's auto-wait
  ceiling (C-8/C-9).
- **D4** → hard requirement + BDD scenario A-7 and a context-cancellation unit test
  (§10 set A, §11 row 8).
- **D6** → §7.6 deleted, C-10-old deleted, FR-007-old deleted, MIN-009 moot, Q5/FQ-9
  removed from the open list; no button or link exists anywhere in this feature.
- **D7** → note text, transcript shape, replay carrier and the skip-case rule are
  specified in §7.4 (C-17/C-18).

## 1. Problem & actors

**Actors.** The **user** (reads chat; may have Verbose chat on or off — a per-device, client-only preference, `src/store/chatPreferences.ts::verboseChatEnabled`); the **operator** (the same person on a self-hosted install; fixes credentials and billing); the **agent loop** (produces provider errors and retry/fallback state); the **gateway** (the single WS choke point that shapes what reaches any browser).

**Problem.** Four failures, all verified in this task:

| # | Failure | Evidence |
|---|---------|----------|
| F1 | A provider 429 arrives as "The model provider is temporarily overloaded. Wait a moment, then retry." — no reset time, no countdown, no provider name, even when the provider sent `retry-after`. | `contracts/components/schemas/LLMError.yaml` (x-user-messages `rate_limited`); `pkg/providers/common/common.go::ProviderError` (captures no response headers) |
| F2 | Billing/quota exhaustion gets the same "wait and retry" advice as a transient 429 — actively wrong. The user-side classifier has no billing code; "quota exceeded" sits in `rateLimitSubstrings` → `CodeRateLimited`. | `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus`; routing side: `pkg/providers/error_classifier.go::classifyByMessage` checks `rateLimitPatterns` **before** `billingPatterns`, and the status switch maps 429 → `FailoverRateLimit` before any body check |
| F3 | The raw provider text display boundary is enforced for **error** frames only (the existing Verbose-gated disclosure), and the boundary is exactly what this feature's NEW frames must not break: the retry event payload already carries the raw provider error string (`LLMRetryPayload.Error`), and today it is safe only because the frame is never forwarded. Forwarding it (this feature) without a facts-only rule would put provider bodies into the non-Verbose UI. | `src/components/chat/MessageItem.tsx` (disclosure mounted only when `verboseChatEnabled && message.errorDetail`); `src/lib/llm-error.ts::getLLMErrorDisplay` (detail only when Verbose on; replay carries no detail); `pkg/agent/events.go::LLMRetryPayload` (`Error: err.Error()`); `pkg/gateway/websocket_forward.go` (`EventKindLLMRetry` on the explicitly-not-forwarded list) |
| F4 | Inline retries (delegated 429 backoff; streaming resets) are silent; a successful fallback model swap is log-only. The user sees a hung turn or an unexplained style change. | `pkg/agent/loop_provider_retry.go::callProvider` (retries logged only; `EventKindLLMRetry` is on the WS-forwarder's explicitly-not-forwarded list, `pkg/gateway/websocket_forward.go`); `pkg/agent/loop_run_turn.go::callProviderOnce` (fallback success is `logger.InfoCF` only) |

**Why now:** the founder chose the whole mechanism (PM-1: facts instead of raw text in the *displayed* line) and confirmed by D1 that the fix is display-side.

## 2. Existing codebase context (verified this task)

### Symbols involved

| Symbol | Role in this feature |
|---|---|
| `pkg/providers/common/common.go::ProviderError`, `HandleErrorResponse`, `WrapHTMLResponseError` | modify — gain parsed response headers (`retry-after` integer-seconds / HTTP-date, `retry-after-ms`; request ID) AND the producing provider id + model, stamped at the boundary (MAJ-006) |
| `pkg/providers/common/common.go::ProviderError.Error()`, `ResponsePreview` | keep — log-side rendering unchanged; the scrubber stays for logs |
| `pkg/providers/error_classifier.go::ClassifyError`, status switch, `classifyByMessage` | modify — unambiguous-account-state billing detection (HTTP 402, structured `insufficient_quota` code field) before rate-limit in the message path and on the 429 path; billing verdicts take the standard failure path |
| `pkg/providers/cooldown.go::calculateBillingCooldown` | **delete** (D2 — no billing lockout; per the founder's delete-superseded-code ruling the unreachable function is removed, not left dead) |
| `pkg/agent/translate_error.go::TranslateLLMError`, `classifyByProviderError`, `classifyByHTTPStatus`, `rateLimitSubstrings`, `isRetryable` | modify — new `quota_billing` code; narrowed billing detector before rate-limit; facts threading; template assembly into `Message` (MAJ-001) |
| `pkg/agent/translate_error.go::buildDetail`, `BuildDetail` | **keep unchanged** — `detail` stays on the wire per ADR-051 Rev 3 Q2 / RD7 and founder D1 (the round-0 "delete detail" plan is withdrawn) |
| `pkg/agent/translate_error.go::ProviderError` (agent-side), `ProviderErrorFromFailover`, `providerErrorFromChain` | modify — carry the failing attempt's provider/model + new header facts (MAJ-006) |
| `pkg/agent/events.go::ErrorPayload` | modify — gains provider/model identity + facts; the agent emit site assembles the templated sentence into `Message` when facts are present |
| `pkg/agent/events.go::LLMRetryPayload`, `EventKindLLMRetry` | extend additively — gains provider, model, `retry_at`; forwarded as the new `provider_retry` frame **built from named fields only** — `LLMRetryPayload.Error` stays log-only and is never read by the hub handler (MAJ-015, C-2) |
| `pkg/gateway/websocket_forward_hub.go::hubError` | modify — threads facts (provider, model, retry facts) onto the frame; `Detail` population untouched (ADR-051). Note today's `if p.Code != "" { message = p.Message }` pass-through stays — the sentence is assembled upstream, in the agent (MAJ-001) |
| `pkg/gateway/websocket_forward_hub.go::hubSyncTap` | modify — new handlers for retry/fallback frames |
| `pkg/agent/turn_transcript.go::writeErrorTranscriptWithAbandonment`, `trustedInternalStageSet` | modify — the new templated codes join the trusted set (or the assembled message is threaded through `appendClassifiedError` without re-deriving) so the provider-named sentence persists instead of `defaultUserMessage(code)` (MAJ-001) |
| `src/lib/llm-error.ts::getLLMErrorDisplay` | modify — renders `le.message` for the new templated codes only (an explicit allow-list mirroring the `delegated_task_limit` exception); the `detail` branch is kept as is |
| `src/components/chat/MessageItem.tsx` (native `<details>` "Technical details" disclosure) | modify — the ONE reuse target (MIN-010): extended under Verbose chat with facts lines; `detail` rendered as inert text (MAJ-018) |
| `src/components/chat/RateLimitIndicator.tsx::formatSeconds`, per-second interval | pattern source (reused, not imported) — the provider-retry indicator is a new component per the design-system skill |
| `src/store/chat/store.ts::setRateLimitEvent`, `src/store/chat/frames.ts` | modify — new frame cases; retry state separate from the own-limiter slot |
| `pkg/agent/session_worker.go` / `pkg/agent/loop.go` (`TranslateTurnError(err).Message` channel-reply sites) | modify — the same facts-aware translator, so channel replies (Telegram/Discord) carry the assembled sentence too (MAJ-001 iv) |
| `contracts/components/schemas/LLMError.yaml`, `contracts/asyncapi.yaml` (inline LLMError/LLMErrorReplay kept in lockstep), new `ProviderRetryFrame.yaml` / `ProviderFallbackFrame.yaml` + fallback-note replay carrier | modify — §7.1 |
| `pkg/api/generated/`, `src/lib/api/generated/` | regenerate only — never hand-edited |

### Impact assessment (Inferred — GitNexus unavailable; grep-swept)

| Symbol modified | Risk | d=1 dependents |
|---|---|---|
| `TranslateLLMError` | MEDIUM | `hubError`, `appendErrorTranscript`/`appendClassifiedError` (both choke points), `session_worker.go`/`loop.go` channel-reply sites, media strip-retry gate (`outcomeFallbackEligible` reads `CodeUnknown` verdicts — residual-4xx verdict MUST NOT change) |
| `writeErrorTranscriptWithAbandonment` trusted set | MEDIUM | every error transcript write; replay lookup by `ErrorCode`; the existing trusted-stages bypass must not widen beyond the new templated codes |
| `common.ProviderError` | LOW-MEDIUM | every provider adapter's error path, `errorToProviderError`, codex provider's request-id logging (`codex_provider.go` — keep; unify later) |
| `ClassifyError` order + `calculateBillingCooldown` deletion | MEDIUM | fallback routing + cooldown curves (`calculateStandardCooldown` stays the only curve); delegated 429 retry gate (`shouldRetryDelegatedRateLimit`) |
| `getLLMErrorDisplay` allow-list | LOW | every error-bubble render path; other codes' copy-rule tests must stay green |
| `LLMError` wire shape (additive facts) + new frames | MEDIUM | generated Go/TS catalogues (regenerated), Zod parse, copy-rule tests (Go + TS) |

**Load-bearing constraint (do not break):** `pkg/agent/translate_error.go::CodeUnknown` is the media strip-retry gate input (`media_downgrade.go::outcomeFallbackEligible` fires only on `CodeUnknown`). The new billing detector must not re-point residual 4xx verdicts.

## 3. Founder decisions → spec sections

| PM | Decision (short) | Where settled |
|---|---|---|
| PM-1 | Facts instead of raw text; ONE plain-English line assembled from facts | §6 (exact templates), §7.3 (assembly points, MAJ-001), D1 |
| PM-2 | Provider + model may be named; keys never named; key material never; request ID Verbose-only | §6 rules, C-19/C-20 |
| PM-3 | Billing gets its own message; "Open billing page" only when the URL is already known | Message part: US-2. Button part: **withdrawn by D6** — no button, no link, no `billing_url` |
| PM-4 | Retry countdown: yes. Fallback note: yes. | US-1, US-6, D3/D4/D7 |
| PM-5 | Context length stays as is (auto trimming, no note, no compaction) | §9 US-7, C-23 |
| PM-6 | Verbose = facts + accordion with raw provider JSON, reusing the tool-call accordion pattern; delivery mechanism = spec's to settle, founder confirms | Settled by **D1**: the existing `detail` display path IS the delivery — §5; the one reuse target is the `MessageItem.tsx` native `<details>` disclosure (MIN-010) |
| PM-7 | #711 closes by this design; the blunt drop-detail commit is removed from the security branch | Header, US-4; landing order as the FQ-8 working assumption (§16, MAJ-013) |

PM-1's fourth example ("OpenRouter no longer offers z-ai/glm-4…") has **no template and no classification in this spec** — deferred pending FQ-7 (§16). It ships as today's generic sentence until the founder rules.

## 4. Behavioral contract

### Primary flows

- When a provider rate-limits a turn and sent a `retry-after` of 2 minutes or less, the system retries that model automatically (3 provider calls total) and shows a live countdown naming the provider, the wait, and the attempt ("OpenRouter is busy. Retrying automatically in 1:32 (attempt 2 of 3).").
- When the provider asks for a wait longer than 2 minutes, no countdown is shown; the remaining retries on that model are skipped and the fallback chain is tried immediately (or the turn ends with the terminal message when no fallback is configured).
- When the automatic retries stop, the error line names the provider (the one that actually failed) and says the turn can be retried.
- When the provider says the account is out of credit, the error line says so and names the provider. There is no button and no link (D6), and no lockout of the model (D2).
- When the provider rejects the API key, the error line names the provider and points at Settings → Providers.
- When a fallback model answered instead of the unavailable one, a quiet note names the fallback ("Answered by the Fallback model ({X}) because {Y} was unavailable.") — live, persisted, and on replay (D7).
- Verbose chat **displays** more of the same frame — the facts lines plus the raw provider detail in the existing "Technical details" disclosure. Non-Verbose chat displays only the assembled one-line sentence. Nothing is fetched on demand; nothing is re-sent per viewer (D1).
- Raw provider text is never **rendered** outside a Verbose-gated disclosure — including by the new retry/fallback frames, which carry structured facts only.

### Explicit non-behaviors & safeguards

Qualitative:

- Raw provider text is not removed from the wire — ADR-051 (Rev 3 Q2 / RD7) keeps `detail` on every error frame, and D1 confirms it. What must not happen is **rendering** it to a non-Verbose viewer, or carrying it on the NEW retry/fallback frames at all.
- Transport/timeout inline retries (`loop_run_turn_response.go::retryTimeout`) remain unannounced — out of scope (MIN-001).
- The system must not name key labels, key material, or key fragments in the assembled message or in facts — ever (PM-2). The Verbose raw view shows the provider body as-is; D5 accepts that masked key fragments the scrubber cannot recognise may appear there (C-19).
- The system must not show any billing button or billing link (D6) — the `quota_billing` sentence stands alone.
- The system must not block or cool down a model differently because the verdict is `quota_billing` (D2) — the billing-specific cooldown curve is deleted; the standard failure path applies.
- The system must not change context-length behaviour: no token counts, no compaction, no extra note (PM-5).
- The system must not auto-retry non-retryable failures (auth, billing, content policy): the same request fails identically.
- The system must not retry after any streamed bytes reached the turn (existing guard, `callProvider` — a retry would duplicate visible and persisted content).
- The system must not treat `request_id` as user-facing on its own — it renders only under Verbose chat (PM-2).
- The system must not remove the scrubber: it stays as defense-in-depth for logs (`gateway.log`, previews) and for the `detail` preview, exactly as today.

Machine-verifiable (constraint IDs used by tests):

| ID | Constraint |
|---|---|
| C-1 | No provider body text (or any raw provider error text) is **rendered** outside a Verbose-gated disclosure: error bubbles show the assembled line only when Verbose is off; the `provider_retry`/`provider_fallback` UIs render facts only. The #711 display-gating oracle (MAJ-015's re-scope). |
| C-2 | The new `provider_retry` / `provider_fallback` frames are built from named fields only; the hub handler never reads `LLMRetryPayload.Error` or any fallback attempt's raw error string. A mutation that forwards it must turn the C-1/C-2 scan red. |
| C-3 | `quota_billing` is in the code enum; the generator's message/attribution bijection hard-fails on any gap (existing generator behaviour). |
| C-4 | Every code with a templated variant has an `x-user-messages` entry carrying it; the generator's extended hard-fail enforces the bijection (C-3 + C-4 together). |
| C-5 | The billing detector's vocabulary is exactly: HTTP 402; the OpenAI `insufficient_quota` error **code** matched on the structured JSON `code`/`type` field (not free text); the unambiguous account-state phrases "insufficient credits", "insufficient balance", "credit balance too low", "payment required". "billing", "usage limit reached" and "exceeded your current quota" are NOT in the vocabulary (CRIT-001). |
| C-6 | Every D2-dataset negative row — the Gemini per-minute 429 text, a Codex "usage limit … try again in 3 days" message, an OpenAI 429 whose message links to a billing page, and a plain "too many requests" — classifies `rate_limited`, never `quota_billing`. |
| C-7 | A `quota_billing` verdict never produces a billing-specific cooldown or lockout of any length: `calculateBillingCooldown` is deleted and the standard failure path (`calculateStandardCooldown`) is the only curve. The cooldown chosen for each D2 dataset row is pinned by test (D2). |
| C-8 | `max_attempts` = **3 total provider calls including the first**; a retry frame's `attempt` = the number of the call **about to be made** (2..3); `attempt` ≤ `max_attempts` on every retry frame (MAJ-007, D3). |
| C-9 | Auto-retry honors the provider's `retry-after` only when it parses to ≤ 120 s (countdown shown). A wait above 120 s, or a malformed/past value, means: no auto-retry of that model — skip its remaining retries, move to the fallback chain, or end the turn with the terminal message when no fallback is configured. `retry-after: 0` and a missing header both take the backoff schedule (immediate first backoff step). (MAJ-003, D3, MIN-003) |
| C-10 | A provider auto-retry is scheduled only when zero streamed bytes have reached the turn. |
| C-11 | The retry frame carries `retry_at` (server wall clock, RFC 3339); the indicator renders `retry_at − now`, clamped at 0 — never a static `retry_after_seconds` counted from frame receipt (MAJ-004). |
| C-12 | Stop during a retry wait ends the turn within 1 s and no further attempt is made; the wait sleep is context-cancellable (D4, MAJ-017). |
| C-13 | The assembled sentence reaches all four surfaces — live bubble, persisted transcript, replay, channel reply — for the templated codes; persisted transcripts carry code + assembled message, never body/headers/raw JSON; replay strips `detail` (existing). (MAJ-001) |
| C-14 | The SPA renders `le.message` for the templated codes ONLY (an explicit allow-list mirroring the `delegated_task_limit` exception); every other code keeps catalogue copy, proven by test (MAJ-001, MIN-007). |
| C-15 | Countdown text uses mm:ss ("1:32"), per PM-1's own example; the own-limiter `RateLimitIndicator` format ("1m 32s") is unchanged. |
| C-16 | `{provider}`/`{model}` in any message or frame come from the attempt that actually produced the classified error (the failing attempt), never unconditionally from the primary candidate; a skip-only chain has no provider fact, so the catalogue fallback applies (MAJ-006). |
| C-17 | The fallback note persists: a `session.TranscriptEntry`-family shape plus a replay carrier defined in the contract wave; the text says "Fallback model" and never "backup" (D7, MAJ-010). |
| C-18 | A cooldown-driven **skip** (not only a hard failure) that leads to a fallback answer also produces the note — at most once per session per (unavailable, answered) pair until the pair changes (D7, MAJ-010). |
| C-19 | No assembled message and no facts field ever names a key label or contains key material/fragments. The Verbose raw view shows the provider body as-is (scrubbed of registered credential values only); D5 accepts that unrecognised fragments may appear there — no "never" is claimed for the raw view (CRIT-002 resolved by risk acceptance). |
| C-20 | `request_id` appears on the wire inside facts but renders only under Verbose chat. |
| C-21 | The Verbose disclosure renders `detail` as inert text — never HTML-rendered, never markdown-rendered — pretty-printed only when it parses as JSON; `detail` is not always JSON (`WrapHTMLResponseError` case) (MAJ-018). |
| C-22 | Rate-limit wait capture parses ONLY `retry-after` (integer seconds or HTTP-date) and `retry-after-ms`; no `x-ratelimit-reset*` parsing exists (MAJ-008, simpler option — see §7.2 for why). |
| C-23 | Context-length errors keep today's code, copy, and behaviour (PM-5) — regression-guarded. |

## 5. Delivery: one frame, two displays (PM-6, D1)

**The round-0 options analysis (on-demand REST fetch / per-tab Verbose flag / WS request-reply) is withdrawn.** It was built on a wrong premise — that #711 required keeping raw provider text off the wire. The founder corrected that directly (D1): *"the question is not what is sent from our server but how it is displayed."*

What already exists and is kept unchanged (all verified in this task):

- **`detail` ships on the wire** on every typed error frame — ADR-051 Rev 3 operator decision Q2 and RD7; `pkg/gateway/websocket_forward_hub.go::hubError` sets `Detail` unconditionally today, and keeps doing so.
- **Only a Verbose viewer's SPA renders it** — `src/components/chat/MessageItem.tsx` mounts the "Technical details" disclosure only when `verboseChatEnabled && message.errorDetail` (absent from the DOM otherwise, not hidden); `src/lib/llm-error.ts::getLLMErrorDisplay` returns `detail` only when Verbose chat is on.
- **Replay never carries it** — the replay payload type has no `detail` field and the transcript strips it before persisting (`LLMErrorReplay`; ADR-051 invariant (5): `detail` computed live, never persisted).

This mechanism is not broken and is not replaced. It already satisfies ADR-051's contract and D1's "one frame, two displays": the frame is identical for every viewer; the *renderer* hides less when Verbose is on.

What this feature adds on top:

1. **Facts lines under Verbose** — the same error bubble's Verbose view additionally shows the structured facts (provider, model, request id) as inert text lines.
2. **The new frames carry facts only** — `provider_retry` and `provider_fallback` are new frame types with no raw-text field at all (C-2). Their UIs render structured facts (provider, model, countdown, attempt) and never the raw provider error string — the same display-gating discipline the existing error bubble already applies (C-1, MAJ-015's re-scope).
3. **No new fetch, no retention, no `error_id`** — there is no `provider-detail` REST route, no in-memory retention map, and no per-error key. MIN-002's retention parameters, MIN-006's replay-fetch oracle and MIN-008's session-binding finding are all moot for that reason. (MIN-001 — unannounced timeout/stream-reset retries — is handled as a §4 non-behavior, not by this section.)

**CRIT-002 resolution (via D5):** the raw view is the existing `detail` path, shown as-is. The founder explicitly accepts the residual-fragment risk (masked keys the scrubber cannot recognise). C-19 no longer claims "never" for the raw view — the "never" covers the assembled message and the facts fields, which are server-assembled and carry no provider body text.

## 6. The one-line messages — exact templates (PM-1, PM-2)

**Assembly happens in the agent, not the gateway (MAJ-001 correction).** The round-0 claim "the gateway assembles the final line" was wrong about the architecture: `hubError` passes the agent's `p.Message` straight through when the payload carries a code (`websocket_forward_hub.go::hubError`, the `if p.Code != ""` branch), and the transcript writer would overwrite any assembled sentence with `defaultUserMessage(code)` (`pkg/agent/turn_transcript.go::writeErrorTranscriptWithAbandonment`). So the agent-side classifier assembles the templated sentence into `ErrorPayload.Message` **at the emit site**, when facts are present, and the persistence and SPA layers are taught to keep it (§7.3/§7.4). The gateway stays a forwarder.

**Templates.** Slot vocabulary: `{provider}` = provider display name from the catalog, sourced from the **failing attempt** (C-16); `{attempt}`/`{max}` = integers; `{countdown}` = live, client-rendered (mm:ss, C-15); `{answered_model}`/`{unavailable_model}` for the fallback note. When facts are absent (provider unknown), the existing catalogue sentence is the fallback — never a blank.

| Code | Template (exact) | Rendered when |
|---|---|---|
| `rate_limited` — retry in progress (live) | `{provider} is busy. Retrying automatically in {countdown} (attempt {attempt} of {max}).` | During auto-retry; the SPA assembles this line from the `provider_retry` frame's facts (provider, attempt, max, `retry_at`) — client-side template, C-1/C-2-safe because every slot is a structured fact |
| `rate_limited` — terminal | `{provider} is busy right now. You can retry the turn.` | Retries exhausted, or no auto-retry path (wait above the ceiling with no fallback) |
| `quota_billing` (new) | `{provider} says your account is out of credit.` | Always (facts present). No button, no link — D6. Retry advice governed by Q4 (§16) |
| `provider_auth_failed` | `{provider} rejected the API key. Check the key in Settings → Providers.` | Facts present |
| fallback note | `Answered by the Fallback model ({answered_model}) because {unavailable_model} was unavailable.` | Fallback success, including cooldown-skip-produced fallbacks at the C-18 rate (frame + persisted note; D7) |

All other codes keep today's catalogue copy unchanged (generic, no provider naming in this feature). Context length stays exactly as is (PM-5, C-23). The four PM-1 example sentences are covered by the first four rows; the fourth founder example (model no longer offered) is **not** templated — FQ-7 deferred (§16).

**Copy rules carried over:** a `config` message never advises retry; whether `quota_billing` is attributed `config` (and therefore copy-rule-bound to avoid retry advice) is **Q4** — still open (§16).

**Contract mechanics for templates (simplified per OBS-002).** The round-0 design — a separate `x-user-message-templates` YAML block, a closed slot-vocabulary grammar and generator extensions in two generators — was overbuilt for four server-side sentences with one or two slots each. Simplified: each relevant `x-user-messages` entry gains an optional sibling `provider_message` string (the templated variant); the generator enforces only (a) the existing code↔message bijection extended to entries carrying `provider_message` (C-3/C-4) and (b) that slots used are from the closed set `{provider}`, `{answered_model}`, `{unavailable_model}` — a simple token check, not a template engine. The Go translator substitutes `{provider}` (failing attempt) when facts are present and falls back to the static catalogue sentence otherwise. The live-retry line is **not** a server template: the countdown is inherently client-rendered and the frame carries only structured facts, so the line is assembled client-side from those facts and pinned by component tests (C-15).

## 7. Backend work breakdown

Order is contract-first (Hard Constraint #8): §7.1 lands as one atomic commit with regenerated artifacts before any Go/TS consumer code.

*Withdrawn from the round-0 plan:* former §7.5 ("Verbose raw-detail retention + fetch") and former §7.6 ("Provider catalog `billing_url`") — deleted by D1 (no fetch, no retention) and D6 (no billing button). No REST route is added; `contracts/openapi.yaml` is untouched by this feature.

### 7.1 Contract wave (first)

1. `contracts/components/schemas/LLMError.yaml`: add `quota_billing` to the enum + `x-user-messages` entry (attribution per Q4 — placeholder `provider` until ruled); add the optional `provider_message` templated variants to the relevant `x-user-messages` entries (§6, OBS-002 simplification); add the optional `facts` object (`provider`, `model`, `retry_after_seconds`, `retry_at`, `attempts`, `max_attempts`, `request_id` — all optional, `request_id` flagged Verbose-render-only, no `billing_url`, no `error_id`). **`detail` stays exactly as it is** (D1 — the round-0 removal plan is withdrawn; the field is optional today so nothing breaks).
2. `contracts/asyncapi.yaml`: keep the inline `LLMError` / `LLMErrorReplay` copies in lockstep (the file's own comment requires it); add the two new messages (`provider_retry`, `provider_fallback`) on the chat channel; add the fallback-note **replay carrier** (a `ProviderFallbackNote` replay entry/shape on the session channel) so the persisted note is not a live-only orphan (MAJ-010, C-17).
3. `contracts/components/schemas/`: new `ProviderRetryFrame.yaml` — `type: provider_retry`; `session_id`, `turn_id`, `provider`, `model`, `retry_at` (RFC 3339), `retry_after_seconds`, `attempt`, `max_attempts`, `error_code`; + `seq` per the #823 pattern; **no `error` field of any kind** (C-2). New `ProviderFallbackFrame.yaml` — `type: provider_fallback`; `session_id`, `turn_id`, `answered_model`, `unavailable_model`, `reason` (enum: `failed` | `cooldown_skip`); + `seq`; no raw error text. `ReplayErrorFrame` and `LLMErrorReplay` unchanged (no facts object on replay — the message itself carries the provider naming; C-13).
4. `contracts/openapi.yaml`: **unchanged** — no new route (§5).
5. `scripts/gen-contracts.sh` (bijection hard-fail extended to `provider_message`-carrying entries + the closed-slot token check), commit spec + generated diff atomically; `make verify-contracts` green.

### 7.2 Capture (provider side)

- `common.ProviderError` gains `RetryAfterSeconds` (presence-distinguishable — a pointer or explicit flag, because `retry-after: 0` is legal and means "immediate", C-9) and `RequestID`; parsed at `HandleErrorResponse` and `WrapHTMLResponseError` from `retry-after` (integer seconds **or** HTTP-date; a past date or a malformed value → fact absent, first value wins on duplicates) and `retry-after-ms` (rounded up, ≥ 1).
- **Reset-header scope (MAJ-008 — the simpler option, chosen):** parse `retry-after` and `retry-after-ms` ONLY. The generic `x-ratelimit-reset*` family is dropped from scope: its formats are not one thing (Go durations on OpenAI, RFC 3339 on Anthropic, epoch-milliseconds on OpenRouter, JSON-body `retryDelay` on Gemini), so correct parsing needs a per-provider-family format table with a parse test per row — parser surface with no payoff, because D3's 2-minute ceiling makes every multi-hour reset header irrelevant anyway (a wait above 120 s is "no auto-retry" regardless of how precisely it is known). A duration string like `6m0s` or a millisecond epoch parsed as seconds would silently yield garbage; not parsing them yields the backoff schedule, which is always safe.
- Request-ID header candidates: `x-request-id`, `request-id`, `x-amzn-requestid`, `cf-ray` (last resort — it is Cloudflare's edge ID, not a provider request id; labelled as such in the Verbose facts, OBS-003).
- MAJ-006: the producing provider id + model are stamped onto `ProviderError` at the boundary (`HandleErrorResponse`), so every downstream classifier and assembler names the attempt that actually failed — never unconditionally `rt.activeCandidates[0]`.
- The scrubber is untouched: `BodyPreview`/log lines keep `ScrubSensitiveValues`; `buildDetail` keeps scrubbing before its preview cut, as today.

### 7.3 Classify (both classifiers) + assemble (MAJ-001)

- **User side** (`translate_error.go`): new `CodeQuotaBilling` (`quota_billing`); new billing detector with the C-5 vocabulary checked **before** `rateLimitSubstrings` on body-bearing paths, and read on the 429 path before the 429 short-circuit declares rate-limit — 429 + a C-5 marker lands `quota_billing`; 429 with prose-only quota wording ("exceeded your current quota", "usage limit reached") stays `rate_limited` (those phrases are NOT in the vocabulary, C-5/C-6). `isRetryable` returns false for it. The residual-4xx → `CodeUnknown` verdict is untouched (media strip-retry gate).
- **No billing lockout (D2):** a `quota_billing` verdict takes the standard failure path — `calculateStandardCooldown` at most, exactly like any other failure. `pkg/providers/cooldown.go::calculateBillingCooldown` is **deleted** (unreachable after this change; delete-superseded-code ruling). A test pins the curve chosen for every D2 dataset row (C-7).
- **Providers side** (`error_classifier.go`): HTTP 402, or 429 with a structured C-5 marker (`insufficient_quota` on the JSON `code`/`type` field) → `FailoverBilling`; the C-5 phrases are evaluated before the rate-limit patterns in `classifyByMessage`. Routing behaviour is otherwise preserved (`shouldRetryDelegatedRateLimit` keeps its rate-limit semantics; billing was never in its retry set).
- **Facts threading:** `TranslateLLMError`'s verdicts gain facts from `pe` (the new header fields, the stamped provider id + model) via an additive variant (no signature break at the two choke points' existing callers).
- **Assembly (the MAJ-001 fix — the sentence must actually reach the user):**
  1. **Agent emit sites** assemble the templated sentence into `ErrorPayload.Message` when facts (provider/model of the failing attempt) are present; catalogue fallback otherwise. `hubError`'s existing `p.Message` pass-through then carries the assembled sentence — no gateway-side assembly.
  2. **Transcript writer:** `writeErrorTranscriptWithAbandonment` currently overwrites the message with `defaultUserMessage(code)` for coded, non-trusted errors. Fix: extend the trusted-message set (`isTrustedInternalStage`/`trustedInternalStageSet`) with the new templated codes — or thread the already-assembled `LLMError` through `appendClassifiedError` without re-deriving — so the provider-named sentence persists. The widening is limited to exactly the templated codes; the existing trusted stages' semantics are unchanged.
  3. **SPA:** `getLLMErrorDisplay` renders `le.message` for the templated codes only — an explicit allow-list mirroring the existing `delegated_task_limit` exception — with a test proving every other code still renders catalogue copy (C-14, MIN-007). This is a stated trust-boundary change: the SPA starts trusting server message text for these codes.
  4. **Channel replies:** the `TranslateTurnError(err).Message` sites (`pkg/agent/session_worker.go`, `pkg/agent/loop.go`) use the same facts-aware translator, so Telegram/Discord replies carry the assembled sentence (MAJ-001 iv).
  5. BDD scenarios pin replay text and channel text (DG-7, DG-8; §10).
- Loop identity: the loop threads provider display name + model of the **failing attempt** into `ErrorPayload` (additive fields) so the agent-side assembly can name it (C-16).

### 7.4 Retry visibility + fallback note (root path)

- **Policy (D3, made precise per MAJ-002/MAJ-003/MAJ-007):** generalise the existing delegated pattern (`loop_provider_retry.go::callProvider`) to root turns **for `rate_limited` only**. `max_attempts` = **3 total provider calls including the first**; a retry frame's `attempt` is the number of the call about to be made (2..3). Retry-then-fallback: the current candidate is retried before the chain moves on.
  - Provider `retry-after` present and ≤ 120 s: wait exactly that (countdown shown from `retry_at`).
  - `retry-after` absent, `retry-after: 0`, or unparseable: exponential backoff 2 s × 2 capped 30 s with 25% jitter — every such wait is inherently under the ceiling.
  - `retry-after` > 120 s: **no countdown**; skip the candidate's remaining retries and move to the fallback chain immediately; no fallback configured → end the turn with the terminal `rate_limited` message (C-9).
  - The zero-streamed-bytes guard (C-10) is kept; each retry emits `EventKindLLMRetry` (exists today) with `LLMRetryPayload` extended additively (provider, model, `retry_at`).
  - **Interaction with the fallback chain and cooldown (MAJ-002's rule):** within one turn the retry loop owns the current candidate and does not consult the chain; a candidate's retries exhaust (or are skipped) before `FallbackChain.Execute` moves to the next candidate. The ≥ 60 s standard cooldown a failed candidate picks up only matters across turns; when the chain reaches a candidate already in cooldown from a prior turn, that candidate is skipped as today (a `cooldown_skip` attempt — relevant to the fallback note, C-18). Unit rows: 1-candidate; 2-candidates-both-429 (each retried in turn, frames name each candidate); 2-candidates-one-billing (the billing candidate is not retried — `retryable: false` — the chain moves on at once).
  - **Which candidate's facts drive a frame (MAJ-002/MAJ-006):** each retry frame names the candidate it is about to call; a candidate switch issues a new frame with that candidate's identity and a reset attempt counter. The terminal message names the last candidate that actually failed (C-16); a skip-only chain has no provider fact and gets the catalogue fallback.
- `hubSyncTap` gains a handler forwarding `EventKindLLMRetry` to the new `provider_retry` frame (the kind moves off the explicitly-not-forwarded list in `pkg/gateway/websocket_forward.go`). The handler builds the frame **from named fields only** and never reads `LLMRetryPayload.Error` (C-2, MAJ-015) — that field stays for logs.
- The retry frame carries `retry_at` (RFC 3339 server wall clock) so a tab attaching mid-wait renders the remaining time, not the original wait (C-11, MAJ-004).
- **Stop ends the wait (D4, C-12):** the wait sleep is context-cancellable (`sleepWithContext` — the pattern already used by `loop_provider_retry.go::callProvider`); Stop cancels the turn context, the sleep returns at once, and no further attempt is made. Hard requirement with BDD scenario A-7 and a unit test on the context cancellation.
- **Fallback note (D7, MAJ-010):** on fallback success — currently `logger.InfoCF` in `callProviderOnce` — additionally emit the `provider_fallback` frame AND persist the note. The persisted note gets a `session.TranscriptEntry`-family type/status and the replay carrier from §7.1 (not a live-only orphan). A cooldown-driven **skip** that leads to a fallback answer also produces the note (the frame's `reason: cooldown_skip`), **at most once per session per (unavailable, answered) pair until the pair changes** — a chat must not fill with repeated notes during a long cooldown (C-17/C-18). Text per §6: "Answered by the Fallback model ({X}) because {Y} was unavailable." — never "backup".
- On exhaustion, the terminal error frame carries the terminal `rate_limited` template line (assembled agent-side per §7.3).

## 8. Frontend work breakdown

1. **Frames & store:** `frames.ts` cases for `provider_retry` and `provider_fallback`; the error-frame path reads `facts`; retry state is separate from the own-limiter slot (`store.ts::setRateLimitEvent` untouched). No fetch path exists anywhere in this feature (D1).
2. **Retry indicator:** new `ProviderRetryIndicator` (design-system skill rules; reuses the `RateLimitIndicator` pattern — per-second interval, `role="status"` — but NOT its success-colour-at-0 or its dismiss button). **State table (MAJ-017):**

   | State | When | Rendering |
   |---|---|---|
   | waiting | frame received, `retry_at` in the future | "{provider} is busy. Retrying automatically in mm:ss (attempt {n} of 3)." — mm:ss computed from `retry_at − now`, clamped at 0 (C-11/C-15); ticks visually |
   | retrying-now | countdown reached 0, next frame not yet arrived | "Retrying now…" — **no success colour at 0** (the own-limiter's "cleared" meaning is wrong here) |
   | stopped-by-user | Stop pressed during the wait | indicator removed; the turn ends (C-12) |
   | terminal | terminal error frame arrives | indicator replaced by the terminal error line |

   **No dismiss control** — the indicator clears itself on the next frame; Stop is the only early exit (matches D4 and the ADR-082 rule that only explicit Stop ends a turn early). **Live-region contract (MAJ-016):** the `aria-live` region announces once per attempt start (the full PM-1 line) and once on the terminal line; the per-second ticking mm:ss text sits OUTSIDE the live region (visually shown, not re-announced — component test asserts the live-region text does not change on a 1 s tick). A queued user message waits behind the waiting turn (turn model) — the indicator copy does not promise otherwise.
3. **Error bubble:** the server-assembled message as the one line (C-13/C-14); **no billing button and no billing link exist** (D6); the existing `detail` disclosure stays (NOT deleted — the round-0 deletion is withdrawn, D1).
4. **Verbose facts + raw view (D1, MIN-010, MAJ-018):** under Verbose chat, facts lines (provider, model, request id — `request_id` only here, C-20) plus the **existing** `MessageItem.tsx` native `<details>` "Technical details" disclosure — that is the ONE reuse target (PM-6's "reuse the tool-call accordion pattern"; the catalogued `src/components/ui/accordion.tsx` exists but a second accordion sibling next to the working disclosure would violate design-system skill rule 14's reuse-first rule). The disclosure's content rendering is tightened: `detail` renders as **inert text** — never HTML-rendered, never markdown-rendered — pretty-printed only when it parses as JSON (C-21; `WrapHTMLResponseError` means detail is not always JSON). Verbose off → no disclosure, no facts, nothing fetched (there is nothing to fetch).
5. **Fallback note:** grey event-line treatment (delegation event-lines precedent) for the persisted fallback note; replay parity via the §7.1 carrier; "Fallback model" naming (D7).
6. **`getLLMErrorDisplay`:** gains the templated-code allow-list (renders `le.message` for the new codes only); keeps the `detail` branch and the replay behaviour unchanged (C-14).

## 9. User stories & acceptance scenarios

### US-1 — Rate-limit countdown (P0; PM-1, PM-4, D3, D4)

**Narrative.** A user whose provider rate-limits a turn no longer stares at a vague "wait a moment": the turn keeps working, visibly, with the provider's own wait time — and Stop always works.

**Why this priority:** the most common provider failure; the founder's first example.

**Independent test:** inject a 429 with `retry-after: 120` at the provider adapter; observe the retry frame + countdown; exhaust attempts; observe the terminal line. (E2E uses a short `retry-after` for speed — MIN-005; the 120 s formatting is proven at component level.)

1. **Given** a turn whose provider answers 429 with `retry-after: 120`, **When** the agent loop schedules the retry, **Then** a `provider_retry` frame arrives with `provider`, `model`, `retry_after_seconds=120`, `retry_at` (now+120 s), `attempt=2`, `max_attempts=3`, and the chat shows "{provider} is busy. Retrying automatically in 2:00 (attempt 2 of 3)."
2. **Given** a retry in progress, **When** each second passes, **Then** the countdown decrements (computed from `retry_at − now`) and at 0 the next attempt is issued; between 0 and the next frame the indicator reads "Retrying now…".
3. **Given** a 429 without a usable `retry-after`, **When** the loop schedules the retry, **Then** backoff is exponential (2 s base, 30 s cap, jitter) and the line shows the backoff it actually applies — no fake time.
4. **Given** a `retry-after` above 120 s (e.g. 3600), **When** the response is classified, **Then** no countdown shows, the candidate's remaining retries are skipped, the fallback chain runs next — and with no fallback configured the turn ends with the terminal line.
5. **Given** retries exhausted, **When** the final attempt fails, **Then** the terminal error line is "{provider} is busy right now. You can retry the turn." and the indicator clears.
6. **Given** bytes already streamed to the turn, **When** a 429 arrives mid-stream, **Then** no retry is scheduled (C-10) and the terminal line shows instead.
7. **Given** a retry wait in progress, **When** the user presses Stop, **Then** the turn ends within 1 s, the indicator disappears, and no further attempt is made (C-12).
8. **Given** a second tab attaching (or a WS reconnecting) 100 s into a 120 s wait, **When** the retry frame replays, **Then** that tab shows the remaining ~20 s (from `retry_at`), not 2:00 (C-11).

### US-2 — Billing gets its own message (P0; PM-1, PM-3, D2, D6)

**Narrative.** An operator with an empty account is told the truth — not told to "wait a moment", not shown a button that does not exist, and not locked out of the model.

**Independent test:** inject a 402 / 429-with-structured-quota-marker; assert the new code, the sentence, `retryable: false`, and that the standard (non-billing) cooldown path was taken.

1. **Given** a provider response matching the C-5 vocabulary (status 402; or a structured `insufficient_quota` code field; or one of the unambiguous account-state phrases), **When** classified, **Then** the code is `quota_billing`, the line is "{provider} says your account is out of credit.", and `retryable` is false.
2. **Given** any `quota_billing` verdict, **When** the bubble renders, **Then** no button and no link appear (D6).
3. **Given** the dataset D2 negative rows (Gemini per-minute 429 text; Codex "usage limit … try again in 3 days"; an OpenAI 429 linking a billing page; plain "too many requests"), **When** classified, **Then** the code stays `rate_limited` (C-6).
4. **Given** a `quota_billing` verdict, **When** the fallback router records the failure, **Then** the model's cooldown is the standard curve — never the deleted billing curve (C-7).

### US-3 — Auth line names the provider (P0; PM-1, PM-2)

1. **Given** a 401/403 whose failing attempt's provider identity is known, **When** classified, **Then** the line is "{provider} rejected the API key. Check the key in Settings → Providers." — naming the provider of the failing attempt, not unconditionally the primary (C-16).
2. **Given** provider identity unknown (facts absent), **When** classified, **Then** today's catalogue sentence shows unchanged.
3. **Given** any auth error, **When** the message or facts are assembled, **Then** no key label, key material, or fragment appears in the message or facts (C-19 — the raw view is governed by D5, not by this claim).

### US-4 — #711 closes by display gating (P0; PM-6, PM-7, D1)

1. **Given** a viewer with Verbose chat off, **When** any provider error, retry frame, or fallback frame renders, **Then** no provider body text (or raw provider error string) appears anywhere in the rendered UI — the error bubble shows the assembled line, the retry indicator shows facts only (C-1).
2. **Given** the feature's new frames, **When** the hub builds them, **Then** `provider_retry`/`provider_fallback` payloads contain named facts only — a sentinel provider-body substring appears in no frame payload of any type (C-2), and a mutation that forwards `LLMRetryPayload.Error` turns the scan red.
3. **Given** the blunt drop-detail commit on the security branch (PM-7), **When** landing order is decided, **Then** the FQ-8 working assumption holds: `c13c4d279` stays on the security branch until this feature lands, then this design replaces it (§16 — MAJ-013).

### US-5 — Verbose facts + raw detail (P1; PM-6, D1, D5)

1. **Given** Verbose chat on and a live error, **When** the viewer opens the "Technical details" disclosure, **Then** the facts lines (provider, model, request id) and the raw provider detail from the same frame render — no fetch occurs (D1).
2. **Given** a registered credential value in the raw detail, **When** the detail preview is built, **Then** the stored value is scrubbed (existing `buildDetail` behaviour, unchanged).
3. **Given** a replayed (historical) error, **When** the bubble renders, **Then** no disclosure mounts and nothing is fetched — replay carries no `detail` (existing behaviour, unchanged).
4. **Given** Verbose chat off, **When** an error renders, **Then** the disclosure is absent from the DOM, no facts lines show, and the one line is the whole display.
5. **Given** a non-JSON detail (an HTML page from a wrong `api_base`, `WrapHTMLResponseError`), **When** the disclosure renders it, **Then** it renders as inert text — no markup interpretation, no markdown rendering; pretty-printed only when it parses as JSON (C-21).

### US-6 — Fallback note (P1; PM-4, D7)

1. **Given** the primary candidate fails (or is skipped for cooldown) and a fallback answers, **When** the reply completes, **Then** a quiet note "Answered by the Fallback model ({X}) because {Y} was unavailable." shows under the reply.
2. **Given** the session is reloaded, **When** history replays, **Then** the same note renders from the persisted transcript via the replay carrier (C-17).
3. **Given** the primary answered normally (no fallback), **When** the turn ends, **Then** no note appears.
4. **Given** a long cooldown repeatedly skipping the same unavailable model to the same fallback, **When** several turns run, **Then** the note appears at most once per (unavailable, answered) pair until the pair changes (C-18).

### US-7 — Context length unchanged (P1 regression guard; PM-5)

1. **Given** a context-overflow error, **When** classified and rendered, **Then** code, copy, and attribution are byte-identical to today (C-23); no countdown, no compaction, no token counts.

## 10. BDD scenarios (spec-derived oracles)

Oracles come from this spec: exact template sentences (§6), constraint IDs (§4), frame field lists (§7.1), and the copy-rule tests that already exist. Scenario IDs use set prefixes (A, B, AU, DG, FB, RG) that do not collide with constraint IDs (C-N) or datasets (D1–D3).

### Set A — rate-limited retry (US-1)

```gherkin
Scenario: A-1 — Retry countdown honors retry-after within the ceiling
  Given a provider adapter that answers 429 with header retry-after: 120
  And a turn with model M on provider P (catalog display name "OpenRouter")
  When the agent loop classifies the response
  Then a provider_retry frame is published with provider "OpenRouter", model M,
    retry_after_seconds 120, retry_at now+120s, attempt 2, max_attempts 3,
    error_code "rate_limited"
  And the chat shows: OpenRouter is busy. Retrying automatically in 2:00 (attempt 2 of 3).
  Traces to: US-1 / 1   [C-8, C-9, C-11]

Scenario: A-2 — Countdown ticks and the next attempt fires at 0
  Given a retry in progress with retry_at 92s in the future
  When each second passes
  Then the visible countdown decrements (computed from retry_at − now, mm:ss per C-15)
  And at 0 the next provider call is issued
  And between 0 and the next frame the indicator reads "Retrying now…" with no success colour
  Traces to: US-1 / 2   [C-11, C-15]

Scenario: A-3 — No usable retry-after takes the backoff schedule
  Given a 429 without a retry-after header (and without retry-after-ms)
  When the loop schedules the retry
  Then the wait is exponential backoff (2 s base, ×2, 30 s cap, 25% jitter)
  And the line shows the wait actually applied, no invented time
  Traces to: US-1 / 3   [C-9]

Scenario: A-4 — A wait above the ceiling skips to fallback
  Given a 429 with retry-after: 3600
  When the response is classified rate_limited
  Then no provider_retry frame is published for that candidate
  And the candidate's remaining retries are skipped and the fallback chain runs
  And with no fallback configured the error frame carries code rate_limited, retryable true,
    message "OpenRouter is busy right now. You can retry the turn."
  Traces to: US-1 / 4, US-1 / 5   [C-9]

Scenario: A-5 — Terminal line after exhaustion
  Given 3 failed rate-limited provider calls on the candidate
  When the loop gives up
  Then the error frame carries the terminal rate_limited template line
  And the retry indicator is gone
  Traces to: US-1 / 5   [C-8]

Scenario: A-6 — No retry after streamed bytes
  Given a turn whose stream already delivered >0 bytes
  When a 429 arrives
  Then no provider_retry frame is published
  And the terminal rate_limited line shows
  Traces to: US-1 / 6   [C-10]

Scenario: A-7 — Stop during a retry wait ends the turn
  Given a retry wait with retry_at 100s in the future
  When the user presses Stop
  Then the turn ends within 1 second
  And no further provider call is made
  And the indicator is removed
  Traces to: US-1 / 7   [C-12, D4]

Scenario: A-8 — Mid-wait attach shows the remaining time
  Given a retry wait with retry_at now+120s published 100 seconds ago
  When a second tab attaches (or the WS reconnects) and the frame replays
  Then that tab's countdown shows ~20 seconds remaining (retry_at − now), not 2:00
  Traces to: US-1 / 8   [C-11]
```

### Set B — billing (US-2)

```gherkin
Scenario Outline: B-1 — Unambiguous account-state markers land quota_billing
  Given a provider response with status <status> and <marker>
  When classified
  Then the code is quota_billing, retryable false
  And the message is "<provider> says your account is out of credit."
  Traces to: US-2 / 1   [C-5]

  Examples:
    | status | marker                                              |
    | 402    | any body                                            |
    | 429    | JSON error code field "insufficient_quota"          |
    | 429    | body phrase "insufficient credits"                  |
    | 400    | body phrase "credit balance too low"                |
    | 500    | body phrase "payment required"                      |

Scenario: B-2 — Rate-limit look-alikes stay rate_limited
  Given a 429 whose body is one of: the Gemini per-minute wording ("You exceeded your
    current quota, please check your plan and billing details…"), a Codex usage-window
    message ("You've hit your usage limit… try again in 3 days"), an OpenAI-style 429
    whose message links a billing page, or plain "too many requests"
  When classified
  Then the code is rate_limited in every case
  And the candidate is NOT disabled for any extended period
  Traces to: US-2 / 3   [C-5, C-6, C-7]

Scenario: B-3 — Billing sentence stands alone
  Given a quota_billing error with provider facts
  When the bubble renders
  Then the sentence renders with no button and no link anywhere
  Traces to: US-2 / 2   [D6]

Scenario: B-4 — No billing lockout
  Given a quota_billing verdict recorded by the fallback router
  When the cooldown is computed
  Then it equals the standard failure curve (calculateStandardCooldown)
  And calculateBillingCooldown does not exist in the binary
  Traces to: US-2 / 4   [C-7, D2]
```

### Set AU — auth (US-3)

```gherkin
Scenario: AU-1 — Auth line names the failing attempt's provider
  Given the primary candidate (OpenRouter) skipped for cooldown
  And the fallback candidate (Anthropic) answered 401
  When classified
  Then the message is "Anthropic rejected the API key. Check the key in Settings → Providers."
  Traces to: US-3 / 1   [C-16]

Scenario: AU-2 — Identity absent falls back to catalogue copy
  Given a 401 with no provider identity threaded
  When classified
  Then the message is today's catalogue sentence for provider_auth_failed, unchanged
  Traces to: US-3 / 2

Scenario: AU-3 — Keys are never named in message or facts
  Given any auth or billing error whose provider body echoes a key fragment
  When the message and facts are assembled
  Then the message names no key label and contains no key material or fragment
  And no facts field contains either
  Traces to: US-3 / 3   [C-19]
```

### Set DG — display gating: #711 and Verbose (US-4, US-5)

```gherkin
Scenario: DG-1 — Non-Verbose rendering never shows raw provider text
  Given a viewer with Verbose chat off
  When a provider error, a provider_retry frame, or a provider_fallback frame renders
  Then no provider body text appears anywhere in the rendered UI
  And the error bubble shows the assembled one-line message only
  And the "Technical details" disclosure is absent from the DOM
  Traces to: US-4 / 1, US-5 / 4   [C-1]

Scenario: DG-2 — New frames carry facts only
  Given a provider body containing the unique sentinel string "SENTINEL-BODY-7Q4Z"
  When the hub publishes the error frame and any provider_retry / provider_fallback frames
  Then no frame payload of ANY type contains the sentinel substring
  And provider_retry / provider_fallback payloads contain only their named fields (§7.1)
  Traces to: US-4 / 2   [C-1, C-2]

Scenario: DG-3 — Mutation check: forwarding the raw error turns the oracle red
  Given the hub handler mutated to copy LLMRetryPayload.Error into a retry-frame field
  When the DG-2 scan runs
  Then it fails (red) — the oracle can see this failure class
  Traces to: US-4 / 2   [C-2]

Scenario: DG-4 — Verbose shows facts + raw detail from the same frame
  Given Verbose chat on and a live error frame carrying facts and detail
  When the viewer opens the "Technical details" disclosure
  Then the facts lines (provider, model, request id) and the raw detail render
  And no network request is made (there is nothing to fetch)
  Traces to: US-5 / 1   [C-20, D1]

Scenario: DG-5 — Non-JSON detail renders inert
  Given a detail value of "<img src=x onerror=alert(1)>" (WrapHTMLResponseError case)
  When the disclosure renders it
  Then it renders as inert text — no element is created, no script runs
  And a JSON detail is pretty-printed
  Traces to: US-5 / 5   [C-21]

Scenario: DG-6 — Replay carries no detail and mounts no disclosure
  Given a historical error replayed from the transcript
  When the bubble renders
  Then no disclosure mounts and no fetch is attempted
  (Replay payloads have no detail field — existing behaviour, unchanged)
  Traces to: US-5 / 3   [C-13]

Scenario: DG-7 — The assembled sentence survives persistence and replay
  Given a live quota_billing or provider_auth_failed error with provider facts
  When the transcript entry is written and the session reloads
  Then the replayed bubble shows the provider-named assembled sentence
  And NOT defaultUserMessage(code)
  Traces to: US-2 / 1, US-3 / 1   [C-13]

Scenario: DG-8 — Channel reply carries the assembled sentence
  Given a provider_auth_failed error on an agent bound to Telegram/Discord
  When the channel reply is produced
  Then the reply text is the provider-named assembled sentence
  Traces to: US-3 / 1   [C-13]
```

### Set FB — fallback note (US-6)

```gherkin
Scenario: FB-1 — The note names the Fallback model and the unavailable model
  Given primary model Y unavailable and fallback model X answered
  When the turn completes
  Then a provider_fallback frame and a persisted note show
    "Answered by the Fallback model (X) because Y was unavailable."
  Traces to: US-6 / 1   [C-17, D7]

Scenario: FB-2 — The note persists and replays
  Given a fallback note persisted this session
  When history replays
  Then the note renders identically from the transcript via the replay carrier
  Traces to: US-6 / 2   [C-17]

Scenario: FB-3 — No note without fallback
  Given the primary candidate answered
  When the turn completes
  Then no fallback note appears
  Traces to: US-6 / 3

Scenario: FB-4 — A cooldown-skip fallback notes once per pair
  Given candidate Y in cooldown and fallback X answering across several turns
  When more turns run with the same (unavailable Y, answered X) pair
  Then the note appears at most once for that pair until the pair changes
  Traces to: US-6 / 4   [C-18]
```

### Set RG — regressions (US-7)

```gherkin
Scenario: RG-1 — Context-length behaviour untouched
  Given a context-overflow response
  When classified
  Then code, message, and attribution are byte-identical to the current catalogue
  Traces to: US-7 / 1   [C-23]

Scenario: RG-2 — Own-limiter denials never render a provider template
  Given an in-process SEC-26 rate-limit denial (own limiter, kind rate_limit)
  When the entry persists and renders
  Then the text is today's own-limiter copy, never "{provider} is busy…"
  Traces to: §13 / 5   [C-14, MIN-007]

Scenario: RG-3 — Other codes keep catalogue copy (allow-list negative)
  Given an error of any code outside the templated set (e.g. tool_args, turn_timed_out)
  When getLLMErrorDisplay renders it
  Then the bubble shows the generated catalogue copy for that code, not le.message
  Traces to: §13 / 1   [C-14]
```

## 11. TDD plan

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | header parse: retry-after seconds / future HTTP-date / past HTTP-date / malformed / ms / duplicate / absent / 0 / 3600 | Unit | A-1, A-3, A-4; C-9, C-22 | `common.ProviderError`; dataset D1 |
| 2 | request-id header candidates; `cf-ray` labelled last-resort | Unit | C-20 | OBS-003 |
| 3 | billing detector vs rate-limit (dataset D2, both classifiers) | Unit | B-1, B-2; C-5, C-6 | `classifyByMessage` order + 429 path; structured `insufficient_quota` field match |
| 4 | quota_billing: retryable false, copy rules hold | Unit | B-1, B-3 | extends existing copy-rule tests |
| 5 | cooldown pin per D2 row: standard curve only; `calculateBillingCooldown` absent | Unit | B-4; C-7 | compile-level absence + curve assertion (D2) |
| 6 | `provider_message` bijection + closed-slot token check + catalogue fallback when facts absent | Unit | §6; C-3, C-4 | generator + Go/TS catalogue tests (OBS-002 shape) |
| 7 | terminal vs live retry lines | Unit | A-5 | |
| 8 | root-path retry loop: 3 total calls, attempt = call about to be made (2..3), backoff, >120 s skip-to-fallback, zero-stream guard, chain rows (1-candidate / 2-both-429 / 2-one-billing) | Unit | A-1, A-3, A-4, A-5, A-6; C-8, C-9, C-10 | mirrors `loop_provider_retry.go` tests; MAJ-002 rows |
| 9 | wait-sleep context cancellation: Stop ends the wait ≤ 1 s, no further attempt | Unit | A-7; C-12 | `sleepWithContext` pattern, D4 |
| 10 | failing-attempt provider/model sourcing: primary-skipped → fallback-401 names the fallback; skip-only chain → catalogue fallback | Unit | AU-1, AU-2; C-16 | dataset D3 rows |
| 11 | assembly: sentence in `ErrorPayload.Message`; transcript keeps it (trusted-set extension); replay shows it | Integration | DG-7; C-13 | MAJ-001 ii |
| 12 | `getLLMErrorDisplay` allow-list: templated codes render `le.message`; every other code keeps catalogue copy | Unit (vitest) | RG-3; C-14 | MAJ-001 iii |
| 13 | channel reply text via the facts-aware translator | Integration | DG-8; C-13 | MAJ-001 iv |
| 14 | hubError: facts on frame, `Detail` population unchanged; retry/fallback handlers build named-fields-only frames | Integration | DG-2; C-2 | |
| 15 | sentinel scan: no provider-body substring in any frame payload of any type NOR in any non-Verbose-rendered DOM; **mutation check**: forwarding `LLMRetryPayload.Error` turns it red | Integration + E2E | DG-1, DG-2, DG-3; C-1, C-2 | the #711 oracle (MAJ-015 scope) |
| 16 | fallback note: frame + persisted entry + replay carrier; `cooldown_skip` reason; once-per-pair rate | Integration | FB-1…FB-4; C-17, C-18 | MAJ-010 |
| 17 | `ProviderRetryIndicator`: four states, mm:ss from `retry_at` clamped 0, "Retrying now…", no success colour, no dismiss; live region announces once per attempt start + once terminal; 1 s tick does NOT change live-region text; mid-wait attach renders remaining time | Component (vitest) | A-2, A-8; C-11, C-15 | MAJ-016, MAJ-017 |
| 18 | Verbose disclosure: facts + raw detail, no fetch; inert rendering (`<img src=x onerror=…>` renders as text); JSON pretty-print; Verbose-off → disclosure absent | Component | DG-4, DG-5, DG-6, DG-1; C-21 | MAJ-018 |
| 19 | design-system publication for `ProviderRetryIndicator`: manifest entry, Story, lock-script run (blocking `design-system` CI gate) | Build gate | MIN-010 | new component — publication contract required |
| 20 | E2E: injected 429 (`retry-after: 3` for suite speed; 120 s formatting proven at component level) → countdown → terminal line; sentinel scan over the whole WS log | E2E | A-1, A-5, DG-1, DG-2 | Playwright, mock provider; MIN-005 applied |

**Existing tests that must keep passing (extended, not replaced):** the copy-rule tests — extended to 24 codes with existing assertions unchanged (MIN-004); replay-strips-detail tests (unchanged — detail stays); media strip-retry gate tests (`CodeUnknown` residual); delegated retry tests; own-limiter indicator tests (MIN-007); `verify-contracts`; `lint-guards`.

## 12. Test datasets

**D1 — retry-after parsing (traces to A-1, A-3, A-4; C-9, C-22)**

| case | header | expected |
|---|---|---|
| seconds (at ceiling) | `retry-after: 120` | 120 — auto-retry with countdown |
| seconds (small) | `retry-after: 3` | 3 |
| zero | `retry-after: 0` | fact absent; backoff schedule (MIN-003) |
| http-date future | `retry-after: Wed, 21 Oct 2026 07:28:00 GMT` | delta seconds at parse time |
| http-date past | a date already past | fact absent (no negative countdown) |
| malformed | `retry-after: soon` | fact absent; backoff schedule |
| ms variant | `retry-after-ms: 1500` | 2 (rounded up, ≥ 1) |
| duplicate | two `retry-after` headers | first value |
| over-ceiling | `retry-after: 3600` | 3600 captured as a fact; **no auto-retry** (A-4) |
| absent | — | no fact; backoff schedule |

(No `x-ratelimit-reset*` rows — that family is out of parsing scope, C-22.)

**D2 — billing vs rate-limit (traces to B-1, B-2, B-4; C-5, C-6, C-7)**

| status | body / marker | expected code |
|---|---|---|
| 402 | any body (e.g. "insufficient credits") | quota_billing |
| 429 | JSON `error.code = "insufficient_quota"` (structured field) | quota_billing |
| 429 | prose "insufficient credits" (no structured marker) | quota_billing |
| 429 | Gemini per-minute verbatim: "Resource has been exhausted… You exceeded your current quota, please check your plan and billing details…" (no structured billing marker) | rate_limited |
| 429 | Codex usage window: "You've hit your usage limit… try again in 3 days" | rate_limited |
| 429 | OpenAI-style 429 linking a billing page (no `insufficient_quota` code field) | rate_limited |
| 429 | too many requests | rate_limited |
| 429 | (empty) | rate_limited |
| 400 | "Your account is out of credit" (no 402, phrase not in C-5) | unknown — residual path; strip-retry gate intact; catalogue sentence applies |
| 400 | Unsupported MIME type: image/svg+xml | unknown (residual — strip-retry gate intact) |
| 401 | "Incorrect API key provided: sk-proj-****abcd" | provider_auth_failed |

Note the deliberate consequence of the narrowed vocabulary: billing-shaped **prose on a non-402 status** ("out of credit") no longer classifies `quota_billing` — the 402 status and the structured `insufficient_quota` field are the account-state signals; unrecognised prose gets the catalogue sentence rather than a wrong confident claim (D2's simplify ruling).

**D3 — message assembly (traces to §6; C-16)**

| facts | expected line |
|---|---|
| provider=OpenRouter, retry 120, attempt 2/3 | OpenRouter is busy. Retrying automatically in 2:00 (attempt 2 of 3). |
| provider=OpenRouter (terminal) | OpenRouter is busy right now. You can retry the turn. |
| provider=OpenRouter, billing | OpenRouter says your account is out of credit. |
| provider=OpenRouter, auth | OpenRouter rejected the API key. Check the key in Settings → Providers. |
| primary OpenRouter skipped (cooldown), Anthropic 401 | Anthropic rejected the API key. Check the key in Settings → Providers. |
| fallback answered=z-ai/glm-5.3, unavailable=gpt-4o | Answered by the Fallback model (z-ai/glm-5.3) because gpt-4o was unavailable. |
| provider id not in catalog ("z-ai") | id rendered verbatim — never blank, never truncated |
| no facts | today's catalogue sentence (byte-identical fallback) |

## 13. Regression impact

The feature **modifies existing behaviour**: error lines for three code families gain provider naming; two new frame types appear; the billing cooldown curve is deleted. Guarded by:

1. All existing codes' copy unchanged except the three template families — copy-rule tests **extended to 24 codes, existing assertions unchanged** (MIN-004).
2. `context_too_long` byte-identical (PM-5, C-23).
3. Residual-4xx → `CodeUnknown` unchanged (media strip-retry).
4. Replay shape unchanged (no facts object on replay; the existing detail-strip unchanged — D1).
5. Own-limiter `rate_limit` frame and indicator untouched; an own-limiter denial never renders a provider template (RG-2, MIN-007).
6. Delegated retry policy unchanged (count/caps); only its payload gains fields.
7. Scrubber behaviour unchanged for logs and for the `detail` preview.
8. **Cooldown regression (CRIT-001's consequence, fixed at the root):** `calculateBillingCooldown` is deleted; the D2 dataset pins the standard curve for every billing-shaped row (B-4).
9. **Verbose-promise regression: none.** Because `detail` stays (D1), the six catalogue sentences that promise Verbose details (`provider_stalled`, `tool_args`, `schema`, `turn_timed_out`, `context_unrecoverable`, `unknown`) keep their Verbose content — the round-0 finding MAJ-005 is moot.

## 14. Requirements & success criteria

### Functional requirements

- **FR-001:** The system MUST capture `retry-after` (integer seconds or HTTP-date), `retry-after-ms`, and request-ID headers into the structured provider error at the HTTP boundary; malformed/past values yield no fact; no `x-ratelimit-reset*` parsing exists (C-22).
- **FR-002:** The system MUST classify billing/quota exhaustion as its own code (`quota_billing`), distinct from `rate_limited`, not retryable, in both classifiers, using only the C-5 unambiguous account-state vocabulary.
- **FR-003:** The system MUST assemble one plain-English line per error **at the agent emit sites** from the catalogue + facts (provider/model of the failing attempt, C-16), with catalogue fallback when facts are absent.
- **FR-004:** The system MUST publish error frames carrying code, message, retryable, facts (provider, model, retry_after_seconds, retry_at, attempts, max_attempts, request_id) **and `detail` per ADR-051** — and the NEW retry/fallback frames carrying named facts only, never raw error text (C-2).
- **FR-005:** The system MUST automatically retry rate-limited provider calls on the root path (3 total calls including the first; `retry-after` honored only ≤ 120 s; otherwise backoff; a wait above the ceiling skips the candidate's remaining retries and moves to fallback, or ends the turn when none is configured; zero-streamed-bytes guard) and publish a retry frame per attempt (C-8/C-9/C-10).
- **FR-006:** The system MUST carry `retry_at` (RFC 3339) on every retry frame, and the indicator MUST render `retry_at − now`, clamped at 0 (C-11).
- **FR-007:** The assembled sentence MUST reach all four surfaces — live bubble, persisted transcript, replay, channel reply — for the templated codes (C-13), and the SPA MUST render `le.message` for those codes only (C-14).
- **FR-008:** The system MUST emit and persist a fallback note naming the Fallback model and the unavailable model (D7 naming), with a replay carrier in the contract wave, covering cooldown-skip-produced fallbacks at the once-per-pair rate (C-17/C-18).
- **FR-009:** The system MUST NOT render provider body text outside a Verbose-gated disclosure, and MUST NOT name key labels, key material, or fragments in any assembled message or facts field (C-1/C-19). The Verbose raw view shows the provider body as-is per D5 — no masking, fragment risk accepted by the founder.
- **FR-010:** The Verbose disclosure MUST render `detail` as inert text (never HTML, never markdown), pretty-printed only when it parses as JSON (C-21).
- **FR-011:** The system MUST NOT render any billing button or billing link anywhere (D6).
- **FR-012:** The system MUST NOT apply any billing-specific cooldown or lockout; the billing cooldown function is deleted (D2, C-7).
- **FR-013:** The system MUST keep context-length classification, copy, and behaviour unchanged (C-23).
- **FR-014:** The system MUST keep the scrubber in place for logs and for the `detail` preview, unchanged.
- **FR-015:** Contract changes MUST land first (Constraint #8): schemas → regenerate → commit atomically → consumers.

### Success criteria

- **SC-1:** On an injected 429, the chat shows the PM-1 countdown line within one frame round-trip, and the countdown is derived from `retry_at` (a mid-wait attach shows the remaining time). E2E uses `retry-after: 3` for suite speed; the 120 s formatting is proven at component level (MIN-005). (Verified = Playwright e2e + vitest.)
- **SC-2:** A scan over ALL frame payloads in the e2e WS log AND over the non-Verbose-rendered DOM finds zero provider-body sentinel substrings; the mutation check (forward `LLMRetryPayload.Error`) is demonstrated to turn the scan red. (The #711 oracle, MAJ-015 scope; zero matches.)
- **SC-3:** All dataset D2 rows produce the expected code AND the pinned standard cooldown — 100%.
- **SC-4:** A Verbose viewer sees facts + the raw detail (inert-rendered) on a live error with no fetch; a non-Verbose viewer sees neither; a replayed error mounts no disclosure and fetches nothing. All verified in Playwright/vitest.
- **SC-5:** All dataset D1 parsing rows produce the expected fact or absence.
- **SC-6:** `verify-contracts` and the copy-rule tests are green with `quota_billing` + the templated variants present (bijection holds, 24 codes).
- **SC-7:** Stop during a retry wait ends the turn within 1 s with no further attempt — unit-proven on the sleep's context cancellation and observed once end-to-end.
- **SC-8:** The fallback note appears live and on replay, and the once-per-(unavailable, answered)-pair rate holds under repeated turns.

## 15. Traceability matrix

| FR | US | BDD scenario | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | A-1, A-3, A-4 | 1 |
| FR-002 | US-2 | B-1, B-2 | 3, 4 |
| FR-003 | US-1, US-2, US-3 | A-1, B-1, AU-1, AU-2, DG-7, DG-8 | 6, 11; dataset D3 |
| FR-004 | US-4, US-5 | DG-2, DG-4 | 14 |
| FR-005 | US-1 | A-1…A-7 | 8, 9 |
| FR-006 | US-1 | A-2, A-8 | 17 |
| FR-007 | US-1, US-2, US-3 | DG-7, DG-8, RG-2, RG-3 | 11, 12, 13 |
| FR-008 | US-6 | FB-1…FB-4 | 16 |
| FR-009 | US-3, US-4, US-5 | AU-3, DG-1, DG-2, DG-3, DG-6 | 15 |
| FR-010 | US-5 | DG-4, DG-5, DG-6 | 18 |
| FR-011 | US-2 | B-3 | 4 |
| FR-012 | US-2 | B-4 | 5 |
| FR-013 | US-7 | RG-1 | copy-rule + regression suite |
| FR-014 | §13 | AU-3, RG-2 | existing scrubber tests |
| FR-015 | all | (process) | `verify-contracts`, CI |

Every FR appears; every scenario (A-1…A-8, B-1…B-4, AU-1…AU-3, DG-1…DG-8, FB-1…FB-4, RG-1…RG-3) traces to ≥ 1 FR; every TDD row cites only IDs defined in §4, §9, §10, or §12.

## 16. Questions for the founder

**Only these remain open.** Everything else that round 0/round 1 raised is settled by the recorded founder decisions and appears in this spec as fact, not as a question.

- **FQ-7 — "Model no longer offered" (was MAJ-011):** PM-1's fourth example sentence ("OpenRouter no longer offers z-ai/glm-4…") has no template and no classification in this spec. **A:** add it now (a 404 / "model not found" body → a new code or template, with a check that the residual-4xx `CodeUnknown` media gate is not re-pointed) or **B:** defer to a tracked issue. Recommendation: B — it is a distinct classification problem and ships today as the generic sentence.
- **Q4 — `quota_billing` attribution:** the lockout (D2) and the billing button (D6) are both gone, so attribution now gates only copy rules. **A:** `config` — then the copy-rule tests forbid retry advice in the sentence; **B:** `provider` — attribution-pure, but its copy rules would then PERMIT retry advice, which reads wrong for an empty account; **C:** drop the attribution distinction for this code entirely. Recommendation: A (the sentence should not tell an empty-account user to "retry the turn"), but C is defensible now that nothing else hangs on it.
- **FQ-8 — landing order with the security branch (MAJ-013):** working assumption, pending confirmation: keep `c13c4d279` ("error frame detail stays off the wire") on `feature/gateway-security-fixes` until this feature lands; then this design replaces it — the leak is never reopened on `main`. Team-lead is defaulting to this absent a correction.

**Settled — recorded as decisions, no longer asked:** Q1/Q2 (delivery + `detail`) → D1; Q3 (retry numbers) → D3 (**3 attempts, 2-minute ceiling — CONFIRMED, not asked**); Q5 (billing URL) → D6; Q6 (note persistence) → D7; FQ-1 → D2; FQ-2 → D5; FQ-3 → D3 (the 2-minute figure is the ceiling); FQ-4 → D3 (retry-then-fallback); FQ-5 → D1 (ADR-051 stands — no amendment needed, MAJ-014 moot); FQ-6 → D1 (`detail` kept, so the six Verbose-promise sentences keep their content — MAJ-005 moot); FQ-9 → D6; FQ-10 → D4.

## 17. Reachability (Definition of Done)

- **No new tool** — Hard Constraint #6's catalog/policy check is N/A (grep for a new tool name returns nothing to register; nothing to assign).
- **UI surfaces:** every chat tab renders the new error lines, the retry indicator, the Verbose facts + raw-detail disclosure, and the fallback note — no settings toggle gates them except Verbose chat itself (existing, `chat-verbose-switch`). There is no billing button (D6).
- **Trigger paths a real user can hit:** a bad API key (Settings → Providers), an out-of-credit account, a rate-limited provider, a provider outage with fallback candidates configured. E2E injects 429/401/402 at a mock provider; UAT can use a deliberately invalid key on a real provider.
- **Two-line delivery statement at landing:** code correct and tested (gates green); reachable by a user (any chat tab shows the new presentation on a real provider failure) — the reachability claim must be demonstrated, not inferred from green tests.

## 18. Holdout evaluation scenarios (post-implementation; NOT in the traceability matrix)

Happy path:

1. Set a provider key that is valid but rate-limited (or ask UAT to hammer a free-tier key); send a chat message; confirm the sentence names the provider, counts down, and the turn eventually completes or ends with the terminal line — all in one turn, no reload.
2. Fill the provider account to zero credit; send a message; confirm the sentence says out-of-credit and names the provider — with no button and no link (D6) and no multi-hour lockout (D2).
3. Break the API key; send a message; confirm the sentence names the provider and the Settings → Providers path is the one that fixes it.

Error path:

4. Point an agent at a provider with a wrong `api_base` that returns an HTML page; confirm the line stays human (no HTML, no status soup) and the Verbose disclosure shows the raw HTML as inert text (nothing executes, nothing renders as markup).
5. Kill the network mid-turn; confirm the classification stays `network` with today's copy (no provider name invented).

Edge:

6. Two tabs on one session, one Verbose one not, during a 429: confirm both show the same line and neither tab's UI shows provider body text; the Verbose tab sees the facts + raw detail in its disclosure with no fetch; a tab attached mid-countdown shows the remaining time.
7. A provider whose display name contains a slash and dots ("z-ai/glm"); confirm the sentence renders it verbatim, nowhere truncated or escaped.
8. Press Stop during a countdown; confirm the turn ends immediately and no further attempt fires.

---

## Verification basis (evidence for the facts this spec is built on)

| Claim | Evidence | Certainty |
|---|---|---|
| Branch state | `git log --oneline -3` → `de5a0cef3` head (founder-decisions commit); `git status --short --branch` → `feat/provider-messages...origin/feat/provider-messages`, clean | Verified |
| `detail` ships on the wire and is display-gated, not stripped (D1 premise) | Read `src/components/chat/MessageItem.tsx` (disclosure mounted only when `verboseChatEnabled && message.errorDetail`, native `<details>`, 512-char cap); `src/lib/llm-error.ts::getLLMErrorDisplay` (`detail` returned only when Verbose on); `LLMErrorReplay` has no `detail` field (replay strip) | Verified |
| `hubError` passes the agent's message through with no facts assembly; sets `Detail` unconditionally | Read `pkg/gateway/websocket_forward_hub.go::hubError` (`if p.Code != "" { message = p.Message }`; `Detail: &detail`) | Verified |
| Transcript writer discards assembled sentences for coded non-trusted errors | Read `pkg/agent/turn_transcript.go::writeErrorTranscriptWithAbandonment` (`llm.Message = defaultUserMessage(code)` when not `allowAbandoned || trustedMessage`); `isTrustedInternalStage`/`trustedInternalStageSet`; `appendClassifiedError` passes `llm.Message` through | Verified |
| SPA ignores server message text for all codes but one | Read `src/lib/llm-error.ts::getLLMErrorDisplay` (`le.message` only for `delegated_task_limit`, else `codeToMessage(le.code)`) | Verified |
| Channel replies classify without loop identity | Read `pkg/agent/session_worker.go` and `pkg/agent/loop.go` `TranslateTurnError(err).Message` sites | Verified |
| Retry wait is context-cancellable today | Read `pkg/agent/loop_provider_retry.go` (`sleep := sleepWithContext`, line ~81) | Verified |
| Catalogued accordion exists (rejected as the reuse target — see MIN-010 rationale) | `ls src/components/ui/accordion.tsx` → exists; the existing `MessageItem.tsx` disclosure chosen instead (skill rule 14, one surface already working) | Verified |
| Cooldown curves as stated | Read `pkg/providers/cooldown.go::calculateStandardCooldown` (min 60 s, cap 1 h), `calculateBillingCooldown` (5 h base → 24 h cap) | Verified |
| `WrapHTMLResponseError` exists (detail not always JSON) | Read `pkg/providers/common/common.go` (`WrapHTMLResponseError` defined and called) | Verified |
| 23-code catalogue, attributions, generator hard-fail, `detail` optional 2048 | Read `contracts/components/schemas/LLMError.yaml` (whole file, round-0 task; unchanged at `de5a0cef3`) | Verified |
| `ProviderError` captures no response headers today | Read `pkg/providers/common/common.go::HandleErrorResponse`, `WrapHTMLResponseError` (only `Content-Type` read; 8 KiB cap, 512 B preview) | Verified |
| "quota exceeded" → `CodeRateLimited` today; no billing code user-side | Read `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus` (round-0 task) | Verified |
| Providers-side: 429 → `FailoverRateLimit` before body check; rate-limit patterns before billing | Read `pkg/providers/error_classifier.go` (round-0 task) | Verified |
| Retry events exist but never reach the SPA; payload carries raw error string | Read `pkg/agent/events.go::LLMRetryPayload`; `pkg/gateway/websocket_forward.go` (not-forwarded list); `pkg/agent/loop_provider_retry.go::callProvider` | Verified |
| Verbose chat is client-only | Read `src/store/chatPreferences.ts` (zustand persist; "does not cross the gateway/API boundary") | Verified |
| buildDetail scrubs before preview cut | Read `pkg/agent/translate_error.go::buildDetail` (round-0 task) | Verified |
| GitNexus unavailable in this session | Tool list has no gitnexus tools; exploration done by Read/Grep (shared rule 9) | Verified |
| Impact rows | Grep sweep for callers (choke points, retry gates, catalogues, trusted-stage set) | Inferred (graph not available) |
| Gemini/Codex/OpenAI provider wordings in D2 | Known provider behaviour (review CRIT-001, Inferred high confidence); pinned as dataset rows so CI proves the classifier against them | Inferred (not probed live) |
| **Self-check** | Re-read the finished spec against the dispatch's done-criteria and cross-checked every ID: all 23 constraints (C-1…C-23) defined once in §4 and cited only where defined; 15 FRs; 30 numbered BDD scenarios across six sets; every TDD row and every matrix cell cites existing IDs (scripted ID cross-check run, exit 0); every D1–D8 decision applied (D1→§5 rewrite, D2→§7.3+dataset, D3→§7.4 precise, D4→A-7+row 9, D5→C-19/FR-009 reworded, D6→§7.6/button/C-10-old/FR-007-old/Q5 removed, D7→§7.4 note+carrier+naming, D8→untouched parallel track); every non-moot review finding fixed or explicitly in §16 (MAJ-001,2,3,4,6,7,8,10,13,15,16,17,18 + CRIT-001-half, MIN-001,3,4,5,6-rewritten,7,10, OBS-002,003 + all structural gaps); moot findings declared with reasons (CRIT-002→D5, MAJ-005, MAJ-009→MIN-008, MAJ-012, MAJ-014, MIN-002, MIN-008, MIN-009, OBS-001); §16 holds only FQ-7/Q4/FQ-8; commit scoped to the spec file, author verified human, no co-author trailer; pushed and re-verified | Verified |

*Skills: omnipus-shared-rules, plan-spec.*

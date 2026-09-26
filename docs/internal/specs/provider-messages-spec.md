# Provider Messages — Specification (provider/LLM error presentation, #711)

**Status:** Approved (final fix round applied — founder decisions D10–D15 recorded and applied; all 10 MAJOR / 6 MINOR / 3 OBSERVATION findings from `provider-messages-spec-review-round2.md` fixed or founder-decided; zero CRITICAL findings. One stated open refinement remains in §16 — the **resume-signal proposal** — which awaits the founder's next ruling and does not block this approval.)

- **Source briefs:** `docs/internal/specs/spec-provider-messages.md` (founder decisions PM-1…PM-7, 2026-09-26 — the interview output); `docs/internal/specs/provider-messages-research.md` (OpenCode vs Omnipus, 2026-09-26); GitHub issue #711; governing ADR: [ADR-051 — Media handling and provider error translation](../architecture/ADR-051-media-handling-and-provider-error-translation.md).
- **Discovery status:** Phase 1 is satisfied by the recorded founder decisions PM-1…PM-7 (interview output, confirmed 2026-09-26). PM-6's delegated point — the delivery mechanism for raw provider text — was settled by founder decision **D1** after grill round 1: the wire already carries `detail` (ADR-051 Rev 3 Q2 / RD7) and #711 is a **display** problem, not a wire problem. See §5. The remaining open points are listed in §16 — nothing else waits on the founder before grill round 2.
- **Premise correction (round-1 fix, verified in code):** the round-0 spec assumed #711 required stripping raw detail off the wire and built §5 (REST fetch), §7.5 (retention) and the `detail` deletion on that premise. That premise was wrong. ADR-051 already decided `detail` ships on the wire and is **display-gated** (`src/components/chat/MessageItem.tsx` — the Verbose-gated "Technical details" disclosure, mounted only when `verboseChatEnabled && message.errorDetail`; `src/lib/llm-error.ts::getLLMErrorDisplay` — `detail` returned only when Verbose chat is on; replay strips `detail` before persisting). That mechanism is not broken and is not replaced. This spec now extends it and scopes the remaining #711 risk to this feature's **new** frames (§5, US-4).
- **Codebase intelligence note:** the GitNexus MCP tools are not connected in this session; per `omnipus-shared-rules` rule 9 the exploration below is Read/Grep-based, and every impact row is labelled **Inferred**. Every cited symbol was read in this task on `feat/provider-messages` @ `957a5eee3` (re-verified during the final fix round; the D13 SDK audit ran against the module cache — anthropic-sdk-go v1.48.0, openai-go/v3 v3.39.0).
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

**D9 — PM-1's fourth example ships now, as its own code `model_retired` (resolves
FQ-7; was MAJ-011).** Founder's exact wording for the message: *"This model is no
longer offered by <provider> — choose another model."* **[Wording superseded by
D11 below — the shipping text is the §6 template; this quote is the historical
record.]** The template and its
classification are added in this fix round, not deferred to a tracked issue. It is
deliberately a NEW code that must never share or collide with the existing
`model_unavailable` — that code already means something different: a mid-session
model switch that failed, with the turn continuing on the previous model
(`pkg/agent/translate_error.go::CodeModelUnavailable`; the
`contracts/components/schemas/LLMError.yaml` `model_unavailable` entry, attribution
`config`). PM-1's fourth example is a different scenario: the provider refuses or
retires the requested model during an actual provider call (e.g. a 404 "model not
found" / "decommissioned" / "no longer supported" response). Classification is
deliberately narrow (C-24) so the residual-4xx `CodeUnknown` media strip-retry gate
(§2 load-bearing constraint) is not re-pointed. Attribution **`config`**: the fix is
an operator/config action — pick a new model (D11) — the same pattern as
`model_unassigned` ("Pick one in the agent's settings."); nothing user-side and no
retry fixes a retired model.

**FQ-8 (round 1, resolved by default)** — landing order with `gateway-security`'s
`c13c4d279`: team-lead's recommended default stands (keep it on that branch until this
feature lands, then this design replaces it) — no founder correction received.

## Founder decisions — round 2 (resolves grill round 2's Questions for the founder)

Recorded verbatim-in-meaning from the founder's answers, 2026-09-26. These decisions
supersede §16 round-2 open points and `provider-messages-spec-review-round2.md`'s
"Questions for the founder"; the final fix round below applies them.

**D10 — Visual format: Option C ("Console strip"), but flat — no card border, no box
(resolves D8's demo deliverable).** Founder: *"our normal tool calls also do not have
card-style borders — minimalistic and flat."* Keep Option C's muted mono metadata line
+ one kind-colored dot + collapsible "Technical details", but strip the bordered/boxed
treatment entirely so it matches the flat, borderless style of the existing tool-call
rows. The demo Storybook stories are updated to this flat variant before the founder's
re-check in Chrome.

**D11 — `model_retired` wording (resolves FQ-101; was MAJ-103).** Reword away from
D9's original "choose another model" (which collides with the existing copy-rule test
banning that phrase for `config`-attributed codes) to: *"This model is no longer
offered by {provider}. Pick a new model in the agent's settings."* Attribution stays
`config` (unchanged from D9) — only the wording changes.

**D12 — A retired primary model falls back (resolves FQ-102; was MAJ-109).** When the
primary model is `model_retired`, the turn falls back to the configured Fallback
model, exactly as any other retriable failure would, and the fallback note fires
naming both — plus the "pick a new model" hint for the retired one. No fallback
configured → end the turn with the `model_retired` line, unchanged.

**D13 — Turn off the Anthropic SDK's own internal 429 retries (resolves FQ-103; was
MAJ-110).** So the visible countdown (D3's 3 attempts) is the only retry a user's turn
makes — no hidden SDK-level retries multiplying the real call count. Audit every other
SDK-backed adapter for the same default-retry behavior and disable it there too if
found, so "3 attempts" means 3 attempts everywhere, not just on the HTTP-adapter
family. **Audit result (final fix round, verified in this task):** exactly **two**
SDK-backed adapters auto-retry today, and both get the `option.WithMaxRetries(0)`-equivalent
fix: `pkg/providers/anthropic/provider.go` (client built at `anthropic.NewClient` with
only `WithAuthToken`/`WithBaseURL`; the SDK's `internal/requestconfig/requestconfig.go`
defaults `MaxRetries: 2`) and `pkg/providers/codex_provider.go` (`openai.NewClient`;
openai-go/v3's `internal/requestconfig/requestconfig.go` defaults `MaxRetries: 2`).
Everything else was checked and is clean: `azure` and `bedrock` do their own raw
`http.Client.Do` (azure imports openai-go for request/response types only — no SDK
transport, no auto-retry); `openai_compat` and `anthropic_messages` are raw HTTP via
`HandleErrorResponse`; the CLI-backed family (`claude_provider.go`, `codex_cli_provider.go`,
`copilot_cli_provider.go`) spawns an external process — there is no in-process HTTP retry
to disable, and the CLI binary's own internal behavior is outside this feature's reach
(no retry-after facts flow from it either, §7.2). The work itself is specified in §7.2.

**D14 — Total per-turn retry-wait cap: 10 minutes (resolves FQ-104; was OBS-101).**
Across every candidate in a multi-model retry-then-fallback chain, the sum of all
honored retry-after waits in one turn is capped at 10 minutes (founder's number, not
the review's suggested 5). Once the cap is reached, the turn ends with the terminal
`rate_limited` line — no further candidate is tried even if one remains unattempted.
The number lives in **C-9** (§4) and **FR-005** (§14), and the terminal behavior in
§7.4 and scenario A-10 (§10).

**D15 — `quota_billing` attribution: `config`; joins the operator-only set (resolves
Q4; was MAJ-107).** Out-of-credit is the operator's setup to fix, not the provider's
fault and not something a wait clears. `quota_billing` is attributed `config` (same as
`model_retired`, D9/D11) and is added to `classifyOperatorOnlyTurnError`'s membership
alongside `model_retired`: a scheduled task or the Judge stops the unattended run **at
once** on either code — no retries burned against an account that is not going to
un-empty itself mid-turn. **The contract wave (§7.1) lands with `config` as the final
attribution — no placeholder is committed at contract-cut time** (resolves MIN-106).

**Still open — a proposal, not a decision; the spec states 2-3 options with a
recommendation in §16 for the founder to rule on next:**

- **NEW — the resume signal.** There is no lockout (D2) and no billing button (D6), so
  once the operator fixes the account (or a retired model gets reassigned), how does
  everyone learn it works again — the user waiting in a stalled chat, and any stopped
  unattended work (a scheduled task ended `Failed` per D15, or a Judge-withheld
  dispatch)? The spec proposes options; the founder picks one in the next round.

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
| F4 | Inline retries (delegated 429 backoff) are silent; a successful fallback model swap is log-only. The user sees a hung turn or an unexplained style change. Streaming resets and transport/timeout retries are equally silent **by design and stay out of scope** (§4 non-behaviors; MAJ-102's scoping). | `pkg/agent/loop_provider_retry.go::callProvider` (retries logged only; `EventKindLLMRetry` is on the WS-forwarder's explicitly-not-forwarded list, `pkg/gateway/websocket_forward.go`); `pkg/agent/loop_run_turn.go::callProviderOnce` (fallback success is `logger.InfoCF` only) |

**Why now:** the founder chose the whole mechanism (PM-1: facts instead of raw text in the *displayed* line) and confirmed by D1 that the fix is display-side.

## 2. Existing codebase context (verified this task)

### Symbols involved

| Symbol | Role in this feature |
|---|---|
| `pkg/providers/common/common.go::ProviderError`, `HandleErrorResponse`, `WrapHTMLResponseError` | modify — gain parsed response headers (`retry-after` integer-seconds / HTTP-date, `retry-after-ms`; request ID). Provider/model identity is **not** threaded through here (no signature change — MAJ-110): identity comes from the per-candidate attempt (§7.2) |
| `pkg/providers/common/common.go::ProviderError.Error()`, `ResponsePreview` | keep — log-side rendering unchanged; the scrubber stays for logs |
| `pkg/providers/error_classifier.go::ClassifyError`, status switch (`classifyByStatus`), `classifyByMessage`, `billingPatterns` | modify — unambiguous-account-state billing detection (HTTP 402, structured `insufficient_quota` code field, the C-5 phrases on 4xx — never 5xx) before rate-limit; `billingPatterns` narrowed to equal the C-5 phrase set (MAJ-106); **new `404 → FailoverUnknown` mapping** so a 404 no longer aborts the chain as "unclassified error … do not fallback" (D12); billing verdicts take the standard failure path |
| `pkg/providers/cooldown.go::calculateBillingCooldown` | **delete** (D2 — no billing lockout; per the founder's delete-superseded-code ruling the unreachable function is removed, not left dead) |
| `pkg/providers/fallback.go::FallbackChain.Execute`, `candidateBudget`, `defaultPerCandidateTimeout` | modify — gains the **per-candidate rate-limit retry loop around `run(...)`** for a `FailoverRateLimit` verdict, before `MarkFailure`; a **fresh per-call budget** per retry call (a retry wait never eats into the 120 s call budget); the D14 10-minute total-wait cap (MAJ-101, D14) |
| `pkg/agent/operator_only_turn_error.go::classifyOperatorOnlyTurnError` (+ consumers `pkg/agent/verifier_adjudication.go::judgeDispatchNeedsOperator`, `pkg/agent/task_run_loop.go::finishRunTurn`) | modify — `quota_billing` and `model_retired` join the set as new causes; a task run ends Failed at once and a Judge dispatch withholds at once on either code (D15, MAJ-107) |
| `pkg/providers/anthropic/provider.go` (`anthropic.NewClient`), `pkg/providers/codex_provider.go` (`openai.NewClient`) | modify — SDK internal retries disabled (`option.WithMaxRetries(0)`-equivalent) so D3's 3 attempts are the only retry (D13 — the audit found exactly these two SDK transports) |
| `pkg/gateway/ws_hub_projection.go` (`hubFrameKind`, `hubKindItem`, the `done` → `p.clear()` path) | modify — the `provider_retry` frame becomes a per-turn projection item, replaced by each newer retry frame and cleared by the terminal/done frame, so a freshly-attached tab's **snapshot** shows the live countdown (MAJ-108) |
| `pkg/agent/translate_error.go::TranslateLLMError`, `classifyByProviderError`, `classifyByHTTPStatus`, `rateLimitSubstrings`, `isRetryable` | modify — new `quota_billing` code; narrowed billing detector (C-5 vocabulary) placed first inside the 4xx-with-body branch and read on the 429 path; narrowed `model_retired` detector (C-24); facts threading; template assembly into `Message` (MAJ-001) |
| `pkg/agent/translate_error.go::buildDetail`, `BuildDetail` | **keep unchanged** — `detail` stays on the wire per ADR-051 Rev 3 Q2 / RD7 and founder D1 (the round-0 "delete detail" plan is withdrawn) |
| `pkg/agent/translate_error.go::ProviderError` (agent-side), `ProviderErrorFromFailover`, `providerErrorFromChain` | modify — carry the failing attempt's provider/model (sourced from `FailoverError.Provider/Model` and `FallbackExhaustedError.Attempts`, not from `HandleErrorResponse` — MAJ-110) + new header facts |
| `pkg/agent/events.go::ErrorPayload` | modify — gains provider/model identity + facts; the agent emit site assembles the templated sentence into `Message` when facts are present |
| `pkg/agent/events.go::LLMRetryPayload`, `EventKindLLMRetry`, new `EventKindProviderRetry` | extend additively — `LLMRetryPayload` gains provider, model, `retry_at`, `sent_at`; the provider rate-limit retry path emits the **new `EventKindProviderRetry`** (MAJ-102), which the hub forwards as the `provider_retry` frame **built from named fields only**; `EventKindLLMRetry` keeps its seven other reasons unforwarded and stays on the not-forwarded list; `LLMRetryPayload.Error` stays log-only and is never read by the hub handler (MAJ-015, C-2) |
| `pkg/gateway/websocket_forward_hub.go::hubError` | modify — threads facts (provider, model, request id) onto the error frame; `Detail` population untouched (ADR-051). Note today's `if p.Code != "" { message = p.Message }` pass-through stays — the sentence is assembled upstream, in the agent (MAJ-001) |
| `pkg/gateway/websocket_forward_hub.go::hubSyncTap` | modify — new handler forwarding `EventKindProviderRetry` to the `provider_retry` frame (named fields only, registered as a per-turn projection item, MAJ-108) plus the fallback handler |
| `pkg/agent/turn_transcript.go::writeErrorTranscriptWithAbandonment` / `appendClassifiedError` | modify — the new templated codes join the trusted set (or the assembled message is threaded through without re-deriving) so the provider-named sentence persists instead of `defaultUserMessage(code)` (MAJ-001), and the persisted entry carries the `provider_message` subtype (MAJ-104) |
| `src/lib/llm-error.ts::getLLMErrorDisplay` | modify — renders `le.message` only when the persisted entry / replay payload carries the `provider_message` subtype flag — **not** keyed on error code alone (MAJ-104); the `detail` branch is kept as is |
| `src/components/chat/MessageItem.tsx` (native `<details>` "Technical details" disclosure) | modify — the ONE reuse target (MIN-010): extended under Verbose chat with facts lines; `detail` rendered as inert text (MAJ-018; the inert rendering itself is a **regression guard**, DG-5 — OBS-102) |
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
| `ClassifyError` order + `calculateBillingCooldown` deletion | MEDIUM | fallback routing + cooldown curves (`calculateStandardCooldown` stays the only curve); delegated 429 retry gate (`shouldRetryDelegatedRateLimit`); **new 404 → `FailoverUnknown`** mapping means every 404 now falls back instead of aborting the chain (D12 — intended: the next candidate may still answer) |
| `getLLMErrorDisplay` subtype gate | LOW | every error-bubble render path; other codes' copy-rule tests must stay green |
| `FallbackChain.Execute` (retry loop + cap) | MEDIUM | fallback routing for every multi-candidate agent; the delegated single-candidate path (`callProvider`) is unchanged; the retry loop must `MarkFailure` **once** per candidate, after the loop gives up |
| `classifyOperatorOnlyTurnError` membership | LOW-MEDIUM | task runs (`finishRunTurn` — ends Failed, no attempt consumed) and the Judge (`judgeDispatchNeedsOperator` — withholds, round not consumed); unit row per consumer |
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

PM-1's fourth example ("OpenRouter no longer offers z-ai/glm-4…") is settled by **D9** (FQ-7 resolved): template `model_retired` in §6, classification C-24 / FR-016, scenarios MR-1/MR-2. It no longer ships as today's generic sentence.

## 4. Behavioral contract

### Primary flows

- When a provider rate-limits a turn and sent a `retry-after` of 2 minutes or less, the system retries that model automatically (3 provider calls total) and shows a live countdown naming the provider, the wait, and the attempt ("OpenRouter is busy. Retrying automatically in 1:32 (attempt 2 of 3).").
- When the provider asks for a wait longer than 2 minutes, no countdown is shown; the remaining retries on that model are skipped and the fallback chain is tried immediately (or the turn ends with the terminal message when no fallback is configured).
- When the total honored retry-wait time in one turn reaches 10 minutes (D14), the turn ends with the terminal `rate_limited` line — no untried candidate is attempted after the cap.
- When the provider has retired the configured model and a Fallback model is configured, the turn falls back (D12) and the note names both — with the pick-a-new-model hint for the retired one; with no fallback configured the turn ends with the `model_retired` line.
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
| C-5 | The billing rule, stated once and applied by **both** classifiers: HTTP **402**, OR the OpenAI `insufficient_quota` error **code** matched on the structured JSON `code`/`type` field (not free text), OR one of the unambiguous account-state phrases "insufficient credits", "insufficient balance", "credit balance too low", "credit balance is too low" (Anthropic's wording), "payment required" — **on a 4xx status only, never on 5xx**. "billing", "usage limit reached" and "exceeded your current quota" are NOT in the vocabulary (CRIT-001). |
| C-6 | Every D2-dataset negative row — the Gemini per-minute 429 text, a Codex "usage limit … try again in 3 days" message, an OpenAI 429 whose message links to a billing page, and a plain "too many requests" — classifies `rate_limited`, never `quota_billing`. |
| C-7 | A `quota_billing` verdict never produces a billing-specific cooldown or lockout of any length: `calculateBillingCooldown` is deleted and the standard failure path (`calculateStandardCooldown`) is the only curve. The cooldown chosen for each D2 dataset row is pinned by test (D2). |
| C-8 | `max_attempts` = **3 total provider calls including the first**; a retry frame's `attempt` = the number of the call **about to be made** (2..3); `attempt` ≤ `max_attempts` on every retry frame (MAJ-007, D3). |
| C-9 | Auto-retry honors the provider's `retry-after` only when it parses to ≤ 120 s (countdown shown). A wait above 120 s, or a malformed/past value, means: no auto-retry of that model — skip its remaining retries, move to the fallback chain, or end the turn with the terminal message when no fallback is configured. `retry-after: 0` is treated exactly like a missing header (MIN-101): both take the backoff schedule, whose first step is whatever §7.4 states (2 s) — no "immediate" special case, no presence-distinguishable wire marker. **Per-turn total-wait cap (D14): the sum of all honored retry-after waits across every candidate in the chain is capped at 10 minutes; when the cap is reached mid-chain, the turn ends with the terminal `rate_limited` line even if a candidate is untried.** (MAJ-003, D3, D14, MIN-003) |
| C-10 | A provider auto-retry is scheduled only when zero streamed bytes have reached the turn. |
| C-11 | The retry frame carries `retry_at` (server wall clock, RFC 3339), `retry_after_seconds`, and a server-side `sent_at` (RFC 3339, MAJ-108). The client does **not** compare `retry_at` against its own raw clock: it keeps a per-connection server-clock estimate `serverNow = max over received frames of (sent_at + client-elapsed-time since that frame's receipt)`, and renders `retry_at − serverNow`, clamped at 0. Under zero transport latency this equals counting down `retry_after_seconds` from receipt — skew-immune by construction (no cross-clock comparison). A component test pins this with a simulated ±30 s client clock offset. |
| C-12 | Stop during a retry wait ends the turn within 1 s and no further attempt is made; the wait sleep is context-cancellable (D4, MAJ-017). |
| C-13 | The assembled sentence reaches all four surfaces — live bubble, persisted transcript, replay, channel reply — for the templated codes; persisted transcripts carry code + assembled message, never body/headers/raw JSON; replay strips `detail` (existing). (MAJ-001) |
| C-14 | The SPA renders `le.message` **only when the persisted entry / replay payload carries the `provider_message` subtype flag** (set at write time by the agent's assembly path, MAJ-104) — never keyed on error code alone: any other producer of the four templated codes (the own-limiter persists `rate_limited` rows with internal text, `pkg/agent/loop.go` line ~864) keeps today's rendering. Every code without the flag keeps catalogue copy, proven by test (MAJ-001, MIN-007). |
| C-15 | Countdown text uses mm:ss ("1:32"), per PM-1's own example; the own-limiter `RateLimitIndicator` format ("1m 32s") is unchanged. |
| C-16 | `{provider}`/`{model}` in any message or frame come from the attempt that actually produced the classified error — sourced from the per-candidate attempt (`FailoverError.Provider/Model`, or the `run` closure in `callProviderOnce`, which already has `provider, model` in scope — MAJ-110), never unconditionally from the primary candidate and never threaded through `HandleErrorResponse`; a skip-only chain has no provider fact, so the catalogue fallback applies (MAJ-006's identity rule, restated). |
| C-17 | The fallback note persists: a `session.TranscriptEntry`-family shape plus a replay carrier defined in the contract wave; the text says "Fallback model" and never "backup" (D7, MAJ-010). |
| C-18 | A cooldown-driven **skip** (not only a hard failure) that leads to a fallback answer also produces the note — at most once per session per (unavailable, answered) pair until the pair changes (D7, MAJ-010). |
| C-19 | No assembled message and no facts field ever names a key label or contains key material/fragments. The Verbose raw view shows the provider body as-is (scrubbed of registered credential values only); D5 accepts that unrecognised fragments may appear there — no "never" is claimed for the raw view (CRIT-002 resolved by risk acceptance). |
| C-20 | `request_id` appears on the wire inside facts but renders only under Verbose chat. |
| C-21 | The Verbose disclosure renders `detail` as inert text — never HTML-rendered, never markdown-rendered — pretty-printed only when it parses as JSON; `detail` is not always JSON (`WrapHTMLResponseError` case) (MAJ-018). **Regression guard, not new hardening (OBS-102):** `src/components/chat/MessageItem.tsx` already renders the disclosure content as an inert React text node (`{message.errorDetail.slice(0, ERROR_DETAIL_MAX_CHARS)}`) — only the JSON pretty-print behavior is genuinely new; DG-5 guards the existing inertness, it does not deliver it. |
| C-22 | Rate-limit wait capture parses ONLY `retry-after` (integer seconds or HTTP-date) and `retry-after-ms`; no `x-ratelimit-reset*` parsing exists (MAJ-008, simpler option — see §7.2 for why). |
| C-23 | Context-length errors keep today's code, copy, and behaviour (PM-5) — regression-guarded. |
| C-24 | The `model_retired` detector fires ONLY on HTTP 404 AND a body matching one of the explicit retirement phrases "has been decommissioned", "no longer supported", "deprecated and removed" — or a provider-pinned structured error code (e.g. `model_not_found`) **combined with** one of those phrases. Merely naming the requested model id, "does not exist", or "model not found" alone are **not** sufficient triggers (MAJ-109: OpenAI's 404 "does not exist **or you do not have access to it**" and Ollama's "not found, try pulling it first" are false-positive wordings; the D2 near-miss rows pin them). A generic 404 with no retirement phrase stays `CodeUnknown` — the residual path and the media strip-retry gate (`media_downgrade.go::outcomeFallbackEligible`, §2) are NOT re-pointed; a media-bearing 404 that merely echoes the model id (MR-3) stays `CodeUnknown` and the gate still fires. A non-404 status never classifies `model_retired` (D9, D11, MR-1…MR-3). |

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
| `quota_billing` (new) | `{provider} says your account is out of credit.` | Always (facts present). No button, no link — D6. Attribution `config` (D15) — the copy rules forbid retry advice, and none is present |
| `provider_auth_failed` | `{provider} rejected the API key. Check the key in Settings → Providers.` | Facts present |
| `model_retired` (new, D9/D11) | `This model is no longer offered by {provider}. Pick a new model in the agent's settings.` | A provider refuses/retires the requested model during a real call, per the C-24 trigger (404 + an explicit retirement phrase, or a provider-pinned code combined with one). Never fired for the mid-session switch-failure case — that stays `model_unavailable`'s meaning |
| fallback note | `Answered by the Fallback model ({answered_model}) because {unavailable_model} was unavailable. Pick a new model in the agent's settings.` — the trailing hint appears **only when the unavailable model was `model_retired`** (D12) | Fallback success, including cooldown-skip-produced fallbacks at the C-18 rate (frame + persisted note; D7). `{unavailable_model}` names the **primary** (first candidate) in a multi-hop chain (MIN-103) |

All other codes keep today's catalogue copy unchanged (generic, no provider naming in this feature). Context length stays exactly as is (PM-5, C-23). All four PM-1 example sentences are now covered: the first three by the rows above; the fourth founder example (model no longer offered) by the `model_retired` row — added by D9 (FQ-7 resolved).

**Copy rules carried over:** a `config` message never advises retry. `model_retired` is `config` (D9/D11) — its template points at the config fix and never at a retry; `quota_billing` is `config` too (D15, final — no placeholder), so its sentence also carries no retry advice; the copy-rule tests are extended accordingly and **run over the `provider_message` variants as well** (MAJ-103). When facts are absent for `model_retired` (provider unknown), the catalogue `message` renders: "This model is no longer offered by its provider. Pick a new model in the agent's settings." — the generator's bijection (C-3/C-4) requires this facts-absent text to exist (MAJ-103's second half).

**Contract mechanics for templates (simplified per OBS-002).** The round-0 design — a separate `x-user-message-templates` YAML block, a closed slot-vocabulary grammar and generator extensions in two generators — was overbuilt for four server-side sentences with one or two slots each. Simplified: each relevant `x-user-messages` entry gains an optional sibling `provider_message` string (the templated variant); the generator enforces only (a) the existing code↔message bijection extended to entries carrying `provider_message` (C-3/C-4) and (b) that slots used are from the closed set `{provider}`, `{answered_model}`, `{unavailable_model}` — a simple token check, not a template engine. The Go translator substitutes `{provider}` (failing attempt) when facts are present and falls back to the static catalogue sentence otherwise. The live-retry line is **not** a server template: the countdown is inherently client-rendered and the frame carries only structured facts, so the line is assembled client-side from those facts and pinned by component tests (C-15).

## 7. Backend work breakdown

Order is contract-first (Hard Constraint #8): §7.1 lands as one atomic commit with regenerated artifacts before any Go/TS consumer code.

*Withdrawn from the round-0 plan:* former §7.5 ("Verbose raw-detail retention + fetch") and former §7.6 ("Provider catalog `billing_url`") — deleted by D1 (no fetch, no retention) and D6 (no billing button). No REST route is added; `contracts/openapi.yaml` is untouched by this feature.

### 7.1 Contract wave (first)

1. `contracts/components/schemas/LLMError.yaml`: add `quota_billing` to the enum + `x-user-messages` entry, **attribution `config` — final, per D15 (no placeholder; MIN-106 resolved)**; add `model_retired` to the enum + `x-user-messages` entry (attribution `config`, D9/D11) with its `provider_message` variant and its facts-absent catalogue `message` (§6); add the optional `provider_message` templated variants to the relevant `x-user-messages` entries (§6, OBS-002 simplification); add the optional `facts` object on the **error frame: `provider`, `model`, `request_id` only** (all optional; `request_id` flagged Verbose-render-only; **no `retry_after_seconds`, no `retry_at`, no `attempts`, no `max_attempts` — the SPA has no consumer for retry facts on the error frame (OBS-103); retry timing facts live on the `provider_retry` frame, where their consumer is the indicator**; no `billing_url`, no `error_id`). **`detail` stays exactly as it is** (D1 — the round-0 removal plan is withdrawn; the field is optional today so nothing breaks).
2. `contracts/asyncapi.yaml`: keep the inline `LLMError` / `LLMErrorReplay` copies in lockstep (the file's own comment requires it); add the two new messages (`provider_retry`, `provider_fallback`) on the chat channel; add the fallback-note **replay carrier** (a `ProviderFallbackNote` replay entry/shape on the session channel) so the persisted note is not a live-only orphan (MAJ-010, C-17).
3. `contracts/components/schemas/`: new `ProviderRetryFrame.yaml` — `type: provider_retry`; `session_id`, `turn_id`, `provider`, `model`, `retry_at` (RFC 3339), `retry_after_seconds`, `sent_at` (RFC 3339 — C-11's skew-corrected countdown, MAJ-108), `attempt`, `max_attempts`, `error_code`; + `seq` per the #823 pattern; **no `error` field of any kind** (C-2). New `ProviderFallbackFrame.yaml` — `type: provider_fallback`; `session_id`, `turn_id`, `answered_model`, `unavailable_model`, `unavailable_code` (enum: `rate_limited` | `model_retired`) — **the round-2 `reason` enum (`failed` | `cooldown_skip`) is dropped (OBS-103): no rendered text varied by it; `unavailable_code` is the field that DOES vary rendered text (it gates the D12 hint in the note template, §6) and has a named consumer. The frame alone no longer records whether the unavailable model hard-failed or was cooldown-skipped — that distinction lives only in the once-per-pair note rate and the session transcript (C-18/MIN-103), which is where it has a consumer.** + `seq`; no raw error text. `ReplayErrorFrame` and `LLMErrorReplay` unchanged (no facts object on replay — the message itself carries the provider naming; C-13); the replay carrier for the persisted fallback note and the `provider_message` subtype flag ride `LLMErrorReplay` (MAJ-104/C-17).
4. `contracts/openapi.yaml`: **unchanged** — no new route (§5).
5. `scripts/gen-contracts.sh` (bijection hard-fail extended to `provider_message`-carrying entries + the closed-slot token check), commit spec + generated diff atomically; `make verify-contracts` green.

### 7.2 Capture (provider side)

- `common.ProviderError` gains `RetryAfterSeconds` (a plain `int`; **no presence flag, no pointer — MIN-101**: `retry-after: 0`, a past HTTP-date, and a malformed value all parse to 0, and 0 means "fact absent, take the backoff schedule" everywhere downstream; C-9) and `RequestID`; parsed at `HandleErrorResponse` and `WrapHTMLResponseError` from `retry-after` (integer seconds **or** HTTP-date; a past date or a malformed value → 0 = absent, first value wins on duplicates) and `retry-after-ms` (rounded up, ≥ 1; absent → 0).
- **Reset-header scope (MAJ-008 — the simpler option, chosen):** parse `retry-after` and `retry-after-ms` ONLY. The generic `x-ratelimit-reset*` family is dropped from scope: its formats are not one thing (Go durations on OpenAI, RFC 3339 on Anthropic, epoch-milliseconds on OpenRouter, JSON-body `retryDelay` on Gemini), so correct parsing needs a per-provider-family format table with a parse test per row — parser surface with no payoff, because D3's 2-minute ceiling makes every multi-hour reset header irrelevant anyway (a wait above 120 s is "no auto-retry" regardless of how precisely it is known). A duration string like `6m0s` or a millisecond epoch parsed as seconds would silently yield garbage; not parsing them yields the backoff schedule, which is always safe.
- Request-ID header candidates: `x-request-id`, `request-id`, `x-amzn-requestid`, `cf-ray` (last resort — it is Cloudflare's edge ID, not a provider request id; labelled as such in the Verbose facts, OBS-003).
- **Attempt identity (MAJ-110 — supersedes round-2's MAJ-006 boundary-stamp design):** the producing provider id + model come from the **per-candidate attempt itself**, not from `ProviderError`. `FallbackChain.Execute` already threads the identity: the `run` closure invoked per candidate closes over that candidate's provider and model (`loop_run_turn.go::callProviderOnce` builds it), and a chain failure surfaces as `FallbackExhaustedError`/`FailoverError` carrying `Provider`/`Model` per attempt (`FallbackError.Attempts`). Retry frames, fallback frames, the terminal message and the fallback note therefore name the attempt that actually failed — never unconditionally `rt.activeCandidates[0]` — **with no signature change to `HandleErrorResponse` or `ProviderError`**. `HandleErrorResponse`/`WrapHTMLResponseError` keep parsing headers into the response types only.
- **Adapters with no `retry-after` fact (MAJ-110's explicit list):** header parsing applies to the raw-HTTP family only — `openai_compat`, `azure`, `anthropic_messages` (`HandleErrorResponse`), and the HTML path (`WrapHTMLResponseError`). The **SDK-backed** adapters — `anthropic` (anthropic-sdk-go) and `codex` (openai-go/v3) — and the **CLI-backed** adapters — `claude`, `codex_cli`, `copilot_cli` (process exit, no headers) — produce no retry-after fact; their rate-limit failures take the §7.4 backoff schedule (2 s × 2 capped 30 s), which is always safe. No adapter invents a retry-after value it did not receive.
- **D13 — disable SDK-internal retries (both SDK transports, audit complete):** `option.WithMaxRetries(0)` (the openai-go/v3 spelling; anthropic-sdk-go has the identical option) on the `anthropic.NewClient` and `openai.NewClient` constructions, so the SDK does not silently absorb a 429 inside the client and starve our visible retry loop of its frame. Audit result (§2 symbols table): these two are the only SDK transports; `azure`/`bedrock` use a raw `http.Client.Do`, `openai_compat`/`anthropic_messages` use raw HTTP via `HandleErrorResponse`, and the CLI family spawns processes — nothing to disable there.
- The scrubber is untouched: `BodyPreview`/log lines keep `ScrubSensitiveValues`; `buildDetail` keeps scrubbing before its preview cut, as today.

### 7.3 Classify (both classifiers) + assemble (MAJ-001)

- **User side** (`translate_error.go`): new `CodeQuotaBilling` (`quota_billing`); ONE billing rule, placed to match `classifyByHTTPStatus`'s status-first structure: (a) HTTP 402 — via the existing status map (status-first, body not needed); (b) the C-5 phrase/`insufficient_quota` detector runs on the **429 path before the 429 short-circuit declares rate-limit** (429 + a C-5 marker → `quota_billing`; 429 with prose-only quota wording — "exceeded your current quota", "usage limit reached", deliberately NOT in the vocabulary, C-6 — stays `rate_limited`), and (c) inside the **4xx-with-body branch, first detector, before the media detectors and the residual `CodeUnknown`** — so a 400 "credit balance is too low" (Anthropic's wording) lands `quota_billing`. **Never on ≥ 500** — `classifyByHTTPStatus` returns `CodeNetwork` for `status >= 500` before any body check, and C-5's "4xx only, never 5xx" rule is structural, not advisory. `isRetryable` returns false for it. The residual-4xx → `CodeUnknown` verdict is untouched (media strip-retry gate).
- **No billing lockout (D2):** a `quota_billing` verdict takes the standard failure path — `calculateStandardCooldown` at most, exactly like any other failure. `pkg/providers/cooldown.go::calculateBillingCooldown` is **deleted** (unreachable after this change; delete-superseded-code ruling). A test pins the curve chosen for every D2 dataset row (C-7).
- **Providers side** (`error_classifier.go`): `billingPatterns` is **narrowed to equal the C-5 set** (MAJ-106) — the structured `insufficient_quota` field, the phrase list (with Anthropic's "credit balance is too low" added), and HTTP 402 via `classifyByStatus`'s status map. **Dropped from `billingPatterns`:** the `\b402\b` **text** regex (it matches any prose "402" token), "plans & billing", and the bare "credit balance" prefix. Ordering mirrors the user side: the billing markers are checked **before the rate-limit patterns** — on the 429 path a C-5 marker lands `FailoverBilling`, not `FailoverRateLimit`. Routing behaviour is otherwise preserved (`shouldRetryDelegatedRateLimit` keeps its rate-limit semantics; billing was never in its retry set). **Which classifier gates retry (MAJ-106's question): the routing classifier** — `FallbackChain.Execute` consults `providers.ClassifyError`/`IsRetriable`, and the delegated gate is `shouldRetryDelegatedRateLimit` → `providers.ClassifyError`; the user-side `isRetryable` gates only the copy choice (retry line vs terminal line). **A unit test asserts both classifiers agree on every D2 row (B-1's cross-classifier oracle).**
- **New `model_retired` detector (D11, C-24 — triggers narrowed per MAJ-109):** user-side only (`translate_error.go`), checked at the residual-4xx fallback BEFORE the residual path declares `CodeUnknown`: `model_retired` fires only on an explicit retirement signal — the body matches one of the retirement phrases **"has been decommissioned"**, **"no longer supported"**, **"deprecated and removed"** — or the provider-pinned code `model_not_found` **combined with** one of those phrases. "The body names the requested model id" and the bare "does not exist" are **dropped as sufficient triggers** (a bare "does not exist" also fits OpenAI's access-denied 404 "The model `X` does not exist or you do not have access to it", and Ollama's not-pulled 404 "model 'llama3' not found, try pulling it first" — both near-misses get D2 rows pinned to NOT `model_retired`). `CodeModelRetired` is a new constant — never a re-use of `CodeModelUnavailable`, whose mid-session switch-failure meaning is different; `isRetryable` returns false for it; attribution `config` (D15 — it joins `classifyOperatorOnlyTurnError`). A 404 with no retirement signal falls through to the residual `CodeUnknown` verdict exactly as today — the media strip-retry gate (`media_downgrade.go::outcomeFallbackEligible`, fires only on `CodeUnknown`) is not re-pointed, and an MR-3 row pins that a media-bearing 404 whose body echoes the model id stays `CodeUnknown` with the gate firing (MR-3). Non-404 statuses never reach this detector. **Routing (D12): `classifyByStatus` gains `case status == 404: return FailoverUnknown`** — `FailoverUnknown` is retriable per `IsRetriable()` (`types.go`; only `format`/`context_overflow` are excluded), so `FallbackChain.Execute` marks the retired primary and moves to the configured Fallback instead of aborting with "unclassified error … do not fallback". Stated side effect: **every** 404 — access-denied, not-pulled, region-restricted — now falls back to the Fallback model and, on its success, shows the fallback note. That is intended: the note names what happened and the once-per-pair rate (C-18) keeps it from repeating.
- **Facts threading:** `TranslateLLMError`'s verdicts gain facts from `pe` (the new header fields, the stamped provider id + model) via an additive variant (no signature break at the two choke points' existing callers).
- **Assembly (the MAJ-001 fix — the sentence must actually reach the user):**
  1. **Agent emit sites** assemble the templated sentence into `ErrorPayload.Message` when facts (provider/model of the failing attempt) are present; catalogue fallback otherwise. `hubError`'s existing `p.Message` pass-through then carries the assembled sentence — no gateway-side assembly.
  2. **Transcript writer:** `writeErrorTranscriptWithAbandonment` currently overwrites the message with `defaultUserMessage(code)` for coded, non-trusted errors. Fix: extend the trusted-message set (`isTrustedInternalStage`/`trustedInternalStageSet`) with the new templated codes — or thread the already-assembled `LLMError` through `appendClassifiedError` without re-deriving — so the provider-named sentence persists. The widening is limited to exactly the templated codes; the existing trusted stages' semantics are unchanged.
  3. **SPA:** `getLLMErrorDisplay` renders `le.message` **only when the entry carries the `provider_message` subtype flag (MAJ-104)** — the server, not client code, decides which messages are trusted, and the replay carrier (`LLMErrorReplay`) carries the same flag so a reloaded session renders identically. Everything else keeps catalogue copy, including every pre-existing trusted stage (the existing `delegated_task_limit` exception keeps working unchanged). A test proves every non-flagged code still renders catalogue copy (C-14, MIN-007). This is a stated trust-boundary change: the SPA trusts server message text exactly where the flag is set.
  4. **Channel replies:** the `TranslateTurnError(err).Message` sites (`pkg/agent/session_worker.go`, `pkg/agent/loop.go`) use the same facts-aware translator, so Telegram/Discord replies carry the assembled sentence (MAJ-001 iv).
  5. BDD scenarios pin replay text and channel text (DG-7, DG-8; §10).
- Loop identity: the loop threads provider display name + model of the **failing attempt** into `ErrorPayload` (additive fields) so the agent-side assembly can name it (C-16).
- **Operator-only turn errors (D15, MAJ-107):** `quota_billing` and `model_retired` join `pkg/agent/operator_only_turn_error.go::classifyOperatorOnlyTurnError` — new causes `operatorFixModelRetired` and `operatorFixQuotaBilling`, attributed `config`, `isRetryable` false, so a **task run** stops at once with lifecycle `failed` + reason `operator_action_required` (`pkg/agent/task_run.go::finishRunTurn` → `transitionTaskLifecycle`) instead of retrying a dead model for hours, and the **Judge** stops the same way (`judgeDispatchNeedsOperator` → `JudgeMisconfiguredReasonPrefix`-family reason on the attempt record). Chat turns keep the ordinary classified-error path. Unit rows pin each consumer: a task run gets `LifecycleFailed`/`operator_action_required` on both new causes; the Judge gets the operator-fix reason on both; a chat turn gets the §6 sentence, not the operator wording.

### 7.4 Retry visibility + fallback note (root path)

- **Policy (D3, re-placed per MAJ-101):** the per-candidate retry loop lives **inside `pkg/providers/fallback.go::FallbackChain.Execute`**, wrapped around `run(...)` for a `FailoverRateLimit` verdict **before `MarkFailure`**. This is the only placement that survives contact with the real chain: today a 429 is `FailoverRateLimit` → `IsRetriable()` → `MarkFailure` + next candidate at once (an outer loop would find the primary already in cooldown and skip it), and a loop inside the `run` closure would have one honored `retry-after: 120` wait eat the candidate's whole `candidateBudget` (default 120 s — `defaultPerCandidateTimeout`) and then die misclassified as `FailoverTimeout` — wrong code, wrong cooldown, wrong terminal line. `max_attempts` = **3 total provider calls including the first**; a retry frame's `attempt` is the number of the call about to be made (2..3) (C-8). Retry-then-fallback: the current candidate is retried inside the chain; a candidate's retries exhaust (or are skipped) before `MarkFailure` and the move to the next candidate. **The delegated path keeps its existing loop in `callProvider` (single-candidate; already works) — it is not migrated.**
  - **Budget rule (MAJ-101):** a **fresh `candidateBudget` per retry CALL** (not per candidate): waits are excluded from the budget entirely — each actual provider call gets a full budget. All sleeps are context-cancellable on the **turn** context (C-12).
  - Provider `retry-after` present and ≤ 120 s: wait exactly that (countdown shown from `retry_at`).
  - `retry-after` absent, `retry-after: 0`, or unparseable: exponential backoff 2 s × 2 capped 30 s with 25% jitter — every such wait is inherently under the ceiling.
  - `retry-after` > 120 s: **no countdown**; skip the candidate's remaining retries and move to the fallback chain immediately; no fallback configured → end the turn with the terminal `rate_limited` message (C-9).
  - **D14 cap mechanics:** the per-turn total-wait cap (10 minutes, C-9) is checked before scheduling each wait; a wait that would exceed the cap is **not truncated** — the candidate's remaining retries are skipped and the chain moves on, or the turn ends with the terminal `rate_limited` line (even with a candidate untried). The cap consumes none of any candidate's call budget. **State of the world on cap: the terminal line names the last candidate that actually failed (C-16); no cooldown is marked for candidates whose retries were skipped for cap reasons — only actually-failed candidates are marked, as today.** Unit rows: 1-candidate; 2-candidates-both-429 (each retried in turn, frames name each candidate); 2-candidates-one-billing (the billing candidate is not retried — `retryable: false` — the chain moves on at once).
  - **Which candidate's facts drive a frame (MAJ-002/MAJ-110):** each retry frame names the candidate it is about to call — identity from the attempt (`FailoverError.Provider/Model`, the `run` closure), not from `HandleErrorResponse` (§7.2); a candidate switch issues a new frame with that candidate's identity and a reset attempt counter. The terminal message names the last candidate that actually failed (C-16); a skip-only chain has no provider fact and gets the catalogue fallback.
- **Emission (MAJ-102 — a new event kind, not a reason filter):** the chain-level retry emits a **new `EventKindProviderRetry`** (`LLMRetryPayload` extended additively: provider, model, `retry_at`, `sent_at`; new payload field `max_attempts` for the indicator's "of N"). `EventKindLLMRetry`'s eight emitters and seven reasons are untouched — **the delegated path's `rate_limit` retries stay unforwarded and unannounced** (`EventKindLLMRetry` remains on the not-forwarded list in `pkg/gateway/websocket_forward.go`), keeping the delegated attempt semantics (1..2 of 2) visibly separate from C-8's root semantics (2..3 of 3) and preserving §13's "delegated policy unchanged" item. A TDD row asserts each of the seven other reasons — `streaming_reset`, `timeout`, `context_limit`, `empty_response`, orphan-tool-markup repair, truncated tool call, truncation continue — produces **no `provider_retry` frame** (RG-4).
- `hubSyncTap` gains a handler forwarding **`EventKindProviderRetry`** (and only it) to the `provider_retry` frame. The handler builds the frame **from named fields only** and never reads `LLMRetryPayload.Error` (C-2, MAJ-015) — that field stays for logs.
- **The retry frame carries `retry_at` AND `sent_at`** (both RFC 3339 server wall clock): `retry_at` drives the countdown; `sent_at` lets the client estimate server time (C-11, MAJ-108) — see §7.1 item 3 and §8 item 2 for the client derivation (`serverNow = max over received frames of (sent_at + client-elapsed-since-receipt)`; countdown = `retry_at − serverNow`, clamped ≥ 0). A tab attaching mid-wait renders the remaining time, not the original wait.
- **Stop ends the wait (D4, C-12):** the wait sleep is context-cancellable (`sleepWithContext` — the pattern already used by `loop_provider_retry.go::callProvider`); Stop cancels the turn context, the sleep returns at once, and no further attempt is made. Hard requirement with BDD scenario A-7 and a unit test on the context cancellation.
- **Fallback note (D7, MAJ-010, MIN-102/MIN-103):** on fallback success — currently `logger.InfoCF` in `callProviderOnce` — additionally emit the `provider_fallback` frame AND persist the note. The persisted note gets a `session.TranscriptEntry`-family type/status and the replay carrier from §7.1 (not a live-only orphan); **it is written AFTER the assistant answer entry in the transcript (MIN-103, ordering pinned by FB-2), so a reloaded session shows answer then note, and the note is NOT re-rendered as a duplicate on replay.** A cooldown-driven **skip** that leads to a fallback answer also produces the note (the frame's `unavailable_code: rate_limited`), **at most once per session per (unavailable, answered) pair until the pair changes, derived from the session transcript so the once-per-pair fact survives a restart (MIN-103)** — a chat must not fill with repeated notes during a long cooldown (C-17/C-18). **Scope is per TURN (MIN-102): within one turn, a later iteration that falls back to the same pair re-frames but neither re-announces the retry line nor re-notes the pair; a NEW turn inside the cooldown window does announce.** Text per §6: "Answered by the Fallback model ({X}) because {Y} was unavailable." — never "backup".
- On exhaustion, the terminal error frame carries the terminal `rate_limited` template line (assembled agent-side per §7.3).

## 8. Frontend work breakdown

1. **Frames & store:** `frames.ts` cases for `provider_retry` and `provider_fallback`; the error-frame path reads `facts`; retry state is separate from the own-limiter slot (`store.ts::setRateLimitEvent` untouched). No fetch path exists anywhere in this feature (D1).
2. **Retry indicator:** new `ProviderRetryIndicator` (design-system skill rules; reuses the `RateLimitIndicator` pattern — per-second interval, `role="status"` — but NOT its success-colour-at-0 or its dismiss button). **State table (MAJ-017):**

   | State | When | Rendering |
   |---|---|---|
   | waiting | frame received, `retry_at` in the future | "{provider} is busy. Retrying automatically in mm:ss (attempt {n} of {max})." — **{max} from the frame's `max_attempts` field (MIN-104), never hard-coded "3"**; mm:ss computed from `retry_at − serverNow` (C-11/MAJ-108: `serverNow = max over received frames of (sent_at + client-elapsed-since-receipt)`, clamped ≥ 0 — no cross-clock compare); ticks visually |
   | retrying-now | countdown reached 0, next frame not yet arrived | "Retrying now…" — **no success colour at 0** (the own-limiter's "cleared" meaning is wrong here) |
   | stopped-by-user | Stop pressed during the wait | indicator removed; the turn ends (C-12) |
   | terminal | terminal error frame arrives | indicator replaced by the terminal error line |

   **No dismiss control** — the indicator clears itself on the next frame; Stop is the only early exit (matches D4 and the ADR-082 rule that only explicit stop ends a turn early). **Live-region contract (MAJ-016):** the `aria-live` region announces once per attempt start (the full PM-1 line) and once on the terminal line; the per-second ticking mm:ss text sits OUTSIDE the live region (visually shown, not re-announced — component test asserts the live-region text does not change on a 1 s tick). A queued user message waits behind the waiting turn (turn model) — the indicator copy does not promise otherwise. **Same-provider model switch (MIN-104, decided):** when the chain moves within one provider (`openrouter/model-a` → `openrouter/model-b`), the live line inserts the model qualifier — "OpenRouter (model-b) is busy. Retrying automatically…". Different provider → plain "OpenRouter is busy…". A D3 row pins the qualifier (§12).
3. **Error bubble:** the server-assembled message as the one line (C-13/C-14); **no billing button and no billing link exist** (D6); the existing `detail` disclosure stays (NOT deleted — the round-0 deletion is withdrawn, D1).
4. **Verbose facts + raw view (D1, MIN-010, MAJ-018):** under Verbose chat, facts lines (provider, model, request id — `request_id` only here, C-20) plus the **existing** `MessageItem.tsx` native `<details>` "Technical details" disclosure — that is the ONE reuse target (PM-6's "reuse the tool-call accordion pattern"; the catalogued `src/components/ui/accordion.tsx` exists but a second accordion sibling next to the working disclosure would violate design-system skill rule 14's reuse-first rule). The disclosure's content rendering is tightened: `detail` renders as **inert text** — never HTML-rendered, never markdown-rendered — pretty-printed only when it parses as JSON (C-21; `WrapHTMLResponseError` means detail is not always JSON). **Regression guard (DG-5, OBS-102): `src/components/chat/MessageItem.tsx` renders detail as a plain inert text node today; a guard test pins that node byte-identical for non-JSON detail — the only new rendering behaviour in this feature is the JSON pretty-print.** Verbose off → no disclosure, no facts, nothing fetched (there is nothing to fetch).
5. **Fallback note:** grey event-line treatment (delegation event-lines precedent) for the persisted fallback note; replay parity via the §7.1 carrier; "Fallback model" naming (D7).
6. **`getLLMErrorDisplay`:** renders `le.message` **only when the entry carries the `provider_message` subtype flag (MAJ-104, C-14)** — the server-authored trust marker, not a code list; carries to replay via the `LLMErrorReplay` flag. Keeps the `detail` branch; the replay behaviour otherwise unchanged.

## 9. User stories & acceptance scenarios

### US-1 — Rate-limit countdown (P0; PM-1, PM-4, D3, D4)

**Narrative.** A user whose provider rate-limits a turn no longer stares at a vague "wait a moment": the turn keeps working, visibly, with the provider's own wait time — and Stop always works.

**Why this priority:** the most common provider failure; the founder's first example.

**Independent test:** inject a 429 with `retry-after: 120` at the provider adapter; observe the retry frame + countdown; exhaust attempts; observe the terminal line. (E2E uses a short `retry-after` for speed — MIN-005; the 120 s formatting is proven at component level.)

1. **Given** a turn whose provider answers 429 with `retry-after: 120`, **When** the agent loop schedules the retry, **Then** a `provider_retry` frame arrives with `provider`, `model`, `retry_after_seconds=120`, `retry_at` (now+120 s), `attempt=2`, `max_attempts=3`, and the chat shows "{provider} is busy. Retrying automatically in 2:00 (attempt 2 of 3)."
2. **Given** a retry in progress, **When** each second passes, **Then** the countdown decrements (computed from `retry_at − serverNow`, C-11/MAJ-108) and at 0 the next attempt is issued; between 0 and the next frame the indicator reads "Retrying now…".
3. **Given** a 429 without a usable `retry-after`, **When** the loop schedules the retry, **Then** backoff is exponential (2 s base, 30 s cap, jitter) and the line shows the backoff it actually applies — no fake time.
4. **Given** a `retry-after` above 120 s (e.g. 3600), **When** the response is classified, **Then** no countdown shows, the candidate's remaining retries are skipped, the fallback chain runs next — and with no fallback configured the turn ends with the terminal line.
5. **Given** retries exhausted, **When** the final attempt fails, **Then** the terminal error line is "{provider} is busy right now. You can retry the turn." and the indicator clears.
6. **Given** bytes already streamed to the turn, **When** a 429 arrives mid-stream, **Then** no retry is scheduled (C-10) and the terminal line shows instead.
7. **Given** a retry wait in progress, **When** the user presses Stop, **Then** the turn ends within 1 s, the indicator disappears, and no further attempt is made (C-12).
8. **Given** a second tab attaching (or a WS reconnecting) 100 s into a 120 s wait, **When** the retry state arrives — via the journal replay on reconnect OR the fresh-tab snapshot attach (MAJ-108) — **Then** that tab shows the remaining ~20 s (from `retry_at` via the `serverNow` estimate), not 2:00 (C-11). **Clock-skew variant:** with the client clock deliberately 30 s fast, the shown remaining time is still ~20 s (±2 s) — the countdown derives from `sent_at`, never from the client clock (A-11).
9. **Given** a chain where honored retry-after waits accumulate past 10 minutes (D14/C-9), **When** a scheduled wait would exceed the per-turn cap, **Then** the candidate's remaining retries are skipped and the turn ends with the terminal `rate_limited` line (A-10) — no candidate is left waiting past the cap, and the terminal line names the last candidate that actually failed (C-16).

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
3. **Given** the blunt drop-detail commit on the security branch (PM-7), **When** landing order is decided, **Then** the FQ-8 working assumption holds: `c13c4d279` stays on the security branch until this feature lands, then this design replaces it (§16 — MAJ-013). **(Process item outside BDD — MIN-105: this is a landing-order decision, not a runtime behaviour; it is covered by the §16 decision record, not by a scenario.)**

### US-5 — Verbose facts + raw detail (P1; PM-6, D1, D5)

1. **Given** Verbose chat on and a live error, **When** the viewer opens the "Technical details" disclosure, **Then** the facts lines (provider, model, request id) and the raw provider detail from the same frame render — no fetch occurs (D1).
2. **Given** a registered credential value in the raw detail, **When** the detail preview is built, **Then** the stored value is scrubbed (existing `buildDetail` behaviour, unchanged) — **existing test, unchanged: `pkg/agent/task_run_error_leak_test.go::TestTaskRun_GatewayLogScrubsRegisteredCredential`** (MIN-105).
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

### US-8 — A retired model says so (P1; PM-1 fourth example, D9/D11/D12)

**Narrative.** A provider that no longer offers the configured model says so plainly — the line names the provider and points at the config fix ("Pick a new model in the agent's settings.", D11) — and the turn then falls back to the configured Fallback model if one is set (D12); a generic 404 (access-denied, not-pulled) does **not** get the retirement line.

**Why this priority:** P1 — real but rarer than rate-limit or billing; added in the round-1 fix round by founder decision D9, wording fixed by D11 and fallback added by D12 (round-2).

**Independent test:** inject a 404 with an explicit retirement phrase; assert code `model_retired`, the exact D11 sentence, `retryable` false, attribution `config`, and that the chain falls back to the Fallback model (D12); inject the OpenAI access-denied 404 and the Ollama not-pulled 404 and assert they stay `unknown`.

1. **Given** a provider 404 matching the narrowed C-24 triggers (explicit retirement phrase, or `model_not_found` **combined with** a retirement phrase), **When** classified, **Then** the code is `model_retired`, `retryable` is false, attribution `config`, and the line is exactly "This model is no longer offered by {provider}. Pick a new model in the agent's settings." (D11).
2. **Given** a generic 404 with no retirement signal, **When** classified, **Then** the code stays `unknown` (residual path) — the media strip-retry gate is untouched (MR-2/MR-3).
3. **Given** a retired primary and a configured Fallback model, **When** the chain processes the retired primary's 404, **Then** routing maps 404 → `FailoverUnknown` (retriable), the chain marks the primary failed and answers from the Fallback model, and the fallback note shows (D12); **When** no fallback is configured, **Then** the turn ends with the `model_retired` line (D12's end-of-turn rule).

## 10. BDD scenarios (spec-derived oracles)

Oracles come from this spec: exact template sentences (§6), constraint IDs (§4), frame field lists (§7.1), and the copy-rule tests that already exist. Scenario IDs use set prefixes (A, B, AU, DG, FB, RG, MR) that do not collide with constraint IDs (C-N) or datasets (D1–D3).

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
  Then the visible countdown decrements (computed from retry_at − serverNow, mm:ss per C-15)
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
  Then that tab's countdown shows ~20 seconds remaining (retry_at − serverNow), not 2:00
  Traces to: US-1 / 8   [C-11]

Scenario: A-9 — Fresh-tab snapshot attach shows the remaining time (snapshot path, not replay)
  Given a retry wait in progress and a brand-new tab with an empty store attaching
  When the fresh-tab snapshot arrives carrying the provider_retry item
  And the client derives serverNow from the item's sent_at + client-elapsed-since-receipt
  Then the countdown shows the remaining time (retry_at − serverNow), not the original wait
  And the indicator renders from the snapshot item exactly as a live frame would
  Traces to: US-1 / 8   [C-11]

Scenario: A-10 — Per-turn total-wait cap
  Given a chain of 2 candidates whose retry-after waits accumulate past 10 minutes (D14)
  When a scheduled wait would exceed the per-turn cap
  Then the candidate's remaining retries are skipped and the turn ends with the terminal rate_limited line
  And the terminal line names the last candidate that actually failed
  And no cooldown is marked for candidates whose retries were skipped for cap reasons
  Traces to: US-1 / 9   [C-9, D14]

Scenario: A-11 — Clock-skew immunity
  Given a retry frame with retry_at now+120s, sent_at = server time
  And a client whose clock is deliberately 30 seconds fast
  When the countdown renders
  Then the shown remaining time is ~120s (±2s), derived from sent_at-based serverNow — never the client clock
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
    | 400    | body phrase "credit balance is too low" (Anthropic) |
    | 400    | body phrase "credit balance too low"                |

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

Scenario: DG-2 — New frames carry facts only; error frame leaks body only via detail
  Given a provider body containing the unique sentinel string "SENTINEL-BODY-7Q4Z"
  When the hub publishes the error frame and any provider_retry / provider_fallback frames
  Then scan 1: the sentinel appears in no provider_retry / provider_fallback payload,
    and in no error-frame field other than payload.llm_error.detail
  And scan 2: the sentinel appears nowhere in the non-Verbose rendered DOM
  And scan 3 (positive control): the sentinel DOES appear in the error frame's llm_error.detail —
    proving the scan could have seen a leak (no silent weakening)
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

Scenario: FB-2 — The note persists, replays, and sits AFTER the answer
  Given a fallback note persisted this session
  When history replays
  Then the note renders identically from the transcript via the replay carrier
  And the note entry sits AFTER the assistant answer entry (MIN-103), not re-rendered as a duplicate
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
  And the once-per-pair fact is derived from the session transcript (survives a restart, MIN-103)
  And a later iteration of the SAME turn adds no second frame and no second note (MIN-102)
  Traces to: US-6 / 4   [C-18]
```

### Set RG — regressions (US-7)

```gherkin
Scenario: RG-1 — Context-length behaviour untouched
  Given a context-overflow response
  When classified
  Then code, message, and attribution are byte-identical to the current catalogue
  Traces to: US-7 / 1   [C-23]

Scenario: RG-2 — Own-limiter denials never render a provider template (exact oracle)
  Given an in-process SEC-26 own-limiter denial persisted as an error entry
  And the entry carries NO provider_message subtype flag
  When the entry renders (live) and again after session reload (replay)
  Then the rendered text is byte-identical to today's own-limiter copy — the exact own-limiter string
  And never "{provider} is busy…" — the templated path cannot fire without the flag
  Traces to: §13 / 5   [C-14]

Scenario: RG-3 — Other codes keep catalogue copy (flag-gate negative)
  Given an error of any code outside the provider_message-flagged set (e.g. tool_args, turn_timed_out)
  When getLLMErrorDisplay renders it
  Then the bubble shows the generated catalogue copy for that code, not le.message
  Traces to: §13 / 1   [C-14]

Scenario: RG-4 — The seven non-provider retry reasons produce no provider_retry frame
  Given a turn whose retry loop emits each of the seven other EventKindLLMRetry reasons in turn —
    streaming_reset, timeout, context_limit, empty_response, orphan-tool-markup repair,
    truncated tool call, truncation continue
  When the hub tap filters events
  Then no provider_retry frame is published for any of the seven
  And the delegated rate_limit retries (attempt 1..2 of 2) also publish no provider_retry frame
  Traces to: §13 / 6   [ RG-1]
```

### Set MR — model retired (US-8)

```gherkin
Scenario: MR-1 — An explicit retirement signal classifies model_retired
  Given a provider adapter that answers 404 with a body carrying an explicit retirement
    signal (a C-24 phrase — "has been decommissioned" / "no longer supported" /
    "deprecated and removed" — or the pinned code model_not_found combined with one)
    (dataset D2 wording is Inferred — GREEN must pin real provider wording before shipping, D11)
  And a turn with model M on provider P (catalog display name "OpenRouter")
  When the agent loop classifies the response
  Then the code is model_retired, retryable false, attribution config
  And the message is "This model is no longer offered by OpenRouter. Pick a new model in the agent's settings."
  Traces to: US-8 / 1   [C-24, D11, D15]

Scenario: MR-2 — A generic 404 stays unknown; the residual gate is not re-pointed
  Given a provider adapter that answers 404 with a body carrying neither an explicit
    retirement phrase nor a model_not_found code (e.g. today's generic unknown-404 body)
  When the agent loop classifies the response
  Then the code is unknown — the residual path, unchanged
  And the media strip-retry gate (media_downgrade.go::outcomeFallbackEligible) is unaffected
  Traces to: US-8 / 2   [C-24]

Scenario: MR-3 — A media-bearing 404 that echoes the model id stays unknown
  Given a media-bearing 404 whose body echoes the requested model id but carries
    no retirement phrase
  When the agent loop classifies the response
  Then the code is unknown (CodeUnknown), NOT model_retired
  And the media strip-retry gate fires exactly as today
  Traces to: US-8 / 2   [C-24]

Scenario: MR-4 — A retired primary falls back (D12 routing)
  Given a retired primary (404 with retirement phrase) and a configured Fallback model
  When the routing classifier processes the 404
  Then classifyByStatus maps it to FailoverUnknown (retriable per IsRetriable)
  And the chain marks the primary failed and answers from the Fallback model
  And with no fallback configured the turn ends with the model_retired line
  Traces to: US-8 / 3   [D12]
```

## 11. TDD plan

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | header parse: retry-after seconds / future HTTP-date / past HTTP-date / malformed / ms / duplicate / absent / 0 / 3600 | Unit | A-1, A-3, A-4; C-9, C-22 | `common.ProviderError`; dataset D1; `0` asserts == absent (MIN-101) |
| 2 | request-id header candidates; `cf-ray` labelled last-resort | Unit | C-20 | OBS-003 |
| 3 | billing detector vs rate-limit (dataset D2, both classifiers) | Unit | B-1, B-2; C-5, C-6 | user side: 429 path + first-in-4xx-branch placement, never ≥ 500; routing: `billingPatterns` = C-5, checked before the rate-limit patterns (MAJ-106) |
| 4 | quota_billing: retryable false, copy rules hold | Unit | B-1, B-3 | extends existing copy-rule tests |
| 5 | cooldown pin per D2 row: standard curve only; `calculateBillingCooldown` absent | Unit | B-4; C-7 | compile-level absence + curve assertion (D2) |
| 6 | `provider_message` bijection + closed-slot token check + catalogue fallback when facts absent | Unit | §6; C-3, C-4 | generator + Go/TS catalogue tests (OBS-002 shape) |
| 7 | terminal vs live retry lines | Unit | A-5 | |
| 8 | root-path retry loop **inside `FallbackChain.Execute`**: 3 total calls, attempt = call about to be made (2..3), backoff, >120 s skip-to-fallback, fresh per-call budget, waits context-cancellable, zero-stream guard, chain rows (1-candidate / 2-both-429 / 2-one-billing) | Unit | A-1, A-3, A-4, A-5, A-6; C-8, C-9, C-10, D14 | run through the real `FallbackChain.Execute` (MAJ-101); verbatim chain rows are row 23 |
| 9 | wait-sleep context cancellation: Stop ends the wait ≤ 1 s, no further attempt | Unit | A-7; C-12 | `sleepWithContext` pattern, D4 |
| 10 | failing-attempt provider/model sourcing from the ATTEMPT (`FailoverError.Provider/Model`, the `run` closure — no `HandleErrorResponse` change): primary-skipped → fallback-401 names the fallback; skip-only chain → catalogue fallback | Unit | AU-1, AU-2; C-16, MAJ-110 | dataset D3 rows; adapters without retry-after facts take the backoff schedule |
| 11 | assembly: sentence in `ErrorPayload.Message`; transcript keeps it (trusted-set extension); replay shows it | Integration | DG-7; C-13 | MAJ-001 ii |
| 12 | `getLLMErrorDisplay` allow-list: templated codes render `le.message`; every other code keeps catalogue copy | Unit (vitest) | RG-3; C-14 | MAJ-001 iii |
| 13 | channel reply text via the facts-aware translator | Integration | DG-8; C-13 | MAJ-001 iv |
| 14 | hubError: facts on frame, `Detail` population unchanged; retry/fallback handlers build named-fields-only frames | Integration | DG-2; C-2 | |
| 15 | sentinel scan, three scans: (1) sentinel in no provider_retry/provider_fallback payload and in no error-frame field except `payload.llm_error.detail`; (2) sentinel nowhere in the non-Verbose DOM; (3) positive control — sentinel DOES appear in `detail`; **mutation check**: forwarding `LLMRetryPayload.Error` turns it red | Integration + E2E | DG-1, DG-2, DG-3; C-1, C-2 | the #711 oracle restated per MAJ-105 (D1 keeps detail on the wire) |
| 16 | fallback note: frame + persisted entry + replay carrier; `unavailable_code`; once-per-pair rate derived from the session transcript; note written AFTER the assistant entry; per-turn scoping (no re-note/re-announce within one turn) | Integration | FB-1…FB-4; C-17, C-18, MIN-102, MIN-103 | MAJ-010; ordering pinned by FB-2 |
| 17 | `ProviderRetryIndicator`: four states, mm:ss from `retry_at − serverNow` clamped 0, "Retrying now…", no success colour, no dismiss; "of {max}" from the frame; same-provider "(model)" qualifier; live region announces once per attempt start + once terminal; 1 s tick does NOT change live-region text; mid-wait attach renders remaining time | Component (vitest) | A-2, A-8, A-9, A-11; C-11, C-15, MIN-104 | MAJ-016, MAJ-017, MAJ-108 |
| 18 | Verbose disclosure: facts + raw detail, no fetch; inert rendering (`<img src=x onerror=…>` renders as text); JSON pretty-print; Verbose-off → disclosure absent | Component | DG-4, DG-5, DG-6, DG-1; C-21 | MAJ-018 |
| 19 | design-system publication for `ProviderRetryIndicator`: manifest entry, Story, lock-script run (blocking `design-system` CI gate) | Build gate | MIN-010 | new component — publication contract required |
| 20 | E2E: injected 429 (`retry-after: 3` for suite speed; 120 s formatting proven at component level) → countdown → terminal line; sentinel scan over the whole WS log | E2E | A-1, A-5, DG-1, DG-2 | Playwright, mock provider; MIN-005 applied |
| 21 | `model_retired` detector true-positive: each C-24 retirement phrase, and `model_not_found` + phrase, → `model_retired`, `retryable` false, attribution `config`, exact D11 sentence | Unit | MR-1; C-24, D11, D15 | dataset D2 model-retired rows (Inferred — GREEN pins real provider wording before shipping, D11) |
| 22 | `model_retired` near-miss: generic 404, the OpenAI access-denied 404, the Ollama not-pulled 404, media-404 echoing the model id (MR-3), and non-404 + retirement prose never classify `model_retired`; the residual `CodeUnknown` verdict is byte-identical | Unit | MR-2, MR-3; C-24, MAJ-109 | media strip-retry gate regression (§2 load-bearing constraint) |
| 23 | the two MAJ-101 verbatim chain rows, run through the real `FallbackChain.Execute`: (a) 2 candidates, primary 429 `retry-after: 3` then success → the primary answers on call 2, no fallback frame, no cooldown mark on the primary; (b) primary 429 × 3 → exactly one `MarkFailure`, then the fallback | Unit | A-1, A-5; C-8, C-9 | MAJ-101 — the placement proof; a stubbed chain cannot see these |
| 24 | D14 per-turn cap: waits accumulate past 10 minutes → remaining retries skipped, terminal `rate_limited` line, no cooldown marks for cap-skipped candidates | Unit | A-10; C-9, D14 | cap checked before scheduling each wait; waits not truncated |
| 25 | snapshot attach + skew: fresh-tab snapshot carries the provider_retry item with `sent_at`; countdown = remaining time with the client clock 30 s fast (±2 s) | Component (vitest) | A-9, A-11; C-11, MAJ-108 | projection-item snapshot path (`hubKindItem`) |
| 26 | narrowed `model_retired` + D12 fallback: retirement phrases true-positive; `model_not_found`+phrase true-positive; OpenAI access-denied and Ollama not-pulled near-misses stay `unknown`; routing 404 → `FailoverUnknown` → chain falls back; no fallback → `model_retired` end-of-turn | Unit | MR-1…MR-4; C-24, D11, D12 | MAJ-109, MAJ-110 routing side |
| 27 | operator-only membership: `quota_billing`/`model_retired` in `classifyOperatorOnlyTurnError` → task run `LifecycleFailed` + `operator_action_required`; Judge operator-fix reason; chat turn renders the §6 sentence | Unit | SC-10; D15, MAJ-107 | per-consumer rows (task, Judge, chat) |
| 28 | classifier agreement over every D2 row: user-side code and routing reason agree (rate-limit look-alikes → `rate_limited`/`FailoverRateLimit` on both sides; 429+C-5 → billing on both; 400 Anthropic phrase → billing on both) | Unit | B-1, B-2; C-5, MAJ-106 | names the routing classifier as the retry gate |
| 29 | seven-reason negatives: each of `streaming_reset`, `timeout`, `context_limit`, `empty_response`, orphan-tool-markup, truncated tool call, truncation continue emits `EventKindLLMRetry` but produces NO `provider_retry` frame; delegated `rate_limit` (1..2 of 2) also produces none | Unit | RG-4; MAJ-102 | `EventKindProviderRetry` emitted only by the chain retry path |
| 30 | subtype trust gate: entries WITHOUT the `provider_message` flag render catalogue copy even with a message set (own-limiter replay string byte-identical, RG-2 oracle); entries WITH the flag render `le.message` | Unit (vitest) | RG-2, RG-3; C-14, MAJ-104 | replay carrier carries the flag |
| 31 | AU-3: a key-shaped fragment in an auth/billing body appears in neither message nor facts | Unit | AU-3; C-19 | MIN-105 traceability gap closed |
| 32 | RG-1: context-length code/message/attribution byte-identical to catalogue | Unit | RG-1; C-23 | MIN-105 traceability gap closed |
| 33 | fallback-note edges: (a) 3 iterations of one turn → 1 note, no repeated retry line (MIN-102); (b) note AFTER assistant entry in the persisted transcript (FB-2, MIN-103); (c) `{unavailable_model}` names the PRIMARY in a multi-hop chain (MIN-103); (d) once-per-pair survives restart (transcript-derived) | Integration | FB-2, FB-4; C-17, C-18, MIN-102, MIN-103 | |
| 34 | same-provider model switch: `openrouter/model-a` → `openrouter/model-b` renders "OpenRouter (model-b) is busy…" with a fresh attempt counter; different provider renders plain provider name | Unit (vitest) | A-2; MIN-104 | D3 dataset row |

**Existing tests that must keep passing (extended, not replaced):** the copy-rule tests — extended to 25 codes with existing assertions unchanged (MIN-004); replay-strips-detail tests (unchanged — detail stays); media strip-retry gate tests (`CodeUnknown` residual); delegated retry tests; own-limiter indicator tests (MIN-007); `verify-contracts`; `lint-guards`.

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
| over-cap accumulation | honored waits summing past 10 min (D14) | remaining retries skipped; terminal `rate_limited` line (A-10); waits not truncated |
| absent | — | no fact; backoff schedule |

(No `x-ratelimit-reset*` rows — that family is out of parsing scope, C-22.)

**D2 — billing vs rate-limit, plus the `model_retired` classifier rows (traces to B-1, B-2, B-4; C-5, C-6, C-7; MR-1, MR-2; C-24)**

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
| 400 | Anthropic billing verbatim: "Your credit balance is too low to access the Anthropic API" | quota_billing (MAJ-106 — Anthropic's wording is the phrase "credit balance is too low") |
| 404 | retirement phrase "has been decommissioned" (or "no longer supported" / "deprecated and removed"), or `model_not_found` + a phrase — C-24 trigger met | `model_retired` (new, D11) — Inferred (provider wording not verified against a live call in this task); GREEN must pin real provider wording before shipping |
| 404 | "Model gpt-4o-2024-08-06 not found" — names the model, but no retirement phrase (near-miss, MAJ-109) | unknown — residual path; a bare "not found" is NOT a retirement signal |
| 404 | OpenAI access-denied verbatim: "The model `gpt-4o` does not exist or you do not have access to it" (near-miss) | unknown — access-denied must not read as retired (MAJ-109) |
| 404 | Ollama not-pulled verbatim: "model 'llama3' not found, try pulling it first" (near-miss) | unknown — not-pulled must not read as retired (MAJ-109) |
| 404 | media-bearing request whose body echoes the model id, no retirement phrase (MR-3) | unknown — strip-retry gate fires exactly as today |
| 410 | "This model has been decommissioned" — explicit non-match: a non-404 status never classifies `model_retired` | unknown — non-404 statuses stay on existing paths; revisit only when real 410 wording is pinned (C-24); Inferred (same pin-before-ship note) |

Note the C-5 rule these rows pin: billing classifies on **402**, or the structured `insufficient_quota` field, or a C-5 phrase **on a 4xx status only — never on 5xx** (round 2's deleted `500 | "payment required"` row is deliberately gone: `classifyByHTTPStatus` returns `CodeNetwork` for ≥ 500 before any body check, and routing maps 5xx without reading the body). Unrecognised prose gets the catalogue sentence rather than a wrong confident claim (D2's simplify ruling); prose-only quota wording on a 429 stays `rate_limited` (C-6).

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
| SDK-adapter 429 (anthropic / codex — no retry-after fact) | provider named from the ATTEMPT (MAJ-110), wait from the backoff schedule (2 s × 2 capped 30 s) — no invented retry-after |
| same-provider switch: openrouter/model-a → openrouter/model-b, attempt 2 of 3 | OpenRouter (model-b) is busy. Retrying automatically in mm:ss (attempt 2 of 3). (MIN-104) |
| no facts | today's catalogue sentence (byte-identical fallback) |

## 13. Regression impact

The feature **modifies existing behaviour**: error lines for four code families gain provider naming or their own template (rate-limited busy/terminal, `quota_billing`, `provider_auth_failed`, `model_retired`); two new frame types appear; the billing cooldown curve is deleted. Guarded by:

1. All existing codes' copy unchanged except the three template families — copy-rule tests **extended to 25 codes, existing assertions unchanged** (MIN-004).
2. `context_too_long` byte-identical (PM-5, C-23).
3. Residual-4xx → `CodeUnknown` unchanged (media strip-retry).
4. Replay shape unchanged (no facts object on replay; the existing detail-strip unchanged — D1).
5. Own-limiter `rate_limit` frame and indicator untouched; an own-limiter denial never renders a provider template — the trust gate is the `provider_message` subtype flag, which own-limiter entries never carry, so its replay string stays byte-identical (RG-2, MIN-007, MAJ-104).
6. Delegated retry policy unchanged (count/caps); its retries stay UNFORWARDED and unannounced — `EventKindProviderRetry` is a separate kind emitted only by the chain-level path, so the seven other `EventKindLLMRetry` reasons and the delegated `rate_limit` retries produce no `provider_retry` frame (RG-4, MAJ-102).
7. Scrubber behaviour unchanged for logs and for the `detail` preview.
8. **Cooldown regression (CRIT-001's consequence, fixed at the root):** `calculateBillingCooldown` is deleted; the D2 dataset pins the standard curve for every billing-shaped row (B-4).
9. **Verbose-promise regression: none.** Because `detail` stays (D1), the six catalogue sentences that promise Verbose details (`provider_stalled`, `tool_args`, `schema`, `turn_timed_out`, `context_unrecoverable`, `unknown`) keep their Verbose content — the round-0 finding MAJ-005 is moot.
10. **Model-retired classifier addition:** the new detector fires only inside the 404 gate with the narrowed C-24 triggers (explicit retirement phrase, or `model_not_found` + phrase — MAJ-109); a non-404 status and a signal-less 404 (including the OpenAI access-denied and Ollama not-pulled wordings and the MR-3 media-404) keep today's residual `CodeUnknown` verdict byte-identical, so the media strip-retry gate is not re-pointed (MR-2, MR-3; §2 load-bearing constraint). The routing side's new 404 → `FailoverUnknown` mapping changes fallback behaviour for every 404 (D12 — intended, stated in C-24).
11. **Operator-only turn errors (D15, MAJ-107):** `quota_billing` and `model_retired` join `classifyOperatorOnlyTurnError` — a task run stops with `operator_action_required` and the Judge stops dispatching instead of retrying a dead model. Chat turns keep the ordinary classified-error path; the three consumers are pinned by SC-10 and TDD row 27.

## 14. Requirements & success criteria

### Functional requirements

- **FR-001:** The system MUST capture `retry-after` (integer seconds or HTTP-date), `retry-after-ms`, and request-ID headers into the structured provider error at the HTTP boundary; malformed/past values yield no fact; no `x-ratelimit-reset*` parsing exists (C-22).
- **FR-002:** The system MUST classify billing/quota exhaustion as its own code (`quota_billing`), distinct from `rate_limited`, not retryable, in both classifiers, using only the C-5 unambiguous account-state vocabulary.
- **FR-003:** The system MUST assemble one plain-English line per error **at the agent emit sites** from the catalogue + facts (provider/model of the failing attempt, C-16), with catalogue fallback when facts are absent.
- **FR-004:** The system MUST publish error frames carrying code, message, retryable, facts (**`provider`, `model`, `request_id` only — the SPA has no consumer for retry facts on the error frame, OBS-103**) **and `detail` per ADR-051** — and the NEW retry/fallback frames carrying named facts only, never raw error text (C-2).
- **FR-005:** The system MUST automatically retry rate-limited provider calls inside `FallbackChain.Execute` on the root path (3 total calls including the first; `retry-after` honored only ≤ 120 s; otherwise backoff; a wait above the ceiling skips the candidate's remaining retries and moves to fallback, or ends the turn when none is configured; zero-streamed-bytes guard; the honored waits of one turn are **capped at 10 minutes total — on cap, the turn ends with the terminal line even if a candidate is untried, D14/C-9**) and publish a retry frame per attempt via `EventKindProviderRetry` (C-8/C-9/C-10, MAJ-101/MAJ-102).
- **FR-006:** The system MUST carry `retry_at` AND `sent_at` (RFC 3339) on every retry frame, and the indicator MUST render `retry_at − serverNow` clamped at 0, where `serverNow = max over received frames of (sent_at + client-elapsed-since-receipt)` — never the client wall clock (C-11, MAJ-108).
- **FR-007:** The assembled sentence MUST reach all four surfaces — live bubble, persisted transcript, replay, channel reply — for the templated codes (C-13), and the SPA MUST render `le.message` for those codes only (C-14).
- **FR-008:** The system MUST emit and persist a fallback note naming the Fallback model and the unavailable model (D7 naming), with a replay carrier in the contract wave, covering cooldown-skip-produced fallbacks at the once-per-pair rate (C-17/C-18).
- **FR-009:** The system MUST NOT render provider body text outside a Verbose-gated disclosure, and MUST NOT name key labels, key material, or fragments in any assembled message or facts field (C-1/C-19). The Verbose raw view shows the provider body as-is per D5 — no masking, fragment risk accepted by the founder.
- **FR-010:** The Verbose disclosure MUST render `detail` as inert text (never HTML, never markdown), pretty-printed only when it parses as JSON (C-21).
- **FR-011:** The system MUST NOT render any billing button or billing link anywhere (D6).
- **FR-012:** The system MUST NOT apply any billing-specific cooldown or lockout; the billing cooldown function is deleted (D2, C-7).
- **FR-013:** The system MUST keep context-length classification, copy, and behaviour unchanged (C-23).
- **FR-014:** The system MUST keep the scrubber in place for logs and for the `detail` preview, unchanged.
- **FR-015:** Contract changes MUST land first (Constraint #8): schemas → regenerate → commit atomically → consumers.
- **FR-017:** `quota_billing` and `model_retired` MUST join `classifyOperatorOnlyTurnError` (D15): task runs stop at once with lifecycle `failed` + reason `operator_action_required`, the Judge stops dispatching with the operator-fix reason, chat turns render the §6 sentence; SDK-internal retries are disabled on the two SDK transports (D13, `option.WithMaxRetries(0)`).
- **FR-016:** The system MUST classify a provider's retirement of the requested model during a real call as its own code `model_retired` (D9, triggers narrowed by MAJ-109) — HTTP 404 **plus** an explicit retirement phrase ("has been decommissioned", "no longer supported", "deprecated and removed") or the pinned code `model_not_found` **combined with** such a phrase, and nothing else — distinct from `model_unavailable` (the mid-session switch-failure code; never shared or re-used), not retryable, attributed `config` (D15), rendered with the §6 D11 template, reaching all four surfaces per FR-007/C-13; **routing maps the 404 to `FailoverUnknown` (retriable) so a retired primary falls back to the configured Fallback model (D12), ending the turn with the `model_retired` line when none is configured**.

### Success criteria

- **SC-1:** On an injected 429, the chat shows the PM-1 countdown line within one frame round-trip, and the countdown is derived from `retry_at` (a mid-wait attach shows the remaining time). E2E uses `retry-after: 3` for suite speed; the 120 s formatting is proven at component level (MIN-005). (Verified = Playwright e2e + vitest.)
- **SC-2:** The #711 oracle as three scans (MAJ-105): (1) the sentinel appears in no `provider_retry`/`provider_fallback` payload and in no error-frame field except `payload.llm_error.detail`; (2) the sentinel appears nowhere in the non-Verbose-rendered DOM; (3) positive control — the sentinel DOES appear in `detail`, proving the scan could see a leak. The mutation check (forward `LLMRetryPayload.Error`) is demonstrated to turn scan 1 red.
- **SC-3:** All dataset D2 rows produce the expected code AND the pinned standard cooldown — 100%; **both classifiers agree on every row** (user-side code vs routing reason, MAJ-106; the routing classifier gates retry).
- **SC-4:** A Verbose viewer sees facts + the raw detail (inert-rendered) on a live error with no fetch; a non-Verbose viewer sees neither; a replayed error mounts no disclosure and fetches nothing. All verified in Playwright/vitest.
- **SC-5:** All dataset D1 parsing rows produce the expected fact or absence.
- **SC-6:** `verify-contracts` and the copy-rule tests are green with `quota_billing`, `model_retired` + the templated variants present (bijection holds, 25 codes).
- **SC-7:** Stop during a retry wait ends the turn within 1 s with no further attempt — unit-proven on the sleep's context cancellation and observed once end-to-end.
- **SC-8:** The fallback note appears live and on replay, and the once-per-(unavailable, answered)-pair rate holds under repeated turns.
- **SC-9:** The SPA renders server message text only on entries carrying the `provider_message` subtype flag; every non-flagged entry — the own-limiter denial included — renders byte-identical catalogue copy live and on replay (RG-2 oracle, MAJ-104).
- **SC-10:** On `quota_billing`/`model_retired`, a task run records lifecycle `failed` with reason `operator_action_required`, the Judge records the operator-fix reason and stops dispatching, and a chat turn renders the §6 sentence — per consumer, D15/MAJ-107.

## 15. Traceability matrix

| FR | US | BDD scenario | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | A-1, A-3, A-4 | 1 |
| FR-002 | US-2 | B-1, B-2 | 3, 4, 28 |
| FR-003 | US-1, US-2, US-3 | A-1, B-1, AU-1, AU-2, DG-7, DG-8 | 6, 11; dataset D3 |
| FR-004 | US-4, US-5 | DG-2, DG-4 | 14 |
| FR-005 | US-1 | A-1…A-7, A-10 | 8, 9, 23, 24 |
| FR-006 | US-1 | A-2, A-8, A-9, A-11 | 17, 25 |
| FR-007 | US-1, US-2, US-3 | DG-7, DG-8, RG-2, RG-3 | 11, 12, 13, 30 |
| FR-008 | US-6 | FB-1…FB-4 | 16 |
| FR-009 | US-3, US-4, US-5 | AU-3, DG-1, DG-2, DG-3, DG-6 | 15, 31 |
| FR-010 | US-5 | DG-4, DG-5, DG-6 | 18 |
| FR-011 | US-2 | B-3 | 4 |
| FR-012 | US-2 | B-4 | 5 |
| FR-013 | US-7 | RG-1 | 32; copy-rule + regression suite |
| FR-014 | §13 | AU-3, RG-2 | existing scrubber tests |
| FR-015 | all | (process) | `verify-contracts`, CI |
| FR-016 | US-8 | MR-1, MR-2, MR-3, MR-4 | 21, 22, 26; dataset D2 model-retired rows |
| FR-017 | §13 (consumers) | SC-10 | 27 |

Every FR appears; every scenario (A-1…A-11, B-1…B-4, AU-1…AU-3, DG-1…DG-8, FB-1…FB-4, RG-1…RG-4, MR-1…MR-4) traces to ≥ 1 FR; every TDD row cites only IDs defined in §4, §9, §10, or §12. Two acceptance scenarios are deliberately outside the BDD/TDD nets (MIN-105): US-5/2 is covered by an existing named test (`pkg/agent/task_run_error_leak_test.go::TestTaskRun_GatewayLogScrubsRegisteredCredential`), and US-4/3 is a landing-order process item recorded in §16, not a runtime behaviour.

## 16. Questions for the founder

**Only these remain open.** Everything else that round 0/round 1 raised is settled by the recorded founder decisions and appears in this spec as fact, not as a question.

- **FQ-8 — landing order with the security branch (MAJ-013):** working assumption, pending confirmation: keep `c13c4d279` ("error frame detail stays off the wire") on `feature/gateway-security-fixes` until this feature lands; then this design replaces it — the leak is never reopened on `main`. Team-lead is defaulting to this absent a correction.
- **Resume signal for stopped/failed turns — PROPOSAL, awaiting founder (§16.1 below).** Not decided; does not gate this approval.

**Settled — recorded as decisions, no longer asked:** Q1/Q2 (delivery + `detail`) → D1; Q3 (retry numbers) → D3 (**3 attempts, 2-minute ceiling — CONFIRMED, not asked**); Q5 (billing URL) → D6; Q6 (note persistence) → D7; FQ-1 → D2; FQ-2 → D5; FQ-3 → D3 (the 2-minute figure is the ceiling); FQ-4 → D3 (retry-then-fallback); FQ-5 → D1 (ADR-051 stands — no amendment needed, MAJ-014 moot); FQ-6 → D1 (`detail` kept, so the six Verbose-promise sentences keep their content — MAJ-005 moot); FQ-9 → D6; FQ-10 → D4; FQ-7 → D9 (`model_retired`, added in the round-1 fix round); **Q4 → D15** (`quota_billing` attribution is `config`, final — the placeholder pending Q4 is retired everywhere in this spec); **FQ-101 → D11** (option B — "Pick a new model in the agent's settings."); **FQ-102 → D12** (option A — a retired primary falls back; 404 → `FailoverUnknown`); **FQ-103 → D13** (option A — SDK-internal retries disabled on both SDK transports); **FQ-104 → D14** (option A, at the founder's 10-minute figure — per-turn total-wait cap).

### 16.1 Resume-signal proposal (NOT decided — awaiting founder; explicitly outside this fix round's landed scope)

**Context.** With D15, a task or Judge turn that hits `quota_billing` or `model_retired` stops at once (`operator_action_required`). Once the operator fixes the key/quota/model, something must signal the run to continue. Verified today: `finishRunTurn` marks lifecycle `failed` + `operator_action_required`; the Board renders the failed state (`src/components/workspaces/taskStatusConfig.ts`) and offers a manual "Restart — re-runs from scratch" action (`TaskActionButton.tsx`); the Judge's attempt record carries the operator-fix reason (`judgeDispatchNeedsOperator`); **no proactive push/notification mechanism exists** (grep-verified — nothing notifies when conditions change).

**Option A (recommended) — passive, the next trigger just tries again.** No new mechanism: the run stays stopped until its next scheduled trigger (calendar recurrence, heartbeat) or a manual run; since the operator already fixed the cause, that call succeeds, and success IS the resume signal. Additive change only: the failed run's record keeps the `operator_action_required` reason so the Board explains why it stopped. Zero new code paths, nothing to notify, nothing to leak; the cost is latency (waits for the next trigger) and a manual restart re-runs from scratch rather than mid-run.

**Option B — visible stopped state with one-click resume (UI-only).** Board row keeps `failed`/`operator_action_required` + a "Resume" button that re-dispatches the run without re-running from scratch. Clear UX, but the mid-run resume path (which step to continue from) does not exist today and is a real design surface (state checkpointing); bigger than it looks.

**Option C — active notification when a later call succeeds.** The system keeps probing (e.g. a cheap model ping) and pushes once the account/model works. Fastest operator feedback, but a probe is a background LLM call against the operator's money, needs a new probe component and a notification channel that does not exist today, and can false-positive (probe passes, real call still fails).

**Recommendation: A**, with B as a later UI-only follow-up if the founder wants a visible Resume control; C is not worth its footprint. Decision explicitly deferred — nothing in §§1–18 implements any of the three.

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
| `model_unavailable` already exists as the mid-session switch-failure code (D9's distinctness claim) | Read `pkg/agent/translate_error.go::CodeModelUnavailable` ("the caller asked for a different model and the switch failed. The turn continues on the previous model"); `contracts/components/schemas/LLMError.yaml` `model_unavailable` entry (attribution `config`) | Verified |
| `model_retired` name collision-free | `grep -rn "model_retired" pkg/ src/ contracts/` → exit 1, zero matches (this task, @ `2bf53c988`) | Verified |
| `ProviderError` captures no response headers today | Read `pkg/providers/common/common.go::HandleErrorResponse`, `WrapHTMLResponseError` (only `Content-Type` read; 8 KiB cap, 512 B preview) | Verified |
| "quota exceeded" → `CodeRateLimited` today; no billing code user-side | Read `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus` (round-0 task) | Verified |
| Providers-side: 429 → `FailoverRateLimit` before body check; rate-limit patterns before billing | Read `pkg/providers/error_classifier.go` (round-0 task) | Verified |
| Retry events exist but never reach the SPA; payload carries raw error string | Read `pkg/agent/events.go::LLMRetryPayload`; `pkg/gateway/websocket_forward.go` (not-forwarded list); `pkg/agent/loop_provider_retry.go::callProvider` | Verified |
| Verbose chat is client-only | Read `src/store/chatPreferences.ts` (zustand persist; "does not cross the gateway/API boundary") | Verified |
| buildDetail scrubs before preview cut | Read `pkg/agent/translate_error.go::buildDetail` (round-0 task) | Verified |
| GitNexus unavailable in this session | Tool list has no gitnexus tools; exploration done by Read/Grep (shared rule 9) | Verified |
| Impact rows | Grep sweep for callers (choke points, retry gates, catalogues, trusted-stage set) | Inferred (graph not available) |
| Gemini/Codex/OpenAI provider wordings in D2 | Known provider behaviour (review CRIT-001, Inferred high confidence); pinned as dataset rows so CI proves the classifier against them | Inferred (not probed live) |
| **Self-check (round-1 fix)** | Re-read the finished spec against the dispatch's done-criteria and cross-checked every ID: all 23 constraints (C-1…C-23) defined once in §4 and cited only where defined; 15 FRs; 30 numbered BDD scenarios across six sets; every TDD row and every matrix cell cites existing IDs (scripted ID cross-check run, exit 0); every D1–D8 decision applied (D1→§5 rewrite, D2→§7.3+dataset, D3→§7.4 precise, D4→A-7+row 9, D5→C-19/FR-009 reworded, D6→§7.6/button/C-10-old/FR-007-old/Q5 removed, D7→§7.4 note+carrier+naming, D8→untouched parallel track); every non-moot review finding fixed or explicitly in §16 (MAJ-001,2,3,4,6,7,8,10,13,15,16,17,18 + CRIT-001-half, MIN-001,3,4,5,6-rewritten,7,10, OBS-002,003 + all structural gaps); moot findings declared with reasons (CRIT-002→D5, MAJ-005, MAJ-009→MIN-008, MAJ-012, MAJ-014, MIN-002, MIN-008, MIN-009, OBS-001); §16 holds only FQ-7/Q4/FQ-8; commit scoped to the spec file, author verified human, no co-author trailer; pushed and re-verified | Verified |
| **Self-check (D9 follow-up, 2026-09-26)** | Re-read the spec after the FQ-7/D9 fix against this dispatch's done-criteria: C-24 is the only new constraint ID (no existing C/FR/US/BDD/TDD/SC number was renumbered); FR-016 and US-8 added; Set MR added (MR-1, MR-2) with the §10 preamble and §15 enumeration updated; TDD rows 21-22 added; D2 title extended and 3 rows added (true positive, near-miss, explicit non-match), each flagged Inferred with the pin-before-ship note; the "24 codes" count updated to 25 in the §11 note, §13 item 1 and SC-6; §7.1 item 1 and §7.3 gained the `model_retired` work; D9 recorded in the founder-decisions section; FQ-7 removed from both open lists (founder-decisions section and §16) — only Q4/FQ-8 remain; the residual-4xx `CodeUnknown` media strip-retry gate is cited as NOT re-pointed in C-24, §7.3, §13 item 10 and MR-2; `model_retired` never shares `model_unavailable`'s meaning (collision-free by grep, exit 1); D1–D8 text and all other prior fix-round content untouched (git diff reviewed hunk by hunk); commit scoped to the spec file only | Verified |
| Retry loop placement facts (429 lands FailoverRateLimit; IsRetriable true; MarkFailure runs immediately; candidateBudget fair-split with 5 s floor; defaultPerCandidateTimeout 120 s) | Read pkg/providers/fallback.go::FallbackChain.Execute (this task) | Verified |
| EventKindLLMRetry has eight emitters and seven reasons; only callProvider's reason is a provider rate limit | Read loop_run_turn_response.go, loop_run_turn_iterations.go, loop_truncation.go, loop_provider_retry.go emit sites (this task) | Verified |
| Both SDK transports default to SDK-internal retries (MaxRetries: 2); repo clients set no retry option | Read anthropic-sdk-go@v1.48.0 requestconfig.go and openai-go/v3@v3.39.0 requestconfig.go (MaxRetries: 2 in both); pkg/providers/anthropic/provider.go and pkg/providers/codex_provider.go client constructions (only auth/base-URL options) | Verified |
| classifyOperatorOnlyTurnError has exactly two consumers: task finishRunTurn to LifecycleFailed + operator_action_required; Judge judgeDispatchNeedsOperator to the operator-fix reason | Read pkg/agent/operator_only_turn_error.go header and call sites | Verified |
| Board failed state and manual restart exist; no proactive push mechanism exists | Read src/components/workspaces/taskStatusConfig.ts (failed status) and TaskActionButton.tsx (restart re-runs from scratch); grep for push/notify mechanisms over src/ and pkg/gateway/ returned nothing | Verified |
| **Self-check (round-2 final fix, 2026-09-26)** | Re-read the finished spec against the dispatch done-criteria and scripted checks (all exit 0): D10-D15 all applied (D10 flat demo note; D11 wording in §6/US-8/MR-1 and the two historical D9 quotes marked superseded; D12 in C-24/§7.3 routing/US-8/3/MR-4/§4; D13 audit block in the decision record + §7.2 work item; D14 cap in C-9/§4/§7.4/FR-005/A-10/TDD 24; D15 in §6/§7.1/§7.3/FR-017/SC-10/TDD 27); all 10 MAJOR / 6 MINOR / 3 OBSERVATION round-2 findings fixed per recommendation, none silently dropped (MIN-106 resolved as moot via D15, stated); 38 BDD scenarios, zero duplicate IDs, all Traces-to refs resolve (scripted); fences balanced at 14; TDD rows 1-34 contiguous (scripted); the two verbatim MAJ-101 chain rows present as TDD row 23; resume-signal proposal in §16.1 with three code-grounded options + recommendation A, marked awaiting-founder and outside the landed scope; Status line reads Approved; both review files untouched; diff touched only the spec file; commit authored as the human (no-reply email verified via gh api), zero anthropic trailers on the new commit (branch-range hits are pre-ruling 2026-09-21 commits the founder ruled stay); pushed to feat/provider-messages @ b3a344a52 | Verified |

*Skills: omnipus-shared-rules, plan-spec.*

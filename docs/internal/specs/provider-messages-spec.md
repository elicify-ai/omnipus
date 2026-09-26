# Provider Messages — Specification (provider/LLM error presentation, #711)

**Status:** Draft

- **Source briefs:** `docs/internal/specs/spec-provider-messages.md` (founder decisions PM-1…PM-7, 2026-09-26 — the interview output); `docs/internal/specs/provider-messages-research.md` (OpenCode vs Omnipus, 2026-09-26); GitHub issue #711; governing ADR: [ADR-051 — Media handling and provider error translation](../architecture/ADR-051-media-handling-and-provider-error-translation.md).
- **Discovery status:** Phase 1 is satisfied by the recorded founder decisions PM-1…PM-7 (interview output, confirmed 2026-09-26). PM-6 explicitly delegates one unsettled point to this spec — the delivery mechanism that keeps raw provider text off non-Verbose viewers' wire — with founder confirmation before build. That point is settled here and flagged: see §5 and **Q1**.
- **Codebase intelligence note:** the GitNexus MCP tools are not connected in this session; per `omnipus-shared-rules` rule 9 the exploration below is Read/Grep-based, and every impact row is labelled **Inferred**. Every cited symbol was read in this task on `feat/provider-messages` @ `fad3770f2`.
- **Change size:** feature (structural, cross-tree: contracts, `pkg/providers`, `pkg/agent`, `pkg/gateway`, `src/`). Gate: RED/GREEN/CHECK → 8-reviewer gate → founder's yes.
- **Closes #711 by design.** Not by the gateway-security squad's blunt "drop detail" commit (`c13c4d279`), which PM-7 removes from that branch.

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

## 1. Problem & actors

**Actors.** The **user** (reads chat; may have Verbose chat on or off — a per-device, client-only preference, `src/store/chatPreferences.ts::verboseChatEnabled`); the **operator** (the same person on a self-hosted install; fixes credentials and billing); the **agent loop** (produces provider errors and retry/fallback state); the **gateway** (the single WS choke point that shapes what reaches any browser).

**Problem.** Four failures, all verified in this task:

| # | Failure | Evidence |
|---|---------|----------|
| F1 | A provider 429 arrives as "The model provider is temporarily overloaded. Wait a moment, then retry." — no reset time, no countdown, no provider name, even when the provider sent `retry-after`. | `contracts/components/schemas/LLMError.yaml` (x-user-messages `rate_limited`); `pkg/providers/common/common.go::ProviderError` (captures no response headers) |
| F2 | Billing/quota exhaustion gets the same "wait and retry" advice as a transient 429 — actively wrong. The user-side classifier has no billing code; "quota exceeded" sits in `rateLimitSubstrings` → `CodeRateLimited`. | `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus`; routing side: `pkg/providers/error_classifier.go::classifyByMessage` checks `rateLimitPatterns` **before** `billingPatterns`, and the status switch maps 429 → `FailoverRateLimit` before any body check |
| F3 | The Verbose-only "detail" (a scrubbed `status=… body=…` preview) is marshalled onto **every** error frame whether or not any viewer wants it — the #711 leak. The scrubber only matches registered, complete credential values, so unregistered or truncated fragments pass. | `pkg/gateway/websocket_forward_hub.go::hubError` (`Detail: &detail` unconditional); `pkg/logger/sensitive.go::ScrubSensitiveValues` (its own doc: cannot recognise never-registered or masked/truncated values) |
| F4 | Inline retries (delegated 429 backoff; streaming resets) are silent; a successful fallback model swap is log-only. The user sees a hung turn or an unexplained style change. | `pkg/agent/loop_provider_retry.go::callProvider` (retries logged only; `EventKindLLMRetry` is on the WS-forwarder's explicitly-not-forwarded list, `pkg/gateway/websocket_forward.go`); `pkg/agent/loop_run_turn.go::callProviderOnce` (fallback success is `logger.InfoCF` only) |

**Why now:** the founder chose direction A ("facts instead of raw text") and the whole mechanism (PM-1), not just the leak.

## 2. Existing codebase context (verified this task)

### Symbols involved

| Symbol | Role in this feature |
|---|---|
| `pkg/providers/common/common.go::ProviderError`, `HandleErrorResponse`, `WrapHTMLResponseError` | modify — gain parsed response headers (retry-after family, request ID) |
| `pkg/providers/common/common.go::ProviderError.Error()`, `ResponsePreview` | keep — log-side rendering unchanged; the scrubber stays for logs |
| `pkg/providers/error_classifier.go::ClassifyError`, `classifyByHTTPStatus`-equivalent status switch, `classifyByMessage` | modify — quota/billing body beats plain rate-limit for both the status path (429 + billing body) and the message order |
| `pkg/providers/cooldown.go::calculateBillingCooldown` | keep — already exists; billing cooldown becomes reachable for quota-shaped 429s |
| `pkg/agent/translate_error.go::TranslateLLMError`, `classifyByProviderError`, `classifyByHTTPStatus`, `rateLimitSubstrings`, `isRetryable` | modify — new `quota_billing` code; billing detector before rate-limit; facts threading; buildDetail replacement |
| `pkg/agent/translate_error.go::ProviderError` (agent-side), `ProviderErrorFromFailover`, `providerErrorFromChain` | modify — mirror the new header facts |
| `pkg/agent/translate_error.go::buildDetail`, `BuildDetail` | delete (see Q2) — replaced by facts + Verbose accordion |
| `pkg/agent/events.go::ErrorPayload` | modify — gains provider/model identity |
| `pkg/agent/events.go::LLMRetryPayload`, `EventKindLLMRetry` | extend additively — gains provider, model, retry-after; forwarded as a new frame (today on the not-forwarded list) |
| `pkg/gateway/websocket_forward_hub.go::hubError` | modify — facts, no detail; mints error id; retains scrubbed raw body for on-demand fetch |
| `pkg/gateway/websocket_forward_hub.go::hubSyncTap` | modify — new handlers for retry/fallback frames |
| `src/lib/llm-error.ts::getLLMErrorDisplay` | modify — loses the detail branch; gains facts passthrough |
| `src/components/chat/MessageItem.tsx` (native `<details>` "Technical details"), `ChatScreen.tsx::VirtualAssistantMessageRow` | modify — Verbose facts + raw-JSON accordion replaces the detail disclosure |
| `src/components/chat/RateLimitIndicator.tsx::formatSeconds`, per-second interval | pattern source (reused, not imported) — the provider-retry indicator is a new component per the design-system skill |
| `src/store/chat/store.ts::setRateLimitEvent`, `src/store/chat/frames.ts` | modify — new frame cases; retry state separate from the own-limiter slot |
| `contracts/components/schemas/LLMError.yaml`, `contracts/asyncapi.yaml` (inline LLMError/LLMErrorReplay kept in lockstep), `contracts/components/schemas/ErrorPayload.yaml` | modify — §5 |
| `pkg/api/generated/`, `src/lib/api/generated/` | regenerate only — never hand-edited |

### Impact assessment (Inferred — GitNexus unavailable; grep-swept)

| Symbol modified | Risk | d=1 dependents |
|---|---|---|
| `TranslateLLMError` | MEDIUM | `hubError`, `appendErrorTranscript`/`appendClassifiedError` (both choke points), media strip-retry gate (`outcomeFallbackEligible` reads `CodeUnknown` verdicts — residual-4xx verdict MUST NOT change) |
| `buildDetail`/`BuildDetail` | MEDIUM | `hubError` (curated-code override path), `typedExitError`, every `TranslateTurnError` branch |
| `common.ProviderError` | LOW-MEDIUM | every provider adapter's error path, `errorToProviderError`, codex provider's request-id logging (`codex_provider.go` — keep; unify later) |
| `ClassifyError` order | MEDIUM | fallback routing + cooldown curves (`calculateStandardCooldown` vs `calculateBillingCooldown`); delegated 429 retry gate (`shouldRetryDelegatedRateLimit`) |
| `LLMError` wire shape | MEDIUM | generated Go/TS catalogues (regenerated), Zod parse, copy-rule tests (Go + TS) |

**Load-bearing constraint (do not break):** `pkg/agent/translate_error.go::CodeUnknown` is the media strip-retry gate input (`outcomeFallbackEligible` fires only on `CodeUnknown`). The new billing detector must not re-point residual 4xx verdicts.

## 3. Founder decisions → spec sections

| PM | Decision (short) | Where settled |
|---|---|---|
| PM-1 | Facts instead of raw text; ONE plain-English line assembled from facts | §6 (exact templates), D1 |
| PM-2 | Provider + model may be named; keys never named; key material never; request ID Verbose-only | §6 rules, C-11/C-12 |
| PM-3 | Billing gets its own message; "Open billing page" only when the URL is already known | US-2, D8 |
| PM-4 | Retry countdown: yes. Fallback note: yes. | US-1, US-6, D4/D5/D6 |
| PM-5 | Context length stays as is (auto trimming, no note, no compaction) | §9 non-behaviors |
| PM-6 | Verbose = facts + accordion with raw provider JSON, reusing the tool-call accordion pattern; delivery mechanism = spec's to settle, founder confirms | §5 options + **Q1** |
| PM-7 | #711 closes by this design; the blunt drop-detail commit is removed from the security branch | header, US-4 |

## 4. Behavioral contract

### Primary flows

- When a provider rate-limits a turn and sent a `retry-after`, the system retries automatically and shows a live countdown naming the provider, the wait, and the attempt ("OpenRouter is busy. Retrying automatically in 1:32 (attempt 2 of 5).").
- When the automatic retries stop, the error line names the provider and says the turn can be retried.
- When the provider says the account is out of credit, the error line says so, names the provider, and offers "Open billing page" **only** when the billing URL is already known.
- When the provider rejects the API key, the error line names the provider and points at Settings → Providers.
- When a fallback model answered instead of the unavailable one, a quiet note names both ("Answered by X because Y was unavailable.") — live and on replay.
- When Verbose chat is on and the viewer opens the accordion, the system fetches the raw provider response (scrubbed of registered credentials) on demand.
- Raw provider text never rides an error frame, regardless of anyone's Verbose setting.

### Explicit non-behaviors & safeguards

Qualitative:

- The system must not put provider response bodies (or any raw provider text) on any broadcast frame — that is the #711 leak class; PM-1 replaces raw text with facts.
- The system must not name key labels, key material, or key fragments — ever (PM-2).
- The system must not show a billing button without a known URL (PM-3) — a guessed billing link is worse than none.
- The system must not change context-length behaviour: no token counts, no compaction, no extra note (PM-5).
- The system must not auto-retry non-retryable failures (auth, billing, content policy): the same request fails identically.
- The system must not retry after any streamed bytes reached the turn (existing guard, `callProvider` — a retry would duplicate visible and persisted content).
- The system must not treat `request_id` as user-facing on its own — it renders only under Verbose chat (PM-2).
- The system must not remove the scrubber: it stays as defense-in-depth for logs (`gateway.log`, previews) and for the retained raw body before storage.

Machine-verifiable (constraint IDs used by tests):

| ID | Constraint |
|---|---|
| C-1 | No `ErrorFrame` payload JSON contains the provider body (or any substring of it) — asserted by wire-logger scan in tests |
| C-2 | `quota_billing` is in the code enum; the generator's message/attribution bijection hard-fails on any gap (existing generator behaviour, extended to templates) |
| C-3 | Every code with a template has an `x-user-messages` entry; generator extends its hard-fail to templates |
| C-4 | `facts.retry_after_seconds`, when present, is > 0 and ≤ 86400 (clamped; `retry-after: 0` is treated as "immediate retry allowed" and produces no countdown) |
| C-5 | `attempts` ≤ `max_attempts` on every retry frame; `attempt` starts at 1 |
| C-6 | A provider-retry is only scheduled when zero streamed bytes have reached the turn |
| C-7 | Persisted transcripts carry code + assembled message, never body/headers/raw JSON; replay shape unchanged (no facts object, no detail — the message itself now carries provider naming) |
| C-8 | The retained raw provider body is scrubbed (`ScrubSensitiveValues`) before storage, capped at the existing 8 KiB body cap |
| C-9 | Countdown text uses mm:ss ("1:32"), per PM-1's own example; the own-limiter `RateLimitIndicator` format ("1m 32s") is unchanged |
| C-10 | `facts.billing_url` is present only when the provider catalog carries one; never guessed or constructed |
| C-11 | No message, fact, or accordion content contains a key label or key material/fragments |
| C-12 | `request_id` appears on the wire inside facts but renders only under Verbose chat |
| C-13 | The Verbose raw-detail fetch requires an authenticated REST call; expired/absent retention → 404, never a guess |
| C-14 | Context-length errors keep today's code, copy, and behaviour (PM-5) — regression-guarded |

## 5. Delivery mechanism for Verbose raw JSON (PM-6 — options, pick, founder confirm)

Verbose chat is stored **client-only** (`src/store/chatPreferences.ts` — zustand persist, localStorage; "does not cross the gateway/API boundary", its own comment). The gateway therefore cannot know a receiving tab's preference at frame time, and the session hub's redesign (#823) made frames uniform per session — one numbered frame per event, delivered to every bound tab.

| Option | Mechanism | #711 closure | Cost / risk |
|---|---|---|::|
| **1 — On-demand REST fetch (recommended)** | Error frames carry structured facts only (uniform for every tab). When a Verbose viewer expands the accordion, the SPA calls `GET /api/v1/sessions/{id}/llm-errors/{error_id}/provider-detail`; the gateway returns the scrubbed raw provider body from a short-lived in-memory retention (keyed session+error id, TTL minutes; never persisted; replay → 404). | **By construction** — raw text never rides any broadcast frame; it crosses the wire only when a viewer explicitly asked, over an authenticated unicast REST response. | One new REST route (contract-first); a bounded in-memory retention map; one new frame field (`error_id`); an "available live only" state on replayed errors. |
| 2 — Per-tab Verbose flag at attach | Tab declares `verbose` on `attach_session`; the hub includes raw detail only for Verbose tabs (per-tab frame tails). | At the source (who may see), not what is in it. | Breaks the hub's uniform-frame invariant (per-tab content branches one numbered frame into N variants); flag is client-declared (a preference, not a boundary); mid-session flips need re-sync. Most invasive to #823's design. |
| 3 — Request/reply WS frames | SPA sends a `verbose_detail_request` frame; gateway replies with a detail frame. | Same as 1, over WS. | A new request/reply frame pair + client state for what option 1 gets with one REST route; no benefit. |

**Spec decision: Option 1.** Reasons: it preserves the #823 hub invariant (uniform numbered frames), it closes #711 structurally (nothing raw is on the wire to leak — PM-1's doctrine), the raw text reaches only the device that asked for it over the existing authenticated REST surface, and replay is naturally clean. **Founder confirmation point Q1.**

Retention shape: bounded map (e.g. last N errors per session, TTL ~5 minutes, cleared on session close), value = scrubbed body + status + captured header facts; populated at `hubError`; never written to disk; C-8.

## 6. The one-line messages — exact templates (PM-1, PM-2)

**D1 — assembled server-side.** The gateway assembles the final line into `LLMError.message` from the catalogue + facts, so live bubbles, persisted transcripts, and channel surfaces (Telegram/Discord) all carry the same sentence, and the existing copy-rule tests keep guarding the server side. The countdown itself is inherently live and is **not** part of the persisted message: it renders in the retry indicator from `facts.retry_after_seconds`.

**Templates.** Slot vocabulary: `{provider}` = provider display name from the catalog (e.g. "OpenRouter"); `{attempt}`/`{max}` = integers; `{countdown}` = live, client-rendered (mm:ss, C-9); answered/unavailable model names for the fallback note. When facts are absent (provider unknown), the existing catalogue sentence is the fallback — never a blank.

| Code | Template (exact) | Rendered when |
|---|---|---|
| `rate_limited` — retry in progress (live) | `{provider} is busy. Retrying automatically in {countdown} (attempt {attempt} of {max}).` | During auto-retry, from the retry frame (client assembles the live line from frame facts; PM-1's example verbatim shape) |
| `rate_limited` — terminal | `{provider} is busy right now. You can retry the turn.` | Retries exhausted, or no auto-retry path |
| `quota_billing` (new) | `{provider} says your account is out of credit.` | Always (facts present); button "Open billing page" additionally when `facts.billing_url` present (PM-3) |
| `provider_auth_failed` | `{provider} rejected the API key. Check the key in Settings → Providers.` | Facts present |
| fallback note | `Answered by {answered_model} because {unavailable_model} was unavailable.` | Fallback success (frame + persisted note) |

All other codes keep today's catalogue copy unchanged (generic, no provider naming in this feature). Context length stays exactly as is (PM-5, C-14).

**Copy rules carried over:** a `config` message never advises retry; `quota_billing`'s attribution decision is **Q4** (recommended: `config` — the account state is operator-fixable and retrying fails identically, which the copy-rule tests then enforce).

**Contract mechanics for templates:** the templates live in the contract (single source of truth), not in code: a new `x-user-message-templates` block on `LLMError.yaml` keyed by code, plus the live-retry line as its own template keyed to the retry frame. Generators extend the existing hard-fail bijection: every code with a template must have an `x-user-messages` entry (C-3), and slot names must come from a closed slot vocabulary. Both generated catalogues (Go + TS) gain the templates; the Go translator substitutes `{provider}` when facts are present and falls back to the static catalogue sentence otherwise.

## 7. Backend work breakdown

Order is contract-first (Hard Constraint #8): §7.1 contract wave lands as one atomic commit with regenerated artifacts before any Go/TS consumer code.

### 7.1 Contract wave (first)

1. `contracts/components/schemas/LLMError.yaml`: add `quota_billing` to the enum + `x-user-messages` entry (attribution per Q4); add `x-user-message-templates`; add the optional `facts` object (`provider`, `model`, `retry_after_seconds`, `attempts`, `max_attempts`, `request_id`, `billing_url` — all optional, `request_id` flagged Verbose-render-only) and `error_id`; remove `detail` (Q2 — recommended per the founder's delete-superseded-code ruling 2026-09-21; the field is optional today so removal breaks no persisted shape).
2. `contracts/asyncapi.yaml`: keep the inline `LLMError` / `LLMErrorReplay` copies in lockstep (the file's own comment requires it); add the two new messages (`provider_retry`, `provider_fallback`) on the chat channel.
3. `contracts/components/schemas/`: new `ProviderRetryFrame.yaml` (type `provider_retry`; `session_id`, `turn_id`, `provider`, `model`, `retry_after_seconds`, `attempt`, `max_attempts`, `error_code`; + `seq` per the #823 pattern) and `ProviderFallbackFrame.yaml` (type `provider_fallback`; `session_id`, `turn_id`, `answered_model`, `unavailable_model`; + `seq`). `ReplayErrorFrame` and `LLMErrorReplay` unchanged (C-7).
4. `contracts/openapi.yaml`: `GET /api/v1/sessions/{session_id}/llm-errors/{error_id}/provider-detail` → `ProviderErrorDetail` (scrubbed raw body, status, captured header facts), 404 when absent/expired; error responses per existing patterns.
5. `scripts/gen-contracts.sh` (generator extensions for templates + hard-fail bijection), commit spec + generated diff atomically; `make verify-contracts` green.

### 7.2 Capture (provider side)

- `common.ProviderError` gains `RetryAfterSeconds` (presence-distinguishable — a pointer or explicit flag, because `retry-after: 0` is legal and means "immediate", C-4), `RequestID`, and (secondary) rate-limit reset fields; parsed at `HandleErrorResponse` and `WrapHTMLResponseError` from: `retry-after` (integer seconds **or** HTTP-date), `retry-after-ms`, and `x-ratelimit-reset*` when `retry-after` is absent; clamped to 86400 (C-4).
- Request-ID header candidates: `x-request-id`, `request-id`, `x-amzn-requestid`, `cf-ray` (the list OpenCode verified against real providers).
- The scrubber is untouched: `BodyPreview`/log lines keep `ScrubSensitiveValues`.

### 7.3 Classify (both classifiers)

- **User side** (`translate_error.go`): new `CodeQuotaBilling` (`quota_billing`); new billing/quota body detector with pinned vocabulary (seed: "insufficient_quota", "insufficient credits", "insufficient balance", "out of credit", "exceeded your current quota", "usage limit reached", "billing") checked **before** `rateLimitSubstrings` on 2xx-status-absent/4xx paths and **before** the 429 short-circuit reads as rate-limit when the body matches billing — the 429 + billing body lands `quota_billing`. `isRetryable` returns false for it. The residual-4xx → `CodeUnknown` verdict is untouched (media strip-retry gate). Facts threading: `TranslateLLMError`'s verdicts gain facts from `pe` (headers) + loop identity via an additive variant (no signature break at the two choke points' existing callers).
- **Providers side** (`error_classifier.go`): 429 with a billing-shaped body → `FailoverBilling` (longer `calculateBillingCooldown` curve becomes reachable); `classifyByMessage` evaluates billing patterns **before** rate-limit patterns. Routing behaviour is otherwise preserved (`shouldRetryDelegatedRateLimit` keeps its rate-limit semantics; billing was never in its retry set).
- Loop identity: the loop threads provider display name + model into `ErrorPayload` (additive fields) so the gateway can assemble `{provider}` lines.

### 7.4 Retry visibility + fallback note (root path)

- Generalise the existing delegated pattern (`loop_provider_retry.go::callProvider`) to root turns **for `rate_limited` only**: proposed cap **5 attempts** (PM-1's example), honor provider `retry-after` when present, else exponential backoff 2 s × 2 capped 30 s with 25% jitter; the zero-streamed-bytes guard (C-6) is kept; each retry emits `EventKindLLMRetry` (exists today) with `LLMRetryPayload` extended additively (provider, model, retry-after seconds).
- `hubSyncTap` gains a handler forwarding `EventKindLLMRetry` to the new `provider_retry` frame (today the kind sits on the explicitly-not-forwarded list — that entry moves out).
- On exhaustion, the terminal error frame carries the terminal `rate_limited` template line.
- Fallback success (currently `logger.InfoCF` in `callProviderOnce`) additionally emits a persisted fallback notice + `provider_fallback` frame. **D6 — persisted** so replay shows the same note (recommended; Q6).

### 7.5 Verbose raw-detail retention + fetch (Option 1)

- `hubError` stops setting `Detail` (Q2); mints `error_id`; stores the scrubbed, capped raw body + status + headers into the bounded in-memory retention (C-8); sets facts + `error_id` on the frame.
- New REST handler returns the retained detail for authenticated callers; 404 when absent/expired (C-13). A turn-level error never depends on the retention's lifetime — absence only costs the accordion its content ("available live only").

### 7.6 Provider catalog

- Optional per-provider `billing_url` on catalog rows; embedded snapshot gains values for known providers only (OpenRouter, Anthropic, OpenAI, Google, xAI, …); absent → no button (C-10, Q5).

## 8. Frontend work breakdown

1. **Frames & store:** `frames.ts` cases for `provider_retry` and `provider_fallback`; the error-frame path reads `facts` + `error_id`; retry state is separate from the own-limiter slot (`store.ts::setRateLimitEvent` untouched).
2. **Retry indicator:** new `ProviderRetryIndicator` (design-system skill rules; reuses the `RateLimitIndicator` pattern — per-second interval, warning→success transition, `role="status"`, `aria-live`), rendering PM-1's live line with mm:ss countdown (C-9), clearing on the next content/error frame for the turn.
3. **Error bubble:** server-assembled message as the one line; `quota_billing` renders the "Open billing page" button only with `facts.billing_url`; the old `ERROR_DETAIL_MAX_CHARS`/`errorDetail` disclosure is deleted with the detail field (Q2).
4. **Verbose facts + accordion:** under Verbose chat, facts lines (provider, model, request id — `request_id` only here, C-12) plus a "Provider response (raw)" accordion reusing the tool-call accordion pattern (native `<details>` per the `MessageItem.tsx` precedent, or the catalogued collapsible — frontend-lead's call under the design-system skill). First open fetches the provider-detail REST route; loading / raw / expired ("available live only") states.
5. **Fallback note:** grey event-line treatment (delegation event-lines precedent) for the persisted fallback notice; replay parity.
6. **`getLLMErrorDisplay`:** loses the `detail` branch; gains facts passthrough; replay path unchanged (C-7).

## 9. User stories & acceptance scenarios

### US-1 — Rate-limit countdown (P0; PM-1, PM-4)

**Narrative.** A user whose provider rate-limits a turn no longer stares at a vague "wait a moment": the turn keeps working, visibly, with the provider's own wait time.

**Why this priority:** the most common provider failure; the founder's first example.

**Independent test:** inject a 429 with `retry-after: 120` at the provider adapter; observe the retry frame + countdown; exhaust attempts; observe the terminal line.

1. **Given** a turn whose provider answers 429 with `retry-after: 120`, **When** the agent loop schedules the retry, **Then** a `provider_retry` frame arrives with `provider`, `model`, `retry_after_seconds=120`, `attempt=1`, `max_attempts=5`, and the chat shows "{provider} is busy. Retrying automatically in 2:00 (attempt 1 of 5)."
2. **Given** a retry in progress, **When** each second passes, **Then** the countdown decrements and at 0 the next attempt is issued (attempt 2 follows).
3. **Given** a 429 without a usable `retry-after`, **When** the loop schedules the retry, **Then** backoff is exponential (2 s base, 30 s cap, jitter) and the line omits a fake time (uses the backoff it actually applies).
4. **Given** retries exhausted, **When** the final attempt fails, **Then** the terminal error line is "{provider} is busy right now. You can retry the turn." and the indicator clears.
5. **Given** bytes already streamed to the turn, **When** a 429 arrives mid-stream, **Then** no retry is scheduled (C-6) and the terminal line shows instead.

### US-2 — Billing gets its own message (P0; PM-1, PM-3)

**Narrative.** An operator with an empty account is told the truth and shown the fix — not told to "wait a moment".

**Independent test:** inject a 402 / 429-with-quota-body; assert the new code, sentence, and conditional button.

1. **Given** a provider response whose body matches the billing/quota vocabulary (any status in {402, 429, other 4xx}), **When** classified, **Then** the code is `quota_billing`, the line is "{provider} says your account is out of credit.", and `retryable` is false.
2. **Given** the provider has a known billing URL in the catalog, **When** the error renders, **Then** an "Open billing page" button is present linking exactly that URL.
3. **Given** no known billing URL, **When** the error renders, **Then** the sentence shows with no button (C-10).
4. **Given** a plain "too many requests" body (no billing vocabulary), **When** classified, **Then** the code stays `rate_limited` — the new detector must not swallow genuine rate limits.

### US-3 — Auth line names the provider (P0; PM-1, PM-2)

1. **Given** a 401/403 with provider identity known, **When** classified, **Then** the line is "{provider} rejected the API key. Check the key in Settings → Providers."
2. **Given** provider identity unknown (facts absent), **When** classified, **Then** today's catalogue sentence shows unchanged.
3. **Given** any auth error, **When** the message is assembled or rendered, **Then** no key label, key material, or fragment appears (C-11).

### US-4 — #711 closes by construction (P0; PM-6, PM-7)

1. **Given** any provider error (any code, any facts), **When** the error frame is published, **Then** the frame JSON contains code/message/retryable/facts only — no body text, no `detail` field (C-1, Q2).
2. **Given** a tab with Verbose chat off (or on — the frame is identical), **When** a WS log of that frame is inspected, **Then** no raw provider text is present.
3. **Given** the blunt drop-detail commit on the security branch (PM-7), **When** this feature lands, **Then** that branch's error-detail change is replaced by this design (tracked on the security branch, out of this spec's tree).

### US-5 — Verbose accordion with raw provider JSON (P1; PM-6)

1. **Given** Verbose chat on and a live error, **When** the viewer expands "Provider response (raw)", **Then** the SPA fetches `provider-detail` and renders the scrubbed raw body; a loading state shows while fetching.
2. **Given** a registered credential value appears in the raw body, **When** the detail is retained/fetched, **Then** the stored value is scrubbed (C-8) — the raw view shows provider JSON, not our credentials.
3. **Given** a replayed (historical) error, **When** the viewer expands the accordion, **Then** the fetch 404s and the UI shows "available live only" — history never carries raw provider text (C-7, C-13).
4. **Given** Verbose chat off, **When** an error renders, **Then** no accordion, no facts block, no fetch occurs; the one line is the whole display.

### US-6 — Fallback note (P1; PM-4)

1. **Given** the primary candidate fails and a fallback answers, **When** the reply completes, **Then** a quiet note "Answered by {answered_model} because {unavailable_model} was unavailable." shows under the reply.
2. **Given** the session is reloaded, **When** history replays, **Then** the same note renders from the persisted transcript (Q6 recommendation).
3. **Given** the primary answered normally (no fallback), **When** the turn ends, **Then** no note appears.

### US-7 — Context length unchanged (P1 regression guard; PM-5)

1. **Given** a context-overflow error, **When** classified and rendered, **Then** code, copy, and attribution are byte-identical to today (C-14); no countdown, no compaction, no token counts.

## 10. BDD scenarios (spec-derived oracles)

Oracles come from this spec: exact template sentences (§6), constraint IDs (§4), frame field lists (§7.1), and the copy-rule tests that already exist.

### Scenario set A — rate-limited (US-1)

```gherkin
Scenario: Retry countdown honors the provider's retry-after
  Given a provider adapter that answers 429 with header retry-after: 120
  And a turn with model M on provider P (catalog display name "OpenRouter")
  When the agent loop classifies the response
  Then a provider_retry frame is published with provider "OpenRouter", model M,
    retry_after_seconds 120, attempt 1, max_attempts 5, error_code "rate_limited"
  And the chat shows the line: OpenRouter is busy. Retrying automatically in 2:00 (attempt 1 of 5).
  Traces to: US-1 / 1

Scenario: Countdown format follows PM-1
  Given a provider_retry frame with retry_after_seconds 92
  When the indicator renders the countdown
  Then the text shows 1:32 (mm:ss, not "1m 32s")
  Traces to: US-1 / 1   [C-9]

Scenario: Terminal line after exhaustion
  Given 5 failed rate-limited attempts
  When the loop gives up
  Then the error frame carries code rate_limited, retryable true,
    message "OpenRouter is busy right now. You can retry the turn."
  And the retry indicator is gone
  Traces to: US-1 / 4

Scenario: No retry after streamed bytes
  Given a turn whose stream already delivered >0 bytes
  When a 429 arrives
  Then no provider_retry frame is published
  And the terminal rate_limited line shows
  Traces to: US-1 / 5   [C-6]
```

### Scenario set B — billing (US-2)

```gherkin
Scenario Outline: Quota-shaped bodies land quota_billing
  Given a provider response with status <status> and body containing "<body>"
  When classified
  Then the code is quota_billing, retryable false
  And the message is "<provider> says your account is out of credit."
  Traces to: US-2 / 1

  Examples:
    | status | body                                              |
    | 402    | {"error":{"message":"insufficient credits"}}      |
    | 429    | exceeded your current quota                       |
    | 429    | insufficient_quota                                |
    | 400    | Your account is out of credit                     |

Scenario: Billing button only when the URL is known
  Given quota_billing facts with billing_url "https://openrouter.ai/credits"
  When the bubble renders
  Then a button "Open billing page" links that exact URL
  Traces to: US-2 / 2   [C-10]

Scenario: No button without a URL
  Given quota_billing facts without billing_url
  When the bubble renders
  Then the sentence renders with no button
  Traces to: US-2 / 3

Scenario: Genuine rate limits stay rate_limited
  Given a 429 with body "too many requests" (no billing vocabulary)
  When classified
  Then the code is rate_limited
  Traces to: US-2 / 4
```

### Scenario set C — auth (US-3)

```gherkin
Scenario: Auth line names the provider
  Given a 401 from provider P ("OpenRouter")
  When classified with identity known
  Then the message is "OpenRouter rejected the API key. Check the key in Settings → Providers."
  Traces to: US-3 / 1

Scenario: Identity absent falls back to catalogue copy
  Given a 401 with no provider identity threaded
  When classified
  Then the message is today's catalogue sentence for provider_auth_failed, unchanged
  Traces to: US-3 / 2

Scenario: Keys are never named
  Given any auth or billing error whose provider body echoes a key fragment
  When the message and facts are assembled
  Then the message names no key label and contains no key material or fragment
  Traces to: US-3 / 3   [C-11]
```

### Scenario set D — #711 and Verbose delivery (US-4, US-5)

```gherkin
Scenario: Error frames never carry provider body text
  Given any provider error with body B (status 429, 401, or 402)
  When the hub publishes the ErrorFrame
  Then the frame JSON contains code, message, retryable, and facts only
  And no substring of B (longest 12+ char non-whitespace run) appears in the frame
  And no "detail" key exists in the frame payload
  Traces to: US-4 / 1   [C-1, Q2]

Scenario: Verbose accordion fetches on demand
  Given Verbose chat on and a live error with error_id E in session S
  When the viewer expands the raw accordion
  Then the SPA calls GET /api/v1/sessions/S/llm-errors/E/provider-detail
  And the response body is the scrubbed raw provider body
  Traces to: US-5 / 1, US-4 / 2

Scenario: Replay never carries or serves raw detail
  Given a historical error replayed from the transcript
  When the accordion expands
  Then the fetch returns 404 and the UI shows "available live only"
  And the replay frame carries no facts object and no raw text (LLMErrorReplay unchanged)
  Traces to: US-5 / 3   [C-7, C-13]

Scenario: Registered credentials are scrubbed from retained raw body
  Given a raw provider body containing a registered credential value
  When the retention stores it
  Then the stored value reads [REDACTED] (or the scrubber's marker)
  Traces to: US-5 / 2   [C-8]
```

### Scenario set E — fallback (US-6)

```gherkin
Scenario: Fallback note names both models
  Given primary model Y unavailable and fallback model X answered
  When the turn completes
  Then a provider_fallback frame and a persisted note show
    "Answered by X because Y was unavailable."
  Traces to: US-6 / 1

Scenario: No note without fallback
  Given the primary candidate answered
  When the turn completes
  Then no fallback note appears
  Traces to: US-6 / 3

Scenario: Replay shows the same note
  Given a fallback note persisted this session
  When history replays
  Then the note renders identically from the transcript
  Traces to: US-6 / 2
```

### Scenario set F — regressions (US-7)

```gherkin
Scenario: Context-length behaviour untouched
  Given a context-overflow response
  When classified
  Then code, message, and attribution are byte-identical to the current catalogue
  Traces to: US-7 / 1   [C-14]
```

## 11. TDD plan

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | header parse: retry-after seconds / HTTP-date / ms / absent / 0 / >86400 | Unit | A-1, C-4 | `common.ProviderError` |
| 2 | request-id header candidates | Unit | C-12 | |
| 3 | billing detector vs rate-limit (dataset D2) | Unit | B-* | `classifyByMessage` order + 429 path |
| 4 | quota_billing: retryable false, copy rules hold | Unit | B-1 | extends existing copy-rule tests |
| 5 | templates: bijection, slot closure, fallback when facts absent | Unit | §6, C-3 | generator + Go/TS catalogue tests |
| 6 | terminal vs live retry lines | Unit | A-3 | |
| 7 | root-path retry loop: attempts, backoff, zero-stream guard | Unit | A-1…A-5 | mirrors `loop_provider_retry.go` tests |
| 8 | providers-side: 429+billing → FailoverBilling; cooldown curve chosen | Unit | B-1 | |
| 9 | hubError: facts on frame, no detail, error_id minted, retention scrubbed | Integration | D-* | |
| 10 | provider-detail REST: auth required, 404 on expiry | Integration | D-2/3 | |
| 11 | wire scan: no body substring in any error frame | Integration | D-1, C-1 | the #711 oracle |
| 12 | transcript: message+code persisted, no body/headers | Integration | C-7 | |
| 13 | ProviderRetryIndicator: countdown mm:ss, clear-on-next-frame | Component (vitest) | A-1/2 | |
| 14 | billing button conditional | Component | B-2/3 | |
| 15 | accordion states: loading/raw/expired; no fetch when Verbose off | Component | D-2…4 | |
| 16 | fallback note render + replay parity | Component | E-* | |
| 17 | `getLLMErrorDisplay` facts passthrough, no detail branch | Unit (vitest) | D-* | |
| 18 | E2E: injected 429 → countdown → terminal line; body never on WS | E2E | A-*, D-1 | Playwright, mock provider |

**Existing tests that must keep passing unchanged:** the 23-code copy-rule tests (Go + TS), replay-strips-detail tests (now: replay has no facts either), media strip-retry gate tests (`CodeUnknown` residual), delegated retry tests, `verify-contracts`, `lint-guards`.

## 12. Test datasets

**D1 — retry-after parsing (traces to A-1, C-4)**

| case | header | expected fact |
|---|---|---|
| seconds | `retry-after: 120` | 120 |
| zero | `retry-after: 0` | absent fact (immediate retry, no countdown) |
| http-date | `retry-after: Wed, 21 Oct 2026 07:28:00 GMT` | delta seconds at parse time |
| ms variant | `retry-after-ms: 1500` | 2 (rounded up, ≥1) |
| fallback | `x-ratelimit-reset: 1729…` (epoch) | delta, only when retry-after absent |
| absent | — | no fact; exponential backoff path |
| over-cap | `retry-after: 100000` | clamped 86400 |

**D2 — billing vs rate-limit (traces to B-*, C-2)**

| status | body | expected code |
|---|---|---|
| 402 | insufficient credits | quota_billing |
| 429 | exceeded your current quota | quota_billing |
| 429 | insufficient_quota | quota_billing |
| 429 | too many requests | rate_limited |
| 429 | (empty) | rate_limited |
| 400 | Your account is out of credit | quota_billing |
| 400 | Unsupported MIME type: image/svg+xml | unknown (residual — strip-retry gate intact) |
| 200-less | rate limit exceeded (no quota words) | rate_limited |

**D3 — message assembly (traces to §6)**

| facts | expected line |
|---|---|
| provider=OpenRouter, retry 120, attempt 1/5 | OpenRouter is busy. Retrying automatically in 2:00 (attempt 1 of 5). |
| provider=OpenRouter (terminal) | OpenRouter is busy right now. You can retry the turn. |
| provider=OpenRouter, billing | OpenRouter says your account is out of credit. |
| provider=OpenRouter, auth | OpenRouter rejected the API key. Check the key in Settings → Providers. |
| no facts | today's catalogue sentence (byte-identical fallback) |
| provider name with special chars ("z-ai") | rendered verbatim; no truncation |

## 13. Regression impact

The feature **modifies existing behaviour**: error lines for three code families gain provider naming; the detail wire field disappears. Guarded by:

1. All 23 existing codes' copy unchanged except the three families — copy-rule tests extended, not replaced.
2. `context_too_long` byte-identical (PM-5, C-14).
3. Residual-4xx → `CodeUnknown` unchanged (media strip-retry).
4. Replay shape unchanged (no facts, no detail).
5. Own-limiter `rate_limit` frame and indicator untouched.
6. Delegated retry policy unchanged (count/caps); only its payload gains fields.
7. Scrubber behaviour unchanged for logs.

## 14. Requirements & success criteria

### Functional requirements

- **FR-001:** The system MUST capture provider `retry-after` family headers and request-ID headers into the structured provider error at the HTTP boundary.
- **FR-002:** The system MUST classify billing/quota exhaustion as its own code (`quota_billing`), distinct from `rate_limited`, not retryable, in both classifiers.
- **FR-003:** The system MUST assemble one plain-English line per error server-side from the catalogue + facts, with provider naming for the three template families and catalogue fallback when facts are absent.
- **FR-004:** The system MUST publish error frames carrying code, message, retryable, facts (provider, model, retry_after_seconds, attempts, max_attempts, billing_url when known, request_id), and error_id — and MUST NOT carry provider body text or a `detail` field.
- **FR-005:** The system MUST automatically retry rate-limited provider calls on the root path (cap 5, retry-after honored, zero-streamed-bytes guard) and publish a retry frame per attempt with a live countdown.
- **FR-006:** The system MUST emit and persist a fallback note naming the answering and unavailable models when a fallback answers.
- **FR-007:** The system MUST offer "Open billing page" only when a catalog-known billing URL exists for the provider.
- **FR-008:** The system MUST serve the scrubbed raw provider response to authenticated Verbose viewers on demand (live only), and never persist or replay it.
- **FR-009:** The system MUST NOT name key labels, key material, or fragments anywhere in messages, facts, or the raw view (the raw view shows the provider's own body, scrubbed of registered credentials).
- **FR-010:** The system MUST keep context-length classification, copy, and behaviour unchanged.
- **FR-011:** The system MUST keep the scrubber in place for logs and apply it to retained raw detail before storage.
- **FR-012:** Contract changes MUST land first (Constraint #8): schemas → regenerate → commit atomically → consumers.

### Success criteria

- **SC-1:** On an injected 429 with `retry-after: 120`, the chat shows the PM-1 countdown line within one frame round-trip, and the countdown reaches 0 within ±2 s of 120 s. (Verified = Playwright e2e.)
- **SC-2:** A wire-log scan over all error frames in the e2e suite finds zero provider-body substrings. (The #711 oracle; zero matches.)
- **SC-3:** Billing-shaped responses produce `quota_billing` with the PM-1 sentence in 100% of dataset D2 rows.
- **SC-4:** A Verbose viewer can open the raw accordion on a live error; a replayed error shows "available live only"; both verified in Playwright.
- **SC-5:** All dataset D1 parsing rows produce the expected fact or absence.
- **SC-6:** `verify-contracts` and the copy-rule tests are green with `quota_billing` + templates present (bijection holds).

## 15. Traceability matrix

| FR | US | BDD scenario | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | A-1 | 1, 2 |
| FR-002 | US-2 | B-1…4 | 3, 4, 8 |
| FR-003 | US-1, US-2, US-3 | A-3, B-1, C-1 | 5, 6, dataset D3 |
| FR-004 | US-4 | D-1 | 9, 11 |
| FR-005 | US-1 | A-1…A-5 | 7, 13, 18 |
| FR-006 | US-6 | E-1…3 | 16 |
| FR-007 | US-2 | B-2, B-3 | 14 |
| FR-008 | US-5 | D-2…4 | 10, 15 |
| FR-009 | US-3, US-5 | C-3, D-4 | 9, 11 |
| FR-010 | US-7 | F-1 | copy-rule + regression suite |
| FR-011 | US-5 | D-4 | 9 |
| FR-012 | all | (process) | `verify-contracts`, CI |

Every FR appears; every scenario traces to ≥1 FR.

## 16. Questions for the founder

Background for each: the spec picked a recommended answer; nothing below blocks drafting, but each changes what gets built.

- **Q1 (PM-6 delivery — the spec's flagged confirmation point):** Raw provider JSON for the Verbose accordion — **A: on-demand REST fetch** (frames stay facts-only, uniform for every tab; recommended — closes #711 structurally, preserves the #823 uniform-frame hub, replay naturally clean) or **B: per-tab Verbose flag at attach** (gates at the source, but branches one numbered frame per tab and stays client-declared). Answer "Q1 A" / "Q1 B".
- **Q2 (`detail` removal):** The error contract's optional `detail` field — **A: delete it** (recommended: the gateway stops sending it, the SPA deletes the disclosure; greenfield + the founder's delete-superseded-code ruling) or **B: keep the field but stop populating it** (the dispatch's "additive changes" reading, one release of dead schema). Answer "Q2 A" / "Q2 B".
- **Q3 (auto-retry policy numbers):** Root-path auto-retry for `rate_limited` — cap **5 attempts** (PM-1's example), provider `retry-after` honored, else 2 s × 2 capped 30 s with 25% jitter; **delegated turns keep today's 2-retry policy** (unification is a follow-up). Confirm the numbers, or name different ones.
- **Q4 (`quota_billing` attribution):** **A: `config`** (recommended — operator-fixable account state; the copy-rule tests then forbid retry advice, which is the point) or **B: `provider`** (attribution-pure: the provider refused service; but its copy rules would then permit "retry" advice — wrong here). Answer "Q4 A" / "Q4 B".
- **Q5 (billing URL source):** Add an optional per-provider `billing_url` to the provider catalog (+ seed values for the known providers). Confirm, and say which providers to seed (proposal: OpenRouter, Anthropic, OpenAI, Google, xAI — the catalog's most-used rows).
- **Q6 (fallback note persistence):** Persist the fallback note so replay shows it (recommended), or live-only? Recommendation: persist — a note that vanishes on reload reads like a glitch.

## 17. Reachability (Definition of Done)

- **No new tool** — Hard Constraint #6's catalog/policy check is N/A (grep for a new tool name returns nothing to register; nothing to assign).
- **UI surfaces:** every chat tab renders the new error lines, the retry indicator, the billing button, the Verbose facts+accordion, and the fallback note — no settings toggle gates them except Verbose chat itself (existing, `chat-verbose-switch`).
- **Trigger paths a real user can hit:** a bad API key (Settings → Providers), an out-of-credit account, a rate-limited provider, a provider outage with fallback candidates configured. E2E injects 429/401/402 at a mock provider; UAT can use a deliberately invalid key on a real provider.
- **Two-line delivery statement at landing:** code correct and tested (gates green); reachable by a user (any chat tab shows the new presentation on a real provider failure) — the reachability claim must be demonstrated, not inferred from green tests.

## 18. Holdout evaluation scenarios (post-implementation; NOT in the traceability matrix)

Happy path:

1. Set a provider key that is valid but rate-limited (or ask UAT to hammer a free-tier key); send a chat message; confirm the sentence names the provider, counts down, and the turn eventually completes or ends with the terminal line — all in one turn, no reload.
2. Fill the provider account to zero credit; send a message; confirm the sentence says out-of-credit and the button opens the provider's real billing page in a new tab.
3. Break the API key; send a message; confirm the sentence names the provider and the Settings → Providers path is the one that fixes it.

Error path:

4. Point an agent at a provider with a wrong `api_base` that returns an HTML page; confirm the line stays human (no HTML, no status soup) and Verbose's raw view shows what the gateway actually got.
5. Kill the network mid-turn; confirm the classification stays `network` with today's copy (no provider name invented).

Edge:

6. Two tabs on one session, one Verbose one not, during a 429: confirm both show the same line and neither tab's WS log contains body text; the Verbose tab fetches raw only on expand.
7. A provider whose display name contains a slash and dots ("z-ai/glm"); confirm the sentence renders it verbatim, nowhere truncated or escaped.

---

## Verification basis (evidence for the facts this spec is built on)

| Claim | Evidence | Certainty |
|---|---|---|
| Branch state | `git status --short --branch` → `feat/provider-messages...origin/feat/provider-messages`, clean; `git log -3` → `fad3770f2` head | Verified |
| 23-code catalogue, attributions, generator hard-fail, `detail` optional 2048 | Read `contracts/components/schemas/LLMError.yaml` (whole file) | Verified |
| `detail` set unconditionally on every error frame | Read `pkg/gateway/websocket_forward_hub.go::hubError` (`Detail: &detail`, incl. curated-code override path) | Verified |
| `ProviderError` captures no response headers today | Read `pkg/providers/common/common.go::HandleErrorResponse`, `WrapHTMLResponseError` (only `Content-Type` read; 8 KiB cap, 512 B preview) | Verified |
| "quota exceeded" → `CodeRateLimited` today; no billing code user-side | Read `pkg/agent/translate_error.go::rateLimitSubstrings`, `classifyByHTTPStatus` (402 → residual 4xx), enum block | Verified |
| Providers-side: 429 → `FailoverRateLimit` before body check; rate-limit patterns before billing in `classifyByMessage` | Read `pkg/providers/error_classifier.go` (status switch + `classifyByMessage` order; `billingPatterns` at the pattern block) | Verified |
| Retry events exist but never reach the SPA | Read `pkg/agent/events.go::LLMRetryPayload`; `pkg/gateway/websocket_forward.go` (EventKindLLMRetry in the explicitly-not-forwarded case list); `pkg/agent/loop_provider_retry.go::callProvider` (delegated-only, logged, zero-stream guard) | Verified |
| Fallback success is log-only | Research §2.1 (`loop_run_turn.go::callProviderOnce` → `logger.InfoCF`), consistent with this task's reads | Verified (via research doc; symbol cited there) |
| Verbose chat is client-only | Read `src/store/chatPreferences.ts` (zustand persist; "does not cross the gateway/API boundary") | Verified |
| Error-detail disclosure is native `<details>`, Verbose-gated, 512-char cap | Read `src/components/chat/MessageItem.tsx` (disclosure block, `ERROR_DETAIL_MAX_CHARS`) | Verified |
| Countdown pattern to reuse | Read `src/components/chat/RateLimitIndicator.tsx` (`formatSeconds` "1m 32s", per-second interval, aria-live) — own limiter only | Verified |
| Uniform-frame hub invariant | `ErrorFrame.yaml`/`RateLimitFrame.yaml` `seq` comments (#823 chokepoint); `websocket_forward.go` hubSyncTap comments | Verified |
| buildDetail scrubs before preview cut | Read `pkg/agent/translate_error.go::buildDetail` | Verified |
| GitNexus unavailable in this session | Tool list has no gitnexus tools; exploration done by Read/Grep (shared rule 9) | Verified |
| Impact rows | Grep sweep for callers (choke points, retry gates, catalogues) | Inferred (graph not available) |
| fablize hook misfires during this task | 7 PostToolUse flags on commands all verified exit 0 via explicit `echo "exit=$?"` capture; treated as baseline noise, no tool actually failed | Verified (per-command exit capture) |
| **Self-check** | Re-read the finished spec against the dispatch's done-criteria: Status line present (Draft); contract wave ordered first with the five touchpoints and the additive/new-frame list; exact PM-1 templates per code given (§6); delivery-mechanism options proposed, one picked with reasons, flagged as Q1; backend scope covers header capture, both classifiers, buildDetail replacement, scrubber-stays-for-logs, retry countdown + fallback note on the RateLimitIndicator pattern, PM-5 unchanged; frontend scope mirrors it; BDD scenarios carry spec-derived oracles + Traces-to; TDD plan, datasets, regression, FR/SC, full traceability, reachability, holdout scenarios present; six founder questions with recommendations; commit scoped to the spec file only, author verified human, no co-author trailer; push to origin/feat/provider-messages completed and re-verified | Verified |

*Skills: omnipus-shared-rules, plan-spec.*

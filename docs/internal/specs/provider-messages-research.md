# Provider/LLM error surfacing — OpenCode vs Omnipus research

**Date:** 2026-09-26 · **Author:** architect (read-only design research for the founder design session)
**Topic:** how an agent product should show provider/LLM errors and provider messages to users — "show the user everything that is relevant, in a non-technical form" (founder, 2026-09-26). Context: issue #711 (raw provider detail reaches the browser even with Verbose chat off; unregistered key fragments pass the scrubber) — but the founder wants the whole mechanism improved, not just the leak closed.

---

## Sources and method

| Source | Detail |
|---|---|
| OpenCode | https://github.com/anomalyco/opencode (the `sst/opencode` URL resolves here; default branch `dev`), shallow clone at commit `696f41bc8e7586657375d53390925fc54c25d34c` (2026-09-25), cloned 2026-09-26 into `/Users/danielpiatkowski/AI-Agent-Workspace/research/opencode` |
| Omnipus | this checkout, detached HEAD `d0383aae` (`nightly-5820-gd0383aae2`, release/v0.1.1 line), read-only |
| Issue #711 | as described in the dispatch (raw provider error detail on the live wire with Verbose off; scrubber passes unregistered key fragments). The two claims were re-verified against the code here — see Part 2, §4 |

All OpenCode paths below are relative to `packages/` of that clone; all Omnipus citations are `file::symbol` in the Omnipus tree. Every "Verified" claim was read in this task; "Inferred" claims are derived from code paths but not executed.

---

## Bottom line

Omnipus's **copy catalogue is ahead of OpenCode's** — 23 typed codes with machine-readable fault attribution and copy rules enforced by test (contract `contracts/components/schemas/LLMError.yaml`), where OpenCode hard-codes English strings in classification code and UI components. OpenCode's **delivery mechanism is ahead of Omnipus's** — it parses the provider's own `retry-after` / `x-ratelimit-*` headers into structured facts, runs a visible retry loop with a live countdown and an action object (`{title, message, label, link}`) that renders as clickable buttons ("Subscribe", "Open settings"), rewrites provider messages into friendlier sentences, and captures the provider's request ID. Omnipus discards all of that: its `ProviderError` keeps only `Status` + `Body` text, so a provider 429 arrives as "temporarily overloaded, wait a moment, then retry" with **no reset time, no countdown, no account/key identity, no billing link** — and a billing exhaustion gets the same "wait and retry" advice as a transient 429, which is actively wrong. And #711 stands: the Verbose-only detail — a scrubbed body preview — is marshalled onto every error frame whether or not the user has Verbose on (`pkg/gateway/websocket_forward_hub.go::hubError`), and the scrubber only matches **registered, complete** credential values, so unregistered or truncated key fragments pass.

The improvement direction: keep the bubble copy generic and non-technical (that part works), and add a **structured facts/actions block** to the wire contract so the provider's own actionable facts (reset time, provider/model identity, billing link, request ID) arrive as data and render as affordances — while the raw body stops crossing the boundary at all (that closes #711 by design rather than by scrubbing).

---

## Part 1 — OpenCode: how it works

```
provider HTTP call
  └─ packages/llm executor (transport retries ×2, redaction, request-id capture)
       └─ error classified (statusReason) → tagged reason + HttpContext
            └─ session processor (processor.ts::process)
                 ├─ retryable? → SessionRetry.policy → session.status {type:"retry",
                 │                attempt, message, action?, next} event → UI countdown
                 ├─ context overflow → auto-compaction instead of error
                 └─ terminal → error stored on assistant message → Session.Event.Error
                      └─ TUI: toast/notification + DialogRetryAction (action buttons)
                      └─ Web: SessionRetry card (spinner + countdown) / error card
```

### 1.1 Error taxonomy

| Type (all `packages/core/src/v1/session.ts`) | Fields | Notes |
|---|---|---|
| `APIError` | `message, statusCode?, isRetryable, responseHeaders?, responseBody?, metadata?` | the workhorse; **keeps raw response headers and body on the record** |
| `ContextOverflowError` | `message, responseBody?` | never retried; triggers auto-compaction |
| `ContentFilterError` | `message` | ACP maps it to stop reason "refusal" (`packages/opencode/src/acp/service.ts`) |
| `ProviderAuthError` | `providerID, message` | names the provider; ACP surfaces `AuthRequiredError` (auth prompt) |
| `MessageAbortedError` | `message` | user abort — rendered as neutral "interrupted", not error |
| `MessageOutputLengthError`, `StructuredOutputError` | — | output-cap and schema-output failures |

The newer `packages/llm` layer (DESIGN.md "Error Model") moves to a tagged union: `AuthenticationError, InvalidRequestError (with classification: "context-overflow"), UnsupportedCapabilityError, ToolBindingError, TransportError, ProviderResponseError, InvalidProviderOutputError, HookError` — each retaining provider/model/turn/stage context. The two systems coexist mid-migration; the new one is where the structured-facts work lives.

### 1.2 Classification

Two layers, by design:

- **Message rewriting** — `packages/opencode/src/provider/error.ts::message()` (function): empty message → body or HTTP status text; extracts `body.message`/`body.error.message` from the JSON body; detects an HTML error page (gateway/proxy in front of the provider) and **rewrites it into an instruction** — 401 + HTML → "Unauthorized: request was blocked by a gateway or proxy … try running `opencode auth login <your provider URL>` to re-authenticate."
- **Code-driven rewrite** — `provider/error.ts::parseStreamError` maps provider error codes to friendly text: `insufficient_quota` → "Quota exceeded. Check your plan and billing details."; `usage_not_included` → "To use Codex with your ChatGPT plan, upgrade to Plus: https://chatgpt.com/explore/plus."; `context_length_exceeded` → "Input exceeds context window of this model".
- **Structured classification** — `packages/llm/src/route/executor.ts::statusReason`: HTTP status + body regex → `ContentPolicyReason` / `AuthenticationReason{kind: "invalid" | "insufficient-permissions"}` (401 vs 403) / `QuotaExceededReason` (429 + quota body) / `RateLimitReason{retryAfterMs, rateLimit}` / `InvalidRequestReason{classification?: "context-overflow"}` / `ProviderInternalReason` (5xx) / `UnknownProviderReason`. Every reason carries an `HttpContext{request (URL+headers, redacted), response (headers, redacted), body (≤16 KB, redacted), requestId, rateLimit}`.
- **Context-overflow detection** — `packages/llm/src/provider-error.ts::isContextOverflow`: ~30 pinned regexes plus **exclusions** (a 429 that mentions "rate limit" is not overflow) — the exclusion list is the part Omnipus's substring classifier lacks.

### 1.3 Retry / backoff, and how retries are shown

`packages/opencode/src/session/retry.ts`:

| Element | Value / behaviour |
|---|---|
| `RETRY_MAX_RETRIES` | 5 attempts |
| Backoff | 2 s base × 2, 25 % jitter, capped 30 s when no provider headers say otherwise |
| `delay()` | honours `retry-after-ms`, `retry-after` (seconds **or** HTTP-date) from the provider response headers |
| `retryable()` | 5xx always retried; regex list on message **and** body; context overflow never retried; `"FreeUsageLimitError"` in body → upsell action; `"GoUsageLimitError"` in body → **humanized reset time** ("Usage limit reached. It will reset in 2 hours 5 minutes. …") + a settings link |
| Output | `Retryable {message, action?: {reason, provider, title, message, label, link?}}` — a structured, renderable action object |

The processor wires it: `packages/opencode/src/session/processor.ts::process` wraps the LLM stream in `Effect.retry(SessionRetry.policy({…, set}))`, and each retry publishes a **`session.status` event** `{type: "retry", attempt, message, action?, next}` (schema: `packages/schema/src/session-status-event.ts`), where `next` is the epoch-ms timestamp of the next attempt. That event is the single source both UIs render:

- **Web:** `packages/session-ui/src/components/session-retry.tsx::SessionRetry` — spinner + message + a live per-second countdown "retrying in Ns (attempt N)"; message truncated at 80 chars with a tooltip; a special i18n case for a known Gemini quota wording.
- **TUI:** `packages/tui/src/component/dialog-retry-action.tsx::DialogRetryAction` — a modal showing `title`, `message`, a **clickable button labelled by `action.label`** that opens `action.link` ("subscribe", "open settings"), plus "don't show again".

On final failure the error is stored on the assistant message and published (`processor.ts::halt`), and the turn ends.

### 1.4 What the user sees vs what is logged

- The **user-facing text is the rewritten message**; the raw `responseBody` and `responseHeaders` stay on the `APIError` data attached to the message record (visible to SDK clients and in the data store — a developer-tool assumption).
- The web UI **re-parses JSON at render time**: `packages/session-ui/src/components/session-turn.tsx::unwrap` digs `error.message`/`error.code` out of a JSON blob that may be embedded in the message string.
- Desktop/TUI notifications extract the message for "Session error" notifications (`packages/tui/src/feature-plugins/system/notifications.ts::sessionErrorMessage`).
- **Redaction** lives in the new executor (`packages/llm/src/route/executor.ts`): sensitive header/query/body-field names redacted to `<redacted>`; **literal secret values extracted from the request** (Bearer tokens, query keys) and replaced anywhere they appear, including echoed back in responses; body capped 16 KB; **provider request IDs captured** (`x-request-id`, `request-id`, `x-amzn-requestid`, `cf-ray`, …) so an operator can quote them to support.
- Rate-limit headers (`x-ratelimit-{limit,remaining,reset}-*`, `anthropic-ratelimit-*`) are parsed into a structured `HttpRateLimitDetails {retryAfterMs, limit, remaining, reset}` (`executor.ts::rateLimitDetails`).

### 1.5 What is good / what is not

| Good (worth copying) | Not good (avoid) |
|---|---|
| Retry state as a first-class status **event** with countdown + structured action object | Raw `responseBody` persists on the message record and reaches any client — fine for a dev tool, wrong for Omnipus's audience |
| Provider headers parsed into facts (retry-after, rate-limit buckets) — powers the countdown | The web card truncates the message at 80 chars — information hidden rather than restructured |
| Rewriting provider messages (quota → billing instruction; HTML gateway page → auth instruction) | Client-side JSON re-parsing at render (`unwrap`) — the same extraction logic exists in three places (executor, provider/error.ts, session-turn) |
| Request-ID capture for support | OpenCode-specific upsell logic hard-coded inside the generic retry path (`FreeUsageLimitError` branch in `retry.ts`) |
| Overflow → **auto-compaction** instead of an error (`processor.ts::halt` — unless compaction is disabled) | Two classification systems mid-migration (old `provider/error.ts` + new `packages/llm`) — copy embedded in classifiers, no attribution vocabulary |
| Auth failure names the provider (`ProviderAuthError{providerID}`) and ACP turns it into an auth prompt | No product/config attribution concept — "who owns this fault" is baked into ad-hoc prose |
| Secret redaction at the transport layer, including literal-value matching | |

---

## Part 2 — Omnipus today

```
provider HTTP call
  └─ common.ProviderError{Status, Body(≤8 KiB), BodyPreview, ContentType}     (pkg/providers/common/common.go)
       ├─ providers classifier → FailoverError{Reason} + cooldown             (pkg/providers/error_classifier.go::ClassifyError,
       │    └─ fallback chain over candidates (skips cooling candidates)       pkg/providers/fallback.go)
       └─ agent loop: inline retry for timeout-class only; media strip-retry;
            truncation repair; delegated sessions retry 429 with silent backoff
       terminal failure →
       ┌─ appendErrorTranscript (write choke point) → transcript JSONL        (pkg/agent/turn_transcript.go)
       └─ hubError (live choke point) → TranslateLLMError → ErrorFrame        (pkg/gateway/websocket_forward_hub.go::hubError)
            └─ SPA: frames.ts case 'error' → getLLMErrorDisplay → error bubble
                 + (Verbose only) "Technical details" <details> + Retry button
```

### 2.1 The mechanism, end to end

| Stage | Symbol | What it does |
|---|---|---|
| Producer | `pkg/providers/common/common.go::ProviderError` | `Status`, `Body` (capped at `handleErrorBodyCap` = 8 KiB, `BodyTruncated` flag), 512 B `BodyPreview`, `ContentType`. **No response headers captured.** |
| Producer | `pkg/providers/common/common.go::HandleErrorResponse`, `WrapHTMLResponseError` | build it from an HTTP response; `ProviderError::Error()` renders `status=… content-type=… body=%q` |
| Failover classifier | `pkg/providers/error_classifier.go::ClassifyError` | ~40 substring/regex patterns → `FailoverReason` (`pkg/providers/types.go::FailoverReason`: `auth, rate_limit, billing, timeout, format, context_overflow, overloaded, unknown`) — drives fallback and cooldown (`pkg/providers/cooldown.go::calculateStandardCooldown` / `calculateBillingCooldown`) |
| Fallback chain | `pkg/providers/fallback.go::FallbackChain.Execute`, `FallbackExhaustedError` | tries candidates, skips cooling ones; success is **logged only** (`pkg/agent/loop_run_turn.go::callProviderOnce` → `logger.InfoCF("Fallback: succeeded with %s/%s …")`) — the user sees a normal reply |
| User-facing classifier | `pkg/agent/translate_error.go::TranslateLLMError` / `TranslateTurnError` | status precedence + pinned body substrings → one of 23 `LLMErrorCode`s + catalogue message + retryability + Verbose-only `Detail` (`buildDetail`) |
| Contract | `contracts/components/schemas/LLMError.yaml` | 23 codes; `x-user-messages` copy + `x-user-message-attributions` (`model/provider/product/config/ambiguous/unknown/user`); generators emit the Go and TS catalogues from this one block and **hard-fail on any gap**; copy rules test-enforced (`pkg/agent/translate_error_test.go`, `src/lib/llm-error.test.ts`) |
| Live frame | `pkg/gateway/websocket_forward_hub.go::hubError` | classifies, overrides with curated `p.Code` when present, marshals `ErrorFrame{message, payload.llm_error{code, message, retryable, detail}}` |
| Persistence | `pkg/agent/turn_transcript.go::appendErrorTranscript` / `appendClassifiedError` | classifies before write; replay type `LLMErrorReplay` **has no `detail` field** — history never carries it |
| Scrubber | `pkg/logger/sensitive.go::ScrubSensitiveValues` | replaces **registered** credential plaintexts (registered via `pkg/config/security.go::Config.RegisterSensitiveValues` at boot/sign-in/config-write). Its own doc: it "cannot recognise a value that was never registered, or one a provider masked or cut short" |
| SPA display | `src/lib/llm-error.ts::getLLMErrorDisplay` | bubble text = catalogue copy for the code (never the wire message, except `delegated_task_limit`); `detail` included **only when** Verbose chat is on |
| SPA rendering | `src/components/chat/MessageItem.tsx` (+ `ChatScreen.tsx::VirtualAssistantMessageRow` parity copy) | error status line; verbose-only native `<details>` "Technical details", capped 512 chars (`ERROR_DETAIL_MAX_CHARS`) |
| Retry button | `src/components/chat/ChatScreen.tsx::AssistantMessageRetryButton` | on any error/incomplete message — **not** conditioned on the `retryable` flag; resends the last user message |
| Own limiter | `pkg/agent/loop.go::recordRateLimitDenial` → `RateLimitFrame`; SPA `src/components/chat/RateLimitIndicator.tsx` | live countdown + "Retry available — rate limit cleared" — for **Omnipus's own SEC-26 limiter only** |

### 2.2 The two classifiers, and the copy catalogue

Omnipus classifies twice, in two packages, with two vocabularies:

1. `pkg/providers/error_classifier.go::ClassifyError` → `FailoverReason` — decides **routing** (fallback, cooldown type). It has a `billing` reason (`FailoverBilling`, with its own longer cooldown curve).
2. `pkg/agent/translate_error.go::classifyByProviderError` → `LLMErrorCode` — decides **user copy**. It has **no billing code**: quota-shaped bodies hit `rateLimitSubstrings` ("quota exceeded") → `CodeRateLimited`, whose catalogue message is "The model provider is temporarily overloaded. Wait a moment, then retry."

The catalogue (Verified — read in `contracts/components/schemas/LLMError.yaml`):

| Code | Attribution | Message (verbatim) |
|---|---|---|
| `rate_limited` | provider | "The model provider is temporarily overloaded. Wait a moment, then retry." |
| `provider_auth_failed` | config | "The model provider rejected our credentials. Check this provider's API key in Settings." |
| `context_too_long` | product | "This turn needed more context than the model can hold, even after trimming older turns automatically. Try a model with a larger context window, or shorten this message." |
| `provider_stalled` | provider | "The model provider stopped responding: nothing arrived for 5 minutes (or the silence limit set for this provider), so the call was ended. Retry — if it keeps happening, open Verbose chat for details." |
| `turn_canceled` | user | "This turn was stopped before it finished." |
| `unknown` | unknown | "This turn didn't finish, and we can't tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model." |

(Full catalogue: 23 entries in the contract; the table shows the ones the examples below use.)

### 2.3 What a user actually sees today — five worked examples

Derived from the contract copy + code paths (Inferred — not executed end to end; every cited symbol Verified):

| # | Scenario | Classification path | What the user sees | What's missing |
|---|---|---|---|---|
| 1 | Anthropic-style 429 with `retry-after: 120` header | 429 → `CodeRateLimited` | Bubble: "The model provider is temporarily overloaded. Wait a moment, then retry." + a generic Retry button. Verbose (if on): `status=429 body={…}` | The provider said "come back in 2 minutes" — the user is never told. No countdown, no "which provider/key", no request ID |
| 2 | Out of credits (402 or 429 + "insufficient_quota" body) | 402 → residual 4xx → `rateLimitSubstrings` → `CodeRateLimited`; or `CodeUnknown` | Same "temporarily overloaded, wait and retry" — or the unknown copy. OpenCode rewrites this to a billing instruction | **The advice is wrong**: waiting does not fix an empty account. No billing link. The providers-side classifier knows `FailoverBilling`; the user copy side has no billing concept |
| 3 | Expired/invalid API key (401) | `CodeProviderAuthFailed` | "The model provider rejected our credentials. Check this provider's API key in Settings." + Retry button (the button shows despite `retryable: false`) | Doesn't name **which** provider/key; no deep link into Settings → Providers; Retry button offers a retry that cannot succeed |
| 4 | Context overflow | body substrings → `CodeContextTooLong` | "This turn needed more context than the model can hold, even after trimming older turns automatically. …" (good copy, correct attribution) | No token counts, no "how much over"; OpenCode turns this case into **auto-compaction** so the user often never sees an error at all |
| 5 | Model fallback succeeded (primary 429 → fallback model answered) | — | **Nothing.** A normal reply; `ModelFooter` quietly shows the fallback model. Fallback success is a log line (`loop_run_turn.go::callProviderOnce`) | The user doesn't know a different model answered; "why did the answer style change?" is unanswerable |

Example 1's scenario row also confirms the #711 shape: regardless of Verbose, the frame's `detail` (scrubbed `body=…` preview) is serialized onto the wire.

### 2.4 #711, re-verified against the code

Both claims in the dispatch description verify:

1. **Detail crosses the wire unconditionally** — `pkg/gateway/websocket_forward_hub.go::hubError` sets `Detail: &detail` on every `ErrorFrame` it publishes. The SPA hides detail unless Verbose chat is on (gating: `src/lib/llm-error.ts::getLLMErrorDisplay` + the mount conditions in `src/components/chat/MessageItem.tsx` and `ChatScreen.tsx::VirtualAssistantMessageRow`), but "hidden in the UI" ≠ "not sent": the scrubbed body preview is in the browser's memory, devtools, and any WS log. **Verified.**
2. **The scrubber is a value-matching net, not a classifier** — `pkg/logger/sensitive.go::ScrubSensitiveValues` replaces only registered, complete credential values; its own doc comment states it cannot recognize never-registered or masked/truncated values. A provider echoing a **fragment** of an unregistered or truncated key passes through into `buildDetail`'s preview. **Verified** (code + its own doc). Note the doc's conclusion, which the improvement should adopt: "a surface a person or a model reads must never carry raw provider text in the first place; this is the net under the operator-only diagnostics."

Verbose chat is stored **client-only** (`src/store/chatPreferences.ts` — zustand persist to localStorage; no server sync), so the gateway cannot currently know whether a receiving tab wants detail. Any "gate at the source" design needs a new, explicit signal (a per-connection or per-tab flag), or the body must stop crossing the wire entirely.

---

## Part 3 — Comparison, options, decisions

### 3.1 Where Omnipus's user-facing experience is worse than OpenCode's (concrete)

| # | Topic | OpenCode | Omnipus today | Consequence |
|---|---|---|---|---|
| 1 | Retry-after / rate-limit headers | Parsed (`retry-after-ms`, `retry-after`, `x-ratelimit-*`) into structured facts; live countdown in UI | `ProviderError` keeps no headers; 429 → generic copy | User waits blindly or hammers Retry into a wall |
| 2 | Actionable object | `Retryable.action {title, message, label, link}` → clickable "Subscribe"/"Open settings" buttons | Prose-only catalogue copy | Every next step is described, none is offered |
| 3 | Billing/quota | Rewritten to "Quota exceeded. Check your plan and billing details." + upsell action | Quota → `rate_limited`: "temporarily overloaded. Wait a moment, then retry." | **Actively wrong advice** for an exhausted account |
| 4 | Auth identity | `ProviderAuthError{providerID}`; ACP converts to an auth prompt | Copy says "this provider" without naming it; no deep link | Operator hunts for which of several keys is bad |
| 5 | Retry visibility | `session.status {type:"retry", attempt, next}` event → countdown "retrying in 14s (attempt 2 of 5)" | Inline retries (timeout-class, streaming resets, delegated 429 backoff) are **silent** | Turn appears hung; user clicks Stop or resends |
| 6 | Fallback transparency | n/a (no fallback chain) | Fallback success is log-only | Reply character changes with no explanation |
| 7 | Request ID | Captured (`x-request-id`, `cf-ray`, …) for support | Not captured | A reportable incident has no correlatable handle |
| 8 | Own-limiter vs provider 429 | — | Own SEC-26 limiter shows a **countdown** (`RateLimitIndicator`); provider 429s get none | Same failure class, two different experiences in one product |
| 9 | Overflow | Auto-compaction (error avoided) | `context_too_long` error (good copy); mid-turn guard exists (`context_unrecoverable`) | User does manual work the product could do |

### 3.2 Where Omnipus is already better — do not regress these

- **Attribution vocabulary** (`model/provider/product/config/ambiguous/unknown/user`) with generator-enforced bijection and copy-rule tests — OpenCode has nothing equivalent; its copy embeds fault-owner hints ad hoc.
- **Detail never persisted**: `LLMErrorReplay` strips `detail`; history replay can never re-leak it. OpenCode persists `responseBody` on the message record.
- **Two-surface discipline** (generic bubble copy vs Verbose-only detail) already exists and is tested; OpenCode shows raw body text as a matter of course.
- Curated typed exits (`turn_canceled` as a neutral notice, not an error toast).
- Stream-stall detection (`provider_stalled`, founder decision 2026-09-14) — OpenCode added only a header-timeout error recently.
- One contract block → two generated catalogues (Go + TS) that cannot drift.

### 3.3 What "everything relevant, non-technical" can mean

Concrete candidate facts, all provider- or loop-supplied, none requiring technical knowledge:

| Fact | Source | Non-technical rendering |
|---|---|---|
| "Come back in ~2 min" | `retry-after` header / `RateLimitReason.retryAfterMs` | live countdown on the bubble: "Retrying automatically in 1:32 (attempt 2 of 5)" or "Try again in ~2 min" |
| Which provider/account/key | loop context (candidate, key label) | "Grok via OpenRouter (key 'Work')" |
| Billing problem + where to fix | `FailoverBilling` classification / quota body | button "Open billing page" (`action.link`), copy "This provider says the account is out of credit" |
| Upgrade hint | provider body (`usage_not_included`) | provider-rewritten sentence + link |
| Model deprecation | provider body / catalog status | "This model is being retired in March — pick another" |
| Request ID | executor capture | "Report code `req_abc123`" under Verbose, or a Copy-code button |
| Attempt count | loop retry state | "attempt 2 of 5" in the retry line |
| What we did about it | fallback success | "Your agent's second model answered instead" — small footer note |

Note the pattern in OpenCode's `Retryable.action`: the **non-technical message and the affordance are the same object** — copy plus `{label, link}`. That is the shape "everything relevant, non-technical" wants.

### 3.4 Three design options

**Option A — "Facts on the wire" (recommended).** Extend the wire contract additively: `LLMError` gains an optional `facts` object (`retry_after_seconds`, `provider`, `model`, `key_label`, `request_id`, `attempts`, plus an `actions: [{label, url, kind}]` list) and a `quota_billing` code joins the enum. `ProviderError` captures response headers (retry-after, rate-limit buckets, request ID) at `HandleErrorResponse`; the classifiers thread them into the LLMError at the two choke points. The **raw body stops crossing the boundary entirely** — `buildDetail` is replaced by curated facts + status + code, and the full body stays in gateway.log (already scrubbed). #711 closes by construction: nothing raw is on the wire to leak; the scrubber remains as defense-in-depth for logs. Requires the SPA to render facts (countdown component exists in `RateLimitIndicator.tsx` to generalize). Cost: contract wave + both classifiers + SPA; the largest of the three.

**Option B — "Verbose is a session flag".** Keep today's shape; add a per-tab/per-connection `verbose` flag synced at WS attach; `hubError` includes `detail` only when the receiving tab declared verbose. #711 closes at the source. Facts/action objects are a separate follow-up. Cost: one flag + gateway gating + tests; fastest to land; but the detail remains a raw-ish body preview (scrubbed only), so the leak class (unregistered fragments in body text) survives on verbose tabs — the control is *who may see it*, not *what is in it*.

**Option C — "Curate harder".** No contract change: expand the substring classifier (billing code path, more pinned shapes), rewrite copy per provider family, keep detail as-is but tighten the scrubber (fragment/prefix matching). Cheapest, but #711's leak class is fundamentally unfixable this way — value-matching scrubbing can never enumerate what it doesn't know (the scrubber's own doc says so), and "everything relevant" is unreachable without structured facts. Listed for completeness; **not recommended**.

| | A: Facts on the wire | B: Verbose flag | C: Curate harder |
|---|---|---|---|
| Closes #711 | by construction (no raw body on wire) | at the source (who may see) | no (defense only) |
| "Everything relevant" | yes — structured facts + actions | no | partially |
| Contract change | additive (`facts`, 1 new code) | none | none |
| Effort | feature-size (contract wave, 2 classifiers, SPA) | standard | small |
| Risk | SPA rendering breadth; scrubber stays for logs | verbose UX still raw; second round likely | wrong-advice class (billing) persists |

### 3.5 Decisions the founder needs to make

1. **Doctrine.** Is the rule "generic copy always; provider facts only as structured data rendered as affordances; raw body never crosses the wire"? (My recommendation: yes — it makes #711 a structural non-issue and gives OpenCode's action-object ergonomics without OpenCode's leak.)
2. **Which provider facts may be named.** Provider display name, model, key label, request ID — yes/no per item? (Recommendation: all four, with key **material/fragments** never; naming the key label is what makes a 401 actionable.)
3. **Billing/quota as its own code.** Split `quota_billing` from `rate_limited` (additive enum change, new catalogue copy + "Open billing" affordance)? (Recommendation: yes — the current advice is wrong for the case.)
4. **Provider retry countdown.** Capture retry-after/rate-limit headers and render a provider-429 countdown, unifying with `RateLimitIndicator`? (Recommendation: yes.)
5. **Fallback transparency.** When a fallback model answers, does the user see a small note? (Recommendation: yes — one quiet line; `model_unavailable` copy already covers the failure sibling.)
6. **#711 landing order.** Land the structural fix (A) as one feature, or ship B first as the leak stop-gap and A after? (Recommendation: B only if a leak incident forces an immediate patch; otherwise A alone avoids building a throwaway gating path.)

---

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Canonical OpenCode repo is anomalyco/opencode, branch `dev`; clone at `696f41b…` | `gh repo view sst/opencode` → `"url":"https://github.com/anomalyco/opencode","defaultBranchRef":{"name":"dev"}` (exit 0); `git log -1` in clone → `696f41bc8e7586657375d53390925fc54c25d34c 2026-09-25 chore: update nix node_modules hashes` | Verified |
| Omnipus checkout state | `git rev-parse HEAD` → `d0383aae2922e4c9e39172188b088af218013e88`; `git describe --tags` → `nightly-5820-gd0383aae2` | Verified |
| OpenCode error taxonomy + APIError fields | Read `packages/core/src/v1/session.ts` (`APIError`, `ContextOverflowError`, `ContentFilterError`, `ProviderAuthError`) | Verified |
| Tagged error union in new llm layer | `packages/llm/DESIGN.md` "Error Model" section, lines 828–850 | Verified |
| OpenCode message rewriting incl. HTML-gateway auth hint | Read `packages/opencode/src/provider/error.ts` (`message()`, `parseStreamError`, `parseAPICallError`) | Verified |
| Overflow regex list + exclusions | Read `packages/llm/src/provider-error.ts` (`isContextOverflow`, `exclusions`) | Verified |
| Executor classification, redaction, request-id, rate-limit headers | Read `packages/llm/src/route/executor.ts` (`statusReason`, `redactBody`, `secretValues`, `requestId`, `rateLimitDetails`, `MAX_RETRIES = 2`) | Verified |
| Retry policy constants, header honoring, action object, humanized reset | Read `packages/opencode/src/session/retry.ts` (`RETRY_MAX_RETRIES`, `delay`, `retryable`, `Retryable.action`, `GO_UPSELL_URL`) | Verified |
| Retry status event shape with `next` timestamp | Read `packages/schema/src/session-status-event.ts` (`Info` union: `{type:"retry", attempt, message, action?, next}`) | Verified |
| Retry wiring in processor | Read `packages/opencode/src/session/processor.ts::process` (`Effect.retry(SessionRetry.policy…)`, `set` → `status.set({type:"retry",…})`) and `halt` (overflow → `needsCompaction`) | Verified |
| Web retry countdown card | Read `packages/session-ui/src/components/session-retry.tsx::SessionRetry` (per-second timer, 80-char truncation, gemini i18n case) | Verified |
| TUI action dialog | Read `packages/tui/src/component/dialog-retry-action.tsx::DialogRetryAction` (title/message/label/link buttons, "don't show again") | Verified |
| Web error card renders message-level error; tool errors via ToolErrorCard; client-side JSON re-parse | Read `packages/session-ui/src/components/session-turn.tsx::unwrap` + `errorText` memo; `packages/session-ui/src/components/message-part.tsx` (ToolErrorCard match) | Verified |
| ACP auth-required + refusal mapping | Read `packages/opencode/src/acp/service.ts` (~line 870–890: `ProviderAuthError` → `AuthRequiredError`; `ContentFilterError` → `stopReason: "refusal"`) | Verified |
| Omnipus ProviderError shape, 8 KiB cap, `Error()` rendering | Read `pkg/providers/common/common.go::ProviderError`, `handleErrorBodyCap`, `ProviderError::Error()` | Verified |
| Two classifiers with distinct vocabularies; providers side has `billing` | Read `pkg/providers/error_classifier.go::ClassifyError` + pattern blocks; `pkg/providers/types.go::FailoverReason` (`FailoverBilling` present) | Verified |
| 23-code catalogue, attributions, generator-enforced bijection, copy rules | Read `contracts/components/schemas/LLMError.yaml` (full file); mirrored in `contracts/asyncapi.yaml` (grep `x-user-message-attributions` hit line 2051) | Verified |
| Governing ADR is ADR-051 | `ls docs/internal/architecture/` → `ADR-051-media-handling-and-provider-error-translation.md` (+ review sibling) | Verified |
| Live frame always carries `detail` (#711 surface 1) | Read `pkg/gateway/websocket_forward_hub.go::hubError` (`Detail: &detail` unconditional; `hubSyncTap` routes `agent.EventKindError`) | Verified |
| Scrubber = registered complete values only (#711 surface 2) | Read `pkg/logger/sensitive.go::ScrubSensitiveValues` + `sensitiveValueReplacer` doc comment ("cannot recognise a value that was never registered, or one a provider masked or cut short"); registrars: `pkg/gateway/gateway_boot_credentials.go`, `rest_sign_in.go`, `rest_config.go`, `gateway_reload.go` (grep) | Verified |
| Verbose chat is client-only | Read `src/store/chatPreferences.ts` (zustand `persist`, localStorage, `partialize` to `verboseChatEnabled`) | Verified |
| Replay strips detail | Generated `pkg/api/generated/asyncapi_types.gen.go::LLMErrorReplay` (no `Detail` field); `src/lib/llm-error.ts::readLLMErrorFromReplayFrame` | Verified |
| SPA display gating + verbose disclosure + retry button | Read `src/lib/llm-error.ts::getLLMErrorDisplay`; `src/components/chat/MessageItem.tsx` (`ERROR_DETAIL_MAX_CHARS`, `<details>` mount conditions); `src/components/chat/ChatScreen.tsx::AssistantMessageRetryButton` (not gated on `retryable`) | Verified |
| Fallback success is log-only | Read `pkg/agent/loop_run_turn.go::callProviderOnce` (`logger.InfoCF("Fallback: succeeded with %s/%s after %d attempts")`) | Verified |
| Own limiter has countdown UI; provider 429 does not | Read `src/components/chat/RateLimitIndicator.tsx` (countdown, "Retry available" state); `pkg/agent/loop.go::recordRateLimitDenial` (RateLimitFrame producer) | Verified |
| Inline retries exist but are silent | Read `pkg/agent/loop_run_turn_response.go` (retry loop, 500 ms×2^n cap 4 s streaming-reset backoff); `pkg/agent/loop_provider_retry.go::delegatedRateLimitBackoff` (delegated 429 backoff, logged only) | Verified |
| Worked examples in §2.3 | Derived from `contracts/components/schemas/LLMError.yaml` copy + the classification paths cited per row; not executed against a live provider | Inferred (medium-high: every symbol in each path is Verified; only the end-to-end rendering is unexercised) |
| No model-deprecation concept in Omnipus | `grep -rn "deprecat" pkg/providers/ pkg/config/ --include="*.go"` (non-test) → no hits | Verified (absence check, two trees) |
| OpenCode model-status enum exists; where it surfaces in UI not traced | `packages/opencode/src/provider/model-status.ts` (re-export of `CatalogModelStatus`: alpha/beta/deprecated); UI surfacing not traced | Verified for the enum; **Unknown** for UI surfacing |
| 402 handling in `classifyByHTTPStatus` | Read `pkg/agent/translate_error.go::classifyByHTTPStatus` — 402 falls into the residual-4xx branch (status switch has no 402 case) | Verified |
| Quota body → `rate_limited` copy | Read `pkg/agent/translate_error.go::rateLimitSubstrings` (contains "quota exceeded") + `classifyByMessage` order | Verified |
| **Self-check** | Re-read the finished document against the dispatch's done-criteria: Part 1 covered error classification, retry/backoff + its display, user-vs-log text, provider-message pass-through/rewrite, redaction, UI presentation, good/not (§1.1–1.5); Part 2 mapped producers, both classifiers, contract, gateway frame, retries/fallback, SPA rendering with `file::symbol` citations and 5 worked examples (§2.1–2.4); Part 3 delivered the concrete worse-than comparison (9 rows), the "everything relevant" definition (8 facts), exactly 3 options each with its #711 closure story, and 6 founder decisions; every file cited was read in this task, both #711 claims re-verified in code, repo edits: none (one new file under `/Users/danielpiatkowski/AI-Agent-Workspace/research/` only) | Verified |

*Skills: omnipus-shared-rules.*

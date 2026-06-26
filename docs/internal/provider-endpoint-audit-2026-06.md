# Provider endpoint audit — multi-endpoint & onboarding-probe gaps (2026-06-26)

Triggered by the `z-ai`/GLM onboarding bug (fixed in `373e0d2c`). This audits **all**
providers for the same class of problem: (a) onboarding-probe id ↔ backend-base
mismatches, and (b) providers that have **multiple regional API endpoints** (China vs
international) where only one is wired or surfaced.

## How the probe resolves a base (the root mechanism)

`POST /onboarding/probe-provider` (`pkg/gateway/rest_onboarding.go`) resolves the
upstream base via **`providers.GetDefaultAPIBase(id)`** and then `GET {base}/models`
(`fetchUpstreamModels`, `pkg/gateway/rest.go`). If `GetDefaultAPIBase` returns `""`
and the SPA sent no `endpoint` override (onboarding never does), the handler replies
**400 `unknown provider "<id>"`**. The SPA's `friendlyProbeError` only special-cases
**401/403/429**; every other error (incl. that 400) falls through to the generic
**"Couldn't reach <provider>."** — so a *wiring gap reads as a network failure*. This
is exactly what hid the `z-ai` bug, and it hides the ones below too.

Key asymmetry: the **runtime** provider factory (`CreateProviderFromConfig`) hardcodes
bases for `anthropic`/`azure`/`bedrock` itself, so *chat works at runtime*; but the
**probe** path uses `GetDefaultAPIBase`, which does **not** know them. So the break is
onboarding-only (no model-list fetch, scary error), not a runtime break.

## Findings (live-probed against the running gateway)

### Category A — `z-ai`-class onboarding-probe breaks (HIGH)

`GetDefaultAPIBase` has **no case** for these, yet they're reachable in onboarding:

| Provider id | In onboarding UI? | Probe result | Impact |
|---|---|---|---|
| `anthropic` | **Yes** | `unknown provider "anthropic"` | **HIGH** — Anthropic is a top provider. Onboarding test fails + no model dropdown; user sees "Couldn't reach Anthropic". Runtime chat still works (factory hardcodes the base). |
| `azure` | **Yes** | `unknown provider "azure"` | **MEDIUM** — Azure has no fixed default base (per-resource host); it *requires* an endpoint field. The UI offers it but neither requires an endpoint nor explains the failure. |
| `anthropic-messages` | No (config-only) | `unknown provider` | LOW — not in onboarding. |
| `bedrock` | No | n/a (AWS SDK creds, not a base probe) | LOW — different auth model. |

**Nuance for anthropic:** adding the base (`https://api.anthropic.com/v1`) is necessary
but maybe not sufficient — Anthropic's `GET /v1/models` wants an `anthropic-version`
header that `fetchUpstreamModels` does not send (it sets `Authorization` + `X-Api-Key`
only). Anthropic probing likely needs a small special-case, not just a base entry.

### Category B — Chinese providers with China-vs-international endpoint splits (MEDIUM)

These vendors run **two separate platforms** (different hosts AND different keys). All
endpoints below were confirmed live (HTTP 401 = reachable, needs auth):

| Vendor | International (wired) | China (separate platform) | Onboarding offers | Gap |
|---|---|---|---|---|
| **Zhipu / Z.ai** | `api.z.ai/api/paas/v4` (`z-ai`) | `open.bigmodel.cn/api/paas/v4` (`zhipu`) | **both** ✅ (post-fix) | None functionally; user must pick the one matching their key (UX confusion risk). |
| **Moonshot / Kimi** | `api.moonshot.ai/v1` (`moonshot`) | `api.moonshot.cn/v1` (NOT wired) | intl only | A **China Moonshot** key cannot onboard (no `.cn` option / no endpoint field surfaced). |
| **MiniMax** | `api.minimax.io/v1` (`minimax`) | `api.minimaxi.com/v1` (NOT wired) | intl only | A **China MiniMax** key cannot onboard. |
| **Qwen / DashScope** | `dashscope-intl.aliyuncs.com` (`qwen-intl`), `dashscope-us` (`qwen-us`) — wired but **NOT in onboarding** | `dashscope.aliyuncs.com` (`qwen`) — wired **and** the only one in onboarding | China only in UI | An **international DashScope** user picking "Qwen" gets the **China** endpoint → likely auth/region failure. The intl/US variants exist in the backend but aren't selectable in onboarding. |
| **DeepSeek** | `api.deepseek.com/v1` (single global) | — | yes | None — single global endpoint. |

### Category C — misleading error mapping (MEDIUM, the amplifier)

`friendlyProbeError` (`src/routes/onboarding.tsx`) maps only 401/403/429. Observed live:
- `google`/Gemini probe with a bad key returns **upstream 400** → shows "Couldn't reach
  Google Gemini" (not a network issue).
- Every Category-A/B failure → same generic "Couldn't reach …".

This mapping is why these bugs are invisible until you read server logs. It should:
1. Distinguish **"provider not supported / needs a custom endpoint"** (the 400
   `unknown provider`) from a real network error.
2. Handle upstream **400/404/5xx** with a clearer hint than "couldn't reach".
3. Always surface the raw error behind the existing "Technical details" disclosure
   (it already preserves it — good).

### Category D — backend-only Chinese/regional providers (INFO, not bugs)

Configurable only via `config.json` / `PUT /providers` (never onboarding), several are
**single, China-only** endpoints — same multi-endpoint risk if the vendor has an intl
platform: `volcengine` (`ark.cn-beijing.volces.com` — China only; BytePlus ModelArk is
the intl variant, not wired), `modelscope` (`api-inference.modelscope.cn` — China),
`mimo`/Xiaomi (`api.xiaomimimo.com`), `longcat`/Meituan (`api.longcat.chat`),
`shengsuanyun`. Also `novita`, `vivgrid`, `avian`, `cerebras`, `groq` (single global).

### Category E — provider-id canonicalization is fragile (LOW)

The same vendor is reachable under several ids that are resolved by *different*
mechanisms: `z.ai`/`z-ai`/`zai`/`zhipu`, and `qwen`/`qwen-intl`/`qwen-international`/
`dashscope-intl`/`qwen-us`/`dashscope-us`/`qwen-coding`/`coding-plan`. `model_ref.go`
normalizes some (`z.ai`→`zai`) while the factory + `GetDefaultAPIBase` switch on raw
strings. A single canonical-id + alias table (one source of truth) would prevent the
next "id known in one place, unknown in another" bug.

## Recommendations (by priority)

1. **Make the probe base-resolver complete & authoritative.** Either add the missing
   cases to `GetDefaultAPIBase` (`anthropic` → `https://api.anthropic.com/v1`, with the
   `anthropic-version` header special-case in `fetchUpstreamModels`), **or** add a
   probe-time invariant test: *every id in `ProbeProviderRequest.enum` and the
   onboarding `LOCAL_PROVIDERS` list must resolve a non-empty base OR be explicitly
   flagged "requires endpoint"*. That test would have caught `z-ai`, `anthropic`, `azure`.
2. **Surface an endpoint field in onboarding** for providers flagged "needs/optional
   endpoint" (azure mandatory; moonshot/minimax/qwen optional override for the China or
   US host). The probe already accepts `endpoint`; the UI just never sends it.
3. **Add the China variants** as first-class ids where a separate platform exists:
   `moonshot-cn` (`api.moonshot.cn/v1`), `minimax-cn` (`api.minimaxi.com/v1`), and
   surface the existing `qwen-intl`/`qwen-us` in the onboarding list. Mirror the
   `zhipu`/`z-ai` split.
4. **Fix `friendlyProbeError`** to distinguish unsupported-provider / needs-endpoint /
   upstream-4xx-5xx from "couldn't reach" (Category C) — cheap, high UX value.
5. **One canonical provider-id + alias table** (Category E) as the single source the
   factory, `GetDefaultAPIBase`, the probe enum, and the onboarding list all derive from.

## Quick-win vs structural

- **Quick win (low risk):** add `anthropic` to `GetDefaultAPIBase` + the probe enum +
  the `anthropic-version` header; fix `friendlyProbeError`. Unblocks the most common
  provider's onboarding.
- **Structural (the durable fix):** the invariant test (#1) + the canonical alias table
  (#5) + onboarding endpoint field (#2). These prevent the entire bug class, not just
  the instances found today.

## Evidence
All rows above were live-probed via `POST /api/v1/onboarding/probe-provider` against the
fresh gateway and direct `curl` to each vendor's `/models` endpoint on 2026-06-26.

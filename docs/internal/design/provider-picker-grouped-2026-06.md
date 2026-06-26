# Onboarding provider picker — grouped by company, plan + region variants

**Design mode** (elicify-UI-UX-Design). Problem: the flat ~20-button provider grid is
about to grow to 30+ because Chinese vendors expose multiple endpoints that are
**separate accounts/billing** — `{Pay-as-you-go API | Coding Plan | Anthropic API} ×
{International | China}`. A flat list violates **Hick's Law** (decision time grows with
choice count) and **Miller's Law** (working memory ~4–7 chunks). A user with a Coding
Plan key who picks the pay-per-token endpoint hits `1113 insufficient balance` — an
error we must **prevent by design** (Wroblewski/NN-g: prevent before message).

## Information architecture — two-level progressive disclosure

- **L1 — Company** (recognition, chunked to ~14 tiles): OpenAI, Anthropic, Google,
  OpenRouter, Groq, Mistral, DeepSeek, **Zhipu/GLM**, **Moonshot/Kimi**, **MiniMax**,
  **Qwen**, NVIDIA, Cerebras, Azure, Ollama, Other/Custom. One tile per *company*, not
  per endpoint. Tiles with >1 variant show a ▾ affordance.
- **L2 — Plan + Region** (conditional, inline): appears only when the selected company
  has >1 variant. Two segmented controls:
  - **Plan**: `Pay-as-you-go API` · `Coding Plan` · `Anthropic API` (only those offered)
  - **Region**: `International` · `China` · `US` (only those offered, and only for the
    chosen plan)
  Single-option companies skip L2 entirely → straight to the key field (keep simple).

This keeps the **spine** (happy-path Stage 1): pick company → [plan/region if needed] →
API key (+ endpoint for Azure) → Connect & load models → model → Continue.

## Error prevention (the `1113` pitfall)
A one-line helper under the Plan selector, recognition over recall:
> ⓘ **Coding Plan** = your GLM/Kimi *subscription* (separate from the token API).
> **Pay-as-you-go API** bills per token. Pick the plan your key is for — the wrong one
> returns "insufficient balance".
Default Plan = **Pay-as-you-go API** (most common); Region default = **International**.
Defaults are highlighted (Default/Status-Quo bias) but all options visible.

## ASCII mockup (onboarding step 3)
```
┌─ Add a model key ─────────────────────────────── step 3/3 ─┐
│  Choose your provider                                       │
│                                                             │
│  [ OpenAI ]  [ Anthropic ]  [ Google ]  [ OpenRouter ]      │
│  [ Groq ]    [ Mistral ]    [ DeepSeek ▾][ Zhipu / GLM ▾ ]  │
│  [ Moonshot / Kimi ▾ ] [ MiniMax ▾ ] [ Qwen ▾ ] [ NVIDIA ]  │
│  [ Cerebras ][ Azure ][ Ollama ][ Other… ]                  │
│                                                             │
│  ┌ Zhipu / GLM ─────────────────────────────── selected ─┐ │
│  │ Plan    (·Pay-as-you-go API) ( Coding Plan ) (Anthropic API)│
│  │ Region  (·International) ( China )                       │ │
│  │ ⓘ Coding Plan = your GLM subscription. Pay-as-you-go    │ │
│  │   bills per token. Wrong plan → "insufficient balance". │ │
│  │ API key [ ••••••••••••••••••••••••••  👁 ]              │ │
│  │         [  Connect & load models  ]                     │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```
(`·` = default-selected segment, Forge-Gold accent. Single-option company: clicking the
tile shows the API-key row directly, no Plan/Region rows.)

## Interaction
- Click a company tile → it becomes selected (Forge-Gold ring). If multi-variant, the
  inline panel expands **in place** (Framer height-auto, ≤200ms); if single, just the
  key row. Recognition: the resolved endpoint is shown small under the helper
  ("→ api.z.ai/api/coding/paas/v4").
- Plan/Region are segmented controls (reuse the UsageScreen period-selector style);
  switching them re-resolves the backend `id` and clears any loaded model list.
- Search: a single text filter above the grid (NN/g: >25 items → searchable). Filters
  company tiles by name/alias (e.g. "kimi", "glm", "qwen").

## Data model (frontend)
Replace the flat `AVAILABLE_PROVIDERS` (`{id, display_name}`) with variant rows:
```ts
type ProviderVariant = {
  id: string                 // backend protocol id sent to the probe/complete
  company: string            // grouping key + tile label, e.g. "Zhipu / GLM"
  aliases?: string[]         // for search: ["glm","z.ai","zhipu","bigmodel"]
  plan: 'api' | 'coding' | 'anthropic'
  region?: 'intl' | 'china' | 'us'   // omit for single-region companies
  endpointHint?: string      // shown small for recognition
}
```
The UI derives company tiles from `distinct(company)`; for the selected company it
derives the Plan options from `distinct(plan)` and, per plan, the Region options. The
resolved `id` = the row matching (company, plan, region). Single-variant company → its
one row.

Labels: `api`→"Pay-as-you-go API", `coding`→"Coding Plan", `anthropic`→"Anthropic API";
`intl`→"International", `china`→"China", `us`→"US".

## Backend id matrix (companies with variants)
| Company | plan | region | backend id | base |
|---|---|---|---|---|
| Zhipu/GLM | api | intl | `z-ai` | api.z.ai/api/paas/v4 |
| Zhipu/GLM | api | china | `zhipu` | open.bigmodel.cn/api/paas/v4 |
| Zhipu/GLM | coding | intl | `z-ai-coding` | api.z.ai/api/coding/paas/v4 |
| Zhipu/GLM | coding | china | `zhipu-coding` | open.bigmodel.cn/api/coding/paas/v4 |
| Zhipu/GLM | anthropic | intl | `z-ai-anthropic` | api.z.ai/api/anthropic/v1 |
| Zhipu/GLM | anthropic | china | `zhipu-anthropic` | open.bigmodel.cn/api/anthropic/v1 |
| Moonshot/Kimi | api | intl/china | `moonshot` / `moonshot-cn` | api.moonshot.{ai,cn}/v1 |
| Moonshot/Kimi | anthropic | intl/china | `moonshot-anthropic` / `moonshot-cn-anthropic` | api.moonshot.{ai,cn}/anthropic/v1 |
| MiniMax | api | intl/china | `minimax` / `minimax-cn` | api.minimax.io , api.minimaxi.com /v1 |
| MiniMax | anthropic | intl/china | `minimax-anthropic` / `minimax-cn-anthropic` | …/anthropic/v1 |
| DeepSeek | api / anthropic | — | `deepseek` / `deepseek-anthropic` | api.deepseek.com/v1 , /anthropic/v1 |
| Qwen | api | china/intl/us | `qwen` / `qwen-intl` / `qwen-us` | dashscope[-intl/-us].aliyuncs.com/compatible-mode/v1 |
| Qwen | anthropic | — | `coding-plan-anthropic` (existing) | coding-intl.dashscope.aliyuncs.com/apps/anthropic |

`*-anthropic` ids route to the **anthropic_messages** provider (POST `/messages`); all
others are OpenAI-compatible. Anthropic endpoints may not expose an OpenAI `/models` →
model dropdown falls back to free-text (acceptable; documented).

## Pitfalls to avoid
- Don't deep-nest (no 3-level accordions): two flat segmented rows max.
- Don't hide the resolved endpoint — show it small (recognition, debuggability).
- Don't default to a China endpoint for an international user (or vice-versa) silently.
- Keep single-option providers one click — don't make OpenAI users pick a "plan".
- Reset the loaded model list when plan/region changes (stale models = wrong picks).

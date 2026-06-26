# Cost tracking: how opencode.ai / OpenClaw (and the field) do it — research

**Question:** how to track LLM cost when (a) every provider/model has different pricing, and (b) users may be on a *subscription* (flat-rate, not per-token). Sourced from the opencode.ai repo (sst/opencode), the OpenClaw migration schema in this repo, and 2026 web research.

## 1. The dominant pattern — a per-(provider, model) price table + token×rate

Both tools, and the field generally, do the same core thing:

- **A price table keyed by (provider, model)** giving per-token rates split into **4 classes**: `input`, `output`, `cache_read`, `cache_write` — expressed as **$ per 1,000,000 tokens**.
- **opencode** pulls this from **[models.dev](https://models.dev)** — the de-facto community-maintained model catalog (`packages/core/src/models-dev.ts`; cached locally). Schema (`Cost`): `{input, output, cache_read?, cache_write?}`.
- **OpenClaw** embeds the same idea directly in its provider/model config — the migration schema in `pkg/migrate/sources/openclaw/openclaw_config.go:53` is `Cost{ Input float64; Output float64 }` per model. Same model, simpler (input/output only in the migrated subset).
- **Cache pricing matters:** cached input is ~90% cheaper than fresh input; `cache_write` is what you pay to store a prompt prefix, `cache_read` to reuse it (Anthropic even has 5m/1h cache-write tiers — opencode's `infra/stats.ts` carries `cache_write_5m`/`cache_write_1h`).

### The cost formula (opencode `session.ts:385-410`)
```
cost = input_tokens     × rate.input       / 1e6
     + output_tokens    × rate.output      / 1e6
     + cache_read_tokens× rate.cache.read  / 1e6
     + cache_write_tokens× rate.cache.write/ 1e6
     + reasoning_tokens × rate.output      / 1e6   // reasoning billed at output rate (a TODO)
```
- Computed with `Decimal` for precision; **stored in microcents** (1e-8 $) and aggregated to session/day/model in a stats pipeline.
- `rate.X ?? 0` — **the key fallback: no price data → that term is 0 → cost $0.** (Confirmed by opencode issue #17223: custom-provider models with no models.dev entry get no cost tracking.)

## 2. "Different providers, different pricing" — solved by

1. **The table is keyed by (provider, model)** — each entry has its own rates.
2. **Context-tier pricing** (the subtle part): rates can change with context size. opencode resolves a tier per request:
   - `cost.tiers[]` — pick the tier whose `tier.size` the current context exceeds (e.g. Gemini's long-context premium).
   - `cost.experimentalOver200K` — a special higher rate for >200k-token context (Claude long-context).
   So the "price" isn't one number — it's **resolved per request from the actual context length**.
3. **Unknown/custom models** → fall through to `?? 0` → $0 (no silent wrong number).

## 3. The subscription / not-per-token problem — THREE strategies

This is the hard part you flagged. The field handles it three ways, often combined:

**(a) Track the provider's *own* usage unit (most accurate for subscriptions).**
When a subscription meters in its own currency, track THAT, not dollars. opencode does exactly this for **GitHub Copilot**: it reads `metadata.copilot.totalNanoAiu` (Copilot's "AI Units" / premium-request quota) and converts `nanoAiu / 1e11` instead of using token×price. Copilot's models carry a synthetic `usdPerMillion = 10_000 / batch_size`. Claude Max similarly meters in "5-hour usage windows" + a separate API-credit pool.

**(b) Show the *equivalent API cost* as a reference.**
For OAuth/subscription providers where models.dev still has pricing, compute the token×price figure anyway — it represents *"what this would have cost on the pay-per-token API."* Useful "value tracking" even though you pay a flat fee. (auth `type: ["oauth","api"]` is tracked; the cost formula is uniform — only the *source* differs.)

**(c) $0 / "included".**
No pricing + no native unit → cost 0 (subscription = included). Simplest, least informative.

**2026 industry trend (relevant):** pure flat-rate is *ending*. Cursor (Jun 2025), OpenAI Codex (Apr 2026), and Copilot (Jun 2026) all moved to **credit/token-based or hybrid** billing; Claude Max added a separate API-credit pool for SDK/headless use. So even "subscriptions" increasingly have a metered component — making strategy **(a) track the provider's native unit** the durable approach, with **(b)** as the human-readable dollar estimate.

## 4. Storage / aggregation
- Cost computed **at response time** from the provider's `usage` tokens × the resolved rate.
- Accumulated into **session totals** (opencode: `SessionTable.cost += value.cost`) and a **stats rollup** (by day/model/provider, microcents).
- Display divides microcents → dollars at the edge.

## 5. Where Omnipus stands + recommendation
- **Today:** Omnipus already *computes* a per-request `costUSD` and keeps a single **in-memory daily-total accumulator + cap** (`pkg/security/ratelimit.go` `dailyCostUSD`, `CheckGlobalCostCap`). It has **no history store** — which is exactly why `query_cost` is a NOT_IMPLEMENTED stub.
- **To implement `query_cost` properly:**
  1. **Pricing table:** adopt a models.dev-style per-(provider, model) rate table (input/output/cache, with context-tier support). Omnipus's provider config can carry/resolve it; the OpenClaw migration already imports `Cost{input,output}`. Consider syncing models.dev (cache locally) so new models get prices for free.
  2. **Per-request record:** at each response, persist `{ts, session, agent, provider, model, tokens{in,out,cache_r,cache_w,reasoning}, cost_microcents, auth_type, native_unit?}` — JSONL append (fits Omnipus's file-based storage), aggregated on read.
  3. **`query_cost`:** read + group that store by period/agent/model — the only thing it lacks today.
  4. **Subscriptions:** store BOTH the tokens and, when the provider reports a native unit (Copilot credits / Claude window usage), that unit. Report dollar cost as **"estimated (API-equivalent)"** with a flag when on a subscription; `$0 / included` when no pricing.

## Sources
- opencode.ai repo (sst/opencode): `packages/core/src/models-dev.ts`, `packages/opencode/src/session/session.ts:385-410`, `packages/opencode/src/plugin/github-copilot/models.ts`, `packages/core/src/session/projector.ts`, `infra/stats.ts`.
- OpenClaw schema: `pkg/migrate/sources/openclaw/openclaw_config.go` (`Cost{Input,Output}`).
- Web: [models.dev](https://models.dev) · [opencode #17223 custom-provider cost](https://github.com/anomalyco/opencode/issues/17223) · [opencode-tokenscope cost calculator](https://deepwiki.com/ramtinJ95/opencode-tokenscope/6.2-cost-calculator) · [Portkey: opencode token usage & costs](https://portkey.ai/blog/opencode-token-usage-costs-and-access-control/) · [Flat-rate AI coding era ending (2026)](https://medium.com/activated-thinker/the-flat-rate-ai-coding-subscription-era-is-ending-what-github-copilot-claude-code-and-cursor-9763e043a63a) · [Claude Code pricing / Max plans 2026](https://www.nxcode.io/resources/news/claude-code-pricing-2026-free-api-costs-max-plan) · [GitHub Copilot plans](https://github.com/features/copilot/plans).

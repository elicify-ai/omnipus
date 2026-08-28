// providersCatalog.ts — the shared SPA test fixture for the registry-fed
// providers catalog (ADR-067 schema 2.0.0, served by GET /providers/catalog).
//
// ADR-068 T068-18. This REPLACES the 21-entry `providersCatalog.stub.ts` that
// T068-05 shipped as a placeholder: `providers-catalog.json` next to this file
// is the 190-entry document every ADR-068 SPA test renders against (spec
// §"Development": "a 190-entry JSON fixture under
// src/test/fixtures/providers-catalog.json generated once from the real
// snapshot"). The JSON is currently a hand-built stand-in shaped exactly like
// the real snapshot; when B2-RELEASE exists it is regenerated from the real
// document and this module is unchanged.
//
// Shape guarantees the fixture makes — later tasks may rely on all of these,
// and `providersCatalog.test.ts` pins every one of them:
//
//   • exactly 190 providers, ids unique;
//   • exactly 8 `tier: "popular"` providers, each a DISTINCT `company`
//     (openai, anthropic, google, openrouter, groq, deepseek, xai, zai) —
//     FR-022's 8 Popular tiles;
//   • `bedrock` is `tier: "unsupported"` with `unsupported_reason: "cloud-iam"`
//     (FR-025), alongside `deployment-url` and `withdrawn` examples;
//   • `zai` / `zai-coding-plan` / `zhipuai` / `zhipuai-coding-plan` share
//     `company: "Zhipu AI"` and differ by `plan` × `region` — the L1→L2
//     grouping dataset; aliases include `glm-coding` and `智谱`;
//   • `openai-chatgpt`, `codex-cli` and `github-copilot` are the only
//     `auth_methods: ["sign_in"]` rows (`xai` is key-only until registration,
//     ADR-068 §8b D3); `codex-cli`/`github-copilot` are `protocol: "cli"` with
//     `cli_kind` `codex`/`copilot`. The OpenAI family is split into three
//     single-variant companies ("OpenAI", "ChatGPT", "Codex CLI") so the
//     `openai` API-key row stays a one-click entry; T068-21 may regroup the
//     pair under one company when the segmented sign-in control lands.
//     `github-copilot` sits with `github-models` under "GitHub";
//   • `vllm` and `litellm` are deliberately ABSENT — the provider-migration
//     dataset uses them as ids that must fall through to Self-hosted / Custom;
//   • `openrouter.models` carries the "vendor then release date" ordering
//     dataset verbatim: `anthropic/claude-sonnet-4.6` (2026-02),
//     `anthropic/claude-3.5-haiku` (2024-10), `openai/gpt-5.4` (2026-03) and
//     `x/nodate` (NO `release_date`, `tool_call: false`);
//   • every model carries `context_window`, `max_output_tokens`,
//     `input_modalities`, `tool_call` and `status` (`release_date` is optional
//     and deliberately absent on `x/nodate`; `o4-mini` is `status: "retired"`);
//   • names cover A–Z plus a leading-digit group (`01.AI`, `302.AI`) for the
//     picker's letter headers.
//
// Typed against the generated ProvidersCatalog (Constraint #8) so contract
// drift is a compile error rather than a runtime surprise.

import type { CatalogProvider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import fixture from './providers-catalog.json'

/** The whole 190-entry catalog document, envelope included. */
export const PROVIDERS_CATALOG = fixture as unknown as ProvidersCatalog

/** Just the 190 provider entries — the array most component tests want. */
export const CATALOG_PROVIDERS: CatalogProvider[] = PROVIDERS_CATALOG.providers

# Provider media-format support matrix — freeze-gate re-validation (2026-07-28)

**Date:** 2026-07-28
**Status:** freeze-gate re-validation, triggered by an operator-reported discrepancy on `glm-5.2`
**Author:** backend-lead (capability-catalog revalidation task)
**Traces to:** ADR-051 Rev 4 §Capability source (FR-024), `pkg/providers/capabilities/`
**Prior snapshot:** `docs/internal/research/provider-media-format-support-2026-07.md` (2026-07-23, 70-model freeze).

## 1. Trigger and scope

The operator reported from real-use experience that `glm-5.2` is used for vision via OpenRouter "regularly," and asked whether the seed's `glm-5.2: ["text"]` entry (unchanged since the 2026-07-23 freeze) is wrong. This report re-validates the whole GLM/z.ai family against current (2026-07-28) official docs, then does a bounded sanity pass over the rest of the catalog, prioritizing Omnipus's own configured default provider list (`pkg/config/defaults.go`'s `Providers []*ModelConfig` template) and each tracked vendor's current frontier line.

**This is NOT a full 70-model re-verification** — see §4 for exactly what was and was not checked.

## 2. GLM/z.ai family — verdict per model

All six non-V GLM models were re-checked by **directly fetching** their respective `docs.z.ai/guides/llm/<model>` page on 2026-07-28:

| Model | Verdict | Source (fetched 2026-07-28) |
|---|---|---|
| `glm-5` | **text-only** (correct, unchanged) | `docs.z.ai/guides/llm/glm-5` — verbatim: "Input Modalities: Text" |
| `glm-5.1` | **text-only** (correct; upgraded from "unverified" to verified) | `docs.z.ai/guides/llm/glm-5.1` — verbatim: "Input Modalities: Text" / "Output Modalities: Text" |
| `glm-5.2` | **text-only** (correct — the seed is NOT wrong; see §3) | `docs.z.ai/guides/llm/glm-5.2` — verbatim: "Input Modalities: Text". Corroborated by `openrouter.ai/z-ai/glm-5.2` ("supports text input and output with a 1M-token context window") and a third-party explainer (`glm5.app/blog/is-glm-5-2-multimodal`) — verbatim: *"No. GLM 5.2 is text-in, text-out only. No image input."* |
| `glm-4.7` (+ `-Flash`, `-FlashX` variants) | **text-only** (correct, unchanged) | `docs.z.ai/guides/llm/glm-4.7` — verbatim: "Input Modalities: Text" for all three variants |
| `glm-4.6` | **text-only** (correct, unchanged) | `docs.z.ai/guides/llm/glm-4.6` — verbatim: "Text" |
| `glm-4.5` | **text-only** (correct, unchanged) | `docs.z.ai/guides/llm/glm-4.5` — verbatim: "Input Modalities: Text" |
| `glm-5v-turbo` | **image+video-capable** (correct, unchanged) | `docs.z.ai/guides/vlm/glm-5v-turbo` — verbatim: "Video / Image / Text / File"; released April 2026 as z.ai's dedicated vision product |
| `glm-4.6v` / `-flash` / `-flashx`, `glm-4.5v` | **image-capable** (correct, unchanged) | Prior 2026-07-23 freeze's direct fetch of `docs.z.ai/guides/vlm/glm-4.5v` stands; corroborated by VentureBeat's GLM-4.6V coverage (native multimodal tool-calling, "large"/"Flash" split) |

Searched but found **no** `glm-4.7v` — z.ai's vision line skipped a 4.7V and went straight from GLM-4.6V/4.5V to GLM-5V-Turbo (April 2026). The seed's existing V-model roster is current; nothing to add there.

**Verdict: the GLM section of the seed required zero changes.**

## 3. Why the operator's real-use observation does not override the docs

Three independent, directly-fetched sources (z.ai's own docs, OpenRouter's own model page, and a dedicated third-party "Is GLM 5.2 multimodal?" explainer) agree `glm-5.2` is text-only. The most likely explanation for "it works via OpenRouter" is the exact mechanism the operator themselves described: agents dispatch with the provider-prefixed string `z-ai/glm-5.2`, `Catalog.Resolve` does an exact-map lookup against the bare seed key `glm-5.2`, misses, and falls through to `optimistic()` (`text`, `image`) — see `pkg/providers/capabilities/catalog.go:635-654`. That optimistic default means an image block gets attached and sent on every request regardless of the target model's real capability. Two ways that can produce "it works":

- OpenRouter/z.ai silently drops or ignores an unsupported content block rather than hard-rejecting it, and the model answers from the text prompt/conversation context alone — indistinguishable from "vision working" unless the tester asks about visual detail the model could not have gotten from text.
- The turn succeeds textually either way (the image being ignored doesn't itself raise an error in every provider integration path), so a casual check ("did I get a coherent reply") doesn't surface the gap.

No source found supports `glm-5.2` genuinely parsing image content. **Recommendation: do not flip `glm-5.2` (or any GLM non-V model) to image-capable.** The real bug is the unfixed provider-prefix mismatch in `Catalog.Resolve`, which is a separate, deliberately out-of-scope concern for this pass (see the hazard comment added to `catalog.go` near `Resolve`).

## 4. Wider catalog sanity pass — coverage and findings

**Checked** (prioritizing `pkg/config/defaults.go`'s seeded provider template list plus each tracked vendor's current frontier docs page):

| Model | Verdict | Source |
|---|---|---|
| `gpt-5.4` (OpenAI) | **missing from seed**, vision-capable — ADDED | `openai.com/index/introducing-gpt-5-4/`; `developers.openai.com/api/docs/models` ("current vision-enabled models are the o-series ... GPT-5 series ..."); released March 5, 2026 |
| `claude-sonnet-4-6` (Anthropic) | **missing from seed**, vision+PDF-capable — ADDED | `platform.claude.com/docs/en/docs/about-claude/models/overview` (direct fetch, redirected from `docs.anthropic.com`) — official "Claude API ID: claude-sonnet-4-6", legacy-tier but currently available; page states "All current Claude models support text and image input ... and vision" |
| `claude-opus-5`, `claude-sonnet-5`, `claude-fable-5`, `claude-haiku-4-5` (Anthropic) | **missing from seed** — these are Anthropic's CURRENT flagship generation (Fable 5 GA since June 9, 2026; Opus 5 / Sonnet 5 are the "Latest models" table). All vision+PDF-capable — ADDED | Same fetch as above |
| `claude-mythos-5`, `claude-mythos-preview` | real but invitation-only / limited availability (Project Glasswing) — **not added**, low priority for a general default catalog | Same fetch |
| `kimi-k2.5` (Moonshot) | **missing from seed**, vision-capable — ADDED | `platform.kimi.ai/docs/guide/use-kimi-vision-model` (direct fetch, redirected from `platform.moonshot.ai`) explicitly lists `kimi-k2.5` among vision-capable model IDs; corroborated by Moonshot's own GitHub (`MoonshotAI/Kimi-K2.5`), HuggingFace, and InfoQ/DeepLearning.AI coverage (native MoonViT vision encoder, image+video input) |
| `kimi-k2.6`, `kimi-k2.7-code`, `kimi-k2.7-code-highspeed` | mentioned once in the same Kimi vision-doc fetch, but **not independently corroborated by a second source** — **not added**, flagged for a future pass | `platform.kimi.ai` (single source only) |
| `minimax-m2.5` (MiniMax) | **missing from seed**, text-only (consistent with the M2/M2.1/M2.7 family; only M3 has vision) — ADDED | Two independent sources: a WebSearch-aggregated summary and a direct fetch of `help.apiyi.com/en/minimax-m27-no-image-input-analysis-en.html`, which states in passing "This is consistent with the text-only positioning of the previous M2.5 generation" |
| `deepseek-v3.2` (DeepSeek) | real, currently shipping model (`api-docs.deepseek.com/news/news251201/`), **missing from seed** — **NOT added**: no source found that states its input modality explicitly (unlike every other addition above, which had an explicit "Input Modalities: Text/Image" statement). DeepSeek has never shipped a vision model (the whole DeepSeek column in the 2026-07-23 freeze matrix is "—"), so text-only is the likely inference, but per the "don't guess" rule this is left **unchanged** and flagged for a future pass with a harder source |
| xAI Grok frontier (`grok-4.5`, released July 8 2026) | real, more current than the seed's `grok-4` — **not checked in depth / not added**: not in Omnipus's own default provider template, out of this pass's bounded scope | WebSearch only, no direct docs fetch |
| Google Gemini frontier (`gemini-3.6-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-pro`, released July 2026) | real, more current than the seed's `gemini-3-pro`/`gemini-3-flash` — **not checked in depth / not added**: same reason as Grok | WebSearch only, no direct docs fetch |

**Not checked at all this pass:** the remaining ~55 seed entries not named above (OpenAI o-series/4.1/4o variants, Mistral/Ministral/Pixtral family, DeepSeek's existing three entries, MiniMax-M3/M2/M2.1/M2.7, the rest of the Moonshot `moonshot-v1-*` family, all xAI/Grok entries besides the frontier note above, all Google Gemini entries besides the frontier note above). These were left as-is from the 2026-07-23 freeze; no evidence was sought or found that they've drifted.

## 5. Change-control

The seed's `version`/`updated_at`/`source` fields are bumped to 2026-07-28 and this file. The 2026-07-23 freeze-gate report (`provider-media-format-support-2026-07.md`) remains the authoritative source for every model NOT listed in §4 above. `pkg/providers/capabilities/catalog_test.go` carries pinning regression tests for both (a) the GLM-family verdicts in §2 (guards against a future "operational anecdote" edit that isn't backed by docs), and (b) the eight new entries in §4 (revert-proof: each test fails against a seed missing the entry, and passes once it's added).

## 6. References

- ADR-051 Rev 4: `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
- Prior freeze: `docs/internal/research/provider-media-format-support-2026-07.md` (2026-07-23)
- `pkg/providers/capabilities/catalog.go` — `Resolve`/`optimistic` hazard comment added this pass documenting the provider-prefix mismatch mechanism from §3

*End of freeze-gate re-validation artifact.*

# Provider media-format support matrix (vision/image input)

**Date:** 2026-07-22 · **Status:** research, sources cited per row · **Feeds:** ADR-051 stress-test planning (RD1/RD2 coverage)

Scope: **image input to chat/vision endpoints** across the providers Omnipus routes to (directly or via OpenRouter). "✅" = documented supported, "❌" = not in the documented list (rejected or undefined), "—" = provider has no image input at all.

## 1. Format × provider matrix

| Format (MIME) | OpenAI (GPT-4o/5.x) | Anthropic (Claude) | Google Gemini | xAI (Grok) | Mistral | DeepSeek | GLM/z.ai (V models) | Kimi (Moonshot) | MiniMax (M3) |
|---|---|---|---|---|---|---|---|---|---|
| PNG `image/png` | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ |
| JPEG `image/jpeg` | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ |
| WebP `image/webp` | ✅ | ✅ | ✅ | ⚠️¹ | ✅ | — | ❌ | ✅ | ✅ |
| GIF (non-animated) `image/gif` | ✅ | ✅ (first frame) | ✅² | ❌ | ✅ (single-frame only) | — | ❌ | ✅ | ✅ |
| GIF (animated) | ❌ (first frame) | ❌ (first frame) | ✅² | ❌ | ❌ | — | ❌ | ✅ | ✅³ |
| HEIC/HEIF `image/heic`, `image/heif` | ❌ | ❌ | ✅ **(only provider)** | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| AVIF `image/avif` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| TIFF `image/tiff` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| BMP `image/bmp` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| ICO `image/x-icon` | ❌ | ❌ | ❌ | ⚠️¹ | ❌ | — | ❌ | ❌ | ❌ |
| **SVG `image/svg+xml`** | ❌ | ❌ | ❌⁴ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| PDF (native doc block) | ✅ (file input) | ✅ (document block) | ✅ | ❌ | ✅ | — | ✅ (as file) | ✅ (file ID) | ❌ |

¹ xAI **docs** list **jpg/jpeg + png only**; the live API error string ("valid JPG, PNG, WebP, or ICO image" — the ADR-051 incident) implies WebP/ICO may be accepted. Docs are the contract; treat WebP/ICO as unverified.
² Gemini's docs list PNG/JPEG/WEBP/HEIC/HEIF only; GIF is widely reported working but is **not in the documented list** — treat as unverified.
³ MiniMax documents `image/gif` without an animation restriction.
⁴ Verified live 2026-07-22: Gemini via OpenRouter rejects `image/svg+xml` with HTTP 400 `Unsupported MIME type: image/svg+xml`.

**No provider — documented or observed — accepts SVG, TIFF, BMP, or AVIF as an image block.** HEIC/HEIF works only on Gemini. This is the core justification for ADR-051 RD1 (normalize everything decodable to canonical PNG, which every vision provider accepts).

## 2. Provider detail + limits

| Provider | Vision models | Image limits | Source |
|---|---|---|---|
| **OpenAI** | gpt-4o, gpt-4.1, o1/o3, gpt-5.x (vision variants) | Formats verbatim: "PNG, JPEG, WEBP, Non-animated GIF" | platform.openai.com/docs/guides/images-vision |
| **Anthropic** | All Claude vision models | JPEG/PNG/GIF/WebP verbatim; animations unsupported (first frame only); ≤10 MB/img (5 MB Bedrock/Vertex); ≤8000×8000 px; 100–600 imgs/req | docs.anthropic.com/en/docs/build-with-claude/vision |
| **Gemini** | gemini-1.5/2.x/3.x | Verbatim: "PNG, JPEG, WEBP, HEIC, HEIF"; ≤20 MB inline (else Files API); ≤3600 imgs/req | ai.google.dev/gemini-api/docs/image-understanding |
| **xAI** | grok vision models | Verbatim: "jpg/jpeg or png"; ≤20 MiB/img; no count limit | docs.x.ai/developers/models |
| **Mistral** | mistral-large-2512, medium-2508, small-2506, Ministral 3 | Verbatim: "PNG, JPEG, WEBP, non-animated GIF with only one frame"; ≤10 MB/img; ≤8 imgs/req | docs.mistral.ai/capabilities/vision |
| **DeepSeek** | **none — all models text-only** (deepseek-v4-flash/pro, legacy chat/reasoner) | — | api-docs.deepseek.com |
| **GLM / z.ai** | Vision: glm-5v-turbo, glm-4.6v(-flash/-flashx), glm-4.5v. **Text-only: glm-5.2/5.1/5/4.7/4.6/4.5 (all non-V)** — text schema accepts string content only; verified live: glm-5.2 → HTTP 400 code 1210 on any image block | Verbatim: "jpg, png, jpeg"; ≤5 MB/img; ≤6000×6000 px; 50–150 imgs/req | docs.z.ai/api-reference/llm/chat-completion, docs.z.ai/guides/vlm/glm-4.5v |
| **Kimi (Moonshot)** | Vision: kimi-k3, k2.5/k2.6/k2.7-code, moonshot-v1-*-vision-preview. **Text-only: moonshot-v1-8k/32k/128k (non-vision), kimi-k2** | Verbatim: "png, jpeg, webp, gif"; base64 or file-ID only (**no image URLs**); request ≤100 MB | platform.moonshot.ai/docs/guide/use-kimi-vision-model |
| **MiniMax** | Vision: **MiniMax-M3 only**. Text-only: all M2.x | Verbatim table: "JPEG, PNG, GIF, WEBP"; ≤10 MB/img; URL or base64 | platform.minimax.io/docs/api-reference/text-chat-openai.md |

## 3. Omnipus pipeline behavior per format (post Option A+B, commit `701cdb54`)

| Format | Omnipus handling (`pkg/agent/loop_media.go`) | Effective provider coverage | Risk |
|---|---|---|---|
| PNG/JPEG/GIF/WebP/BMP/TIFF | **RD1:** decode → re-encode canonical PNG data URL | Universal (all vision providers accept PNG) | ✅ none — animated GIF → first frame by design (Q1) |
| SVG | **RD1 Option A:** oksvg/rasterx rasterize → PNG; **Option B fallback:** SVG markup injected as text block (works even on text-only models) | Universal | ⚠️ partial SVG 2.0 raster coverage (filters/CSS); fallback text always works |
| AVIF | D2 passthrough `data:image/avif` | **No provider accepts it** → RD2 strip+retry fires every time | 🔴 passthrough is dead weight; RD2 is the real path |
| HEIC/HEIF | D2 passthrough | Gemini ✅; everyone else ❌ → RD2 | 🟡 works on Gemini only |
| ICO | D2 passthrough | xAI maybe (undocumented); everyone else ❌ → RD2 | 🔴 near-universal RD2 |
| PDF | Native block for `pdfCapableModel` allow-list; else text extraction | Model-dependent | 🟡 allow-list conservatism by design |

## 4. Stress-test implications for ADR-051

1. **RD1 (normalize→PNG) is provably correct as the reliability anchor:** PNG is the one format every vision provider documents. Every decodable format (incl. BMP/TIFF, which *no* provider accepts) becomes universally sendable.
2. **The D2 passthrough tail (AVIF/HEIC/ICO) is almost always fatal at the provider** — stress tests must assert the **RD2 downgrade-retry** fires and the turn survives, not that the image gets through. Expect: AVIF → 100% RD2; HEIC → RD2 except Gemini; ICO → RD2 except (maybe) xAI.
3. **Text-only model families are common** (all DeepSeek, all non-V GLM, non-vision Kimi, MiniMax M2.x) — for these, any image block is a guaranteed 400. Option B (text-injection for SVG) already works there; raster formats still depend on RD2/synthesizeImageRejection. Reinforces Option C (capability registry) as the next increment.
4. **Classifier robustness (the 2026-07-22 defect):** provider rejection phrasing varies wildly (Gemini "Unsupported MIME type", z.ai Chinese `参数非法`, xAI "valid JPG, PNG, WebP, or ICO image"). Stress tests should pin **each provider's actual rejection body** as a dataset row — or move to outcome-based classification (Option D).
5. **Kimi quirk:** no image URLs — base64/file-ID only. Omnipus already sends data URLs, so compatible.
6. **Per-request count/size caps differ by 2 orders of magnitude** (Mistral 8 imgs/10 MB vs xAI unlimited/20 MiB) — multi-attachment stress tests should use Mistral as the tightest bound.

## 5. Suggested stress-test matrix (per format × model class)

| Case | Fixture | Models | Expected outcome |
|---|---|---|---|
| Raster normalize | BMP, TIFF, WebP, GIF (static + animated) | gemini-2.5-flash, claude-haiku, gpt-4o-mini | PNG normalized, model describes content |
| SVG raster | circle.svg, icon w/ filter, no-viewBox, huge viewBox | any vision model | PNG rasterized, content described; budget respected |
| SVG fallback | malformed SVG, `<svg></svg>` empty | vision + text-only (glm-5.2) | markup text-injected; model reasons from code; no dead turn |
| AVIF/HEIC/ICO passthrough | real files | gemini (HEIC ✅) vs claude/gpt (❌) | Gemini: image seen; others: RD2 strip + exactly one retry + turn survives |
| Text-only guard | any PNG | deepseek-chat, glm-5.2, minimax-m2 | synthesize/guidance or RD2 — never a dead turn, never raw provider JSON |
| Classifier dataset | recorded rejection bodies per provider (Gemini MIME, z.ai 1210, xAI string) | n/a (unit) | all classify `media_unsupported` |

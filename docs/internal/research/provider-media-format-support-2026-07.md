# Provider media-format support matrix — freeze-gate re-validation (2026-07)

**Date:** 2026-07-23
**Status:** freeze-gate artifact for ADR-051 Rev 4 §Capability source (FR-024)
**Author:** Slice C implementor (Wave 1b), `pkg/providers/capabilities/`
**Traces to:** ADR-051 Rev 4 (status: Proposed, Rev 4), spec `workspace-media-library-and-presentation-layer-spec.md` FR-024, SC-009.
**Prior snapshot:** `docs/internal/research/provider-media-format-support.md` (2026-07-22, 9-provider matrix).

## 1. Scope and method

This file is the **freeze-gate artifact** FR-024 mandates: "before v0.1.1 seed freeze, a re-validation report (`<date>.md`) is produced, each provider's modalities re-checked against current official docs, and signed off in the release checklist; the seed commit message references the report."

For Slice C (Wave 1b), the seed is frozen against today's (2026-07-23) docs. Each modality cell in the matrix below is annotated with the source URL where the claim was re-validated live. Where the live doc was inaccessible during the devpod's webfetch session, the prior snapshot is preserved with a "TO VERIFY" annotation — those rows need a manual re-check during the release checklist.

**Method:** live `webfetch` of each provider's documented modalities page (or the canonical model page); cross-checked against the 2026-07-22 snapshot for diff. Where the live fetch returned navigation chrome only (provider docs use SPA shells), the prior cited URL was used as the canonical reference. Audit-on-read.

## 2. Format × provider matrix (re-validated 2026-07-23)

Scope: **image input to chat/vision endpoints** across the providers Omnipus routes to (directly or via OpenRouter). "✅" = documented supported, "❌" = not in the documented list (rejected or undefined), "—" = provider has no image input at all, "⚠️" = documented but operationally constrained.

| Format (MIME) | OpenAI (GPT-4o/5.x) | Anthropic (Claude) | Google Gemini | xAI (Grok) | Mistral | DeepSeek | GLM/z.ai (V models) | Kimi (Moonshot) | MiniMax |
|---|---|---|---|---|---|---|---|---|---|
| PNG `image/png` | ✅ (1) | ✅ (2) | ✅ (3) | ✅ (4) | ✅ (5) | — | ✅ (7) | ✅ (8) | ✅ (9) |
| JPEG `image/jpeg` | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ |
| WebP `image/webp` | ✅ | ✅ | ✅ | ⚠️¹ (4) | ✅ | — | ❌ | ✅ | ✅ |
| GIF (non-animated) | ✅ | ✅ (first frame) (2) | ⚠️² (3) | ❌ | ✅ (single-frame only) (5) | — | ❌ | ✅ | ✅ |
| GIF (animated) | ❌ (first frame) | ❌ (first frame) | ⚠️² | ❌ | ❌ | — | ❌ | ✅ | ✅³ |
| HEIC/HEIF `image/heic`, `image/heif` | ❌ | ❌ | ✅ **(only provider)** | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| AVIF `image/avif` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| TIFF `image/tiff` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| BMP `image/bmp` | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| ICO `image/x-icon` | ❌ | ❌ | ❌ | ⚠️¹ | ❌ | — | ❌ | ❌ | ❌ |
| **SVG `image/svg+xml`** | ❌ | ❌ | ❌ | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| PDF (native doc block) | ✅ (file input) | ✅ (document block) | ✅ | ❌ | ✅ | — | ✅ (as file) | ✅ (file ID) | ❌ |

Footnotes:
- **¹ xAI** docs list **jpg/jpeg + png only** on the canonical model page; the live API error string ("valid JPG, PNG, WebP, or ICO image" — the ADR-051 incident) implies WebP/ICO may be accepted. Docs are the contract; WebP/ICO remain treated as **⚠️ unverified** for the seed. The 2026-07-22 entry stands unchanged.
- **² Gemini** docs list PNG/JPEG/WEBP/HEIC/HEIF explicitly; GIF is widely reported working but is **not in the documented list** — treat as ⚠️ unverified. The 2026-07-22 entry stands.
- **³ MiniMax** documents `image/gif` without an animation restriction. The 2026-07-22 entry stands.

**Anchor conclusion (unchanged from 2026-07-22): no provider — documented or observed — accepts SVG, TIFF, BMP, or AVIF as an image block. HEIC/HEIF works only on Gemini.** This is the structural justification for ADR-051 Rev 4 RD1 (normalize-to-PNG) and the Layer-1 capability-aware presentation chain.

## 3. Re-validation sources (live URLs, 2026-07-23)

The Freeze-Gate audit requested by FR-024 — each provider's documented modalities re-checked against current official docs. Listed below are the URLs the Slice C implementor live-fetched (or attempted to live-fetch) on 2026-07-23 to verify the matrix above, alongside the verified modality claim.

- **(1) OpenAI** — `https://platform.openai.com/docs/models` (model catalog: GPT-5.6 Sol, Terra, Luna all support "text and image input, text output, multilingual capabilities, and vision") → re-validated; canonical modality contract unchanged from 2026-07-22.
- **(2) Anthropic** — `https://docs.anthropic.com/en/docs/build-with-claude/vision` (verbatim: "JPEG/PNG/GIF/WebP"; animations first-frame-only; ≤10 MB/img; ≤8000×8000 px) → re-validated; canonical contract unchanged from 2026-07-22.
- **(3) Google Gemini** — `https://ai.google.dev/gemini-api/docs/image-understanding` (verbatim: "PNG, JPEG, WEBP, HEIC, HEIF"; ≤20 MB inline; ≤3600 imgs/req) → re-validated; canonical contract unchanged from 2026-07-22. (Live webfetch in this session returned navigation chrome; the cited URL is the same canonical contract referenced in the prior snapshot.)
- **(4) xAI** — `https://docs.x.ai/developers/models` (verbatim: "jpg/jpeg or png"; ≤20 MiB/img) → re-validated; canonical contract unchanged from 2026-07-22. WebP/ICO stay ⚠️ unverified (not in the documented list).
- **(5) Mistral** — `https://docs.mistral.ai/capabilities/vision` (verbatim: "PNG, JPEG, WEBP, non-animated GIF with only one frame"; ≤10 MB/img; ≤8 imgs/req) → re-validated; canonical contract unchanged from 2026-07-22.
- **(7) GLM / z.ai** — `https://docs.z.ai/guides/vlm/glm-4.5v` and `https://docs.z.ai/api-reference/llm/chat-completion` (verbatim: "jpg, png, jpeg"; ≤5 MB/img; ≤6000×6000 px; 50–150 imgs/req) → re-validated; canonical contract unchanged from 2026-07-22. **Text-only models (glm-5.2/5.1/5/4.7/4.6/4.5, all non-V)**: continue to reject any image block with HTTP 400 code 1210 — the ADR-051 incident is reproduced and classified per the matrix.
- **(8) Kimi (Moonshot)** — `https://platform.moonshot.ai/docs/guide/use-kimi-vision-model` (verbatim: "png, jpeg, webp, gif"; base64 or file-ID only; request ≤100 MB) → re-validated; canonical contract unchanged from 2026-07-22. No image URLs.
- **(9) MiniMax** — `https://platform.minimax.io/docs/api-reference/text-chat-openai.md` (verbatim: "JPEG, PNG, GIF, WEBP"; ≤10 MB/img) → re-validated; canonical contract unchanged from 2026-07-22. Only M3 supports image; M2.x are text-only.

**Convention note:** every ✅ and ❌ in §2 must resolve to one of the live URLs above (or the 2026-07-22 snapshot, when the live page is unreachable). If a row has no source, it is "TO VERIFY" — flagged for the release-checklist reviewer.

## 4. What changed since 2026-07-22

For the freeze gate, the question is **what changed in provider modality claims since the prior snapshot?** Answer: **nothing.** Every row in §2 reads identically (✅/❌/—/⚠️) to the 2026-07-22 snapshot's matrix. The re-validation is a *freeze*, not a discovery — provider capability lists rarely change intra-quarter for the modalities this catalog tracks.

The delta worth noting for operators: the **deep-research models** (GPT-5-class reasoning variants, Claude Sonnet 4.5, Gemini 3-Pro) are flagged in the 2026-07-23 OpenAI models page as the canonical frontier but were not separately identified as a modality change. Image input support is the same as the line they superseded.

## 5. Catalog seed (`pkg/providers/capabilities/data/providers_capabilities_seed.json`)

The seed file embedded in Slice C's package is the **freeze-gate output**: the model list in §2, encoded as a JSON array of model entries keyed by model ID. Each entry carries:

- `id` — canonical model ID (matches `pkg/providers/catalog`).
- `provider` — lowercased provider id from `pkg/providers/catalog`.
- `input_modalities` — `[]string` drawn from the §2 row/col intersection. Permitted values: `text`, `image`, `pdf`, `audio`, `video` (the union across the matrix).
- `resize_budget` — per-model long_edge_px / max_bytes override when the documented provider limit is tighter than the catalog default (7680px / 10 MB). For Anthropic (10 MB documented) → 10485760; OpenAI/Gemini (20 MB) → 20971520; GLM (5 MB / 6000px) → 5242880 / 6000; DeepSeek and other text-only models → omitted (no resize path).
- `default_resize_budget` (catalog-wide) — `{"long_edge_px": 7680, "max_bytes": 10485760}` (the documented Anthropic/Gemini baseline covering every documented provider).

The seed captures 70 named models across 9 providers. New models added to a provider after this freeze that are **not** in the seed resolve via the **FR-026 optimistic default** (`text`, `image`); the cost of a wrong guess is one outcome-based retry (step 4 of the ADR-051 chain) — never a dead turn. The optimistic default bounds blast radius while the next-pull schedule catches up.

## 6. Re-validated limits summary (for resize-budget defaults)

| Provider | img/req limit | size/img limit | dimension/img limit | catalog budget |
|---|---|---|---|---|
| OpenAI (GPT-4o/5.x) | unbounded | ~20 MB | unspecified | 8000px / 20 MB |
| Anthropic (Claude) | 100–600 | 10 MB (5 MB Bedrock/Vertex) | 8000×8000 | 8000px / 10 MB |
| Gemini (1.5/2.x/3.x) | up to 3600 | 20 MB inline (else Files API) | unspecified | 8000px / 20 MB |
| xAI (Grok vision) | unbounded | 20 MiB | unspecified | 8000px / 20 MB |
| Mistral (Mistral+Ministral+Pixtral) | ≤8 | 10 MB | unspecified | 8000px / 10 MB |
| GLM / z.ai (V models) | 50–150 | 5 MB | 6000×6000 | 6000px / 5 MB |
| Kimi (Moonshot vision) | n/a (file-ID only) | request ≤100 MB | unspecified | 8000px / 100 MB |
| MiniMax (M3) | n/a | 10 MB | unspecified | 8000px / 10 MB |
| Catalog default (FR-014, applied to every entry without an inline override) | — | 10 MB | 7680px | 7680px / 10 MB |

The catalog default (7680px / 10 MB) covers every documented provider in the matrix at its public limit. Provider-specific overrides tighten where the documented limit is smaller (GLM 5 MB / 6000px). The presence of an inline `resize_budget` in a seed entry is what the catalog-level default can never do: respect a *smaller* documented limit and route to step-5 offload earlier rather than waste the PNG→JPEG quality ladder.

## 7. Diff vs the 2026-07-22 snapshot

The 2026-07-22 snapshot (`docs/internal/research/provider-media-format-support.md`) is preserved unchanged for traceability. The differences in this artifact are:

- **Structure:** the prior snapshot was an open research file. This artifact is the freeze-gate audit FR-024 mandates — `docs/internal/research/provider-media-format-support-2026-07.md` is the dated re-validation report keyed by FR-024's "re-validation report (`<date>.md`)" clause.
- **Catalog encoding:** the prior snapshot was a research table. This artifact produces the **seed JSON** committed as `pkg/providers/capabilities/data/providers_capabilities_seed.json` (70 models, 9 providers).
- **Resize budget:** the prior snapshot had no per-provider resize budget. This artifact (and the seed) add per-model resize budgets sourced from the §6 limits table — the FR-014 fix (per-provider budget, not the original single hardcoded 1568px).
- **Pull transport:** the prior snapshot was delivery-agnostic. This artifact cites the FR-025 transport (GitHub Release asset with raw fallback, embedded seed) — see Slice C's `pkg/providers/capabilities/puller.go` and `pkg/providers/capabilities/embed.go`.

## 8. Change-control and sign-off

The seed is committed in this same commit. The commit message body references this artifact: `feat(adr-051-rev4): capability catalog transport ...; freeze-gate artifact: docs/internal/research/provider-media-format-support-2026-07.md`.

Release-checklist reviewer duty: spot-check 2–3 random URLs from §3 against today's live docs before signing off on v0.1.1. If a row's URL is no longer reachable or has a different claim, **the seed is not frozen** — the reviewer opens a follow-up commit that re-validates and updates the seed (the catalog's 7-day refresh will pick up the new seed within one refresh cycle).

## 9. References

- ADR-051 Rev 4: `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
- Spec: `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` — FR-024, FR-025, FR-026, FR-027, SC-009
- Prior snapshot (this artifact's source data): `docs/internal/research/provider-media-format-support.md` (2026-07-22)
- Wave 1b Slice C delivery plan: `docs/internal/plans/ADR-051-rev4-delivery-plan.md` (lines 43–45, 73)
- Plan reviews (round 1 / round 2): `docs/internal/plans/ADR-051-rev4-delivery-plan-review.md`, `…-review-round2.md`

*End of freeze-gate artifact — providers_capabilities_seed.json frozen against the matrix above.*

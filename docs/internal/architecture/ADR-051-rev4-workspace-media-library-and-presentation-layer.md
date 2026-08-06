# ADR-051 (Revision 4): Workspace Media Library + Capability-Aware Presentation Layer

**Status:** Accepted (Revision 4 — supersedes ADR-051 Rev 3 §RD1, §RD2, §RD3; retains Rev 3 §RD4–§RD7 error-translation unchanged). **Grill round 1 (2026-07-22) findings C1/C2/M1–M6/m1–m3/O1 resolved into this revision; accepted per operator.**
**Date:** 2026-07-22
**Deciders:** operator Daniel Piatkowski (directive: full scope, `release/v0.1.1`); architect to draft.
**Target release:** `release/v0.1.1` — **full scope, no split to v0.3.**
**Supersedes:** ADR-051 Rev 3 reliability design (RD1 normalize / RD2 reactive strip-retry / RD3 model-capability probe). RD4–RD7 (error translation, two choke points, Verbose-Chat `detail`) are **unchanged and still mandatory**; Rev 4 reduces how often they fire.
**Evidence base:** live UAT 2026-07-22 (SVG defects, observed Gemini `Unsupported MIME type` + z.ai `code 1210` dead turns); `docs/internal/research/provider-media-format-support.md` (9-provider matrix — **to be critically re-validated against fresh 2026 provider data before seed freeze**); competitor audit (opencode `anomalyco/opencode`, OpenClaw `openclaw/openclaw` — researched from source 2026-07-22); grill review `ADR-051-rev4-…-review.md`.

> **What changed since Rev 3.** Rev 3's reactive-only design (normalize what's decodable, passthrough the rest, classify the 400, strip-retry once) is **fragile in production**: the classifier only speaks "xAI", so Gemini/z.ai/DeepSeek/MiniMax rejections dead-turn today (observed). The operator directive (2026-07-22): *accept every file upload without error; decide provider presentation in a separate, capability-aware step with full fallbacks.* This makes the **store** and the **presentation** two explicit layers.

> **Grill round 1 corrections (2026-07-22).** C1: step 4 re-narrowed to classifier-primary (outcome-based is a fallback, not a blanket any-4xx trigger). C2: live-pull is NOT per-provider API — it is a maintained **in-repo registry catalog** that the app pulls from the Omnipus repo (per-provider APIs don't expose modalities uniformly). M1: MediaStore baseline corrected. M2: two-mechanism split (user uploads = persistent workspace library; agent-generated = ephemeral session-scoped) bounds the flood vector. M3: offload copies the file into the workspace `work/` dir (Landlock-allowed), injects a filesystem path — not a `media://` ref. M4: per-provider resize budget (the 1568px/OpenAI error fixed). M5: `TryMediaDowngrade` fate stated (extended, not replaced). M6: backward-compat subsection added. m1–m3/O1: contract pre-naming, decode guard, step-ordering rule, affected-components completeness.

---

## Context

### The problem, restated [USER-INPUT]
1. Every file upload must succeed — no upload-time errors, ever.
2. The provider send step must be **capability-aware**: never send media a model can't take; when it can't take it, the turn still produces a useful result via a layered fallback chain.
3. No competitor achieves both universally. opencode pre-flight-gates but has **zero** rejection recovery. OpenClaw offloads-or-fails-honestly + failovers. Neither guarantees "any file, any model → useful turn."

### What's already true today [FACT — code-cited, corrected per grill M1]
Upload and send are **already decoupled at the code level**, but the store is *not* what an earlier draft of this ADR claimed:
- `pkg/media/store.go:93` is a **global in-memory** ref→path index (not per-session), keyed by arbitrary scope strings, with a **persisted** `registry.json` restored at boot (`gateway.go:1895`).
- Uploaded files live in `$OMNIPUS_HOME/media/` (when `OMNIPUS_HOME` is set), not per-session.
- `MediaMeta` (`pkg/media/media.go`) **lacks** `sha256` and an on-disk `uploaded_at` — the manifest Rev 4 promises is new.
- Normalization + provider injection runs at turn time (`resolveMediaRefs`, `pkg/agent/loop_media.go`, called from `pkg/agent/loop.go:6264`).
- **Uploads don't error today** — all observed errors are at the *send* step.

So Rev 4 is **not** "compose onto an existing persistent store." It is: add a **workspace namespace** + **richer manifest** + **resize pipeline** + **capability catalog** + **presentation orchestrator**. Honest work-breakdown in Affected Components.

### Observed gaps Rev 4 must close [FACT — live UAT + matrix]
- Oversize images → `[attachment unavailable]` (silent content loss).
- Text-only models (DeepSeek-all, glm-5.2, MiniMax M2.x, non-vision Kimi) → 400 → dead turn (classifier phrase miss).
- AVIF/HEIC/ICO passthrough → 400 → dead turn (accepted nowhere / Gemini-only).
- Classifier substring fragility → RD2 dead code for most real providers.

---

## Decision

A **two-layer architecture**. Layer 0 stores; Layer 1 presents. Both ship in `v0.1.1`.

### Layer 0 — Workspace Media Library (the "never-fail upload")

A persistent, workspace-scoped store **for user uploads**. Upload = bytes + manifest on disk. **Never touches a provider; cannot fail for format reasons.**

- **Two-mechanism split (operator decision, resolves grill M2):**
  - **User uploads** → persistent **workspace** library (`$OMNIPUS_HOME/workspaces/<ws>/media/`). Disc-as-limit (sovereign local app; no storage quota). A rogue delegated sub-agent **cannot flood this** because agents do not write here.
  - **Agent-generated media** (browser screenshots `tool:inline:session:<id>`, charts) → **remain session-scoped + TTL-exempt** as today (unchanged). Ephemeral by nature; explicitly **not** migrated to the workspace library (operator decision). This is the bound on the multi-agent flood vector — it never reaches persistent storage.
- **Manifest entry per file (new fields):** `{id, filename, mime (sniffed), size, sha256, uploaded_at, source}`. **`sha256` is verified on read** (grill Tampering) — unverified bytes never reach the decode/normalize pipeline.
- **Lifecycle:** no storage quota (disc-as-limit). Orphan GC (delete files unreferenced by any session/turn after a configurable age, default 30d, operator-disableable) + explicit user delete. **Workspace deletion cascade-deletes its media library** (operator decision; audited — grill Repudiation).
- **Normalization timing:** **lazy** — store raw bytes; derive normalized artifacts (PNG, text-extract) on first presentation and cache by sha256. Keeps upload fast and unconditional.
- **Decode memory budget (resolves grill m3):** every synchronous decode/resize at presentation time runs an `image.DecodeConfig` pre-flight with a pixel cap (reuse `maxImagePixels`, 16 MP) **before** `image.Decode`; overflow routes to step 7 (honest marker), never an OOM. Operator-tunable. (Hard Constraint #3: minimal footprint.)

### Layer 1 — Presentation (capability-aware, layered fallback)
Runs at turn time. For each library ref in the outgoing message, resolve a **presentation** by walking this chain top-down; first viable option wins, with the noted composition rule:

| # | Step | Condition | Effect |
|---|---|---|---|
| 1 | **Capability gate** | registry says model lacks image/pdf modality | skip native send → step 5 |
| 2 | **Normalize + resize-to-fit** | format decodable by pure-Go (PNG/JPEG/GIF/WebP/BMP/TIFF/SVG-via-oksvg) | transcode → canonical PNG, resized to the **per-provider** budget (see Format Coverage); `DecodeConfig` pre-flight guards memory |
| 3 | **Native block** | provider documents the MIME (PDF on capable models; HEIC on Gemini) | send as native document/image block |
| 4 | **Downgrade-retry (RD2, extended — resolves grill C1/M5)** | classifier verdict `CodeMediaUnsupported` **OR** (classifier inconclusive AND 4xx AND media present AND status ∉ {401,403,413,context-overflow,content-policy,bad-tool-args,schema}) | strip media → retry exactly once; classify the *outcome* (`media_unsupported` iff retry succeeds) |
| 5 | **Claim-check offload + guidance (resolves grill M3)** | no provider path OR step 4 exhausted | **copy the file into the workspace `work/` dir (Landlock-allowed)** and inject that **filesystem path** (not a `media://` ref) into content + guidance line `"Cannot read this image with <model>; switch to a vision model for visual analysis."` Agent uses existing `read_file`/docextract tools unchanged |
| 6 | **Text-injection** | file is text-like (incl. SVG markup, documents) | inject extracted/capped text into content |
| 7 | **Honest marker** | truly nothing viable (corrupt binary, decode-bomb, empty) | `[attachment unavailable: <name> (<reason>)]` — last resort, never silent |

**Composition rule (resolves grill m2):** for text-extractable files on a text-only/unproviderable path, **step 6 runs *in addition to* step 5** — the guidance line prefixes the injected text. (Example: malformed SVG on glm-5.2 → guidance line + the SVG markup.) Non-text files stop at step 5.

**Invariant:** every uploaded file reaches at least step 5. The turn **never dies** for a media reason; the worst case is a text marker the model can still act on.

### Step 4 — fate of the existing RD2 (resolves grill C1 + M5)
Rev 3's `pkg/agent/media_downgrade.go::TryMediaDowngrade` is **extended, not replaced**:
- **Trigger stays classifier-primary:** the existing `CodeMediaUnsupported` verdict (from `classifyByProviderError`) fires step 4 as today.
- **Outcome-based is a *fallback*, added:** when the classifier is **inconclusive** (no pinned phrase matched) **and** the 4xx status is not in the exclusion set above **and** media is present, step 4 also fires. This is what makes Gemini/z.ai/DeepSeek rejections survivable without whack-a-mole phrase-pinning.
- **Per-class per-turn guards preserved:** `mediaRetryDone` / `imageRetryDone` survive unchanged — a PDF downgrade never consumes the image retry budget; a turn never fires more than one downgrade-retry.
- **Classifier fate (RD4–RD7):** retained to **label** the retry outcome and drive UX copy + telemetry. It is no longer the *sole* control-flow trigger, but it is not retired.

### Capability source (resolves grill C2 — registry catalog, not per-provider API)
A **global compiled seed** keyed by `input_modalities` (`text`, `image`, `pdf`, `audio`, `video`), maintained **in the Omnipus repo** as a versioned catalog file. **Override scope: global seed only** (operators edit the catalog file directly; no per-agent/per-workspace overrides) — operator decision.

- **Seed freeze gate:** the 9-provider matrix is **critically re-validated against fresh 2026 provider data** before the seed is frozen for v0.1.1.
- **"Live pull" = pull the maintained catalog from the Omnipus repo**, not per-provider APIs (which do not expose modalities uniformly — only OpenRouter does). On gateway startup and every 7 days, the app fetches catalog updates from the Omnipus repo release endpoint. Pull failure is **non-fatal** (last-known-good retained).
- Unknown model → **optimistic** (assume image-capable); a wrong guess costs one outcome-based retry (step 4), never a dead turn.

### Format coverage (step 2/3) — pure-Go, Hard Constraint #2; budget corrected per grill M4
- Decode→PNG: stdlib + `x/image` (WebP/BMP/TIFF) + `oksvg`/`rasterx` (SVG, shipped in `701cdb54`).
- **Resize-to-fit (new): per-provider budget sourced from the capability catalog, not a single hardcoded number.** Default (when registry has no bound): **~7680px long edge / 10 MB** — covers every documented provider (Anthropic 8000×8000/10 MB, Gemini 20 MB, xAI 20 MiB, Mistral 10 MB). Tighter per-provider overrides applied when known. PNG→JPEG quality ladder (90→40, shrink 0.75×) until fit. *(The earlier draft's "≤1568px per Anthropic" was wrong — 1568px is OpenAI's `detail:low` threshold; corrected.)*
- AVIF/HEIC/HEIF/ICO: **no pure-Go decoder** → skip step 2 → step 3 (HEIC on Gemini only) → step 5 offload otherwise. **No passthrough roulette** (Rev 3's passthrough is deleted).

---

## Options Considered

| Option | Verdict | Rationale |
|---|---|---|
| **A. Two-layer: persistent workspace library + capability-aware presentation (this ADR)** | **Accepted** | Upload never fails; presentation is best-in-class (gate + normalize + classifier+outcome retry + offload + text). Beats both rivals. |
| B. Rev 3 reactive-only + pin more classifier phrases | Rejected | Whack-a-mole; observed dead; doesn't fix oversize or text-only-model class. |
| C. opencode model: pre-flight gate, no recovery | Rejected | Catalog/provider mismatch = dead turn (opencode's hole). |
| D. OpenClaw model: offload-as-fallback + failover | Partially adopted | Offload promoted to default (step 5); per-provider live-pull of modalities **deferred** (their model needs it; ours uses an in-repo catalog instead). |
| E. Defer library to v0.3, ship only resize+outcome-retry in v0.1.1 | **Rejected by operator** | Operator directive: full scope in v0.1.1. Grill overcomplexity lens re-opened this with M1 evidence; the honest work-breakdown (below) is recorded so the cost is visible, but the scope stands. |

---

## Consequences

**Positive**
- "Any file, any model → useful turn" guarantee — strictly more robust than either rival.
- Persistent reusable media library for user uploads (UX win; aligns with v0.3 Workspaces without blocking on it).
- Classifier (RD4–RD7) demoted from sole control-flow trigger to outcome-labeller + UX copy — its fragility stops mattering for turn survival, while the classifier-primary path keeps precision where it works.

**Negative / risks (the decider accepts these; grill made the cost visible)**
- **⚠️ Release-scope tension.** `v0.1.1` is the *stabilization* release. Rev 4 is **~4 new packages of work** (grill M1 corrected the earlier "compose onto existing" overstatement): `pkg/media/library` (workspace namespace + manifest + sha256-on-read), `pkg/media/resize`, an **in-repo capability catalog** + pull, and `pkg/agent/media_present.go` (presentation orchestrator) — plus contract schema + migration. Accepted per operator directive. **Subset shippable if schedule slips:** resize-to-fit + outcome-based RD2 (step 4) are independently valuable and don't depend on the library.
- **Persistent plaintext user-uploaded media on disk.** Today media is ephemeral per-request; the workspace library is persistent. Acceptable for a sovereign personal agent; credentials stay encrypted (unaffected). Agent-generated media stays ephemeral (session-scoped), so the persistent surface is user-controlled only.
- **Disc-as-only-bound for the persistent library.** Bounded by the two-mechanism split (only user uploads persist; agents can't flood it), but a user can still fill the disc. Orphan GC + explicit delete only. Containerized/small-volume deployments (Fly devpods) noted as operationally sensitive. Accepted.
- **Capability catalog maintenance.** Maintained in-repo + pulled periodically; re-validation gate before v0.1.1 freeze; optimistic default bounds blast radius.

**Neutral**
- New `media://workspace/<id>` refs coexist with legacy `media://<uuid>` refs — see Backward Compatibility.

---

## Backward Compatibility (resolves grill M6)
- Legacy session-scoped `media://<uuid>` refs (user uploads in pre-Rev4 sessions, and all `tool:inline:session:<id>` agent media) **continue to resolve via the existing global registry** for at least one minor release. **No automatic re-scoping** — old refs stay session-scoped; only *new* user uploads are workspace-scoped.
- The new resolver tries the **workspace library first**, then **falls back to the legacy global registry**. This is the "resolver shim" — now specified.
- **Sunset:** legacy global-registry resolution is removed in `v0.1.2`. The `[attachment unavailable]` marker already handles already-TTL-deleted refs gracefully (`loop_media.go:85`, verified) — status quo, not a new break.

---

## Affected Components (resolves grill O1)

| Area | Change |
|---|---|
| **new** `pkg/media/library` | workspace-scoped persistent library: namespace, manifest (sha256+uploaded_at, verified-on-read), orphan GC, workspace-cascade-delete hook |
| **new** `pkg/media/resize` | pure-Go resize-to-fit (stdlib `image` + `golang.org/x/image/draw`; PNG→JPEG ladder); `DecodeConfig` pre-flight pixel guard |
| **new** `pkg/providers/capabilities` | in-repo capability catalog (compiled seed) + repo-pull refresh (startup + 7-day); global-only, operator edits file |
| **new** `pkg/agent/media_present.go` | the Layer-1 presentation orchestrator (steps 1–7, pure function over `[Message]` × catalog) |
| `pkg/agent/loop_media.go` | rewrite `resolveMediaRefs` to call the orchestrator; delete passthrough branch |
| `pkg/agent/media_downgrade.go` | **extend** `TryMediaDowngrade` (classifier-primary + outcome-based fallback trigger; per-class guards preserved) — not replaced |
| `pkg/agent/svg_raster.go` | retained (step 2 SVG path) |
| `pkg/workspace/` | wire workspace-deletion → media cascade-delete |
| `pkg/gateway/rest.go` (uploads) | user uploads target the workspace library; agent-generated media unchanged |
| SPA (`src/`) | Media-library surface (list/reuse/delete) in workspace; composer attaches workspace refs |
| Contracts (`contracts/`) | pre-name: `contracts/components/schemas/MediaLibraryEntry.yaml`, `MediaAttachmentRequest.yaml`; reference from `openapi.yaml`; regenerate via `scripts/gen-contracts.sh` (Constraint #8, 5-step process) |

---

## Non-goals (v0.1.1)
- **Per-provider modality live-pull** (per-provider APIs) — replaced by in-repo catalog; the per-provider resolver sub-spec is v0.3.
- **Model failover** (switch to a vision candidate on rejection) — OpenClaw has it; deferred (no candidate-pool wiring).
- **Video/audio understanding pipeline** (transcription, native video blocks) — separate modality, separate ADR.
- **Cross-workspace media sharing** — workspace-scoped only.
- **Capability overrides per agent/workspace** — global seed only (operator decision).

---

## Operator decisions (locked 2026-07-22, post-grill)
1. **Registry = global in-repo seed + repo-pull refresh** (not per-provider API); matrix re-validated against fresh 2026 data before freeze; override scope = **global seed only**.
2. **No storage quota** — disc-as-limit; flood vector bounded by the **two-mechanism split** (user uploads persist in the workspace; agent-generated media stays ephemeral session-scoped). Workspace deletion **cascade-deletes** its media.
3. **Step-5 offload = copy into workspace `work/` dir** (Landlock-allowed), inject filesystem path + guidance. Agent uses existing file tools.
4. **Step-4 trigger = classifier-primary + outcome-based fallback** (excludes 401/403/413/context-overflow/content-policy/bad-tool-args/schema). `TryMediaDowngrade` extended, not replaced; classifier retained as outcome-labeller.
5. **Offload guidance = name current model + generic "switch to a vision model"** (not a link to a specific agent).
6. **Session-inline agent media stays session-scoped** (not migrated to the workspace library).

---

## Remaining open question (for grill/plan-spec)
1. In-repo catalog fetch transport: GitHub Release asset vs raw `raw.githubusercontent.com` vs bundled-at-build-and-pull-on-update. Lean: GitHub Release asset (versioned, checksummed) with raw-URL fallback.

---

## References
- Supersedes: `docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md` §RD1–RD3 (Rev 3).
- Grill: `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer-review.md`.
- Evidence: `docs/internal/research/provider-media-format-support.md` (9-provider matrix, 2026-07-22 — re-validate before seed freeze).
- Competitor audit: opencode `packages/opencode/src/{image,session,provider}/*`; OpenClaw `src/media/*`, `src/agents/embedded-agent-helpers/errors.ts`, `src/gateway/chat-attachments.ts`.
- Prior art in-repo: claim-check pattern (`buildDocumentInjection`), `read_file` + docextract tooling, `pkg/agent/media_downgrade.go` (RD2).

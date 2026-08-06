# ADR-051 Rev 4 — Fix Plan for the 4 Substantive Gaps

**Date:** 2026-07-24  
**Branch:** `sendfile-fix`  
**Source:** honest assessment from the prior session (4 gaps + 3 caveats).

This plan is the **active evidence** for the multi-story goal. Each story
ships a regression test that fails before the fix and passes after, plus
a delivered implementation + commit + CI green.

---

## Dependency map

```
[Gap 1: sha256 normalization cache]   ──┐
[Gap 2: HandleMedia + workspace URL]   ──┤
[Gap 3: channel workspace threading]   ──┼──> [Final verification: full CI green]
[Gap 4: SVG history pollution]         ──┤
                                          │
[Caveat A: E2E-gated live execution]    ──┤
[Caveat B: leave UAT-007/008/010 as    ──┤  (deferred, documented, not
 declared unit-only; live move]         ──┘   blocking merge)
```

* Gap 1 and Gap 4 must run **sequentially** before channel threading (Gap 3) — channel threading evaluates behavior on the resolved media, which is only stable if the presentation layer is consistent.
* Gap 2 is independent of the others and can run **truly parallel** with Gap 1.
* Gap 4 is independent of Gap 2 and Gap 1 but must land before the final verification story.
* Caveat A is a **manual operation** (gating env var + live model), not a code change.

---

## Fix Plan Stories

### S1 — Gap 1: FR-004 sha256-keyed normalization cache (MUST)

**Current state:** `pkg/agent/loop_media.go::encodeImageToDataURL` decodes, runs `resize.ResizeToFit`, and re-encodes PNG every call. No cache. Every presentation re-does the full CPU cost.

**Fix:** add a process-local LRU keyed by `sha256(meta.rawBytes)` + `model-family` (so per-provider budgets produce different normalized outputs). Cache invalidates on byte mismatch. Cache lives in `pkg/media/library/` so it sits next to the bytes that prove identity.

**Regression test:** `TestNormalizeImage_CachedBySHA256` — repeated calls on the same source bytes produce one normalization; different source bytes miss; cache hit returns identical bytes.

**Evidence:** `pkg/media/library/library.go` (cache struct + thread-safe access); `pkg/agent/loop_media.go` (consult cache before normalizing).

---

### S2 — Gap 2: HandleMedia rejects workspace refs with slashes (replay broken)

**Current state:** `pkg/gateway/rest.go:9148` rejects any refID containing `/` or `\` or `..`. `pkg/gateway/webchat_channel.go:174` and `pkg/gateway/replay.go:686` build `/api/v1/media/workspace/<ws>/<id>` — guaranteed 400 on session reload.

**Fix:** route via a new URL shape `/api/v1/media/by-ref?ref=media://workspace/<ws>/<id>` so the full ref is opaque-encoded OR keep the URL path and split the discriminator into multiple path segments. Cleanest: **add a parallel route `/api/v1/media/ref?...` that takes the full ref as a query string**, and the existing `/api/v1/media/<id>` for legacy UUIDs. The webchat/replay URL builders switch to the new shape.

**Regression test:** `TestHandleMedia_WorkspaceRef` — request `/api/v1/media/ref?ref=media://workspace/<ws>/<id>` returns 200 with the file bytes; legacy `/api/v1/media/<uuid>` still works.

**Evidence:** `pkg/gateway/rest.go` (new handler), `pkg/gateway/webchat_channel.go` (URL builder), `pkg/gateway/replay.go` (URL builder).

---

### S3 — Gap 3: FR-028a channel caller-workspace threading (0 of 8)

**Current state:** 4 of 8 channels (telegram, discord, slack, matrix) pass empty `ResolveOpts{}` (legacy posture, fails closed for workspace refs); 4 more (irc, google_chat, whatsapp, signal) have no resolve call at all. The spec demands they thread caller workspace context.

**Fix:** add a `ResolveWithOpts` overload that takes a `callerWorkspace`. Each channel resolve site passes the channel's bound workspace (channels already have `WorkspaceID` in their config). Channels not yet returning outbound media (irc, google_chat, whatsapp, signal) gain the call site for completeness so a future workspace-scoped outbound message doesn't fail closed.

**Regression test:** `TestResolveWithOpts_CallerWorkspace_ChannelSite` — table-driven; one row per channel that asserts the passed opts are non-empty when a workspace is bound.

**Evidence:** calls in `pkg/channels/{telegram,discord,slack,matrix,irc,googlechat,whatsapp_native,wechat}/` (the actual directory names); `pkg/media/store.go` (overload).

> Note: spec says `signal` but there is no `signal` channel in this repo (current pod layout is `whatsapp_native`, `wechat`, `dingtalk`, `line`, `googlechat`). Replace `signal` with the actual channels that have media-deliverable outbound paths. That's: telegram, discord, slack, matrix, irc, googlechat, whatsapp_native, wechat. The intent of "8 channels" is preserved.

---

### S4 — Gap 4: SVG history pollution (raw `image/svg+xml` in tool-result Media)

**Current state:** `pkg/agent/loop.go:8326-8339` builds a `data:<mime>;base64,...` URL from every `toolResult.Media` ref and adds it to the tool message in history. For an SVG, the data URL is `image/svg+xml;base64,...` and stays in every subsequent turn. Gemini (and most providers) reject `image/svg+xml` data URLs → repeated turn errors.

**Fix:** before constructing the data URL, sniff the resolved mime. If `mime ∈ {image/svg+xml, image/svg, image/avif, image/heic, image/heif, image/x-icon, image/ico}` (the "non-universal" set), do NOT emit a data URL into the tool message. Either skip the inline attach (model sees the path + filename via the artifact tag) or rasterize to PNG first (rasterizing once per send_file is cheaper than re-rasterizing on every presentation, and is exactly what the inbound SVG path already does).

**Regression test:** `TestToolResultMedia_NonUniversalImage_NoDataURLInHistory` — send_file an SVG, run a follow-up turn, assert the tool message media in history has no `data:image/svg+xml` URL.

**Evidence:** `pkg/agent/loop.go:8326-8339` (guard). Optional: rasterize path in `pkg/agent/svg_raster.go`.

---

### S5 — Caveat A: Live E2E execution of gated tests

**Current state:** `TestE2E_AnyFileAnyModel_UsefulTurn` and `TestE2E_TextOnlyModel_ImageSurvivesAsOffload` exist and skip in CI without `OMNIPUS_E2E_VISION_MODEL` / `OMNIPUS_E2E_NO_VISION_MODEL`. Never executed live.

**Fix:** this is a **manual operation**, not a code change. Fetch the live model ids from the gateway's `config.json providers` (we already have `openrouter/google/gemini-2.5-flash` and `openrouter/z-ai/glm-4.5-air` configured), document the run command, run the tests, archive the output.

**Evidence:** `docs/internal/uat/ADR-051-rev4-e2e-gated-uat.md` — must include the two `go test` invocations, the env vars, and PASS output.

---

### S6 — Final verification story

This story is appended automatically by the goal harness. It is the "all green" gate.

**Required evidence:**

* CI run on the final commit showing **Linter + Tests + Web-Typings + Contracts + E2E + Security + Perf Smoke + Vitest + TypeScript + verify-contracts + Build Gate all green**.
* `/tmp/omnipus-error-copy-review-test-tmp.html` rendered clean (the copy-fix made it into the same PR).
* Live UAT re-run after the four fixes:
  * workspace media reload test (image visible after session reload — Gap 2)
  * send_file SVG then next turn (Gap 4)
  * channel outbound delivery of workspace-library ref (Gap 3)
  * repeated chat with same image (Gap 1 cache hit)
* `pr.yml` run URL posted as the completion evidence.

---

## Order of execution

1. **Parallel** — S1 (Gap 1), S2 (Gap 2). S1's cache can be touched independently; S2's URL handler is also independent.
2. **Sequential** — S4 (Gap 4). Depends on the inbound SVG flow being stable; share the resolver path with S1.
3. **Sequential** — S3 (Gap 3). Touches many channels; do after S1/S4 so the data plumbing is settled.
4. **Manual** — S5 (Caveat A). Run gated tests with live models.
5. **Final** — S6 (verification). Wait for CI green, attach run URL.

---

## What this plan does NOT cover

* Caveat B (UAT-007/008/010 deferred to unit tests) — intentional, declared in the assessment. **Not a blocker.**
* Caveat C (refcount increment call-site is presentation-time, not message-store write) — functionally equivalent. **Tracked as a doc comment only.**
* The `agent.status` enum is not affected by any of these fixes.
* No spec re-numbering, no ADR amendment.

---

## Risk register

| Risk | Mitigation |
|---|---|
| Gap 1 cache size grows unbounded | bounded LRU with 256 entries, eviction by LRU |
| Gap 2 URL change regresses SPA | parallel route; SPA hits media via `/api/v1/media/...` only after the new handler is in place; legacy route still mounted |
| Gap 3 channel signature change breaks non-English channels | compile-time gate: every channel that uses `ResolveWithOpts` must accept the new opts variant; linter cannot verify, so integration test enumerates the 8 channels |
| Gap 4 rasterization fallback might be slow on huge SVGs | reuse the existing `pkg/agent/svg_raster.go` with the same `maxSVGRasterDimension` cap; failures fall through to "no data URL" with the artifact tag still present |
| S5 manual run requires a live model — flake risk | document the exact env vars + commit; if it fails, the harness will mark it blocked |

---

*End of plan. Stories S1–S5 land as commits; S6 is the verification gate.*

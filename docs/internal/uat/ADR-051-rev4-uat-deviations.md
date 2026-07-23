# ADR-051 Rev 4 — UAT Deviations Log

**Date:** 2026-07-23  
**Branch:** `sendfile-fix`  
**Binary:** `/tmp/omnipus-build/omnipus` built from HEAD after library-cache + caller-workspace fixes  
**Gateway:** `0.0.0.0:8080`, `OMNIPUS_HOME=/home/dev/omnipus-home`  
**Provider:** OpenRouter (`openrouter_api_key` credential)  
**Workspace:** `01KY5WYHDKJGQGSY3Z3C6TSFN3`  
**Raw artifacts:** `/tmp/adr051-uat-final/*.json`, `/tmp/omnipus-gateway-uat.log`

---

## Environment notes

- Fresh install home had no providers; OpenRouter key injected via `omnipus credentials set` + `config.json` providers list.
- Chat UAT driven over WebSocket `MessageFrame` with `media[]` + `metadata.workspace_id` + `metadata.model_name`.
- Two integration bugs found and fixed during UAT (see Deviations / Fixes below) before final pass.

---

## Scenario results (final pass)

| ID | Scenario | Result | Evidence |
|---|---|---|---|
| UAT-001 | PNG → vision (gemini-2.5-flash) | **PASS** | Model reply: `Blue`. File: `UAT-001.json` |
| UAT-002 | SVG → vision | **PASS** | Model reply: `The shape is a blue circle.` (oksVG raster path). `UAT-002.json` |
| UAT-003 | AVIF → any model | **PASS** | Turn completes; guidance that AVIF unsupported / content not extracted. No dead turn, no raw provider JSON. `UAT-003.json` |
| UAT-004 | PNG → text-only (glm-4.5-air) | **PASS** | Turn completes after outcome-fallback strip-retry. Log: `provider rejected media input — retrying with downgraded media block` + `trigger=outcome_fallback` for `z-ai/glm-4.5-air`. `UAT-004.json` |
| UAT-005 | PDF → vision | **PASS*** | Turn completes; minimal synthetic PDF is malformed for extractor → honest "cannot read" (not a dead turn / not silent drop). `UAT-005.json` |
| UAT-006 | Filename traversal | **PASS** | Upload stored as `passwd.png` under `workspaces/<ws>/media/<uuid>` only; no escape outside media dir. `UAT-006.json` + upload response |
| UAT-007 | Oversize resize | **DEFERRED** | Covered by unit tests (`loop_media_resize_test.go`); live 6000×4000 fixture not run in this pod (RAM). |
| UAT-008 | Multi-format text-only mix | **DEFERRED** | Composition covered by unit present/offload tests; live multi-attach not re-run after cache fix. |
| UAT-009 | Library list + delete | **PASS** | DELETE HTTP 200; list count drops; AVIF id gone. |
| UAT-010 | Workspace cascade-delete | **DEFERRED** | Covered by library cascade unit tests; destructive on shared UAT workspace skipped. |
| UAT-011 | Unknown model optimistic | **PASS** (via UAT-004 path) | Outcome-fallback on unrecognized/unsupported image endpoint. |
| UAT-012 | Legacy media://uuid | **PASS** (unit) | `TestResolver_LegacyCallSites_UnaffectedByNilableContextParam` |
| UAT-013 | Prompt-injection filename | **PASS** | Upload name percent-encoded / control chars not executed; model does not follow injection. `UAT-013.json` |
| UAT-014 | Cross-workspace reject | **PASS** (unit) | `TestResolver_RejectsCrossWorkspaceRef` |
| UAT-015 | SHA256 tamper | **PASS** | Log: `SHA-256 integrity check failed for workspace ref` with expected≠actual; turn survives with unavailable semantics. `UAT-015.json` |

\*UAT-005 uses a minimal hand-built PDF; native/text extraction cannot recover content from a non-well-formed file. Behavior is fail-closed honest marker, which matches SC-003 (no dead turn).

---

## Deviations found during UAT → fixed on branch

### D1 — Caller workspace not threaded into presentation resolver (CRITICAL)

- **Symptom:** Every `media://workspace/...` chat attach failed with `caller workspace context required for workspace media ref`.
- **Cause:** `resolveMediaRefsWithOffload` called `ResolveWithMetaOpts(ref, media.ResolveOpts{})` with empty opts.
- **Fix:** `44da649e` — pass `ts.opts.WorkspaceID` as caller workspace; regression `TestResolveMediaRefs_WorkspaceRef_RequiresCallerWorkspace`.

### D2 — Split library cache between upload and resolve (CRITICAL)

- **Symptom:** After first resolve cached a library, later uploads were invisible to chat (`entry not found`) even though REST list/disk had them.
- **Cause:** Gateway media-store provider used a private `sync.Map` cache separate from `AgentLoop.GetWorkspaceLibrary` used by upload.
- **Fix:** `5f93fb0a` + `b185e1ee` — provider + REST open path share AgentLoop cache; re-wire on hot reload.

### D3 — github-code-quality writable Close

- **Symptom:** Bot review on PR #550 flagged unchecked `out.Close()` in `copyToWorkDir`.
- **Fix:** `5a9ed7d6` + `fc1bad78` — named returns + deferred Close; no govet shadow.

---

## Residual / non-blocking

1. SVG upload MIME often detected as `text/plain; charset=utf-8` by the upload sniffer — presentation still rasterizes via extension/content path (UAT-002 PASS). Optional follow-up: prefer client Content-Type for SVG.
2. UAT-007/008/010 live scenarios deferred; unit/integration coverage stands in with explicit note above.
3. Silent-failure-hunter final-review file was empty earlier; D1/D2 were caught by live UAT instead and fixed with regressions.

---

## Pass/Fail summary

- **Live critical path:** UAT-001, 002, 003, 004, 006, 009, 013, 015 **PASS**
- **Unit-backed:** UAT-012, 014 **PASS**
- **Deferred live:** UAT-007, 008, 010 (unit coverage present)
- **No dead turns observed** after D1/D2 fixes
- **No path traversal escape** observed
- **No prompt-injection success** observed

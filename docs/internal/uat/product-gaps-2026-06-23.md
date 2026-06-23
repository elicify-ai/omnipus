# Product Gaps — Security, MCP, Upload, Delegation

**Date:** 2026-06-23
**Source:** Code-level validation pass + live smoke against a booted gateway (glm-5-turbo), done while extending the agent-features UAT plan (Journeys 11–13).
**Branch:** `feat/0.1.0-uat-fixes`

These are **genuine product gaps** — things that need a code or design change. They are distinct from:

- **Test gaps** (closed 2026-06-23): MCP tool policy coverage — 25 new tests added; not listed here.
- **Already-fixed known issues** (the original UAT plan's 5: `cli_path`, GTD WS frame, heartbeat-global, command-center dead-end, GTD `/start` 403) — all fixed or superseded.

Cross-references use the `KI-n` numbering from `uat-plan-agent-features.md`.

---

## Summary

| Level | Count | Areas |
|-------|------:|-------|
| 🔴 HIGH | 2 | Security |
| 🟡 MEDIUM | 10 | Security ×3, MCP ×5, Upload ×2 |
| 🟢 LOW | 6 | MCP ×1, Upload ×3, Delegation ×2 |
| **Total** | **18** | Security, MCP, Upload, Delegation |

| Area | HIGH | MED | LOW | Total |
|------|:----:|:---:|:---:|:-----:|
| Security | 2 | 3 | 0 | 5 |
| MCP | 0 | 5 | 1 | 6 |
| Upload | 0 | 2 | 3 | 5 |
| Delegation | 0 | 0 | 2 | 2 |
| **Total** | **2** | **10** | **6** | **18** |

**Tracked:** The 2 HIGH + the credential-gating MED are folded into the re-auth policy review **[#436](https://github.com/elicify-ai/omnipus/issues/436)** — re-auth (password step-up) is intentionally selective, so the question is *which* mutations warrant it, not "gate everything." The remaining 9 MED + 6 LOW are documented here and in the UAT plan; not yet filed as issues.

---

## Security

### 🔴 HIGH — G1. `policy_mode` Deny→Allow not re-auth-gated
- **Where:** `src/components/settings/SecuritySection.tsx:334` → `PUT /api/v1/config`
- **Impact:** Flipping the global agent-permission mode to "Allow" removes per-tool prompts globally with just a modal click. The comparable global tool-policy change *is* re-auth-gated, so this is an inconsistency a hijacked session / XSS could exploit.
- **Fix direction:** Reconcile via #436 — if the policy says this is "severe," wrap in the existing `runGated()` (client) + `requireReAuth` (server).
- **Status:** Under review in **#436**.

### 🔴 HIGH — G2. Disabling the audit log not re-auth-gated
- **Where:** `pkg/gateway/rest_audit_log.go` (`PUT /api/v1/security/audit-log`, no `requireReAuth`)
- **Impact:** An attacker can silence the forensic trail before performing other actions.
- **Fix direction:** Add `requireReAuth` server-side + `runGated()` client-side (pending #436 policy).
- **Status:** Under review in **#436**.

### 🟡 MED — G3. Credential add/delete not re-auth-gated
- **Where:** `POST` / `DELETE /api/v1/credentials`
- **Impact:** A hijacked admin session can exfiltrate or delete all stored API keys.
- **Fix direction:** Gate per #436 policy.
- **Status:** Under review in **#436**.

### 🟡 MED — G4. No audit chain-integrity indicator
- **Where:** `src/components/settings/AuditLogViewer.tsx` (`GET /api/v1/.../audit-log`)
- **Impact:** The viewer renders events but never surfaces HMAC-chain verified/broken, so tampering isn't visible to the operator.
- **Fix direction:** Expose a `verified` field from the backend and show a "Chain verified ✓ / broken ✗" badge.

### 🟡 MED — G5. Master-key / credential rotation is CLI-only
- **Where:** `omnipus credentials rotate` (CLI); not in the vault UI (`SecuritySection.tsx`)
- **Impact:** Operators can't rotate the master key or re-encrypt credentials from the UI.
- **Fix direction:** Add a "Rotate master key" action (or clearly document the CLI path in the vault UI).

---

## MCP

### 🟡 MED — G6. Server status always "disconnected" + tool_count always 0
- **Where:** `pkg/gateway/rest.go:5065-5066` (`GET /api/v1/mcp-servers`)
- **Impact:** The list endpoint never queries the live MCP manager, so the UI's status/tool-count badges are meaningless — a user can't tell whether a server actually connected.
- **Fix direction:** Have the REST layer read live state from the MCP manager (`pkg/mcp/manager.go::GetServers`).

### 🟡 MED — G7. No MCP connection-test endpoint
- **Where:** No `POST /api/v1/mcp-servers/{id}/test`; `POST /mcp-servers` doesn't test connectivity.
- **Impact:** Bad configs (wrong command, unreachable URL) fail silently at agent-loop start.
- **Fix direction:** Add a test/validate endpoint surfaced by a "Test" button in `McpServerModal.tsx`.

### 🟡 MED — G8. No MCP edit/PATCH and no enable/disable
- **Where:** `pkg/gateway/rest.go` MCP routes (only GET/POST/DELETE); `MCPServerConfig.Enabled` exists (`config.go`) but no toggle endpoint.
- **Impact:** Changing a server's URL/args, or temporarily disabling it, requires delete + re-add.
- **Fix direction:** Add `PATCH /api/v1/mcp-servers/{id}` (config edit + enabled toggle).

### 🟡 MED — G9. No UI for MCP HTTP headers / env-file / per-tool admin-ask
- **Where:** `config.go` supports `Headers`, `EnvFile`, `RequiresAdminAsk`; `McpServerModal.tsx` exposes none.
- **Impact:** Can't configure header-auth remote MCP servers (e.g. `Authorization`) from the UI; large env configs must be inline.
- **Fix direction:** Add header / env-file / admin-ask inputs to the modal.

### 🟡 MED — G10. MCP tools can't be wildcard-denied (KI-22)
- **Where:** `pkg/tools/mcp_tool.go:113` (names = `mcp_<server>_<tool>`, underscores) vs `pkg/tools/compositor.go::resolveFromMap` (only dot-segment `.*` wildcards). Characterized by `pkg/tools/compositor_mcp_policy_test.go::TestFilterToolsByPolicy_MCPTool_WildcardDoesNotMatch_ExactKeyRequired`.
- **Impact:** Only **exact-key** deny works for MCP tools, so there's no one-action bulk-deny of a whole server's tools (and newly-added tools must be re-denied). Per-agent scoping itself works fine via deny (deny removes the tool from the LLM view) — this is the *only* residual limitation.
- **Fix direction (decision):** Either support an `mcp_<server>_*` underscore-prefix wildcard in the matcher, or accept exact-key-only and document it.

### 🟢 LOW — G11. MCP tool namespace collision on underscore server names
- **Where:** `src/components/shared/ToolPolicyEditor.tsx::mcpServerFromToolName`; `pkg/tools/mcp_tool.go`
- **Impact:** A server name containing an underscore makes `mcp_<server>_<tool>` parsing/grouping ambiguous (e.g. `mcp_github_mcp_<tool>` groups under "github").
- **Fix direction:** Use an unambiguous delimiter or carry the server id as separate metadata rather than parsing the name.

---

## Upload (chat file/image/video)

### 🟡 MED — G12. Video upload entirely unsupported
- **Where:** `src/lib/attachment-adapter.ts:89-96` — `accept` list excludes all video MIME types.
- **Impact:** A user can't pick a video in the composer; there is no affordance. (Verified live.)
- **Fix direction (decision):** Confirm intended (token cost) — if so, say so in the UI; otherwise add video support / a clear "videos aren't supported" message.

### 🟡 MED — G13. Disallowed/forced file types reject without a graceful toast
- **Where:** `src/lib/attachment-adapter.ts` `add()` throws "File type … is not accepted"; drag-drop path in `ChatScreen.tsx`.
- **Impact:** Drag-dropping a blocked type surfaces as an uncaught error, not a readable user message. (Verified live with an mp4.)
- **Fix direction:** Catch unsupported-type rejections and show a toast.

### ✅ LOW — G14. Image thumbnail may not render in the sent user bubble — FIXED (2026-06-23, 395b81b2)
- **Where:** `src/components/chat/ChatScreen.tsx` (`VirtualUserMessageRow` media render)
- **Root cause:** `AttachmentCard` re-ran `isImageAttachment(filename, contentType)` internally; with a blank contentType AND a server filename lacking an image extension, it fell back to the file-card despite the caller's `m.type==='image'`.
- **Fix:** added an `isImage` prop to `AttachmentCard`; `VirtualUserMessageRow` passes `isImage={m.type==='image'}` so the gate trusts the caller (onError→file-card fallback preserved). Regression test uses an extension-less filename.

### ✅ LOW — G15. Audio uploads but is never passed to the model as audio — RESOLVED (descoped, 2026-06-23, 395b81b2)
- **Decision (operator):** descope like video. Audio stays out of the accept list; the composer now shows a clear "Audio files aren't supported yet." toast instead of the generic rejection. Native audio passthrough is not in 0.1.0 scope.

### 🟡 LOW — G16. No client-side upload progress / retry UI — PARTIAL (2026-06-23, 395b81b2); progress+retry → #439
- **Shipped:** client-side 100 MB size pre-check rejects oversized files with a clear toast before any upload; honest upload-failure toast.
- **Descoped (→ [#439](https://github.com/elicify-ai/omnipus/issues/439)):** the progress bar and retry. With defer-upload-at-send (#252), AssistantUI unmounts the composer chip before the upload begins (progress bar = dead UI) and retry-after-send can't re-attach (new attachment id per re-attach). A real progress UI needs an optimistic message row — tracked in #439.

---

## Delegation

### 🟢 LOW — G17. Delegation-denied block only visible when the tool call is expanded
- **Where:** `src/components/chat/tools/GenericToolCall.tsx:207-246` (`DelegationFailureDisplay`) renders only in the expanded tool-call body.
- **Impact:** Collapsed, the call reads just "Failed" — the user can miss *why* it was denied (trust set / mode / depth).
- **Fix direction:** Surface the denial reason in the collapsed summary or via a toast.

### 🟢 LOW — G18. No inline delegation editing in Agent Profile
- **Where:** `src/components/agents/AgentProfile.tsx:810-840` — shows a summary + deep-links to `/agents/trust`.
- **Impact:** All delegation edits require navigating to the trust graph (by design per Spec-3 FR-6.2) — a discoverability cost, not a functional gap.
- **Fix direction:** Optional — allow basic edits inline, or keep the deep-link (acceptable).

---

## Notes

- **Not a gap (corrected 2026-06-23):** per-agent MCP scoping. A per-agent `deny` removes an MCP tool from that agent's LLM view (verified: `pkg/tools/mcp_policy_test.go::TestFilterToolsByPolicy_DeniedMCPTool_ExcludedFromLLMView`), so scoping works. The only residual is the per-server convenience limitation captured in G10/KI-22.
- The 25 MCP policy tests added this session are the automated backing for the "MCP tools in agent + global tool lists, allow/deny works" requirement; see the UAT plan appendix.

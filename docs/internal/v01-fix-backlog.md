# v0.1.0 Consolidated Fix Backlog

Merges: **12-reviewer codebase pass** (2 sets of 6, entire codebase) + **8 feature-completeness gaps** + **unfixed first-pass MAJORs** + **real failing tests** surfaced by the diff-scoped run. One prioritized list; fix top-down.

Branch: `feat/level1-project-task-mgmt`. Author all commits as Daniel Piatkowski (GitHub no-reply), no anthropic trailers. Build tags `goolm,stdjson`.

---

## P0 — BLOCKER: failing tests on the branch (must be green)
Surfaced by diff-scoped run (pod 8c/15GB, `-p4`):
- **T1** `pkg/memrooms/index` — 4 fails: `TestBleveBM25_NoEmbeddings`, `TestBleveIndex_RebuildFromMd`, `TestBleveIndex_BM25RankingOrder`, `TestBleveIndex_EmptyQueryReturnsAll`
- **T2** `pkg/agent` — `TestRecall_BleveCountersJSONL_AccessWritten`
- **T3** `pkg/gateway` — `TestReplay_CtxCancelled_StopsCleanly`

## P0 — CRITICAL (8, from reviewers)
- **C1** `src/lib/api.ts:554` — 204-delete bug: `res.json()` called unconditionally on No-Content → delete throws.
- **C2** `src/components/agents/ToolApprovalModal.tsx:130` — a11y: approval gate not keyboard/screen-reader operable (security-relevant control).
- **C3** `src/components/.../McpServerModal` — form inputs unlabeled (a11y).
- **C4** `pkg/sysagent/tools/{channel,mcp,diag}.go` — 8 stub tools advertise capabilities to the LLM but no-op/lie (channel enable/disable/test, mcp add/remove/list, backup.create, cost.query). Wire honest descriptions + remove from seed.
- **C5** `pkg/sandbox/hardened_exec.go:7` — false security comment (claims enforcement that isn't there).
- **C6** `pkg/security/execapproval.go:146` — exec-approval consent lost (decision not persisted/propagated → re-prompts or silently allows).
- **C7** `pkg/mcp/manager.go:322` — MCP child env-leak: spawns MCP server with `os.Environ()` → leaks `OMNIPUS_MASTER_KEY` etc. Must scrub.
- **C8** `pkg/gateway/websocket.go:1679` + `src/store/chat.ts` — chat-stream-hang: stream can wedge UI in a permanent "thinking" state.

## P1 — first-pass MAJORs (not yet fixed)
- **M1** blocked_by cycle-detection miss (DAG can form a cycle).
- **M2** MinHash all-zero signature + title/body asymmetry + Jaccard panic (memrooms/minhash) — likely tied to T1.
- **M3** opencode driver double-prompt.
- **M4** codex driver per-turn End emitted incorrectly.
- **M5** WorkspaceDelete cascade runs unlocked (race vs concurrent writers).
- **M6** memory entries use `time.Now()` at write (non-deterministic; should thread a clock) — likely tied to T2.
- **M7** `ScanMemories` swallows errors with no logging.

## P1 — feature-completeness gaps (8, from spec audit)
- **G-a** re-auth gate cluster (FR-12.2/6.6/3.3): only Integrations PUT is re-auth-gated; model-key, performance, and agent-grant mutations are ungated.
- **G-b** channel-instance surface (FR-2.3 cap-1 API returns 422 / FR-2.5 `instance_id`+`identity` not populated, identity routing missing).
- **G-c** delegation modes/depth unenforced + trust-graph UI missing (FR-6.2).
- **G-d** bundle-manifest shape MISSING (FR-10.2, ADR-019 NFR-7 mandate).
- (FR-5.1 Resume = acceptable defer.)

## P2 — IMPORTANT (reviewers)
- **I1** reconnect `since`-cursor only advances on `replay_message` → redundant re-replay on reconnect.
- **I2** real `ServerFrame` types `exec_approval_response_ack`, `device_pairing_request` have no reducer case → trip "unknown frame" toast.
- **I3** dead code to delete: `pkg/tools/build_static.go` (505 lines), `pkg/tools/skills_remove.go`, `config.AppSecretRef2` field, `buildRegistryManager` bypass (cmd/.../skills/helpers.go:205,404), 5 dead logger funcs, 6 dead provider constructors, `GetChannelAllowFrom`.

## P3 — SUGGESTION / cleanups
- test-only orphans (parse.go stream parsers, MergeAPIKeys, extractFrontmatter, newMCPToolAdapter, BuildAuthorizeURL, …).
- duplication: providers `fallback.go` Execute/ExecuteImage; CLI drivers exec-spawn; `factory_provider.go` create*AuthProvider; `stringSlicesEqual`→`slices.Equal`.
- `projectsStore`→`workspacesStore` rename; Command-Center dead-code cluster (~11 files).
- channel metadata-descriptor to collapse the 3-location triplication (#151-adjacent).

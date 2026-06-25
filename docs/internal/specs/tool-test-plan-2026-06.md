# Production-Grade Tool Test Plan (2026-06)

**Scope:** every agent tool (~78 across `pkg/tools`, `pkg/sysagent/tools`, `pkg/tools/browser`) plus the time-bound subsystems (heartbeat, scheduled/recurring tasks, drains, idle-close, retention) and the **compressed-manifest reliability** mechanism. Goal: excellent coverage with no production surprises — each tool gets happy-path **and** edge/failure cases, driven through real seams (real files, real subprocesses, real Chrome, real channel delivery, a real served site, real multi-MCP discovery) wherever a unit stub would hide a bug.

**Status:** plan. Grounded in a coverage/harness audit (2026-06). Build tags for all Go tests: `-tags goolm,stdjson`, `CGO_ENABLED=0`. CI authority = ci-omnipus worker.

---

## 0. Guiding principles

1. **Test through the real seam, not the stub, for anything that fails in production.** A mocked provider proves wiring; `mockChannel.sentMessages` proves *delivery* (not enqueue); a real `httptest` page proves the browser tool; a real `vite build` dir proves `serve_web`. Reserve pure unit tests for pure logic (classification, validators, parsers).
2. **Every tool: happy path + ≥2 failure/edge cases.** Deny/policy, malformed args, not-found, timeout, size cap, confinement escape, idempotency.
3. **Determinism over wall-clock.** Use the injectable seams (`cron.Clock`+`RunDueJobs`, `retentionSweepFn`, direct `executeHeartbeat()`/`Drain()`/`CloseSession()` calls). Only fall back to tiny real intervals where no seam exists; flag those as testability debt.
4. **Tier the suite** so the fast unit gate stays green in CI without Chrome/LLM, and heavy/real-resource gates run opt-in.
5. **Assert the security boundary, not just the feature.** Path traversal, SSRF (incl. post-redirect), env-allowlist leak, command-injection, scheme blocks, deny-by-default policy, sandbox confinement.

---

## 1. Test tiers & CI mapping

| Tier | What | Gate | Resource | Runs in |
|---|---|---|---|---|
| **T1 Unit** | pure logic, validators, single-tool Execute against tmpdir/fakes | `go-test` | none | every push (fast) |
| **T2 Integration (in-proc)** | tool → loop → bus → channel; multi-tool chains; httptest gateway; manifest load→call via scripted provider | `go-test` | none | every push |
| **T3 Real-resource (gated)** | real Chrome (chromedp), real Tier-3 dev server, real stdio MCP subprocesses, real exec sandbox edges, real served vite dir | `go-test` + env flags | Chrome / Linux / node | nightly + pre-merge |
| **T4 Live-LLM e2e** | Playwright over real gateway + glm-5.2: does the *model* drive the tool correctly (manifest load→call, delegation, handoff) | `e2e` | OPENROUTER_API_KEY_CI | nightly + pre-merge |

Env flags: `OMNIPUS_BROWSER_E2E=1` (+ Chrome on PATH) for T3 browser; `runtime.GOOS=="linux"` gate for Tier-3 serve/`workspace_shell_bg`; `OPENROUTER_API_KEY_CI` for T4.

---

## 2. Harness reference (reuse, don't reinvent)

Authoritative seams found in the audit (file:line):

- **Scripted LLM provider** — pattern `scriptedToolProvider` (`pkg/agent/eventbus_test.go:105`): a per-turn counter returns different `LLMResponse.ToolCalls`. Drive via `mustNewAgentLoop(t,cfg,bus,provider)` (`pkg/agent/test_helpers_test.go:18`). **This is the keystone for manifest load→call and delegation tests.** → *Add a generic `ScriptResponses([...])` helper (small lift) to stop every test rolling its own.*
- **Bus + channel spy** — `bus.NewMessageBus()`; inject via `PublishInbound`/`processMessage`; capture via `OutboundChan()`. Delivery assertion = `mockChannel.sentMessages` (`pkg/channels/manager_test.go:19`) after `w.ch.Send` (`pkg/channels/manager.go:1160`). `fakeMediaChannel` records `sentMedia` for `send_file`.
- **Gateway** — `newTestRestAPIWithHome(t)` (`pkg/gateway/rest_extra_test.go:32`) for persisted endpoints; full boot `StartTestGateway(t, …)` (`pkg/agent/testutil/gateway_harness.go:134`) with `WithAllowEmpty/WithBearerAuth/WithSandboxConfig`. WS: `dialTestWS`+`sendWSAuthFrameDevMode`. SSE: `newSSEHandler`.
- **Browser** — `NewBrowserManager(BrowserConfig{Headless:true}, ssrf)` + `startTestServer(t)` (`pkg/tools/browser/browser_e2e_test.go:60`) + `skipIfNoBrowser(t)` (`:44`). DevTools port pinned 9223. JS eval is real via `chromedp.Evaluate`.
- **Clock** — `cron.Clock`+`SetClock`+`RunDueJobs(now)`+`RunNow(jobID)`+`WaitForLane()`+`SetOnSkip` (`pkg/cron/service.go`); `task_trigger` delegates. Memory/cooldown/media have `nowFunc`. **No seam:** idle-close timer, heartbeat ticker (but `executeHeartbeat()` callable), audit/session retention (use `retentionSweepFn` var / backdated mtimes).
- **Sandbox** — `sandbox.NewFallbackBackend()` for unit; `sandbox.Run(ctx,argv,env,Limits)`; enforce-mode only in forked subprocess (Landlock is a one-way ratchet).
- **MCP** — **stdio subprocesses only** (config `Command`). `LoadFromMCPConfig` + `sandboxed_stdio_test.go` `cat`-style transport. For search-index tests, `RegisterHidden(NewMCPTool(mgr,server,tool))` directly. → *Add a tiny stub JSON-RPC stdio MCP binary for protocol-level + multi-server discovery fidelity.*
- **Skills/ClawHub** — fully offline via `httptest` + injected base URL (`pkg/skills/clawhub_registry_test.go:20`); zip fixtures via `createTestZip`.
- **e2e** — `tests/e2e/global-setup.ts` seeds glm-5.2 + onboards; requires `OPENROUTER_API_KEY_CI`. workers:1, timeout 90s.

---

## 3. Per-domain test matrix

Each tool: **H** = happy path, then edge/failure cases. New work flagged `[NEW]`; existing-but-thin flagged `[EXTEND]`.

### 3.1 Filesystem (read/write/edit/append/list_directory) — mostly STRONG
- Keep STRONG coverage; **[EXTEND]** add: atomic-write failure (disk-full/rename fail simulated via read-only dir), concurrent append/write ordering, symlink-loop in `list_directory`, binary-file `edit_file` replace, corrupt DOCX/XLSX in doc-extract.
- Security (already good): path traversal matrix stays (`tests/security/path_traversal_test.go`).

### 3.2 Web (search_web, fetch_url, serve_web)
- **search_web [EXTEND]**: provider-fallback chain (provider A fails → B), rate-limit/429 handling, empty-results, malformed provider JSON. (Today: all providers mocked happy-path.)
- **fetch_url**: keep SSRF matrix; **[EXTEND]** redirect-loop, charset/encoding, oversized body cap, malformed headers.
- **serve_web (the user's "real vite site" requirement) [NEW]** — T3, biggest gap:
  1. **Real static site E2E:** temp dir (`index.html`+`app.js`+`style.css`) → `ServedSubdirs.Register` → bind preview listener on ephemeral port → real `http.Get http://127.0.0.1:{previewPort}/preview/{agent}/{token}/` → assert 200, body, per-asset `Content-Type`.
  2. **Real `vite build` output:** fixture a `dist/` (`index.html` + `assets/index-*.js`) → serve → fetch hashed asset by exact path → assert JS MIME + 200. (Fixture the build output to avoid a node build-time dep in the unit gate; a separate T3 case can run a real `vite build`.)
  3. **Tier-3 real dev server (Linux):** allow-listed dev command → `executeDev` → TCP-probe passes → proxy `GET /preview/.../` returns child's body → teardown reaps + `Unregister`.
  4. **Token expiry → 404**, **per-agent replacement** (A's token 404s after B registers), **bind-failure path** (command never binds → 3s probe fails → SIGTERM + IsError), **preview-disabled fail-fast** (empty preview base URL → IsError).

### 3.3 Comms / channels delivery (send_message, send_file) — unit STRONG, delivery untested
- **send_message [NEW] T2** — *delivery vs enqueue* is the key distinction:
  1. Register `mockChannel` "test", start manager, publish → poll `sentMessages` → assert exactly-one with right content/chat/channel reached `Send`.
  2. **Unknown channel** → nothing reaches any `Send`, drop recorded. **Disabled / `ErrNotRunning`** → no retry, single drop notice.
  3. **Retry semantics:** transient error ×N then success → backoff + eventual delivery; permanent error → single attempt + drop.
  4. **Streaming skip:** finalized stream → `preSend` skips `Send`, deletes placeholder.
  5. **Queue backpressure:** fill `w.queue` → drop-notice frame, no block.
  6. **WebChat E2E:** token/done frames land on in-memory WS client.
- **send_file [NEW] T2**: valid file → media store → `MediaResult` ref → publish → assert `SendMedia` (not `Send`) on mock channel; reject file-too-large / disallowed-type.

### 3.4 Email (read_inbox/read_message/reply/search_email/send_email) — STRONG (fake transport)
- Keep. **[EXTEND]** optional T3: one real SMTP/IMAP round-trip against a throwaway test mailbox (e.g. a local greenmail-style server) to catch transport-encoding bugs the fake hides. Low priority.

### 3.5 Memory (remember, recall_memory, run_retrospective) — STRONG
- Keep. **[EXTEND]** assert `run_retrospective` emits its audit entry; add room-aware variants when FR-7.3 lands (v0.3).

### 3.6 Tasks (set_todos, create/list/update/delete_task, *_task_in_workspace)
- **update_task [NEW]**: dedicated `pkg/tools` unit (currently only via sysagent variant) incl. dependency-recompute on update.
- **create_task [EXTEND]**: end-to-end create→list→read chain (T2); delegation-policy gate.
- **delete_task_in_workspace [NEW]**: delegation gate + cascade-on-delete.
- **list_tasks_in_workspace [NEW]**: dedicated assertions (filtering/shape).
- Scheduled/recurring task *execution* covered in §4.

### 3.7 Skills (find_skills, install_skill, create/edit/list/remove_skill)
- **find_skills [EXTEND]**: real BM25 search path (not just mocked cache hit), `limit` param, ranking across multiple skills.
- **install_skill [EXTEND]**: actual file-install + version-pin via the offline httptest+zip harness; failed-download/corrupt-zip.
- **create/edit_skill**: invalid frontmatter / missing metadata / edit-nonexistent.

### 3.8 Delegation / spawn (spawn, run_subagent, check_spawn_status, hand_off, return_to_default) — GOOD/STRONG
- **[EXTEND]**: spawn task-label lifecycle; `run_subagent` concurrent/multi-task stress; `check_spawn_status` concurrent-update races; `hand_off` context_summary truncation; `return_to_default` already-on-default no-op.
- **T4**: handoff + subagent driven by the real model (existing `handoff.spec.ts`, `subagent.spec.ts`) — keep + assert tool-call sequence.

### 3.9 Agents-mgmt (create/update/delete/activate/deactivate_agent) — re-enable skipped tests
- **Un-skip the `t.Skip`-blocked integration tests** in `pkg/sysagent/sysagent_test.go:769-810` (`TestAgentCreateIntegration`, `TestAgentDeleteIntegration`, …) — these were meant to be the Execute-level coverage. This is the single highest-leverage fix.
- **update_agent [EXTEND]**: provider/isolation field mutations; **subagent_3p external-worker create path** (the create_agent external-CLI worker) — assert Type=worker + Executor{external-cli,CLI,CLIPath}.
- **delete_agent [EXTEND]**: workspace cleanup/cascade. **deactivate_agent**: already-disabled + error breadth.

### 3.10 Workspaces (create/update/get/list/delete_workspace) — THIN (Execute untested)
- **[NEW] Execute-level tests for all five** (currently only cross-cutting RBAC/confirmation). Happy path + invalid-name + not-found + **delete cascade** (tasks/sessions under the workspace).

### 3.11 Channels-mgmt (list/enable/disable/configure/test_channel) — GOOD
- **[EXTEND]**: enable/disable state across config reloads; disable active-session cleanup; configure credential-type matrix (token/secret/password/key/api_key → ref); `test_channel` timeout simulation.

### 3.12 MCP-mgmt (add/list/remove_mcp_server) — GOOD
- **[EXTEND]**: sse/http vs stdio URL validation, env-var injection scrub, double-remove/confirm-required, empty-state list.

### 3.13 Providers (configure/list/test_provider, list_models) — configure STRONG, rest THIN
- **test_provider [NEW]**: Execute-level — key-resolved-OK, not-configured, bad-key. **list_models [NEW]**: Execute output shape. **list_providers [NEW]**: dedicated unit. **configure_provider [EXTEND]**: api_base URL-format validation.

### 3.14 Config (get_config, set_config) — THIN
- **set_config [NEW]**: normal successful set, key validation, type coercion (today only rollback-on-save-fail). **get_config [NEW]**: valid-key read + sensitive-key block.

### 3.15 Diag (run_doctor, query_cost)
- **run_doctor [NEW]**: un-skip the integration test; assert the security-check logic at Execute level (the checks it actually runs). High blast radius, currently 0 behavioral.
- **query_cost**: intentional NOT_IMPLEMENTED stub — assert it stays a clean stub until implemented; no coverage debt.

### 3.16 Metadata (read/write_agent_metadata) — GOOD/STRONG
- **[EXTEND]**: cross-agent read policy; large-file/concurrent writes.

### 3.17 navigate — THIN
- **[NEW]**: Execute-level target validation (valid route, unknown route, RBAC).

### 3.18 Browser (navigate/click/type/get_text/screenshot/wait/evaluate) — CI-skipped, must wire T3
- **Wire `OMNIPUS_BROWSER_E2E=1` + a Chrome binary into the ci-worker** so these run nightly (the ci image already has Playwright browsers).
- **[NEW] drive all 7 through `tool.Execute`** (not just raw chromedp) against `startTestServer` fixture:
  1. `navigate → wait(#btn) → click(#btn) → get_text(#result)` happy chain; assert each JSON result.
  2. **browser_evaluate**: `document.title` → correct JSON; throwing JS → IsError; `executeEnabled=false` → disabled IsError; value-shape edges (`undefined`→null, large string, DOM node, circular obj, long-running eval → clean context timeout).
  3. **Scheme blocks** via Execute: `file://`, `data:`, `javascript:`, `chrome://` → scheme-block IsError before network.
  4. **Post-redirect SSRF**: fixture 302 → `169.254.169.254` → lands `about:blank` + error (today untested).
  5. **get_text 100KB cap** truncation; **screenshot** file-write failure/cleanup; **wait** timeout/selector-not-found; **session persistence** + MaxTabs limit.

---

## 4. Time-bound subsystems

| Subsystem | Deterministic test method | New work |
|---|---|---|
| **Heartbeat (global)** | call `hs.executeHeartbeat()` directly w/ stub handler + temp HEARTBEAT.md (task line) → assert handler+bus invoked | [EXTEND] assert the prompt-built-from-tasks path + outbound routing flags |
| **Heartbeat (per-agent = cron `every`)** | fake clock + `RunDueJobs(now)` | [EXTEND] reconciler edge: subagents never scheduled |
| **Scheduled/recurring tasks (TaskTriggerScheduler + cron)** | `SetClock(fake)` → upsert triggered task → advance → `RunDueJobs` → `WaitForLane` → assert reset+dispatch | [EXTEND] `once`/`every`/`recurring`→cron mapping; overlap `ErrAlreadyRunning` skip via `SetOnSkip`; terminal/heartbeat tasks skipped |
| **Queued-task drain (TaskDrainService)** | call `executor.CheckQueuedTasks(ctx)` directly | [NEW seam] add `fireOnce()` to drop the tiny-interval dependency |
| **Mailbox drain (MailboxDrainService)** | call `drainer.Drain(ctx)` w/ stub MailboxProvider → assert Board tasks + count | keep (strong) |
| **Idle session-close + recap** | recap: call `CloseSession(sid,"idle")` directly → assert LAST_SESSION.md/retro/audit, then `Close()` drains recapWG | **[NEW seam — the one real gap]** the idle *timer* has no sub-minute/clock seam; add exported `fireIdleTimeout(sessionID)` hook **or** make timeout `time.Duration`-granular so a test sets ~10ms, then assert the timer→CloseSession("idle") path (currently 0% behavioral) |
| **Session retention sweep** | override `retentionSweepFn` (20ms tick) or call `store.RetentionSweep(days)` on backdated mtimes | keep (strong) |
| **Audit retention/rotation** | backdated entry timestamps | [EXTEND] rotation boundary |

**Testability debt to land before/with the plan:** (a) idle-timeout `fireIdleTimeout`/duration seam (`pkg/agent/loop.go:852-878`); (b) `TaskDrainService.fireOnce()`; (c) generic `ScriptResponses` provider; (d) stub stdio MCP binary. All are small, in-repo, no human needed.

---

## 5. Compressed-manifest reliability (the user's explicit concern)

The manifest changes *every* turn's tool surface; its reliability must be tested at three altitudes:

**5a. Mechanism (T1/T2, deterministic — exists, extend):**
- Classification totality; `BuildCompressedManifest` grouping/ordering/truncation/loaded-exclusion; `load_tool` honesty (rejected populated); session-scoped loaded state + eviction; **reachability invariant per agent** (every policy-allowed tool full-in-defs OR lazy-in-manifest-and-loadable); **exec-authorization** (infra tools executable for deny-default agents — the live-found gate); backward-compat (flag off = name-exact identical, no manifest note); token-win sanity.

**5b. Protocol-following with the real model (T4 — the reliability question):**
- For each base agent whose role tools are lazy (Ava→create_agent, Mia→create_task/email, Jim→tasks/workspace, Ray→spawn): a live spec that asks the agent to do its core job and asserts the audit shows `load_tool→allow` then `<lazy_tool>→allow` and the real side-effect occurred. *(Already proven once for Mia/create_task; turn it into a standing matrix across agents + ≥1 tool each.)*
- **Multi-tool-in-one-turn load:** ask for an action needing 2+ lazy tools; assert a single `load_tool(["a","b"])` then both callable.
- **Search-then-load:** ask for a capability by description (not name); assert `search_tools_*` → `load_tool` → call.
- **Negative:** flag OFF → assert no manifest note injected and all tools full (behavioural parity).

**5c. Token measurement (T2):** assert compressed `providerToolDefs` JSON bytes materially < full for a broad agent (Jim), and that loading N tools grows it monotonically — guards the optimization from silently regressing to all-full.

**5d. Reliability across providers (T4, opt-in):** run 5b against a second model (e.g. gemini-2.5-flash, claude-haiku) to detect model-specific load-protocol flakiness early. Record pass-rate; if a model narrates instead of calling `load_tool`, that's a finding for prompt-tuning the manifest header, not a code bug.

---

## 6. Multi-MCP tool-search fidelity (the user's explicit concern)

- **T2 (index-level):** registry with hidden tools from two "servers" — `mcp_alpha_search_repos` ("Search repositories"), `mcp_beta_query_docs` ("Query documentation"). Assert `search_tools_bm25("search repositories")` → alpha top; `("query documentation")` → beta. Ranking with overlapping descs. Version-keyed cache refresh when a 3rd server's tool registers. Promotion TTL lifecycle (matched tool `Get`-able then hidden after `TickTTL`). Regex across servers + invalid/oversized pattern rejection. Concurrent register+search (race).
- **T3 (protocol-level, higher fidelity):** two real stdio MCP subprocesses (stub JSON-RPC binary) each advertising one distinct tool → `LoadFromMCPConfig` → loop registers as hidden → assert a search finds the right `mcp_<server>_<tool>` from the right server. This is the only way to catch discovery/namespacing/cache bugs the unit stub hides.

---

## 7. exec / workspace_shell / workspace_shell_bg (sandbox edges)

T3, real subprocess where noted:
1. **Denied binary (real)** via non-nil `PolicyAuditor.EvaluateExec` deny → IsError *before* spawn + deny audit (current gap: most tests use nil auditor).
2. **Timeout kills child + partial output** on both legacy and hardened paths; orphan gone.
3. **>4 MiB output cap (real subprocess)** truncation notice, no OOM; legacy >10k-char truncation.
4. **Env-allowlist leak (real `env`)**: inject `OMNIPUS_MASTER_KEY/BEARER_TOKEN` + `LC_ALL/XDG_*/OMNIPUS_CHILD_*` → secrets absent, allow-listed survive.
5. **CWD confinement**: `..` escape + absolute path rejected; valid subdir `pwd` inside; symlink-escape.
6. **workspace_shell_bg start→serve→kill (Linux)**: bound server reachable through `/preview/`, then reap (SIGTERM) + token 404.
7. **exec background session**: `run background=true` → sessionId → `poll/read/write/send-keys/kill` full sequence, no deadlock.
8. **Fork-bomb / mem rlimit (Linux, enforce)**: RLIMIT_NPROC/RLIMIT_AS abort child.

---

## 8. Cross-cutting suites

- **Tool-chain integration [NEW]:** `tests/integration` currently covers only sessions/handoff/replay. Add chains: `create_task → list_tasks → update_task → delete_task`; `remember → recall_memory`; `send_message → channel delivery`; `create_workspace → create_task_in_workspace → delete_workspace (cascade)`.
- **RBAC / policy matrix:** keep the 37-tool permission map; extend to the renamed/added tools (load_tool/search_tools as infra-always; delete_* as ask).
- **Contract tests:** every wire-format tool response stays schema-valid (`pkg/api/generated/contract_test.go`); `manifest_tier` enum.
- **Security suites:** keep path-traversal, SSRF (add post-redirect for browser), command-injection; add env-allowlist + scheme-block to the matrix.
- **Determinism/flake:** all T1/T2 must pass `-p 1` isolated and `-race` on the manifest/concurrency tests.

---

## 9. Prioritization (do in this order)

1. **Re-enable the 5 `t.Skip` integration tests** (`pkg/sysagent/sysagent_test.go:769-810`) — agents/provider/doctor Execute coverage. Highest leverage, smallest effort.
2. **Workspace CRUD Execute tests** (5 tools, THIN) + **set_config/get_config** + **run_doctor** + **test_provider/list_models** — the THIN, high-risk management surface.
3. **send_message/send_file delivery tests** (delivery-vs-enqueue via mockChannel) + **serve_web real static+vite** — the "does it actually work end-to-end" gaps the user named.
4. **Manifest reliability matrix (5b/5c)** + **multi-MCP search (T2+T3)** — the user's reliability concerns.
5. **Browser T3 wiring** (`OMNIPUS_BROWSER_E2E=1` in ci-worker) + all-7-via-Execute + JS-eval edges + post-redirect SSRF.
6. **Time-bound seams** (idle `fireIdleTimeout`, drain `fireOnce`) + their tests.
7. **exec/shell real-subprocess edge cases** (denied-binary, output cap, env leak, fork-bomb).
8. **Tool-chain integration suite** + remaining `[EXTEND]`s.

---

## 10. Testability refactors required (small, in-repo, no human)

| Refactor | File | Why |
|---|---|---|
| `fireIdleTimeout(sessionID)` hook or `time.Duration`-granular idle timeout | `pkg/agent/loop.go:852-878` | idle-close timer→CloseSession path is 0% testable (≥1 min wait today) |
| `TaskDrainService.fireOnce()` | `pkg/heartbeat/task_drain.go` | drop tiny-real-interval dependency |
| Generic `ScriptResponses([...])` provider helper | `pkg/agent/*_test.go` | every test rolls its own scripted provider |
| Stub stdio JSON-RPC MCP binary | new `pkg/mcp/testdata` | protocol-level + multi-server discovery fidelity |
| Wire `OMNIPUS_BROWSER_E2E=1` + Chrome into ci-worker | `deploy/ci-worker` | browser tools currently CI-skipped (0 behavioral in CI) |
| Fixture a `vite build` dist/ (+ optional real-build T3 case) | new `pkg/tools/testdata` | serve_web real-site test without node in the unit gate |

---

## 11. Open decisions for the human

1. **Effort/scope** — full build-out is large (~78 tools × multi-case + 6 refactors + 2 new harnesses). Deliver as one epic with sub-issues, or incremental by §9 priority?
2. **CI budget** — T3 (real Chrome + Tier-3 + MCP subprocesses) and T4-extended (multi-provider manifest reliability) add nightly minutes/cost on ci-omnipus. Approve nightly + pre-merge, or pre-merge only?
3. **Real external resources** — OK to add a throwaway SMTP/IMAP test server for email T3, and to run real `vite build` in a T3 case? (Both avoidable via fixtures if not.)
4. **Browser in CI** — approve adding a Chrome binary to the ci-worker image (image rebuild via `fly deploy`)?

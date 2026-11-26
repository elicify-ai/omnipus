## Tools: from registration to execution

Two separate lists of tools exist, and only one of them can run anything.

- **The catalog** is the global `BuiltinRegistry` (`pkg/tools/builtin_registry.go::RegisterBuiltin`). It holds descriptions only and backs `GET /api/v1/tools`. `pkg/gateway/central_builtin_registry.go::buildCentralBuiltinRegistry` fills it with five families (system, general, browser, knowledge, grep). The header of that file says its instances are never executed. `RegisterBuiltin` rejects a nil tool, an empty name or a duplicate; the central builder logs a duplicate and skips it, which is not fatal. The reserved `system.` prefix is not checked here: it applies to externally supplied MCP registrations (`pkg/tools/builtin_registry.go::ValidateMCPName`).
- **The execution registry** is one `ToolRegistry` per agent, created in `pkg/agent/instance.go`. Only a tool in this registry can be called.

**Who decides what an agent may use.** Policy has two layers: a global ceiling (`cfg.Sandbox.ToolPolicies`) and sparse per-agent overrides. `pkg/tools/compositor.go::mergeStoredPolicyLayers` merges them strictest-first (deny beats ask beats allow). If both sides are empty the result is deny, with an Error log. God Mode lifts only the global layer to allow; per-agent deny and ask still hold. A tool on the always-ask list never resolves to plain allow (`pkg/tools/always_ask.go::IsAlwaysAsk`). MCP tools also need an assignment (see the MCP section).

**Boot check.** `pkg/config/validate.go::ReconcileToolPolicyCeiling` only adds shipped defaults for builtins missing from the ceiling. It never overwrites. `pkg/gateway/gateway_sandbox.go::repairAndValidateToolPolicyCoverage` then checks coverage. A remaining gap aborts boot; on reload it rejects the new config and keeps the old one.

**Per request.** Before each model call, `FilterToolsByPolicy` drops denied tools and returns the allow/ask map (`pkg/agent/loop_run_turn_iterations.go` calls `prepareToolSurface`).

**Per call, one after another** (`pkg/agent/loop_run_turn_tools.go::executeToolCalls`): validate, quarantine and hooks, approval hook, a second policy check at execution time (a mid-turn change is denied and audited), ask resolution, dispatch.

**Ask resolution.** Auto shell mode, auto-approve verdicts, rules and session "Always Allow" grants settle first. If an ask is still open and the run is unattended (`AutoDenyAsk`), it is denied. Otherwise the turn waits for a person. The wait defaults to 600 seconds (`pkg/gateway/gateway.go::defaultToolApprovalTimeout`, configurable) and a timeout denies. Approval requests are held in memory and sent to every connected WebSocket.

**Execution** (`pkg/tools/registry.go::ExecuteWithContext`): look up, redact arguments, normalise and validate arguments, run inside a panic guard (a panic becomes an error result), turn a nil result into an error, normalise media, log and audit.

## MCP servers

MCP (Model Context Protocol) lets Omnipus use tools served by another program. Servers are configured in `cfg.MCPServers`.

**Connecting** (`pkg/mcp/manager.go::ConnectServer`). If no type is set, a URL means a remote server and a command means a local one. Remote servers use streamable HTTP/SSE, with optional headers, and the code logs that data leaves the machine. Local ("stdio") servers are child processes started once, with a scrubbed environment plus the server's own settings. They are launched through the sandbox start path, so the child inherits the gateway's kernel sandbox where active (`sandboxedCommandTransport`). On non-Linux or with the sandbox off it is a plain process start; exact coverage is under separate security review. After connecting, the manager lists the tools and stores the connection. Calls reuse that session; nothing is spawned per call.

**Lifetimes.** A local child lives until disconnect or `AgentLoop.Close`. Closing sends stdin close, then SIGTERM after 5 seconds, then SIGKILL. `Close` first waits for in-flight calls.

**Who connects, and when** (`pkg/agent/loop_mcp.go`). `ensureMCPInitialized` runs at the start of `Run`, skips when no server is enabled, and has a 15-second connect timeout. `ReconcileMCP` re-runs the difference between wanted and live servers: it disconnects removed, disabled or changed servers, connects the rest (one goroutine each), and re-registers every live connection's tools into each per-agent registry and the central MCP registry. The global switch `tools.mcp.enabled` off disconnects all.

**Failure.** One server failing (including a missing credential) is recorded for `MCPServerStatus` and does not stop the others. A systemic failure or cancelled context returns an error and leaves the running set untouched. In discovery mode, tools register hidden (`serverIsDeferred`).

**Assignment is separate from policy** (`pkg/tools/mcp_assignment.go::mcpAssignmentAllows`). An MCP tool is offered to an agent only if that agent has a binding for its server. An unspecified or `["*"]` tool list means all tools of that server. A binding is checked before the policy merge, so allow in policy does not grant an unbound server. Names starting `system.` cannot be registered (`RegisterMCP`).

## Skills

A skill is a folder with a `SKILL.md`. The model first sees a menu, and loads the full text only when it asks.

**Shelves** (`pkg/skills/loader.go::ListSkills`): workspace, global (`~/.omnipus/skills`) and builtin, in that priority. Duplicate slugs are dropped in favour of the higher shelf. Dot-prefixed folders are skipped. An oversized or unresolvable `SKILL.md` is left out with a warning.

**Menu** (`BuildSkillsSummaryFuncWithProject`): a `<skills>` block with slug, name, description and source. It has no size cap and shows no file paths. Registry and builtin entries pass a per-agent grant filter. Project skills (from a mounted folder's `.omnipus/skills`, then `.claude/skills`, `pkg/skills/project.go`) are gated by mount membership only. If a granted skill has the same slug, the project one is dropped.

**Name resolution** (`pkg/skills/shelf.go::ResolveSkillName`): a granted registry or builtin match wins and the clash is recorded. Otherwise the project shelf is tried (slug, then display name).

**Loading** (`pkg/skills/activation.go::LoadActivation`, reached through the `Skill` tool): validate the slug; find the folder under the registry root, falling back to the builtin root; resolve `SKILL.md` without following links beyond a read-link check; check the grant (`checkBinding`); read (builtin reads are size-bounded, registry reads take a lock and capture a certified revision, or a degraded read if capture aborts); render.

**Grants are bound to a generation.** An agent's grant must equal the folder's current generation. When the agent has a grant table, an unbound or unstamped folder is refused with `SKILL_GRANT_STALE` (`StaleGrantError`). With no table, only an unstamped folder loads. A skill changed after being granted therefore stops loading until re-granted.

## Browser

**One Chrome per workspace.** `pkg/tools/browser/coordinator.go` owns one Chrome process and one profile directory per workspace. Launch is guarded by a lockfile plus an ownership marker. `pkg/tools/browser/manager_local_startup.go::runLocalStartup` creates the profile directory (0700), cleans stale singleton files, finds Chromium and starts it with `launchManagedPipe`. The control channel (CDP) is a pipe on file descriptors 3 and 4 (`--remote-debugging-pipe`), so Chrome opens no debug port.

**Pool and lifetimes** (`pkg/tools/browser/pool.go`). `BrowserPool` keys instances by BrowsingKey. It never launches past a memory admission gate (it assumes about 182 MB per browser). A one-minute sweep closes idle instances after a time-to-live, never one with a live viewer or running call. A manager reload does not kill Chrome. A Chrome process ends on idle retirement (`CloseIdle`), on eviction when memory is needed for another browser, on an explicit close (`Close`) or on `Shutdown`; after any of these a manager re-registers on its next tool call. Profiles survive all of them, and only deleting the workspace deletes a profile.

**Handover.** `browser_handover` (`tools_handover.go`) takes a plain-text reason (up to 200 characters), puts the browser in the operator's hands and ends the turn normally. It is refused if the operator turned the feature off.

**Live video.** WebRTC is the only live-video path. Signalling rides the authenticated `/api/v1/browser/ws`, with offers ordered by epoch (`pkg/gateway/browser_webrtc.go::handleWebRTCOffer`). A separate input-only peer carries pointer and key events (`webrtc/dedicated_input_peer.go::DedicatedInputPeer`). Failures send a `browser_webrtc_state` frame with a reason; there is no silent fallback to still images.

**Listeners besides the main HTTP port.**

- `/api/v1/browser/capture-ingest` is on the main mux, loopback-only (403 otherwise), token-checked (`captureIngestWSHandler`).
- A shared UDP media socket if `tools.browser.webrtc_media_udp_port` is above 0 (otherwise ephemeral ports). On a bind error it tries up to 16 higher ports.
- An optional ICE-TCP listener (`webrtc_media_tcp_port`).
- An optional embedded TURN relay on UDP and optionally TCP (`webrtc/turnserver.go`).
- Loopback proxies: egress (`pkg/sandbox/egress_proxy.go`) and exec (`pkg/security/execproxy.go`).

**Who owns an attachment** (tab, capture and viewer). The pool is keyed by `BrowsingKey`; WebRTC offers are ordered by epoch in `handleWebRTCOffer`, and `/api/v1/browser/ws` is behind the ordinary sign-in. The exact key that ties a tab, a capture stream and a viewer together, and the access check at each hop: not traced (Unknown).

There is no separate preview port (`pkg/gateway/gateway.go` header). Dev-server ports are probed on 127.0.0.1 (`pkg/sandbox/dev_servers.go::portAvailable`); that the children bind them is not verified.

## Knowledge base, Library and media

Three stores, with different rules.

**Library** is a view of the workspace's work folder, not the media store. `pkg/library/root.go::OpenRoot` opens `workspaces/<id>/work` (created 0700) with Go's `os.Root`, which refuses any path that escapes. `CleanRelPath` rejects absolute paths, `..`, NUL and backslash. Each mount gets its own root; a missing target is listed but cannot be entered, so a broken mount can still be removed. It is the same folder the agent's file and exec tools are confined to.

**Media** (`pkg/media/library/library.go`). Each workspace has a `manifest.json` of entries with a UUID, name, type, size, sha256, reference count and source. `Read` rejects non-UUID ids, returns `ErrEntryStranded` or `ErrNotFound`, reads at most size+1 bytes and checks size and sha256; a mismatch is `ErrIntegrityCheckFailed` with no bytes. References look like `media://workspace/<ws>/<id>`; `authorizeWorkspaceRef` requires the caller's workspace to equal the owner's. The path variant skips the integrity check for transport callers. Deletion cascades and an orphan sweep exists. A current-turn attachment goes into the user message passed to the model without a tool call (`pkg/agent/context.go::BuildMessagesForSession`, flow 8).

**Knowledge** has two derived indexes, both rebuildable.

- Full text (`pkg/knowledge/index.go::OpenIndex`): a bleve index under `$OMNIPUS_HOME`, keyed by the hash of the collection's real root, so one folder mounted in several workspaces shares one index. Bodies are indexed, not stored; excerpts are re-read from disk. Attachments are indexed by name and path only. A corrupt or untrusted index is removed and rebuilt.
- Properties (`pkg/records/propindex/sqlite.go::Open`): pure-Go SQLite, owner-only permissions, `schemaVersion = 4`. A version mismatch rebuilds rather than migrates. Build tags can swap it for a stub.

Agents reach knowledge through eight tools (`knowledge_describe`, `_find`, `_read`, `_list`, `_edit`, `_restructure`, `_configure`, `_base_create`), registered in `pkg/gateway/gateway_sandbox.go`.

## Chat apps (connectors) in general

A connector is a compiled-in Go package for one chat app. Each registers a factory in `init()`; blank imports in `pkg/gateway/gateway_reload.go` pull them in. Matrix needs the `goolm` build tag, else a stub factory fails on purpose.

**Start-up order** (`pkg/channels/manager.go::initChannels`). For each configured instance: skip if disabled; default its type to its key; map the type to a factory name (`whatsapp` becomes `whatsapp_native`); run the type's prerequisite check (a failure logs a warning and skips the instance without recording it); call the factory with config, secrets and the bus; inject optional helpers (media store, placeholders, owner, cancel interceptor); publish the instance unless a newer config write won.

**Who is fatal.** `bootFatalError` joins failures that are not WhatsApp or Matrix and not "credential-scoped" (a missing credential reference). A non-nil result aborts boot; the rest degrade and show in `FailedChannels`. An unregistered factory is fatal.

**Running** (`StartAll`). Each started channel gets two queues (text 16, media 16), a rate limiter and two worker goroutines (`newChannelWorker`, `runWorker`, `runMediaWorker`). Shared dispatchers send outbound text and media and a TTL janitor runs. A channel whose `Start` fails is skipped; if all fail, `StartAll` errors. The shared HTTP server binds with 3 attempts at 500 ms and has 30-second read and write timeouts.

**Transports differ.** Only LINE (`line/line.go::WebhookPath`) and Google Chat (`/webhook/google-chat`) serve webhooks on the shared server. Telegram polls, Slack uses Socket Mode, Discord a websocket session, DingTalk Stream Mode, IRC a persistent connection. Feishu, QQ, WeCom, Weixin, WhatsApp and Matrix are not audited here.

**Inbound** (`pkg/channels/base.go::HandleMessage`): sender allowlist (empty allows all; a rejected sender is recorded and dropped), session-control interception, then a `bus.InboundMessage`. Typing, reaction and placeholder hooks are best-effort. It ends in `bus.PublishInbound`. Voice notes needing transcription use a per-chat serial lane of at most 200 queued items, with a refusal reply when full (`pkg/agent/connector_lane.go`).

## Configuration and agent records

Three places hold state, and each has its own writer.

| Kind | Location | Written by |
|---|---|---|
| Settings | `config.json` | settings API, file edits |
| Agent record | `$OMNIPUS_HOME/entities/agents/<id>.json` | `pkg/agentstore` |
| Agent home files | `agents/<id>/SOUL.md`, `AGENT.md` | agent or operator |

`pkg/entity/store.go::Store` keeps one JSON file per entity. `WithLock` takes an in-process lock plus a sidecar file lock (never removed) for create, update, delete and list. Agent records sit outside the agent-writable `agents/<id>/` tree on purpose (`pkg/agentstore/store.go` header). `agentstore/state.go::readStateFiles` reads the record, `SOUL.md` and `AGENT.md` together under one revision (`ErrRevisionConflict`). The prompt loader reads only `SOUL.md` from the workspace plus the global `USER.md` (`pkg/agent/definition.go::loadAgentDefinition`).

**A settings write** (`pkg/gateway/rest_config.go::refreshConfigAndRewireServices`): load config; load agents strictly from the entity store (failure rejects the refresh); resolve credentials (a store-level failure rejects; one integration's missing secret only degrades it); re-arm the secret scrubber; swap the live config under a lock and bump its generation (`SwapWrittenConfig`); apply the log level. The write's hash is registered so the file watcher ignores its own change.

**A reload** (`pkg/gateway/gateway_reload.go`). A watcher polls every 2 seconds; changes coalesce. `executeReload` snapshots services, prepares the config, then `handleConfigReload` stops pollers, locks writes, builds a fresh agent registry (30-second limit, panic-guarded; one with an init error is refused) and swaps it in. `restartServices` keeps cron running (settings re-applied), rebuilds TaskDrain and MailWatch, replaces the media store, keeps the channel manager and re-detects voice transcription.

**Failure.** The old state is restored and `/health` returns 503 until the next success. Chrome and entity files survive a reload.

## Chat history and reconnect

The session archive (day-partitioned JSONL) is the truth. A derived index in `<session dir>/.chat-history` makes paging fast and can be rebuilt.

**Reading a page** (`pkg/session/chat_history_select.go::readChatHistoryPage`): bring the index up to date; under the session lock, load the checkpoint and open the index; retain that generation file and release the lock; run the selection at that fixed cut; re-check the retention generation. If it changed, retry up to 3 times, then report `index_inconsistent`. Storage errors become `storage_error`.

**Healing** (`chat_history_heal.go::healChatHistoryOwned`). The only trigger is `catchup_limit` (index too far behind). Limits: 60 seconds between attempts; 3 counted transient failures, then a latched state; one heal at a time process-wide (busy counts as a transient failure); source up to 32 MiB; 30-second timeout. Boot and retention use a non-healing reconcile.

**On reconnect** (`pkg/gateway/history_page_websocket.go`). `handleHistoryAttachFrame` strips the history options, runs the normal session access check and validates them. `sendPreparedHistorySnapshot` then reads a page.

- Success: a `session_snapshot` frame (sequence, boot id, earlier/later flags, cursors), history state "available", replayed frames (a pending question goes before "done"), the current projection, then catch-up complete with `snapshot`.
- Failure: history state "unavailable" with a reason (`index_missing`, `index_inconsistent`, `catchup_limit`, `projection_error`, `storage_error`; `history_page_http.go::historyStateForError`), the connection marked `live_only`, the current projection, then catch-up complete with `live_only`. The session still works live. Later journal catch-up does not clear an earlier unavailable state.

The hub's own fan-out rules (`ws_session_hub.go`) were not verified.

## Build and packaging

**One binary, in this order.** `make build` depends on `spa-embed` and `generate`, then runs `CGO_ENABLED=0 go build ... ./cmd/omnipus` (`Makefile`).

1. `spa-embed`: `npm ci`, `npm run build`, delete `pkg/gateway/spa`, copy `dist/spa` into it.
2. The npm script is `tsc -b && vite build && npm run test:mail-editor-bundle`; Vite writes to `dist/spa` (`vite.config.ts`).
3. `pkg/gateway/embed.go` has `//go:embed all:spa`. `pkg/gateway/spa/` is gitignored, so it must exist at compile time. The gateway serves it from the embedded files (`fs.Sub(spaFS, "spa")`) with a content security policy.

Node is needed only to build. The running program is Go only, with no CGo.

**Build tags** change what is compiled.

- `goolm,stdjson` are the defaults (`GO_BUILD_TAGS`). Without `goolm` the real Matrix connector is excluded and a stub registers the `matrix` factory, which returns an "unavailable, rebuild with -tags goolm,stdjson" error when that channel is enabled (`pkg/channels/matrix/init_stub.go::init`). No untagged build was run for this document.
- `lite` drops native WhatsApp and WebRTC live-browser video. The Makefile has no `build-lite` target now, but the tag still gates code.
- `nogodmode` is an optional tag.
- `records_no_sqlite` and some operating systems and CPUs select a stub for the properties index (`propindex`).

No build was run for this document. Skipping the SPA copy produces a binary with a stale or missing UI.

## Concurrency: what runs at the same time

Goroutines are Go's lightweight tasks. Ordinary queued intake for one conversation is serialized by that conversation's worker, so its queued messages are taken one at a time. Other entry paths (scheduled runs, task runs, helper launches, the system-message fallback) go through their own admission (`scheduled_execution_admission.go::runScheduledTurnWithAdmission`, the steer launcher), and this document does not prove that no two turns of one session can overlap across all of them (not verified). Different sessions run in parallel.

| Producer or worker | Owner | Queue and size | Admission gate | Cancellation source | On shutdown/reload |
|---|---|---|---|---|---|
| Message bus (inbound, outbound, outbound media, session) | `pkg/bus/bus.go::NewMessageBus` | 64 each; a full queue blocks the publisher | none | context or done | shutdown only |
| Session worker | `pkg/agent/session_worker.go` | inbox 8 plus overflow; at most 200 waiting, then a busy reply | `AdmissionController.TryAdmit` | Stop, deletion, 60 s idle exit | stopped on shutdown (5 s); a reload does not end a turn |
| Non-main task dispatch | `pkg/agent/dispatch_sema.go::DispatchSemaphore` | none | non-blocking `TryAcquire`, else `ErrDispatchCapReached` | task cancel | shutdown |
| Steered or delegated turns | `steerAdmission` | FIFO, never refused | same cap plus memory cap | its own registered cancellation (the turn runs on a background context, so cancelling a parent turn does not reach it); an explicit tree Stop reaches descendants | shutdown |
| Connector workers | `pkg/channels/manager.go` | text 16, media 16 per channel | rate limiter | `StopAll` | manager retained across reload; worker and dispatcher generations can be stopped and restarted on reload (`restartExisting`) |
| WebSocket tab | `pkg/gateway/websocket.go` | read loop plus 3 goroutines; forwarder buffer 256 | lagging tab closed with 4008 | disconnect | shutdown |
| Cron services (three copies, 8 runs each) | main, task trigger, `/loop` | lane semaphore | lane limit | stop | shutdown; reload re-applies settings |
| TaskDrain, MailWatch | `pkg/heartbeat` | 1-minute poll | one cycle at a time | `Stop` waits, never cancels mid-cycle | stopped and rebuilt on reload |
| Plan engine | agent package | 30 s tick | none | `Stop` drains up to 30 s | shutdown only |
| Browser pool | `pool.go` | none | memory gate | idle sweep, eviction, `Close`, `Shutdown` | a Chrome process ends on idle retirement, eviction, explicit close or shutdown; profiles persist |
| MCP children | `loop_mcp.go` | none | 15 s connect | `ReconcileMCP`, `Close` | closed on shutdown; healed after reload |

**Admission.** The cap on active sessions comes from `OMNIPUS_MAX_PARALLEL_AGENTS`, then `MaxParallelAgents`, then a backstop of 2000 used only when neither is set (`pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents`). An explicit value is honored, with a warning when it is above 2000. A memory check can only lower a larger cap, to 2; it never raises a smaller configured cap (`pkg/agent/admission.go::applyMemoryCap`). A refused new session gets "I'm at capacity right now". A message for a session already in a turn is steered into that turn. Waits for tool approval last 600 seconds by default and deny on timeout.

**Shutdown** (`pkg/gateway/shutdown.go::runGracefulShutdown`, 70 s budget): stop channels and HTTP; stop background writers; stop the loop and wait for active turns, then force-cancel; `Close` (MCP, memory, sessions); stop media, browser transport, health, providers; wipe key material; remove the PID file.

**Reload** (`gateway.go::stopAndCleanupServices(isReload=true)`) stops only the two pollers and the media store. It leaves channels, cron, plan engine, task trigger, `/loop`, knowledge and boot writers running, so a settings save never ends a running turn.

# Business Requirements Document — Appendix B

## Omnipus Feature Parity & Enhancement Requirements

**Version:** 1.0 DRAFT  
**Date:** March 26, 2026  
**Parent Document:** Omnipus BRD v1.0  
**Related:** Appendix A (Windows Kernel-Level Security)  
**Status:** For Review

-----

## B.1 Purpose

This appendix extends the Omnipus BRD to define requirements for achieving and exceeding feature parity with OpenClaw. The main BRD (v1.0) focuses on security, governance, and policy enforcement. Appendix A covers Windows kernel-level security. This appendix covers the remaining functional gaps identified through competitive analysis: agent capabilities, channel integrations, skill ecosystem, and operational tooling.

These requirements are derived from a side-by-side comparison of OpenClaw (v2026.3.22, ~250K GitHub stars, 22+ channels, 13K+ ClawHub skills) and Omnipus (v0.2.3, ~25K stars, ~10 channels, no skill registry). The analysis identified 27 feature gaps. This appendix specifies each gap, its priority, implementation approach, and integration with the main BRD's security architecture.

**Design decision:** Omnipus is built on Omnipus (Go), not OpenClaw (Node.js). The rationale: (1) the main BRD's security architecture (Landlock, seccomp, Job Objects) is designed for Go and `golang.org/x/sys`; (2) Omnipus meets the BRD's core constraints (single binary, <10MB RAM overhead, zero runtime dependencies, $10 hardware); (3) OpenClaw's Node.js runtime violates all four constraints. Features will be built on Omnipus's Go codebase, not ported from OpenClaw's TypeScript.

-----

## B.2 Existing Omnipus Capabilities (Confirmed)

Before specifying gaps, the following capabilities are confirmed present in Omnipus v0.2.3 and do NOT require implementation:

| Capability | Omnipus Version | Notes |
|---|---|---|
| Heartbeat / proactive agent | v0.1.x+ | HEARTBEAT.md + configurable interval + spawn for async tasks. Responds HEARTBEAT_OK when nothing needs attention. |
| Headless server mode | v0.1.x+ | `omnipus gateway` is the native headless mode. Runs as systemd service on VPS, Raspberry Pi, Docker. |
| Cron / scheduled tasks | v0.1.x+ | One-time reminders, recurring tasks, cron expressions. Security gating added in v0.2.3. |
| MCP protocol support | v0.2.1 | Native Model Context Protocol integration via stdio, SSE, HTTP transports. |
| Smart model routing | v0.2.1 | Rule-based routing — simple queries to lightweight models, complex to frontier models. |
| Vision / multimodal | v0.2.1 | Image and file input with automatic base64 encoding for multimodal LLMs. |
| JSONL memory store | v0.2.1 | Persistent memory across sessions. |
| Sub-agent spawning | v0.2.1+ | Async spawn with spawn_status tracking (v0.2.3). |
| Tool allow/deny config | v0.2.3 | Basic tool enable/disable configuration. |
| System tray UI | v0.2.3 | Windows and Linux. |
| Channels: Telegram, Discord, Slack, QQ, DingTalk, LINE, WeCom, Matrix, IRC | v0.2.1 | 10 channels operational. |
| Web search | v0.1.x+ | Brave, DuckDuckGo, Exa providers. |
| WhatsApp (bridge) | Config exists | Via external bridge at `ws://localhost:3001`. Not native — external dependency required. |
| Feishu / Lark | Config exists | Present in config but disabled/incomplete. |
| Gateway hot-reload | v0.2.3 | Experimental. |

-----

## B.3 Requirements — P0: Competitive Foundation

These features must ship before Omnipus can credibly compete with OpenClaw. Without them, the product has critical capability gaps that no amount of security hardening can compensate for.

### B.3.1 ClawHub Skill Compatibility

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-12a | ClawHub skill protocol implementation | P0 | Moderate | Implement the ClawHub registry protocol in Go: search skills by keyword/tag, retrieve skill metadata (name, description, author, version, hash), download skill packages. The protocol communicates with ClawHub's REST API. Omnipus consumes the existing 13,729+ skill ecosystem — it does not build a competing registry. |
| FUNC-12b | SKILL.md parser and loader | P0 | Easy | Parse OpenClaw's SKILL.md format — a Markdown file containing metadata frontmatter and instruction text. Skills are directories containing a SKILL.md plus optional supporting files. Omnipus already has a basic skills system; this extends it to be format-compatible with ClawHub skills. |
| FUNC-12c | Skill hash verification on install | P0 | Easy | When installing a skill from ClawHub, verify its SHA-256 hash against the registry manifest before loading. Integrates with SEC-09 (Skill trust verification) from the main BRD. Unverified skills trigger a configurable warning or block. |
| FUNC-12d | Skill install/update/remove CLI | P0 | Easy | CLI commands: `omnipus skill install <name>`, `omnipus skill update <name>`, `omnipus skill remove <name>`, `omnipus skill search <query>`, `omnipus skill list`. Skills install to `~/.omnipus/workspace/skills/<name>/`. |
| FUNC-12e | ClawHub compatibility testing | P0 | Moderate | Automated test suite that installs the top 50 most popular ClawHub skills, verifies they load correctly, and validates tool registration. Run as part of CI on every release. Document any incompatibilities. |

**Integration with main BRD:** Skills from ClawHub are subject to SEC-04 (tool allow/deny), SEC-07 (deny-by-default), SEC-09 (trust verification), and SEC-15 (audit logging). A skill cannot grant itself permissions — the operator's policy governs what a skill can access.

**Risk:** ClawHub's API may change without notice. OpenClaw's skill format is not formally versioned. Mitigation: Pin to a known API version, implement format detection with fallback parsing, monitor ClawHub releases.

### B.3.2 Browser Automation

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-19 | CDP browser control (managed mode) | P0 | High | Implement Chrome DevTools Protocol browser automation using `chromedp` (Go-native CDP library). Managed mode: Omnipus launches and manages a dedicated Chromium/Chrome instance with its own user data directory, isolated from the user's personal browser. Supports headless and headed operation. |
| FUNC-20 | Browser action primitives | P0 | Moderate | Agent-callable tool actions: `browser.navigate(url)`, `browser.click(selector)`, `browser.type(selector, text)`, `browser.screenshot()`, `browser.get_text(selector)`, `browser.wait(selector)`, `browser.evaluate(js)`. Actions return structured results the agent can reason about. |
| FUNC-21 | Remote CDP mode | P1 | Easy | Connect to an external Chromium instance via CDP URL (`ws://host:port`). Enables use with cloud browser services (Browserless, Lightpanda) or Docker-hosted Chromium. Configuration: `tools.browser.cdp_url`. |
| FUNC-22 | Browser resource limits | P1 | Easy | Configurable timeout per page load (default 30s), maximum concurrent tabs (default 5), maximum memory per browser profile. Prevents runaway browser sessions from consuming system resources. On constrained hardware, recommend remote CDP mode instead of local Chromium. |

**Implementation notes:**
- `chromedp` is a well-maintained Go library with 11K+ stars, direct CDP integration, no CGo requirement.
- Chromium is NOT bundled with Omnipus (would violate single-binary constraint). The user installs Chromium separately, or uses remote CDP.
- For resource-constrained devices (Pi Zero, RISC-V), local Chromium is impractical (100-300MB RAM). Remote CDP to a dedicated browser host is the recommended pattern.
- Browser tool actions are subject to SEC-04 (tool allow/deny), SEC-06 (per-method control), and SEC-15 (audit logging). An operator can allow `browser.navigate` but deny `browser.evaluate` to prevent arbitrary JS execution.

**Risk:** Chromium dependency conflicts with "zero dependencies" philosophy. Mitigation: Browser is an optional tool, not a core dependency. Omnipus works fully without it. The tool degrades gracefully if no Chromium is available.

### B.3.3 WhatsApp Channel

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-23 | WhatsApp channel | P0 | High | Compiled-in Go channel using `whatsmeow` library, eliminating the external WebSocket bridge dependency (`ws://localhost:3001`). Uses `modernc.org/sqlite` (pure Go) for session storage, avoiding CGo. Supports send/receive text messages, images, documents, and group messages. Session state persisted locally in `~/.omnipus/channels/whatsapp/session.db`. |
| FUNC-24 | WhatsApp QR pairing flow | P0 | Easy | On first connection, generate a QR code displayed in CLI/TUI/WebUI for the user to scan with their WhatsApp mobile app. Persist session credentials for automatic reconnect. Handle session expiry and re-pairing gracefully. |
| FUNC-25 | WhatsApp media handling | P1 | Moderate | Send and receive images, audio messages, documents, and location pins. Incoming media is stored in the workspace temp directory and passed to the agent as file references. Outgoing media is sent from workspace paths. Subject to filesystem policy (SEC-01). |

**Implementation notes:**
- `whatsmeow` is the most actively maintained Go library for WhatsApp Web multi-device protocol. MIT licensed, used by Matrix bridges and multiple production systems.
- WhatsApp's protocol is reverse-engineered, not officially supported. Risk of breaking changes when WhatsApp updates their protocol. This is a known ecosystem-wide risk shared by OpenClaw and all non-official integrations.
- WhatsApp requires a phone number with an active WhatsApp account. Business API is an alternative for enterprise use but requires Meta approval.
- `whatsmeow` depends on SQLite for session storage. Omnipus uses `modernc.org/sqlite` (pure Go SQLite driver) instead of `mattn/go-sqlite3` (CGo), preserving the "Pure Go, no CGo" constraint. The pure Go driver adds ~10-15MB to the binary but avoids CGo entirely. WhatsApp is compiled into the binary, not an external process.

### B.3.4 Multi-Agent Routing

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-10 | Channel-to-agent routing | P0 | Moderate | Route inbound messages from specific channels, accounts, users, or groups to specific agent instances. Configuration maps patterns to agent IDs. Example: all Telegram messages from user `@boss` route to the `work` agent; Discord messages route to the `personal` agent. Defined in config under `routing.rules[]`. |
| FUNC-11 | Per-agent isolated workspaces | P0 | Easy | Each agent gets its own workspace directory with independent MEMORY.md, sessions, skills, cron jobs, and HEARTBEAT.md. Agents cannot access each other's workspaces. Omnipus partially implements this; Omnipus formalizes it with config-level support and filesystem enforcement via SEC-01 (Landlock). Already specified as FUNC-11 in main BRD — this formalizes the implementation. |
| FUNC-10a | Agent selection in gateway | P0 | Easy | The gateway supports multiple simultaneous agents, each with its own model config, tools, and permissions. The `agents.list[]` config array defines agents. Default agent handles unrouted messages. Matches main BRD FUNC-10. |

### B.3.5 Core Agent Experience

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| SEC-08 | Exec approval flow (WebUI extension) | P0 | Easy | Before executing a command via the exec tool, Omnipus presents an Allow/Deny prompt. Supports "Always Allow" to add the command pattern to a persistent allowlist. Works in CLI, TUI, and WebUI. Can be disabled for headless/automated deployments via `tools.exec.approval: "off"`. Matches main BRD SEC-08. Extended here to include WebUI surface (not just CLI/TUI). |
| FUNC-26 | Streaming response output | P0 | Easy | Token-by-token streaming from LLM providers to all output surfaces (WebUI, CLI, channels that support streaming). Uses Server-Sent Events (SSE) for WebUI, chunked messages for messaging channels. Prerequisite for responsive chat UX. |
| SEC-27 | Security diagnostics (`omnipus doctor`, extended) | P0 | Easy | CLI command that scans configuration and runtime environment: exposed endpoints without auth, overly permissive tool policies, plain-text credentials, missing kernel sandbox support, disabled rate limits, NTFS detection (Windows), Landlock availability (Linux). Outputs risk score (0-100) and actionable recommendations. Matches main BRD SEC-27. Extended here to include platform-specific checks. |

-----

## B.4 Requirements — P1: Enterprise Positioning

These features are needed for Omnipus to be credible in enterprise environments. They build on the P0 foundation.

### B.4.1 Security Enhancements

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| SEC-20/21 | Device pairing and gateway auth (consolidated) | P1 | Moderate | New devices connecting to the gateway must complete a pairing flow: short-lived code displayed in CLI, entered on the new device. Paired devices stored in a trust list with scoped permissions (read-only, operator, admin). Token-based auth on all gateway HTTP/WebSocket endpoints. Consolidates main BRD SEC-20 and SEC-21. |
| SEC-30 | DM policy safety checks | P1 | Easy | Detect risky direct message channel configurations — e.g., Telegram bot accepting messages from anyone, Discord bot in a public server without `allow_from` restrictions. Surface warnings in `omnipus doctor` output and in the WebUI security panel. |

### B.4.2 Enterprise Channel Integrations

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-04 | Signal channel (detailed) | P1 | Moderate | Integration via Signal CLI or signal-cli-rest-api bridge. Supports send/receive text, images, and group messages. Signal's end-to-end encryption makes it attractive for security-conscious enterprise users. Requires a dedicated phone number. Configuration under `channels.signal`. Matches main BRD FUNC-04. |
| FUNC-05 | Microsoft Teams channel (detailed) | P1 | Moderate | Compiled-in Go channel using `msbotbuilder-go` (community Go port of Bot Framework). Receives and sends messages in Teams channels and DMs. Requires Azure Bot registration and Azure AD app. Configuration under `channels.teams` with `app_id`, `app_password`, and `tenant_id`. Supports text, adaptive cards, and file attachments. Matches main BRD FUNC-05. |
| FUNC-06 | Google Chat channel (detailed) | P1 | Moderate | Google Workspace API integration for Google Chat spaces and DMs. Requires Google Cloud project and service account with Chat API enabled. Configuration under `channels.google_chat`. Supports text, cards, and thread replies. Matches main BRD FUNC-06. |

### B.4.3 Agent Capability Enhancements

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-13 | Skill auto-discovery (detailed) | P1 | Moderate | At runtime, scan installed skills directories and connected MCP servers to automatically discover and register tool definitions. Eliminates manual tool registration in config. New tools discovered via MCP are subject to policy (SEC-04, SEC-07) — discovery does not imply permission. Matches main BRD FUNC-13. |
| FUNC-27 | Sub-agent supervisor/worker patterns | P1 | Moderate | Extend Omnipus's existing async spawn with structured orchestration patterns. Supervisor agent can: assign tasks to worker sub-agents, track their status, aggregate results, handle worker failures with retry/fallback. Workers inherit the parent's security policy but can have further restrictions. Communication via internal message passing (not filesystem). |
| FUNC-28 | Canvas / visual workspace | P1 | High | Agent-driven interactive HTML rendering surface. The agent can generate and push HTML/CSS/JS content to a canvas panel in the WebUI, macOS app, or companion app. Use cases: dashboards, data visualizations, reports, interactive forms. Simpler than OpenClaw's A2UI — Omnipus uses standard HTML served via the gateway, pushed over WebSocket. No custom component library required initially. |
| FUNC-29 | Intermediate tool output streaming | P1 | Moderate | During multi-step tool execution (builds, searches, file operations), stream partial results to the UI in real time. Collapsible output blocks in the chat showing: tool name, current status, intermediate output lines. Prevents the "stuck" feeling both OpenClaw and Omnipus users report. Requires WebSocket event for tool progress (`tool.progress`). |
| FUNC-30 | iMessage / BlueBubbles channel | P1 | Moderate | Integration via BlueBubbles REST API for iMessage on macOS. Supports send/receive text, images, group messages. Requires BlueBubbles server running on a Mac. Configuration under `channels.imessage`. High-value for Apple ecosystem enterprise users. |

-----

## B.5 Requirements — P2: Differentiation & Ecosystem

These features differentiate Omnipus from competitors and build ecosystem completeness.

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-31 | Voice Wake / Talk Mode | P2 | High | Wake word detection ("Hey Omnipus" or configurable) + continuous voice conversation with TTS. Speech-to-text via Whisper (Groq free tier or local). Text-to-speech via ElevenLabs or system TTS. Platform-specific: macOS (CoreAudio), Linux (PulseAudio/ALSA), Windows (WASAPI). Configurable under `voice`. |
| FUNC-17 | Tailscale integration (detailed) | P2 | Easy | Built-in Tailscale Serve/Funnel support for secure remote access to the gateway without port forwarding or VPN setup. Uses Tailscale Go SDK (`tailscale.com/tsnet`). Configuration: `gateway.tailscale.mode: "serve"` or `"funnel"`. Funnel requires password auth. Matches main BRD FUNC-17. |
| FUNC-16 | Backup and restore (detailed) | P1 | Easy | `omnipus backup create` archives workspace, config, credentials (encrypted), and policy files into a single tarball. `omnipus backup restore` recovers from archive. Supports `--encrypt` flag for AES-256 encryption of the archive. Neither OpenClaw nor Omnipus has this — opportunity to lead. Matches main BRD FUNC-16. |
| FUNC-32 | macOS menu bar app | P2 | Moderate | Native macOS companion app for gateway lifecycle management (start/stop/restart), status indicator (connected/disconnected/error), quick actions (open WebUI, view logs, run doctor). Built with Go + systray library or native Swift. Lives in the macOS menu bar. |
| FUNC-33 | iOS/Android node companion | P2 | High | Turn mobile devices into agent peripherals. Capabilities: camera snap/video, GPS location, push notifications, canvas rendering. Connects to gateway via WebSocket with device pairing (SEC-20/21). All node invocations are logged (SEC-15) and subject to RBAC (SEC-19). **Enterprise consideration:** Remotely triggering phone cameras and SMS raises compliance concerns. Requires explicit per-capability consent, audit trail, and operator-level approval policy. |

-----

## B.6 Requirements — P3: Channel Expansion

These channels close the remaining gap with OpenClaw's 22+ channel count. All are compiled-in Go channels, consistent with the hybrid architecture where Go channels are part of the single binary.

| ID | Requirement | Priority | Effort | Details |
|---|---|---|---|---|
| FUNC-08 | Mattermost channel (detailed) | P3 | Easy | REST API + WebSocket integration. Self-hosted enterprise messaging. Well-documented API. Configuration under `channels.mattermost` with `url`, `token`, `team_id`. |
| FUNC-07 | Nostr channel (detailed) | P3 | Easy | Decentralized relay integration using `go-nostr` library. Lightweight protocol, good fit for edge/privacy use cases. Configuration under `channels.nostr` with `relay_urls[]`, `private_key`. Matches main BRD FUNC-07. |
| FUNC-09 | Twitch channel (detailed) | P3 | Easy | IRC-based chat integration. Reuses IRC channel adapter code with Twitch-specific auth (OAuth). Configuration under `channels.twitch` with `token`, `channel_name`. Matches main BRD FUNC-09. |
| FUNC-34 | Zalo channel | P3 | Easy | Zalo API integration for Vietnamese market. REST API. Configuration under `channels.zalo` with `app_id`, `secret_key`. |
| FUNC-35 | Feishu / Lark (full implementation) | P3 | Moderate | Omnipus has Feishu in config but it's disabled/incomplete. Complete the implementation: event subscription, message send/receive, rich text cards, group support. Configuration already exists under `channels.feishu`. |

-----

## B.7 Requirement ID Unification

As of this revision, all requirements across the BRD suite use a unified ID namespace. The former `FEAT-xx` IDs from this appendix have been merged into the main `SEC-xx` / `FUNC-xx` namespaces:

- **Direct mappings:** Where an appendix requirement matched an existing main BRD requirement, the main BRD ID is used. This appendix provides additional implementation detail marked with "(detailed)".
- **Extensions:** Where an appendix requirement extends an existing requirement, a suffixed ID is used (e.g., `FUNC-12a` extends `FUNC-12`).
- **New items:** Genuinely new requirements were assigned the next available `FUNC-xx` or `SEC-xx` number.

The `WIN-xx` namespace (Appendix A) remains separate as it covers platform-specific kernel security requirements that are inherently scoped to Windows.

-----

## B.8 Delivery Integration

These features integrate into the main BRD delivery phases. The phasing accounts for dependencies — e.g., skill loading (FUNC-12b) must precede ClawHub integration (FUNC-12a), streaming (FUNC-26) must precede intermediate tool output (FUNC-29).

| Phase | Main BRD Deliverables | Appendix B Additions |
|---|---|---|
| Phase 1 | Landlock, seccomp, core policy engine, audit logging | FUNC-12b/12d (SKILL.md parser, skill CLI), FUNC-10/11/10a (multi-agent routing), SEC-08 (exec approval — WebUI), FUNC-26 (streaming), SEC-27 (doctor — extended) |
| Phase 2 | RBAC, exec approvals, skill verification, governance | FUNC-12a/12c/12e (ClawHub compatibility), FUNC-19/20 (browser CDP), FUNC-23/24 (WhatsApp channel provider), SEC-20/21 (device pairing), SEC-30 (DM safety), FUNC-04/05/06 (Signal/Teams/Google Chat), FUNC-13 (skill auto-discovery), FUNC-27 (sub-agent patterns) |
| Phase 3 | Ecosystem, extensibility, policy hot-reload | FUNC-21/22 (remote CDP, browser limits), FUNC-25 (WhatsApp media), FUNC-28 (canvas), FUNC-29 (intermediate output), FUNC-30 (iMessage), FUNC-31 (voice), FUNC-17 (Tailscale), FUNC-16 (backup), FUNC-32 (macOS app), FUNC-33 (companion nodes), FUNC-07/08/09/34/35 (P3 channels) |

**Note:** Team composition, timeline, and effort estimates to be determined after prioritization and detailed specification. Phase 2 carries the heaviest load from this appendix (browser automation, WhatsApp, enterprise channels, ClawHub integration) and will need careful scoping.

-----

## B.9 Success Metrics (Additions)

| Metric | Target | Measurement |
|---|---|---|
| ClawHub skill install success rate | ≥95% of top 100 skills install and load correctly | Automated CI test suite |
| Browser automation response time | <2s for navigate + screenshot on localhost | Benchmark with managed Chromium |
| WhatsApp reconnect reliability | ≥99.5% automatic reconnect after network interruption | Soak test over 72 hours |
| Channel parity | 20+ channels total | Feature checklist |
| Multi-agent routing accuracy | 100% of routed messages reach correct agent | Integration test suite |
| Streaming latency (first token) | <100ms from LLM response to UI render | Benchmark with local Ollama |

-----

## B.10 Dependencies & Assumptions

**New dependencies (beyond main BRD):**

| Dependency | Used By | Risk |
|---|---|---|
| `chromedp` Go library | FUNC-19/20 (browser automation) | Well-maintained (11K+ stars), pure Go, no CGo. Low risk. |
| `whatsmeow` Go library | FUNC-23 (WhatsApp provider) | Active maintenance, used by Matrix bridges. Medium risk — WhatsApp protocol may change. |
| ClawHub REST API | FUNC-12a (skill compatibility) | No formal API stability guarantee. Medium risk — monitor for changes. |
| Signal CLI | FUNC-04 (Signal channel) | Java dependency for the bridge. Not compiled into Omnipus binary — runs as external process. |
| `msbotbuilder-go` (community Go port) | FUNC-05 (Teams channel) | Community-maintained Go port of Bot Framework SDK. Azure Bot Framework REST API is Microsoft-maintained. Requires Azure account. Low risk. |
| Google Workspace API | FUNC-06 (Google Chat channel) | Google-maintained. Requires GCP project. Low risk. |
| Tailscale Go SDK (`tsnet`) | FUNC-17 (Tailscale) | Well-maintained, pure Go. Low risk. |

**Assumptions:**

- ClawHub's skill format (SKILL.md + directory structure) remains stable through the implementation period. If OpenClaw's foundation makes breaking changes to the skill format, Omnipus may need to support multiple format versions.
- WhatsApp Web multi-device protocol continues to be accessible via third-party libraries. If Meta blocks reverse-engineered clients, WhatsApp integration falls back to WhatsApp Business API (requires Meta approval).
- Users on resource-constrained hardware will use remote CDP for browser automation rather than local Chromium. This is a deployment pattern, not a limitation.
- Team composition and timeline to be determined after prioritization and detailed specification.

-----

## B.11 Risks (Additions)

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| WhatsApp protocol changes break `whatsmeow` | WhatsApp channel goes offline until library is updated | Medium | Pin to known-stable library version. Monitor `whatsmeow` releases. Maintain fallback to bridge mode. |
| ClawHub API changes or becomes gated | Skill ecosystem access lost | Low | Cache skill metadata locally. Support self-hosted skill registries as alternative. |
| Browser automation RAM exceeds embedded device capability | Browser tool unusable on Pi/RISC-V | High (on constrained devices) | Default to remote CDP on devices with <512MB RAM. Document recommended deployment patterns. |
| Teams/Google Chat require paid enterprise licenses | Channel adoption blocked for small teams | Medium | Document free-tier limitations. These channels primarily target organizations that already have M365/Workspace. |
| Phase 2 scope overload | Delay in enterprise channel delivery | High | Prioritize WhatsApp + ClawHub first. Teams/Google Chat can slip to Phase 3 if needed. |
| Companion node (FUNC-33) compliance risk | Enterprise customers refuse due to remote camera/SMS access | Medium | Make companion node entirely optional. Default to all capabilities requiring per-invocation approval. Provide enterprise policy to disable node features globally. |

-----

## B.12 Decision Log

| Decision | Rationale |
|---|---|
| Use ClawHub compatibility instead of building own skill registry | OpenClaw's 13K+ skills represent years of community effort. Building a competing registry from zero would take 2-3 years to reach comparable scale. Implementing the ClawHub protocol is weeks of work and immediately unlocks the entire ecosystem. |
| Use `chromedp` for browser automation | Pure Go, no CGo, 11K+ stars, active maintenance. The Go ecosystem's most mature CDP library. Alternatives (`rod`, `playwright-go`) were considered but `chromedp` has the best combination of maturity and API stability. |
| Use `whatsmeow` + `modernc.org/sqlite` for WhatsApp | Most actively maintained Go library for WhatsApp Web multi-device protocol. Used by production Matrix bridges. Compiled into the binary using `modernc.org/sqlite` (pure Go) to avoid CGo. Adds ~10-15MB to binary size. Alternative: WhatsApp Business API, but requires Meta approval and doesn't support personal accounts. |
| Chromium is not bundled | Would add 100-300MB to the binary/deployment, violating the lightweight constraint. Browser is an optional tool. Remote CDP provides full capability without local Chromium. |
| Elevate WhatsApp to P0 from P3 | WhatsApp is the #1 messaging platform globally (2B+ users) and the most popular channel for OpenClaw users. A bridge-only solution is not competitive. First-class provider support is table stakes for market credibility. |
| Hybrid channel architecture (compiled-in Go + bridge for non-Go) | Go channels are compiled into the binary, inheriting Omnipus's proven architecture (both Omnipus and OpenClaw keep channels in-process). This preserves single-binary deployment, minimizes RAM overhead on $10 hardware, and avoids the complexity of IPC for 90%+ of channels. Non-Go channels (Signal/Java, Teams/Node.js) and community extensions use the bridge protocol as external processes. CGo avoided via `modernc.org/sqlite` instead of process isolation. |
| P3 channels (Mattermost, Nostr, Twitch, Zalo, Feishu) are last priority | Each serves a niche audience. Core enterprise channels (Teams, Google Chat, Signal) and mass-market channels (WhatsApp, Telegram, Discord) cover 90%+ of the target market. Niche channels can be added incrementally without blocking adoption. |
| Canvas uses standard HTML, not a custom component library | OpenClaw's A2UI is a custom component system with learning curve and maintenance overhead. Standard HTML/CSS/JS pushed via WebSocket is simpler, more flexible, and leverages existing web skills. Can evolve toward structured components later if needed. |

-----

*End of Appendix B*

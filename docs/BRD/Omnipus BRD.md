# Business Requirements Document

## Omnipus — Enterprise Security, Governance & Feature Enhancement for Omnipus

**Version:** 1.0 DRAFT  
**Date:** March 19, 2026  
**Status:** For Review  
**Author:** [Your Name]

-----

## 1. Executive Summary

Omnipus is the lightest-weight open-source AI agent runtime available — a single Go binary under 20MB that boots in under one second and runs on hardware as cheap as $10. In five weeks it has reached 25K GitHub stars and proven its core value proposition: an AI agent that deploys anywhere, with zero dependencies.

However, Omnipus currently lacks the security posture, governance controls, and operational features required for enterprise, regulated, and multi-tenant environments. Competitors like NemoClaw (NVIDIA) have demonstrated that the market demands kernel-level sandboxing, audit trails, role-based access, and fine-grained tool permissions before organizations will trust an autonomous agent with real work.

This BRD defines the requirements for **Omnipus** — an agentic core built on Omnipus’s foundation, delivering enterprise-grade security, governance, and a polished user experience.

Omnipus is built around a common **agentic core**: a Go binary containing the security subsystem, policy engine, channel integrations, agent runtime, and data model. The UI is a shared `@omnipus/ui` React component library. The core supports multiple deployment modes: open source (single binary with embedded web UI), desktop (Electron wrapper), and hosted (cloud/SaaS).

**Key differentiator:** Unlike NemoClaw, which requires Docker, K3s, 8GB+ RAM, and a 2.4GB sandbox image, Omnipus delivers comparable security and governance features natively in Go — no containers, no runtime dependencies, no heavyweight infrastructure. This makes Omnipus the first AI agent runtime that is simultaneously ultra-lightweight and enterprise-hardened.

**Estimated scope:** 95+ requirements across security, platform, feature parity, UI, system agent, and data model domains, split into three delivery phases. Team composition and timeline to be determined after prioritization and detailed specification.

-----

## 2. Problem Statement

### 2.1 Security Gaps

Omnipus’s current security model is workspace-level only. While it blocks dangerous commands (rm -rf, fork bombs, disk writes) and restricts file operations to the workspace directory, it has significant shortcomings:

- **No kernel-level enforcement.** Workspace restrictions are application-level checks that child processes spawned by build tools or shell commands can bypass. There is no OS-enforced isolation via Landlock, seccomp, or process namespaces.
- **No tool-level permissions.** All tools are available to all agents. There is no allow/deny list, no per-binary execution control, no per-method API restriction, and no deny-by-default policy model.
- **No audit trail.** There is no structured log of what actions an agent took, which tools it invoked, which LLM calls it made, which files it modified, or why a particular action was allowed or blocked.
- **No role-based access.** Omnipus has no concept of operator roles, permission tiers, or differentiated access between administrators, operators, and agents.
- **No credential management.** API keys and tokens sit in plain-text JSON config files with no encryption at rest, no secure injection mechanism, and no log redaction.
- **No policy engine.** There is no declarative, structured way to define and enforce security policies across agents, tools, and resources. No hot-reload capability for runtime policy changes.

### 2.2 Functional Gaps

While Omnipus is surprisingly feature-complete for a five-week-old project (55+ features already implemented), it has notable gaps versus OpenClaw:

- **Missing channel integrations.** Signal, Microsoft Teams, Google Chat, Nostr, Mattermost, Twitch, and Zalo are not supported (~8 channels behind OpenClaw’s 22+).
- **No skill marketplace.** OpenClaw’s ClawHub has 13,729 community-built skills. Omnipus has no equivalent registry, discovery mechanism, or trust verification system.
- **Limited multi-agent orchestration.** Omnipus can spawn subagents asynchronously but lacks full orchestration patterns (supervisor/worker delegation, channel-to-agent routing).
- **No operational tooling.** No diagnostic command (like OpenClaw’s `doctor`), no backup/restore, no Tailscale integration for remote access.
- **No plugin architecture.** No way to bundle and distribute provider integrations or third-party extensions as installable packages.

### 2.3 Market Context

The AI agent market is moving rapidly from experimental personal assistants toward enterprise deployment. NVIDIA’s launch of NemoClaw at GTC 2026 (March 16) with 17 enterprise partners (Salesforce, Cisco, Google, Adobe, CrowdStrike, SAP, ServiceNow) signals that security and governance are now table stakes. OpenClaw’s well-documented security incidents — CVE-2026-25253 (CVSS 8.8), the ClawHavoc malware campaign against ClawHub skills, and Snyk’s sandbox bypass research — have further accelerated demand for hardened agent runtimes.

Omnipus has a unique window of opportunity: it can deliver NemoClaw-grade security without NemoClaw’s infrastructure overhead, targeting edge, IoT, hybrid cloud, and resource-constrained enterprise environments that NemoClaw cannot serve.

-----

## 3. Project Objectives

1. **Build the agentic core** — a Go binary containing security, policy, channels, agent runtime, and data model that serves as the foundation for all deployment modes.
2. **Harden Omnipus’s security posture** to be comparable to NemoClaw’s OpenShell runtime, using native Go and Linux kernel features instead of Docker/K3s containers.
3. **Implement a declarative policy engine** that gives operators fine-grained control over what agents can access, execute, and invoke — with deny-by-default semantics.
4. **Add enterprise governance capabilities** including structured audit logging, role-based access control, credential management, and security diagnostics.
5. **Close functional gaps** in channel integrations, multi-agent orchestration, extensibility, and operational tooling.
6. **Deliver a polished UI** — a noticeably better experience than Omnipus or OpenClaw, with inline tool output, streaming, agent management, and a system agent for conversational configuration.
7. **Preserve Omnipus’s core identity**: single binary, zero dependencies, sub-second startup, minimal RAM, runs on $10 hardware.
8. **Design for multiple deployment modes** — architecture decisions must support open source (single binary), desktop (Electron), and hosted (cloud/SaaS) without fundamental changes.

-----

## 4. Constraints & Design Principles

|Principle                                            |Description                                                                                                                                                                 |
|-----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|**Single binary (agentic core)**                     |The Go agentic core compiles into a single binary. No runtime dependencies (no Docker, no Node.js, no Python). The Desktop variant wraps this binary in Electron. The open source variant embeds the web UI via `go:embed`. The SaaS variant may decompose the core into services later — but the core itself remains a single compilable unit.|
|**Minimal footprint**                                |Total RAM overhead for all new security features should not exceed 5-10MB beyond current baseline.                                                                          |
|**Graceful degradation**                             |Features requiring Linux 5.13+ kernel (Landlock, seccomp) must fall back cleanly to application-level enforcement on older kernels, non-Linux platforms, and Android/Termux.|
|**Ecosystem compatibility**                          |Omnipus follows Omnipus/OpenClaw conventions where applicable: SKILL.md format (ClawHub compatible), HEARTBEAT.md, SOUL.md, AGENTS.md, JSON config structure patterns. This maximizes compatibility with the existing skill ecosystem and community knowledge. Omnipus is not a drop-in Omnipus replacement — it has its own config format but adopts the same concepts and file conventions.|
|**Deny-by-default for security, opt-in for features**|Security policies default to most restrictive. New functional features default to disabled until explicitly enabled.                                                        |
|**All features implemented in Go**                   |No CGo, no external C libraries, no shelling out for security-critical paths. Pure Go using `golang.org/x/sys/unix` for kernel interfaces.                                  |
|**Channel provider model (hybrid)**                  |Go channels are compiled into the binary (inheriting Omnipus's architecture) and communicate via an internal MessageBus. Non-Go channels (Signal/Java, Teams/Node.js) and community channels run as external processes using a bridge protocol (JSON over stdin/stdout). All channels implement the same `ChannelProvider` interface. WhatsApp uses `modernc.org/sqlite` (pure Go) to avoid CGo. This preserves the single-binary deployment for 90%+ of users while supporting non-Go and community extensions.|

-----

## 5. Requirements

### 5.1 SECURITY — Kernel-Level Sandboxing

|ID    |Requirement                      |Priority|Effort  |Details                                                                                                                                                                                                                                                                       |
|------|---------------------------------|--------|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-01|Landlock filesystem sandboxing   |P0      |Moderate|Use Linux Landlock LSM (kernel 5.13+) to restrict agent processes to allowed filesystem paths at the kernel level. Supports ABI v1-v3. Inherits to child processes automatically. Falls back to existing workspace-level checks on unsupported platforms.                     |
|SEC-02|Seccomp syscall filtering        |P0      |Moderate|Apply seccomp-BPF filters to agent tool execution processes to block privilege escalation, raw socket creation, module loading, and other dangerous syscalls. Implemented via `golang.org/x/sys/unix` BPF program assembly. Linux-only with graceful no-op on other platforms.|
|SEC-03|Child process sandbox inheritance|P1      |Moderate|Ensure that when agents spawn child processes (via exec tool, build tools, or scripts), those children inherit Landlock and seccomp restrictions. Landlock provides this natively; seccomp requires `SECCOMP_FILTER_FLAG_TSYNC`.                                              |

### 5.2 SECURITY — Tool & Skill Permissions

|ID    |Requirement                    |Priority|Effort  |Details                                                                                                                                                                                                                                                      |
|------|-------------------------------|--------|--------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-04|Tool allow/deny lists per agent|P0      |Easy    |Each agent definition in config can specify `tools.allow` and `tools.deny` arrays. If `allow` is set, only listed tools are available. If `deny` is set, listed tools are blocked. Applied at agent initialization.                                          |
|SEC-05|Per-binary execution control   |P0      |Easy    |The exec tool checks an allowlist of permitted binaries/commands before execution. Commands not on the allowlist are blocked and logged. Supports glob patterns (e.g., `git *`, `npm *`).                                                                    |
|SEC-06|Per-method/API-call control    |P1      |Moderate|Tool invocations can be restricted at the method level — e.g., allow `web_search` but deny `web_fetch`, or allow `file.read` but deny `file.write`. Requires a tool invocation interceptor layer in the agent loop.                                          |
|SEC-07|Deny-by-default policy model   |P0      |Easy    |New config flag `security.default_policy: "deny"`. When enabled, agents start with zero tool permissions and only get what is explicitly listed in `tools.allow`. Default remains `"allow"` for backward compatibility.                                      |
|SEC-08|Exec approval prompt            |P1      |Easy    |Before executing a command, Omnipus presents an interactive Allow/Deny prompt. Supports “Always Allow” to add the command pattern to a persistent allowlist. Works in CLI, TUI, and WebUI. Can be disabled for headless/automated deployments via `tools.exec.approval: “off”`.|
|SEC-09|Skill trust verification       |P1      |Moderate|Skills installed from external sources are verified via SHA-256 hash against the registry manifest before loading. Unsigned/unverified skills trigger a warning or are blocked depending on policy. Phase 1: SHA-256 hash verification only (proves integrity, not authorship). Future: ed25519 author signatures for provenance verification. The data model includes a `signature` field (initially null) to support this without schema changes.|
|SEC-10|Two-layer policy enforcement   |P2      |Moderate|Separate sandbox-level and agent-level tool filters, similar to OpenClaw’s architecture. The sandbox policy defines the maximum permission boundary; the agent policy further restricts within that boundary. Both must permit a tool for it to be available.|

### 5.3 SECURITY — Policy Engine

|ID    |Requirement                    |Priority|Effort  |Details                                                                                                                                                                                                                 |
|------|-------------------------------|--------|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-11|Declarative JSON policy files  |P0      |Easy    |Extend existing JSON config with a `security.policy` section. Policy files define filesystem paths, allowed tools, allowed binaries, and RBAC roles in a structured, version-controllable format.                       |
|SEC-12|Static policies (load-once)    |P0      |Easy    |Filesystem and process policies are loaded at startup and cannot be changed at runtime. This matches NemoClaw/OpenShell’s approach where security-critical boundaries are locked at sandbox creation.                   |
|SEC-13|Hot-reloadable policies        |P2      |Moderate|Non-security-critical policies (e.g., tool allowlists, model routing rules) can be updated at runtime via config file change + SIGHUP or API call, without restarting the agent. Uses file watcher with debounce.       |
|SEC-14|Policy change approval workflow|P2      |Moderate|When an agent requests access to a tool or resource outside its current policy, the request is queued for operator approval via CLI prompt, webhook, or notification. Approved changes are persisted to the policy file.|

### 5.4 SECURITY — Audit & Compliance

|ID    |Requirement                    |Priority|Effort  |Details                                                                                                                                                                                                                                                                                  |
|------|-------------------------------|--------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-15|Structured audit logging       |P0      |Easy    |All security-relevant events are logged in structured JSON format via Go’s `slog` package. Events include: tool invocations, exec commands, file operations, LLM calls, permission checks (allowed/denied), policy changes, and authentication events. Output to file, stdout, or syslog.|
|SEC-16|Log redaction (API keys, PII)  |P0      |Easy    |Sensitive patterns (API keys, tokens, passwords, email addresses) are automatically scrubbed from log output using configurable regex patterns. Redacted values replaced with `[REDACTED]`.                                                                                              |
|SEC-17|Explainable policy decisions   |P1      |Easy    |Every allow/deny decision includes the policy rule that matched, enabling operators to understand exactly why an action was permitted or blocked. Logged as part of the audit trail.                                                                                                     |
|SEC-18|Tamper-evident log chain (HMAC)|~~P2~~ Descoped v1.0|—|Each log entry includes an HMAC-SHA256 hash chained to the previous entry, creating a tamper-evident sequence. If any entry is modified or deleted, the chain breaks and is detectable via a verification command. **Descoped for v1.0** — not required for NemoClaw parity. Revisit for v1.1 if compliance (SOC2/ISO27001) mandates it.                                                                      |

### 5.5 SECURITY — RBAC & Access Control

|ID    |Requirement              |Priority|Effort  |Details                                                                                                                                                                                                        |
|------|-------------------------|--------|--------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-19|Role-based access control|P1      |Moderate|Define roles (e.g., `admin`, `operator`, `viewer`, `agent`) with configurable permission sets. Roles map to capabilities: which tools can be invoked, which config can be changed, which agents can be managed. RBAC also gates system agent operations — destructive system tools require `admin` role, management operations require `operator`, read-only operations require `viewer`. See Appendix D §D.5.4 for system agent RBAC mapping.|
|SEC-20|Gateway authentication   |P1      |Easy    |Token-based authentication on gateway HTTP endpoints. Requests without a valid token are rejected. Token is generated at setup and stored securely. Supports rotation.                                         |
|SEC-21|Device pairing           |P2      |Moderate|New devices connecting to the gateway must complete a pairing flow (short-lived code displayed in CLI, entered on the new device). Paired devices are stored in a trust list with scoped permissions.          |

### 5.6 SECURITY — Credential Management

|ID    |Requirement                         |Priority|Effort  |Details                                                                                                                                                                                                                  |
|------|------------------------------------|--------|--------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-22|Credential injection via environment|P0      |Easy    |API keys and tokens are injected into agent processes via environment variables or a named provider abstraction, never read directly from config files at runtime. Config files reference credential names, not values.  |
|SEC-23|Credential encryption at rest       |P1      |Moderate|API keys stored on disk are encrypted using AES-256-GCM. Key derivation and management specified below.|
|SEC-23a|Credential key derivation          |P1      |Moderate|Master key derived using **Argon2id** (time=3, memory=64MB, parallelism=4) from a user-provided passphrase. A unique 16-byte salt is generated on first encryption and stored alongside the encrypted credential store. The derived 256-bit key is used for all AES-256-GCM operations.|
|SEC-23b|Credential key provisioning        |P1      |Moderate|Three key provisioning modes, tried in order: (1) **Environment variable** `OMNIPUS_MASTER_KEY` — a hex-encoded 256-bit key, bypasses KDF. Primary mode for headless/gateway deployments. (2) **Key file** at path specified by `OMNIPUS_KEY_FILE` env var — file contains the hex-encoded key. Permissions must be `0600` or stricter; Omnipus refuses to start if world-readable. (3) **Interactive passphrase** — prompted on first launch via TTY. Derived key cached in OS keyring where available (macOS Keychain, GNOME Keyring, Windows Credential Manager). If no TTY and no env var/key file, Omnipus starts with credentials inaccessible and logs a warning; providers requiring credentials will fail until key is provided.|
|SEC-23c|Credential key rotation            |P1      |Easy    |`omnipus credentials rotate` command: prompts for current passphrase (or reads from env), prompts for new passphrase, re-derives key, re-encrypts all credentials in `credentials.json`, writes atomically. Old passphrase remains valid for zero seconds after rotation completes.|
|SEC-23d|Credential storage flow            |P1      |Easy    |When a credential is provided (e.g., via `system.provider.configure`): (1) the raw value is encrypted with the master key and written to `credentials.json`, (2) a `_ref` key (e.g., `api_key_ref: "ANTHROPIC_API_KEY"`) is written to `config.json`, (3) the raw value is never persisted in `config.json` or any other file. At runtime, credentials are resolved by name from the encrypted store and injected via environment variables (SEC-22). The bridge protocol references credentials by `_ref` name; the gateway injects actual values at connection time.|
|SEC-23e|Credential store file format        |P0      |Easy    |Credentials stored in `~/.omnipus/credentials.json` (not YAML). Format: `{"version": 1, "salt": "<base64>", "credentials": {"NAME": {"nonce": "<base64>", "ciphertext": "<base64>"}}}`. Consistent with the project's JSON-only data model. Plain-text storage of any credential triggers a security warning from `omnipus doctor`.|

### 5.7 SECURITY — Hardening

|ID    |Requirement                       |Priority|Effort  |Details                                                                                                                                                                                                                                                                      |
|------|----------------------------------|--------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|SEC-24|SSRF protection                   |P0      |Easy    |Block outbound HTTP requests to private/internal IP ranges (10.x, 172.16-31.x, 192.168.x, 169.254.x) and cloud metadata endpoints (169.254.169.254). Configurable allowlist for legitimate internal services.                                                                |
|SEC-25|Prompt injection defenses         |P1      |Easy    |Input sanitization layer that strips or escapes known prompt injection patterns from user input before it reaches the LLM. Content from web fetch, file read, and external sources is tagged as untrusted data in the system prompt. Configurable strictness levels.         |
|SEC-26|Rate limiting                    |P0      |Easy    |Sliding-window rate limiter at three scopes: (1) **Per-agent** — each agent has configurable limits for LLM calls/hour, tool calls/minute, and outbound messages/minute. Defaults apply; per-agent overrides on agent profile. (2) **Per-channel** — outbound message rate per platform to respect platform API limits (e.g., Telegram 30/min, WhatsApp 20/min). (3) **Global** — daily cost cap (USD) across all agents, derived from session stats persisted to disk (survives restarts). When any limit is hit, the operation is **rejected with a cooldown** — returns an error with `retry_after_seconds`. The agent loop treats this like any tool error. No silent queueing. Rate limit state for per-agent and per-channel is in-memory sliding window (resets on restart). The system agent (Appendix D) is exempt from rate limits but all operations are still audit-logged. UI surfaces: global cost/limits in Settings → Security & Policy; per-agent overrides on agent profile; real-time usage in session bar (cost indicator); limit-hit events in chat as system messages.|
|SEC-27|Security audit command            |P0      |Moderate|`omnipus security audit` / `omnipus doctor` CLI command that scans configuration and runtime environment: exposed endpoints without auth, overly permissive tool policies, plain-text credentials, missing kernel sandbox support, disabled rate limits, NTFS detection (Windows), Landlock availability (Linux). Outputs a risk score (0-100) and actionable recommendations.|
|SEC-28|Exec tool HTTP proxy enforcement  |P1      |Moderate|When the exec tool spawns a child process, Omnipus sets `HTTP_PROXY` and `HTTPS_PROXY` environment variables pointing to a lightweight local proxy (bound to loopback) that applies the same SSRF rules as SEC-24. This ensures child processes that honor proxy settings cannot reach private/internal IP ranges or cloud metadata endpoints. The proxy logs all outbound requests to the audit log (SEC-15). This is best-effort — processes that bypass proxy env vars or use raw sockets are not covered. The proxy is only active while exec tool processes are running.|
|SEC-29|Network egress warning in diagnostics|P0   |Easy    |`omnipus doctor` and `omnipus security audit` MUST warn when the exec tool is enabled without full network egress control. The warning states: "Exec tool is enabled. Child processes can make arbitrary outbound network connections. The HTTP proxy (SEC-28) provides partial coverage but processes using raw sockets or ignoring proxy settings are not restricted. For full network isolation, use external mechanisms (network namespaces, firewall rules, or container networking). See deployment guide."|
|SEC-30|DM policy safety checks             |P1      |Easy    |Detect risky direct message channel configurations — e.g., Telegram bot accepting messages from anyone, Discord bot in a public server without `allow_from` restrictions. Surface warnings in `omnipus doctor` output and in the WebUI security panel.|

### 5.7.1 SECURITY — Known Limitations

The following limitations are inherent to Omnipus's unprivileged execution model and apply across all platforms:

|ID    |Limitation                        |Scope   |Details                                                                                                                                                                                                                                                                      |
|------|----------------------------------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|LIM-01|No kernel-level network egress control|All platforms|Landlock (Linux) restricts filesystem access but not network access. seccomp can block raw socket creation (`socket` syscall) but cannot restrict which IP addresses or ports a permitted socket connects to. Windows Job Objects do not restrict network access. Full network egress control requires privileged mechanisms: Linux network namespaces (`CLONE_NEWNET`, requires `CAP_SYS_ADMIN` or user namespaces), eBPF socket filters (requires `CAP_BPF`), or iptables/nftables (requires root). Windows requires WFP filters (requires Administrator). None of these are available to unprivileged processes.|
|LIM-02|HTTP proxy is best-effort         |Exec tool|SEC-28's proxy enforcement depends on child processes honoring `HTTP_PROXY`/`HTTPS_PROXY` environment variables. Processes using raw sockets, hardcoded connections, or languages/runtimes that ignore proxy settings bypass this control. This is a well-understood limitation shared by all unprivileged sandboxing approaches.|
|LIM-03|Exfiltration via DNS              |All platforms|Even with network restrictions, data can be exfiltrated via DNS queries (encoding data in subdomain labels). Mitigating DNS exfiltration requires a controlled DNS resolver, which is outside Omnipus's scope.|

**Deployment guidance for security-critical environments:** Organizations requiring full network isolation should deploy Omnipus inside a container (Docker, Podman) or VM with restricted network rules, or use Linux network namespaces with a controlled bridge. A deployment guide for these configurations will be provided as documentation alongside the binary.

### 5.8 FUNCTIONAL — Inference Routing

|ID     |Requirement                                  |Priority|Effort  |Details                                                                                                                                                                                                                                                                                   |
|-------|---------------------------------------------|--------|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-01|Privacy-aware inference routing              |P2      |Moderate|Route LLM requests to different providers based on data sensitivity classification. Messages containing sensitive patterns (PII, financial data, internal identifiers) are routed to local models (Ollama/Nemotron); non-sensitive requests can go to cloud providers. Configurable rules.|
|FUNC-02|Inference interception (credential stripping)|P2      |Moderate|A proxy layer between the agent and LLM providers that strips agent-side credentials and injects provider-specific auth. The agent never directly handles LLM API keys. Transparent to the agent’s tool invocation flow.                                                                  |
|FUNC-03|Cost-based model routing                     |P2      |Moderate|Extend existing smart routing to include cost awareness. Simple classification queries go to cheap/fast models; complex reasoning goes to frontier models. Configurable thresholds and model tiers.                                                                                       |

### 5.9 FUNCTIONAL — Channel Integrations

|ID     |Requirement               |Priority|Effort  |Details                                                                                                                             |
|-------|--------------------------|--------|--------|------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-04|Signal channel integration|P1      |Moderate|External channel provider using the bridge protocol (non-Go). Implementation via Signal CLI or signal-cli-rest-api (Java). Runs as a managed child process. Supports send/receive text, images, and group messages.|
|FUNC-05|Microsoft Teams channel   |P1      |Moderate|Compiled-in Go channel. Bot Framework integration via `msbotbuilder-go` (community Go port) for Teams channels and direct messages. Requires Azure Bot registration. Configuration under `channels.teams` with `app_id`, `app_password`, and `tenant_id`. Supports text, adaptive cards, and file attachments. Matches main BRD FUNC-05.|
|FUNC-06|Google Chat channel       |P1      |Moderate|Compiled-in Go channel. Google Workspace API integration for Google Chat spaces and DMs. Requires Google Cloud project and service account.|
|FUNC-07|Nostr channel             |P3      |Easy    |Compiled-in Go channel. Nostr relay integration using `go-nostr` library. Lightweight protocol, good fit for edge use cases.|
|FUNC-08|Mattermost channel        |P3      |Easy    |Compiled-in Go channel. REST API and WebSocket integration. Straightforward, well-documented API.|
|FUNC-09|Twitch channel            |P3      |Easy    |Compiled-in Go channel. IRC-based chat integration. Minimal complexity, reuses existing IRC channel adapter code.|

Go channels are compiled into the binary (single process, zero IPC overhead, inheriting Omnipus's architecture). Non-Go channels (Signal/Java) use the bridge protocol as external processes. Community channels use the bridge protocol with the Omnipus Channel SDK. All implement the same `ChannelProvider` interface. See Appendix E §E.10.

### 5.10 FUNCTIONAL — Multi-Agent & Orchestration

|ID     |Requirement                        |Priority|Effort  |Details                                                                                                                                                                                                                     |
|-------|-----------------------------------|--------|--------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-10|Multi-agent routing (channel→agent)|P0      |Moderate|Route inbound messages from specific channels, users, or groups to specific agent instances. Configuration maps channel+user patterns to agent names. Enables running a personal agent and a work agent on the same gateway.|
|FUNC-10a|Agent selection in gateway        |P0      |Easy    |The gateway supports multiple simultaneous agents, each with its own model config, tools, and permissions. The `agents.list[]` config array defines agents. Default agent handles unrouted messages.|
|FUNC-11|Per-agent isolated workspaces      |P0      |Easy    |Each agent gets its own workspace directory with independent MEMORY.md, sessions, skills, cron jobs, and HEARTBEAT.md. Agents cannot access each other's workspaces. Formalized with config-level support and filesystem enforcement via SEC-01 (Landlock).|

### 5.11 FUNCTIONAL — Extensibility

|ID     |Requirement                 |Priority|Effort  |Details                                                                                                                                                                                                |
|-------|----------------------------|--------|--------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-12|Skill marketplace / registry|P2      |Moderate|CLI commands to browse, search, install, and update skills from a central registry. Includes metadata (description, author, version, hash). Supports self-hosted registries for enterprise deployments.|
|FUNC-12a|ClawHub skill protocol     |P0      |Moderate|Implement the ClawHub registry protocol in Go: search, retrieve metadata, download packages. Consumes the existing 13,729+ skill ecosystem.|
|FUNC-12b|SKILL.md parser and loader |P0      |Easy    |Parse OpenClaw's SKILL.md format — Markdown with metadata frontmatter. Extends Omnipus's basic skills system to be format-compatible with ClawHub skills.|
|FUNC-12c|Skill hash verification    |P0      |Easy    |When installing a skill from ClawHub, verify its SHA-256 hash against the registry manifest. Integrates with SEC-09.|
|FUNC-12d|Skill install/update/remove CLI|P0   |Easy    |CLI commands: `omnipus skill install/update/remove/search/list`. Skills install to `~/.omnipus/workspace/skills/<name>/`.|
|FUNC-12e|ClawHub compatibility testing|P0     |Moderate|Automated test suite: install top 50 ClawHub skills, verify loading and tool registration. Run in CI.|
|FUNC-13|Skill auto-discovery        |P1      |Moderate|At runtime, automatically discover and register tool definitions from installed MCP servers and skills. Eliminates manual tool registration in config. New tools discovered via MCP are subject to policy (SEC-04, SEC-07).|
|FUNC-14|Plugin system (bundles)     |P3      |Moderate|Architecture for distributing provider integrations, channel adapters, and tool packs as installable bundles. Plugin loader discovers and initializes plugins at startup.                              |

### 5.12 FUNCTIONAL — Operational Tooling

|ID     |Requirement                         |Priority|Effort|Details                                                                                                                                                                                            |
|-------|------------------------------------|--------|------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-15|Diagnostic command (`omnipus doctor`)|P1      |Easy  |CLI command that checks system state: config validity, provider connectivity, channel health, disk space, kernel feature availability (Landlock, seccomp), and reports issues with fix suggestions.|
|FUNC-16|Backup and restore                  |P1      |Easy  |`omnipus backup create` archives workspace, config, credentials, and policy files into a single tarball. `omnipus backup restore` recovers from archive. Supports `--encrypt` flag using AES-256-GCM with the same master key used for `credentials.json` (SEC-23b). If no master key is available, `--encrypt` prompts for a passphrase.|
|FUNC-17|Tailscale integration               |P2      |Easy  |Built-in Tailscale Serve/Funnel support for secure remote access to the gateway without port forwarding or VPN setup. Uses Tailscale Go SDK.                                                       |
|FUNC-18|NVIDIA Nemotron native provider     |P3      |Easy  |Direct API integration with NVIDIA’s Nemotron model endpoints, bypassing the need for OpenRouter as intermediary. Reduces latency and cost for Nemotron users.                                     |
|FUNC-36|Graceful shutdown                   |P0      |Easy  |On shutdown signal (SIGTERM, window close, service stop): (1) Stop accepting new inbound messages. (2) Wait up to 10 seconds (configurable) for in-flight LLM calls to complete. (3) If timeout exceeded, save partial streamed response to session transcript marked as `"status": "interrupted"`. (4) Flush all buffered transcript entries and memory writes to disk. (5) Send disconnect to all channel connections. (6) Exit. On next startup, interrupted sessions show the partial response with a system message: "Session interrupted. Last response may be incomplete." Critical for data integrity on constrained hardware and headless deployments where unclean shutdowns are common.|

### 5.13 FUNCTIONAL — Browser Automation

|ID      |Requirement                         |Priority|Effort|Details                                                                                                                                                                                            |
|--------|------------------------------------|--------|------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-19|CDP browser control (managed mode)   |P0      |High  |Chrome DevTools Protocol browser automation using `chromedp`. Managed mode: Omnipus launches and manages a dedicated Chromium instance with its own user data directory, isolated from personal browser. Headless and headed operation.|
|FUNC-20|Browser action primitives            |P0      |Moderate|Agent-callable tools: `browser.navigate(url)`, `browser.click(selector)`, `browser.type(selector, text)`, `browser.screenshot()`, `browser.get_text(selector)`, `browser.wait(selector)`, `browser.evaluate(js)`. Structured results.|
|FUNC-21|Remote CDP mode                      |P1      |Easy  |Connect to external Chromium via CDP URL (`ws://host:port`). Enables cloud browser services (Browserless, Lightpanda) or Docker-hosted Chromium. Config: `tools.browser.cdp_url`.|
|FUNC-22|Browser resource limits              |P1      |Easy  |Configurable timeout per page load (default 30s), max concurrent tabs (default 5), max memory per browser profile. On constrained hardware, recommend remote CDP.|

### 5.14 FUNCTIONAL — WhatsApp

|ID      |Requirement                         |Priority|Effort|Details                                                                                                                                                                                            |
|--------|------------------------------------|--------|------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-23|WhatsApp channel                     |P0      |High  |Compiled-in Go channel using `whatsmeow` library with `modernc.org/sqlite` (pure Go SQLite, no CGo). Supports text, images, documents, group messages. Session state persisted locally in `~/.omnipus/channels/whatsapp/session.db`.|
|FUNC-24|WhatsApp QR pairing flow             |P0      |Easy  |On first connection, generate QR code in CLI/TUI/WebUI for phone scanning. Persist session credentials for auto-reconnect. Handle session expiry and re-pairing gracefully.|
|FUNC-25|WhatsApp media handling              |P1      |Moderate|Send and receive images, audio, documents, location pins. Incoming media stored in workspace temp directory as file references. Subject to filesystem policy (SEC-01).|

### 5.15 FUNCTIONAL — Core Experience

|ID      |Requirement                         |Priority|Effort|Details                                                                                                                                                                                            |
|--------|------------------------------------|--------|------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-26|Streaming response output            |P0      |Easy  |Token-by-token streaming from LLM providers to all output surfaces (WebUI, CLI, channels). SSE for WebUI, chunked messages for messaging channels. Prerequisite for responsive chat UX.|
|FUNC-27|Sub-agent supervisor/worker patterns |P1      |Moderate|Extend async spawn with structured orchestration. Supervisor can: assign tasks, track status, aggregate results, handle failures with retry/fallback. Workers inherit parent security policy. Internal message passing.|
|FUNC-28|Canvas / visual workspace            |P1      |High  |Agent-driven interactive HTML rendering surface. Agent generates and pushes HTML/CSS/JS to canvas panel via WebSocket. Use cases: dashboards, visualizations, reports, forms. Standard HTML, not custom component library.|
|FUNC-29|Intermediate tool output streaming   |P1      |Moderate|During multi-step tool execution, stream partial results to UI in real time. Collapsible output blocks showing tool name, status, intermediate output. WebSocket event `tool.progress`.|

### 5.16 FUNCTIONAL — Additional Channels

|ID      |Requirement                         |Priority|Effort|Details                                                                                                                                                                                            |
|--------|------------------------------------|--------|------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|FUNC-30|iMessage / BlueBubbles channel       |P1      |Moderate|Channel provider via BlueBubbles REST API. Send/receive text, images, group messages. Requires BlueBubbles server on Mac. Config under `channels.imessage`.|
|FUNC-31|Voice wake / talk mode               |P2      |High  |Wake word detection + continuous voice conversation with TTS. STT via Whisper (Groq/local). TTS via ElevenLabs or system TTS. Platform-specific audio: macOS (CoreAudio), Linux (PulseAudio/ALSA), Windows (WASAPI).|
|FUNC-32|macOS menu bar app                   |P2      |Moderate|Native macOS companion for gateway lifecycle (start/stop/restart), status indicator, quick actions (open WebUI, view logs, run doctor). Go + systray or native Swift.|
|FUNC-33|iOS/Android node companion           |P2      |High  |Turn mobile devices into agent peripherals: camera, GPS, push notifications, canvas rendering. WebSocket to gateway with device pairing (SEC-21). Logged (SEC-15) and subject to RBAC (SEC-19). Requires per-capability consent.|
|FUNC-34|Zalo channel                         |P3      |Easy  |Channel provider via Zalo API. Vietnamese market. REST API. Config under `channels.zalo`.|
|FUNC-35|Feishu / Lark (full implementation)  |P3      |Moderate|Complete the existing Feishu config: event subscription, message send/receive, rich text cards, group support. Config already exists under `channels.feishu`.|

-----

## 6. Delivery Phases

### Phase 1 — Security Foundation

**Goal:** Establish the core security layer that makes Omnipus enterprise-viable.

Features included:

- SEC-01: Landlock filesystem sandboxing
- SEC-02: Seccomp syscall filtering
- SEC-04: Tool allow/deny lists per agent
- SEC-05: Per-binary execution control
- SEC-07: Deny-by-default policy model
- SEC-11: Declarative JSON policy files
- SEC-12: Static policies (load-once)
- SEC-15: Structured audit logging
- SEC-16: Log redaction
- SEC-22: Credential injection via environment
- SEC-24: SSRF protection
- SEC-26: Rate limiting
- SEC-28: Exec tool HTTP proxy enforcement
- SEC-29: Network egress warning in diagnostics
- FUNC-10/10a: Multi-agent routing and agent selection
- FUNC-11: Per-agent isolated workspaces
- FUNC-12b/12d: SKILL.md parser and skill CLI
- FUNC-26: Streaming response output

**Exit criteria:** An agent running on Linux 5.13+ is kernel-sandboxed with filesystem and syscall restrictions, has a deny-by-default tool policy, produces a structured audit log with redacted credentials, and blocks SSRF attempts. Exec tool child processes route HTTP through a local proxy applying SSRF rules. Multi-agent routing is operational. Streaming output works across all surfaces. On older kernels, the same policy config falls back to application-level enforcement.

### Phase 2 — Governance & Controls

**Goal:** Add the governance, RBAC, and fine-grained control features that enterprises require.

Features included:

- SEC-03: Child process sandbox inheritance
- SEC-06: Per-method/API-call control
- SEC-08: Exec approval prompt (CLI/TUI/WebUI)
- SEC-09: Skill trust verification
- SEC-17: Explainable policy decisions
- SEC-19: Role-based access control
- SEC-20: Gateway authentication
- SEC-21: Device pairing
- SEC-23/23a–e: Credential encryption at rest (full key management)
- SEC-25: Prompt injection defenses
- SEC-27: Security audit command (`omnipus doctor`)
- SEC-30: DM policy safety checks
- FUNC-12a/12c/12e: ClawHub protocol, hash verification, compat testing
- FUNC-13: Skill auto-discovery
- FUNC-15: Diagnostic command
- FUNC-16: Backup and restore
- FUNC-19/20: CDP browser control and action primitives
- FUNC-23/24: WhatsApp channel provider and QR pairing
- FUNC-04/05/06: Signal, Teams, Google Chat channels
- FUNC-27: Sub-agent supervisor/worker patterns

**Exit criteria:** Operators can define roles, authenticate to the gateway, approve/deny exec commands interactively, verify skill integrity before installation, run `omnipus doctor` to assess posture, and back up/restore their deployment. ClawHub skills install and load. Browser automation and WhatsApp are operational. Enterprise channels (Signal, Teams, Google Chat) are connected.

### Phase 3 — Ecosystem & Extensibility

**Goal:** Close functional gaps versus OpenClaw/NemoClaw and build the extensibility platform.

Features included:

- SEC-10: Two-layer policy enforcement
- SEC-13: Hot-reloadable policies
- SEC-14: Policy change approval workflow
- ~~SEC-18: Tamper-evident log chain~~ (descoped v1.0)
- FUNC-01: Privacy-aware inference routing
- FUNC-02: Inference interception
- FUNC-03: Cost-based model routing
- FUNC-07/08/09: Nostr, Mattermost, Twitch channels
- FUNC-12: Skill marketplace (full registry)
- FUNC-14: Plugin system
- FUNC-17: Tailscale integration
- FUNC-18: Nemotron native provider
- FUNC-21/22: Remote CDP and browser limits
- FUNC-25: WhatsApp media handling
- FUNC-28: Canvas / visual workspace
- FUNC-29: Intermediate tool output streaming
- FUNC-30: iMessage channel
- FUNC-31: Voice wake / talk mode
- FUNC-32: macOS menu bar app
- FUNC-33: iOS/Android node companion
- FUNC-34/35: Zalo, Feishu channels

**Exit criteria:** Omnipus supports 20+ channels, has a functional skill registry with trust verification, supports privacy-aware inference routing, and provides a plugin architecture for third-party extensions. The full policy engine supports hot-reload, approval workflows, and tamper-evident audit logs.

-----

## 7. Success Metrics

|Metric                                |Target                                          |Measurement                                |
|--------------------------------------|------------------------------------------------|-------------------------------------------|
|RAM overhead from security features   |< 10MB above baseline                           |Benchmark on Raspberry Pi Zero 2W          |
|Startup time regression               |< 200ms added                                   |Benchmark on 0.8GHz single-core            |
|Binary size increase                  |< 20MB added (includes `modernc.org/sqlite`)    |Build comparison                           |
|Kernel sandbox escape rate            |No escapes in structured pen test covering: filesystem access outside workspace, privilege escalation, child process escape, inter-agent workspace access|Security assessment after Phase 1 (automated suite + manual red-team)|
|Audit log coverage                    |100% of tool invocations + exec commands        |Automated test suite                       |
|Ecosystem compatibility               |Top 50 ClawHub skills install and load correctly|Automated CI test (FUNC-12e)               |
|Channel parity with OpenClaw          |20+ channels (from current ~14)                 |Feature checklist                          |
|Skill verification coverage           |100% of externally-installed skills verified    |Integration test                           |
|`omnipus doctor` detection rate       |Catches all known misconfigurations             |Test against intentionally insecure configs|

**Hardware tiers:** Not all features are expected to run on all hardware. The following tiers define the minimum hardware for each feature set:

| Tier | Example hardware | RAM | Expected feature set |
|---|---|---|---|
| Constrained | Pi Zero 2W ($10) | 512MB | Gateway + CLI + 2-3 compiled-in channels, no browser, no web UI serving |
| Standard | Pi 4 / entry VPS ($35) | 2-4GB | All features except browser automation. Web UI served. |
| Full | Desktop / VPS ($100+) | 8GB+ | All features including browser automation (chromedp + local Chromium) |

-----

## 8. Dependencies & Assumptions

**Dependencies:**

- Linux kernel 5.13+ for Landlock LSM (ABI v1). Kernel 5.19+ for ABI v2, 6.2+ for ABI v3.
- Linux kernel 3.17+ for seccomp-BPF with `SECCOMP_FILTER_FLAG_TSYNC`.
- Go 1.21+ for `slog` structured logging (stdlib).
- `golang.org/x/sys/unix` package for Landlock and seccomp syscall access.
- External channel APIs (Signal CLI, Microsoft Bot Framework, Google Workspace API) for Phase 3 integrations.

**Assumptions:**

- Omnipus’s existing codebase (Go, ~86.8% Go) is architecturally suitable for adding security middleware in the agent loop and tool invocation paths without major refactoring.
- Team composition and timeline to be determined after prioritization and detailed specification.
- Kernel-level features (Landlock, seccomp) will be Linux-only. macOS, Windows, FreeBSD, and Android/Termux deployments will use application-level enforcement only. This is acceptable because the primary enterprise deployment target is Linux.
- The existing JSON config format can be extended without breaking changes. No migration to YAML is required.
- MCP support (added in v0.2.1) is stable enough to build skill auto-discovery on top of.

-----

## 9. Risks

|Risk                                                 |Impact                                   |Likelihood|Mitigation                                                                                                                                                  |
|-----------------------------------------------------|-----------------------------------------|----------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Landlock/seccomp complexity exceeds estimate         |Phase 1 delay                            |Medium    |Spike: build a standalone PoC in week 1 before committing to the architecture. Use existing Go libraries (go-landlock, go-seccomp) as starting points.      |
|Security features increase RAM beyond budget         |Erodes Omnipus’s core value prop        |Low       |Profile continuously. Audit logging uses ring buffer with configurable max size. Policy engine uses minimal in-memory representation.                       |
|Backward compatibility break in config               |Alienates existing 25K-star community    |Medium    |All new config sections are additive under a `security` key. Validate with v0.2.x config corpus before each release.                                        |
|Channel integration APIs change or require paid tiers|Phase 3 scope creep                      |Medium    |Prioritize open-protocol channels (Nostr, Mattermost, Twitch/IRC) first. Enterprise channels (Teams, Google Chat) may require user-provided API credentials.|
|Omnipus upstream moves fast (95% AI-bootstrapped)   |Merge conflicts, divergence from upstream|High      |Contribute security features upstream to upstream omnipus. Maintain Omnipus as a compatible fork if upstream declines.                                        |
|NemoClaw matures faster than expected                |Competitive window closes                |Medium    |Focus on the lightweight differentiator — Omnipus runs where NemoClaw cannot (edge, IoT, constrained hardware). This is not a market NemoClaw targets.       |

-----

## 10. Glossary

|Term                  |Definition                                                                                                                                                                                                                      |
|----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|**Landlock**          |A Linux Security Module (LSM) available since kernel 5.13 that enables unprivileged processes to restrict their own filesystem access. Unlike AppArmor or SELinux, it requires no system-wide configuration or root access.     |
|**seccomp-BPF**       |Secure Computing mode with Berkeley Packet Filters. Allows a process to install a filter that restricts which system calls it can make. Used to block privilege escalation, raw socket creation, and other dangerous operations.|
|**MCP**               |Model Context Protocol. An open standard for connecting AI models to external tools and data sources. Omnipus supports MCP as of v0.2.1.                                                                                       |
|**OpenShell**         |NVIDIA’s open-source security runtime for autonomous AI agents. Used by NemoClaw to provide sandboxing, policy enforcement, and inference routing. Requires Docker and K3s.                                                     |
|**RBAC**              |Role-Based Access Control. A method of regulating access based on the roles of individual users or processes within an organization.                                                                                            |
|**SSRF**              |Server-Side Request Forgery. An attack where an agent is tricked into making HTTP requests to internal/private network endpoints.                                                                                               |
|**Deny-by-default**   |A security model where all actions are blocked unless explicitly permitted by policy. The opposite of Omnipus’s current permissive model.                                                                                      |
|**ClawHub**           |OpenClaw’s community skill registry, hosting 13,729+ skills as of February 2026. Has been targeted by malware campaigns (ClawHavoc).                                                                                            |
|**Hot-reload**        |The ability to update configuration or policy at runtime without restarting the agent process.                                                                                                                                  |
|**Tamper-evident log**|~~An audit log where each entry is cryptographically chained to the previous entry via HMAC, making modifications or deletions detectable.~~ **Descoped v1.0.** HMAC-chained audit logs are not required for NemoClaw parity or v1.0 enterprise readiness.                                                                                       |

-----

*End of Document*
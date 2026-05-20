# OpenClaw vs Omnipus — Feature & UI/UX Comparison

**Version:** 1.1 DRAFT
**Date:** March 28, 2026
**Purpose:** Research foundation for Omnipus UI/UX and feature requirements
**Status:** Reviewed — requirements derived from this analysis have been accepted into the Omnipus BRD (Appendix B, Appendix C) as of March 28, 2026.

**Note:** This is a research document, not a requirements specification. Findings have been validated against Omnipus v0.2.3 source code and OpenClaw documentation. Corrections applied: Omnipus heartbeat feature confirmed present (v0.1.x+).

---

## 1. Executive Summary

This document provides a structured comparison of OpenClaw and Omnipus across two dimensions: (1) feature capabilities and (2) UI/UX maturity. The goal is to identify gaps, weaknesses, and opportunities that will inform the Omnipus UI/UX specification.

**Key finding:** Both products have significant UI/UX weaknesses, confirming the hypothesis. OpenClaw has a broader feature set but its UI is buggy, developer-centric, and fragmented across multiple surfaces. Omnipus's UI is minimal and immature — essentially a configuration launcher with basic chat bolted on. Neither product delivers a cohesive, polished experience suitable for both technical and non-technical users. This represents a clear opportunity for Omnipus.

---

## 2. Product Profiles

### 2.1 OpenClaw

- **Origin:** Created by Peter Steinberger (Nov 2025), renamed from Clawdbot → Moltbot → OpenClaw (Jan 2026). Steinberger joined OpenAI in Feb 2026; project moved to open-source foundation.
- **Tech stack:** Node.js runtime, TypeScript.
- **Scale:** ~250K+ GitHub stars, 13,729+ skills on ClawHub, 145K+ forks.
- **Architecture:** Gateway (WebSocket control plane) → Agent Runtime → Tools. Hub-and-spoke model. Messaging platforms are the primary interface. Canvas (A2UI) is a secondary visual workspace.
- **Deployment:** Mac Mini, VPS, Docker, Raspberry Pi. Requires Node.js runtime (~180MB+ memory).

### 2.2 Omnipus

- **Origin:** Created by Sipeed team (Feb 9, 2026). Inspired by nanobot project, rewritten in Go.
- **Tech stack:** Pure Go, single binary. 95% AI-bootstrapped code.
- **Scale:** ~25K GitHub stars, pre-v1.0, rapid growth.
- **Architecture:** Core binary + Gateway + Web Launcher (separate process). React frontend (Vite + TanStack Router) served by Go backend.
- **Deployment:** Runs on $10 hardware, RISC-V/ARM/x86. <10MB RAM, <1s boot.

---

## 3. Feature Comparison

### 3.1 Channel Integrations

| Channel | OpenClaw | Omnipus |
|---------|----------|----------|
| WhatsApp | ✅ | ❌ |
| Telegram | ✅ | ✅ |
| Discord | ✅ | ✅ |
| Slack | ✅ | ✅ |
| Signal | ✅ | ❌ |
| iMessage / BlueBubbles | ✅ | ❌ |
| Google Chat | ✅ | ❌ |
| Microsoft Teams | ✅ | ❌ |
| Matrix | ✅ | ✅ (v0.2.1) |
| IRC | ✅ | ✅ (v0.2.1) |
| LINE | ✅ | ✅ |
| Feishu / Lark | ✅ | ❌ |
| WeCom | ✅ | ✅ (v0.2.1) |
| QQ | ❌ | ✅ |
| DingTalk | ❌ | ✅ |
| Mattermost | ✅ | ❌ |
| Nostr | ✅ | ❌ |
| Twitch | ✅ | ❌ |
| Zalo | ✅ | ❌ |
| Bluesky | ❌ | ❌ |
| WebChat (built-in) | ✅ | ✅ (v0.2.0) |
| **Total** | **~22+** | **~10** |

**Assessment:** OpenClaw has roughly double the channel coverage. Omnipus's strength is in Asian messaging platforms (QQ, DingTalk, WeCom). OpenClaw dominates Western enterprise channels (Teams, Google Chat, Signal, WhatsApp).

### 3.2 Core Agent Capabilities

| Capability | OpenClaw | Omnipus |
|------------|----------|----------|
| File read/write | ✅ | ✅ |
| Shell command execution | ✅ | ✅ |
| Browser automation (CDP) | ✅ | ❌ |
| Persistent memory | ✅ (Markdown) | ✅ (JSONL, v0.2.1) |
| Cron / scheduled tasks | ✅ | ✅ |
| Sub-agents | ✅ | ✅ (async) |
| Multi-agent routing | ✅ | ❌ (planned) |
| Vision / multimodal input | ✅ | ✅ (v0.2.1) |
| Voice Wake / Talk Mode | ✅ (macOS/iOS/Android) | ❌ |
| Smart model routing | ✅ | ✅ (v0.2.1) |
| MCP protocol support | ✅ | ✅ (v0.2.1) |
| Skills system | ✅ (ClawHub, 13K+ skills) | ✅ (basic, no registry) |
| Heartbeat / proactive actions | ✅ | ✅ (v0.1.x+, HEARTBEAT.md + configurable interval + subagent spawn) |
| Canvas (visual workspace) | ✅ (A2UI) | ❌ |
| Web search | ✅ (Brave) | ✅ (Brave, DuckDuckGo, Exa) |
| Hardware I/O (I2C/SPI) | ❌ | ✅ (v0.2.1) |

**Assessment:** OpenClaw is more feature-rich in agent capabilities. Browser automation, Canvas/A2UI, and Voice Wake are major differentiators. Both have heartbeat/proactive agent support. Omnipus's unique strengths are hardware I/O support (IoT use cases) and its web search provider diversity.

### 3.3 Security & Governance

| Feature | OpenClaw | Omnipus |
|---------|----------|----------|
| Workspace isolation | ✅ | ✅ (basic) |
| Tool allow/deny lists | ✅ (profiles + per-agent) | ✅ (v0.2.3, config-level) |
| Kernel-level sandbox (Landlock) | ❌ | ❌ |
| Kernel-level sandbox (seccomp) | ❌ | ❌ |
| Exec approval prompts | ✅ | ❌ |
| Dangerous command blocking | ✅ | ✅ |
| RBAC | ❌ | ❌ |
| Structured audit logging | ❌ (basic logging) | ❌ |
| Credential encryption | ❌ | ❌ |
| Device pairing / auth | ✅ (token + device flow) | ❌ |
| Cron security gating | ❌ | ✅ (v0.2.3) |
| DM policy checking (`doctor`) | ✅ | ❌ |
| Security diagnostics | ✅ (`openclaw doctor`) | ❌ |

**Assessment:** Neither product has the enterprise security features defined in the Omnipus BRD. OpenClaw is ahead with tool profiles, exec approvals, device pairing, and the `doctor` diagnostic. Omnipus is catching up quickly (cron gating in v0.2.3, tool config in v0.2.1) but lacks the governance layer entirely. This is the primary gap Omnipus addresses.

### 3.4 Deployment & Operations

| Feature | OpenClaw | Omnipus |
|---------|----------|----------|
| Docker support | ✅ | ✅ (v0.2.0) |
| Tailscale integration | ✅ (Serve/Funnel) | ❌ |
| Health check endpoints | ✅ | ✅ |
| `doctor` diagnostic command | ✅ | ❌ |
| Backup/restore | ❌ | ❌ |
| System tray (Windows/Linux) | ❌ | ✅ (v0.2.3) |
| macOS menu bar app | ✅ | ❌ |
| iOS/Android companion apps | ✅ (nodes) | ❌ (APK in development) |
| Hot-reload config | Experimental | Experimental (v0.2.3) |

---

## 4. UI/UX Comparison

### 4.1 OpenClaw UI Architecture

OpenClaw's UI is fragmented across multiple surfaces:

| Surface | Technology | Purpose |
|---------|-----------|---------|
| Control UI (Gateway Dashboard) | Vite + Lit SPA | Chat, config, monitoring, tools management. Served from Gateway on port 18789. |
| WebChat | Native (SwiftUI on macOS/iOS) OR Control UI chat tab | Conversational interface. Uses Gateway WebSocket. |
| Canvas (A2UI) | Separate server (port 18793), WKWebView/WebView | Agent-driven visual workspace. HTML/CSS/JS rendered surfaces. |
| macOS Menu Bar App | Native Swift | Gateway control, Voice Wake, Canvas. |
| iOS/Android Nodes | Native apps | Camera, screen, Voice Wake, Canvas. |
| CLI | Terminal | Full operational control. |

**Key observation:** The primary user interface for most OpenClaw users is **not the web UI** — it is the messaging platform (WhatsApp, Telegram, etc.). The Control UI is a secondary management dashboard. This is a fundamental architectural choice: OpenClaw treats messaging apps as the frontend and the Control UI as the backend management console.

### 4.2 OpenClaw UI/UX Weaknesses

Based on GitHub issues, community feedback, and documentation analysis:

**Stability Problems:**
- Control UI crashes with >50-200 messages in a session due to synchronous full-history rendering (Issues #44107, #45687).
- v2026.3.22 shipped without Control UI assets in the npm package, breaking the dashboard entirely (503 errors).
- Image/screenshot upload was broken in WebChat for months (Issues #4685, #16152, #32474). Only recently partially fixed.
- Chat history reload storm during tool-heavy agent runs caused dashboard freezes (fixed in v2026.3.13).
- Oversized warning icons hiding message input (Issue #45694).

**Configuration UX:**
- Config/Models/Updates sections described as "nearly unusable for day-to-day configuration management" (Issue #13142).
- No clear indication of what settings are safe to change vs. what could break things.
- Setup wizard shows stale version information.
- No version indicator visible in Config panel.
- Tool availability is confusing: difference between "Available Right Now" (runtime) and "Tool Configuration" (catalog) is unclear.

**Chat Interface:**
- The chat interface itself works reasonably well for basic text exchange.
- Tool call visualization is minimal in the built-in UI — this is why third-party UIs like PinchChat and Nerve exist.
- PinchChat (third-party) was specifically created to fill the gap of tool call visualization: "colored badges, visible parameters, expandable results. The killer feature missing from every other chat UI."
- Nerve (third-party) positions itself as "the cockpit OpenClaw deserves" — adding workspace view, kanban board, sub-agent sessions, charts, and usage visibility. Its tagline: "Chat is great for talking to agents. It is not enough for operating them."

**Navigation & Information Architecture:**
- Only the Chat tab works well; the rest of the dashboard needs improvement.
- Mobile navigation was broken between 769px and 1100px until recently fixed.
- No clear separation between "user-facing" and "admin-facing" features.

**Missing In-Chat UX Capabilities:**
- No inline tool call visualization with parameters and results (must use third-party UIs).
- No rich rendering of structured agent outputs (tables, charts, code).
- No approval workflow UI integrated into chat.
- No cost/token tracking visible during conversation.
- No progress indicators during multi-step tool execution.

### 4.3 Omnipus UI Architecture

Omnipus's UI is simpler but less mature:

| Surface | Technology | Purpose |
|---------|-----------|---------|
| Web Launcher | React + Vite + TanStack Router (Go backend) | Config wizard, gateway control, basic chat. Port 18800. |
| TUI Launcher | Bubble Tea (Go) | Terminal-based config and management. |
| System Tray | OS-native (v0.2.3) | Gateway status on Windows/Linux. |
| CLI | Terminal | Full operational control. |

**Key observation:** Omnipus's Web UI was added in v0.2.0 (Feb 28, 2026) — less than one month old. It is primarily a **configuration launcher**, not a comprehensive agent management interface. The chat interface uses a native WebSocket protocol.

### 4.4 Omnipus UI/UX Weaknesses

Based on GitHub issues, roadmap, and documentation:

**Maturity:**
- The WebUI is less than one month old and still described as needing "frontend optimization" in the official roadmap.
- The roadmap explicitly calls out: "Many users get stuck during the initial configuration. The UI needs to be more intuitive and secure."
- No security configuration UI exists yet (roadmap item: "Add a dedicated UI panel for security and permission configurations").
- No dedicated documentation site (roadmap: "Build and deploy a dedicated documentation website").

**Missing Capabilities:**
- No tool call visualization in chat.
- No session management or session switching.
- No multi-agent view or agent status dashboard.
- No skill management UI.
- No audit log viewer.
- No workspace/file browser.
- No intermediate output streaming during tool execution (roadmap: "Stream intermediate results during tool execution loops to prevent the UX from feeling 'stuck'").
- No execution interruption capability (roadmap: "Allow users to manually interrupt/cancel an ongoing tool execution loop").
- No Canvas or visual workspace equivalent.
- No voice interface.

**Architecture Concern:**
- The Web Launcher is a separate binary (`omnipus-launcher`) from the core (`omnipus`). This two-process model adds deployment complexity. The proposal for `omnipus_webui` as yet another separate repository would fragment this further.

### 4.5 Side-by-Side UI/UX Comparison

| UX Dimension | OpenClaw | Omnipus | Winner |
|-------------|----------|----------|--------|
| **Onboarding** | CLI-guided (`openclaw onboard`), confusing for non-technical users. Many users report getting stuck. | Web-based setup wizard (double-click launcher). Simpler but still requires API key knowledge. | Omnipus (slightly) |
| **Chat experience** | Functional but basic. No rich tool call rendering. Third-party UIs fill the gap. | Minimal. Basic text chat only. No streaming feedback during tool execution. | OpenClaw |
| **Tool call visibility** | Minimal in built-in UI. PinchChat/Nerve needed for real-time tool badges and parameters. | None. Tools execute invisibly. | OpenClaw (barely) |
| **Configuration** | Config panel exists but described as "nearly unusable." Unsafe to edit without CLI knowledge. | Config wizard exists. Clean but very basic. No advanced settings exposed. | Tie (both poor) |
| **Agent monitoring** | Session list, basic status. Crashes with large sessions. | None in UI. | OpenClaw |
| **Mobile responsive** | Recently fixed (navigation drawer at <1100px). Android/iOS native apps exist. | Not documented. Android APK in development. | OpenClaw |
| **Security management** | Tool profiles visible in UI. DM policy checking via `doctor`. | Nothing. Security UI is a roadmap item. | OpenClaw |
| **Extensibility UX** | ClawHub for skills (CLI-based). Canvas for visual output. | Basic skill system, no browsing UI. | OpenClaw |
| **Visual workspace** | Canvas (A2UI) — agent can generate interactive HTML surfaces. | None. | OpenClaw |
| **Multi-agent UX** | Session switching, per-agent workspaces visible in UI. | Not in UI. | OpenClaw |
| **Cost visibility** | Token usage tracking exists. PinchChat adds progress bars. | Not visible in UI. | OpenClaw |
| **Stability** | Frequent crashes with large sessions, packaging bugs breaking dashboard. | Too new to have stability issues documented. | Inconclusive |
| **Design polish** | Functional but unpolished. Community contributors fixing CSS regressions. "Vite + Lit" is lightweight but limiting. | Clean React/Tailwind setup, but minimal content. | Tie |
| **Offline/degraded UX** | WebChat is read-only when gateway unreachable. | Not documented. | N/A |

---

## 5. Third-Party UI Ecosystem (OpenClaw Only)

The existence of third-party UIs for OpenClaw is itself a strong signal: the built-in UI is insufficient for serious users.

| Project | Focus | Key Features | Signal |
|---------|-------|-------------|--------|
| **PinchChat** | Chat-focused | Tool call visualization (badges, params, results), GPT-like session sidebar, token progress bars, inline images. | "The killer feature missing from every other chat UI" |
| **Nerve** | Operations cockpit | Multi-agent management, kanban board, workspace/file view, sub-agent sessions, charts, voice, themes. React 19 + shadcn/ui. | "Chat is great for talking to agents. It is not enough for operating them." |
| **Allclaw** | Documentation/aggregation | Describes OpenClaw WebUI features comprehensively. | Community documentation filling official gaps. |

**Implication for Omnipus:** The features that PinchChat and Nerve add are exactly what users want. These should be first-class capabilities in Omnipus, not afterthoughts or third-party add-ons.

---

## 6. Hypothesis Validation

**Original hypothesis:** "Both tools are not great from a UI perspective."

**Verdict: Confirmed, with nuance.**

**OpenClaw** has a functional UI that covers many surfaces (chat, config, monitoring, canvas) but suffers from:
- Architectural fragmentation (Control UI + Canvas + native apps + messaging + third-party UIs)
- Stability issues (crashes, packaging bugs, rendering storms)
- Poor configuration UX for non-CLI users
- Weak in-chat tool call visibility (the #1 community complaint)
- No integrated security/governance UI

**Omnipus** has a minimal UI that is:
- Very new (< 1 month old)
- Config-first, not experience-first
- Missing most operational and monitoring capabilities
- Clean tech stack (React + Vite + Tailwind) but almost no content

**Neither product** has:
- Integrated security policy management UI
- Audit log viewing/searching
- Approval workflow UI (exec approvals, policy change approvals)
- Rich, real-time tool call visualization as a first-class feature
- Coherent dual-audience design (technical + non-technical)
- A desktop-native experience (Electron or equivalent)

---

## 7. Derived Requirements for Omnipus UI

Based on this comparison, the following UX requirements emerge. These are **research-derived observations**, not final requirements — they need validation and prioritization with stakeholders.

### 7.1 Must-Have (Gaps both products fail to address)

1. **Real-time tool call visualization** — Show what the agent is doing: tool name, parameters, status (running/success/fail), results. Expandable/collapsible. This is the single most requested missing feature across the ecosystem.

2. **Integrated security management UI** — Policy editor, audit log viewer, permission matrix. Neither product exposes security controls in the UI. This is Omnipus's differentiator.

3. **Exec approval workflow in UI** — When the agent wants to run a command, present Allow/Deny/Always Allow directly in the chat or a side panel. Not just CLI prompts.

4. **Progress indicators for multi-step operations** — Token streaming, tool execution progress, intermediate results. Omnipus's own roadmap calls this out: users feel "stuck" without feedback.

5. **Stable large-session handling** — Virtualized message rendering, lazy loading. OpenClaw's #1 crash cause is loading full chat history synchronously.

### 7.2 Should-Have (Features OpenClaw has but does poorly)

6. **Session management** — Create, switch, archive sessions. Show session metadata (model, tokens used, cost, duration).

7. **Agent monitoring dashboard** — Status, active tools, memory usage, cron jobs, recent activity. Nerve calls this the "cockpit" — and it's right.

8. **Configuration with guardrails** — Categorize settings (basic vs. advanced), show impact warnings, validate before applying, preview changes. OpenClaw's config UI was called "nearly unusable."

9. **Mobile-responsive design** — Functional on tablet/phone. Not an afterthought.

10. **Cost/token visibility** — Per-session and aggregate. Inline during conversation.

### 7.3 Could-Have (Differentiation opportunities)

11. **Visual workspace (Canvas equivalent)** — Agent-generated interactive UI surfaces. OpenClaw's A2UI is interesting but buggy and fragmented across platforms.

12. **Dual-mode interface** — Simple mode (chat-focused, clean, non-technical) and Advanced mode (full operational dashboard). Toggle between them.

13. **Skill marketplace browser** — Search, install, rate, review skills from within the UI. Neither product does this in-UI today.

14. **Audit trail explorer** — Searchable, filterable log of all agent actions. Timeline view. This doesn't exist anywhere in the ecosystem.

15. **Desktop-native shell (Electron)** — System tray, native notifications, keyboard shortcuts, offline status awareness. Omnipus has system tray (v0.2.3); OpenClaw has a macOS menu bar app. Neither has a comprehensive desktop experience.

### 7.4 Anti-Requirements (What to explicitly avoid)

- **Do not** fragment the UI across 5+ surfaces like OpenClaw. One primary UI with clear navigation.
- **Do not** treat the web UI as secondary to messaging platforms. For Omnipus's enterprise audience, the web UI / desktop app IS the primary interface.
- **Do not** ship configuration editors without validation and impact warnings.
- **Do not** render full chat history synchronously. Virtualize from day one.
- **Do not** make tool call execution invisible. Transparency is a security feature, not just a UX feature.

---

## 8. Confidence Assessment

| Claim | Confidence | Basis | Missing |
|-------|-----------|-------|---------|
| Feature comparison accuracy | Medium-High | Based on official GitHub repos, docs, and changelogs for both projects. Both move fast. | May miss features in unreleased branches or undocumented capabilities. |
| UI/UX weakness assessment | High | Corroborated by multiple GitHub issues, community third-party projects, official roadmap admissions, and blog coverage. | No first-hand user research or usability testing data. |
| Third-party UI analysis (PinchChat, Nerve) | Medium | Based on GitHub READMEs and feature descriptions. | Have not tested these tools hands-on. |
| Derived requirements | Medium | Logical inference from gap analysis. | Requires stakeholder validation, user research, and prioritization. Not yet grounded in actual Omnipus user needs or personas. |

---

## 9. Next Steps

1. **Validate derived requirements** with Omnipus stakeholders. Are these the right priorities?
2. **Define user personas** — The "technical operator" and "non-technical business user" need concrete profiles before screen-level design.
3. **Decide deployment model** — Single-agent vs. multi-agent, single-user vs. multi-tenant. This affects every screen.
4. **Choose UI technology** — The comparison suggests React (Omnipus, Nerve, ZeroClaw all use it) is the ecosystem standard. Lit (OpenClaw) is an outlier.
5. **Produce the standalone UX/UI design spec** with screen-by-screen wireframes based on validated requirements.

---

*End of Comparison Document*

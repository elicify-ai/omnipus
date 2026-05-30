# Plugin Extensibility — Evidence-Based Assessment

**Date:** 2026-04-27
**Question:** Do the four subsystems treat their extensions as *plugins*?
1. All channels should be plugins.
2. All tools should be plugins.
3. Skills are already a form of plugin; MCP tools likewise.
Does the codebase already support extending these systems with additional plugins?

**Method:** Verdicts derive only from `pkg/` and `cmd/` source. Anything not in code is marked as such.

**Working definition of "plugin"** used here (taken from how the code actually behaves, not a doc):
- **P1 — Discoverability:** the system finds new extensions without hand-editing core files.
- **P2 — Open registration:** anyone implementing the interface can register without modifying a `switch`/if-ladder.
- **P3 — Configuration without code:** activating a new extension is a config edit, not a Go edit.
- **P4 — Runtime loading:** new extensions can be added without recompiling the binary.
- **P5 — Isolation/lifecycle:** the system manages start/stop/error of an extension uniformly.

A subsystem is a "real plugin system" only if it satisfies P1–P3 at minimum. P4 is the strong form.

---

## 1. Channels — partial plugin model, **not yet a plugin system**

### Evidence

- **Interface exists and is uniform** — `channels.Channel` (`pkg/channels/base.go:47-56`) plus opt-in capability interfaces (`pkg/channels/interfaces.go:13-70`). ✅ P5-shaped.
- **Factory map exists** — `RegisterFactory(name, factory)` writes into a `sync.Mutex`-guarded map (`pkg/channels/registry.go`). ✅ P2 *for the factory layer*.
- **`init()`-based registration** — each subpackage registers itself on import, e.g. `pkg/channels/telegram/init.go:10-16`. ✅ P2.
- **But activation is a hardcoded if-ladder** — `Manager.initChannels()` at `pkg/channels/manager.go:433-530` is a fixed switch over typed fields (`channels.Telegram`, `channels.Discord`, `channels.WhatsApp`, …). To add a new channel you must:
  1. Add a typed field to `ChannelsConfig` in `pkg/config/config.go:673-690`, **and**
  2. Add a new `if cfg.Channels.X.Enabled { m.initChannel("x", "X") }` branch in `manager.go:initChannels`.
  Only then does the factory entry registered by `RegisterFactory` get used. ❌ P3.
- **No runtime loading** — no `plugin.Open`, no `.so`/`.dll`, no subprocess channels. The only `exec.Command` in the channels package is Weixin's SILK voice transcoder (`pkg/channels/weixin/media.go`), which is a media codec, not a channel-loading mechanism. ❌ P4.
- **No `BridgeAdapter` / `ChannelBridge` type** — even WhatsApp, the one external bridge, is encoded as a regular in-process Go `Channel` that opens a WebSocket to a separate process configured via `BridgeURL` (`pkg/channels/whatsapp/whatsapp.go:31-46`). There is no generic external-channel adapter. ❌

### Verdict

> **Channels are not currently plugins. They are compile-time-registered modules whose activation requires editing two core files.**

The factory pattern is plugin-shaped, but two things break the plugin contract:
- `ChannelsConfig` has a typed field per channel (so config is closed).
- `initChannels()` has a typed branch per channel (so activation is closed).

Migrating channels to a real plugin model is a **bounded refactor**, not a rewrite, because the `Channel` interface and `RegisterFactory` already exist. The required changes:

1. Replace `ChannelsConfig`'s 16 typed structs with `map[string]ChannelInstanceConfig`.
2. Replace `initChannels()` with a loop: `for name, cfg := range channels { if cfg.Enabled { m.initChannel(name, cfg) } }`.
3. Decide whether external channels live in-process (factory + WebSocket like WhatsApp today) or as subprocess plugins (would require a new transport).

P4 (true runtime loading) is **not** delivered by that refactor and would be a separate, larger change.

---

## 2. Tools — **not a plugin system today**

### Evidence

- **Interface is uniform** — `tools.Tool` (`pkg/tools/types.go:22-30`). ✅ P5-shaped.
- **Registration is explicit and centralised** — all native tools are registered once at boot via `registerSharedTools` (`pkg/agent/loop.go:677`) into the shared `BuiltinRegistry` (`pkg/tools/builtin_registry.go:42`). The old per-agent `ToolCompositor.ComposeAndRegister` and `sysagent/tools.BuildRegistry` paths were deleted by the central tool registry redesign (completed 2026-04-28; see `docs/internal/specs/tool-registry-redesign-spec.md`). Each registration is still a hand-written `New*Tool()` constructor call. ❌ P1, ❌ P2.
- **No `init()`-based tool registration** — unlike channels, tools have no per-package self-registration. Adding a tool requires editing one of the registration sites.
- **No dynamic loading** — no `plugin.Open`, no subprocess tools. ❌ P4.
- **There is one well-defined out-of-process tool source: MCP** (covered in §4).

### Verdict

> **Native tools are not plugins.** They are a closed, hand-curated registry. The catalog is also explicit (`pkg/tools/catalog.go:45-147` enumerates the 35 known builtins).

A plugin migration here is more involved than channels because there is no factory map yet. To make tools pluggable in the same shape as channels:

1. Introduce `tools.RegisterFactory(name, factory)` and move each `New*Tool()` into a per-package `init()`.
2. Move tool **policy** (currently per-agent in `ToolCompositor`) to consume a registry, not a hardcoded list.
3. Decide what "external native tool" means — today the only out-of-process answer is MCP, and MCP **already** plays this role well (see §4). So the realistic plugin story for tools is: *native tools stay compiled-in; external/3rd-party tools come via MCP.*

This is the path the code is already on: MCP is the de facto plugin transport for tools, and the compositor unifies them at the registry level (`pkg/tools/compositor.go:30-75`, MCP wins on name collision).

---

## 3. Skills — **already a plugin system (P1–P3, partial P4)**

### Evidence

- **Filesystem discovery** — `SkillsLoader` (`pkg/skills/loader.go:99-187`) walks three roots in priority order: `{workspace}/skills/`, `~/.omnipus/skills/`, then compiled-in builtins. Each `SKILL.md` becomes an entry. ✅ P1.
- **Format-driven, no code edit needed** — frontmatter (`name`, `description`, `allowed-tools`, …) plus body (`pkg/skills/loader.go:28-39`). ✅ P3.
- **Open registration via filesystem drop-in** — copy a `SKILL.md` into a scanned dir and the loader picks it up. ✅ P2.
- **Runtime install via registry** — `SkillRegistry` interface + `RegistryManager` + ClawHub implementation (`pkg/skills/registry.go:49-117`) supports `Search`, `GetSkillMeta`, `DownloadAndInstall`, with hash verification and `IsMalwareBlocked` / `IsSuspicious` / `Verified` flags. **This is true runtime installation.** ✅ P4 (for install — not for in-process activation, which still requires the loader to re-scan).
- **Skills compose with tools** — `DiscoverAllTools` (`pkg/skills/discovery.go:16-44`) extracts `allowed-tools` and the compositor promotes hidden tools (TTL=1) when a skill is active. ✅ P5.

### Verdict

> **Skills are already plugins.** They satisfy P1, P2, P3, P5 fully and P4 substantially (install at runtime; promotion to active set is per-invocation).

The one nuance: a skill **does not introduce a new executable extension**. It declares an *allow-list of existing tools* (native or MCP) plus prompt text. Skills are therefore **prompt + capability bundles**, not new code paths. That is correct for what they are intended to do, but it means the interesting question is whether the *tools they reference* can themselves be plugins — which is §2/§4.

---

## 4. MCP — **the actual plugin transport for external tools**

### Evidence

- **External process model** — `pkg/mcp/manager.go:100-114` manages `map[string]*ServerConnection`. Each server is configured (not hardcoded) with `Command`/`Args` (stdio) or `URL` (HTTP/SSE), `Headers`, `EnvFile`, `Enabled` (`pkg/mcp/manager.go:124-237`). ✅ P3.
- **Lifecycle management** — `LoadFromConfig` connects all enabled servers concurrently; `ConnectServer` initialises the session; `GetAllTools` enumerates discovered tools; `CallTool(server, tool, args)` invokes (`pkg/mcp/manager.go:239-330+`). ✅ P5.
- **Native tool unification** — `MCPTool` wrapper (`pkg/tools/mcp_tool.go`) implements the same `tools.Tool` interface with name `serverName:toolName`. The compositor merges both registries (`pkg/tools/compositor.go:30-75`); MCP wins on name collision. ✅ P2.
- **Runtime activation** — adding an MCP server requires only a config entry; no code edits, no recompile. ✅ P3, ✅ P4.

### Verdict

> **MCP is a real, working plugin system for tools** — the only one in the codebase that satisfies P1–P5.

**Implication for the "all tools should be plugins" goal:** the codebase already supports it via MCP, *for tools that can run as MCP servers*. Native Go tools remain compiled-in. The architectural question is therefore not "can we add a plugin system for tools?" — one already exists — but "should every native tool be re-implemented as an MCP server, or should the native registry stay closed and treat MCP as the extension point?"

---

## 5. Side-by-side scorecard

| Property | Channels | Native tools | Skills | MCP |
|---|---|---|---|---|
| P1 — Discoverable | ❌ (typed config fields) | ❌ (hardcoded lists) | ✅ (FS scan) | ✅ (config list) |
| P2 — Open registration | ⚠️ (factory ✓, but switch closed) | ❌ | ✅ | ✅ |
| P3 — Config-only activation | ❌ | ❌ | ✅ | ✅ |
| P4 — Runtime install/load | ❌ | ❌ | ✅ (install) / ⚠️ (re-scan) | ✅ |
| P5 — Lifecycle uniform | ✅ | ✅ | ✅ | ✅ |
| **Overall** | **Module, not plugin** | **Closed registry** | **Plugin** | **Plugin** |

---

## 6. Direct answers to the three questions

### Q1. "All channels should be plugins."

**Today: no.** Channels are a closed registry guarded by a hardcoded if-ladder in `Manager.initChannels()` and a hand-written struct field per channel in `ChannelsConfig`.

**Foreseen by the architecture? Half-yes.**
The `Channel` interface, the `RegisterFactory` factory map, and the per-package `init()` self-registration are all consistent with a plugin model. The two blockers (typed config struct, typed if-ladder) are mechanical, not architectural — they can be replaced with a `map[string]ChannelInstanceConfig` and a single loop. **The interface is plugin-ready; the wiring is not.**

True runtime plugin loading (P4) is **not** in the codebase: there is no subprocess channel transport, no `plugin.Open`, no equivalent of MCP for channels. WhatsApp's WebSocket bridge is the closest analog and it is hand-coded, not generic.

### Q2. "All tools should be plugins."

**Today: native tools are not plugins; MCP tools are.**
- Native tools are a closed registry assembled by hand at three known sites; there is no `tools.RegisterFactory` and no `init()`-based tool registration.
- MCP fills the plugin role for tools and is fully wired into the same `tools.Tool` interface and the same `ToolRegistry`.

**Foreseen?**
Yes, **but via MCP rather than via a parallel native plugin system**. The compositor already treats native and MCP tools as polymorphic `Tool` instances; MCP takes precedence on name collision. The architecture is consistent with "native tools are core; everything else extends via MCP."

If you want native Go tools to also be plugins (i.e., a second extension axis on top of MCP), the code does not currently support that — it would require adding `tools.RegisterFactory` and moving each `New*Tool()` into per-package `init()`, mirroring the channels pattern. None of this exists in the repo today.

### Q3. "Skills are already a form of plugin, as are MCP tools — does the architecture foresee extension?"

**Yes, and the evidence is in the code:**
- Skills: filesystem-discovered `SKILL.md` plus runtime install via `SkillRegistry` + ClawHub (`pkg/skills/loader.go`, `pkg/skills/registry.go`).
- MCP: config-driven external servers, transport-agnostic (stdio / HTTP / SSE), unified into the native tool registry via `MCPTool` (`pkg/mcp/manager.go`, `pkg/tools/mcp_tool.go`).

These two are the *only* genuine plugin systems in the codebase. Channels and native tools are not — yet the channel side has the scaffolding (interface + factory map) to become one, and the native-tool side intentionally cedes that role to MCP.

---

## 7. Recommendations (mechanical, evidence-anchored)

These follow only from the gaps observed in code; no design speculation.

1. **Channels → real plugins (interface already in place):**
   - Replace `ChannelsConfig`'s typed fields (`pkg/config/config.go:673-690`) with `Instances map[string]ChannelInstanceConfig`.
   - Replace `Manager.initChannels()`'s if-ladder (`pkg/channels/manager.go:433-530`) with a loop driven by that map and the existing `RegisterFactory` registry.
   - Cost: bounded; the interface, factory map, and per-package `init()` already exist.

2. **External channels → bridge transport (currently absent):**
   - Define a `BridgeAdapter` (or generalize WhatsApp's WebSocket pattern) so a single in-process factory can drive any external channel implementation by URL/command. Mirrors the MCP transport story.

3. **Native tools → leave compiled-in; canonicalise MCP as the extension axis:**
   - The compositor already merges native and MCP. Adding `tools.RegisterFactory` + per-package `init()` for native tools is *possible* but offers little marginal value over what MCP already gives, so only do it if the goal is third-party in-process Go tools (rare).

4. **Skills → minor:**
   - Skills are already plugins. Two non-blocking items: (a) the `~/.omnipus/agents/{id}/memory/daily/` dir is provisioned but unused (`pkg/datamodel/init.go:140-172`); (b) the `RegistryManager` currently has only ClawHub — adding a second registry impl is a `SkillRegistry` interface implementation, no core change.

5. **Memory / providers** are out of scope for this question; both are closed registries with no plugin pathway in code (and the user did not raise them).

---

## 8. One-line summary

> **Skills and MCP are real plugin systems today. Channels have the interface and factory map but not the wiring — a bounded refactor away. Native tools are intentionally closed and rely on MCP for external extension. There is no runtime code-loading anywhere in the binary.**

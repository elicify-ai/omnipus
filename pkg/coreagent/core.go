// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package coreagent defines the 4 built-in core agents for Omnipus per
// the v0.1.0 roster re-cast (Spec-3): Mia·Assistant, Jim·Orchestrator,
// Ray·Scout, Ava·Builder. Max was retired from the seeded base.
//
// Core agents use the same mechanism as custom agents — same AgentInstance,
// registerSharedTools, ContextBuilder pipeline. The only differences:
//
//   - Prompts are compiled into the binary (not stored as SOUL.md on disk)
//   - Agents are seeded into config.json on first boot via SeedConfig
//   - Identity fields are locked (name, description, color, icon, prompt)
//   - Users CAN change model, remove tools, and set heartbeat
package coreagent

import (
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// CoreAgentID identifies a core agent.
type CoreAgentID string

const (
	IDJim CoreAgentID = "jim"
	IDAva CoreAgentID = "ava"
	IDMia CoreAgentID = "mia"
	IDRay CoreAgentID = "ray"
	// IDMax is intentionally absent: Max was retired from the 4-base roster
	// in Spec-3 (v0.1.0 foundation). The ID constant is removed so that any
	// remaining compile-time reference to IDMax surfaces as a build error.
)

// CoreAgent describes a built-in agent with compiled metadata and prompt.
type CoreAgent struct {
	ID          CoreAgentID
	Name        string // Display name (e.g., "Jim")
	Subtitle    string // Role subtitle (e.g., "General Purpose")
	Description string // One-line description
	Color       string // Hex color for avatar (e.g., "#22C55E")
	Icon        string // Phosphor icon name (e.g., "chat-circle")
	// DefaultTools is the list of tool names enabled by default.
	DefaultTools []string
}

// All returns the 4 base core agents in display order (Mia first, as the default).
// Max was retired from the seeded base in Spec-3 (v0.1.0 roster re-cast).
func All() []*CoreAgent {
	return []*CoreAgent{
		Mia(),
		Jim(),
		Ava(),
		Ray(),
	}
}

// ByID looks up a core agent by ID. Returns nil if not found.
func ByID(id CoreAgentID) *CoreAgent {
	for _, a := range All() {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// IsCoreAgent returns true if the given agent ID is a core agent.
func IsCoreAgent(id string) bool {
	return ByID(CoreAgentID(id)) != nil
}

// init validates that every core agent has a corresponding compiled prompt.
// A missing prompt is a programmer error that silently degrades the agent
// to the default identity — panic at startup to make it loud.
func init() {
	for _, ca := range All() {
		if _, ok := prompts[string(ca.ID)]; !ok {
			panic(fmt.Sprintf("coreagent: no compiled prompt for agent %q — add to prompts map", ca.ID))
		}
	}
}

// GetPrompt returns the compiled system prompt for the given agent ID.
// Returns empty string if the ID is not a core agent — callers should
// apply their own fallback (e.g., check SOUL.md or use default identity).
func GetPrompt(id string) string {
	return prompts[id]
}

// coreAgentSeed returns the constructor-seeded policy map and sandbox profile
// for the named core agent (FR-010, FR-022). The map is keyed by tool name
// (or trailing-wildcard prefix like "system.*") with values "allow", "ask",
// or "deny". sandboxProfile is the default SandboxProfile for the agent;
// empty string means "use global default (workspace)".
//
// All core agents share the rail: default_policy=allow plus system.*→deny.
// Ava additionally has explicit system.* allows for her 4 agent-CRUD tools.
// Jim gets sandbox_profile=workspace+net with workspace.shell,
// workspace.shell_bg, and web_serve explicitly allowed.
//
// The returned maps are independent allocations — callers may mutate them safely.
func coreAgentSeed(
	id CoreAgentID,
) (defaultPolicy config.ToolPolicy, policies map[string]config.ToolPolicy, sandboxProfile config.SandboxProfile) {
	// Every core agent starts with the same rail: allow-by-default + deny system.*.
	// Memory tools are explicitly seeded as allow (FR-016/FR-017) so they survive
	// any future default_policy change and appear prominently in the tool picker UI.
	// web_serve is explicitly allowed for every core agent (MAJOR-4): hoisting it
	// here ensures that all agents have a traceable allow entry, not just Jim.
	// Default_policy is allow, so this is belt-and-suspenders, but explicit entries
	// make intent visible in the tool picker UI and survive any future policy change.
	base := map[string]config.ToolPolicy{
		"system.*":      config.ToolPolicyDeny,
		"remember":      config.ToolPolicyAllow,
		"recall_memory": config.ToolPolicyAllow,
		"retrospective": config.ToolPolicyAllow,
		"web_serve":     config.ToolPolicyAllow,
	}
	switch id {
	case IDAva:
		// Ava is the only core agent with explicit system.* allows (FR-010).
		// Her four agent-CRUD tools must be allowed through the deny-wildcard rail.
		base["system.agent.create"] = config.ToolPolicyAllow
		base["system.agent.update"] = config.ToolPolicyAllow
		base["system.agent.delete"] = config.ToolPolicyAllow
		base["system.models.list"] = config.ToolPolicyAllow
	case IDJim:
		// Jim additionally uses workspace.shell and workspace.shell_bg (all
		// explicitly allowed so the policy passes through even when
		// default_policy is allow — belt-and-suspenders). web_serve is already
		// in the base map above so it is not repeated here.
		base["workspace.shell"] = config.ToolPolicyAllow
		base["workspace.shell_bg"] = config.ToolPolicyAllow
		return config.ToolPolicyAllow, base, config.SandboxProfileWorkspaceNet
	}
	return config.ToolPolicyAllow, base, ""
}

// HasSystemAllowsInConstructorSeed returns true if the named core agent's
// constructor seed contains explicit system.* allow entries (FR-062).
// Today only Ava qualifies. This is the predicate for the boot-time
// "critical abort on corrupt config" path.
func HasSystemAllowsInConstructorSeed(agentID string) bool {
	return CoreAgentID(agentID) == IDAva
}

// SeedConfig ensures all core agents exist in cfg.Agents.List with Locked=true
// and with the correct constructor-seeded tool policy (FR-010, FR-022).
//
// Creates missing agents and re-enforces Locked=true + identity fields on
// existing core agents (prevents config tampering from downgrading protection).
// Policy seeds are applied to agents that have no existing Tools config — agents
// that were manually configured via the SPA keep their existing policy entries.
//
// Returns true if config was modified (caller should save).
func SeedConfig(cfg *config.Config) bool {
	existing := make(map[string]bool, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		existing[a.ID] = true
	}

	modified := false

	// Re-enforce identity fields on existing core agents (tamper protection + rename).
	// Also apply idempotent profile migrations: if an existing core agent's
	// SandboxProfile is empty, fill it with the seed value. This covers the case
	// where a user upgrades from an older release — their Jim entry already
	// exists so the "new agent" branch below won't fire, but the profile is blank.
	// Operator-set profiles (non-empty) are left unchanged — operator's choice wins.
	for i := range cfg.Agents.List {
		ca := ByID(CoreAgentID(cfg.Agents.List[i].ID))
		if ca == nil {
			continue
		}
		a := &cfg.Agents.List[i]
		if !a.Locked {
			a.Locked = true
			modified = true
		}
		if a.Name != ca.Name {
			a.Name = ca.Name
			modified = true
		}
		if a.Description != ca.Description {
			a.Description = ca.Description
			modified = true
		}
		if a.Color != ca.Color {
			a.Color = ca.Color
			modified = true
		}
		if a.Icon != ca.Icon {
			a.Icon = ca.Icon
			modified = true
		}
		// Idempotent sandbox_profile migration.
		// Apply the seeded profile only when the existing entry has no profile set.
		_, _, seedProfile := coreAgentSeed(ca.ID)
		if seedProfile != "" && a.SandboxProfile == "" {
			a.SandboxProfile = seedProfile
			modified = true
		}

		// Jim is the operator-blessed agent for workspace.shell / workspace.shell_bg.
		// Ensure workspace_shell_enabled=true for Jim so he gets the tools even
		// when the global default is false (deny-by-default). Applied idempotently —
		// only flips when the pointer is currently nil (unset). Operator explicit
		// false is left unchanged.
		if ca.ID == IDJim && cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
			t := true
			cfg.Sandbox.Experimental.WorkspaceShellEnabled = &t
			modified = true
		}
	}

	for _, ca := range All() {
		if existing[string(ca.ID)] {
			continue
		}
		enabled := true
		dp, policies, seedProfile := coreAgentSeed(ca.ID)
		// Mia is the default agent on fresh installs: she appears first in the
		// All() list and is the friendliest entry-point for new users.
		// Only set Default=true on the fresh-seed path (here). The re-enforcement
		// loop above intentionally does NOT touch the Default field on existing
		// entries so operator choices survive config reload.
		isDefault := ca.ID == IDMia
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID:             string(ca.ID),
			Name:           ca.Name,
			Description:    ca.Description,
			Color:          ca.Color,
			Icon:           ca.Icon,
			Type:           config.AgentTypeCore,
			Locked:         true,
			Enabled:        &enabled,
			Default:        isDefault,
			SandboxProfile: seedProfile,
			Tools: &config.AgentToolsCfg{
				Builtin: config.AgentBuiltinToolsCfg{
					DefaultPolicy: dp,
					Policies:      policies,
				},
			},
		})
		// Jim is the operator-blessed agent for workspace.shell / workspace.shell_bg.
		// Flip workspace_shell_enabled=true when seeding Jim for the first time so
		// he gets the tools even when the global default is false (deny-by-default).
		// Applied once at creation time so the re-enforcement loop on subsequent
		// calls sees a non-nil value and skips.
		if ca.ID == IDJim && cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
			t := true
			cfg.Sandbox.Experimental.WorkspaceShellEnabled = &t
		}
		modified = true
	}
	return modified
}

// NewCustomAgentToolsCfg returns the default AgentToolsCfg for a newly created
// custom agent (FR-008, FR-022). Every custom agent starts with:
//
//   - default_policy: allow  (per FR-008: explicit allow-by-default)
//   - policies: {"system.*": "deny"}  (privilege rail — no system.* by default)
//
// Callers should embed this into config.AgentConfig.Tools when constructing a
// new custom agent via the REST API or system.agent.create tool.
func NewCustomAgentToolsCfg() *config.AgentToolsCfg {
	return &config.AgentToolsCfg{
		Builtin: config.AgentBuiltinToolsCfg{
			DefaultPolicy: config.ToolPolicyAllow,
			Policies: map[string]config.ToolPolicy{
				"system.*": config.ToolPolicyDeny,
			},
		},
	}
}

// --- Agent definitions ---

// Jim returns the Orchestrator core agent.
func Jim() *CoreAgent {
	return &CoreAgent{
		ID:       IDJim,
		Name:     "Jim — Orchestrator",
		Subtitle: "Orchestrator",
		Description: "Your coordination hub — breaks complex goals into tasks, " +
			"delegates to the right agents, tracks progress, and drives work to completion.",
		Color: "#22C55E",
		Icon:  "graph",
		DefaultTools: []string{
			"read_file", "write_file", "edit_file", "list_dir",
			"web_search", "web_fetch",
			"message", "send_file",
			"task_create", "task_update", "task_list",
			"cron", "spawn", "subagent",
			"handoff", "return_to_default",
		},
	}
}

// Ava returns the Builder core agent.
func Ava() *CoreAgent {
	return &CoreAgent{
		ID:       IDAva,
		Name:     "Ava — Builder",
		Subtitle: "Builder",
		Description: "Your agent architect — interviews you about what you need, " +
			"then creates a custom agent with a tailored personality and tools.",
		Color: "#D4AF37",
		Icon:  "wrench",
		DefaultTools: []string{
			"read_file", "write_file", "edit_file", "list_dir",
			"web_search", "web_fetch",
			"message",
			"system.agent.create", "system.agent.update", "system.agent.delete",
			"system.models.list",
			"handoff", "return_to_default",
		},
	}
}

// Mia returns the Assistant core agent (default ⭐).
func Mia() *CoreAgent {
	return &CoreAgent{
		ID:       IDMia,
		Name:     "Mia — Assistant",
		Subtitle: "Assistant",
		Description: "Your friendly everyday assistant — guides you through Omnipus, " +
			"answers questions, and connects you with the right specialist when needed.",
		Color: "#3B82F6",
		Icon:  "lightbulb",
		DefaultTools: []string{
			"read_file", "list_dir",
			"web_search", "web_fetch",
			"message",
			"handoff", "return_to_default",
		},
	}
}

// Ray returns the Scout core agent.
func Ray() *CoreAgent {
	return &CoreAgent{
		ID:       IDRay,
		Name:     "Ray — Scout",
		Subtitle: "Scout",
		Description: "Your research analyst — digs deep into topics, synthesizes findings " +
			"from multiple sources, and presents results with citations.",
		Color: "#A855F7",
		Icon:  "magnifying-glass",
		DefaultTools: []string{
			"read_file", "write_file", "edit_file", "list_dir",
			"web_search", "web_fetch",
			"message", "send_file",
			"handoff", "return_to_default",
		},
	}
}

// --- Compiled prompts ---
// These are the system prompts for each core agent, compiled into the binary.
// They are NOT stored on disk (no SOUL.md) so users cannot read them.
// The ContextBuilder calls GetPrompt(agentID) to inject these as the SOUL content.
//
// Crafted following Anthropic's context engineering principles:
// - Concise, structured sections (persona → scope → behavior → constraints)
// - Negative constraints for critical boundaries ("NEVER do X")
// - Concrete behavioral examples over abstract descriptions
// - Clear delegation rules with specific agent names
// - Token-efficient — no redundancy with ContextBuilder's injected content

var prompts = map[string]string{
	"jim": `You are Jim — the Orchestrator.

You are the coordination hub. When goals are complex, you break them into tasks, delegate each to the right agent, and track progress until the work is done. You also handle everyday requests yourself when no delegation is needed — you're a capable generalist who knows when to act and when to coordinate.

## How you work

- **Concise by default.** Give the answer, not a lecture. Expand only when asked or when the topic genuinely requires it.
- **Action over discussion.** When someone asks you to write something, write it. When they ask to find something, search for it. Don't ask "would you like me to…" — just do it.
- **Honest about limits.** Say "I'm not sure" rather than guessing. Indicate confidence levels when sharing factual claims.
- **Proactive follow-ups.** After completing a task, suggest one natural next step — but keep it brief.

## When to coordinate

You can handle most things yourself. Delegate when the task genuinely requires a specialist:

- **"Build me a custom agent"** → Create a task for Ava. You cannot create agents.
- **"Deep research with citations"** → Create a task for Ray when the user explicitly wants a multi-source investigation.
- **Complex multi-step goals** → Break into tasks, assign each to the best agent, monitor blocked_by dependencies until the DAG resolves.

NEVER deflect simple requests to other agents. If someone asks "what's the capital of France?" just answer it.

## Serving web apps

You can scaffold and serve web applications inside your sandboxed workspace.

Use workspace.shell to run any command (foreground, captures output):

  workspace.shell { command: "npm create next-app@latest hello-world --typescript --app --no-eslint --no-tailwind --no-src-dir", cwd: "" }
  workspace.shell { command: "npm install", cwd: "hello-world" }

Use workspace.shell_bg to start long-running processes like dev servers
(returns a clickable preview URL):

  workspace.shell_bg { command: "npm run dev", cwd: "hello-world", expose_port: 18000 }

The result includes a "url" field — share that URL with the user as a clickable link.
The user can click "Open in new tab" in the rendered preview to view the running app.

Both tools run inside your kernel sandbox: filesystem writes are confined to your
workspace, network access goes through an audited egress proxy. You can run any
command — npm, pip, go, cargo — without further restrictions inside that boundary.

Prefer workspace.shell over the generic exec tool — workspace.shell is
sandbox-aware end-to-end and gives clearer error messages on policy denial.

## What you never do

- NEVER add unnecessary caveats, disclaimers, or "as an AI" hedges
- NEVER refuse a reasonable request by suggesting another agent when you can handle it yourself
- NEVER produce walls of text when a few sentences suffice
`,

	"ava": `You are Ava — the Builder.

You help users bring their ideal AI assistant to life. You ask the right questions, design a clear personality, select tools, and build the agent — all through conversation.

## Interview flow

Run a structured interview — one question at a time:

1. **Purpose**: "What should this agent help you with?" — Listen for the core use case.
2. **Name & Identity**: "What should we call this agent?" — Get a name, suggest a color and icon.
3. **Personality**: "How should it communicate? Formal or casual? Concise or detailed?" — Get the voice right.
4. **Model**: "Want to use the system default model, or pick a different one?" — Call system.models.list if the user wants to browse options. Default to the system default model.
5. **Tools**: Reference the "Available Resources" section injected into your context. Suggest tools that match the use case. Ask if they want all tools (inherit) or a specific set (explicit).
6. **Advanced** (ask only if relevant): delegation targets, heartbeat scheduling, workspace restrictions, timeouts.
7. **Review**: Present a complete summary card. Ask for confirmation or adjustments.

## Summary card (present before creating)

| Field | Value |
|---|---|
| Name | {display name} |
| Description | {one-line purpose} |
| Model | {model slug} |
| Color | {hex color} |
| Icon | {phosphor icon name} |
| Tools | {inherit / explicit: list} |
| Delegation | {agent IDs or "none"} |
| Soul | {first 2 lines of the prompt...} |

## Creating the agent

Once confirmed, call system.agent.create with ALL mandatory parameters:
- **name**, **description**, **model**, **color**, **icon** — from the card
- **soul** — the full personality prompt (10-30 lines covering: role, personality traits, how to work, what to avoid). This is the most important parameter.
- **tools_mode** + **tools_visible** — if the user chose explicit tools
- **can_delegate_to** — if delegation targets were discussed
- **heartbeat** — if proactive scheduling was discussed
- **model_fallbacks** — if fallback models were discussed

Available colors: #22C55E (green), #3B82F6 (blue), #A855F7 (purple), #F97316 (orange), #EF4444 (red), #D4AF37 (gold), #6B7280 (gray), #EAB308 (yellow).
Available icons: robot, pencil, book, chat-circle, lightning, magnifying-glass, wrench, lightbulb, code, globe, heart, star, brain, shield, music-note, camera, rocket, calendar, envelope, chart-bar.

## Your personality

- **Thoughtful and creative** — genuinely care about getting the design right
- **Encouraging** — treat every idea as worth exploring
- **Structured** — interview flows naturally but covers all bases
- **Concise** — one question at a time, never overwhelm

## What you never do

- NEVER handle tasks, research, or automation — suggest Jim or Ray for those
- NEVER skip the interview — understand what the user wants first
- NEVER call system.agent.create without a detailed soul prompt
- NEVER write a one-line soul — craft 10-30 lines of behavioral instructions
`,

	"mia": `You are Mia — the Assistant.

You are the first face new users see and the always-available helper for anyone using Omnipus. You answer questions about the platform, guide people through setup, and hand off to the right specialist when the user's goal is beyond Omnipus help. Think of yourself as a patient, warm concierge who knows every corner of the system.

## Your personality

- **Warm and encouraging** — celebrate small wins ("Great, you've connected your first provider!")
- **Never condescending** — if someone asks a basic question, answer it with the same care as a complex one
- **Concrete** — always reference specific buttons, menu paths, and screen names
- **Brief when possible** — don't over-explain simple things, but be thorough for complex setups

## What you know

You have deep knowledge of every Omnipus feature:

**Screens & Navigation**: Chat (message agents, switch sessions), Agents (view/create/configure agents), Command Center (task board, status, rate limits), Skills & Tools (installed skills, MCP servers, channels, built-in tools), Settings (providers, security, gateway, data, routing, profile, devices)

**The Agent Team**: Jim is the Orchestrator — handles everyday tasks and multi-agent coordination. Ava is the Builder — creates custom agents through interviews. Ray is the Scout — deep research with citations.

**Key Features**: Per-agent tool visibility with presets (Researcher, Developer, Task Manager, Unrestricted, Custom). Browser automation (navigate, click, type, screenshot — requires Chromium). Task delegation between agents. Heartbeat scheduling for proactive agent runs.

**Channels**: Telegram (@BotFather → token → Settings → Channels), Discord (Developer Portal → bot token), Slack (App manifest), WhatsApp (whatsmeow, QR pairing).

**Security**: Landlock/seccomp sandboxing, exec approval dialogs, SSRF protection, rate limiting, audit logging, credential encryption (AES-256-GCM).

## How you communicate

- Use numbered steps for any setup guide: "1. Open Settings → Providers  2. Click '+ Add Provider'  3. Select OpenRouter…"
- When explaining a feature, describe what it does AND where to find it in the UI
- If someone asks about a task (not a question): use the handoff tool to connect them with Jim

## When to hand off — MANDATORY

You have a tool called handoff. You MUST call it when the user asks for anything outside Omnipus help:

- "I want to research..." → IMMEDIATELY call handoff(agent_id="ray", context="...", message="Connecting you with Ray...")
- "Automate..." / "Schedule..." / "Help me with..." / general tasks → IMMEDIATELY call handoff(agent_id="jim", context="...", message="Connecting you with Jim...")
- "Build me an agent..." → IMMEDIATELY call handoff(agent_id="ava", context="...", message="Connecting you with Ava...")

NEVER tell the user to "click the dropdown" or "switch manually". You have the handoff tool — USE IT.
NEVER say "I can't switch you". You CAN and you MUST. Call the handoff tool.

## What you never do

- NEVER suggest manual agent switching — always use the handoff tool
- NEVER execute tasks, write files, or run commands — you only explain and guide
- NEVER create agents — hand off to Ava for that
- NEVER guess about a feature you're unsure of — say "I'm not sure about that specific detail, but here's where you can check: Settings → …"
`,

	"ray": `You are Ray — the Scout.

You don't just search — you investigate. You dig through multiple sources, cross-reference claims, weigh evidence, and present findings with the rigor of a professional analyst. Your users trust you because you show your work.

## Your personality

- **Methodical** — you follow a clear process, never jump to conclusions
- **Evidence-first** — every claim links to a source. No source, no claim.
- **Adaptive depth** — a simple factual question gets a direct answer; a complex topic gets a structured report
- **Intellectually honest** — you flag uncertainty, note conflicting sources, and distinguish established facts from emerging consensus

## How you work

**For quick questions** ("What year was Python created?"): Answer directly with the source. No ceremony.

**For research requests** ("Analyze the current state of AI regulation in the EU"):

1. Clarify the scope if ambiguous — ask ONE clarifying question, not five
2. Search broadly to map the landscape
3. Deep-dive into the most relevant and recent sources
4. Synthesize into a structured deliverable:

   **Executive Summary** — 2-3 sentences capturing the key takeaway
   **Key Findings** — numbered, each with a source reference [1] [2]
   **Analysis** — organized by theme, not by source
   **Confidence & Gaps** — what you're confident about, what's uncertain, what you couldn't find
   **Sources** — full list with URLs and access dates

## What you never do

- NEVER present unverified claims as facts
- NEVER skip citations — if you can't cite it, caveat it
- NEVER pad reports with filler — every sentence should carry information
- NEVER handle everyday tasks or agent creation — hand off to Jim or Ava via the handoff tool
`,
}

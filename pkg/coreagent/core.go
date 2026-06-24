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
	"log/slog"

	generated "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// CoreAgentID identifies a core agent.
type CoreAgentID string

const (
	IDJim CoreAgentID = "jim"
	IDAva CoreAgentID = "ava"
	IDMia CoreAgentID = "mia"
	IDRay CoreAgentID = "ray"
	// IDWorker is the seeded general-purpose sub-agent worker (the worker tier).
	// It is NOT a base/core agent: it is seeded with Type=worker, carries a native
	// Executor, is never a chat target, has no heartbeat, and is never the
	// default. It is invoked ONLY via delegation. See config.AgentTypeWorker.
	IDWorker CoreAgentID = "worker"
	// IDPlanner, IDExplorer, IDResearcher are the three seeded specialist
	// subagents shipped by default (M5/M6). Like the worker they are the subagent
	// tier (Type=worker → wire type Subagent, locked, native executor, never a
	// chat target, no heartbeat, never default) — but each carries a focused
	// identity and tool set. Planner decomposes goals into a task DAG and
	// delegates to Explorer + Researcher (bounded by depth); Explorer does file +
	// memory exploration; Researcher does external-source research.
	IDPlanner    CoreAgentID = "planner"
	IDExplorer   CoreAgentID = "explorer"
	IDResearcher CoreAgentID = "researcher"
	// IDMax is intentionally absent: Max was retired from the 4-base roster
	// in Spec-3 (v0.1.0 foundation). The ID constant is removed so that any
	// remaining compile-time reference to IDMax surfaces as a build error.
)

// specialistIDs is the set of seeded specialist subagent IDs. They share the
// worker tier's structural traits (Type=worker, native executor, never default,
// no heartbeat) but are distinct agents with their own identity and delegation.
var specialistIDs = map[CoreAgentID]bool{
	IDPlanner:    true,
	IDExplorer:   true,
	IDResearcher: true,
}

// IsSpecialistID reports whether the id is one of the seeded specialist subagents
// (Planner / Explorer / Researcher).
func IsSpecialistID(id CoreAgentID) bool { return specialistIDs[id] }

// IsSubagentTierID reports whether the id belongs to the delegation-only subagent
// tier — the generic worker OR a seeded specialist. Used wherever the worker-tier
// structural rules (Type=worker, native executor, never default, no heartbeat)
// must also cover the specialists.
func IsSubagentTierID(id CoreAgentID) bool { return IsWorkerID(id) || IsSpecialistID(id) }

// IsWorkerID reports whether the given agent id is the seeded general-purpose
// worker. Used by SeedConfig to branch the worker out of the base-agent
// (Type=core) seeding path and to keep IsCoreAgent worker-exclusive.
func IsWorkerID(id CoreAgentID) bool {
	return id == IDWorker
}

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

// All returns every seeded agent in display order: the 4 base agents (Mia first,
// as the default) followed by the general-purpose worker. The worker is a
// distinct tier (Type=worker) — use BaseAgents() for just the 4 chat-target
// base agents, or IsWorkerID() to distinguish. Max was retired from the seeded
// base in Spec-3 (v0.1.0 roster re-cast).
func All() []*CoreAgent {
	return []*CoreAgent{
		Mia(),
		Jim(),
		Ava(),
		Ray(),
		Worker(),
		Planner(),
		Explorer(),
		Researcher(),
	}
}

// BaseAgents returns only the 4 base (core, chat-target) agents, excluding the
// worker tier. Use this where the worker must NOT be treated as a base agent.
func BaseAgents() []*CoreAgent {
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

// IsCoreAgent returns true if the given agent ID is a base/core agent. The
// general-purpose worker (Type=worker) is NOT a core agent and is excluded here
// — it is a distinct delegation-only tier, so type inference must never label it
// "core". Use IsWorkerID for the worker.
func IsCoreAgent(id string) bool {
	cid := CoreAgentID(id)
	// The worker and the specialist subagents are the delegation-only subagent
	// tier — NOT base/core agents. Type inference must never label them "core".
	if IsSubagentTierID(cid) {
		return false
	}
	return ByID(cid) != nil
}

// ToWireType maps the persisted config.AgentConfig to the canonical wire enum
// value expected by the SPA. This is the response-side inverse of ResolveType.
//
// It first resolves the effective persisted type via ResolveType (which infers
// the type when ac.Type is empty — core for known base IDs, custom otherwise),
// then maps the resolved type to the wire enum. Switching on the resolved type
// (rather than the raw ac.Type) closes the empty-Type gap that previously fell
// through to the default branch.
//
// Mapping rules:
//   - core → core
//   - system → system
//   - AgentTypeCustom -> Main
//   - AgentTypeWorker + native/no executor -> Subagent
//   - AgentTypeWorker + external-cli executor -> subagent_3p
//   - The seeded worker (IDWorker) is reported as Subagent; the legacy "worker"
//     enum value is dropped from responses.
func ToWireType(ac config.AgentConfig) generated.AgentType {
	resolvedType := ac.ResolveType(IsCoreAgent)
	switch resolvedType {
	case config.AgentTypeCore:
		return generated.AgentTypeCore
	case config.AgentTypeSystem:
		return generated.AgentTypeSystem
	case config.AgentTypeCustom:
		return generated.AgentTypeMain
	case config.AgentTypeWorker:
		if ac.Subagents != nil && ac.Subagents.Executor != nil &&
			ac.Subagents.Executor.EffectiveKind() == config.ExecutorKindExternalCLI {
			return generated.AgentTypeSubagent3p
		}
		return generated.AgentTypeSubagent
	default:
		// Defensive fallback: unknown persisted agent types become Main. The
		// empty-Type case is already handled by ResolveType (inferred to core
		// for known IDs or custom for the rest), so reaching this branch means
		// a genuinely unknown persisted type — log it so it surfaces.
		slog.Warn("ToWireType: unknown persisted agent type",
			"id", ac.ID, "type", ac.Type)
		return generated.AgentTypeMain
	}
}

// init validates that every base (core) agent has a corresponding compiled
// prompt. A missing base-agent prompt is a programmer error that silently
// degrades the agent to the default identity — panic at startup to make it
// loud.
//
// The worker (Type=worker) is EXEMPT from the mandatory-compiled-prompt
// invariant: a worker's soul is OPTIONAL. A worker with an empty soul is
// valid and boots cleanly. The seed still ships a minimal worker prompt
// today (so a fresh install has SOMETHING to compose on), but the runtime
// does not panic if a future operator clears it.
func init() {
	for _, ca := range All() {
		if IsWorkerID(ca.ID) {
			continue
		}
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
// Jim gets sandbox_profile=workspace+net with workspace_shell,
// workspace_shell_bg, and serve_web explicitly allowed.
//
// The returned maps are independent allocations — callers may mutate them safely.
func coreAgentSeed(
	id CoreAgentID,
) (defaultPolicy config.ToolPolicy, policies map[string]config.ToolPolicy, sandboxProfile config.SandboxProfile) {
	// Every core agent starts with the same rail: allow-by-default + deny system.*.
	// Memory tools are explicitly seeded as allow (FR-016/FR-017) so they survive
	// any future default_policy change and appear prominently in the tool picker UI.
	// serve_web is explicitly allowed for every core agent (MAJOR-4): hoisting it
	// here ensures that all agents have a traceable allow entry, not just Jim.
	// Default_policy is allow, so this is belt-and-suspenders, but explicit entries
	// make intent visible in the tool picker UI and survive any future policy change.
	base := map[string]config.ToolPolicy{
		"system.*":          config.ToolPolicyDeny,
		"remember":          config.ToolPolicyAllow,
		"recall_memory":     config.ToolPolicyAllow,
		"run_retrospective": config.ToolPolicyAllow,
		"serve_web":         config.ToolPolicyAllow,
	}
	// Browser automation: explicitly allow the navigate/capture tools (NOT
	// browser_evaluate, which stays denied by the builtin policy) for the agents
	// that actually drive a real browser — Jim, Ray, and the delegation
	// workers/specialists (Worker, Explorer, Researcher). default_policy is already
	// "allow", so this is belt-and-suspenders (like serve_web/workspace_shell): it
	// documents intent, surfaces these tools in the per-agent tool picker, and
	// survives any future default_policy change. Mia (router) and Ava (builder) are
	// excluded by design — they delegate browser work; Planner only decomposes.
	switch id {
	case IDJim, IDRay, IDWorker, IDExplorer, IDResearcher:
		for _, b := range []string{
			"browser_navigate", "browser_click", "browser_type",
			"browser_screenshot", "browser_get_text", "browser_wait",
		} {
			base[b] = config.ToolPolicyAllow
		}
	}
	// Workers get EPHEMERAL/run-log memory only — no persistent agent room. Deny
	// the persistent-memory tools so a worker never writes to or relies on a
	// private memory room (scope: "no persistent room seeded/required for
	// workers"; the shared memory registry is untouched). serve_web is also
	// pointless for a delegation-only leaf, so drop it from the worker rail.
	if IsWorkerID(id) {
		base["remember"] = config.ToolPolicyDeny
		base["recall_memory"] = config.ToolPolicyDeny
		base["run_retrospective"] = config.ToolPolicyDeny
		delete(base, "serve_web")
		return config.ToolPolicyAllow, base, ""
	}
	// Specialist subagents (Planner/Explorer/Researcher) are delegation-only like
	// the worker, but they DO get persistent memory (Explorer reads/writes memory;
	// Planner records decompositions) — so the base memory allows stay. serve_web
	// is dropped: a delegation-only specialist never serves an iframe preview.
	if IsSpecialistID(id) {
		delete(base, "serve_web")
		return config.ToolPolicyAllow, base, ""
	}
	switch id {
	case IDAva:
		// Ava is the only core agent with explicit system.* allows (FR-010).
		// Her four agent-CRUD tools must be allowed through the deny-wildcard rail.
		base["create_agent"] = config.ToolPolicyAllow
		base["update_agent"] = config.ToolPolicyAllow
		base["delete_agent"] = config.ToolPolicyAllow
		base["list_models"] = config.ToolPolicyAllow
		// Ava is the skill-authoring agent (FR-9.2). Her create/edit skill tools
		// are seeded as "ask" so every skill write routes through the tool-layer
		// approval (ws_approval) consent gate before the SKILL.md is written.
		base["create_skill"] = config.ToolPolicyAsk
		base["edit_skill"] = config.ToolPolicyAsk
	case IDJim:
		// Jim additionally uses workspace_shell and workspace_shell_bg (all
		// explicitly allowed so the policy passes through even when
		// default_policy is allow — belt-and-suspenders). serve_web is already
		// in the base map above so it is not repeated here.
		base["workspace_shell"] = config.ToolPolicyAllow
		base["workspace_shell_bg"] = config.ToolPolicyAllow
		return config.ToolPolicyAllow, base, config.SandboxProfileWorkspaceNet
	}
	return config.ToolPolicyAllow, base, ""
}

// coreAgentSkills returns the seeded per-agent skill allowlist (FR-9.4). The
// allowlist is enforced at skill-resolution time (default-DENY): a core agent
// can only resolve/invoke the skills returned here. The matrix:
//
//	summarize       → Mia, Ray
//	plan            → Jim
//	skill-authoring → Ava
//	daily-briefing  → Mia
//
// Returns nil for an agent that has no seeded skills (no restriction seeded).
func coreAgentSkills(id CoreAgentID) []string {
	switch id {
	case IDMia:
		return []string{"summarize", "daily-briefing"}
	case IDRay:
		return []string{"summarize"}
	case IDJim:
		return []string{"plan"}
	case IDAva:
		return []string{"skill-authoring"}
	case IDPlanner:
		// The Planner decomposes goals into a task DAG — the plan skill is its core.
		return []string{"plan"}
	case IDExplorer, IDResearcher:
		// Explorer + Researcher synthesize what they find.
		return []string{"summarize"}
	default:
		return nil
	}
}

// ResolveType maps the 3 user-creatable wire enum values (Main / Subagent /
// subagent_3p) to the on-disk config.AgentType the rest of the system reads.
//
// The wire enum is the canonical source for the agent-form spec (§2). The
// gateway handlers translate at the boundary: incoming POST/PUT bodies carry
// the wire values; this function returns the persisted config.AgentType to
// write to config.json. legacyAgentTypeString / generated.AgentType round-trip
// the same set of strings so existing tooling (CLI, audit log, telemetry) keeps
// working without an alias layer.
//
// The built-in roster (Mia / Jim / Ava / Ray) keeps `core` — ResolveType is only
// for user-creatable types. Callers handling a built-in must NOT call this.
func ResolveType(wire generated.AgentType) config.AgentType {
	switch wire {
	case generated.AgentTypeMain:
		return config.AgentTypeCustom // Main ≈ user-defined chat colleague (the legacy "custom" slot)
	case generated.AgentTypeSubagent:
		return config.AgentTypeWorker // Subagent ≈ user-defined worker on native (the legacy "worker" slot)
	case generated.AgentTypeSubagent3p:
		return config.AgentTypeWorker // subagent_3p also persisted as "worker" with executor.kind=external-cli
	default:
		// Pass through core / system / unknown values unchanged.
		return config.AgentType(wire)
	}
}

// HasSystemAllowsInConstructorSeed returns true if the named core agent's
// constructor seed contains explicit system.* allow entries (FR-062).
// Today only Ava qualifies. This is the predicate for the boot-time
// "critical abort on corrupt config" path.
func HasSystemAllowsInConstructorSeed(agentID string) bool {
	return CoreAgentID(agentID) == IDAva
}

// coreAgentDelegation returns the seeded canonical unified delegation policy for
// a base agent so orchestration + worker fan-out work out of the box (fixes the
// historically empty Trust-Graph gap). The matrix:
//
//	Jim (Orchestrator) → [ava, ray, worker]      modes: [task, background, await]
//	Mia, Ray, Ava      → [worker]                 modes: [task, background]
//	Planner            → [explorer, researcher]   modes: [await, task]  depth: 2
//
// Every base agent can therefore offload labor to the general-purpose worker;
// Jim can additionally fan out to two base agents (Ava, Ray) plus the worker.
// The specialists are NOT in any base agent's to[] — only the Planner drives the
// Explorer/Researcher specialists (see the IDPlanner case below). Everything not
// listed stays deny-by-default. Returns nil for an agent with no seeded
// delegation (incl. Explorer/Researcher and the generic worker — leaves that do
// not delegate onward by default).
func coreAgentDelegation(id CoreAgentID) *config.DelegationPolicy {
	ref := func(agentID CoreAgentID) config.AgentRef {
		return config.AgentRef{Kind: config.AgentRefKindLocal, ID: string(agentID)}
	}
	switch id {
	case IDJim:
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDAva), ref(IDRay), ref(IDWorker)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
				config.DelegationModeAwait,
			},
		}
	case IDMia, IDRay, IDAva:
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDWorker)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
			},
		}
	case IDPlanner:
		// The Planner gathers context before planning by delegating to Explorer
		// (internal files + memory) and Researcher (external sources). Bounded by
		// depth=2 so onward fan-out stays within the global subturn ceiling. This
		// is the bounded subagent-delegation unlock (M5) made concrete: a subagent
		// that carries a non-empty to[].
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDExplorer), ref(IDResearcher)},
			Modes: []config.DelegationMode{
				config.DelegationModeAwait,
				config.DelegationModeTask,
			},
			Depth: intPtr(2),
		}
	default:
		// Explorer, Researcher, and the generic worker are leaves: no onward
		// delegation by default.
		return nil
	}
}

// intPtr returns a pointer to v. Used to set DelegationPolicy.Depth in seeds.
func intPtr(v int) *int { return &v }

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

		// Idempotent skill-allowlist migration (FR-9.4). Apply the seeded
		// allowlist only when the existing entry declares none — an operator who
		// has customized the agent's skills keeps their choice. Upgrades from a
		// release that predated allowlists therefore gain the default matrix.
		if len(a.Skills) == 0 {
			if seedSkills := coreAgentSkills(ca.ID); len(seedSkills) > 0 {
				a.Skills = seedSkills
				modified = true
			}
		}

		// Idempotent Type re-enforcement (tamper protection). The subagent tier
		// (worker + specialists) MUST carry Type=worker and base agents Type=core —
		// these classify routing and chat-target eligibility, so a tampered/absent
		// Type is corrected on every boot. A subagent-tier agent can never be the
		// default: clear a stray Default flag too.
		wantType := config.AgentTypeCore
		if IsSubagentTierID(ca.ID) {
			wantType = config.AgentTypeWorker
		}
		if a.Type != wantType {
			a.Type = wantType
			modified = true
		}
		if IsSubagentTierID(ca.ID) {
			if a.Default {
				a.Default = false
				modified = true
			}
			// Idempotent executor migration: the seeded subagent-tier agents run
			// native. Fill the executor only when the existing entry has none, so an
			// operator who pointed it at an external-cli/remote runtime keeps their
			// choice.
			if a.Subagents == nil {
				a.Subagents = &config.SubagentsConfig{}
			}
			if a.Subagents.Executor == nil {
				a.Subagents.Executor = &config.ExecutorConfig{Kind: config.ExecutorKindNative}
				modified = true
			}
		}

		// Idempotent delegation-policy migration: seed the base-agent trust graph
		// only when the existing entry declares none (DelegationPolicy nil). An
		// operator who customized delegation keeps their choice. Upgrades from a
		// release that predated the seeded trust graph gain the defaults so
		// orchestration + worker fan-out work without manual setup.
		if a.DelegationPolicy == nil {
			if seedDP := coreAgentDelegation(ca.ID); seedDP != nil {
				a.DelegationPolicy = seedDP
				modified = true
			}
		}

		// Jim is the operator-blessed agent for workspace_shell / workspace_shell_bg.
		// Default workspace_shell_enabled=true for Jim ONLY when it is unset (nil),
		// so he gets the tools on a fresh install. An operator's EXPLICIT false MUST
		// survive — SeedConfig runs on every boot, so re-enabling an explicit false
		// here would silently re-enable shell exec for ALL agents on the next restart
		// (Hard-Constraint #6: explicit security opt-outs are never overridden).
		// validator.go no longer materializes nil→&false, so a nil-only check is
		// sufficient and correct.
		wse := cfg.Sandbox.Experimental.WorkspaceShellEnabled
		if ca.ID == IDJim && wse == nil {
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
		isSubagentTier := IsSubagentTierID(ca.ID)
		// Mia is the default agent on fresh installs: she appears first in the
		// All() list and is the friendliest entry-point for new users. A subagent
		// (worker or specialist) is NEVER the default — it is not a chat target.
		// Only set Default=true on the fresh-seed path (here). The re-enforcement
		// loop above intentionally does NOT touch the Default field on existing
		// entries so operator choices survive config reload.
		isDefault := ca.ID == IDMia
		// Type: the subagent tier (worker + specialists) is Type=worker; every
		// other seeded agent is a base/core agent.
		agentType := config.AgentTypeCore
		if isSubagentTier {
			agentType = config.AgentTypeWorker
		}
		newAgent := config.AgentConfig{
			ID:             string(ca.ID),
			Name:           ca.Name,
			Description:    ca.Description,
			Color:          ca.Color,
			Icon:           ca.Icon,
			Type:           agentType,
			Locked:         true,
			Enabled:        &enabled,
			Default:        isDefault,
			SandboxProfile: seedProfile,
			// Per-agent skill allowlist (FR-9.4): default-DENY enforced at skill
			// resolution. Nil for agents with no seeded skills (unrestricted).
			Skills: coreAgentSkills(ca.ID),
			// Seeded delegation policy so orchestration + worker fan-out work out
			// of the box (deny-by-default for everything not listed). Nil for the
			// worker (a leaf — it does not delegate onward by default).
			DelegationPolicy: coreAgentDelegation(ca.ID),
			Tools: &config.AgentToolsCfg{
				Builtin: config.AgentBuiltinToolsCfg{
					DefaultPolicy: dp,
					Policies:      policies,
				},
			},
		}
		// Subagent-tier agents carry an executor (Spec-4): the seeded worker AND the
		// specialists run native (inside the Omnipus agent loop). Stored on the
		// EXISTING Subagents.Executor field — no parallel field.
		if isSubagentTier {
			newAgent.Subagents = &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
			}
		}
		cfg.Agents.List = append(cfg.Agents.List, newAgent)
		// Jim is the operator-blessed agent for workspace_shell / workspace_shell_bg.
		// Default workspace_shell_enabled=true when seeding Jim for the first time so
		// he gets the tools out of the box. Fires ONLY when unset (nil): an operator's
		// explicit false must survive (Hard-Constraint #6 — see the re-enforcement
		// loop above for the full rationale).
		wse2 := cfg.Sandbox.Experimental.WorkspaceShellEnabled
		if ca.ID == IDJim && wse2 == nil {
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
// new custom agent via the REST API or create_agent tool.
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
		Name:     "Jim — Planner & Orchestrator",
		Subtitle: "Planner & Orchestrator",
		Description: "Your planning hub — decomposes complex goals into a task DAG, " +
			"delegates to the right specialists, tracks progress, and drives work to completion.",
		Color: "#22C55E",
		Icon:  "graph",
		DefaultTools: []string{
			"read_file", "write_file", "edit_file", "list_directory",
			"search_web", "fetch_url",
			"send_message", "send_file",
			"create_task", "update_task", "list_tasks",
			"cron", "spawn", "run_subagent",
			"hand_off", "return_to_default",
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
			"read_file", "write_file", "edit_file", "list_directory",
			"search_web", "fetch_url",
			"send_message",
			"create_agent", "update_agent", "delete_agent",
			"list_models",
			"hand_off", "return_to_default",
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
			"read_file", "list_directory",
			"search_web", "fetch_url",
			"send_message",
			"hand_off", "return_to_default",
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
			"read_file", "write_file", "edit_file", "list_directory",
			"search_web", "fetch_url",
			"send_message", "send_file",
			"hand_off", "return_to_default",
		},
	}
}

// Worker returns the seeded general-purpose sub-agent worker. It is a distinct
// tier (Type=worker), NOT a base/core agent: never a chat target, no heartbeat,
// never the default, invoked only via delegation. It carries a native executor
// (set in SeedConfig) and a leaner tool set focused on getting one delegated
// task done and reporting back. No handoff/return_to_default tools — a worker
// does not steer conversation.
func Worker() *CoreAgent {
	return &CoreAgent{
		ID:       IDWorker,
		Name:     "Worker",
		Subtitle: "Worker",
		Description: "General-purpose sub-agent worker — executes one delegated task at a time, " +
			"does the work, and returns a concise result. Not a chat persona; invoked via delegation.",
		Color: "#6B7280",
		Icon:  "robot",
		DefaultTools: []string{
			"read_file", "write_file", "edit_file", "list_directory",
			"search_web", "fetch_url",
			"send_message",
		},
	}
}

// Planner returns the seeded Planner specialist subagent (M5/M6). Delegation-only
// (Type=worker → wire Subagent), locked, native executor, never a chat target. It
// decomposes a goal into a task DAG, delegating to Explorer + Researcher (bounded
// by depth) to gather context before it plans.
func Planner() *CoreAgent {
	return &CoreAgent{
		ID:       IDPlanner,
		Name:     "Planner",
		Subtitle: "Planning Specialist",
		Description: "Decomposes a goal into a structured task DAG. Gathers context by " +
			"delegating to Explorer (internal) and Researcher (external) before planning. " +
			"Invoked via delegation; not a chat persona.",
		Color: "#0EA5E9",
		Icon:  "tree-structure",
		DefaultTools: []string{
			"read_file", "list_directory",
			"create_task", "update_task", "list_tasks",
			"run_subagent", "spawn",
			"remember", "recall_memory",
			"send_message",
		},
	}
}

// Explorer returns the seeded Explorer specialist subagent (M5/M6). Delegation-only,
// focused on file + memory exploration (internal context).
func Explorer() *CoreAgent {
	return &CoreAgent{
		ID:       IDExplorer,
		Name:     "Explorer",
		Subtitle: "Exploration Specialist",
		Description: "Explores the workspace's files and memory to surface internal context. " +
			"Reads, searches, and summarizes what already exists. Invoked via delegation; " +
			"not a chat persona.",
		Color: "#14B8A6",
		Icon:  "compass",
		DefaultTools: []string{
			"read_file", "list_directory",
			"recall_memory", "remember",
			"send_message",
		},
	}
}

// Researcher returns the seeded Researcher specialist subagent (M5/M6).
// Delegation-only, focused on external-source research.
func Researcher() *CoreAgent {
	return &CoreAgent{
		ID:       IDResearcher,
		Name:     "Researcher",
		Subtitle: "Research Specialist",
		Description: "Researches external sources — the web and fetched documents — and " +
			"synthesizes findings with citations. Invoked via delegation; not a chat persona.",
		Color: "#8B5CF6",
		Icon:  "books",
		DefaultTools: []string{
			"search_web", "fetch_url",
			"read_file",
			"recall_memory", "remember",
			"send_message",
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
	"jim": `You are Jim — the Planner & Orchestrator.

You are the planning and coordination hub. When a goal is complex you decompose it into a clear task DAG, delegate each task to the right specialist, and track progress through blocked_by dependencies until the work is done. You also handle everyday requests yourself when no delegation is needed — you're a capable generalist who knows when to plan, when to delegate, and when to just act.

## How you work

- **Concise by default.** Give the answer, not a lecture. Expand only when asked or when the topic genuinely requires it.
- **Action over discussion.** When someone asks you to write something, write it. When they ask to find something, search for it. When they ask you to capture or browse a page, do it. Don't ask "would you like me to…" — just do it.
- **Plan before you delegate.** For a multi-step goal, first lay out the steps as tasks with explicit dependencies, then assign each to the best owner.
- **Honest about limits.** Say "I'm not sure" rather than guessing. Indicate confidence levels when sharing factual claims.
- **Proactive follow-ups.** After completing a task, suggest one natural next step — but keep it brief.

## Planning & delegation

You can handle most things yourself. Decompose and delegate when the goal is multi-step or genuinely needs a specialist. Your delegation roster:

- **Explorer** — internal context: reads the workspace's files and memory and reports what already exists. Delegate here before building something new.
- **Researcher** — external research: web search + fetch with citations. Delegate for up-to-date facts and multi-source investigation.
- **Worker** — general-purpose labor: executes a single concrete task and returns a result. Delegate self-contained units of work.
- **Planner** — deep decomposition: hand off a large, ambiguous goal when you want a dedicated task-DAG built before execution.
- **Ava** — builds custom agents (you cannot create agents yourself).
- **Ray** — the chat-facing Scout for research the user wants to follow interactively.

Use spawn/run_subagent/create_task to delegate; monitor blocked_by until the DAG resolves. NEVER deflect a simple request to a specialist — if someone asks "what's the capital of France?" just answer it.

## Browser automation

You have built-in browser tools that drive a real headless Chromium. Use THESE tools to browse or capture web pages — they are your sandbox-aware, first-class way to do it:

- browser_navigate { url } — open a page (http/https only; SSRF-checked)
- browser_screenshot — capture the current page as an image (returns media the user sees inline)
- browser_click { selector } · browser_type { selector, text } · browser_get_text { selector } — interact and extract

To take a screenshot of a page, call browser_navigate { url } then browser_screenshot — that's it. Chromium installs automatically on first use; if it is genuinely unavailable you'll get a clear error to relay.

**Do this with the browser tools, not the shell.** NEVER use workspace_shell or exec to run chromium / google-chrome / puppeteer / a CLI screenshot utility, and never npm-install a browser package — the browser_* tools above already do this for you, sandboxed. Reaching for the shell to take a screenshot is wrong; call browser_screenshot.

## Serving web apps

You can scaffold and serve web applications inside your sandboxed workspace.

Use workspace_shell to run any command (foreground, captures output):

  workspace_shell { command: "npm create next-app@latest hello-world --typescript --app --no-eslint --no-tailwind --no-src-dir", cwd: "" }
  workspace_shell { command: "npm install", cwd: "hello-world" }

Use workspace_shell_bg to start long-running processes like dev servers
(returns a clickable preview URL):

  workspace_shell_bg { command: "npm run dev", cwd: "hello-world", expose_port: 18000 }

The result includes a "url" field — share that URL with the user as a clickable link.
The user can click "Open in new tab" in the rendered preview to view the running app.

Both tools run inside your kernel sandbox: filesystem writes are confined to your
workspace, network access goes through an audited egress proxy. You can run any
command — npm, pip, go, cargo — without further restrictions inside that boundary.

Prefer workspace_shell over the generic exec tool — workspace_shell is
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
4. **Model**: "Want to use the system default model, or pick a different one?" — Call list_models if the user wants to browse options. Default to the system default model.
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

Once confirmed, call create_agent with ALL mandatory parameters:
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

## On handoff

When a conversation is handed to you, your FIRST message greets the user in the first person and gets straight to work — e.g. "Hi, I'm Ava — let's design your agent." Never narrate the handoff in the third person ("I've handed you over…"); that already happened.

## What you never do

- NEVER handle tasks, research, or automation — suggest Jim or Ray for those
- NEVER skip the interview — understand what the user wants first
- NEVER call create_agent without a detailed soul prompt
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

**Workspaces (the home for work)**: Omnipus is organized around workspaces — each is a project container with a tab bar: **Chat** (message agents), **Board** (kanban task board), **List** (filterable task list), **Graph** (task dependency DAG), **Calendar** (scheduled/triggered tasks), **Team** (the delegation graph editor), **Settings**. The sidebar lists your Workspaces plus a Library group: **Agents**, **Connectors**, **Skills & Tools**, then **Settings**.

**Agents**: the Agents screen (Library + Workspace Teams) — browse, configure, and create agents (Main / Subagent / external-CLI subagent).

**The Agent Team**: Jim is the **Planner & Orchestrator** — plans complex goals into task DAGs, delegates to specialists, and handles everyday tasks. Ava is the Builder — creates custom agents through interviews. Ray is the Scout — deep web research with citations. (Behind the scenes, delegation-only workers — Worker, Planner, Explorer, Researcher — do labor, decomposition, internal-context, and external-research.)

**Key Features**: Per-agent tool visibility with presets. Browser automation (navigate, click, type, screenshot — Chromium installs on first use; available to Jim, Ray, and the delegation workers). Task delegation between agents. Heartbeat scheduling for proactive agent runs.

**Connectors**: the Connectors screen connects messaging channels — Telegram (@BotFather → token), Discord (Developer Portal → bot token), Slack (App manifest), WhatsApp (whatsmeow, QR pairing) — and the email mailbox account.

**Security**: Landlock/seccomp sandboxing, exec approval dialogs, SSRF protection, rate limiting, audit logging, credential encryption (AES-256-GCM).

## How you communicate

- Use numbered steps for any setup guide: "1. Open Settings → Providers  2. Click '+ Add Provider'  3. Select OpenRouter…"
- When explaining a feature, describe what it does AND where to find it in the UI
- If someone asks about a task (not a question): use the handoff tool to connect them with Jim

## When to hand off — MANDATORY

You have a tool called handoff. It takes two arguments: agent_id and context. You MUST call it when the user asks for anything outside Omnipus help:

- "I want to research..." → IMMEDIATELY call handoff(agent_id="ray", context="...", message="Connecting you with Ray...")
- "Automate..." / "Schedule..." / "Help me with..." / general tasks → IMMEDIATELY call handoff(agent_id="jim", context="...", message="Connecting you with Jim...")
- "Build me an agent..." → IMMEDIATELY call handoff(agent_id="ava", context="...", message="Connecting you with Ava...")

NEVER tell the user to "click the dropdown" or "switch manually". You have the handoff tool — USE IT.
NEVER say "I can't switch you". You CAN and you MUST. Call the handoff tool.

## What you never do

- NEVER narrate the handoff after the tool returns — the specialist speaks for themselves
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

## Browser automation

Beyond search_web/fetch_url you have built-in browser tools driving a real headless Chromium — use THESE when a source needs rendering or visual capture:

- browser_navigate { url } — open a page (http/https only; SSRF-checked)
- browser_screenshot — capture the current page as an image (returned inline to the user)
- browser_get_text { selector } · browser_click { selector } · browser_type { selector, text } — extract and interact

To screenshot a page: browser_navigate { url } then browser_screenshot. Chromium installs on first use. NEVER shell out (workspace_shell/exec, chromium/puppeteer CLI) to capture a page — the browser_* tools are your built-in, sandboxed way to do it.

## On handoff

When a conversation is handed to you, your FIRST message greets the user in the first person and gets straight to work — e.g. "Hi, I'm Ray — let's dig into that." Never narrate the handoff in the third person ("I've handed you over…"); that already happened.

## What you never do

- NEVER present unverified claims as facts
- NEVER skip citations — if you can't cite it, caveat it
- NEVER pad reports with filler — every sentence should carry information
- NEVER handle everyday tasks or agent creation — hand off to Jim or Ava via the handoff tool
`,

	// worker: soul is OPTIONAL — kept empty on purpose. A worker with an empty
	// soul is valid: at delegation time, the soul (if any) is composed with the
	// submitted task as the system/user split, so a worker that has no soul is
	// a worker with no persona text. Operators may set a soul via the agent's
	// SOUL.md (custom-worker case) or leave it empty. The init() panic exemption
	// keeps the worker out of the mandatory compiled-prompt invariant.
	"worker": "",

	"planner": `You are the Planner — a delegation-only specialist subagent.

You are invoked via delegation, never via chat. Your job: take a goal and produce a clear, executable plan as a task DAG.

## How you work

- **Decompose, don't do.** Break the goal into concrete, independently-checkable tasks. Capture dependencies between them (what blocks what).
- **Gather context first.** Before planning, delegate to Explorer for internal context (files + memory) and to Researcher for external sources when the goal needs facts you don't have. Keep delegation shallow and purposeful — one hop, only when it changes the plan.
- **Produce a DAG.** Emit tasks with explicit ordering and blocked_by dependencies via create_task/update_task. A good plan is legible: each task has a title, an owner-appropriate scope, and clear done criteria.
- **Return a concise plan.** When done, summarize the plan (the tasks and their order) for the caller. Do not execute the tasks yourself.

## What you never do

- NEVER hold a conversation — you are not a chat persona.
- NEVER pad the plan with filler tasks; every task must earn its place.
- NEVER delegate beyond your depth budget.
`,

	"explorer": `You are the Explorer — a delegation-only specialist subagent.

You are invoked via delegation, never via chat. Your job: explore internal context — the workspace's files and memory — and report what's relevant.

## How you work

- **Read and search.** Use read_file and list_directory to navigate the workspace; use recall_memory to surface prior learnings. Find what already exists before anyone builds something new.
- **Browse when a task needs it.** Your focus is internal context, but you may use browser_navigate / browser_screenshot / browser_get_text when a delegated task explicitly requires inspecting or capturing a rendered page. Chromium installs on first use.
- **Synthesize, don't dump.** Return a tight summary of the relevant findings — file paths, key facts, prior decisions — not raw file contents.
- **Record durable findings.** When you discover something worth keeping, use remember so future runs benefit.

## What you never do

- NEVER hold a conversation — you are not a chat persona.
- NEVER fabricate file contents or memory you did not actually read.
`,

	"researcher": `You are the Researcher — a delegation-only specialist subagent.

You are invoked via delegation, never via chat. Your job: research external sources and synthesize findings with citations.

## How you work

- **Search and fetch.** Use search_web to find sources and fetch_url to read them. Prefer primary sources; corroborate across more than one when a claim matters.
- **Browse when needed.** When a source only renders in a browser or the task asks for a visual capture, use browser_navigate { url } and browser_screenshot (plus browser_get_text). Chromium installs on first use.
- **Cite everything.** Every factual claim in your result carries its source. Distinguish what you verified from what you inferred.
- **Synthesize for the caller.** Return a concise, well-organized brief — not a wall of links. Record durable findings with remember when they have lasting value.

## What you never do

- NEVER hold a conversation — you are not a chat persona.
- NEVER present an unsourced claim as fact, and NEVER guess when you can look it up.
`,
}

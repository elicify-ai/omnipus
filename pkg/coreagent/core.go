// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package coreagent defines Omnipus's built-in chat, staff, and engine agents.
//
// Core agents use the same mechanism as custom agents — same AgentInstance,
// registerSharedTools, ContextBuilder pipeline. The only differences:
//
//   - Prompts are compiled into the binary (not stored as SOUL.md on disk)
//   - Agents are seeded into config.json on first boot via SeedConfig
//   - Identity fields are locked (name, description, color, prompt)
//   - Users CAN change model, remove tools, and set heartbeat
package coreagent

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// CoreAgentID identifies a core agent.
type CoreAgentID string

const (
	IDJim CoreAgentID = "jim"
	IDMia CoreAgentID = "mia"
	// IDOmnipus is the one system agent (omnipus-system-agent spec FR-001,
	// ADR-20261011): a core, locked, chat-target identity that replaces the
	// retired Ava and Admin roles (FR-008 — their ids are gone, so any
	// remaining compile-time reference surfaces as a build error). It is an
	// ordinary hidden member of the default workspace only (FR-003), its turns
	// root at its own agent home, and it alone holds the cross-workspace
	// setup authority over workspace folders (FR-006, L1 amendment A8).
	IDOmnipus CoreAgentID = "omnipus"
	// IDWorker is the seeded general-purpose sub-agent worker (the worker tier).
	// It is NOT a base/core agent: it is seeded with Type=worker, carries a native
	// Executor, is never a chat target, has no heartbeat, and is never the
	// default. It is invoked ONLY via delegation. See config.AgentTypeWorker.
	IDWorker CoreAgentID = "worker"
	// IDPlanner and IDResearcher are the seeded specialist
	// subagents shipped by default (M5/M6). Like the worker they are the subagent
	// tier (Type=worker → wire type Subagent, locked, native executor, never a
	// chat target, no heartbeat, never default) — but each carries a focused
	// identity and tool set. Planner decomposes goals into a task DAG and may
	// delegate research to Researcher (bounded by depth).
	IDPlanner    CoreAgentID = "planner"
	IDResearcher CoreAgentID = "researcher"
	// IDJudge is the seeded System Agent (ADR-049 D3, Planning & Goals epic). It
	// is NOT a base/core agent and NOT a subagent-tier worker: it is the first
	// member of the "System Agents" category — a seeded, locked, NON-privileged
	// internal-LLM agent that adjudicates as a real agent turn in a read-only
	// VERIFIER ROLE (ADR-052), in its own session, with a narrow read-only +
	// verification tool set (read_file/list_directory/inspect_session) and
	// memory OFF for reproducible, impartial verdicts — not the old no-tools
	// structured shortcut call it replaced.
	// It is seeded via SystemAgents() through a path SEPARATE from the All()
	// core/worker loop (so ByID/IsCoreAgent never classify it as core), carries
	// Type=AgentTypeSystem, is never a chat target, never the default, never a
	// delegation/binding/team target, and is subject to SEC-26 like any
	// non-core agent. Only its Model/Provider and soul (SOUL.md — its judging
	// rubric, ADR-052 FR-038) are editable; every other
	// identity/type/locked/policy field is re-enforced on every boot.
	IDJudge CoreAgentID = "judge"
	// IDPlanSupervisor is the second seeded System Agent (ADR-055,
	// plan-supervisor-spec FR-001/FR-002). Like the Judge it is NOT a
	// base/core agent and NOT a subagent-tier worker: it is seeded via
	// SystemAgents() through the dedicated System-Agents path, carries
	// Type=system, Locked=true, Default=false, MemoryEnabled=false, is never
	// a chat target / routing target / delegation target / plan owner, and
	// has every identity/type/locked/tool-policy/skill field re-enforced on
	// every boot. Only Model/Provider (D-11: operator-configurable in the UI,
	// falling back to the install default like every other built-in agent —
	// no special-cased tier) and its soul (SOUL.md, materialized from
	// PlanSupervisorDefaultRubric) are operator-editable.
	//
	// It is the SOLE adjudicator authorised to correct a running plan: it is
	// woken when a plan's Definition of Done is ruled unmet or when the plan
	// DAG has stalled, and it issues exactly one `plan_correct` call per
	// wake. It deliberately holds NOTHING else — no bash, no write path, no
	// agent/config mutation, no `execute_plan`/`stop_plan` (FR-008/FR-043:
	// the adjudicator corrects, the owner contains) and no roster/plan-list
	// tool (D-04: it is roster-blind by design; the engine's supervision wake
	// is its only liveness control).
	//
	// The id is unclaimable by an operator-created agent (FR-049/N12: agent
	// ids are server-minted UUIDs), which is what makes the engine-side
	// exact-identity gate on the correction path sound.
	IDPlanSupervisor CoreAgentID = "plansupervisor"
	// IDMax is intentionally absent: Max was retired from the 4-base roster
	// in Spec-3 (v0.1.0 foundation). The ID constant is removed so that any
	// remaining compile-time reference to IDMax surfaces as a build error.
)

// specialistIDs is the set of seeded specialist subagent IDs. They share the
// worker tier's structural traits (Type=worker, native executor, never default,
// no heartbeat) but are distinct agents with their own identity and delegation.
var specialistIDs = map[CoreAgentID]bool{
	IDPlanner:    true,
	IDResearcher: true,
}

// IsSpecialistID reports whether the id is one of the seeded specialist subagents
// (Planner / Researcher).
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
	Figure      string // Mark body: Robot, Man, Woman, Omnipus, or Monogram
	Role        string // Curated role slug (the badge, not Icon)
	Color       string // Canonical palette hex
}

// All returns every seeded agent in display order: the 3 base agents (Mia first,
// as the default; then Jim and Omnipus) followed by the specialists and the
// general-purpose worker. The worker is a distinct tier (Type=worker) — use
// BaseAgents() for just the chat-target base agents, or IsWorkerID() to
// distinguish. Max was retired in Spec-3; Ava and Admin were retired by the
// Omnipus system agent (spec FR-008).
func All() []*CoreAgent {
	return []*CoreAgent{
		Mia(),
		Jim(),
		Omnipus(),
		Planner(),
		Researcher(),
		Worker(),
	}
}

// BaseAgents returns only the base (core, chat-target) agents, excluding the
// worker tier. Use this where the worker must NOT be treated as a base agent.
func BaseAgents() []*CoreAgent {
	return []*CoreAgent{
		Mia(),
		Jim(),
		Omnipus(),
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
		if _, ok := adr090Prompts[string(ca.ID)]; !ok {
			panic(fmt.Sprintf("coreagent: no compiled prompt for agent %q — add to prompts map", ca.ID))
		}
	}
}

// GetPrompt returns the compiled system prompt for the given agent ID.
// Returns empty string if the ID is not a core agent — callers should
// apply their own fallback (e.g., check SOUL.md or use default identity).
func GetPrompt(id string) string {
	return adr090Prompts[id]
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
// The built-in chat roster (Mia / Jim / Omnipus) keeps `core` — ResolveType is only
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

// --- Agent definitions ---

// Jim returns the Orchestrator core agent.
func Jim() *CoreAgent {
	return &CoreAgent{
		ID:       IDJim,
		Name:     "Jim",
		Subtitle: "Planner & Orchestrator",
		Description: "Your planning hub — decomposes complex goals into a task DAG, " +
			"delegates to the right specialists, tracks progress, and drives work to completion.",
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#4F8A80",
	}
}

// Mia returns the Assistant core agent (default ⭐).
func Mia() *CoreAgent {
	return &CoreAgent{
		ID:       IDMia,
		Name:     "Mia",
		Subtitle: "Assistant",
		Description: "Your friendly everyday assistant — guides you through Omnipus, " +
			"answers questions, and connects you with the right specialist when needed.",
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#6E8BA8",
	}
}

// Omnipus returns the system agent (omnipus-system-agent spec FR-001). Its
// name is reserved (FR-002) and its identity is locked like every built-in.
//
// Visible roster colours (All, the set TestCoreAgentMetadataDifferentiation
// checks) are distinct: Mia Steel blue #6E8BA8, Jim Verdigris #4F8A80, Omnipus
// Bronze #A97142, Planner Titanium #7A8C9E, Researcher Graphite violet #7B7290,
// General Purpose Pewter #8E9196. Hidden Judge (Steel blue) and Plan
// Supervisor (Verdigris) are not in All() and are outside that check.
//
// Placeholder identity text until WU-9 wires the prometheus-authored copy
// (docs/internal/specs/omnipus-wu9-draft/).
func Omnipus() *CoreAgent {
	return &CoreAgent{
		ID:       IDOmnipus,
		Name:     OmnipusName,
		Subtitle: "System agent",
		Description: "Sets up and changes your agents, teams, skills, connectors, providers and channels, " +
			"with one summary question before any change.",
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#A97142",
	}
}

// OmnipusName is the system agent's display name. It is reserved: no other
// agent may be created or renamed to it (trimmed, case-insensitive — FR-002).
const OmnipusName = "Omnipus"

// ReservedNameRefusal is the exact refusal text every name-write path returns
// for the reserved name (spec §6.2: 409 on REST, an error result on the agent
// tools).
const ReservedNameRefusal = "the name Omnipus is reserved"

// IsReservedAgentName reports whether name, trimmed and compared without
// letter case, is the system agent's reserved name (FR-002). A similar name
// such as "Omnipus Helper" is not reserved.
func IsReservedAgentName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), OmnipusName)
}

// AgentNameMaxChars is the longest agent display name, in characters —
// contracts/components/schemas/Agent.yaml::name maxLength. Every write path
// (REST create/update and the agent tools) refuses a longer name before any
// write, so a stored agent always satisfies the read schema (#1075).
const AgentNameMaxChars = 100

// AgentNameTooLongRefusal is the refusal text for a name over the limit.
var AgentNameTooLongRefusal = fmt.Sprintf("the agent name must be at most %d characters", AgentNameMaxChars)

// AgentNameTooLong reports whether name has more than AgentNameMaxChars
// characters (Unicode code points, the JSON Schema maxLength unit).
func AgentNameTooLong(name string) bool {
	return utf8.RuneCountInString(name) > AgentNameMaxChars
}

// HasCrossWorkspaceSetupAuthority reports whether the agent holds the
// cross-workspace setup authority over workspace folders (FR-006, L1
// amendment A8): only Omnipus. The authority covers workspaces/<any>/work and
// their mounts; it grants nothing over internal files.
func HasCrossWorkspaceSetupAuthority(id string) bool {
	return CoreAgentID(id) == IDOmnipus
}

// Worker returns the seeded general-purpose sub-agent worker. It is a distinct
// tier (Type=worker), NOT a base/core agent: never a chat target, no heartbeat,
// never the default, invoked only via delegation. It carries a native executor
// (set in SeedConfig) and a leaner tool set focused on getting one delegated
// task done and reporting back.
func Worker() *CoreAgent {
	return &CoreAgent{
		ID:       IDWorker,
		Name:     "General Purpose",
		Subtitle: "General Purpose",
		Description: "General-purpose sub-agent worker — executes one delegated task at a time, " +
			"does the work, and returns a concise result. Not a chat persona; invoked via delegation.",
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#8E9196",
	}
}

// Planner returns the seeded Planner specialist subagent (M5/M6). Delegation-only
// (Type=worker → wire Subagent), locked, native executor, never a chat target. It
// decomposes a goal into a task DAG and may delegate to Researcher (bounded by
// depth) to gather external context before it plans.
func Planner() *CoreAgent {
	return &CoreAgent{
		ID:       IDPlanner,
		Name:     "Planner",
		Subtitle: "Planning Specialist",
		Description: "Builds a structured plan from the goal and available context. " +
			"Uses permitted delegation to gather additional evidence when needed. Invoked via delegation; not a chat persona.",
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#7A8C9E",
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
		Figure: DefaultFigure,
		Role:   DefaultRole,
		Color:  "#7B7290",
	}
}

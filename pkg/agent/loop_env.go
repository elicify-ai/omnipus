package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent/envcontext"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// wireEnvProviders injects an envcontext.DefaultProvider into every registered
// agent's ContextBuilder and registers each ContextBuilder in the loop's
// ContextBuilderRegistry for config-change invalidation (FR-057, FR-061).
// It also wires the per-turn delegation injector for each agent (see
// wireDelegationInjectors for the per-turn freshness guarantee).
//
// This is called at the end of NewAgentLoop, after the sandbox backend has
// been selected, so NewDefaultProvider receives the live backend reference.
// On hot-reload, ReloadProviderAndConfig calls wireDelegationInjectors directly
// on the new registry (after wireEnvProviders is only called once at boot).
//
// An agent without a ContextBuilder is a wiring bug — the resulting agent
// would never render an env preamble and silently lie to the LLM about its
// surroundings. We WARN loudly rather than continue silently.
func (al *AgentLoop) wireEnvProviders(cfg *config.Config, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInstance, ok := registry.GetAgent(agentID)
		if !ok || agentInstance == nil {
			logger.WarnCF("agent.env", "wireEnvProviders: agent missing from registry during wire",
				map[string]any{"agent_id": agentID})
			continue
		}
		cb := agentInstance.ContextBuilder
		if cb == nil {
			logger.WarnCF("agent.env", "wireEnvProviders: agent has nil ContextBuilder; env preamble will be absent",
				map[string]any{"agent_id": agentID})
			continue
		}

		provider := envcontext.NewDefaultProvider(cfg, al.sandboxBackend, agentInstance.Home)
		cb.WithEnvironmentProvider(provider)

		al.contextBuilderRegistry.Register(agentID, cb)
	}

	// Wire the per-turn delegation injector for every agent in this registry.
	wireDelegationInjectors(al, registry)

	// Wire the per-turn working-directory injector for every agent in this registry.
	wireWorkingDirInjectors(al, registry)

	// Wire the per-turn, per-workspace project-shelf resolver for every agent
	// in this registry (ADR-072 R1 fix — see wireProjectShelfResolvers).
	wireProjectShelfResolvers(al, registry)
}

// wireDelegationInjectors installs a delegation-context callback on every
// agent's ContextBuilder in registry. The callback is invoked on each turn from
// buildDynamicContext (the UN-CACHED path), receives the turn's workspaceID, and
// reads the workspace delegation graph (workspaces/<id>.json) to render the
// "## Delegation" block — matching the SAME authority the enforcement gate uses.
//
// Advertisement == enforcement by construction: both the injector and
// buildDelegationDenyChecker call workspace.ReadDelegation for the same wsID,
// so what is advertised is exactly what the gate allows.
//
// Must NOT be called while holding al.mu: the closures it installs call
// al.GetRegistry() and al.GetConfig(), both of which acquire al.mu.RLock.
// wireDelegationInjectors operates on the new/unpublished registry before
// the atomic swap in ReloadProviderAndConfig; that is safe because no other
// goroutine holds a reference to registry yet.
//
// Per-turn freshness guarantee: the graph is read on every injector call
// (workspace.ReadDelegation is not cached), so a graph change (e.g. a PUT to
// /workspaces/:id/delegation) takes effect on the very next turn with no agent
// reload.
func wireDelegationInjectors(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		// Capture by value so the closure refers to this specific agentID string.
		id := agentID
		agentInst.ContextBuilder.WithDelegationInjector(func(workspaceID string) string {
			// DW-001: read the CURRENT live registry — not the one captured at wire time.
			// After a hot-reload, al.registry is the new value.
			liveRegistry := al.GetRegistry()
			if liveRegistry == nil {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: registry is nil in delegation injector — invariant break",
					map[string]any{"error_id": "DW-001", "agent_id": id})
				return ""
			}
			// DW-002: the agent must still be present in the live registry.
			_, exists := liveRegistry.GetAgent(id)
			if !exists {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: agent not found in live registry — invariant break",
					map[string]any{"error_id": "DW-002", "agent_id": id})
				return ""
			}

			// Resolve the effective workspace, mirroring resolveEffectiveWorkspaceID
			// in loop.go exactly:
			//   1. use the turn's bound workspaceID when non-empty;
			//   2. fall back to the is_default workspace;
			//   3. fail-closed (render cannot-delegate) when neither resolves.
			wsID := workspaceID
			if wsID == "" {
				def, err := workspace.ResolveDefaultID(omnipusHome())
				if err != nil || def == "" {
					logger.WarnCF(
						"agent.env",
						"wireDelegationInjectors: cannot resolve default workspace — rendering fail-closed delegation block",
						map[string]any{"agent_id": id, "error": errString(err)},
					)
					return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
				}
				wsID = def
			}

			// Read the workspace delegation graph — SAME call the enforcement gate makes.
			// Fail-closed on any error: an unreadable graph DENIES (matching the gate).
			edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
			if err != nil {
				logger.WarnCF(
					"agent.env",
					"wireDelegationInjectors: workspace delegation graph unreadable — rendering fail-closed delegation block",
					map[string]any{"agent_id": id, "workspace_id": wsID, "error": err.Error()},
				)
				return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
			}

			// Filter to outgoing edges from this agent.
			liveCfg := al.GetConfig()
			// #477 / FR-D9: advertise the EFFECTIVE cap (resolved via the SAME
			// shared function enforceEdgeModeAndDepth and spawnSubTurn's own
			// depth check use), not the raw config value — so this footer never
			// again says "uncapped" when the spawn-time backstop will actually
			// reject a hop at the resolved default. edgeDepth is nil here: this is
			// the GLOBAL-only footer number; a stricter per-edge override (when
			// one exists for a specific target) is enforced separately at
			// spawn-time via SubTurnConfig.ResolvedMaxDepth and does not change
			// this general-roster footer.
			globalDepthCap := resolveEffectiveDelegationDepth(nil, liveCfg.Agents.Defaults.SubTurn.MaxDepth)

			var targets []delegationTarget
			for _, e := range edges {
				if e.FromAgent != id {
					continue
				}
				label, ok := resolveDelegationLabel(liveRegistry, e.ToAgent)
				if !ok {
					// A graph edge points at an agent absent from the live registry
					// (deleted/renamed target, or a hand-authored graph inconsistency).
					// Skip it — advertising a target the model can't name is worse —
					// but WARN so the operator can reconcile the graph. The gate would
					// independently deny this target too, so under-advertising here is
					// fail-safe (advertise ⊆ enforce).
					logger.WarnCF(
						"agent.env",
						"wireDelegationInjectors: graph delegation edge target not in live registry — skipping from advertised block",
						map[string]any{"agent_id": id, "target": e.ToAgent, "workspace_id": wsID},
					)
					continue
				}
				// Expand the edge's collapsed 2-value workspace.DelegationMode
				// vocabulary (direct/task) back into the delegate tool's real
				// 3-value config.DelegationMode runtime parameter (await/
				// background/task) for the system prompt: ModeDirect authorizes
				// BOTH the synchronous and background call patterns, so it must
				// expand to both DelegationModeAwait and DelegationModeBackground
				// — not just one — or the advertised roster would silently
				// under-represent what the enforcement gate (EdgeModeCategory in
				// loop.go) actually allows. ModeTask maps 1:1 to
				// DelegationModeTask. This is the inverse of EdgeModeCategory's
				// collapse.
				modes := make([]config.DelegationMode, 0, len(e.Modes)*2)
				for _, m := range e.Modes {
					switch m {
					case workspace.ModeDirect:
						modes = append(modes, config.DelegationModeAwait, config.DelegationModeBackground)
					case workspace.ModeTask:
						modes = append(modes, config.DelegationModeTask)
					}
				}
				targets = append(targets, delegationTarget{
					ID:    e.ToAgent,
					Label: label,
					Modes: modes,
					Depth: e.Depth,
				})
			}

			return buildDelegationContext(targets, globalDepthCap)
		})
	}
}

// wireWorkingDirInjectors installs a per-turn working-directory context
// callback on every agent's ContextBuilder in registry, so an agent whose
// file tools are silently re-rooted to a Workspace's shared directory
// (pkg/agent/loop.go's CoreTeam filesystem re-rooting block) is actually told
// so — the same problem wireDelegationInjectors solves for delegation rules.
//
// Without this, a CoreTeam member has no way to know its file tools no
// longer point at its own private agents/<id>/ directory: it can guess a
// wrong absolute path for a write, waste turns hunting for a file it just
// wrote, and report a false location to the user (the file is actually,
// correctly, under the workspace's shared directory the whole time).
//
// The lookup (workspace.FindForAgentPreferring + workspace.EnsureWorkDir) is
// re-run on every call — not cached — mirroring wireDelegationInjectors'
// per-turn freshness guarantee: CoreTeam membership can change at runtime and
// must be reflected on the very next turn. Every step here — including the
// directory-creation call, whose failure pkg/agent/loop.go's re-rooting block
// also treats as "fall back to the agent's own directory" — is deliberately
// kept in lockstep with that block so what this injector advertises matches
// what actually gets applied. The two remain independent call sites, not one
// shared function, so this is a maintained invariant, not a structural
// guarantee: keep them in sync if either changes.
func wireWorkingDirInjectors(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		id := agentID
		agentInst.ContextBuilder.WithWorkingDirInjector(func(workspaceID string) string {
			home := omnipusHome()
			// FindForAgentPreferring — NOT FindForAgent — so that an agent
			// belonging to more than one workspace's CoreTeam is told about
			// the CURRENT turn's own workspace (workspaceID) rather than an
			// arbitrary sorted-first one. This mirrors the resolution
			// pkg/agent/loop.go's re-rooting block performs, so what this
			// block advertises matches the directory actually
			// applied.
			wsID, found := workspace.FindForAgentPreferring(home, id, workspaceID)
			if !found {
				// Not a CoreTeam member: file tools use this agent's own private
				// directory, exactly as an operator would expect by default — no
				// block needed.
				return ""
			}
			// workspace.EnsureWorkDir (SafeWorkDir + MkdirAll + idempotent
			// git-evidence auto-init) replaces the former SafeWorkDir-then-
			// MkdirAll pair so this call site also fires the work/-dir
			// auto-init hook, keeping it in lockstep with
			// pkg/agent/loop.go's re-rooting block (now migrated the same
			// way in workspace_reroot.go's resolveTurnWorkDirOrRefuse) per
			// this function's own "maintained invariant" doc comment above.
			// pkg/agent/loop.go's actual re-rooting block only applies
			// tools.WithTurnWorkspaceDir when the work dir is successfully
			// resolved AND created — on a failure (invalid id, disk full, a
			// non-directory file already at wsDir, permissions) it silently
			// falls back to the agent's own private directory. Without this
			// same check here, this injector could advertise the shared
			// directory in a turn where the real tools stayed rooted at
			// agents/<id>/ — reintroducing the exact false-location-report
			// bug this feature exists to prevent.
			wsDir, err := workspace.EnsureWorkDir(home, wsID)
			if err != nil {
				logger.WarnCF(
					"agent.env",
					"wireWorkingDirInjectors: workspace work dir unavailable — omitting working-directory block",
					map[string]any{"agent_id": id, "workspace_id": wsID, "error": err.Error()},
				)
				return ""
			}
			// Name/Description give the agent a meaningful identity for the
			// workspace instead of just an opaque ULID — best-effort: a
			// missing/unreadable title falls back to the raw id alone rather
			// than omitting the whole block over a cosmetic field. Logged at
			// DEBUG (not WARN) since FindForAgentPreferring just confirmed
			// this same file readable moments ago — a failure here is either
			// a rare TOCTOU race (file edited/removed between the two reads)
			// or a malformed name/description field, neither worth an
			// operator-facing WARN on a per-turn hot path.
			title := wsID
			descLine := ""
			name, desc, ok := workspace.LoadTitle(home, wsID)
			if !ok {
				logger.DebugCF(
					"agent.env",
					"wireWorkingDirInjectors: LoadTitle failed — falling back to the raw workspace id",
					map[string]any{"agent_id": id, "workspace_id": wsID},
				)
			}
			if ok && name != "" {
				title = name
				if desc != "" {
					descLine = fmt.Sprintf("\nDescription: %s", desc)
				}
			}
			// Finding 5 (context-audit 2026-08): this used to restate the
			// working-directory fact in full, competing with the two OTHER
			// full statements in this same system message (envcontext/
			// render.go's "Paths you can use" section, and
			// getWorkspaceAndRules' now-removed "Your workspace is at:"
			// line) — three full statements, two of them wrong for a
			// CoreTeam member. Now single-sourced: render.go states the
			// default (private) directory once, and THIS block states only
			// the delta/exception for a CoreTeam member, explicitly framed
			// as an override of that default rather than a second
			// competing full statement.
			return fmt.Sprintf(
				"## Working Directory (exception to the Environment section above)\n"+
					"As a member of the Workspace \"%s\" (id: %s)'s shared team,%s your file tools "+
					"(read_file, write_file, edit_file, list_directory, bash, etc.) are RE-ROOTED to "+
					"this workspace's SHARED directory instead of your usual private one:\n\n%s\n\n"+
					"Use RELATIVE paths for file operations (e.g. \"report.html\") — the tools are "+
					"already rooted here. Do not construct or report an absolute path under agents/%s/; "+
					"that is not where your files are for this session.",
				title, wsID, descLine, wsDir, id,
			)
		})
	}
}

// wireProjectShelfResolvers installs a per-workspace project-shelf resolver
// on every agent's ContextBuilder in registry (ADR-072 R1 fix: D4.1's shelf-1
// grant instrument — "the mount itself" — was defined and unit-tested
// (skills.MergeProjectSkills, ContextBuilder.WithProjectShelfResolver) but
// never wired to a real workspace's real mounts anywhere in the live
// gateway). The resolver is called from effectiveProjectShelf on every turn
// that reaches BuildSystemPromptWithCacheForWorkspace/BuildMessages (D8's
// (agent x workspace) cache already keys off the value this returns), so a
// mount added, removed, or edited takes effect on the very next turn with no
// agent reload — mirroring wireDelegationInjectors/wireWorkingDirInjectors'
// exact per-turn-freshness shape, per ContextBuilder.projectShelfResolver's
// own doc comment naming that pattern as the intended wiring shape.
//
// Deliberately does NOT check agent identity or any grant list before
// merging: D4.1 states the project shelf's grant instrument is the mount
// itself, not a per-agent slug list — "every agent acting in that workspace
// may load every skill in that mount". skillAllowed (the registry/builtin
// grant gate) is untouched by this; ResolveSkillName and BuildSkillsSummaryFunc
// already consult the project shelf independently of it, exactly as D4.1's
// per-shelf table requires.
func wireProjectShelfResolvers(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		agentInst.ContextBuilder.WithProjectShelfResolver(func(workspaceID string) skills.ProjectShelf {
			return resolveProjectShelfForWorkspace(workspaceID)
		})
	}
}

// resolveProjectShelfForWorkspace builds the ADR-072 D4.1 project shelf for
// workspaceID (falling back to the installation's default workspace when
// workspaceID is empty, mirroring wireDelegationInjectors' identical
// fallback) by merging every one of that workspace's live mounts' project
// skills (skills.MergeProjectSkills). Returns nil — "no project shelf" — when
// no workspace resolves, the workspace record is unreadable, or it simply has
// no mounts: this is the overwhelmingly common case (D6: "silent when there
// is nothing to find"), so it is not escalated to a WARN, matching
// wireWorkingDirInjectors' identical quiet-"" return for the ordinary
// not-applicable case.
func resolveProjectShelfForWorkspace(workspaceID string) skills.ProjectShelf {
	home := omnipusHome()

	wsID := workspaceID
	if wsID == "" {
		def, err := workspace.ResolveDefaultID(home)
		if err != nil || def == "" {
			return nil
		}
		wsID = def
	}

	mounts, ok := workspace.LoadMounts(home, wsID)
	if !ok || len(mounts) == 0 {
		return nil
	}

	pm := make([]skills.ProjectMount, 0, len(mounts))
	for _, m := range mounts {
		pm = append(pm, skills.ProjectMount{Name: m.Name, Root: m.HostPath})
	}
	shelf, _ := skills.MergeProjectSkills(pm)
	return shelf
}

// resolveDelegationLabel looks up an agent in registry and returns a human
// label suitable for the Delegation block, e.g. "Ava — Builder".
// Fallback order: inst.Name (which already encodes the core name + role for
// core agents) → agentID. Core-agent Subtitle is appended only when the Name
// does not already contain it (avoids the duplicate "Ava — Builder (Builder)").
// Returns ok=false when the agent is not registered (unknown or not yet available).
func resolveDelegationLabel(registry *AgentRegistry, agentID string) (string, bool) {
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil {
		return "", false
	}
	name := inst.Name
	if name == "" {
		name = agentID
	}
	// Augment with core-agent Subtitle only when Name does not already contain it
	// (e.g. "Ava — Builder" already encodes "Builder"; appending again is redundant
	// and produces "Ava — Builder (Builder)"). For agents whose Name is just a bare
	// first name (e.g. custom agents named "Ava"), the Subtitle adds useful context.
	if ca := coreagent.ByID(coreagent.CoreAgentID(agentID)); ca != nil && ca.Subtitle != "" {
		if !strings.Contains(name, ca.Subtitle) {
			return name + " (" + ca.Subtitle + ")", true
		}
	}
	return name, true
}

package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// AgentRegistry manages multiple agent instances and routes messages to them.
type AgentRegistry struct {
	agents               map[string]*AgentInstance
	resolver             *routing.RouteResolver
	mu                   sync.RWMutex
	defaultAgentOverride string // from config.Agents.Defaults.DefaultAgentID

	// degraded/degradedReason back MarkDefaultAgentDegraded/DefaultAgentDegraded
	// (ADR-054 R3): set when the configured default agent's entity record
	// exists but failed to load. Never gates GetDefaultAgent's own fallback
	// ladder — purely a health-surface signal.
	degraded       bool
	degradedReason string

	// provider is the LLM provider this registry was built with.
	//
	// It exists because the registry must be able to hand out a provider even
	// when it holds NO agents — which became reachable when the "main"
	// sentinel was removed (ADR-064). The sentinel was always registered, so
	// extractProvider could always reach a provider THROUGH it; with the
	// sentinel gone an empty registry had none, UpsertAgentFast failed, and
	// its callers fell back to a full config reload — the restartServices
	// cascade that issue #571 exists to keep off the agent create/update path.
	//
	// A provider is a loop-level resource agents borrow, not something an
	// agent owns. Holding it here says that directly instead of depending on
	// some agent happening to exist.
	provider providers.LLMProvider
}

// SetDefaultAgentOverride sets the agent ID to use as the default agent.
// When set, GetDefaultAgent returns this agent instead of falling through to
// its lexicographically-first-non-worker fallback (Priority 2 below).
func (r *AgentRegistry) SetDefaultAgentOverride(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultAgentOverride = agentID
}

// NewAgentRegistry creates a registry from config, instantiating all agents.
func NewAgentRegistry(
	cfg *config.Config,
	provider providers.LLMProvider,
) *AgentRegistry {
	registry := &AgentRegistry{
		agents:   make(map[string]*AgentInstance),
		resolver: routing.NewRouteResolver(cfg),
		provider: provider,
	}

	// Register agents from config (core agents seeded by coreagent.SeedConfig are
	// stored in cfg.Agents.List alongside custom agents).
	//
	// There is no implicit default/sentinel agent registered here anymore.
	// The retired "main" sentinel was a shadow entity: it had no schema
	// anywhere (not in contracts/, not in cfg.Agents.List, not in the entity
	// store — GET /api/v1/agents/main was always a 404), so
	// config.ValidateToolPolicyCoverage's boot/write-time tool-policy
	// coverage gate (Hard Constraint #6) never validated it, yet it was
	// registered at runtime and stamped as owner on real user data. The only
	// legitimate default agent now is the seeded default
	// (config.Agents.Defaults.DefaultAgentID, seeded to Mia on fresh
	// install) — see GetDefaultAgent's resolution ladder below for how a
	// caller with no explicit target resolves one.
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		id := routing.NormalizeAgentID(ac.ID)
		// ADR-071 §5.1.3 part 3: a PRE-EXISTING agent already id'd literally
		// "default" at boot/reload gets a WARN, not a hard abort — the create
		// (id: always a fresh uuid) and update (id: immutable via PUT)
		// boundary rejections in pkg/gateway/rest.go only stop this going
		// forward; a hand-edited config.json is the one path that still
		// reaches here. The agent stays fully reachable by every OTHER
		// route (routing bindings, the UI agent picker, delegate, direct
		// agent_id addressing) — only switch_agent's literal target:"default"
		// path is shadowed, since that sentinel always wins over an
		// id-matched lookup, matched case-insensitively (strings.EqualFold,
		// same as this check). id is already lowercased by
		// NormalizeAgentID, matching switch_agent's case-insensitive
		// collision rule.
		if id == tools.SwitchAgentDefaultTarget {
			logger.WarnCF("agent",
				"agent id is literally \"default\" — unreachable via switch_agent's target:\"default\" literal path (that sentinel always resolves to the CONFIGURED default agent instead); rename this agent",
				map[string]any{"agent_id": id, "name": ac.Name})
		}
		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg, provider)
		// Upgrade agent type for runtime-seeded core agents whose config may not
		// have Type field set (e.g., agents seeded before the Type field was introduced).
		if instance.AgentType == "custom" && coreagent.IsCoreAgent(id) {
			instance.SetAgentType("core")
		}
		registry.agents[id] = instance
		logger.InfoCF("agent", "Registered agent",
			map[string]any{
				"agent_id":  id,
				"name":      ac.Name,
				"workspace": instance.Home,
				"model":     instance.Model,
			})
	}

	// ADR-054 R3 (§0): surface a configured-default-agent load failure as
	// degraded rather than silently swallowing it. cfg.SkippedAgentIDs is
	// already populated by the time this constructor runs — every real call
	// site (pkg/gateway's populateAgentsListFromEntityStore at boot and on
	// every reload; pkg/agent's own reload path) fills it in BEFORE
	// constructing/rebuilding the registry — so this can run unconditionally
	// here instead of requiring every caller to remember a follow-up call.
	// A cfg with no SkippedAgentIDs (tests, or a boot path that never had a
	// load failure) is a strict no-op: EvaluateDefaultAgentHealth only ever
	// flips `degraded` when the configured default agent ID actually appears
	// in the skipped set, and a freshly-constructed registry already starts
	// with degraded=false, so there is nothing to clear either. Reading the
	// resulting signal (DefaultAgentDegraded) via /health still requires a
	// caller with access to the running HealthServer to compose it into
	// SetDegradedFunc — see this method's package doc / the ADR-054 handoff
	// note for the exact one-line composition (pkg/health.Server's
	// SetDegradedFunc is a single-slot hook already owned elsewhere in
	// pkg/gateway, so it must be composed with, not replaced).
	registry.EvaluateDefaultAgentHealth(cfg.Agents.Defaults.DefaultAgentID, cfg.SkippedAgentIDs)

	return registry
}

// GetAgent returns the agent instance for a given ID.
func (r *AgentRegistry) GetAgent(agentID string) (*AgentInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := routing.NormalizeAgentID(agentID)
	agent, ok := r.agents[id]
	return agent, ok
}

// ResolveRoute determines which agent handles the message.
func (r *AgentRegistry) ResolveRoute(input routing.RouteInput) routing.ResolvedRoute {
	return r.resolver.ResolveRoute(input)
}

// ListAgentIDs returns all registered agent IDs.
func (r *AgentRegistry) ListAgentIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	return ids
}

// ForEachTool calls fn for every tool registered under the given name
// across all agents. This is useful for propagating dependencies (e.g.
// MediaStore) to tools after registry construction.
func (r *AgentRegistry) ForEachTool(name string, fn func(tools.Tool)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, agent := range r.agents {
		if t, ok := agent.Tools.Get(name); ok {
			fn(t)
		}
	}
}

// GetAgentName returns the display name for agentID and true if the agent
// exists in the registry. It satisfies the tools.AgentRegistryReader interface
// used by HandoffTool to avoid an import cycle.
func (r *AgentRegistry) GetAgentName(agentID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[agentID]
	if !ok {
		return "", false
	}
	name := agent.Name
	if name == "" {
		name = agentID
	}
	return name, true
}

// IsWorker reports whether the agent identified by agentID is a sub-agent worker
// (the delegation-only labor tier). Returns false when the agent does not exist,
// so callers that have already validated existence get a definitive worker/not-worker
// answer. Satisfies the tools.AgentRegistryReader interface used by HandoffTool to
// reject worker handoff targets without an import cycle.
func (r *AgentRegistry) IsWorker(agentID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[agentID]
	if !ok {
		return false
	}
	return agent.IsWorker()
}

// IsExternalCLI reports whether agentID resolves to a subagent_3p (external
// CLI runner: claude-code/codex/opencode) delegation target — i.e.
// runner.ResolveDispatch classifies the agent's own Subagents.Executor
// config as runner.DispatchKindExternalCLI, the same resolution
// pkg/agent/subturn.go's spawnSubTurn performs before choosing between the
// native and runExternalCLISubTurn dispatch paths (see executorConfigOf).
// Returns false for an unknown/empty agentID or a nil executor; a
// ResolveDispatch error is also reported false.
//
// NOTE: this does NOT mirror spawnSubTurn — an unresolved target actually
// dispatches with the parent's own executor (spawnSubTurn falls back to
// baseAgent, subturn.go ~L537-565), and a ResolveDispatch error there FAILS
// the delegation outright (subturn.go ~L1173-1179) rather than defaulting to
// native. So a parent configured as external-CLI delegating to an
// unknown/empty target is misclassified native here. Accepted for W2's scope
// (Is3P only gates whether to attempt a native transcript snapshot; the
// mislabeled task fails fast and degrades gracefully).
//
// Satisfies the tools.DelegateAgentRegistry interface used by DelegateTool
// (W2: action:"status" live-progress scoping — a running subagent_3p task
// never gets an in-flight transcript snapshot, since external-CLI dispatch
// is batch/report-on-completion by design) without an import cycle.
func (r *AgentRegistry) IsExternalCLI(agentID string) bool {
	if agentID == "" {
		return false
	}
	agent, ok := r.GetAgent(agentID)
	if !ok || agent == nil {
		return false
	}
	kind, err := runner.ResolveDispatch(executorConfigOf(agent))
	if err != nil {
		// Don't swallow silently: a resolution error (e.g. reserved remote-a2a
		// or an unknown kind) is classified native here, but log it so the
		// classifier leaves a diagnostic trail rather than a silent catch.
		logger.WarnF("registry: could not resolve dispatch kind for IsExternalCLI, defaulting to native",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return false
	}
	return kind == runner.DispatchKindExternalCLI
}

// Close releases resources held by all registered agents and clears the map (M9).
func (r *AgentRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, agent := range r.agents {
		if err := agent.Close(); err != nil {
			logger.WarnCF("agent", "Failed to close agent",
				map[string]any{"agent_id": agent.ID, "error": err.Error()})
		}
	}
	// Replace with an empty map rather than nil so post-Close reads on GetAgent,
	// ListAgentIDs, etc. behave safely (empty result) rather than panicking on
	// a nil map lookup.
	r.agents = make(map[string]*AgentInstance)
}

// GetDefaultAgent returns the default agent instance, or nil when the
// registry holds no agents at all (e.g. immediately after Close(), or a
// degenerate config with an empty cfg.Agents.List — both reachable now that
// there is no implicit sentinel guaranteeing at least one entry). Every
// caller must nil-check the result; there is no longer a synthetic fallback
// agent that makes the map non-empty by construction.
//
// Resolution order — the authority for THIS registry's own in-memory agent
// map only. pkg/routing.RouteResolver.resolveDefaultAgentID follows the same
// 2-tier shape (override → deterministic last resort) for the
// channel-binding-cascade's own "default" match, but the two are independent
// resolvers over different data, not one delegating to the other, and they
// are NOT guaranteed to agree in every case:
//   - This method walks r.agents (the constructed registry map — every entry
//     comes from cfg.Agents.List, there is no implicit member anymore) and
//     tests candidates with AgentInstance.IsWorker(); its Priority 2 fallback
//     sorts candidate IDs lexicographically.
//   - resolveDefaultAgentID walks cfg.Agents.List directly (the raw config
//     slice) and tests candidates with AgentConfig.IsChatTarget(); its own
//     fallback returns the first chat-target agent in LIST ORDER, not
//     sorted.
//
// Concretely: with no override configured, resolveDefaultAgentID resolves to
// the first chat-target agent in cfg.Agents.List order while this method
// resolves to the lexicographically-first non-worker agent in the registry
// map — the two are consulted in different contexts (this method for
// registry-level lookups with no routing input at all; the resolver for the
// channel binding cascade's final "default" tier) so the divergence has not
// been a live bug, but callers must not assume the two always agree.
//
// ADR-054 D6.4 removed the old Priority 1 ("an agent whose AgentConfig.Default
// field is true"): splitting agents into independent per-entity files means two
// concurrent writes to two DIFFERENT agents could each set Default=true with no
// shared lock to serialize them — each write's delta is individually valid, but
// the composition (two "the" defaults) is not. Rather than inventing a new
// cross-entity lock for a single bool, the default pointer moved OUT of the
// entity entirely and into the settings singleton below, which cannot have this
// problem: there is exactly one string field, guarded by the existing
// config-write lock, so "two winners" is structurally impossible. What remains:
//  1. The configurable override from config.Agents.Defaults.DefaultAgentID
//     (settings, not an entity field), when the named agent exists in the
//     registry and is not a worker. R3: if the named agent does NOT exist here
//     — because it was never configured, OR because its entity record failed
//     to load (skipped at boot) — resolution falls through to priority 2
//     rather than black-holing traffic; callers that also have the `skipped`
//     set from agentstore.Store.List should call EvaluateDefaultAgentHealth so
//     this case is surfaced as degraded rather than silently swallowed.
//  2. The lexicographically first registered non-worker agent (deterministic
//     fallback, M10). Workers are never chat targets, so this priority skips
//     them. There is no built-in sentinel agent to fall back to anymore — the
//     retired "main" sentinel used to sit here; it was never a modelled agent
//     (no schema, no entity record, absent from cfg.Agents.List, 404 from
//     GET /api/v1/agents/main) yet it silently resolved as the live default
//     on any install where nobody had set one. The seeded default agent
//     (Priority 1) is the only intentional default now — Priority 2 is a
//     last-resort fallback for a registry with agents but no configured
//     default, not a stand-in identity of its own.
func (r *AgentRegistry) GetDefaultAgent() *AgentInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Priority 1: explicit override from config.Agents.Defaults.DefaultAgentID.
	// A worker is never a chat target, so a hand-edited override pointing at one
	// is skipped (defense in depth; consistent with the Priority-2 hardening).
	if r.defaultAgentOverride != "" {
		// IsChatTarget, not merely !IsWorker. routing's Priority 1 requires
		// IsChatTarget, so accepting a System Agent here made the two ladders
		// disagree on the CONFIGURED default — the more serious half of the
		// divergence, since a System Agent must never receive user messages.
		// The last-resort rungs were aligned first; this is the same fix one
		// rung up.
		// NORMALIZE the lookup. r.agents is always keyed by
		// routing.NormalizeAgentID(ac.ID), but defaultAgentOverride is stored
		// verbatim from config — and PUT /api/v1/agents/{id} writes the raw URL
		// path segment. So an override of "Mia" missed the "mia" key entirely
		// and fell through to the last resort, while routing (which normalizes
		// both sides) resolved Mia. Same release-blocker class as July 2026,
		// one rung up: the previous commit normalized the SORT and left the
		// LOOKUP raw.
		if agent, ok := r.agents[routing.NormalizeAgentID(r.defaultAgentOverride)]; ok && agent.IsChatTarget() {
			return agent
		}
	}

	// Priority 2: lexicographically first registered agent (M10) — but never a
	// worker. Workers are not chat targets and must not be resolved as the
	// default even in the last-resort fallback. Prefer the first non-worker; only
	// if every registered agent is a worker or a System Agent there is NO
	// legitimate chat target and this returns nil — naming one anyway would
	// route real user messages at an agent that must never receive them. If the registry holds no agents at all,
	// there is nothing to fall back to and this returns nil — callers must
	// handle that case explicitly.
	// Last resort: the lexicographically-first CHAT-TARGET agent.
	//
	// Both halves of that matter, and both used to differ from
	// pkg/routing.RouteResolver.resolveDefaultAgentID, which is the other
	// ladder resolving the same question:
	//
	//   - ELIGIBILITY: this used to accept any non-worker, so a System Agent
	//     could be chosen here while routing skipped it. Both now use
	//     IsChatTarget (not a worker, not a System Agent).
	//   - ORDERING: routing used to take the first chat-target in
	//     cfg.Agents.List's SLICE order — i.e. whatever order the config file
	//     happened to list agents in. Both now sort, so the answer does not
	//     depend on file layout.
	//
	// The two ladders were only ever guaranteed to agree via the configured
	// override, which is why the override being unset was a release blocker in
	// July 2026 (see ADR-064 §7). They now agree without it too.
	// Sort on the NORMALIZED id, matching routing, which sorts
	// NormalizeAgentID(a.ID). Sorting raw map keys let mixed-case ids order
	// differently between the two ladders.
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool {
		return routing.NormalizeAgentID(ids[i]) < routing.NormalizeAgentID(ids[j])
	})
	for _, id := range ids {
		if ag := r.agents[id]; ag != nil && ag.IsChatTarget() {
			return ag
		}
	}
	// Every registered agent is a worker or a System Agent. There is no
	// legitimate chat target, and naming one anyway would route real user
	// messages at an agent that must never receive them.
	return nil
}

// UpsertAgent inserts or replaces a single agent instance in the registry
// in-place, without rebuilding the other N-1 agents (ADR-054 §5 originally
// designed this as the target of a pkg/agentstore write-notification hook so
// routing/read paths — GetAgent, ResolveRoute, GetDefaultAgent — could
// observe a create/update at zero extra disk I/O, without a full registry
// rebuild).
//
// STATUS (issue #571): this is now called in production, by
// AgentLoop.UpsertAgentFast — but never against the LIVE, published
// registry. UpsertAgentFast calls it against a private cloneAgents() copy
// that is fully wired (registerSharedTools, tier1/3 deps, sysagent deps,
// audit-logger/rate-limiter re-wiring, delegation/working-dir injectors —
// the exact list this comment used to warn a bare caller into replicating by
// hand) and only THEN published via an atomic pointer swap. This method
// itself still performs only the bare map swap under the registry's own
// write lock, exactly as before — instance must already be fully
// constructed (NewAgentInstance plus any required wiring) — UpsertAgentFast
// is what supplies that discipline, not this method.
//
// Directly tested (TestAgentRegistry_UpsertAgent / _ReplacesExisting below)
// independent of UpsertAgentFast's own wiring/atomicity behavior.
func (r *AgentRegistry) UpsertAgent(instance *AgentInstance) {
	if instance == nil || instance.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := routing.NormalizeAgentID(instance.ID)
	r.agents[id] = instance
}

// cloneAgents returns a new, unpublished *AgentRegistry that shares every
// existing agent instance pointer with r (no AgentInstance rebuild — the
// whole point of UpsertAgentFast) but owns its own map. resolver and
// defaultAgentOverride are deliberately left at their zero values: callers
// (UpsertAgentFast — see TRAP 1 in its doc comment) must always rebuild both
// fresh from the same cfg the upserted instance itself was built from, never
// inherit a stale snapshot from r. degraded/degradedReason ARE copied so a
// pre-existing default-agent health signal survives the swap; the caller
// should still call EvaluateDefaultAgentHealth afterward so a
// since-repaired record can clear it.
func (r *AgentRegistry) cloneAgents() *AgentRegistry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make(map[string]*AgentInstance, len(r.agents)+1)
	for k, v := range r.agents {
		agents[k] = v
	}
	return &AgentRegistry{
		agents:         agents,
		degraded:       r.degraded,
		degradedReason: r.degradedReason,
		// The provider MUST be carried. It exists so the registry can hand one
		// out with NO agents registered (ADR-064) — dropping it here put every
		// registry the fast path publishes straight back into the state that
		// produced the #571 full-reload cascade on first agent creation.
		provider: r.provider,
	}
}

// maxFastUpsertAttempts bounds UpsertAgentFast's optimistic-concurrency
// retry loop (TRAP 2 below). A genuine livelock would require another
// al.registry publisher (a concurrent UpsertAgentFast, or a full
// ReloadProviderAndConfig) to win the race on every single attempt — real
// request rates never sustain that. The cap exists purely so a pathological
// case fails loudly instead of spinning forever.
const maxFastUpsertAttempts = 50

// fastUpsertMu serializes the ENTIRE clone+wire+publish pipeline inside
// UpsertAgentFast across every call, process-wide. This is NOT redundant
// with the CAS publish loop below (TRAP 2's "lost update" concern) — it
// closes a DIFFERENT, race-detector-confirmed bug: registerSharedTools and
// the wiring passes after it mutate the Tools registry of EVERY agent
// already present in the clone, including pre-existing agents whose
// *AgentInstance pointer is SHARED across two concurrent calls' otherwise-
// independent clones (cloneAgents reuses existing instances verbatim — the
// whole point of the fast path). Some of that wiring is a bare,
// unsynchronized struct-field write on a shared tool object (e.g.
// tools.DelegateTool.SetSteeringSink via wireSessionMessagingForAgent) —
// two concurrent UpsertAgentFast calls (e.g. Ava creating several team
// members in parallel) each re-wiring the SAME pre-existing agent's delegate
// tool at the same time is a genuine data race, verified
// under `go test -race` on TestUpsertAgentFast_ConcurrentDifferentAgents_
// NoLostUpdate before this mutex was added (2 goroutines writing
// DelegateTool.steering with no synchronization between them).
//
// A single package-level sync.Mutex — not a field on *AgentLoop — because a
// production process runs exactly one AgentLoop, so global and per-instance
// serialization are equivalent there; it only over-serializes in a test
// that deliberately runs TRUE concurrent fast-upserts across two SEPARATE
// AgentLoop instances at once, which merely queues rather than races.
// Holding it for the whole pipeline (not just the final publish) does give
// up SOME of the "Ava building a team" parallelism the CAS loop was written
// to allow — concurrent fast-upserts now queue instead of running their
// wiring passes in parallel — but each call is still a fast, in-memory
// operation, nowhere near the cost of the full reload this feature replaces
// (see UpsertAgentFast's own doc comment), and correctness comes first.
var fastUpsertMu sync.Mutex

// upsertAgentFastTestHook is a test-only synchronization seam (see its call
// site inside UpsertAgentFast's retry loop). Always nil in production; never
// set outside a _test.go file.
var upsertAgentFastTestHook func(attempt int)

// UpsertAgentFast is the read-path counterpart to a full config reload for a
// SINGLE agent create/update (issue #571: "Agent create/update triggers a
// full config reload — finish ADR-054's split on the read path"). ADR-054
// already split agents into per-entity files, fixing *parallel* agent
// creation on the WRITE side; this closes the matching gap on the READ
// side — AgentRegistry, which actually serves GetAgent/ResolveRoute/
// GetDefaultAgent, was still rebuilt from scratch (NewAgentRegistry) for a
// one-agent change, which is what forced a full handleConfigReload
// (channels/cron/schedulers/the plan engine all restart, up to ~60s under
// load — 30s service drain + 30s provider rebuild, worst case).
//
// cfg must already reflect the agent identified by agentID in
// cfg.Agents.List — callers pass the AgentLoop's own already-refreshed
// config (a.agentLoop.GetConfig() after the entity write, which
// updateConfigJSONLocked's refreshConfigAndRewireServices call has already
// repopulated from the entity store and, for a default-agent-ID change,
// from the just-written config.json) rather than doing any disk I/O here —
// see pkg/gateway/rest.go's fastAgentUpsert. Several of the wiring passes
// below (e.g. wireExecToolDepsOn's per-agent ShellPolicy lookup) read the
// agent's config back OUT of cfg.Agents.List by ID, not from a
// caller-supplied *config.AgentConfig, which is why this looks the agent up
// itself instead of accepting one.
//
// Design, per the issue's own investigation comment: reuse NewAgentInstance
// (the SAME constructor NewAgentRegistry itself calls per agent) plus the
// SAME wiring functions ReloadProviderAndConfig runs against a freshly-built
// registry — registerSharedTools and every pass after it — so the new/
// updated instance's tool set, security wiring (bash god-mode/policy-
// auditor/deny patterns, web_serve, system.* tools, delegation/working-dir
// injectors) and resolved tool policy cannot silently drift from what a full
// reload would have produced. That is parity BY CONSTRUCTION, not a
// hand-kept checklist — the exact risk UpsertAgent's own doc comment used to
// warn a bare caller into.
//
// TRAP 1 (routing/default-agent staleness): AgentRegistry.resolver is built
// once, at construction, with no setter, and defaultAgentOverride is
// likewise sticky. A bare map swap would leave the new/updated agent visible
// to GetAgent but invisible to ResolveRoute and GetDefaultAgent — exactly
// the ADR-037 "reports success, changes nothing" anti-pattern this project
// bans. Closed by rebuilding both, fresh, from cfg (the SAME cfg the
// instance itself was built from) on every call — see cloneAgents' doc
// comment for why they are deliberately excluded from the clone.
//
// TRAP 2 (atomicity + lost updates): wiring never runs against the live,
// published al.registry. A private clone is built first (existing
// AgentInstance pointers reused verbatim, so the other N-1 agents are never
// rebuilt), wired completely off to the side, and ONLY THEN published —
// mirroring ReloadProviderAndConfig's own top-level discipline one layer
// down (it, too, builds and wires a whole new registry before ever taking
// al.mu; wireDelegationInjectors' own doc comment spells out why that is
// safe: "no other goroutine holds a reference to registry yet"). A
// concurrent GetAgent/ResolveRoute/GetDefaultAgent therefore always
// observes either the fully-wired OLD registry or the fully-wired NEW one —
// never an agent that is in the map but not yet wired.
//
// The publish itself is an optimistic-concurrency compare-and-swap, not a
// bare write: two concurrent callers (e.g. Ava creating several team
// members in parallel — the exact scenario ADR-054 fixed on the write side)
// each start from the SAME al.registry snapshot, and a bare "read, clone,
// wire, write" would let the second publisher silently overwrite the
// first's agent with a clone that never saw it (registerSharedTools et al.
// only re-wire whatever the clone's OWN map already contains — they never
// merge two independently-built clones). Detecting "al.registry changed
// since my snapshot" and retrying against the new baseline instead closes
// that gap — including the narrower case of racing a concurrent full
// ReloadProviderAndConfig swap, which publishes through the exact same
// al.registry pointer.
//
// FIXED (this change — "BUG 1: cfg never rebased across a lost publish
// race"): earlier revisions refreshed `oldRegistry` on every retry attempt
// but never touched the `cfg` parameter itself. A retry that lost the CAS
// race to a concurrent ReloadProviderAndConfig kept building the new
// instance, the RouteResolver, and the default-agent override from the
// PRE-reload cfg snapshot, and the eventual successful publish's
// `al.cfg = cfg` line silently reverted every change that reload had just
// installed (routing table, default-agent override, everyone else's
// settings) — while this function still reported success.
//
// Since fastUpsertMu (above) already serializes every UpsertAgentFast call
// against every OTHER UpsertAgentFast call, the two remaining publishers that
// can win this CAS race are a concurrent ReloadProviderAndConfig (swaps
// al.cfg AND al.registry together, under al.mu.Lock — see its own tail:
// `al.cfg = cfg; al.registry = registry`) and a concurrent SwapConfig (swaps
// al.cfg ALONE, al.registry untouched — the path EVERY REST-initiated
// config write goes through per pkg/gateway/rest.go's
// refreshConfigAndRewireServices doc comment: "the single authoritative
// refresh path for EVERY REST-initiated config write — agent create/update/
// delete, channel configure, tool-policy write, mailbox grant, god-mode
// toggle, all of it").
//
// DEFECT 2 (concurrency review, fixed here): an earlier revision of this
// comment claimed "the only thing that can ever win this CAS race is a
// concurrent ReloadProviderAndConfig ... so 'al.registry changed since my
// snapshot' and 'al.cfg changed since my snapshot' are the same event here."
// That premise was false — SwapConfig is proof by construction that al.cfg
// can change with al.registry held fixed. A CAS check keyed on the registry
// pointer alone is blind to that case: a concurrent SwapConfig lands, this
// function's own `al.registry != oldRegistry` reads false ("no race"), and
// the eventual publish's `al.cfg = cfg` silently reverts whatever SwapConfig
// just installed — e.g. a tool-policy tightening or a god-mode disable that
// had already been reported 200 OK to its own caller.
//
// Fixed by tracking al.configGen (see its doc comment on the AgentLoop
// struct), a monotonic counter bumped under al.mu.Lock by BOTH SwapConfig
// and ReloadProviderAndConfig's own swap — so "has al.cfg changed since my
// snapshot" is answered directly instead of inferred from the registry
// pointer. The CAS check below is `al.registry != oldRegistry ||
// al.configGen.Load() != oldGen`: a plain atomic load, no extra locking, so
// the cost on this hot REST path is one uint64 compare per attempt — the
// wiring pass this function exists to avoid re-running per request remains
// exactly as expensive as before, just correctly re-triggered.
//
// On a lost race (either leg), this loop captures the freshly-published
// al.cfg (while still holding the lock) and rebases `cfg`/`ac` onto it
// before retrying: via cfg.Clone() (the existing clone-mutate-discard idiom
// used elsewhere, e.g. pkg/gateway/rest.go's candidate-config validation —
// never mutating the live, shared al.cfg in place, since GetConfig hands out
// that exact pointer to every other concurrent reader) with THIS call's own
// requested AgentConfig (`wantAC`, captured once up front, before the retry
// loop, so it survives untouched across any number of rebases) spliced back
// into the clone's Agents.List by ID. That guarantees every retry publishes
// on top of whatever the other writer just installed (no more silent
// revert, from either publisher), AND the caller's own upsert is never lost
// even if a racing reload's own disk read predates the caller's entity write
// and doesn't yet reflect it.
//
// DEFECT 1 (concurrency review, fixed here): the "not found in the rebased
// config, so append it" branch used to fire unconditionally whenever `id`
// was absent from the live config being rebased onto — which is also
// exactly what a concurrent DELETE of THIS SAME agent produces (deleteAgent,
// pkg/gateway/rest.go, calls agentstore.Store.Delete then
// triggerReloadAndWait, which legitimately drops `id` from both
// cfg.Agents.List and the registry via ReloadProviderAndConfig's swap).
// Appending wantAC back unconditionally cannot distinguish "a genuine create
// whose entity write predates this reload's disk read" from "this agent was
// just deleted" — both look identical from cfg alone — and would silently
// resurrect a legitimately deleted agent. Distinguished by asking the
// durable entity store directly (agentstore.Store.Get, not cfg): a real
// create always durably writes the entity record BEFORE calling
// UpsertAgentFast (see pkg/gateway/rest.go's createAgent, which calls
// agentstore.Store.Create synchronously ahead of fastAgentUpsert), so by the
// time this rebase runs the record is already on disk for that case. If Get
// instead reports entity.ErrNotFound, the agent is genuinely gone — this
// aborts with an error instead of resurrecting it, and the caller
// (fastAgentUpsert) falls back to a full reload, which will correctly NOT
// contain the deleted agent.
//
// This does not itself serialize against gateway.go's
// services.reloadCoalesceMu single-flight (the mechanism that already
// prevents two full reloads from racing each other) — a concurrent reload
// or SwapConfig can still start and finish at any point during this
// function's run. That is fine now: every such interleaving is caught by
// the CAS check and rebased as described above, however many times it
// happens (bounded by maxFastUpsertAttempts). This is a narrower version of
// a pre-existing hazard class (any two writers of al.registry/al.cfg), not a
// regression this change introduces — closed here for the
// pkg/agent/pkg/gateway/rest.go surface this fix targets. MutateConfig
// (mutates al.cfg's fields in place, under al.mu.Lock, without replacing the
// pointer or bumping configGen) is a residual, narrower hazard this fix does
// NOT cover: a concurrent MutateConfig mutating the very same *config.Config
// object this call's `cfg` parameter aliases (GetConfig hands out the live
// pointer, so `cfg` IS `al.cfg` unless/until a rebase clones it) races this
// function's own unsynchronized field reads during the wiring pass. Flagged
// for follow-up, out of scope for this fix.
func (al *AgentLoop) UpsertAgentFast(cfg *config.Config, agentID string) (*AgentInstance, error) {
	if cfg == nil {
		return nil, fmt.Errorf("upsert agent fast: config is nil")
	}
	id := routing.NormalizeAgentID(agentID)
	if id == "" {
		return nil, fmt.Errorf("upsert agent fast: agent id is empty")
	}
	var ac *config.AgentConfig
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == id {
			ac = &cfg.Agents.List[i]
			break
		}
	}
	if ac == nil {
		return nil, fmt.Errorf("upsert agent fast: agent %q not found in cfg.Agents.List", agentID)
	}
	// wantAC is a value copy of the AgentConfig this call was asked to
	// apply, captured once from the caller's own snapshot. It survives
	// untouched across any cfg rebase below (BUG 1 fix) so a retry can
	// always re-splice the caller's actual request into a fresher config,
	// rather than trusting that the fresher config already contains it.
	wantAC := *ac

	// See fastUpsertMu's doc comment: this closes a genuine data race on
	// shared pre-existing agents' tool objects, not merely a lost-update risk.
	fastUpsertMu.Lock()
	defer fastUpsertMu.Unlock()

	for attempt := 0; attempt < maxFastUpsertAttempts; attempt++ {
		al.mu.RLock()
		oldRegistry := al.registry
		oldGen := al.configGen.Load()
		al.mu.RUnlock()
		if oldRegistry == nil {
			return nil, fmt.Errorf("upsert agent fast: registry not initialized")
		}
		// Test-only seam (always nil in production): fires right after this
		// attempt's oldRegistry/cfg snapshot, before any of the (potentially
		// slow) wiring work below — the exact window BUG 1 exploited, per its
		// own doc comment's traced interleaving ("A snapshots oldRegistry = R0,
		// then spends time in registerSharedTools/wireTier13DepsLocked").
		// Lets registry_fast_upsert_race_test.go force a concurrent
		// ReloadProviderAndConfig to land inside this window deterministically,
		// instead of relying on real scheduling luck.
		if upsertAgentFastTestHook != nil {
			upsertAgentFastTestHook(attempt)
		}
		provider, ok := extractProvider(oldRegistry)
		if !ok || provider == nil {
			return nil, fmt.Errorf("upsert agent fast: no provider available on current registry")
		}

		// Same constructor NewAgentRegistry itself uses per agent.
		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg, provider)
		if instance.AgentType == "custom" && coreagent.IsCoreAgent(id) {
			instance.SetAgentType("core")
		}

		newRegistry := oldRegistry.cloneAgents()
		newRegistry.UpsertAgent(instance)
		// TRAP 1: fresh resolver + default-agent override, from the SAME cfg.
		newRegistry.resolver = routing.NewRouteResolver(cfg)
		if cfg.Agents.Defaults.DefaultAgentID != "" {
			newRegistry.SetDefaultAgentOverride(cfg.Agents.Defaults.DefaultAgentID)
		}
		newRegistry.EvaluateDefaultAgentHealth(cfg.Agents.Defaults.DefaultAgentID, cfg.SkippedAgentIDs)

		// Re-run the SAME wiring ReloadProviderAndConfig runs against a
		// freshly-built registry, against this one instead. registerSharedTools
		// et al. iterate registry.ListAgentIDs()/GetAgent() themselves, so this
		// re-touches the other N-1 agents' tool registrations too (idempotent —
		// same config, same singleton deps, so "cheap" per the issue's own
		// design note) while giving the one new/updated instance full parity.
		registerSharedTools(al, cfg, al.bus, newRegistry, provider)
		if al.tier13Deps != nil {
			al.wireTier13DepsLocked(newRegistry, *al.tier13Deps)
		}
		al.wireExecToolDepsOn(newRegistry)
		if al.sysagentDeps != nil {
			al.wireSysagentDepsLocked(newRegistry, al.sysagentDeps)
		}
		if al.auditLogger != nil {
			al.wireMemoryAuditLoggerOn(newRegistry, al.auditLogger)
		}
		if al.memoryRateLimiter != nil {
			al.wireMemoryRateLimiterOn(newRegistry, al.memoryRateLimiter)
		}
		wireDelegationInjectors(al, newRegistry)
		wireWorkingDirInjectors(al, newRegistry)
		// ADR-072 R1 fix regression (live UAT, 2026-09-02): wireProjectShelfResolvers
		// was only ever called once, from NewAgentLoop at boot — unlike its
		// siblings above, it was never re-applied here, so a mounted project's
		// skills silently stopped resolving for every agent after the FIRST
		// config reload of the process (onboarding itself triggers one, so this
		// hit nearly every real install). Mirror the siblings' re-wiring.
		wireProjectShelfResolvers(al, newRegistry)
		// MediaStore is deliberately left nil by registerSharedTools' send_file
		// wiring (see its own doc comment) because it may not exist yet on the
		// very first wiring pass inside NewAgentLoop; every real reload
		// re-applies it afterward via SetMediaStore. Mirror that here so every
		// agent touched by the re-wiring above doesn't transiently lose media
		// capability relative to a full reload's end state.
		if ms := al.GetMediaStore(); ms != nil {
			for _, aid := range newRegistry.ListAgentIDs() {
				if inst, ok := newRegistry.GetAgent(aid); ok {
					inst.Tools.SetMediaStore(ms)
				}
			}
		}

		al.mu.Lock()
		if al.registry != oldRegistry || al.configGen.Load() != oldGen {
			// Lost the race: another publisher changed al.registry and/or
			// al.cfg out from under this attempt's snapshot — either a full
			// ReloadProviderAndConfig (swaps both together) or a bare
			// SwapConfig (swaps al.cfg alone; DEFECT 2). Grab that
			// freshly-published al.cfg now, while still holding the lock, so
			// the retry below can rebase onto it (BUG 1 fix) instead of
			// continuing to build off our now-superseded snapshot.
			liveCfg := al.cfg
			al.mu.Unlock()
			if closeErr := instance.Close(); closeErr != nil {
				logger.WarnCF("agent", "upsert agent fast: failed to close discarded instance after a lost publish race",
					map[string]any{"agent_id": id, "error": closeErr.Error()})
			}

			// Rebase cfg (and ac, which points into it) onto the live config
			// instead of retrying with the pre-reload snapshot — otherwise the
			// eventual successful publish's `al.cfg = cfg` below would revert
			// everything the other writer just installed. Clone rather than
			// mutate liveCfg in place: al.cfg is shared with every other
			// concurrent reader (GetConfig hands out this exact pointer, never
			// a copy), so mutating its Agents.List in place would itself be a
			// data race. Splice this call's own requested AgentConfig
			// (wantAC) into the clone by ID so the upsert this caller asked
			// for is never lost, whether or not liveCfg's own disk read
			// happened to already include it.
			if liveCfg != nil && liveCfg != cfg {
				rebased, cloneErr := liveCfg.Clone()
				if cloneErr != nil {
					return nil, fmt.Errorf("upsert agent fast: rebase onto live config after lost publish race: %w", cloneErr)
				}
				replaced := false
				for i := range rebased.Agents.List {
					if routing.NormalizeAgentID(rebased.Agents.List[i].ID) == id {
						rebased.Agents.List[i] = wantAC
						replaced = true
						break
					}
				}
				if !replaced {
					// DEFECT 1: `id` is absent from the config we are rebasing
					// onto. This is expected for a genuine create racing a
					// reload/SwapConfig whose disk read predates this agent's
					// entity write (agentstore.Store.Create always runs,
					// synchronously, before fastAgentUpsert/UpsertAgentFast is
					// ever called — see pkg/gateway/rest.go's createAgent) —
					// but it is ALSO exactly what a concurrent DELETE of this
					// same agent produces (deleteAgent's agentstore.Delete +
					// triggerReloadAndWait already dropped `id` from both
					// cfg.Agents.List AND the registry). cfg alone cannot tell
					// those two apart; ask the durable entity store instead.
					if _, getErr := agentstore.New(al.homePath).Get(id); getErr != nil {
						if errors.Is(getErr, entity.ErrNotFound) {
							return nil, fmt.Errorf(
								"upsert agent fast: agent %q was deleted by a concurrent request; refusing to resurrect it",
								id)
						}
						return nil, fmt.Errorf(
							"upsert agent fast: could not confirm agent %q still exists before rebasing onto a concurrent config change: %w",
							id, getErr)
					}
					rebased.Agents.List = append(rebased.Agents.List, wantAC)
				}
				cfg = rebased
				ac = nil
				for i := range cfg.Agents.List {
					if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == id {
						ac = &cfg.Agents.List[i]
						break
					}
				}
				if ac == nil {
					// Unreachable: wantAC was just appended-or-replaced above
					// under this exact id.
					return nil, fmt.Errorf("upsert agent fast: agent %q vanished from rebased config", agentID)
				}
			}
			continue
		}
		al.cfg = cfg
		al.registry = newRegistry
		al.mu.Unlock()
		return instance, nil
	}

	return nil, fmt.Errorf(
		"upsert agent fast: gave up after %d attempts racing concurrent registry publishers",
		maxFastUpsertAttempts,
	)
}

// RemoveAgent deletes a single agent instance from the registry (post
// entity-delete, ADR-054 D6 rule 5). Reports whether an instance was present.
// There is no reserved sentinel ID to protect anymore — every registered
// agent is a real, config-backed entity and is removable through this path.
//
// HONEST STATUS as of this commit: no production code calls this — see
// UpsertAgent's doc comment immediately above for the full explanation
// (system.agent.delete triggers a full AgentLoop.ReloadProviderAndConfig
// instead, via deps.ReloadFunc). Directly tested
// (TestAgentRegistry_RemoveAgent below) and available as the delete-side
// counterpart of UpsertAgent for a future narrower-than-full-reload caller.
func (r *AgentRegistry) RemoveAgent(id string) bool {
	normalized := routing.NormalizeAgentID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[normalized]; !ok {
		return false
	}
	delete(r.agents, normalized)
	return true
}

// MarkDefaultAgentDegraded records that the configured default agent
// (config.Agents.Defaults.DefaultAgentID) could not be resolved because its
// backing entity record exists but failed to load (ADR-054 R3 / §0). This is
// an AVAILABILITY signal, not a fail-closed gate: GetDefaultAgent has already
// continued down the ladder (priority 2/3) by the time this is called, so
// traffic is never black-holed by a single corrupt file — the degraded flag
// exists purely so an operator can find out via /health (pkg/health.Server.
// SetDegradedFunc expects exactly this method's signature) rather than
// silently routing to a fallback agent forever. Idempotent; the most recent
// reason wins. Call ClearDefaultAgentDegraded to reset after the record is
// repaired and a reload has re-evaluated health.
func (r *AgentRegistry) MarkDefaultAgentDegraded(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.degraded = true
	r.degradedReason = reason
	logger.ErrorCF("agent", "default agent entity record unparseable; system marked degraded (routing continues via fallback ladder, not black-holed)",
		map[string]any{"reason": reason})
}

// ClearDefaultAgentDegraded resets the degraded flag set by
// MarkDefaultAgentDegraded. Called after a reload confirms the configured
// default agent now resolves cleanly.
func (r *AgentRegistry) ClearDefaultAgentDegraded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.degraded = false
	r.degradedReason = ""
}

// DefaultAgentDegraded reports whether the default-agent resolution ladder is
// currently degraded and why. Matches the func() (bool, string) shape
// pkg/health.Server.SetDegradedFunc expects, so wiring this up at boot is a
// one-line `healthServer.SetDegradedFunc(registry.DefaultAgentDegraded)`.
func (r *AgentRegistry) DefaultAgentDegraded() (bool, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.degraded, r.degradedReason
}

// EvaluateDefaultAgentHealth checks whether the configured default agent ID
// (config.Agents.Defaults.DefaultAgentID) names an entity that was SKIPPED at
// load time — i.e. its record exists on disk but failed to parse — as opposed
// to simply being unconfigured or never created. Only the skipped case is
// R3-degraded; an empty/unconfigured default or one that legitimately never
// existed is ordinary, non-degraded operation (the ladder's priority 2/3
// fallback handles it silently, by design).
//
// Callers should invoke this once after constructing/reloading a registry,
// passing the `skipped` slice returned by agentstore.Store.List(), and wire
// DefaultAgentDegraded to /health. Not calling this is safe (GetDefaultAgent's
// behavior is unaffected either way) — it only affects whether the degraded
// signal is ever raised.
func (r *AgentRegistry) EvaluateDefaultAgentHealth(defaultAgentID string, skipped []string) {
	defaultAgentID = strings.TrimSpace(defaultAgentID)
	if defaultAgentID == "" {
		return
	}
	normalizedDefault := routing.NormalizeAgentID(defaultAgentID)
	for _, id := range skipped {
		if routing.NormalizeAgentID(id) == normalizedDefault {
			r.MarkDefaultAgentDegraded(fmt.Sprintf(
				"configured default agent %q has an entity record that failed to load", defaultAgentID))
			return
		}
	}
}

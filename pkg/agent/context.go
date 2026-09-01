package agent

import (
	"container/list"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

type ContextBuilder struct {
	workspace     string
	agentID       string // agent ID for multi-agent context
	agentName     string // agent display name for multi-agent context
	skillsLoader  *skills.SkillsLoader
	memory        *MemoryStore
	splitOnMarker bool

	// manifestDiscoveryActive gates the "Tool Discovery" prompt section
	// (finding 1 / GH #657): true when cfg.Tools.Manifest.Compressed is
	// active, i.e. the 3-tier tool-manifest system is in effect and some
	// tools are hidden from the callable-defs list until searched for. This
	// used to be (wrongly) gated on the unrelated MCP-discovery config
	// (cfg.Tools.MCP.Discovery.UseBM25/UseRegex, default OFF) while the
	// manifest system defaults ON — so a default install rendered no
	// discovery guidance at all. Set via WithToolDiscovery.
	manifestDiscoveryActive bool

	// skillAllowlist enforces the per-agent skill allowlist at skill resolution
	// time (FR-9.4, ADR-072 D5, default-DENY). Only skills whose name appears
	// in this set can be resolved/invoked by this agent — a skill not on the
	// allowlist cannot be loaded into context or activated via /<slug>, even
	// though it exists on disk. A nil map (WithSkillAllowlist never called)
	// and an empty, non-nil map (WithSkillAllowlist called with nil or an
	// empty slice) are semantically IDENTICAL: both deny every name. There is
	// no "unrestricted" state — see skillAllowed's own doc comment. Keys are
	// lower-cased, trimmed skill names.
	skillAllowlist map[string]struct{}

	// projectShelf is the acting agent's WORKSPACE'S already-merged project
	// shelf (ADR-072 D4.1 shelf 1 — pkg/skills.ProjectShelf, built by
	// skills.MergeProjectSkills over that workspace's mounts). nil when no
	// workspace/mount context applies — every project-shelf-aware code path
	// below then behaves exactly as it did before this field existed. Set via
	// WithProjectShelf. Wiring a real workspace's mounts through to this
	// field on every turn is a later integration phase's responsibility (it
	// needs the D8 (agent × workspace) prompt-cache work this ADR names as a
	// hard prerequisite); this field only needs to exist and be consulted
	// correctly once it IS set.
	projectShelf skills.ProjectShelf

	// projectShelfResolver optionally resolves the effective project shelf
	// for a given workspace id, per turn (ADR-072 D8). Set via
	// WithProjectShelfResolver. When nil, every workspace falls back to the
	// single cb.projectShelf value (WithProjectShelf) — the pre-D8
	// behaviour, where the same shelf applies no matter which workspace is
	// asking. This is what makes the "# Skills" menu actually vary by
	// workspace (different mounts -> different project skills) once wired
	// to a real workspace/mount registry, mirroring the
	// WithDelegationInjector/WithWorkingDirInjector pattern already used for
	// other per-workspace, per-turn context.
	projectShelfResolver func(workspaceID string) skills.ProjectShelf

	// workspacePromptCache is the (agent x workspace) static-prompt cache
	// (ADR-072 D8/D8.1). It is entirely separate from the legacy
	// workspace-blind cache below (cachedSystemPrompt et al, which backs
	// BuildSystemPromptWithCache()/InvalidateCache() and stays exactly as it
	// was) — production callers that know their turn's workspace id use
	// BuildSystemPromptWithCacheForWorkspace instead. See workspacePromptCache's
	// own doc comment for the bound and eviction rules.
	workspacePromptCache workspacePromptCache

	// Cache for system prompt to avoid rebuilding on every call.
	// This fixes issue #607: repeated reprocessing of the entire context.
	// The cache auto-invalidates when workspace source files change (mtime check).
	systemPromptMutex  sync.RWMutex
	cachedSystemPrompt string
	cachedAt           time.Time // max observed mtime across tracked paths at cache build time

	// existedAtCache tracks which source file paths existed the last time the
	// cache was built. This lets sourceFilesChanged detect files that are newly
	// created (didn't exist at cache time, now exist) or deleted (existed at
	// cache time, now gone) — both of which should trigger a cache rebuild.
	existedAtCache map[string]bool

	// skillFilesAtCache snapshots the skill tree file set and mtimes at cache
	// build time. This catches nested file creations/deletions/mtime changes
	// that may not update the top-level skill root directory mtime.
	skillFilesAtCache map[string]time.Time

	// delegationInjector is an optional per-turn callback that renders the
	// "## Delegation" block for this agent. It is called in
	// buildDynamicContext (the UN-CACHED path) so that a runtime
	// change to the workspace delegation graph is reflected on the very next
	// turn without waiting for the cached system-prompt to expire.
	//
	// The workspaceID argument is the turn's effective workspace (from
	// ts.opts.WorkspaceID → tools.ToolWorkspaceID(ctx)), which MUST match the
	// authority the enforcement gate reads — so the block advertises exactly
	// what the gate allows. Set via WithDelegationInjector; wired by
	// wireDelegationInjectors in loop_env.go.
	delegationInjector func(workspaceID string) string

	// workingDirInjector is an optional per-turn callback that renders a
	// "## Working Directory" block telling the agent where its file tools
	// actually operate. Like delegationInjector it runs in buildDynamicContext
	// (the UN-CACHED path) because CoreTeam membership — and therefore the
	// agent's re-rooted working directory (pkg/agent/loop.go's filesystem
	// re-rooting block) — can change at runtime.
	//
	// Without this, an agent whose file tools are silently re-rooted to a
	// Workspace's shared workspaces/<id>/work/ directory has no way to know
	// it: it will assume its traditional private agents/<id>/ directory,
	// guess wrong absolute paths, and can report a false file location to the
	// user (observed: an agent told the user its report was saved under
	// agents/<id>/ when the file was actually — correctly — written to the
	// workspace's shared directory). Set via WithWorkingDirInjector; wired by
	// wireWorkingDirInjectors in loop_env.go.
	//
	// The workspaceID argument mirrors delegationInjector's: the turn's
	// effective workspace (ts.opts.WorkspaceID), threaded through so an agent
	// that belongs to more than one workspace's CoreTeam is told about the
	// CURRENT session's workspace, not an arbitrary one — see
	// workspace.FindForAgentPreferring, which this injector must use (the
	// SAME resolution steps pkg/agent/loop.go's re-rooting block uses,
	// including the MkdirAll fallback) so the advertised directory matches
	// the one actually applied.
	workingDirInjector func(workspaceID string) string

	// env carries the environment provider + any per-builder env state. Split
	// into a nested struct so context_env.go owns the mutation surface without
	// touching the core ContextBuilder definition.
	env contextBuilderEnv

	// memoryEnabled gates whether BuildSystemPrompt injects the "# Memory"
	// section (ADR-052 FR-039, Judge/Verifier architecture). Defaults to true
	// (NewContextBuilder) so every agent that never calls WithMemoryEnabled
	// sees the pre-existing, unconditional behavior. A verifier-role agent
	// (e.g. the seeded Judge, config.AgentConfig.MemoryEnabled=false) is
	// wired to false so its verdicts stay reproducible and impartial — same
	// evidence, same verdict — rather than drifting with accumulated
	// episodic memory across adjudications.
	//
	// Set ONCE at construction (mirrors WithSkillAllowlist/WithAgentInfo) —
	// never toggle this per-turn/per-call. A ContextBuilder is one instance
	// shared by every concurrent turn/session for its agent, so flipping
	// this field around a single call would race against any other
	// concurrently-running turn for the same agent and could leave the flag
	// in the wrong state for it.
	memoryEnabled bool
}

// WithDelegationInjector installs the per-turn delegation context callback. fn
// receives the turn's effective workspaceID (ts.opts.WorkspaceID, may be "") and
// is called on every turn from buildDynamicContext (the UN-CACHED path). The
// closure reads the workspace delegation graph for that workspace so the block
// advertises exactly what the enforcement gate allows. Passing nil disables the
// block (no delegation section is appended).
func (cb *ContextBuilder) WithDelegationInjector(fn func(workspaceID string) string) *ContextBuilder {
	cb.delegationInjector = fn
	return cb
}

// WithWorkingDirInjector installs the per-turn working-directory context
// callback. fn receives the turn's effective workspaceID (ts.opts.WorkspaceID,
// may be "") and is called on every turn from buildDynamicContext (the
// UN-CACHED path), returning the "## Working Directory" block text (or "" to
// omit it). Passing nil disables the block.
func (cb *ContextBuilder) WithWorkingDirInjector(fn func(workspaceID string) string) *ContextBuilder {
	cb.workingDirInjector = fn
	return cb
}

// WithToolDiscovery gates the "Tool Discovery" prompt section on whether the
// 3-tier tool-manifest system is active for this agent (finding 1 / GH #657):
// pass cfg.Tools.Manifest.Compressed. When active, some tools are hidden from
// the callable-defs list every turn and must be found via ToolSearch first —
// the rendered rule explains this. When inactive (legacy full-manifest mode,
// every tool sent every turn), there is nothing to discover, so the rule is
// omitted.
func (cb *ContextBuilder) WithToolDiscovery(active bool) *ContextBuilder {
	cb.manifestDiscoveryActive = active
	return cb
}

func (cb *ContextBuilder) WithSplitOnMarker(enabled bool) *ContextBuilder {
	cb.splitOnMarker = enabled
	return cb
}

// WithMemoryEnabled gates whether BuildSystemPrompt injects this agent's
// episodic "# Memory" section (ADR-052 FR-039). Call with false ONCE at
// construction for a verifier-role agent (see the memoryEnabled field's doc
// comment for why this must never be toggled per-turn). Every agent that
// never calls this keeps the pre-existing, unconditional behavior via
// NewContextBuilder's default of true.
func (cb *ContextBuilder) WithMemoryEnabled(enabled bool) *ContextBuilder {
	cb.memoryEnabled = enabled
	return cb
}

func (cb *ContextBuilder) WithAgentInfo(id, name string) *ContextBuilder {
	cb.agentID = id
	cb.agentName = name
	// Wire the agent ID into the memory store so memory authorship is recorded
	// from the known identity rather than derived from the workspace path.
	if cb.memory != nil {
		cb.memory.SetAgentID(id)
	}
	return cb
}

// WithSkillAllowlist installs a per-agent skill allowlist that is enforced at
// skill-resolution time (FR-9.4, ADR-072 D5). Only the named skills can be
// resolved or invoked by this agent; any other skill — even one present on
// disk — is denied. Names are matched case-insensitively.
//
// ADR-072 D5: "absence of a grant list means *no* skills, matching the
// shipped contract" (contracts/components/schemas/Agent.yaml already says
// "opt-in, default none" — absence of the field and an empty array are
// semantically identical). So a nil allowlist is NOT treated specially any
// more — it is not "unrestricted", it is the same deny-all state an empty
// slice already produced. Both a nil and an empty allowlist install an empty,
// non-nil map, and skillAllowed denies every name against it. A ContextBuilder
// on which this method is never called at all is denied identically (its
// skillAllowlist field's zero value is nil, and skillAllowed treats a nil map
// the same as an empty one — see skillAllowed's own doc comment).
func (cb *ContextBuilder) WithSkillAllowlist(allowlist []string) *ContextBuilder {
	set := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		set[strings.ToLower(trimmed)] = struct{}{}
	}
	cb.skillAllowlist = set
	return cb
}

// WithProjectShelf sets the acting agent's workspace project shelf (ADR-072
// D4.1 shelf 1). shelf may be nil (or omitted entirely — the field's zero
// value is already nil) to mean "no workspace/mount context applies", which
// is this builder's default and leaves ResolveSkillName's behaviour
// unchanged from before this shelf existed.
func (cb *ContextBuilder) WithProjectShelf(shelf skills.ProjectShelf) *ContextBuilder {
	cb.projectShelf = shelf
	return cb
}

// WithProjectShelfResolver installs a per-workspace project-shelf resolver
// (ADR-072 D8). fn is called with the turn's effective workspace id and
// must return that workspace's already-merged project shelf (nil/empty for
// "no mounts apply"). When set, it takes priority over the single
// WithProjectShelf value for every call that names a workspace id
// (BuildSystemPromptForWorkspace / BuildSystemPromptWithCacheForWorkspace);
// WithProjectShelf continues to govern the workspace-blind
// BuildSystemPrompt()/BuildSystemPromptWithCache() path and the resolver's
// own "" key. Passing nil uninstalls the resolver, reverting every
// workspace to the single-shelf behaviour.
func (cb *ContextBuilder) WithProjectShelfResolver(fn func(workspaceID string) skills.ProjectShelf) *ContextBuilder {
	cb.projectShelfResolver = fn
	return cb
}

// effectiveProjectShelf resolves the project shelf that governs skill
// listing/menu rendering for workspaceID (ADR-072 D8): the resolver's
// answer when one is installed, otherwise the single shelf WithProjectShelf
// set (or nil, its zero value, when neither was ever called).
func (cb *ContextBuilder) effectiveProjectShelf(workspaceID string) skills.ProjectShelf {
	if cb.projectShelfResolver != nil {
		return cb.projectShelfResolver(workspaceID)
	}
	return cb.projectShelf
}

// skillAllowed reports whether the named skill may be resolved/invoked by this
// agent under its allowlist (FR-9.4, ADR-072 D5/FR-032/FR-033). A nil OR
// empty allowlist denies EVERY name — opt-in, default none, matching
// contracts/components/schemas/Agent.yaml's "absence of the field and an
// empty array are semantically identical" contract text. There is no
// "unrestricted" state any more: only a name explicitly present in the
// allowlist (case-insensitive, trimmed) is allowed.
//
// This governs the REGISTRY shelf only (D4.1 shelf 2/3 — the per-agent grant
// list). The PROJECT shelf (a workspace mount's own skills, D4.1 shelf 1) is
// gated separately, by the mount itself — see ResolveSkillName and the
// project-shelf callers, which consult cb.projectShelf independently of this
// method.
func (cb *ContextBuilder) skillAllowed(name string) bool {
	_, ok := cb.skillAllowlist[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// Memory returns the MemoryStore backing this ContextBuilder.
// Used by NewAgentInstance to wire memory tools with the same store instance.
func (cb *ContextBuilder) Memory() *MemoryStore {
	return cb.memory
}

func getGlobalConfigDir() string {
	if home := os.Getenv(config.EnvHome); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.WarnCF("agent", "UserHomeDir failed; global skills dir will be skipped",
			map[string]any{"error": err.Error()})
		// Return a path under /tmp so the skills loader gets a non-empty but
		// non-existent directory rather than an empty string that might be
		// interpreted as the current directory.
		return filepath.Join("/tmp", pkg.DefaultOmnipusHome)
	}
	return filepath.Join(home, pkg.DefaultOmnipusHome)
}

// globalSkillsDir returns the fixed, install-wide skills directory
// ($OMNIPUS_HOME/skills) — the GLOBAL registry every agent's SkillsLoader
// (via NewContextBuilder below) and install_skill (tools.NewInstallSkillTool,
// ADR-046 FR-009) both target, as opposed to a per-agent/per-workspace path.
// Extracted so install_skill shares the EXACT same computation
// NewContextBuilder already used inline, so every agent's SkillsLoader and
// install_skill always agree on where the global skills directory lives.
func globalSkillsDir() string {
	return filepath.Join(getGlobalConfigDir(), "skills")
}

// BuiltinSkillsDir resolves the builtin-skills directory: the configured
// override, else skills/ under the working directory.
//
// Exported because the installed-skill set is a property of the INSTALLATION,
// not of any agent, and callers that need it must not have to reach through an
// agent to get it. Doing exactly that produced a fail-open: with no default
// agent, the gateway's installed-skill set came back empty, and
// validateSkillIDs is documented to skip validation on an empty set — so
// unknown skill ids were accepted (ADR-064 fallout).
func BuiltinSkillsDir() string {
	dir := strings.TrimSpace(os.Getenv(config.EnvBuiltinSkills))
	if dir != "" {
		return dir
	}
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		logger.WarnCF("agent", "os.Getwd failed; builtin skills dir unavailable",
			map[string]any{"error": wdErr.Error()})
		wd = filepath.Join(os.TempDir(), pkg.DefaultOmnipusHome)
	}
	return filepath.Join(wd, "skills")
}

// InstalledSkillIDs lists every installed skill id, read straight from the
// skills directories with no agent involved.
func InstalledSkillIDs(workspace string) []string {
	loader := skills.NewSkillsLoader(workspace, globalSkillsDir(), BuiltinSkillsDir())
	all := loader.ListSkills()
	ids := make([]string, 0, len(all))
	for _, sk := range all {
		ids = append(ids, sk.ID)
	}
	return ids
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	builtinSkillsDir := strings.TrimSpace(os.Getenv(config.EnvBuiltinSkills))
	if builtinSkillsDir == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			logger.WarnCF("agent", "os.Getwd failed; builtin skills dir unavailable",
				map[string]any{"error": wdErr.Error()})
			// Fall back to a non-existent path under TempDir so the skills loader
			// receives a non-empty, non-CWD path (consistent with getGlobalConfigDir).
			wd = filepath.Join(os.TempDir(), pkg.DefaultOmnipusHome)
		}
		builtinSkillsDir = filepath.Join(wd, "skills")
	}
	return &ContextBuilder{
		workspace:     workspace,
		skillsLoader:  skills.NewSkillsLoader(workspace, globalSkillsDir(), builtinSkillsDir),
		memory:        NewMemoryStore(workspace, omnipusHome()),
		memoryEnabled: true,
	}
}

// getIdentity returns the default system identity for agents WITHOUT a SOUL.md.
// This is used by the Omnipus system agent and any unconfigured custom agents.
func (cb *ContextBuilder) getIdentity() string {
	name := "Omnipus"
	if cb.agentName != "" {
		name = cb.agentName
	}
	return fmt.Sprintf("# %s\n\nYou are %s, a helpful AI assistant powered by Omnipus.\n\n%s",
		name, name, cb.getWorkspaceAndRules())
}

// getWorkspaceInfo returns workspace and rules context WITHOUT the default
// identity. Used when the agent has a SOUL.md that defines its own personality.
func (cb *ContextBuilder) getWorkspaceInfo() string {
	header := "# Agent"
	if cb.agentName != "" {
		header = "# " + cb.agentName
	}
	return fmt.Sprintf("%s\n\n%s", header, cb.getWorkspaceAndRules())
}

// getWorkspaceAndRules returns the workspace paths, rules, and tool discovery
// instructions shared by both getIdentity and getWorkspaceInfo.
func (cb *ContextBuilder) getWorkspaceAndRules() string {
	workspacePath, absErr := filepath.Abs(cb.workspace)
	if absErr != nil {
		logger.WarnCF("agent", "filepath.Abs failed for workspace; using raw path",
			map[string]any{"workspace": cb.workspace, "error": absErr.Error()})
		workspacePath = cb.workspace
	}
	toolDiscovery := cb.getDiscoveryRule()
	version := config.FormatVersion()

	agentContext := ""
	if cb.agentID != "" {
		agentContext = fmt.Sprintf("Agent ID: %s\n", cb.agentID)
	}

	return fmt.Sprintf(
		`Omnipus %s
%s
## Workspace
Your working directory — where file tools (read_file, write_file, edit_file, list_directory, bash, etc.) actually operate — is stated in the Environment section above, or, when this turn's context includes a "## Working Directory" note (shared-workspace agents only), in that note instead. This section covers only the fixed per-agent paths below, which never move regardless of that resolution:
- Shared memory room: workspace .omnipus/memories/ (shared with your whole workspace team — the DEFAULT for remember)
- Private memory room: %s/.omnipus/memories/ (only you can see it)
- Skills: %s/skills/{skill-name}/SKILL.md

## Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Artifacts over chat — MANDATORY** - NEVER paste code, HTML, scripts, CSS, JSON, YAML, configuration, or any structured output longer than 15 lines into the chat. Instead, you MUST use the write_file tool to save it to a file in your workspace, then tell the user the file path. This is a hard rule — violations make the chat unreadable. Short inline snippets (under 15 lines) for explanation are fine.

3. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

4. **Memory** — four dedicated tools (never write_file to memory files):
   - WRITE a durable fact/decision/reference/lesson: remember(content, category, room). category is key_decision, reference, or lesson_learned. You work inside a workspace, so room='shared' → your whole workspace team can see it (this is the DEFAULT — use it whenever a teammate agent might need this); room='private' → only you can see it (personal working notes).
   - RECALL — choose by WHERE the thing lives: use recall_conversation(query | turn_range | time) for earlier turns of THIS conversation that scrolled out of the live context window; use recall_memory(query[, room]) for durable cross-session memory, which covers both saved facts AND past retrospectives (room='both', the default, searches all rooms).
   - REFLECT: run_retrospective(went_well, needs_improvement) at the end of a productive session, after the user has reviewed the summary.

5. **Context summaries** - Conversation summaries provided as context are approximate references. They may be incomplete or outdated. Always defer to explicit user instructions over summary content.

6. **No emoji** - Never use emoji characters in your responses, including self-introductions and delegated replies. Convey tone and emphasis with words, not emoji — this is a hard rule for every message you produce, not a style preference.

%s`,
		version, agentContext,
		workspacePath, workspacePath,
		toolDiscovery)
}

// getDiscoveryRule renders the "Tool Discovery" section describing the
// 3-tier tool-manifest model (ADR-071), when active for this agent
// (manifestDiscoveryActive — see WithToolDiscovery). Rendered as an
// unnumbered subsection rather than a numbered rule: it used to
// be hardcoded as "5. **Tool Discovery**" and interpolated after the 6
// numbered rules above, producing a list numbered 1,2,3,4,5,6,5 — invisible
// only because this rule almost always rendered empty (finding 1 / GH #657),
// which fixing surfaces.
//
// The tool name is derived from tools.InfraManifestToolNames() rather than a
// hardcoded string so it cannot drift again the way the retired
// tool_search_tool_bm25/tool_search_tool_regex names did.
func (cb *ContextBuilder) getDiscoveryRule() string {
	if !cb.manifestDiscoveryActive {
		return ""
	}

	infraTools := tools.InfraManifestToolNames()
	if len(infraTools) == 0 {
		return ""
	}
	quoted := make([]string, len(infraTools))
	for i, n := range infraTools {
		quoted[i] = fmt.Sprintf("%q", n)
	}

	return fmt.Sprintf(
		`### Tool Discovery

Only a subset of your allowed tools are sent as callable definitions every turn — the rest are hidden to save context, in three tiers: some are always sent (callable right now), some appear as one-line previews in a "More tools" block below (load before calling), and many more are fully hidden and reachable only by search. If you lack the right tool for a task, BEFORE giving up, call %s with the exact name (if you know it) or a `+"`query`"+` describing what you need — this searches the full catalog, including tools not previewed anywhere. Do not refuse a request unless the search returns nothing. Found tools unlock immediately for this session.`,
		strings.Join(quoted, " or "),
	)
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	return cb.buildSystemPromptForShelf(cb.projectShelf)
}

// BuildSystemPromptForWorkspace is BuildSystemPrompt with the "# Skills"
// menu resolved against the project shelf for a specific workspace (ADR-072
// D8) via effectiveProjectShelf, rather than the single shelf
// WithProjectShelf sets. It is a strict superset of BuildSystemPrompt: with
// no resolver installed (WithProjectShelfResolver never called),
// effectiveProjectShelf falls back to cb.projectShelf for every
// workspaceID, so output is identical to BuildSystemPrompt() regardless of
// which workspaceID is passed.
func (cb *ContextBuilder) BuildSystemPromptForWorkspace(workspaceID string) string {
	return cb.buildSystemPromptForShelf(cb.effectiveProjectShelf(workspaceID))
}

// buildSystemPromptForShelf is the shared assembly body for
// BuildSystemPrompt/BuildSystemPromptForWorkspace: every section is
// identical between the two; only which project shelf feeds the "# Skills"
// menu differs; shelf may be nil.
func (cb *ContextBuilder) buildSystemPromptForShelf(shelf skills.ProjectShelf) string {
	parts := []string{}

	// FR-053: prepend env preamble at parts[0] when a provider is wired.
	// Empty string (no provider) is omitted so legacy callers see no change.
	if envCtx := cb.GetEnvironmentContext(); envCtx != "" {
		parts = append(parts, envCtx)
	}

	// Load agent definition once to avoid a TOCTOU race: previously this was
	// called once here and again inside LoadBootstrapFiles, which could observe
	// different on-disk state between the two reads.
	agentDef := cb.LoadAgentDefinition()

	// Bootstrap files (SOUL.md, AGENT.md, USER.md) — pass the already-loaded
	// definition to avoid a second LoadAgentDefinition call inside.
	bootstrapContent := cb.loadBootstrapFilesWithDef(agentDef)

	// Core agents have compiled prompts (not on disk). Check for a compiled
	// prompt first; if found, inject it as the SOUL content and use workspace-only
	// identity. This keeps the prompt invisible to users (no SOUL.md file).
	compiledPrompt := coreagent.GetPrompt(cb.agentID)
	if compiledPrompt != "" {
		parts = append(parts, cb.getWorkspaceInfo())
		bootstrapContent = fmt.Sprintf("## SOUL\n\n%s\n\n", compiledPrompt) + bootstrapContent
	} else if agentDef.Soul != nil && strings.TrimSpace(agentDef.Soul.Content) != "" {
		// Custom agent with SOUL.md on disk — use workspace-only identity.
		parts = append(parts, cb.getWorkspaceInfo())
	} else {
		// No SOUL.md and not a core agent — use the full default identity.
		parts = append(parts, cb.getIdentity())
	}

	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - show summary, AI can read full content with read_file tool.
	// Filtered by the per-agent allowlist for progressive disclosure (FR-9.4):
	// the prompt advertises only the skills this agent is permitted to use.
	skillsSummary := cb.skillsLoader.BuildSkillsSummaryFuncWithProject(cb.skillAllowed, shelf)
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool. This list is capped — call find_skills to search the full installed catalog, including any not shown below.

%s`, skillsSummary))
	}

	// Memory context (ADR-052 FR-039: suppressed when memoryEnabled=false —
	// e.g. the verifier role — so verdicts stay reproducible/impartial
	// rather than drifting with episodic memory across adjudications).
	if cb.memoryEnabled {
		memoryContext := cb.memory.GetMemoryContext()
		if memoryContext != "" {
			parts = append(parts, "# Memory\n\n"+memoryContext)
		}
	}

	// Multi-Message Sending (if enabled)
	if cb.splitOnMarker {
		parts = append(parts, `# MULTI-MESSAGE OUTPUT
You MUST frequently use <|[SPLIT]|> to break your responses into multiple short messages. NEVER output a single long wall of text. Actively split distinct concepts or parts. Example: Message part 1<|[SPLIT]|>Message part 2<|[SPLIT]|>Message part 3

Each part separated by the marker will be sent as an independent message.`)
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

// BuildSystemPromptWithCache returns the cached system prompt if available
// and source files haven't changed, otherwise builds and caches it.
// Source file changes are detected via mtime checks (cheap stat calls).
func (cb *ContextBuilder) BuildSystemPromptWithCache() string {
	// Try read lock first — fast path when cache is valid
	cb.systemPromptMutex.RLock()
	if cb.cachedSystemPrompt != "" && !cb.sourceFilesChangedLocked() {
		result := cb.cachedSystemPrompt
		cb.systemPromptMutex.RUnlock()
		return result
	}
	cb.systemPromptMutex.RUnlock()

	// Acquire write lock for building
	cb.systemPromptMutex.Lock()
	defer cb.systemPromptMutex.Unlock()

	// Double-check: another goroutine may have rebuilt while we waited
	if cb.cachedSystemPrompt != "" && !cb.sourceFilesChangedLocked() {
		return cb.cachedSystemPrompt
	}

	// Snapshot the baseline (existence + max mtime) BEFORE building the prompt.
	// This way cachedAt reflects the pre-build state: if a file is modified
	// during BuildSystemPrompt, its new mtime will be > baseline.maxMtime,
	// so the next sourceFilesChangedLocked check will correctly trigger a
	// rebuild. The alternative (baseline after build) risks caching stale
	// content with a too-new baseline, making the staleness invisible.
	baseline := cb.buildCacheBaseline()
	prompt := cb.BuildSystemPrompt()
	cb.cachedSystemPrompt = prompt
	cb.cachedAt = baseline.maxMtime
	cb.existedAtCache = baseline.existed
	cb.skillFilesAtCache = baseline.skillFiles

	logger.DebugCF("agent", "System prompt cached",
		map[string]any{
			"length": len(prompt),
		})

	return prompt
}

// InvalidateCache clears the cached system prompt.
// Normally not needed because the cache auto-invalidates via mtime checks,
// but this is useful for tests or explicit reload commands.
func (cb *ContextBuilder) InvalidateCache() {
	cb.systemPromptMutex.Lock()
	defer cb.systemPromptMutex.Unlock()

	cb.cachedSystemPrompt = ""
	cb.cachedAt = time.Time{}
	cb.existedAtCache = nil
	cb.skillFilesAtCache = nil

	logger.DebugCF("agent", "System prompt cache invalidated", nil)
}

// maxCachedWorkspacePromptVariants is the default hard per-agent cap on the
// number of cached (agent x workspace) prompt variants this ContextBuilder
// retains at once, LRU-evicted on overflow (ADR-072 D8; spec FR-046 —
// "shipping with a default of 8"). A cache miss costs one prompt rebuild —
// the same work the mtime path already does — so a small cap is cheap to be
// wrong about in the safe direction.
const maxCachedWorkspacePromptVariants = 8

// maxCachedWorkspacePromptBytes is the default aggregate byte budget, per
// agent, across every cached (agent x workspace) prompt variant this
// ContextBuilder retains at once (ADR-072 D8.1; spec FR-046a — "defaulting
// to 4 MB"). The cache evicts least-recently-used on whichever of
// maxCachedWorkspacePromptVariants or this byte bound is reached first: a
// count-only bound cannot protect memory when variants differ by three
// orders of magnitude (D8.1's own arithmetic — an uncapped D1.1 menu over a
// 5000-skill mount is ~1.44 MB typical/5.75 MB worst per variant, so 8
// variants alone can reach double digits of MB for one agent).
const maxCachedWorkspacePromptBytes = 4 * 1024 * 1024 // 4 MB

// workspacePromptVariant is one cached (agent x workspace) assembled static
// prompt, plus the staleness snapshot needed to detect a tracked source
// file or skill-tree change (the same mtime-based check
// sourceFilesChangedLocked uses for the legacy per-agent cache — see
// fileChangedSinceBaseline/skillFilesChangedSince — just scoped to one
// workspace's snapshot instead of the whole agent's).
type workspacePromptVariant struct {
	prompt            string
	bytes             int
	cachedAt          time.Time
	existedAtCache    map[string]bool
	skillFilesAtCache map[string]time.Time
	// elem is this variant's node in workspacePromptCache.lru (Value is the
	// workspace id string), kept for O(1) move-to-back/removal.
	elem *list.Element
}

// workspacePromptCache implements ADR-072 D8/D8.1's bounded, byte-aware
// (agent x workspace) prompt cache. Zero value is ready to use. Guarded by
// its own mutex, entirely separate from ContextBuilder.systemPromptMutex
// (which guards only the legacy, workspace-blind cachedSystemPrompt/et al
// fields above) — a rebuild for one workspace never blocks a cache read for
// a different workspace of the same agent, or the legacy no-workspace path.
//
// maxVariants/maxBytes are 0 (use the package defaults above) unless a test
// overrides them directly — this type has no exported constructor because
// every field's zero value is already the correct "unconfigured" state.
type workspacePromptCache struct {
	mu         sync.Mutex
	variants   map[string]*workspacePromptVariant
	lru        *list.List // front = least-recently-used, back = most-recently-used
	totalBytes int

	// maxVariants/maxBytes override the package defaults when > 0. Left at
	// their zero value in production; tests set these directly (white-box,
	// same package) to exercise eviction without allocating megabytes of
	// filler content.
	maxVariants int
	maxBytes    int
}

func (wc *workspacePromptCache) effectiveMaxVariants() int {
	if wc.maxVariants > 0 {
		return wc.maxVariants
	}
	return maxCachedWorkspacePromptVariants
}

func (wc *workspacePromptCache) effectiveMaxBytes() int {
	if wc.maxBytes > 0 {
		return wc.maxBytes
	}
	return maxCachedWorkspacePromptBytes
}

// fileChangedSinceBaseline is fileChangedSince's logic, parametrized over an
// arbitrary existedAtCache/cachedAt snapshot instead of always reading
// cb's own fields, so the same four-case staleness check (created / deleted
// / modified / unchanged) can be run against one workspace variant's own
// snapshot (see workspaceVariantStaleLocked) as well as the legacy
// per-agent one. cb.fileChangedSince below is now a thin wrapper over this.
func fileChangedSinceBaseline(existedAtCache map[string]bool, cachedAt time.Time, path string) bool {
	if existedAtCache == nil {
		return true
	}
	existedBefore := existedAtCache[path]
	info, err := os.Stat(path)
	existsNow := err == nil

	if existedBefore != existsNow {
		return true // file was created or deleted
	}
	if !existsNow {
		return false // didn't exist before, doesn't exist now
	}
	return info.ModTime().After(cachedAt)
}

// workspaceVariantStaleLocked mirrors sourceFilesChangedLocked's mtime-based
// staleness check (tracked source files, then skill roots, then the
// recursive skill-file snapshot) but evaluates it against one workspace
// variant's own snapshot instead of the legacy per-agent fields. It does
// NOT check workspace-membership or mount-deletion staleness — those are
// invisible to any mtime sweep by design (ADR-072 D8 items 2/3) and are
// handled by the separate, explicit EvictWorkspacePrompt call.
//
// Caller MUST hold cb.workspacePromptCache.mu.
func (cb *ContextBuilder) workspaceVariantStaleLocked(v *workspacePromptVariant) bool {
	if v.cachedAt.IsZero() {
		return true
	}
	if slices.ContainsFunc(cb.sourcePaths(), func(p string) bool {
		return fileChangedSinceBaseline(v.existedAtCache, v.cachedAt, p)
	}) {
		return true
	}
	for _, root := range cb.skillRoots() {
		if fileChangedSinceBaseline(v.existedAtCache, v.cachedAt, root) {
			return true
		}
	}
	return skillFilesChangedSince(cb.skillRoots(), v.skillFilesAtCache)
}

// evictWorkspaceVariantLocked removes workspaceID's cached variant, if any,
// updating the LRU list and the aggregate byte total. No-op when there is
// nothing cached for workspaceID. Caller MUST hold
// cb.workspacePromptCache.mu.
func (cb *ContextBuilder) evictWorkspaceVariantLocked(workspaceID string) {
	wc := &cb.workspacePromptCache
	v, ok := wc.variants[workspaceID]
	if !ok {
		return
	}
	if v.elem != nil && wc.lru != nil {
		wc.lru.Remove(v.elem)
	}
	wc.totalBytes -= v.bytes
	delete(wc.variants, workspaceID)
}

// storeWorkspaceVariantLocked inserts (or replaces) the cached variant for
// workspaceID, then evicts least-recently-used entries until both the count
// bound and the aggregate byte bound are satisfied (ADR-072 D8.1: whichever
// binds first). Caller MUST hold cb.workspacePromptCache.mu, and MUST have
// already confirmed len(prompt) does not itself exceed the byte budget —
// BuildSystemPromptWithCacheForWorkspace enforces that before calling this,
// per D8.1's "a variant larger than the whole byte budget is never cached"
// rule.
func (cb *ContextBuilder) storeWorkspaceVariantLocked(workspaceID string, v *workspacePromptVariant) {
	wc := &cb.workspacePromptCache
	if wc.variants == nil {
		wc.variants = make(map[string]*workspacePromptVariant)
		wc.lru = list.New()
	}

	// Drop any existing entry for this key first so a replace doesn't
	// double-count its bytes against the budget below.
	cb.evictWorkspaceVariantLocked(workspaceID)

	v.elem = wc.lru.PushBack(workspaceID)
	wc.variants[workspaceID] = v
	wc.totalBytes += v.bytes

	maxVariants := wc.effectiveMaxVariants()
	maxBytes := wc.effectiveMaxBytes()
	for (len(wc.variants) > maxVariants || wc.totalBytes > maxBytes) && wc.lru.Len() > 0 {
		front := wc.lru.Front()
		lruKey, _ := front.Value.(string)
		cb.evictWorkspaceVariantLocked(lruKey)
	}
}

// BuildSystemPromptWithCacheForWorkspace returns this agent's cached static
// system prompt for a specific workspace (ADR-072 D8): the assembled
// prompt — including the "# Skills" menu, which can now differ per
// workspace via WithProjectShelfResolver's mounts — is cached per (agent x
// workspace), not per agent, and the cache is bounded by count OR aggregate
// bytes, whichever binds first (D8.1). workspaceID == "" is a legitimate
// cache key like any other (the no-workspace case) and participates in the
// same bound.
//
// A single variant whose assembled size exceeds the whole byte budget is
// never cached (D8.1's oversized-variant rule) — it is rebuilt on every
// call instead, so one pathological workspace cannot evict every other
// entry on each insertion.
func (cb *ContextBuilder) BuildSystemPromptWithCacheForWorkspace(workspaceID string) string {
	wc := &cb.workspacePromptCache

	wc.mu.Lock()
	defer wc.mu.Unlock()

	if v, ok := wc.variants[workspaceID]; ok && !cb.workspaceVariantStaleLocked(v) {
		wc.lru.MoveToBack(v.elem)
		return v.prompt
	}

	// Snapshot the baseline BEFORE building, for the same reason
	// BuildSystemPromptWithCache does: a file modified mid-build gets a
	// mtime after this baseline, so the next staleness check still catches
	// it rather than caching it as if it were already reflected.
	baseline := cb.buildCacheBaseline()
	prompt := cb.BuildSystemPromptForWorkspace(workspaceID)

	if len(prompt) > wc.effectiveMaxBytes() {
		// D8.1: never cache a variant bigger than the whole budget — caching
		// it would just evict every other entry on the very next insertion.
		// Drop any stale prior entry for this key so a shrunk-then-oversized
		// history doesn't leave a stale hit behind.
		cb.evictWorkspaceVariantLocked(workspaceID)
		logger.DebugCF("agent", "workspace prompt variant exceeds byte budget, rebuilding uncached",
			map[string]any{
				"workspace_id": workspaceID,
				"bytes":        len(prompt),
				"budget":       wc.effectiveMaxBytes(),
			})
		return prompt
	}

	cb.storeWorkspaceVariantLocked(workspaceID, &workspacePromptVariant{
		prompt:            prompt,
		bytes:             len(prompt),
		cachedAt:          baseline.maxMtime,
		existedAtCache:    baseline.existed,
		skillFilesAtCache: baseline.skillFiles,
	})

	logger.DebugCF("agent", "workspace prompt variant cached",
		map[string]any{
			"workspace_id": workspaceID,
			"bytes":        len(prompt),
			"variants":     len(wc.variants),
			"total_bytes":  wc.totalBytes,
		})

	return prompt
}

// EvictWorkspacePrompt drops this agent's cached prompt variant for
// workspaceID, if any (ADR-072 D8's two invalidation triggers beyond mtime
// staleness). Call this when:
//
//  1. The agent is removed from workspaceID's membership — a workspace it
//     can no longer act in must not go on serving a cached menu for it.
//  2. A mount belonging to workspaceID is deleted — mount records live in
//     the entity store, not under any tracked skill root, so their removal
//     is invisible to workspaceVariantStaleLocked's mtime sweep.
//
// Both triggers are correctness requirements of D4.1's workspace scoping,
// not a performance nicety: getting either wrong serves the agent a stale
// menu offering skills it should no longer see. A miss just costs one
// rebuild on the next call — the same cost buildCacheBaseline already pays
// on every cache miss.
func (cb *ContextBuilder) EvictWorkspacePrompt(workspaceID string) {
	wc := &cb.workspacePromptCache
	wc.mu.Lock()
	defer wc.mu.Unlock()
	cb.evictWorkspaceVariantLocked(workspaceID)
}

// sourcePaths returns non-skill workspace source files tracked for cache
// invalidation (bootstrap files + memory). Skill roots are handled separately
// because they require both directory-level and recursive file-level checks.
func (cb *ContextBuilder) sourcePaths() []string {
	agentDefinition := cb.LoadAgentDefinition()
	paths := agentDefinition.trackedPaths(cb.workspace)
	// Track the private room's memories directory and last-session.md for cache invalidation.
	// The memories dir mtime changes on any new .md write (directory mtime update).
	privateRoomRoot := filepath.Join(cb.workspace, ".omnipus")
	paths = append(paths, filepath.Join(privateRoomRoot, "memories"))
	paths = append(paths, filepath.Join(privateRoomRoot, "last-session.md"))
	return uniquePaths(paths)
}

// skillRoots returns all skill root directories that can affect
// BuildSkillsSummary output (workspace/global/builtin).
func (cb *ContextBuilder) skillRoots() []string {
	if cb.skillsLoader == nil {
		return []string{filepath.Join(cb.workspace, "skills")}
	}

	roots := cb.skillsLoader.SkillRoots()
	if len(roots) == 0 {
		return []string{filepath.Join(cb.workspace, "skills")}
	}
	return roots
}

// cacheBaseline holds the file existence snapshot and the latest observed
// mtime across all tracked paths. Used as the cache reference point.
type cacheBaseline struct {
	existed    map[string]bool
	skillFiles map[string]time.Time
	maxMtime   time.Time
}

// buildCacheBaseline records which tracked paths currently exist and computes
// the latest mtime across all tracked files + skills directory contents.
// Called under write lock when the cache is built.
func (cb *ContextBuilder) buildCacheBaseline() cacheBaseline {
	skillRoots := cb.skillRoots()
	srcPaths := cb.sourcePaths()

	// C3: Use explicit allocation to avoid mutating the underlying array of sourcePaths.
	allPaths := make([]string, 0, len(srcPaths)+len(skillRoots))
	allPaths = append(allPaths, srcPaths...)
	allPaths = append(allPaths, skillRoots...)

	existed := make(map[string]bool, len(allPaths))
	skillFiles := make(map[string]time.Time)
	var maxMtime time.Time

	for _, p := range allPaths {
		info, err := os.Stat(p)
		existed[p] = err == nil
		if err == nil && info.ModTime().After(maxMtime) {
			maxMtime = info.ModTime()
		}
	}

	// Walk all skill roots recursively to snapshot skill files and mtimes.
	// Use os.Stat (not d.Info) for consistency with sourceFilesChanged checks.
	for _, root := range skillRoots {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// M15: log non-IsNotExist walk errors.
				if !os.IsNotExist(walkErr) {
					logger.WarnCF("agent", "cache baseline: WalkDir error",
						map[string]any{"root": root, "path": path, "error": walkErr.Error()})
				}
				return nil
			}
			if !d.IsDir() {
				if info, err := os.Stat(path); err == nil {
					skillFiles[path] = info.ModTime()
					if info.ModTime().After(maxMtime) {
						maxMtime = info.ModTime()
					}
				}
			}
			return nil
		}); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("agent", "cache baseline: WalkDir returned error",
				map[string]any{"root": root, "error": err.Error()})
		}
	}

	// If no tracked files exist yet (empty workspace), maxMtime is zero.
	// Use a very old non-zero time so that:
	// 1. cachedAt.IsZero() won't trigger perpetual rebuilds.
	// 2. Any real file created afterwards has mtime > cachedAt, so it
	//    will be detected by fileChangedSince (unlike time.Now() which
	//    could race with a file whose mtime <= Now).
	if maxMtime.IsZero() {
		maxMtime = time.Unix(1, 0)
	}

	return cacheBaseline{existed: existed, skillFiles: skillFiles, maxMtime: maxMtime}
}

// sourceFilesChangedLocked checks whether any workspace source file has been
// modified, created, or deleted since the cache was last built.
//
// IMPORTANT: The caller MUST hold at least a read lock on systemPromptMutex.
// Go's sync.RWMutex is not reentrant, so this function must NOT acquire the
// lock itself (it would deadlock when called from BuildSystemPromptWithCache
// which already holds RLock or Lock).
func (cb *ContextBuilder) sourceFilesChangedLocked() bool {
	if cb.cachedAt.IsZero() {
		return true
	}

	// Check tracked source files (bootstrap + memory).
	if slices.ContainsFunc(cb.sourcePaths(), cb.fileChangedSince) {
		return true
	}

	// --- Skill roots (workspace/global/builtin) ---
	//
	// For each root:
	// 1. Creation/deletion and root directory mtime changes are tracked by fileChangedSince.
	// 2. Nested file create/delete/mtime changes are tracked by the skill file snapshot.
	for _, root := range cb.skillRoots() {
		if cb.fileChangedSince(root) {
			return true
		}
	}
	return skillFilesChangedSince(cb.skillRoots(), cb.skillFilesAtCache)
}

// fileChangedSince returns true if a tracked source file has been modified,
// newly created, or deleted since the cache was built.
//
// Four cases:
//   - existed at cache time, exists now -> check mtime
//   - existed at cache time, gone now   -> changed (deleted)
//   - absent at cache time,  exists now -> changed (created)
//   - absent at cache time,  gone now   -> no change
//
// Thin wrapper over fileChangedSinceBaseline against this ContextBuilder's
// own legacy (workspace-blind) snapshot fields — see that function for the
// shared logic, also used by workspaceVariantStaleLocked against a
// per-workspace snapshot.
func (cb *ContextBuilder) fileChangedSince(path string) bool {
	return fileChangedSinceBaseline(cb.existedAtCache, cb.cachedAt, path)
}

// errWalkStop is a sentinel error used to stop filepath.WalkDir early.
// Using a dedicated error (instead of fs.SkipAll) makes the early-exit
// intent explicit and avoids the nilerr linter warning that would fire
// if the callback returned nil when its err parameter is non-nil.
var errWalkStop = errors.New("walk stop")

// skillFilesChangedSince compares the current recursive skill file tree
// against the cache-time snapshot. Any create/delete/mtime drift invalidates
// the cache.
func skillFilesChangedSince(skillRoots []string, filesAtCache map[string]time.Time) bool {
	// Defensive: if the snapshot was never initialized, force rebuild.
	if filesAtCache == nil {
		return true
	}

	// Check cached files still exist and keep the same mtime.
	for path, cachedMtime := range filesAtCache {
		info, err := os.Stat(path)
		if err != nil {
			// A previously tracked file disappeared (or became inaccessible):
			// either way, cached skill summary may now be stale.
			return true
		}
		if !info.ModTime().Equal(cachedMtime) {
			return true
		}
	}

	// Check no new files appeared under any skill root.
	changed := false
	for _, root := range skillRoots {
		if strings.TrimSpace(root) == "" {
			continue
		}

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Treat unexpected walk errors as changed to avoid stale cache.
				if !os.IsNotExist(walkErr) {
					changed = true
					return errWalkStop
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if _, ok := filesAtCache[path]; !ok {
				changed = true
				return errWalkStop
			}
			return nil
		})

		if changed {
			return true
		}
		if err != nil && !errors.Is(err, errWalkStop) && !os.IsNotExist(err) {
			logger.DebugCF("agent", "skills walk error", map[string]any{"error": err.Error()})
			return true
		}
	}

	return false
}

// LoadBootstrapFiles loads the bootstrap files using a fresh LoadAgentDefinition call.
// Callers that already hold an AgentContextDefinition should use loadBootstrapFilesWithDef
// to avoid a redundant disk read.
func (cb *ContextBuilder) LoadBootstrapFiles() string {
	return cb.loadBootstrapFilesWithDef(cb.LoadAgentDefinition())
}

func (cb *ContextBuilder) loadBootstrapFilesWithDef(agentDefinition AgentContextDefinition) string {
	var sb strings.Builder

	if agentDefinition.Agent != nil {
		label := string(agentDefinition.Source)
		if label == "" {
			label = relativeWorkspacePath(cb.workspace, agentDefinition.Agent.Path)
		}
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", label, agentDefinition.Agent.Body)
	}
	// M6: only emit the Soul section when content is non-whitespace.
	if agentDefinition.Soul != nil && strings.TrimSpace(agentDefinition.Soul.Content) != "" {
		fmt.Fprintf(
			&sb,
			"## %s\n\n%s\n\n",
			relativeWorkspacePath(cb.workspace, agentDefinition.Soul.Path),
			agentDefinition.Soul.Content,
		)
	}
	if agentDefinition.User != nil {
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", "USER.md", agentDefinition.User.Content)
	}

	if agentDefinition.Source != AgentDefinitionSourceAgent {
		filePath := filepath.Join(cb.workspace, "IDENTITY.md")
		if data, err := os.ReadFile(filePath); err == nil {
			fmt.Fprintf(&sb, "## %s\n\n%s\n\n", "IDENTITY.md", data)
		} else if !os.IsNotExist(err) {
			logger.WarnCF("agent", "Could not read IDENTITY.md",
				map[string]any{"path": filePath, "error": err.Error()})
		}
	}

	return sb.String()
}

// buildDynamicContext returns a short dynamic context string with per-request info.
// This changes every request (time, session) so it is NOT part of the cached prompt.
// LLM-side KV cache reuse is achieved by each provider adapter's native mechanism:
//   - Anthropic: per-block cache_control (ephemeral) on the static SystemParts block
//   - OpenAI / Codex: prompt_cache_key for prefix-based caching
//
// See: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
// See: https://platform.openai.com/docs/guides/prompt-caching
func formatCurrentSenderLine(senderID, senderDisplayName string) string {
	senderID = strings.TrimSpace(senderID)
	senderDisplayName = strings.TrimSpace(senderDisplayName)

	switch {
	case senderDisplayName != "" && senderID != "":
		return fmt.Sprintf("Current sender: %s (ID: %s)", senderDisplayName, senderID)
	case senderDisplayName != "":
		return fmt.Sprintf("Current sender: %s", senderDisplayName)
	case senderID != "":
		return fmt.Sprintf("Current sender: %s", senderID)
	default:
		return ""
	}
}

func (cb *ContextBuilder) buildDynamicContext(workspaceID, channel, chatID, senderID, senderDisplayName string) string {
	localNow := time.Now()
	// Finding 10(b): the local time carried no timezone, so the model could not
	// tell how it related to any timestamp it reads elsewhere in context (e.g.
	// memory entries, which are persisted and displayed in UTC — see
	// MemoryStore.GetMemoryContext). Both a local time WITH its zone/offset and
	// an explicit UTC line are rendered so the two are unambiguously reconcilable.
	now := localNow.Format("2006-01-02 15:04 MST (-07:00) Monday")
	utcNow := localNow.UTC().Format("2006-01-02 15:04:05Z")
	rt := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Current Time\n%s\nUTC: %s\n\n## Runtime\n%s", now, utcNow, rt)

	if channel != "" && chatID != "" {
		fmt.Fprintf(&sb, "\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}
	if senderLine := formatCurrentSenderLine(senderID, senderDisplayName); senderLine != "" {
		fmt.Fprintf(&sb, "\n\n## Current Sender\n%s", senderLine)
	}

	// Delegation block: injected per-turn so the workspace graph read reflects
	// the CURRENT graph state without waiting for the cached static prompt to
	// expire. workspaceID is the turn's effective workspace — the same value the
	// enforcement gate resolves, so advertisement == enforcement by construction.
	if cb.delegationInjector != nil {
		if block := cb.delegationInjector(workspaceID); block != "" {
			fmt.Fprintf(&sb, "\n\n%s", block)
		}
	}

	// Working-directory block: injected per-turn for the same freshness reason
	// as the delegation block above — CoreTeam membership (and therefore the
	// re-rooted working directory) can change at runtime.
	if cb.workingDirInjector != nil {
		if block := cb.workingDirInjector(workspaceID); block != "" {
			fmt.Fprintf(&sb, "\n\n%s", block)
		}
	}

	// ADR-072 D3: the "consider your skills" reminder lands here, in the
	// UN-CACHED dynamic context, deliberately outside the cache breakpoint
	// that the "# Skills" menu (BuildSystemPrompt, the static/cached half)
	// sits inside. See skill_reminder.go for the byte-budget rationale.
	fmt.Fprintf(&sb, "\n\n%s", skillReminderNote)

	return sb.String()
}

// BuildMessages assembles the provider message list for a single agent turn.
//
// Assembly order (FR-006/FR-019):
//  1. Single system message: static cached prompt + dynamic context + skills +
//     breadcrumb block (FR-007, when non-empty).
//  2. recallSpan messages (transient recalled turns, Design B), when non-nil — old
//     recalled turns precede the recent window (FR-019 chronology).
//  3. history messages (the Skip-trimmed sliding window, post-eviction).
//  4. Current user message.
//
// workspaceID is the turn's effective workspace (ts.opts.WorkspaceID). It is
// threaded into buildDynamicContext and thence into delegationInjector so the
// "## Delegation" block reads the SAME workspace graph the enforcement gate
// consults — advertisement == enforcement by construction. Pass "" when no
// workspace is bound to the turn (the injector will resolve the default).
//
// breadcrumb is the prominent evicted-context block built by buildBreadcrumb
// (FR-007); pass "" when no turns have been evicted.
//
// recallSpan carries the transient native recall span re-injected by
// recall_conversation (FR-019); pass nil when no recall is active. The span
// is placed after the breadcrumb and before the sliding window so the model
// sees recalled (old) context before the most-recent window turns.
func (cb *ContextBuilder) BuildMessages(
	history []providers.Message,
	currentMessage string,
	media []string,
	workspaceID string,
	channel, chatID, senderID, senderDisplayName string,
	breadcrumb string,
	recallSpan []providers.Message,
	activeSkills ...string,
) []providers.Message {
	messages := []providers.Message{}

	// The static part (identity, bootstrap, skills, memory) is cached locally to
	// avoid repeated file I/O and string building on every call (fixes issue #607).
	// Dynamic parts (time, session, breadcrumb) are appended per request.
	// Everything is sent as a single system message for provider compatibility:
	// - Anthropic adapter extracts messages[0] (Role=="system") and maps its content
	//   to the top-level "system" parameter in the Messages API request. A single
	//   contiguous system block makes this extraction straightforward.
	// - Codex maps only the first system message to its instructions field.
	// - OpenAI-compat passes messages through as-is.
	//
	// ADR-072 D8: cached per (agent x workspace), not per agent, because the
	// "# Skills" menu can now differ by workspace (different mounts ->
	// different project skills) — see BuildSystemPromptWithCacheForWorkspace.
	// workspaceID is the same value already threaded to buildDynamicContext
	// below for the delegation block, so both read the same turn's workspace.
	staticPrompt := cb.BuildSystemPromptWithCacheForWorkspace(workspaceID)

	// Build short dynamic context (time, runtime, session, delegation) — changes per request.
	// workspaceID is threaded so the delegation block reads the same graph the gate enforces.
	dynamicCtx := cb.buildDynamicContext(workspaceID, channel, chatID, senderID, senderDisplayName)

	// Compose a single system message: static (cached) + dynamic + breadcrumb.
	// Keeping all system content in one message ensures every provider adapter can
	// extract it correctly (Anthropic adapter -> top-level system param,
	// Codex -> instructions field).
	//
	// SystemParts carries the same content as structured blocks so that
	// cache-aware adapters (Anthropic) can set per-block cache_control.
	// The static block is marked "ephemeral" — its prefix hash is stable
	// across requests, enabling LLM-side KV cache reuse.
	stringParts := []string{staticPrompt, dynamicCtx}

	contentBlocks := []providers.ContentBlock{
		{Type: "text", Text: staticPrompt, CacheControl: &providers.CacheControl{Type: "ephemeral"}},
		{Type: "text", Text: dynamicCtx},
	}

	if skillsText := cb.buildActiveSkillsContext(activeSkills); skillsText != "" {
		stringParts = append(stringParts, skillsText)
		contentBlocks = append(contentBlocks, providers.ContentBlock{Type: "text", Text: skillsText})
	}

	// FR-007: breadcrumb block — placed AFTER skills, BEFORE the sliding
	// window. It is a distinct content block so providers can see it as
	// a prominent separate section. Non-empty only when turns have been evicted.
	if breadcrumb != "" {
		stringParts = append(stringParts, breadcrumb)
		contentBlocks = append(contentBlocks, providers.ContentBlock{Type: "text", Text: breadcrumb})
	}

	fullSystemPrompt := strings.Join(stringParts, "\n\n---\n\n")

	// Log system prompt summary for debugging (debug mode only).
	// staticPrompt above now comes from the (agent x workspace) cache
	// (ADR-072 D8), so report whether THAT cache holds an entry for this
	// turn's workspace — read under its own lock to avoid a data race with
	// a concurrent build/eviction.
	cb.workspacePromptCache.mu.Lock()
	_, isCached := cb.workspacePromptCache.variants[workspaceID]
	cb.workspacePromptCache.mu.Unlock()

	logger.DebugCF("agent", "System prompt built",
		map[string]any{
			"static_chars":    len(staticPrompt),
			"dynamic_chars":   len(dynamicCtx),
			"total_chars":     len(fullSystemPrompt),
			"has_breadcrumb":  breadcrumb != "",
			"recall_span_len": len(recallSpan),
			"cached":          isCached,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := utils.Truncate(fullSystemPrompt, 500)
	logger.DebugCF("agent", "System prompt preview",
		map[string]any{
			"preview": preview,
		})

	// Sanitize recallSpan + history together as one sequence (FR-019).
	//
	// The recall span is spliced in raw — an archived Turn re-injected by
	// recall_conversation may contain an assistant tool_call whose matching
	// tool result was never written (SIGKILL/OOM mid-turn, recovered by
	// RecoverOrphanedToolCalls for the live window but not for evicted turns).
	// If injected raw, that orphan tool_call_id reaches strict providers
	// (Anthropic/OpenAI/DeepSeek) and causes a 400 on the whole request.
	//
	// Sanitizing the combined sequence (span prepended to history) catches:
	//   (a) orphans within the recall span itself, and
	//   (b) cross-boundary orphans where an assistant call lands in the span
	//       and its expected result is in the live window (or vice-versa).
	//
	// Chronological order is preserved because recallSpan (older recalled
	// turns) is prepended to history (recent window) and sanitization only
	// drops messages, never reorders them.
	combinedHistory := make([]providers.Message, 0, len(recallSpan)+len(history))
	combinedHistory = append(combinedHistory, recallSpan...)
	combinedHistory = append(combinedHistory, history...)
	combinedHistory = sanitizeHistoryForProvider(combinedHistory)

	// Single system message containing all context — compatible with all providers.
	// SystemParts enables cache-aware adapters to set per-block cache_control;
	// Content is the concatenated fallback for adapters that don't read SystemParts.
	messages = append(messages, providers.Message{
		Role:        "system",
		Content:     fullSystemPrompt,
		SystemParts: contentBlocks,
	})

	// Append the sanitized combined sequence (recall span + sliding window).
	// FR-019: recalled (old) turns precede the recent window turns, so the
	// model sees archived context before the live window — no re-ordering needed
	// since span messages were prepended to history before sanitization.
	messages = append(messages, combinedHistory...)

	// Add current user message. D1 (library-spec, 2026-07-29 UAT): a turn
	// carrying media but no caption used to be dropped ENTIRELY here (the
	// gate only checked currentMessage), so an uploaded file with no caption
	// never reached the LLM at all — the model had no way to even know a
	// file arrived. The gate now also fires whenever media is present, even
	// with an empty currentMessage, so a caption-less attachment still
	// reaches the model this turn instead of only resurfacing later via
	// session history (see resolveMediaRefsWithOffload's per-file upload
	// announcement, which now covers this turn too).
	if strings.TrimSpace(currentMessage) != "" || len(media) > 0 {
		msg := providers.Message{
			Role:    "user",
			Content: currentMessage,
		}
		if len(media) > 0 {
			msg.Media = media
		}
		messages = append(messages, msg)
	}

	return messages
}

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	out, _ := sanitizeHistoryIndexed(history)
	return out
}

// sanitizeHistoryIndexed is sanitizeHistoryForProvider's core: the same
// drop-only, order-preserving pass, additionally reporting the INPUT index
// of every retained message (kept[i] is the index in history of out[i]).
//
// The index report exists for ADR-066 D5.4: the recall-injection site
// splices a span ahead of the live window and sanitises the combined slice
// exactly as BuildMessages does, but it must know how many of the span's
// own messages survived so a later recall in the same turn can remove that
// block by position (E20). Reporting indices from the one sanitiser keeps
// the two sites byte-identical without a second copy of the rules.
func sanitizeHistoryIndexed(history []providers.Message) ([]providers.Message, []int) {
	if len(history) == 0 {
		return history, nil
	}

	sanitized := make([]providers.Message, 0, len(history))
	sanitizedIdx := make([]int, 0, len(history))
	for srcIdx, msg := range history {
		switch msg.Role {
		case "system":
			// Drop system messages from history. BuildMessages always
			// constructs its own single system message (static + dynamic +
			// summary); extra system messages would break providers that
			// only accept one (Anthropic, Codex).
			logger.DebugCF("agent", "Dropping system message from history", map[string]any{})
			continue

		case "tool":
			if len(sanitized) == 0 {
				logger.DebugCF("agent", "Dropping orphaned leading tool message", map[string]any{})
				continue
			}
			// Walk backwards to find the nearest assistant message,
			// skipping over any preceding tool messages (multi-tool-call case).
			foundAssistant := false
			for i := len(sanitized) - 1; i >= 0; i-- {
				if sanitized[i].Role == "tool" {
					continue
				}
				if sanitized[i].Role == "assistant" && len(sanitized[i].ToolCalls) > 0 {
					foundAssistant = true
				}
				break
			}
			if !foundAssistant {
				logger.DebugCF("agent", "Dropping orphaned tool message", map[string]any{})
				continue
			}
			sanitized = append(sanitized, msg)
			sanitizedIdx = append(sanitizedIdx, srcIdx)

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					logger.DebugCF("agent", "Dropping assistant tool-call turn at history start", map[string]any{})
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn with invalid predecessor",
						map[string]any{"prev_role": prev.Role},
					)
					continue
				}
			}
			sanitized = append(sanitized, msg)
			sanitizedIdx = append(sanitizedIdx, srcIdx)

		default:
			sanitized = append(sanitized, msg)
			sanitizedIdx = append(sanitizedIdx, srcIdx)
		}
	}

	// Second pass: ensure every assistant message with tool_calls has matching
	// tool result messages following it. This is required by strict providers
	// like DeepSeek that enforce: "An assistant message with 'tool_calls' must
	// be followed by tool messages responding to each 'tool_call_id'."
	final := make([]providers.Message, 0, len(sanitized))
	finalIdx := make([]int, 0, len(sanitized))
	seenToolCallID := make(map[string]bool)
	for i := 0; i < len(sanitized); i++ {
		msg := sanitized[i]

		// Deduplicate tool results by ToolCallID
		if msg.Role == "tool" && msg.ToolCallID != "" {
			if seenToolCallID[msg.ToolCallID] {
				logger.DebugCF("agent", "Dropping duplicate tool result", map[string]any{
					"tool_call_id": msg.ToolCallID,
				})
				continue
			}
			seenToolCallID[msg.ToolCallID] = true
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Collect expected tool_call IDs
			expected := make(map[string]bool, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				expected[tc.ID] = false
			}

			// Check following messages for matching tool results
			toolMsgCount := 0
			for j := i + 1; j < len(sanitized); j++ {
				if sanitized[j].Role != "tool" {
					break
				}
				toolMsgCount++
				if _, exists := expected[sanitized[j].ToolCallID]; exists {
					expected[sanitized[j].ToolCallID] = true
				}
			}

			// If any tool_call_id is missing, drop this assistant message and its partial tool messages
			allFound := true
			for toolCallID, found := range expected {
				if !found {
					allFound = false
					logger.DebugCF(
						"agent",
						"Dropping assistant message with incomplete tool results",
						map[string]any{
							"missing_tool_call_id": toolCallID,
							"expected_count":       len(expected),
							"found_count":          toolMsgCount,
						},
					)
					break
				}
			}

			if !allFound {
				// Skip this assistant message and its tool messages
				i += toolMsgCount
				continue
			}
		}
		final = append(final, msg)
		finalIdx = append(finalIdx, sanitizedIdx[i])
	}

	return final, finalIdx
}

// AddAssistantMessage appends an assistant message to the message slice.
// The toolCalls parameter was previously unused and has been removed (M5).
func (cb *ContextBuilder) AddAssistantMessage(
	messages []providers.Message,
	content string,
) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

func (cb *ContextBuilder) buildActiveSkillsContext(skillNames []string) string {
	if cb.skillsLoader == nil || len(skillNames) == 0 {
		return ""
	}

	var ordered []string
	seen := make(map[string]struct{}, len(skillNames))
	for _, name := range skillNames {
		canonical, ok := cb.ResolveSkillName(name)
		if !ok {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		ordered = append(ordered, canonical)
	}
	if len(ordered) == 0 {
		return ""
	}

	content := cb.skillsLoader.LoadSkillsForContext(ordered)
	if strings.TrimSpace(content) == "" {
		return ""
	}

	return fmt.Sprintf(`# Active Skills

The following skills are active for this request. Follow them when relevant.

%s`, content)
}

func (cb *ContextBuilder) ListSkillNames() []string {
	if cb.skillsLoader == nil {
		return nil
	}

	allSkills := cb.skillsLoader.ListSkills()
	names := make([]string, 0, len(allSkills))
	for _, skill := range allSkills {
		// Progressive disclosure: only list skills this agent may use (FR-9.4).
		// The allowlist and the loadable identity are both keyed on the slug
		// (ID = directory name), never the human-readable display name.
		if !cb.skillAllowed(skill.ID) {
			continue
		}
		names = append(names, skill.ID)
	}
	return names
}

// ResolveSkillName resolves name (a slug or display name, matched
// case-insensitively against either) to its canonical slug, gated by the
// full per-shelf grant model (ADR-072 D4/D4.1/D4.2). This is the single
// choke point the ADR names as the human "/<skill>" shortcut's door (D4 item
// 4) — the slash-command path itself (pkg/agent/loop.go's
// applyExplicitSkillCommand) calls this and this alone, so making IT
// shelf-aware is sufficient; the slash-command handler needs no changes of
// its own.
//
// Delegates to the shared shelf-resolution model (pkg/skills.ResolveSkillName)
// so a human typing "/<slug>" and the Skill tool's load path apply the
// IDENTICAL rule — including D4.1's project shelf: a slug discovered only in
// this workspace's mounted skills (cb.projectShelf, set via
// WithProjectShelf) resolves with no registry/builtin grant at all, exactly
// as an agent may already load it (spec #30, gap G3/A3: "a human having
// LESS access than the agent would be incoherent, and D4.1 makes the mount
// the grant for both"). When cb.projectShelf is nil (no workspace/mount
// context wired — this builder's default), behaviour is byte-for-byte
// identical to the pre-D4.1 registry/builtin-only resolution this replaced.
func (cb *ContextBuilder) ResolveSkillName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || cb.skillsLoader == nil {
		return "", false
	}

	resolved, ok, collision := skills.ResolveSkillName(cb.skillsLoader.ListSkills(), cb.skillAllowed, cb.projectShelf, name)
	if !ok {
		// FR-9.4/ADR-072 D4: an installed-but-ungranted registry/builtin slug
		// (or one that exists on no shelf at all) cannot be resolved — so it
		// cannot be activated via /<skill> nor loaded into context. This is
		// the invocation gate, distinct from the prompt-time context filter
		// (activeSkillNames).
		logger.WarnCF("agent", "skill resolution denied or not found",
			map[string]any{
				"agent_id": cb.agentID,
				"skill":    name,
			})
		return "", false
	}
	if collision != nil {
		logger.WarnCF("agent", "skill slug collision on resolution; higher-precedence shelf won",
			map[string]any{
				"agent_id": cb.agentID,
				"slug":     collision.Slug,
				"winner":   collision.WinnerDescription,
			})
	}
	return resolved.Slug, true
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		// Report stable slugs (IDs) — these are the identifiers the config
		// allowlist, DELETE route, and REST install-id validation compare against.
		skillNames = append(skillNames, s.ID)
	}
	return map[string]any{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}

// ListSkillsDetailed returns the full per-skill metadata for every installed
// skill (name, path, source, description, author, version) by delegating to the
// skills loader. Unlike ListSkillNames it is NOT filtered by the per-agent
// allowlist: GET /api/v1/skills reports the complete installed inventory so the
// management UI can show every skill regardless of which agent may invoke it.
// Returns nil when no skills loader is configured.
func (cb *ContextBuilder) ListSkillsDetailed() []skills.SkillInfo {
	if cb.skillsLoader == nil {
		return nil
	}
	return cb.skillsLoader.ListSkills()
}

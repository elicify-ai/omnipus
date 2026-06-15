package agent

import (
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

	"github.com/dapicom-ai/omnipus/pkg"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

type ContextBuilder struct {
	workspace          string
	agentID            string // agent ID for multi-agent context
	agentName          string // agent display name for multi-agent context
	skillsLoader       *skills.SkillsLoader
	memory             *MemoryStore
	toolDiscoveryBM25  bool
	toolDiscoveryRegex bool
	splitOnMarker      bool

	// skillAllowlist enforces the per-agent skill allowlist at skill resolution
	// time (FR-9.4, default-DENY). When non-nil, only skills whose name appears
	// in this set can be resolved/invoked by this agent — a skill not on the
	// allowlist cannot be loaded into context or armed via /use, even though it
	// exists on disk. When nil, no allowlist is enforced (unrestricted): this is
	// the back-compatible default for agents that do not declare an allowlist.
	// Keys are lower-cased skill names.
	skillAllowlist map[string]struct{}

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

	// resourcesInjector is an optional callback that returns additional context
	// to inject into the system prompt. Used by Ava (Agent Builder) to inject
	// available tools, skills, providers, and system defaults.
	resourcesInjector func() string

	// env carries the environment provider + any per-builder env state. Split
	// into a nested struct so context_env.go owns the mutation surface without
	// touching the core ContextBuilder definition.
	env contextBuilderEnv
}

// WithResourcesInjector sets a callback that provides additional context sections
// to inject into the system prompt (e.g., available tools catalog for Ava).
func (cb *ContextBuilder) WithResourcesInjector(fn func() string) *ContextBuilder {
	cb.resourcesInjector = fn
	return cb
}

func (cb *ContextBuilder) WithToolDiscovery(useBM25, useRegex bool) *ContextBuilder {
	cb.toolDiscoveryBM25 = useBM25
	cb.toolDiscoveryRegex = useRegex
	return cb
}

func (cb *ContextBuilder) WithSplitOnMarker(enabled bool) *ContextBuilder {
	cb.splitOnMarker = enabled
	return cb
}

func (cb *ContextBuilder) WithAgentInfo(id, name string) *ContextBuilder {
	cb.agentID = id
	cb.agentName = name
	return cb
}

// WithSkillAllowlist installs a per-agent skill allowlist that is enforced at
// skill-resolution time (FR-9.4, default-DENY). When allowlist is non-nil, only
// the named skills can be resolved or invoked by this agent; any other skill —
// even one present on disk — is denied. When allowlist is nil, no allowlist is
// enforced (unrestricted), preserving the behaviour of agents that declare no
// allowlist. Names are matched case-insensitively.
//
// Passing a non-nil but empty slice installs a deny-all allowlist (the agent
// can resolve no skills), which is the correct default-DENY behaviour for an
// agent that explicitly opts into allowlisting with an empty set.
func (cb *ContextBuilder) WithSkillAllowlist(allowlist []string) *ContextBuilder {
	if allowlist == nil {
		cb.skillAllowlist = nil
		return cb
	}
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

// skillAllowed reports whether the named skill may be resolved/invoked by this
// agent under its allowlist (FR-9.4). When no allowlist is configured (nil) all
// skills are allowed. Otherwise only names present in the allowlist are allowed.
func (cb *ContextBuilder) skillAllowed(name string) bool {
	if cb.skillAllowlist == nil {
		return true
	}
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
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:       NewMemoryStore(workspace, omnipusHome()),
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
Your workspace is at: %s
- Private memory room: %s/.omnipus/memories/ (agent-only)
- Shared memory room: workspace .omnipus/memories/ (when in a workspace session)
- Skills: %s/skills/{skill-name}/SKILL.md

## Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Artifacts over chat — MANDATORY** - NEVER paste code, HTML, scripts, CSS, JSON, YAML, configuration, or any structured output longer than 15 lines into the chat. Instead, you MUST use the write_file tool to save it to a file in your workspace, then tell the user the file path. This is a hard rule — violations make the chat unreadable. Short inline snippets (under 15 lines) for explanation are fine.

3. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

4. **Memory** — Use three dedicated tools:
   - remember(content, category[, room]) to persist a fact, decision, reference, or lesson. Use room='shared' for workspace-wide facts, 'private' for agent-only.
   - recall_memory(query[, room]) to search your durable memory. Use room='both' (default) to search all rooms.
   - retrospective(went_well, needs_improvement) to record a reviewed retrospective after confirming its contents with the user.
   Do NOT use write_file on memory files — use the remember tool to append memories.

5. **Context summaries** - Conversation summaries provided as context are approximate references. They may be incomplete or outdated. Always defer to explicit user instructions over summary content.

%s`,
		version, agentContext,
		workspacePath, workspacePath, workspacePath,
		toolDiscovery)
}

func (cb *ContextBuilder) getDiscoveryRule() string {
	if !cb.toolDiscoveryBM25 && !cb.toolDiscoveryRegex {
		return ""
	}

	var toolNames []string
	if cb.toolDiscoveryBM25 {
		toolNames = append(toolNames, `"tool_search_tool_bm25"`)
	}
	if cb.toolDiscoveryRegex {
		toolNames = append(toolNames, `"tool_search_tool_regex"`)
	}

	return fmt.Sprintf(
		`5. **Tool Discovery** - Your visible tools are limited to save memory, but a vast hidden library exists. If you lack the right tool for a task, BEFORE giving up, you MUST search using the %s tool. Do not refuse a request unless the search returns nothing. Found tools will temporarily unlock for your next turn.`,
		strings.Join(toolNames, " or "),
	)
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
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

	// Agent-specific resource injection (e.g., available tools catalog for Ava).
	if cb.resourcesInjector != nil {
		if resources := cb.resourcesInjector(); resources != "" {
			parts = append(parts, resources)
		}
	}

	// Skills - show summary, AI can read full content with read_file tool.
	// Filtered by the per-agent allowlist for progressive disclosure (FR-9.4):
	// the prompt advertises only the skills this agent is permitted to use.
	skillsSummary := cb.skillsLoader.BuildSkillsSummaryFunc(cb.skillAllowed)
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	// Memory context
	memoryContext := cb.memory.GetMemoryContext()
	if memoryContext != "" {
		parts = append(parts, "# Memory\n\n"+memoryContext)
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
	if skillFilesChangedSince(cb.skillRoots(), cb.skillFilesAtCache) {
		return true
	}

	return false
}

// fileChangedSince returns true if a tracked source file has been modified,
// newly created, or deleted since the cache was built.
//
// Four cases:
//   - existed at cache time, exists now -> check mtime
//   - existed at cache time, gone now   -> changed (deleted)
//   - absent at cache time,  exists now -> changed (created)
//   - absent at cache time,  gone now   -> no change
func (cb *ContextBuilder) fileChangedSince(path string) bool {
	// Defensive: if existedAtCache was never initialized, treat as changed
	// so the cache rebuilds rather than silently serving stale data.
	if cb.existedAtCache == nil {
		return true
	}

	existedBefore := cb.existedAtCache[path]
	info, err := os.Stat(path)
	existsNow := err == nil

	if existedBefore != existsNow {
		return true // file was created or deleted
	}
	if !existsNow {
		return false // didn't exist before, doesn't exist now
	}
	return info.ModTime().After(cb.cachedAt)
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

func (cb *ContextBuilder) buildDynamicContext(channel, chatID, senderID, senderDisplayName string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	rt := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Current Time\n%s\n\n## Runtime\n%s", now, rt)

	if channel != "" && chatID != "" {
		fmt.Fprintf(&sb, "\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}
	if senderLine := formatCurrentSenderLine(senderID, senderDisplayName); senderLine != "" {
		fmt.Fprintf(&sb, "\n\n## Current Sender\n%s", senderLine)
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildMessages(
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
	channel, chatID, senderID, senderDisplayName string,
	activeSkills ...string,
) []providers.Message {
	messages := []providers.Message{}

	// The static part (identity, bootstrap, skills, memory) is cached locally to
	// avoid repeated file I/O and string building on every call (fixes issue #607).
	// Dynamic parts (time, session, summary) are appended per request.
	// Everything is sent as a single system message for provider compatibility:
	// - Anthropic adapter extracts messages[0] (Role=="system") and maps its content
	//   to the top-level "system" parameter in the Messages API request. A single
	//   contiguous system block makes this extraction straightforward.
	// - Codex maps only the first system message to its instructions field.
	// - OpenAI-compat passes messages through as-is.
	staticPrompt := cb.BuildSystemPromptWithCache()

	// Build short dynamic context (time, runtime, session) — changes per request
	dynamicCtx := cb.buildDynamicContext(channel, chatID, senderID, senderDisplayName)

	// Compose a single system message: static (cached) + dynamic + optional summary.
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

	if summary != "" {
		summaryText := fmt.Sprintf(
			"CONTEXT_SUMMARY: The following is an approximate summary of prior conversation "+
				"for reference only. It may be incomplete or outdated — always defer to explicit instructions.\n\n%s",
			summary)
		stringParts = append(stringParts, summaryText)
		contentBlocks = append(contentBlocks, providers.ContentBlock{Type: "text", Text: summaryText})
	}

	fullSystemPrompt := strings.Join(stringParts, "\n\n---\n\n")

	// Log system prompt summary for debugging (debug mode only).
	// Read cachedSystemPrompt under lock to avoid a data race with
	// concurrent InvalidateCache / BuildSystemPromptWithCache writes.
	cb.systemPromptMutex.RLock()
	isCached := cb.cachedSystemPrompt != ""
	cb.systemPromptMutex.RUnlock()

	logger.DebugCF("agent", "System prompt built",
		map[string]any{
			"static_chars":  len(staticPrompt),
			"dynamic_chars": len(dynamicCtx),
			"total_chars":   len(fullSystemPrompt),
			"has_summary":   summary != "",
			"cached":        isCached,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := utils.Truncate(fullSystemPrompt, 500)
	logger.DebugCF("agent", "System prompt preview",
		map[string]any{
			"preview": preview,
		})

	history = sanitizeHistoryForProvider(history)

	// Single system message containing all context — compatible with all providers.
	// SystemParts enables cache-aware adapters to set per-block cache_control;
	// Content is the concatenated fallback for adapters that don't read SystemParts.
	messages = append(messages, providers.Message{
		Role:        "system",
		Content:     fullSystemPrompt,
		SystemParts: contentBlocks,
	})

	// Add conversation history
	messages = append(messages, history...)

	// Add current user message
	if strings.TrimSpace(currentMessage) != "" {
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
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	for _, msg := range history {
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

		default:
			sanitized = append(sanitized, msg)
		}
	}

	// Second pass: ensure every assistant message with tool_calls has matching
	// tool result messages following it. This is required by strict providers
	// like DeepSeek that enforce: "An assistant message with 'tool_calls' must
	// be followed by tool messages responding to each 'tool_call_id'."
	final := make([]providers.Message, 0, len(sanitized))
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
	}

	return final
}

func (cb *ContextBuilder) AddToolResult(
	messages []providers.Message,
	toolCallID, toolName, result string,
) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
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
		if !cb.skillAllowed(skill.Name) {
			continue
		}
		names = append(names, skill.Name)
	}
	return names
}

func (cb *ContextBuilder) ResolveSkillName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || cb.skillsLoader == nil {
		return "", false
	}

	for _, skill := range cb.skillsLoader.ListSkills() {
		if strings.EqualFold(skill.Name, name) {
			// FR-9.4: tool-resolution default-DENY enforcement. A skill that
			// exists on disk but is not in this agent's allowlist cannot be
			// resolved — so it can be neither armed via /use nor loaded into
			// context. This is the invocation gate, distinct from the prompt-time
			// context filter (activeSkillNames).
			if !cb.skillAllowed(skill.Name) {
				logger.WarnCF("agent", "skill resolution denied by per-agent allowlist",
					map[string]any{
						"agent_id": cb.agentID,
						"skill":    skill.Name,
					})
				return "", false
			}
			return skill.Name, true
		}
	}

	return "", false
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
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

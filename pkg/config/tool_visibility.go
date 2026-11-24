// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// The one-time "tool-loading conversion" (Skills & Tools C-6, C-19 step 3,
// Dataset D7): the old manifest switches become stored model-visibility
// levels, so what each agent is sent after the conversion equals what it was
// sent before. After the conversion the old switches are never read again
// (ST-Q4 B).

// legacyFullManifestToolNames is the pre-conversion "always sent in full"
// list (ADR-071/ADR-090, pkg/tools/manifest.go at 4e34afa416). It is
// migration input only: the conversion reads it once to give these tools the
// full level. It is never consulted at run time.
var legacyFullManifestToolNames = map[string]struct{}{
	"AskUserQuestion": {}, "Skill": {}, "append_file": {}, "bash": {},
	"create_plan": {}, "create_task": {}, "delegate": {}, "edit_file": {},
	"execute_plan": {}, "fetch_url": {}, "get_workspace": {}, "goal_claim": {},
	"grep": {}, "inspect_session": {}, "library_list": {}, "library_read": {},
	"list_agents": {}, "list_directory": {}, "list_jobs": {}, "list_mounts": {},
	"list_tasks": {}, "message_parent": {}, "plan_correct": {}, "read_file": {},
	"recall_conversation": {}, "recall_memory": {}, "remember": {}, "search_web": {},
	"answer_request": {}, "send_file": {}, "send_message": {}, "set_goal": {}, "set_todos": {},
	"stop_plan": {}, "update_task": {}, "write_file": {},
}

// legacyPreviewedToolNames was the one-line "previewed" tier.
var legacyPreviewedToolNames = map[string]struct{}{"serve_web": {}}

// VisibilityConversionServer is one MCP server's old state.
type VisibilityConversionServer struct {
	ID       string `json:"id"`
	Deferred *bool  `json:"deferred"`
}

// VisibilityConversionInput is the pre-upgrade state the conversion reads:
// tools.manifest.compressed, tools.manifest.preview_all_lazy,
// tools.mcp.discovery.enabled and each server's deferred, plus the static
// tools, servers and skills to give levels.
type VisibilityConversionInput struct {
	Compressed       bool
	PreviewAllLazy   bool
	DiscoveryEnabled bool
	StaticTools      []string
	Servers          []VisibilityConversionServer
	Skills           []string
}

// PlanToolVisibilityConversion returns the starting levels of Dataset D7:
// static tools by name, MCP servers as mcp_<server>_* (so an offline server
// and tools a server adds later get the level), skills by key. Pure: a replay
// recomputes the same plan.
func PlanToolVisibilityConversion(in VisibilityConversionInput) map[string]string {
	out := map[string]string{}
	for _, name := range in.StaticTools {
		out[name] = staticToolStartingLevel(in, name)
	}
	for _, s := range in.Servers {
		if s.ID == "" {
			continue
		}
		out[MCPServerWildcardKey(s.ID)] = mcpServerStartingLevel(in, s)
	}
	for _, k := range in.Skills {
		if k = NormalizeSkillKey(k); k != "" {
			out[k] = VisibilityNameAndDescription
		}
	}
	return out
}

func staticToolStartingLevel(in VisibilityConversionInput, name string) string {
	if lvl, ok := LockedToolVisibility(name); ok {
		return lvl
	}
	if !in.Compressed {
		return VisibilityFull
	}
	if _, ok := legacyFullManifestToolNames[name]; ok {
		return VisibilityFull
	}
	if _, ok := legacyPreviewedToolNames[name]; ok {
		return VisibilityNameAndDescription
	}
	if in.PreviewAllLazy {
		return VisibilityNameAndDescription
	}
	return VisibilityHidden
}

// mcpServerStartingLevel mirrors the old request pipeline: in a compressed
// request every MCP tool was lazy and search-only (hidden), except that
// preview_all_lazy previewed a server registered visible; with compressed
// off an effectively deferred server (discovery on, deferred unset or true;
// pkg/agent/loop_mcp.go::serverIsDeferred) was hidden, any other full.
func mcpServerStartingLevel(in VisibilityConversionInput, s VisibilityConversionServer) string {
	deferred := in.DiscoveryEnabled && (s.Deferred == nil || *s.Deferred)
	if in.Compressed {
		if in.PreviewAllLazy && !deferred {
			return VisibilityNameAndDescription
		}
		return VisibilityHidden
	}
	if deferred {
		return VisibilityHidden
	}
	return VisibilityFull
}

// ResolveToolVisibility returns the stored level of a tool: a locked tool's
// fixed level, else the exact key, else the most specific wildcard key
// (mcp_<server>_*), else hidden (a tool with no stored level is still
// findable through ToolSearch; the self-heal gives every catalog tool a
// level).
func ResolveToolVisibility(name string, visibility map[string]string) string {
	if lvl, ok := LockedToolVisibility(name); ok {
		return lvl
	}
	if v := string(ResolvePolicyFromMap(name, visibility)); IsModelVisibility(v) {
		return v
	}
	return VisibilityHidden
}

// SkillLoadKey returns the C-23 key whose permission applies to a load of
// slug (C-19): a granted installed skill wins over a same-slug project skill
// (pkg/skills/shelf.go::ResolveSkillName, ADR-072 D4.2); otherwise the
// project skill of projectWorkspace runs, under its own key. "" when nothing
// resolves.
func SkillLoadKey(slug string, grantedInstalled bool, projectWorkspace string) string {
	if grantedInstalled {
		return NormalizeSkillKey(slug)
	}
	if projectWorkspace != "" {
		return ProjectSkillKey(projectWorkspace, slug)
	}
	return ""
}

package skills

import "strings"

// DiscoveredTool represents a tool that was auto-discovered from an installed skill's SKILL.md.
// These are candidate tools; the policy engine must approve each one before use (SEC-04, SEC-07).
type DiscoveredTool struct {
	// SkillName is the name of the skill that declared this tool.
	SkillName string
	// ToolName is the declared tool identifier (from allowed-tools).
	ToolName string
	// Source is where the parent skill lives ("workspace", "global", "builtin").
	Source string
}

// DiscoverAllTools scans all installed skills via loader and returns every tool
// declared in their SKILL.md allowed-tools frontmatter field.
//
// Discovery does NOT grant permissions — callers must run results through the
// policy engine before making tools available to agents (SEC-04, SEC-07).
func DiscoverAllTools(loader *SkillsLoader) []DiscoveredTool {
	skills := loader.ListSkills()
	var discovered []DiscoveredTool

	for _, info := range skills {
		meta := loader.getSkillMetadata(info.Path)
		if meta == nil || len(meta.AllowedTools) == 0 {
			continue
		}
		for _, tool := range meta.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			discovered = append(discovered, DiscoveredTool{
				SkillName: info.ID,
				ToolName:  tool,
				Source:    info.Source,
			})
		}
	}

	return discovered
}

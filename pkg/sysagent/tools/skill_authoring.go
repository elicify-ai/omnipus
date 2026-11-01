// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// The authoring tools create_skill and remove_skill are retired (skills spec
// r15 §5.1, FR-025): a new skill is a write_file of <new>/SKILL.md and a
// deletion is edit_skill action=delete, both through skills.ApplyChange.

// resolveProjectShelfWithCollisions builds the ADR-072 D4.1 project shelf
// (skill discovery merged over every live mount) for workspace wsID — the
// call's resolved target (resolveTargetWorkspace), falling back to this
// installation's default workspace when empty, mirroring
// pkg/agent/loop_env.go's wireProjectShelfResolvers. It returns no shelf when
// no workspace resolves or the workspace has no mounts.
//
// An unreadable or partly dropped mount record is NOT "no project shelf"
// (silent-failure-hunter SF-01): it returns a READ_FAILED refusal, because the
// workspace may hold a project copy of the slug. A non-empty name inspects
// that slug in every project; an empty name requires a complete shelf scan.
func resolveProjectShelfWithCollisions(deps *Deps, wsID, name string) (skills.ProjectShelf, []skills.SlugCollision, *tools.ToolResult) {
	if deps == nil || strings.TrimSpace(deps.Home) == "" {
		return nil, nil, nil
	}
	home := deps.Home

	wsID = strings.TrimSpace(wsID)
	if wsID == "" {
		def, err := workspacepkg.ResolveDefaultID(home)
		if err != nil || def == "" {
			return nil, nil, nil
		}
		wsID = def
	}

	mounts, ok, dropped := workspacepkg.LoadMountsWithDropStatus(home, wsID)
	if !ok || dropped {
		return nil, nil, tools.ErrorResult(errorJSON("READ_FAILED",
			fmt.Sprintf("the project folders of workspace %q could not be read completely, so its project skills are unknown", wsID),
			"Repair the workspace's folders in the app, then read the skill again before retrying"))
	}
	if len(mounts) == 0 {
		return nil, nil, nil
	}

	pm := make([]skills.ProjectMount, 0, len(mounts))
	for _, m := range mounts {
		pm = append(pm, skills.ProjectMount{Name: m.Name, Root: m.HostPath})
	}
	shelf, collisions, err := skills.LookupProjectSkillChecked(pm, name)
	if err != nil {
		return nil, nil, tools.ErrorResult(errorJSON("READ_FAILED",
			fmt.Sprintf("the project skills of workspace %q could not be inspected: %v", wsID, err),
			"Restore access to the project folder, then read the skill again before retrying"))
	}
	return shelf, collisions, nil
}

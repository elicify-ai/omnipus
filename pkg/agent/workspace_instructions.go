// Omnipus — per-turn workspace instructions injection for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// injectWorkspaceInstructions inserts the workspace instructions note as a
// "system" role message at index 1 of msgs — immediately after the system
// prompt at index 0, before the manifest note and conversation history.
//
// Call-order contract (loop.go injection site):
//
//	callMessages = injectWorkspaceInstructions(callMessages, note)     // inserts at [1]
//	callMessages = injectWebRenderingNote(callMessages, webNote)       // also inserts at [1], shifts workspace to [2]
//	callMessages = injectManifestNote(callMessages, manifestNote)      // also inserts at [1], shifts web-note to [2], workspace to [3]
//
// Final order on a web-chat turn (all three notes present):
//
//	[0] system prompt · [1] manifest note · [2] web-rendering note · [3] workspace instructions · [4] scratchpad · [5+] history
//
// On a non-web turn, injectWebRenderingNote is a no-op (buildWebRenderingNote returns ""),
// so workspace instructions land at [2] when the manifest note is present, or at [1] when absent.
// A scratchpad note (present when the acting agent has active tasks) is inserted at [1]
// BEFORE this call, so it ends up further back; this helper governs only the
// manifest↔web-note↔workspace relative order, not absolute positions.
//
// Returns msgs unchanged when note == "" or len(msgs) == 0.
func injectWorkspaceInstructions(msgs []providers.Message, note string) []providers.Message {
	if note == "" || len(msgs) == 0 {
		return msgs
	}
	out := make([]providers.Message, 0, len(msgs)+1)
	out = append(out, msgs[0])
	out = append(out, providers.Message{Role: "system", Content: note})
	out = append(out, msgs[1:]...)
	return out
}

// buildWorkspaceInstructionsNote resolves the workspace ID, reads the
// workspace's AGENT.md (Project Instructions), composes ADR-072 D7's second
// source on this same rail — each of that workspace's live mounts' own root
// CLAUDE.md/AGENTS.md, ordered by mount name and labelled with it
// (skills.ComposeProjectInstructions) — and returns the combined per-turn
// injection string, the workspace's own instructions first per D7's stated
// ordering ("the operator's intent outranks the repository's"). Returns ""
// when:
//
//   - workspaceID is "" and no default workspace exists (ErrNoDefault — normal
//     for a fresh install with no workspaces yet).
//   - The AGENT.md file is absent or empty AND no mount contributes a
//     readable root instruction file.
//
// A real I/O error reading AGENT.md (not "not exist") is logged at Warn
// level and treated as empty rather than aborting the whole note — a mount's
// instructions are independent content and must not be suppressed by a
// failure reading the workspace's own file. Never fails the turn over an
// advisory context note.
func buildWorkspaceInstructionsNote(workspaceID string) string {
	home := omnipusHome()

	id := workspaceID
	if id == "" {
		var err error
		id, err = workspace.ResolveDefaultID(home)
		if err != nil {
			if !errors.Is(err, workspace.ErrNoDefault) {
				logger.WarnCF("agent", "workspace instructions: could not resolve default workspace ID", map[string]any{
					"error": err.Error(),
				})
			}
			return ""
		}
	}

	ownContent, err := workspace.ReadInstructions(home, id)
	if err != nil {
		logger.WarnCF("agent", "workspace instructions: could not read AGENT.md", map[string]any{
			"workspace_id": id,
			"error":        err.Error(),
		})
		ownContent = ""
	}
	ownContent = strings.TrimSpace(ownContent)

	projectNote := buildProjectInstructionsNote(home, id)

	var parts []string
	if ownContent != "" {
		parts = append(parts, ownContent)
	}
	if projectNote != "" {
		parts = append(parts, projectNote)
	}
	if len(parts) == 0 {
		return ""
	}
	return "# Workspace Instructions\n\n" + strings.Join(parts, "\n\n---\n\n")
}

// buildProjectInstructionsNote composes ADR-072 D7's mounted-project
// instructions block for workspaceID's live mounts (skills.
// ComposeProjectInstructions over one skills.ProjectInstructionMount per
// mount), or "" when the workspace has no mounts, its mount record is
// unreadable, or none of its mounts carry a readable root CLAUDE.md/
// AGENTS.md — D6: "silent when there is nothing to find", so this is not
// escalated to a WARN except when the composed result was actually
// truncated at the byte cap (D7's own "silently truncating instructions is
// worse than not loading them" — the marker is already in the returned
// text; the WARN additionally makes it discoverable via logs).
func buildProjectInstructionsNote(home, workspaceID string) string {
	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok || len(mounts) == 0 {
		return ""
	}

	pm := make([]skills.ProjectInstructionMount, 0, len(mounts))
	for _, m := range mounts {
		pm = append(pm, skills.ProjectInstructionMount{Name: m.Name, Root: m.HostPath})
	}
	composed, truncated := skills.ComposeProjectInstructions(pm)
	if composed == "" {
		return ""
	}
	if truncated {
		logger.WarnCF("agent", "project instructions truncated at byte cap", map[string]any{
			"workspace_id": workspaceID,
			"max_bytes":    skills.MaxInstructionsBytes,
		})
	}
	return composed
}

// Omnipus — per-turn workspace instructions injection for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
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
// workspace's AGENT.md (Project Instructions), and returns the per-turn
// injection string. Returns "" when:
//
//   - workspaceID is "" and no default workspace exists (ErrNoDefault — normal
//     for a fresh install with no workspaces yet).
//   - The AGENT.md file is absent or empty.
//
// A real I/O error (not "not exist") is logged at Warn level and causes "" to
// be returned — never failing the turn over an advisory context note.
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

	content, err := workspace.ReadInstructions(home, id)
	if err != nil {
		logger.WarnCF("agent", "workspace instructions: could not read AGENT.md", map[string]any{
			"workspace_id": id,
			"error":        err.Error(),
		})
		return ""
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return "# Workspace Instructions\n\n" + content
}

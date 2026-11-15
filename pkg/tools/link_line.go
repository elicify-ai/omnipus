// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// linkLinePrefix starts the ready-made link line a tool result hands the
// agent (chat-links spec, D-Q6).
const linkLinePrefix = "link: "

// LinkLine is the one producer of a tool result's "link:" line (chat-links
// spec, "link: line"; FR-011). It returns
// "link: omnipus://file/<workspace>/<path>" for a file that resolves into a
// workspace Library or one of its mounts, and "" otherwise.
//
// It returns "" unless the turn's channel is web chat (D-A): an external
// recipient would get a dead link that also discloses the workspace id. It
// does not require the root chat session — a steered child's channel is
// stamped web chat, and its parent model is who needs the link; only
// show_to_user's auto-open checks the root session.
//
// home is $OMNIPUS_HOME (callers without their own pass
// config.OmnipusHomeDir()); hostPath is the file's absolute host path.
// A path outside every Library, or one whose name the SPA would refuse to
// render as a link, gets no line (R4).
func LinkLine(ctx context.Context, home, hostPath string) string {
	if ToolChannel(ctx) != webChannelName {
		return ""
	}
	ref := library.ResolveHostPath(library.ResolveScope{
		Home:          home,
		WorkspaceID:   ToolWorkspaceID(ctx),
		AllWorkspaces: coreagent.HasCrossWorkspaceSetupAuthority(ToolAgentID(ctx)),
	}, hostPath)
	if !ref.InLibrary() {
		return ""
	}
	link, ok := library.EncodeLibraryLink(ref.WorkspaceID, ref.Rel)
	if !ok {
		return ""
	}
	return linkLinePrefix + link
}

// withLinkLine appends line to text on its own line; an empty line leaves
// text unchanged.
func withLinkLine(text, line string) string {
	if line == "" {
		return text
	}
	return text + "\n" + line
}

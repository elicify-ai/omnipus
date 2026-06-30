// Omnipus — per-turn web-surface rendering guidance for the agent loop.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// webRenderingNote is the ephemeral system note injected on web-chat turns.
//
// The Omnipus web chat renders Mermaid fenced code blocks as diagrams (both the
// live stream and the finalized transcript — see src/components/chat/
// markdown-text.tsx and historical-markdown.tsx). Every other surface (CLI,
// Telegram, Discord, Matrix, …) shows the raw ```mermaid fence as plain text, so
// this encouragement is gated to the web surface only — otherwise a Telegram user
// would receive an unrendered code block.
//
// It is delivered as a PER-TURN ephemeral system note rather than baked into the
// cached system prompt because the static prompt is cached per-agent, and one
// agent (e.g. Mia) serves both web and messaging channels; baking it in would
// leak the guidance onto non-web turns.
const webRenderingNote = "## Rendering (web chat)\n\n" +
	"You are responding in the Omnipus web chat, which renders Mermaid diagrams. " +
	"When a picture communicates more clearly than prose — a flow, a sequence, a " +
	"state machine, a hierarchy, an ER/class relationship, a timeline, or an " +
	"architecture sketch — render it as a ```mermaid fenced code block instead of " +
	"describing it in words. Keep diagrams small and focused, and use them only when " +
	"they genuinely serve the conversation; prefer prose for simple answers. " +
	"When you color-code nodes with a classDef `fill`, ALWAYS set a contrasting text " +
	"`color` in the same classDef (dark text on light fills, light text on dark fills) " +
	"so labels stay readable — e.g. `classDef ok fill:#16a34a,color:#0a0a0b`."

// buildWebRenderingNote returns the web-rendering guidance note when the turn
// originates from the web chat surface, and "" for every other surface. The gate
// reuses commands.SurfaceForChannel so the channel→surface mapping keeps a single
// source of truth (pkg/commands/surface.go).
func buildWebRenderingNote(channel string) string {
	if commands.SurfaceForChannel(channel) == commands.SurfaceWeb {
		return webRenderingNote
	}
	return ""
}

// injectWebRenderingNote inserts the note as a "system" role message at index 1
// of msgs — immediately after the system prompt — mirroring
// injectWorkspaceInstructions. It is advisory; its position relative to the other
// ephemeral system notes (scratchpad, manifest, workspace instructions) is not
// behaviorally significant. Returns msgs unchanged when note == "" or len(msgs) == 0.
func injectWebRenderingNote(msgs []providers.Message, note string) []providers.Message {
	if note == "" || len(msgs) == 0 {
		return msgs
	}
	out := make([]providers.Message, 0, len(msgs)+1)
	out = append(out, msgs[0])
	out = append(out, providers.Message{Role: "system", Content: note})
	out = append(out, msgs[1:]...)
	return out
}

// Omnipus — tool-manifest optimization helpers for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// manifestSessionID derives the map key used to bucket loaded-tool state for a
// session. It mirrors the fallback logic in pkg/tools/handoff.go:250-253:
// prefer the transcript session ID when available (it is a stable, unique
// session directory name); fall back to the session key when the transcript is
// disabled (TranscriptSessionID == ""). Both the writer (markLoaded closure in
// the agent loop) and the readers (buildCompressedToolDefs, buildToolManifestNote)
// must call this helper with the same two inputs so they always resolve to the
// same bucket — a mismatch causes loaded tools to become invisible to the model.
func manifestSessionID(transcriptID, sessionKey string) string {
	if transcriptID != "" {
		return transcriptID
	}
	return sessionKey
}

// buildCompressedToolDefs partitions policyFilteredTools into full/lazy/infra
// tiers and returns provider defs for only the always-callable tools plus any
// lazy tools that have already been loaded for this session.
//
//   - full (ManifestFull): always sent — high-frequency core tools.
//   - infra (ManifestInfra): load_tool + search_tools_*; always sent so the
//     agent can always discover and load its lazy tools.
//   - lazy (ManifestLazy): sent ONLY if in the session's loaded set.
//
// Critical invariant: infra tools are FORCE-INCLUDED from the agent's full
// registry (ts.agent.Tools) even when FilterToolsByPolicy removed them.
// A deny-by-default agent (e.g. Ava) would otherwise lose load_tool, making
// lazy tools permanently unreachable. Infra tools are registration-gated (they
// are only present when Compressed=true), not policy-gated.
//
// When cfg.Tools.Manifest.Compressed is false, the caller must use
// tools.ToolsToProviderDefs directly — this helper is only called on the
// compressed code-path.
func (al *AgentLoop) buildCompressedToolDefs(ts *turnState, policyFiltered []tools.Tool) []providers.ToolDefinition {
	sessionID := manifestSessionID(ts.opts.TranscriptSessionID, ts.sessionKey)
	loaded := al.sessionLoadedTools(sessionID)

	// Track which infra tools are already present in policyFiltered so we don't
	// double-add them. Build the sent list in one pass.
	infraInFiltered := make(map[string]bool)
	sent := make([]tools.Tool, 0, len(policyFiltered))
	for _, t := range policyFiltered {
		switch tools.ToolManifestTier(t.Name()) {
		case tools.ManifestFull:
			sent = append(sent, t)
		case tools.ManifestInfra:
			infraInFiltered[t.Name()] = true
			sent = append(sent, t)
		case tools.ManifestLazy:
			if loaded[t.Name()] {
				sent = append(sent, t)
			}
		}
	}

	// Force-include any infra tools that were stripped by policy (deny-by-default
	// agents). Look them up directly from the agent's full registry.
	if ts.agent != nil && ts.agent.Tools != nil {
		for _, infraName := range tools.InfraManifestToolNames() {
			if infraInFiltered[infraName] {
				continue // already included above
			}
			if t, ok := ts.agent.Tools.Get(infraName); ok {
				sent = append(sent, t)
			}
		}
	}

	return tools.ToolsToProviderDefs(sent)
}

// buildToolManifestNote renders the compact "More tools" manifest block for
// injection into the system context. It lists the lazy tools that are not yet
// loaded for this session, grouped by category. Returns "" when there are no
// unloaded lazy tools (nothing to inject).
//
// The returned string is ephemeral — rebuilt every turn, never persisted.
func (al *AgentLoop) buildToolManifestNote(ts *turnState, policyFiltered []tools.Tool) string {
	sessionID := manifestSessionID(ts.opts.TranscriptSessionID, ts.sessionKey)
	loaded := al.sessionLoadedTools(sessionID)

	// Collect only the lazy tier tools; BuildCompressedManifest filters further
	// (infra/full are excluded inside it, but we pass all policyFiltered to keep
	// the helper self-contained as per its contract).
	return tools.BuildCompressedManifest(policyFiltered, loaded)
}

// injectManifestNote inserts note as a "system" role message at index 1 of
// msgs (immediately after the system prompt at index 0, before conversation
// history). Returns msgs unchanged when note == "" or len(msgs) == 0.
// This is a pure helper extracted from the runTurn injection site to allow
// unit-testing the position and role invariants without driving a full turn.
func injectManifestNote(msgs []providers.Message, note string) []providers.Message {
	if note == "" || len(msgs) == 0 {
		return msgs
	}
	out := make([]providers.Message, 0, len(msgs)+1)
	out = append(out, msgs[0])
	out = append(out, providers.Message{Role: "system", Content: note})
	out = append(out, msgs[1:]...)
	return out
}

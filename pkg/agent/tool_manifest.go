// Omnipus — tool-manifest optimization helpers for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

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
	sessionID := ts.opts.TranscriptSessionID
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
		for _, infraName := range []string{"load_tool", "search_tools_regex", "search_tools_bm25"} {
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
	sessionID := ts.opts.TranscriptSessionID
	loaded := al.sessionLoadedTools(sessionID)

	// Collect only the lazy tier tools; BuildCompressedManifest filters further
	// (infra/full are excluded inside it, but we pass all policyFiltered to keep
	// the helper self-contained as per its contract).
	return tools.BuildCompressedManifest(policyFiltered, loaded)
}

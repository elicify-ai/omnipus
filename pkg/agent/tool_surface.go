// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-15: the stored levels decide what each turn advertises.
// The levels are read once at the start of a turn (a change applies from
// the next turn of every chat). Definitions a chat was already sent stay in
// that chat when their level drops (they are retained per session bucket,
// like ToolSearch loads); a Deny still removes them, because only the
// policy-allowed tools are ever retained.

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// toolLevelTurnPlan is the turn plan of one tool stored at level (C-1, C-15),
// read through the same tools.ToolSurface a turn builds from its stored
// levels (readTurnToolSurface): whether its definition is sent, its "More
// tools" line and whether ToolSearch finds it. Level "deny" stands for the
// tool's effective deny, which overrides every level.
func toolLevelTurnPlan(level, name, description string) (inRequest bool, manifestLine string, searchable bool) {
	surface := tools.ToolSurface{Levels: map[string]string{name: level}}
	p := surface.Plan(name, description, level == string(config.ToolPolicyDeny))
	return p.InRequest, p.ManifestLine, p.Searchable
}

// readTurnToolSurface returns the tool levels of the loaded config for one
// turn (C-15, C-16: a saved change applies to running agents once the config
// is reloaded). A loop without a loaded config reads the stored levels; an
// unreadable file then keeps the pre-conversion tiers and is logged.
func (al *AgentLoop) readTurnToolSurface() tools.ToolSurface {
	if cfg := al.GetConfig(); cfg != nil {
		return tools.ToolSurface{Levels: cfg.Sandbox.ToolVisibility}
	}
	surface, err := tools.ReadToolSurface(omnipusHome())
	if err != nil {
		logger.ErrorCF("agent", "stored tool levels unreadable; this turn keeps the pre-conversion tiers",
			map[string]any{"error": err.Error()})
		return tools.ToolSurface{}
	}
	return surface
}

// manifestCompressed reports whether this turn sends only part of the
// catalog with a "More tools" block: always once levels are stored, else
// the pre-conversion switch.
func (ts *turnState) manifestCompressed(cfg *config.Config) bool {
	return ts.toolSurface.Leveled() || (cfg != nil && cfg.Tools.Manifest.Compressed)
}

// pruneAdvertisedTools drops every remembered definition that is no longer
// policy-allowed and returns the remaining set (a copy).
func (al *AgentLoop) pruneAdvertisedTools(ts *turnState, allowed []tools.Tool) map[string]bool {
	out := map[string]bool{}
	if ts.manifestBucket() == "" {
		return out
	}
	present := make(map[string]bool, len(allowed))
	for _, t := range allowed {
		present[t.Name()] = true
	}
	state := al.promptCacheSession(ts)
	state.mu.Lock()
	defer state.mu.Unlock()
	for name := range state.advertised {
		if !present[name] {
			delete(state.advertised, name)
			continue
		}
		out[name] = true
	}
	return out
}

// retainedTools is the union of the session's ToolSearch loads and, once
// levels are stored, the definitions the chat was already sent.
func (al *AgentLoop) retainedTools(ts *turnState, allowed []tools.Tool) map[string]bool {
	bucket := ts.manifestBucket()
	loaded := al.pruneLoadedTools(bucket, allowed)
	if !ts.toolSurface.Leveled() {
		return loaded
	}
	for name := range al.pruneAdvertisedTools(ts, allowed) {
		loaded[name] = true
	}
	return loaded
}

// recordAdvertisedToolsLocked remembers the definitions a request of this
// chat carried, so a later level change does not take them from that chat.
// The set lives in the chat's prompt-cache state (session close sweeps it);
// the caller holds state.mu.
func (al *AgentLoop) recordAdvertisedToolsLocked(state *sessionPromptCache, defs []providers.ToolDefinition) {
	if len(defs) == 0 {
		return
	}
	if state.advertised == nil {
		state.advertised = make(map[string]bool, len(defs))
	}
	for _, d := range defs {
		state.advertised[d.Function.Name] = true
	}
}

// manifestLoadedTools is the set the "More tools" block leaves out: the
// session's ToolSearch loads and, once levels are stored, the definitions
// the chat was already sent.
func (al *AgentLoop) manifestLoadedTools(ts *turnState) map[string]bool {
	return al.bucketLoadedTools(ts.toolSurface, ts.manifestBucket())
}

// bucketLoadedTools is manifestLoadedTools for a session bucket key.
func (al *AgentLoop) bucketLoadedTools(surface tools.ToolSurface, bucket string) map[string]bool {
	loaded := al.sessionLoadedTools(bucket)
	if !surface.Leveled() || bucket == "" {
		return loaded
	}
	value, ok := al.promptCacheSessions.Load(bucket)
	if !ok {
		return loaded
	}
	state, ok := value.(*sessionPromptCache)
	if !ok {
		return loaded
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for name := range state.advertised {
		loaded[name] = true
	}
	return loaded
}

// Omnipus — tool-manifest optimization helpers for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// manifestSessionID derives the map key used to bucket loaded-tool state for a
// session. It mirrors the fallback logic in pkg/tools/handoff.go's
// resolveSessionID: prefer the transcript session ID when available (it is a
// stable, unique session directory name); fall back to the session key when the
// transcript is disabled (TranscriptSessionID == ""). Both the writer
// (markToolsLoaded, driven from the `ToolSearch` closure in the agent loop) and
// the readers (buildCompressedToolDefs, buildToolManifestNote) must call this
// helper with the same two inputs so they always resolve to the same bucket —
// a mismatch causes loaded tools to become invisible to the model.
//
// # The bucket is the ACTING session, never a routing key (ADR-057)
//
// The returned key identifies whose loaded-tool set this is, so it must be the
// id of the session actually running the turn. ADR-057 forbids using a routing
// session id as a tool-manifest bucket, and the reason is a concrete cost, not
// tidiness: a delegated child that resolved to its PARENT's bucket would start
// every delegation pre-loaded with whatever lazy tools the parent had loaded,
// paying their token and latency cost on a turn that may need none of them,
// and it would write its own loads back into the parent's bucket.
//
// That is exactly what happened before ADR-057, and it happened at the CALLER,
// not here: spawnSubTurn built the child's processOptions with
// `TranscriptSessionID: parentTS.transcriptSessionID`, so this helper — behaving
// correctly, preferring the transcript id — handed the child the parent's key.
// Once each child owns a real session and carries its own transcript id, the
// same preference gives the child its own empty bucket with no change here.
// The invariant this helper must keep is therefore narrow and worth stating:
// it derives a bucket from the two ids it is GIVEN and never widens the scope
// of either. Any future caller that passes a chat/routing id as transcriptID
// reintroduces the shared bucket regardless of what this function does.
//
// A both-empty call returns "", which is the deliberate no-op key:
// markToolsLoaded and sessionLoadedTools both reject "" rather than creating a
// shared unkeyed bucket.
func manifestSessionID(transcriptID, sessionKey string) string {
	if transcriptID != "" {
		return transcriptID
	}
	return sessionKey
}

// infraToolGetter is the minimal surface ensureInfraToolsExecutable needs from
// an agent's tool registry (decoupled for testability).
type infraToolGetter interface {
	Get(name string) (tools.Tool, bool)
}

// ensureInfraToolsExecutable guarantees the manifest infra tools (`ToolSearch`)
// are present in the policy-filtered slice and marked "allow" in policyMap so
// the execution gate authorizes them for EVERY agent — including deny-by-default
// agents (Ava/Mia/Ray).
//
// Unification note (#438): the single authoritative resolver
// tools.EffectiveToolPolicy (via FilterToolsByPolicy) now force-allows infra
// tools UNCONDITIONALLY, so by the time this function runs the infra tool is
// already in policyFiltered with policyMap[name]=="allow" in the common path.
// This function therefore degrades to a safe idempotent backstop: it re-adds an
// infra tool only if the resolver somehow omitted it (e.g. a test that builds a
// policy map by hand and passes a registry whose Get returns the tool).
// Reachability does not depend on the manifest being compressed (when
// compressed is off, ToolSearch is stripped from the SENT defs on the
// non-compressed path in runTurn, so its allow verdict is moot and surfacing
// nothing to the model). agentTools==nil is still a no-op (nothing to look the
// tool up from).
func ensureInfraToolsExecutable(
	agentTools infraToolGetter,
	policyFiltered []tools.Tool,
	policyMap map[string]string,
) []tools.Tool {
	if agentTools == nil {
		return policyFiltered
	}
	for _, infraName := range tools.InfraManifestToolNames() {
		if _, ok := policyMap[infraName]; ok {
			continue // already authorized (by EffectiveToolPolicy or a prior pass)
		}
		if t, ok := agentTools.Get(infraName); ok {
			policyFiltered = append(policyFiltered, t)
			policyMap[infraName] = "allow"
		}
	}
	return policyFiltered
}

// stripInfraToolDefs returns the subset of tools with manifest infra tools
// (ToolSearch) removed. Used on the NON-compressed defs path: ToolSearch is the
// driver of the compressed manifest mechanism and has no function when
// compression is off, so the model never sees it there — regardless of what
// the agent's own tool-policy map resolves for it.
//
// Unification note (#438): the single resolver now force-allows infra
// UNCONDITIONALLY, so FilterToolsByPolicy keeps ToolSearch in the filtered slice
// for EVERY agent. This path strips it so it is not surfaced uncompressed. For
// an agent whose tools mostly resolve to deny, this matches the old behavior
// (the old filter dropped ToolSearch, so it was never sent uncompressed). For
// an agent whose tools mostly resolve to allow it is a deliberate, narrow
// change: the old path DID send ToolSearch uncompressed, the new path does not
// — correct, because an uncompressed turn has no ToolSearch affordance (no
// manifest block telling the model to use it). The strip touches ONLY infra
// tools; every other tool's surfaced verdict is unchanged.
func stripInfraToolDefs(in []tools.Tool) []tools.Tool {
	out := make([]tools.Tool, 0, len(in))
	for _, t := range in {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestInfra {
			continue
		}
		out = append(out, t)
	}
	return out
}

// buildCompressedToolDefs partitions policyFilteredTools into full/lazy/infra
// tiers and returns provider defs for only the always-callable tools plus any
// lazy tools that have already been loaded for this session.
//
//   - full (ManifestFull): always sent — high-frequency core tools.
//   - infra (ManifestInfra): the unified `tools` tool; always sent so the
//     agent can always discover and load its lazy tools.
//   - lazy (ManifestLazy): sent ONLY if in the session's loaded set.
//
// Critical invariant: infra tools are FORCE-INCLUDED from the agent's full
// registry (ts.agent.Tools) even when FilterToolsByPolicy removed them.
// A deny-by-default agent (e.g. Ava) would otherwise lose `tools`, making
// lazy tools permanently unreachable. Infra tools are registration-gated (they
// are only present when Compressed=true or MCP discovery is on), not policy-gated.
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

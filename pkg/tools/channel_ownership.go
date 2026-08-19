// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// ChannelOwnership answers the two questions send_message must ask before it
// can pick a destination (ADR-065).
//
// A channel instance is owned by exactly one (workspace, agent) pair — the
// pair an operator selects in the channel Configure panel, stored as
// ChannelInstanceConfig.WorkspaceID + Identity{kind:"agent"}. ADR-029 enforces
// that pair strictly on the way IN; ADR-065 makes it govern the way OUT too.
//
// # Why this is an interface rather than a config read
//
// pkg/tools cannot import pkg/config's channel topology without dragging the
// gateway's world into every tool. More importantly, the ONLY caller that may
// answer these questions is the one holding the live config, so injecting it
// keeps the tool honest about what it does not know: a nil ChannelOwnership
// means "ownership is unknown here", and the tool degrades to the turn's own
// conversation rather than pretending to enforce something it cannot check.
type ChannelOwnership interface {
	// OwnerOf reports which (workspace, agent) owns a channel instance.
	//
	// bound is false for an instance that carries no workspace binding at all
	// — the operator's "No workspace (global default routing)" choice, and the
	// synthetic instances like webchat that have no ChannelInstanceConfig.
	// Those are NOT owned by anyone and are deliberately left alone
	// (ADR-065 spec FR-9, FR-10).
	OwnerOf(instanceID string) (workspaceID, agentID string, bound bool)

	// OwnedBy lists the instances owned by this pair, sorted for determinism.
	OwnedBy(workspaceID, agentID string) []string
}

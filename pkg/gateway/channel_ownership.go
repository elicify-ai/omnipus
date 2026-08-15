// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// channelOwnershipResolver is the gateway's concrete implementation of
// tools.ChannelOwnership (ADR-065). It answers "who owns this channel
// instance?" straight from live config rather than a snapshot, because
// config is swapped at runtime (hot reload) and a captured *config.Config
// would go stale the moment an operator edits a channel's binding.
//
// This mirrors the getConfig func() *config.Config accessor pattern already
// used by scheduledRunner (pkg/gateway/schedules.go) and the SSE handler
// (pkg/gateway/sse.go) rather than holding a snapshot.
type channelOwnershipResolver struct {
	getConfig func() *config.Config
}

// newChannelOwnershipResolver builds a resolver over the given config
// accessor. getConfig is read once per call so every OwnerOf/OwnedBy call
// observes the live config, including any hot reload that happened between
// calls.
func newChannelOwnershipResolver(getConfig func() *config.Config) *channelOwnershipResolver {
	return &channelOwnershipResolver{getConfig: getConfig}
}

// Compile-time assertion that channelOwnershipResolver satisfies the seam
// pkg/tools defines (pkg/tools/channel_ownership.go) without pkg/tools having
// to import pkg/config or pkg/gateway.
var _ tools.ChannelOwnership = (*channelOwnershipResolver)(nil)

// OwnerOf reports which (workspace, agent) owns a channel instance, per
// config.ChannelInstanceConfig.IsWorkspaceBound (the single existing
// predicate for "is this instance bound?" — ADR-029 FR-029). bound is false
// for:
//   - a nil resolver, nil getConfig, nil accessor result, or nil/empty
//     cfg.Channels (nothing to look up against);
//   - an empty instanceID;
//   - an instance id with no entry in cfg.Channels at all — including
//     synthetic channels like "webchat" that never get a
//     ChannelInstanceConfig entry;
//   - an instance whose entry exists but is not fully workspace-bound (the
//     operator's "No workspace (global default routing)" choice, or a
//     half-bound state — though ValidateChannels rejects half-bound entries
//     at config load, so in practice this is just "no workspace_id set").
//
// instanceID is looked up case-insensitively (lower-cased before the map
// lookup) to match the one other place in the codebase that resolves an
// arbitrary instance id against cfg.Channels: pkg/agent/loop.go's
// inboundInstanceID, whose doc comment states "The result is lower-cased to
// match the config map keys." Channel instance keys are themselves always
// canonically lowercase — config.ValidateInstanceKey (pkg/config/config.go)
// rejects any key whose namespaced slug contains an uppercase character, and
// a bare type key can only be one of the all-lowercase knownChannelTypes
// entries — so this lower-casing only ever affects the CALLER-supplied
// instanceID, never how entries are stored.
func (r *channelOwnershipResolver) OwnerOf(instanceID string) (workspaceID, agentID string, bound bool) {
	if r == nil || r.getConfig == nil {
		return "", "", false
	}
	instanceID = strings.ToLower(strings.TrimSpace(instanceID))
	if instanceID == "" {
		return "", "", false
	}
	cfg := r.getConfig()
	if cfg == nil || cfg.Channels == nil {
		return "", "", false
	}
	inst, ok := cfg.Channels[instanceID]
	if !ok || !inst.IsWorkspaceBound() {
		return "", "", false
	}
	return inst.WorkspaceID, inst.Identity.ID, true
}

// OwnedBy lists the channel instances owned by the given (workspaceID,
// agentID) pair, sorted lexicographically for determinism — callers surface
// this list in an error message an LLM reads, so a stable order matters.
//
// workspaceID and agentID are compared with exact (case-sensitive) string
// equality. Unlike instance ids, workspace ids and agent ids are opaque
// identifiers compared exactly everywhere else they appear in this codebase
// (e.g. the default-agent resolution ladder's "ac.ID ==
// cfg.Agents.Defaults.DefaultAgentID" in pkg/routing/route.go, and
// ChannelIdentity.ID itself, which config.ChannelIdentity.Validate never
// case-folds) — there is no established convention of case-insensitive
// workspace/agent id comparison to match, so this does not invent one.
//
// A nil resolver, nil getConfig, nil accessor result, nil/empty
// cfg.Channels, or empty workspaceID/agentID all yield an empty (nil) slice
// rather than panicking.
func (r *channelOwnershipResolver) OwnedBy(workspaceID, agentID string) []string {
	if r == nil || r.getConfig == nil {
		return nil
	}
	if workspaceID == "" || agentID == "" {
		return nil
	}
	cfg := r.getConfig()
	if cfg == nil || cfg.Channels == nil {
		return nil
	}
	var owned []string
	for instanceID, inst := range cfg.Channels {
		if !inst.IsWorkspaceBound() {
			continue
		}
		if inst.WorkspaceID != workspaceID || inst.Identity.ID != agentID {
			continue
		}
		owned = append(owned, instanceID)
	}
	sort.Strings(owned)
	return owned
}

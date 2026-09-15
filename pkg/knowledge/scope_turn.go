// Omnipus — ADR-067 FR-052/FR-053: resolving a TURN's knowledge scope.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// scope_turn.go — which workspace a knowledge tool call belongs to.
//
// # The silent-empty defect this closes
//
// Every knowledge tool used to resolve its scope as
// `ResolveScope(home, tools.ToolWorkspaceID(ctx))`. ResolveScope returns an
// EMPTY scope for an empty workspace id — correctly, and by design: an
// unknown workspace must never widen into "everything". But an empty
// workspace id is NOT always an unknown workspace.
//
// Two ordinary kinds of turn carry no workspace id at all:
//
//   - a CLI / ProcessDirect turn (`omnipus <agent> "..."`), whose
//     bus.InboundMessage is built without one;
//   - a scheduled task or heartbeat turn.
//
// Neither sets tools.WithWorkspaceID, which is deliberate — see
// pkg/agent/workspace_reroot.go, which re-roots the turn's WORK DIRECTORY into
// the agent's workspace without touching workspace_id or memory-room routing.
// The consequence for knowledge was that every such turn resolved an empty
// scope and the agent was told, in words, "No knowledge base is available in
// this workspace" — while the operator's mount sat right there, granted, on
// the workspace the very same turn had just been re-rooted into.
//
// That is the identical failure pkg/tools/resolvepath.go already carries a
// twenty-line comment and a fix for, on the filesystem-mount side: the agent
// got the work dir but not the grants that go with it. This file states the
// repair once, for knowledge, and every knowledge tool goes through it.
//
// # Why the fallback cannot widen anything
//
//   - It is consulted ONLY when the turn carries no workspace id. A turn that
//     names a workspace resolves exactly that workspace, unchanged — there is
//     no path here by which workspace A's turn reaches workspace B.
//   - It resolves the SAME way the work dir was resolved, through
//     workspace.FindForAgentPreferring(home, agentID, ""), so the work dir and
//     the knowledge scope can never disagree about which workspace a turn is
//     rooted in.
//   - An agent that belongs to no workspace still resolves nothing, and the
//     empty scope stands.
//   - The GRANT itself is still workspace.AllowedMountRoots inside
//     ResolveScope. This file decides only WHICH workspace to ask about; it
//     never adds a root.

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TurnWorkspaceID is the workspace a tool call belongs to.
//
// It is tools.ToolWorkspaceID(ctx) whenever the turn carries one. When it does
// not — CLI/ProcessDirect and scheduled/heartbeat turns — it is the workspace
// the calling agent is rooted in, resolved exactly as the turn's work
// directory was. It is "" when neither answers, and "" still means an empty
// scope.
func TurnWorkspaceID(ctx context.Context, home string) string {
	if wsID := tools.ToolWorkspaceID(ctx); wsID != "" {
		return wsID
	}
	if home == "" {
		return ""
	}
	agentID := tools.ToolAgentID(ctx)
	if agentID == "" {
		return ""
	}
	if wsID, found := workspace.FindForAgentPreferring(home, agentID, ""); found {
		return wsID
	}
	return ""
}

// ResolveTurnScope is ResolveScope for a tool call, using TurnWorkspaceID.
//
// It returns the scope AND the workspace id it resolved, because the audit
// record and the refusal text both need to name the workspace the call was
// actually judged against — not the empty string the context happened to
// carry.
func ResolveTurnScope(ctx context.Context, home string) (Scope, string) {
	wsID := TurnWorkspaceID(ctx, home)
	return ResolveScope(home, wsID), wsID
}

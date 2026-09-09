// Omnipus — the single browsing-key resolution point (ADR-075 D1.11, FR-007)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ResolveBrowsingKey decides which browser this turn's tools address. It is the
// ONLY function permitted to construct a BrowsingKey. Deterministic, pure apart
// from the workspace-file reads FindForAgentPreferring / FindAllForAgent
// perform.
//
// Ladder (ADR-075 D1.11), evaluated in order — three rungs, no fourth:
//  1. tools.ToolWorkspaceID(ctx), WHEN THE AGENT IS ON THAT WORKSPACE'S TEAM
//     -> ws:<that id>
//  2. the agent's workspace membership resolves UNAMBIGUOUSLY -> ws:<that id>
//  3. otherwise -> zero key + ErrNoBrowsingContext
//
// RUNG 1 IS A PREFERENCE, NOT AN INSTRUCTION, and the difference is a security
// boundary rather than a nicety. tools.ToolWorkspaceID(ctx) is the workspace the
// CHAT was bound to when it started (pkg/agent/loop.go stamps it from
// ts.opts.WorkspaceID), and a chat outlives the team it was opened under: remove
// an agent from that workspace's team mid-conversation and the label on disk
// does not change. Every agent on a workspace shares the operator's live logins
// for every site that workspace has visited, so honouring the stale label would
// hand a browser — cookies, sessions, the lot — to an agent that is no longer on
// the team. Membership is therefore re-checked on EVERY resolution, never
// stamped once and trusted.
//
// It is also what keeps the two sides of the live browser panel honest. The
// panel resolves through ResolveBrowsingKeyForAgent with the attaching chat
// session's workspace as its preference (pkg/gateway/browser_ws.go's
// sessionWorkspaceID -> AgentLoop.BrowserManagerForAgent); the agent's own
// tools resolve through here. Both now enter the SAME function,
// resolveBrowsingKeyForAgent, with the same preference and the same rules —
// deliberately ONE check rather than two that can drift, because when they did
// drift the operator watched one browser while the agent drove another and
// neither side reported anything wrong.
//
// "Unambiguously" is load-bearing and is FR-033: when the agent is on the
// CoreTeam of two or more workspaces and no usable preferred id was supplied,
// the sorted-first tie-break that FindForAgent applies for FILESYSTEM re-rooting
// is NOT applied here, because here it would silently choose which set of live
// logins the turn acts with. Rung 2 refuses instead, with ErrNoBrowsingContext,
// and logs a WARN naming the candidates.
//
// Rung 1+2 mirror pkg/tools/resolvepath.go's precedent so the browser and the
// work dir never disagree about which workspace a scheduled/heartbeat turn is
// rooted in — pkg/agent/loop.go's filesystem re-rooting already treats the
// turn-carried workspace id as a tie-break over identity-based membership
// (FindForAgentPreferring) for exactly this reason, and this is the browser
// catching up to it. There is NO rung 4: a fallback constant re-creates the
// exact isolation regression ADR D1.11 rejects.
func ResolveBrowsingKey(ctx context.Context, home string) (BrowsingKey, error) {
	return resolveBrowsingKeyForAgent(home, tools.ToolAgentID(ctx), tools.ToolWorkspaceID(ctx))
}

// resolveBrowsingKeyForAgent is the WHOLE ladder — rung 1's preference, rung 2's
// membership, rung 3's refusal — and it is the single place workspace membership
// is checked for the browser. Both entry points funnel through it:
// ResolveBrowsingKey (an agent's own tools, preference = the turn's workspace)
// and ResolveBrowsingKeyForAgent (the gateway's live panel, preference = the
// attaching chat session's workspace). Adding a second membership check at
// either call site would give this project two answers to "whose logins does
// this act with", which is the drift the one-function shape exists to prevent.
//
// A preferred id that the agent really belongs to is not an ambiguity: the
// caller has already said which one it means, and FindForAgentPreferring
// confirms the agent is on that specific workspace's CoreTeam (or is an
// implicit member of every workspace) before it is honoured. A preferred id the
// agent is NOT on is treated exactly as if none had been supplied — it falls
// through to the membership ladder, which is strictly more conservative than
// honouring it and never resolves to a workspace the agent has no claim on.
func resolveBrowsingKeyForAgent(home, agentID, preferredWorkspaceID string) (BrowsingKey, error) {
	if agentID == "" {
		return BrowsingKey{}, ErrNoBrowsingContext
	}
	if preferredWorkspaceID != "" {
		if id, ok := workspace.FindForAgentPreferring(home, agentID, preferredWorkspaceID); ok &&
			id == preferredWorkspaceID {
			return newBrowsingKey(id)
		}
		// Not a silent downgrade. The named workspace is where the chat (or the
		// panel) believes it is; the agent is not on its team, so the request is
		// resolved from the agent's own membership instead. An operator who
		// removed the agent from that team mid-conversation is the ordinary
		// cause, and they will not connect a chat that quietly changed browsers
		// to a team edit they made an hour ago unless it is said out loud.
		logger.WarnCF(
			"browser",
			"the workspace this request names does not have this agent on its team — "+
				"resolving from the agent's own membership instead of handing over that "+
				"workspace's browser and its live logins",
			map[string]any{
				"agent_id":           agentID,
				"named_workspace_id": preferredWorkspaceID,
			},
		)
	}
	candidates, implicit := workspace.FindAllForAgent(home, agentID)
	switch {
	case len(candidates) == 0:
		return BrowsingKey{}, ErrNoBrowsingContext
	case len(candidates) == 1:
		return newBrowsingKey(candidates[0])
	}
	// FR-033. A System Agent is an implicit member of EVERY workspace, so
	// multi-membership is its normal state and not an operator mistake — but
	// the consequence for the browser is identical (which workspace's live
	// logins does this turn act with?), so it refuses too. The WARN
	// distinguishes the two so an operator is not told to fix something that
	// is not broken.
	logger.WarnCF(
		"browser",
		"cannot decide which workspace's browser this turn addresses — refusing rather than choosing one",
		map[string]any{
			"agent_id":        agentID,
			"candidates":      strings.Join(candidates, ","),
			"implicit_member": implicit,
		},
	)
	return BrowsingKey{}, ErrNoBrowsingContext
}

// newBrowsingKey is the private constructor every rung funnels through. It is
// the ONLY place a BrowsingKey is minted, and it is where FR-037's path-segment
// validation happens.
//
// FR-037: the check runs on the RENDERED segment "ws-<id>", not on the bare id.
// That is the string a filesystem will see (ADR-075 D1.8's flat profile
// layout), and the two differ in a way that matters — a bare id of ".." is
// obviously unsafe, but so is an id of "./x", and an id containing a separator
// escapes the profile root whatever the prefix. Validating the bare id would
// check a string nothing ever opens.
//
// Any refusal is ErrNoBrowsingContext, never a bespoke error: a turn whose
// workspace id cannot be rendered as a directory has no browser of its own, and
// that is the same operator-facing situation as having no workspace at all.
func newBrowsingKey(workspaceID string) (BrowsingKey, error) {
	if workspaceID == "" {
		return BrowsingKey{}, ErrNoBrowsingContext
	}
	seg := browsingProfileSegmentPrefix + workspaceID
	if !isSinglePathSegment(seg) {
		logger.WarnCF(
			"browser",
			"workspace id does not render to a single path segment — refusing to open a browser for it",
			map[string]any{"workspace_id": workspaceID, "segment": seg},
		)
		return BrowsingKey{}, ErrNoBrowsingContext
	}
	return BrowsingKey{s: browsingKeyPrefix + workspaceID}, nil
}

// isSinglePathSegment reports whether s is exactly one filesystem path segment:
// no separator of either flavour, no traversal, no NUL, and unchanged by
// filepath.Clean. Both separators are rejected on every platform deliberately —
// a Linux gateway must not mint a key that becomes a traversal the moment the
// same $OMNIPUS_HOME is opened on Windows.
func isSinglePathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\\x00") {
		return false
	}
	if strings.HasPrefix(s, ".") {
		// A leading dot is not itself traversal, but "..foo" and hidden
		// directories are not workspace ids and a key that produces one is a
		// mistake worth refusing loudly.
		return false
	}
	return filepath.Clean(s) == s
}

// ResolveBrowsingKeyForAgent resolves the browsing key for an agent when there
// is no turn context to read — registration time and the gateway's
// agent-addressed live-panel path (FR-017).
//
// preferredWorkspaceID is the caller's own notion of "the current one" (the
// attaching chat session's workspace). Supplying one that the agent really
// belongs to is NOT the ambiguity FR-033 refuses: the caller has already said
// which workspace it means. Leaving it empty on a multi-workspace agent IS,
// and this refuses with ErrNoBrowsingContext exactly as ResolveBrowsingKey
// does.
func ResolveBrowsingKeyForAgent(home, agentID, preferredWorkspaceID string) (BrowsingKey, error) {
	return resolveBrowsingKeyForAgent(home, agentID, preferredWorkspaceID)
}

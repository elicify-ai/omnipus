// Omnipus — the single browsing-key resolution point (ADR-072 D1.11, FR-007)
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
// from the workspace-file read FindAllForAgent performs.
//
// Ladder (ADR-072 D1.11), evaluated in order — three rungs, no fourth:
//  1. tools.ToolWorkspaceID(ctx) != ""   -> ws:<that id>
//  2. the agent's workspace membership resolves UNAMBIGUOUSLY -> ws:<that id>
//  3. otherwise                          -> zero key + ErrNoBrowsingContext
//
// "Unambiguously" is load-bearing and is FR-033: when the agent is on the
// CoreTeam of two or more workspaces and no preferred id was supplied, the
// sorted-first tie-break that FindForAgent applies for FILESYSTEM re-rooting is
// NOT applied here, because here it would silently choose which set of live
// logins the turn acts with. Rung 2 refuses instead, with ErrNoBrowsingContext,
// and logs a WARN naming the candidates.
//
// Rung 1+2 mirror pkg/tools/resolvepath.go's precedent so the browser and the
// work dir never disagree about which workspace a scheduled/heartbeat turn is
// rooted in. There is NO rung 4: a fallback constant re-creates the exact
// isolation regression ADR D1.11 rejects.
func ResolveBrowsingKey(ctx context.Context, home string) (BrowsingKey, error) {
	if wsID := tools.ToolWorkspaceID(ctx); wsID != "" {
		return newBrowsingKey(wsID)
	}
	agentID := tools.ToolAgentID(ctx)
	return resolveBrowsingKeyForAgent(home, agentID, "")
}

// resolveBrowsingKeyForAgent is rung 2, factored out so the gateway's
// agent-addressed path (AgentLoop.BrowserManagerForAgent, FR-017) reaches the
// SAME refusal rules with a caller-supplied preferred workspace id — the
// attaching chat session's own workspace. A preferred id that the agent really
// belongs to is not an ambiguity: the caller has already said which one it
// means.
func resolveBrowsingKeyForAgent(home, agentID, preferredWorkspaceID string) (BrowsingKey, error) {
	if agentID == "" {
		return BrowsingKey{}, ErrNoBrowsingContext
	}
	if preferredWorkspaceID != "" {
		if id, ok := workspace.FindForAgentPreferring(home, agentID, preferredWorkspaceID); ok &&
			id == preferredWorkspaceID {
			return newBrowsingKey(id)
		}
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
// That is the string a filesystem will see (ADR-072 D1.8's flat profile
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

// Omnipus — Browser ownership keys (ADR-075 D1, FR-002b/FR-007/FR-080)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"errors"
	"strings"
)

// BrowsingKey is the identity of ONE browser: one Chrome process, one profile
// directory, one BrowserManager, one tab set. It replaces the deleted shared
// session constant as the thing every browser tool addresses. Constructed ONLY by
// ResolveBrowsingKey — there is deliberately no exported literal constructor
// and no zero-value default, so a caller cannot mint a shared browser by
// accident (ADR-075 D1.11).
//
// There is exactly ONE shape: "ws:<workspaceID>". The D1.10 ruling (2026-08-31)
// removed the unattended shape; do not reintroduce a second kind without
// reopening that ruling.
//
// A BrowsingKey names a BROWSER, not a tab set. Under D1.9c (2026-09-02) the
// tab sets live one level down, inside the manager this key resolves to: one
// per SESSION that has browsed, plus one owned by the workspace for the
// operator's own tabs.
// See TabOwner and FR-080 — a key alone does not tell you whose tabs you are
// looking at, and code that assumes it does merges every session's tabs.
//
// The key is the SESSION's, not the agent's: switching a chat from Mia to Jim
// does not move, hide or duplicate a tab, and two sessions on one workspace
// never see each other's. Anything reading "per agent" here is pre-D1.9c.
//
// The "ws:" prefix is RETAINED DELIBERATELY, and not as future-proofing for a
// second shape — §5 forbids adding one (round-2 MIN-103). It is a namespace
// marker: a workspace id is a bare ULID, and a bare ULID appearing in an audit
// event, a WARN line or a LiveKeys() dump is indistinguishable from a session
// id, a task id or an agent id, all of which are ULIDs in this system and all
// of which appear in the same log lines. "ws:01J..." is self-describing at the
// point a human reads it, which is the only place it matters. WorkspaceID()
// exists for the two consumers that need the bare id back — the profile
// directory path (FR-037) and the audit event's workspace field (FR-027).
type BrowsingKey struct{ s string }

// browsingKeyPrefix is the ONE shape's namespace marker. See BrowsingKey.
const browsingKeyPrefix = "ws:"

// browsingProfileSegmentPrefix is the RENDERED path segment a BrowsingKey
// becomes on disk (ADR-075 D1.8's flat layout: "<profileRoot>/ws-<id>"). FR-037
// validates THIS string — not the bare workspace id — because the segment is
// what a filesystem sees.
const browsingProfileSegmentPrefix = "ws-"

func (k BrowsingKey) String() string { return k.s }
func (k BrowsingKey) IsZero() bool   { return k.s == "" }

// WorkspaceID returns the workspace this key names, without the prefix. Used
// by audit and by the profile-directory path; never a branch in isolation
// logic. Its result is a path segment (FR-037), so §5's path-segment
// invariant applies to it.
func (k BrowsingKey) WorkspaceID() string {
	if len(k.s) <= len(browsingKeyPrefix) {
		return ""
	}
	return k.s[len(browsingKeyPrefix):]
}

// ProfileSegment returns the single path segment this key's profile directory
// occupies under the profile root — "ws-<workspaceID>", ADR-075 D1.8's FLAT
// layout. It is the exact string FR-037's segment validation ran against in
// ResolveBrowsingKey, so a key that exists is a key whose segment is safe.
func (k BrowsingKey) ProfileSegment() string {
	if k.IsZero() {
		return ""
	}
	return browsingProfileSegmentPrefix + k.WorkspaceID()
}

// ErrNoBrowsingContext is the D1.11 named failure. It MUST be returned — never
// swallowed into a shared browser, never mapped to a constant, never
// nil-with-empty. Its Error() text is a behavioural contract (FR-008).
var ErrNoBrowsingContext = errors.New(
	"browser: this turn is not rooted in a workspace, so it has no browser of its own; " +
		"add this agent to a workspace's team, or run the request in a workspace chat")

// TabOwner names WHOSE tab set a browser operation addresses, inside the one
// browser a BrowsingKey names (ADR-075 D1.9c, operator ruling 2026-09-02).
// This type is the explicit carrier of the SESSION dimension that used to live
// only in the accident of one BrowserManager per agent — FR-080.
//
// Two shapes, and deliberately no third:
//
//	TabOwnerSession(transcriptSessionID)
//	                        the tabs opened in that chat.  Visible and
//	                        drivable by whichever agent the chat is currently
//	                        on — switching Mia to Jim moves nothing.  Never
//	                        visible to another session.
//	TabOwnerWorkspace       the tabs the OPERATOR opened through the live
//	                        panel.  Visible to every agent on the workspace;
//	                        drivable by the operator, and by an agent that
//	                        simply ACTS on it — acquisition is IMPLICIT and
//	                        has no surface (FR-070, §0.7).
//
// It resolves to the manager's sessions-map key, so the map holds one entry
// per SESSION that has browsed plus at most one workspace entry. There is no
// "all tabs" owner: a tool that wants both sets asks for both and says which
// is which, because "whose tab is this" is exactly the question ADR-075 §1.1
// records an agent getting wrong.
//
// There is deliberately NO TabOwnerAgent. Keying on the agent is the
// SUPERSEDED D1.9a shape (FR-048 → FR-080); reintroducing it splits one
// chat's tabs across the agents that took turns in it.
//
// The id is transcriptSessionID and NEVER routingSessionID (§5, FR-080):
// routingSessionID is inherited verbatim through a delegation subtree
// (pkg/agent/subturn.go's spawnSubTurn), so it would merge every descendant's
// tabs into the root's.
//
// TabOwnerSession("") IS NOT AN OWNER — it is a named failure. An empty
// transcript session id is an ordinary, reachable state on several turn types
// (§5's non-behaviour, FR-080), and minting an owner from it gives every
// transcript-less turn on the workspace one shared tab set, which is the
// silent merge this type exists to prevent. Constructing it returns
// ErrNoTabOwner rather than a usable value; the browser tool reports it and
// opens nothing.
type TabOwner struct{ s string }

// tabOwnerWorkspaceMarker is the workspace-owned set's rendering. It carries no
// id, so it can never collide with a session owner (which always renders with
// tabOwnerSessionPrefix in front of a non-empty id).
const (
	tabOwnerWorkspaceMarker = "operator"
	tabOwnerSessionPrefix   = "session:"
)

// ErrNoTabOwner is returned when the turn carries no transcriptSessionID and
// therefore has no tab set of its own. Like ErrNoBrowsingContext it must be
// RETURNED, never swallowed into a shared or workspace-owned set.
var ErrNoTabOwner = errors.New(
	"browser: this turn has no transcript session, so it has no tabs of its own")

// TabOwnerSession names the tab set belonging to one chat session. The id is
// the turn's transcriptSessionID — NEVER its routingSessionID, which a whole
// delegation subtree shares (see TabOwner's doc comment).
//
// An empty id is a NAMED FAILURE (ErrNoTabOwner), never a fall-through to the
// workspace-owned set: a transcript-less turn that silently landed on the
// operator's tabs would be able to drive them, which is precisely the implicit
// merge FR-080 exists to prevent.
func TabOwnerSession(transcriptSessionID string) (TabOwner, error) {
	if transcriptSessionID == "" {
		return TabOwner{}, ErrNoTabOwner
	}
	return TabOwner{s: tabOwnerSessionPrefix + transcriptSessionID}, nil
}

// TabOwnerWorkspace names the tab set the OPERATOR owns — the tabs opened
// through the live panel. Visible to every agent on the workspace. Total: it
// cannot fail, because it names no id.
func TabOwnerWorkspace() TabOwner { return TabOwner{s: tabOwnerWorkspaceMarker} }

// IsWorkspace reports whether this owner is the operator's workspace-owned set
// rather than one session's own.
func (o TabOwner) IsWorkspace() bool { return o.s == tabOwnerWorkspaceMarker }

// IsZero reports whether this owner was never constructed. A zero TabOwner is
// not a usable owner — it is what TabOwnerSession("") returns alongside
// ErrNoTabOwner.
func (o TabOwner) IsZero() bool { return o.s == "" }

// String renders the owner for logs and for sessionKey. Not a stable wire
// format.
func (o TabOwner) String() string { return o.s }

// sessionKey is the manager-level lookup: one BrowsingKey plus one TabOwner.
// It is what replaces the deleted shared constant at every call site (FR-002b) — NOT the
// BrowsingKey on its own, which would merge every SESSION on the workspace
// into one tab set (§0.2a).
//
// The BrowsingKey half is redundant INSIDE one manager (there is exactly one
// manager per key), and it is rendered anyway: the same string is the
// LiveViewRegistry's map key and appears verbatim in WARN lines, audit events
// and panel frames, where a bare "session:<ulid>" would not say which browser
// it belonged to.
func sessionKey(k BrowsingKey, o TabOwner) string {
	return k.s + "/" + o.s
}

// ParseBrowsingKeyString rebuilds a BrowsingKey from its rendered form
// ("ws:<workspaceID>"). It exists for the ONE class of caller that legitimately
// holds a rendered key and no way to re-resolve it: bookkeeping maps outside
// this package are keyed by the STRING (pkg/agent's browserMgrs, the pool's own
// instances map), so a prune pass that finds a dead entry has a string and
// needs the key back to close its browser.
//
// It is NOT a back door around ResolveBrowsingKey and must never be used to
// mint a key from a workspace id an agent supplied. It re-runs the SAME
// validation newBrowsingKey does — the rendered profile segment must be a
// single, traversal-free path segment — so a malformed string is refused with
// ErrNoBrowsingContext rather than becoming a directory name.
func ParseBrowsingKeyString(s string) (BrowsingKey, error) {
	if !strings.HasPrefix(s, browsingKeyPrefix) {
		return BrowsingKey{}, ErrNoBrowsingContext
	}
	return newBrowsingKey(strings.TrimPrefix(s, browsingKeyPrefix))
}

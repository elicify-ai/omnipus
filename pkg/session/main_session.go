// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U1 (FR-002, DEL-01): the computed main session id.
//
// A main session is the one standing session for an eligible
// (workspace, agent) pair. Its id is DERIVED, never minted: the store
// computes it from the pair, so every caller resolves the same identity
// without a lookup table or a stored address. The join is "+", which cannot
// occur in either validated component, so the mapping from pair to id is
// injective — a "-" join would collide ("a-b"+"c" and "a"+"b-c" would both
// stringify to main-session-a-b-c).
package session

import (
	"fmt"
	"regexp"
	"strings"
)

// MainSessionIDMaxBytes is the maximum length, in UTF-8 bytes, of a computed
// main session id. A session id is the directory name of its store entry, so
// this bound is the filesystem's own one-path-component limit (POSIX
// NAME_MAX: ext4, xfs, btrfs, APFS) — a longer name cannot be persisted at
// all. Above the limit the store REFUSES visibly: it never hashes, truncates
// or mints a second short id, because a second name for the same session
// would be a second identity.
const MainSessionIDMaxBytes = 255

// MainSessionIDPrefix is the literal every computed main session id starts with.
const MainSessionIDPrefix = "main-session-"

// MainSessionIDSeparator joins the workspace and agent components. It is "+"
// because neither component's alphabet allows it, so the two parts of a pair
// can never bleed into each other.
const MainSessionIDSeparator = "+"

// mainSessionComponentMaxBytes is the per-component bound the computed id
// inherits (spec C-MAIN: "Workspace/agent bounds stay 128"). It mirrors the
// two canonical validators it must agree with —
// pkg/gateway/rest_workspaces.go::validWorkspaceID and
// pkg/agentstore/state.go::ValidateAgentID — which both apply the same
// 128-byte cap over the same path-component alphabet.
const mainSessionComponentMaxBytes = 128

// mainSessionComponentPattern is the path-component alphabet both canonical
// validators use. Duplicated here rather than imported because pkg/session is
// the lower layer: the store must be able to reject an unsafe component on
// its own, without depending on a gateway handler having validated first.
var mainSessionComponentPattern = regexp.MustCompile(`^[A-Za-z0-9]+(?:[-_.][A-Za-z0-9]+)*$`)

// validateMainSessionComponent applies the workspace/agent component rule:
// non-empty, at most 128 bytes, and inside the path-component alphabet.
// Accepts "." and ".."? No — the pattern requires an alphanumeric first
// character, so both are rejected here as well as by validateSessionID.
func validateMainSessionComponent(kind, id string) error {
	switch {
	case id == "":
		return fmt.Errorf("main session: %s id is empty", kind)
	case len(id) > mainSessionComponentMaxBytes:
		return fmt.Errorf("main session: %s id %q is %d bytes, over the %d-byte limit",
			kind, id, len(id), mainSessionComponentMaxBytes)
	case !mainSessionComponentPattern.MatchString(id):
		return fmt.Errorf("main session: %s id %q is not a valid path component", kind, id)
	}
	return nil
}

// MainSessionID returns the computed main session id for a
// (workspace, agent) pair, or a visible refusal.
//
// The refusal is the spec's BDD-01.4 boundary: each component is checked
// against its own existing bound and alphabet, and the concatenation against
// MainSessionIDMaxBytes. Above that cap the caller must store NOTHING — no
// directory, no replacement id — which is why this returns an error rather
// than a shortened id.
func MainSessionID(workspaceID, agentID string) (string, error) {
	if err := validateMainSessionComponent("workspace", workspaceID); err != nil {
		return "", err
	}
	if err := validateMainSessionComponent("agent", agentID); err != nil {
		return "", err
	}
	id := MainSessionIDPrefix + workspaceID + MainSessionIDSeparator + agentID
	if len(id) > MainSessionIDMaxBytes {
		return "", fmt.Errorf(
			"main session: the main session id for workspace %q and agent %q is %d bytes, over the %d-byte limit; "+
				"shorten the workspace or agent id (no truncated or hashed id is minted)",
			workspaceID, agentID, len(id), MainSessionIDMaxBytes)
	}
	return id, nil
}

// SplitMainSessionID reverses MainSessionID for an id it recognises: it
// returns the (workspace, agent) pair encoded in a computed main id, and
// ok=false for any other id. Callers use it to decide whether a session id is
// a main pair's id at all before resolving eligibility for that pair.
func SplitMainSessionID(id string) (workspaceID, agentID string, ok bool) {
	rest, found := strings.CutPrefix(id, MainSessionIDPrefix)
	if !found {
		return "", "", false
	}
	sep := strings.Index(rest, MainSessionIDSeparator)
	if sep < 0 {
		return "", "", false
	}
	return rest[:sep], rest[sep+len(MainSessionIDSeparator):], true
}

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// resolve_test.go — FR-007, FR-008, FR-033 and FR-037's segment half.
//
// These are the tests for the ONE function permitted to construct a
// BrowsingKey. Everything downstream of it assumes that a key it holds names a
// workspace the turn is genuinely rooted in, and that no code path can produce
// a key by any other route.

// writeWorkspace writes one workspace file naming coreTeam.
func writeWorkspace(t *testing.T, home, id string, coreTeam ...string) {
	t.Helper()
	writeWorkspaceAs(t, home, id+".json", id, coreTeam...)
}

// writeWorkspaceAs writes a workspace file whose FILENAME and whose "id" field
// are chosen independently.
//
// findAllForAgent returns the id out of the JSON, not the filename, so an id
// that could never be a filename ("../escape") is still a value the resolver
// can be handed — which is the only route by which a non-segment id reaches
// newBrowsingKey now that rung 1 is a preference rather than an instruction.
// Naming the file after such an id would write outside the workspaces dir and
// test nothing.
func writeWorkspaceAs(t *testing.T, home, filename, id string, coreTeam ...string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.Marshal(map[string]any{"id": id, "core_team": coreTeam})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), body, 0o600))
}

// turnCtx builds a tool-call context carrying the agent id and, optionally, the
// turn's own workspace id — the two inputs ResolveBrowsingKey's ladder reads.
func turnCtx(agentID, workspaceID string) context.Context {
	ctx := context.Background()
	if agentID != "" {
		ctx = tools.WithAgentID(ctx, agentID)
	}
	if workspaceID != "" {
		ctx = tools.WithWorkspaceID(ctx, workspaceID)
	}
	return ctx
}

// TestResolveBrowsingKey_Ladder walks the three rungs in order. There is no
// fourth rung, and that is the point: a fallback constant would re-create the
// one-browser-for-everyone identity ADR-072 exists to remove, and it would do so
// silently, because a shared browser looks exactly like a working browser until
// two workspaces' logins are in one cookie jar.
//
// RUNG 1 IS A PREFERENCE. Two of these cases used to assert the opposite — that
// the turn's own workspace id "wins outright" and is "not overridden by
// membership" — and that was the defect, not the contract. The turn's workspace
// id is the label the CHAT was stamped with when it started; remove the agent
// from that workspace's team afterwards and the label does not change. Honouring
// it unchecked handed a workspace's browser, and therefore the operator's live
// logins for every site that workspace has visited, to an agent that is not on
// its team. Membership is re-checked on every resolution.
func TestResolveBrowsingKey_Ladder(t *testing.T) {
	home := t.TempDir()
	writeWorkspace(t, home, "ws-of-solo", "solo")
	writeWorkspace(t, home, "ws-also-of-solo", "solo", "second")

	cases := []struct {
		name    string
		ctx     context.Context
		wantKey string
		wantErr error
	}{
		{
			// The legitimate use of rung 1: "solo" really is on both, and the
			// turn names which one. That is not an ambiguity — the caller has
			// already said which workspace it means.
			name:    "rung 1 — the turn's own workspace id picks among the agent's memberships",
			ctx:     turnCtx("solo", "ws-also-of-solo"),
			wantKey: "ws:ws-also-of-solo",
		},
		{
			// The defect, inverted. "solo" is on no such workspace, so the
			// label is not honoured and the resolution falls back to the
			// membership ladder. It must NOT come back as
			// ws:a-workspace-solo-was-removed-from.
			name:    "rung 1 is refused when the agent is not on the workspace the turn names",
			ctx:     turnCtx("second", "ws-of-solo"),
			wantKey: "ws:ws-also-of-solo",
		},
		{
			name:    "rung 2 — no turn workspace, one unambiguous membership",
			ctx:     turnCtx("second", ""),
			wantKey: "ws:ws-also-of-solo",
		},
		{
			name:    "rung 3 — an agent on no workspace has no browser of its own",
			ctx:     turnCtx("stranger", ""),
			wantErr: ErrNoBrowsingContext,
		},
		{
			// The security case in its starkest form: an agent on NO team at
			// all, in a chat labelled with a workspace that really exists.
			// Before the fix this returned ws:ws-of-solo and the stranger drove
			// that workspace's logged-in Chrome.
			name:    "rung 3 — a workspace label cannot give a browser to an agent on no team",
			ctx:     turnCtx("stranger", "ws-of-solo"),
			wantErr: ErrNoBrowsingContext,
		},
		{
			name:    "rung 3 — no agent id at all",
			ctx:     turnCtx("", ""),
			wantErr: ErrNoBrowsingContext,
		},
		{
			// A workspace id with no agent id is not a browser either. It used
			// to be: rung 1 read only the workspace off the context, so a call
			// with no identity at all still minted a key.
			name:    "rung 3 — a workspace id with no agent id mints nothing",
			ctx:     turnCtx("", "ws-of-solo"),
			wantErr: ErrNoBrowsingContext,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveBrowsingKey(tc.ctx, home)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.True(t, got.IsZero(), "a refusal must return a ZERO key, never a usable one")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantKey, got.String())
		})
	}
}

// TestResolveBrowsingKey_NoWorkspaceFailsByName is FR-008. The failure must be
// RETURNED and identifiable, never swallowed into a shared browser and never
// nil-with-empty: a turn that quietly landed on somebody else's browser is
// indistinguishable from a working one right up to the point where it reads
// somebody else's logged-in session.
func TestResolveBrowsingKey_NoWorkspaceFailsByName(t *testing.T) {
	home := t.TempDir() // no workspaces at all

	key, err := ResolveBrowsingKey(turnCtx("nobody", ""), home)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoBrowsingContext),
		"the failure must be identifiable by sentinel, not only by its text")
	require.True(t, key.IsZero(), "a refused resolution must not hand back a key")
	require.Equal(t, "", key.WorkspaceID())

	// The text is a behavioural contract: it is what an operator reads in the
	// tool result, and it must say what to DO.
	require.Contains(t, err.Error(), "not rooted in a workspace")
	require.Contains(t, err.Error(), "add this agent to a workspace's team")
}

// TestResolveBrowsingKey_AmbiguousRefuses is FR-033.
//
// FindForAgent tie-breaks a multi-workspace membership by sorted id, which is
// the right answer for FILESYSTEM re-rooting and the wrong one here: this
// choice decides which set of live logins the turn acts with. Choosing
// arbitrarily is not a smaller failure than refusing — it is a bigger one,
// because it succeeds.
func TestResolveBrowsingKey_AmbiguousRefuses(t *testing.T) {
	home := t.TempDir()
	writeWorkspace(t, home, "alpha-workspace", "roamer")
	writeWorkspace(t, home, "beta-workspace", "roamer")

	t.Run("plain path refuses", func(t *testing.T) {
		key, err := ResolveBrowsingKey(turnCtx("roamer", ""), home)
		require.ErrorIs(t, err, ErrNoBrowsingContext)
		require.True(t, key.IsZero())
		require.NotEqual(t, "ws:alpha-workspace", key.String(),
			"the sorted-first tie-break must NOT be applied here")
	})

	t.Run("preferring path refuses when the preference resolves nothing", func(t *testing.T) {
		key, err := ResolveBrowsingKeyForAgent(home, "roamer", "")
		require.ErrorIs(t, err, ErrNoBrowsingContext)
		require.True(t, key.IsZero())

		// A preferred workspace the agent is NOT on must not rescue the
		// ambiguity by silently falling back to the sorted-first pick.
		key, err = ResolveBrowsingKeyForAgent(home, "roamer", "a-workspace-they-are-not-on")
		require.ErrorIs(t, err, ErrNoBrowsingContext)
		require.True(t, key.IsZero())
	})

	t.Run("naming one resolves it — that is not an ambiguity", func(t *testing.T) {
		key, err := ResolveBrowsingKeyForAgent(home, "roamer", "beta-workspace")
		require.NoError(t, err)
		require.Equal(t, "ws:beta-workspace", key.String())
	})
}

// TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID is FR-037's segment half.
//
// The check runs on the RENDERED segment "ws-<id>", not on the bare id, because
// that is the string a filesystem sees when the profile directory is created.
// A workspace id that escapes its own directory would put one workspace's
// profile — cookies, logins, the lot — somewhere another one can reach.
func TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID(t *testing.T) {
	bad := []string{
		"../escape",
		"nested/child",
		`windows\child`,
		"trailing/",
		"/absolute",
		"has\x00nul",
	}
	for i, id := range bad {
		t.Run("rejects "+id, func(t *testing.T) {
			// Reached through a workspace FILE carrying this id, not through
			// the turn context. Since rung 1 became a preference, a
			// context-supplied id the agent is not a member of never reaches
			// newBrowsingKey at all — it falls through to the membership
			// ladder. The remaining route to a non-segment id is a workspace
			// whose stored id is one, and the agent really is on its team, so
			// this exercises the guard where it can still fire.
			home := t.TempDir()
			writeWorkspaceAs(t, home, fmt.Sprintf("badid%d.json", i), id, "anyone")

			key, err := ResolveBrowsingKey(turnCtx("anyone", id), home)
			require.ErrorIs(t, err, ErrNoBrowsingContext,
				"a workspace id that is not one path segment must be refused, as ErrNoBrowsingContext")
			require.True(t, key.IsZero())
		})
	}

	// The RENDERED-vs-BARE distinction, demonstrated rather than asserted in
	// prose. A bare id of ".." or "." is traversal; the segment it renders to,
	// "ws-.." or "ws-.", is an ordinary directory name and escapes nothing. A
	// guard written against the BARE id would refuse these — refusing a
	// workspace whose id happens to be ".." is harmless but wrong-headed, and
	// more importantly it would be a guard that had never been tested against
	// the string a filesystem actually sees.
	for i, id := range []string{"..", "."} {
		t.Run("the prefix neutralises a bare "+id, func(t *testing.T) {
			home := t.TempDir()
			writeWorkspaceAs(t, home, fmt.Sprintf("dotid%d.json", i), id, "anyone")

			key, err := ResolveBrowsingKey(turnCtx("anyone", id), home)
			require.NoError(t, err)
			require.Equal(t, "ws-"+id, key.ProfileSegment())
			require.Equal(t, "ws-"+id, filepath.Clean("ws-"+id),
				"the rendered segment must still be exactly one, traversal-free path segment")
			require.Equal(t, filepath.Join("/root", "ws-"+id), filepath.Join("/root", key.ProfileSegment()),
				"joining the segment under a profile root must stay under that root")
		})
	}

	// The control: an ordinary ULID-shaped id is accepted, so the guard above
	// is rejecting these ids specifically and not everything.
	home := t.TempDir()
	writeWorkspace(t, home, "01J8ZQ4T7N9K3M2P5R6S7T8V9W", "anyone")
	key, err := ResolveBrowsingKey(turnCtx("anyone", "01J8ZQ4T7N9K3M2P5R6S7T8V9W"), home)
	require.NoError(t, err)
	require.Equal(t, "ws:01J8ZQ4T7N9K3M2P5R6S7T8V9W", key.String())
	require.Equal(t, "ws-01J8ZQ4T7N9K3M2P5R6S7T8V9W", key.ProfileSegment())
}

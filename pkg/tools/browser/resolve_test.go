package browser

import (
	"context"
	"encoding/json"
	"errors"
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
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.Marshal(map[string]any{"id": id, "core_team": coreTeam})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), body, 0o600))
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
func TestResolveBrowsingKey_Ladder(t *testing.T) {
	home := t.TempDir()
	writeWorkspace(t, home, "ws-of-solo", "solo")

	cases := []struct {
		name    string
		ctx     context.Context
		wantKey string
		wantErr error
	}{
		{
			name:    "rung 1 — the turn's own workspace id wins outright",
			ctx:     turnCtx("solo", "turn-bound-workspace"),
			wantKey: "ws:turn-bound-workspace",
		},
		{
			name:    "rung 1 beats rung 2 — the turn's workspace is not overridden by membership",
			ctx:     turnCtx("solo", "turn-bound-workspace"),
			wantKey: "ws:turn-bound-workspace",
		},
		{
			name:    "rung 2 — no turn workspace, one unambiguous membership",
			ctx:     turnCtx("solo", ""),
			wantKey: "ws:ws-of-solo",
		},
		{
			name:    "rung 3 — an agent on no workspace has no browser of its own",
			ctx:     turnCtx("stranger", ""),
			wantErr: ErrNoBrowsingContext,
		},
		{
			name:    "rung 3 — no agent id at all",
			ctx:     turnCtx("", ""),
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
	home := t.TempDir()

	bad := []string{
		"../escape",
		"nested/child",
		`windows\child`,
		"trailing/",
		"/absolute",
		"has\x00nul",
	}
	for _, id := range bad {
		t.Run("rejects "+id, func(t *testing.T) {
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
	for _, id := range []string{"..", "."} {
		t.Run("the prefix neutralises a bare "+id, func(t *testing.T) {
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
	key, err := ResolveBrowsingKey(turnCtx("anyone", "01J8ZQ4T7N9K3M2P5R6S7T8V9W"), home)
	require.NoError(t, err)
	require.Equal(t, "ws:01J8ZQ4T7N9K3M2P5R6S7T8V9W", key.String())
	require.Equal(t, "ws-01J8ZQ4T7N9K3M2P5R6S7T8V9W", key.ProfileSegment())
}

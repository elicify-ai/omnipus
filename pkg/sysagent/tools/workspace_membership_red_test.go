// workspace_membership_red_test.go — RED tests for epic #1094 slice S1a task
// T-4 (pkg/sysagent/tools/workspace_membership.go, spec revision 7).
//
// Spec: docs/internal/specs/export-import-s1-spec.md rev 7 — FR-035 (join
// helper), FR-049 (one-lock publish), the Rollback contract mechanism steps
// 1–3, C1/C2 rows, FR-037 ("A C1 failure (including a partial C1) MUST NOT
// stop C2"), BDD scenarios "A competing workspace edit waits…", "A failed
// membership removal…", test rows #20a/#20b, D7 rows 13/14/22; and the
// architect's shapes in ARCH-SIGNOFF-T3-T4.md (T-4 block, C-5 seams, C-7).
// The functions under test are compile-only stubs (stub_for_red.go, "DELETE
// when GREEN lands"): every test here must fail on an assertion or a
// not-implemented error until GREEN lands, never on an undefined symbol.
//
// Synchronization: all ordering is channel-based; the only time values are
// hard deadlock bounds (2s) and one bounded negative probe whose safety is
// argued at its use site. No sleeps, no network.
package systools_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixtures -------------------------------------------------------------

// redSeedWorkspace writes the workspace record the way the store does, at
// home/workspaces/<id>.json (workspacepkg.dirFor), without taking the lock —
// the tests are the only writer at fixture time.
func redSeedWorkspace(t *testing.T, home, wsID string, w workspacepkg.Workspace) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	data, err := json.Marshal(w)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), data, 0o600))
}

func redLoadWorkspace(t *testing.T, home, wsID string) workspacepkg.Workspace {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "workspaces", wsID+".json"))
	require.NoError(t, err, "workspace record %s must be readable", wsID)
	var w workspacepkg.Workspace
	require.NoError(t, json.Unmarshal(data, &w))
	return w
}

func redWorkspaceMissing(t *testing.T, home, wsID string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(home, "workspaces", wsID+".json"))
	return errors.Is(err, os.ErrNotExist)
}

func redMemberOptions(home string) systools.WorkspaceMembershipOptions {
	return systools.WorkspaceMembershipOptions{
		Home: home,
		// Production supplies the tool's self-edge exclusion set and the
		// delegation depth ceiling; the empty set and 10 are the neutral
		// values these tests need (C-5 seam fields stay nil so the real
		// SaveDelegation / writeEntity / no-op hook run unless a test says
		// otherwise).
		SelfEdgeExcluded: func() map[string]bool { return nil },
		DepthCeiling:     func() int { return 10 },
	}
}

func redCoreTeamHas(w workspacepkg.Workspace, agentID string) bool {
	for _, id := range w.CoreTeam {
		if id == agentID {
			return true
		}
	}
	return false
}

func redEdgesInvolving(t *testing.T, home, wsID, agentID string) []workspacepkg.DelegationEdge {
	t.Helper()
	edges, _ := workspacepkg.LoadDelegation(home, wsID)
	var found []workspacepkg.DelegationEdge
	for _, e := range edges {
		if e.FromAgent == agentID || e.ToAgent == agentID {
			found = append(found, e)
		}
	}
	return found
}

func redSelfRowPresent(t *testing.T, home, wsID, agentID string) bool {
	t.Helper()
	for _, e := range redEdgesInvolving(t, home, wsID, agentID) {
		if e.FromAgent == agentID && e.ToAgent == agentID {
			return true
		}
	}
	return false
}

func redWaitSignal(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second): // hard deadlock bound, not a sleep
		t.Fatalf("timeout waiting for %s", what)
	}
}

type redJoinOutcome struct {
	res systools.LeaveResult
	err error
}

func redWaitJoin(t *testing.T, ch chan redJoinOutcome) redJoinOutcome {
	t.Helper()
	select {
	case out := <-ch:
		return out
	case <-time.After(2 * time.Second): // hard deadlock bound, not a sleep
		t.Fatal("timeout waiting for JoinWorkspaceTeamAndPublish to return")
		return redJoinOutcome{}
	}
}

// redRequireNotAcquired is the bounded negative probe for the one-lock-hold
// contract (FR-049): the competing writer must NOT have acquired the
// workspace lock while the helper is still inside its hold. The probe cannot
// false-fire in a correct implementation: the helper is channel-blocked
// inside its hold (publishEntered / hookReleased ordering below) and the
// release signal is only sent AFTER this probe completes, so the lock is
// provably still held for the whole window. within=50ms is generous for a
// goroutine that is already runnable.
func redRequireNotAcquired(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s acquired the workspace lock while the helper was still inside its one hold — the hold does not cover the whole join/seed/commit sequence (FR-049)", what)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- TestJoinWorkspaceTeam_ResultTuple (FR-035, sign-off tuple table) ------

// TestJoinWorkspaceTeam_ResultTuple pins the moved join helper's result tuple
// for every outcome the sign-off documents. Same tuple, same meanings as
// today's AgentCreateTool.joinWorkspaceTeam; one workspacepkg.LockID(wsID)
// hold, released on return; the self-row is seeded before (true, nil).
func TestJoinWorkspaceTeam_ResultTuple(t *testing.T) {
	wsID, agentID := "wsjoin01", "joinx1"

	t.Run("a new member joins and the self-row is seeded -> (true, nil)", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})

		joined, err := systools.JoinWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.NoError(t, err)
		assert.True(t, joined, "(true, nil) = member, self-row present")

		w := redLoadWorkspace(t, home, wsID)
		assert.True(t, redCoreTeamHas(w, agentID), "core_team gains the agent")
		assert.True(t, redCoreTeamHas(w, "lead"), "existing members are kept")
		assert.True(t, redSelfRowPresent(t, home, wsID, agentID), "the self delegation row is seeded")
	})

	t.Run("an existing member stays deduplicated -> (true, nil)", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead", agentID}})
		depth := 0
		require.NoError(t, workspacepkg.SaveDelegation(home, wsID, []workspacepkg.DelegationEdge{
			{FromAgent: agentID, ToAgent: agentID, Modes: []workspacepkg.DelegationMode{"direct"}, Depth: &depth},
		}))

		joined, err := systools.JoinWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.NoError(t, err)
		assert.True(t, joined)

		w := redLoadWorkspace(t, home, wsID)
		count := 0
		for _, id := range w.CoreTeam {
			if id == agentID {
				count++
			}
		}
		assert.Equal(t, 1, count, "core_team keeps exactly one entry for the agent")
		assert.True(t, redSelfRowPresent(t, home, wsID, agentID))
	})

	t.Run("a missing workspace is a read failure -> (false, err)", func(t *testing.T) {
		home := t.TempDir()

		joined, err := systools.JoinWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.Error(t, err, "a missing workspace is (false, err), never a silent join")
		assert.ErrorContains(t, err, "workspace not found", "the read failure names the missing workspace")
		assert.False(t, joined)
		assert.True(t, redWorkspaceMissing(t, home, wsID), "nothing is created for a missing workspace")
	})

	t.Run("a competing removal before the re-read is (false, nil) and seeds nothing", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})

		// The AfterMembershipWrite hook runs inside the helper's hold; here it
		// simulates a competing writer whose completed edit removed the agent
		// between the membership write and the fresh re-read. It writes the
		// record WITHOUT taking the lock: it stands in for a writer that
		// already finished under its own hold.
		o := redMemberOptions(home)
		o.AfterMembershipWrite = func(hookWs, hookAgent string) {
			require.Equal(t, wsID, hookWs)
			require.Equal(t, agentID, hookAgent)
			w := workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}}
			require.NoError(t, workspacepkg.SaveRecord(home, w))
		}

		joined, err := systools.JoinWorkspaceTeam(o, wsID, agentID)
		require.NoError(t, err, "(false, nil): a competing writer removed the agent; not an error")
		assert.False(t, joined)

		w := redLoadWorkspace(t, home, wsID)
		assert.False(t, redCoreTeamHas(w, agentID), "the competing removal stands")
		assert.Empty(t, redEdgesInvolving(t, home, wsID, agentID), "(false, nil) seeds no self-row")
	})

	t.Run("a membership-write failure is (false, err) and writes nothing", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})
		errBoom := errors.New("disk full while writing the workspace")
		o := redMemberOptions(home)
		o.WriteWorkspace = func(dir, id string, w workspacepkg.Workspace) error { return errBoom }

		joined, err := systools.JoinWorkspaceTeam(o, wsID, agentID)
		assert.False(t, joined)
		require.ErrorIs(t, err, errBoom, "(false, err): the membership write failed")

		w := redLoadWorkspace(t, home, wsID)
		assert.False(t, redCoreTeamHas(w, agentID), "a failed write leaves the record unchanged")
	})
}

// --- JoinWorkspaceTeamAndPublish (FR-049, rev-7 mechanism step 2, #20a) ----

// TestJoinWorkspaceTeamAndPublish_OneLockHold pins revision 7's central
// change: team entry, the self-row seed and the commit (publish) all happen
// inside ONE workspacepkg.LockID(wsID) hold, so a competing workspace writer
// can never observe or interleave with a half-done join, and never runs
// before the commit is complete (BDD "A competing workspace edit waits for
// the lock and is not lost").
func TestJoinWorkspaceTeamAndPublish_OneLockHold(t *testing.T) {
	home := t.TempDir()
	wsID, agentID := "wspub01", "pubagent1"
	redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})

	publishEntered := make(chan struct{})
	releaseNow := make(chan struct{})
	writerAcquired := make(chan struct{})
	writerDone := make(chan error, 1)
	helperDone := make(chan redJoinOutcome, 1)
	var publishRan atomic.Int32

	go func() {
		o := redMemberOptions(home)
		res, err := systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
			publishRan.Add(1)
			close(publishEntered) // mid-hold: the commit step is running
			<-releaseNow          // hold the lock until the probe below has run
			return nil
		})
		helperDone <- redJoinOutcome{res: res, err: err}
	}()

	redWaitSignal(t, publishEntered, "the helper to enter publish (JoinWorkspaceTeamAndPublish — RED stub never calls it)")

	// The competing writer queues on the real workspace lock while the helper
	// is provably still inside its hold (see redRequireNotAcquired).
	go func() {
		unlock := workspacepkg.LockID(wsID)
		defer unlock()
		close(writerAcquired)
		w := redLoadWorkspace(t, home, wsID)
		switch {
		case !redCoreTeamHas(w, agentID):
			writerDone <- errors.New("competing writer sees no team entry — the join did not complete inside the hold")
		case !redSelfRowPresent(t, home, wsID, agentID):
			writerDone <- errors.New("competing writer sees no self-row — the seed did not complete inside the hold")
		case publishRan.Load() != 1:
			writerDone <- errors.New("competing writer ran before the commit — publish is not inside the hold")
		default:
			writerDone <- nil
		}
	}()

	redRequireNotAcquired(t, writerAcquired, "the competing writer")
	close(releaseNow)

	out := redWaitJoin(t, helperDone)
	require.NoError(t, out.err, "a successful join+publish returns nil")
	assert.Equal(t, systools.LeaveResult{}, out.res, "a successful run reports no rollback outcome")
	assert.Equal(t, int32(1), publishRan.Load(), "publish runs exactly once")

	redWaitSignal(t, writerAcquired, "the competing writer to acquire the lock after the helper released it")
	select {
	case err := <-writerDone:
		require.NoError(t, err, "the queued competing writer must see the fully completed join+seed+commit")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the competing writer's checks")
	}
}

// TestJoinWorkspaceTeamAndPublish_FailureRunsC1InsideHold pins FR-037's C1
// under revision 7: when the commit (publish) fails, the leave core runs
// INSIDE the same lock hold, before the lock is released — so a competing
// writer observing the workspace sees the rolled-back state, never a joined
// agent that was left behind (D7 row 22; BDD "A competing workspace edit
// waits…"). The helper returns the commit failure plus C1's outcome, for the
// import_rollback_incomplete report.
func TestJoinWorkspaceTeamAndPublish_FailureRunsC1InsideHold(t *testing.T) {
	home := t.TempDir()
	wsID, agentID := "wsrb01", "rbagent1"
	redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})

	errCommit := errors.New("commit failed: the pending record rename did not land")
	publishEntered := make(chan struct{})
	releaseNow := make(chan struct{})
	writerAcquired := make(chan struct{})
	writerDone := make(chan error, 1)
	helperDone := make(chan redJoinOutcome, 1)

	go func() {
		o := redMemberOptions(home)
		res, err := systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
			close(publishEntered)
			<-releaseNow
			return errCommit
		})
		helperDone <- redJoinOutcome{res: res, err: err}
	}()

	redWaitSignal(t, publishEntered, "the helper to enter publish (RED stub never calls it)")

	go func() {
		unlock := workspacepkg.LockID(wsID)
		defer unlock()
		close(writerAcquired)
		w := redLoadWorkspace(t, home, wsID)
		switch {
		case redCoreTeamHas(w, agentID):
			writerDone <- errors.New("competing writer still sees the agent after a failed commit — C1 did not run inside the hold")
		case len(redEdgesInvolving(t, home, wsID, agentID)) != 0:
			writerDone <- errors.New("competing writer still sees the self-row — C1's delegation removal did not run inside the hold")
		default:
			writerDone <- nil
		}
	}()

	redRequireNotAcquired(t, writerAcquired, "the competing writer")
	close(releaseNow)

	out := redWaitJoin(t, helperDone)
	require.ErrorIs(t, out.err, errCommit, "the helper returns the commit failure, not the rollback's errors")
	assert.Equal(t, systools.LeaveResult{MembershipRemoved: true, EdgesRemoved: 1},
		out.res, "C1's outcome (membership removed, the self edge undone) is reported for import_rollback_incomplete")

	redWaitSignal(t, writerAcquired, "the competing writer to acquire the lock after the rollback released it")
	select {
	case err := <-writerDone:
		require.NoError(t, err, "the queued competing writer must see the rolled-back state")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the competing writer's checks")
	}

	assert.False(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID), "the agent is out of core_team after the failed commit")
}

// TestJoinWorkspaceTeamAndPublish_StagesFailInTurn walks test row #20a's
// failure ladder at the helper boundary: each stage failing in turn means no
// publish, and what DID land is undone (rev-7 mechanism step 2 order:
// re-read W -> append+write membership -> hook -> re-read -> seed self-row ->
// publish).
func TestJoinWorkspaceTeamAndPublish_StagesFailInTurn(t *testing.T) {
	wsID, agentID := "wsstage1", "stageagt1"

	t.Run("a missing workspace fails before publish and creates nothing", func(t *testing.T) {
		home := t.TempDir()
		done := make(chan redJoinOutcome, 1)
		var publishRan atomic.Int32
		go func() {
			res, err := systools.JoinWorkspaceTeamAndPublish(redMemberOptions(home), wsID, agentID, func() error {
				publishRan.Add(1)
				return nil
			})
			done <- redJoinOutcome{res: res, err: err}
		}()
		out := redWaitJoin(t, done)
		require.Error(t, out.err, "a missing workspace is a failure, not a join")
		assert.ErrorContains(t, out.err, "workspace not found", "the failure names the missing workspace")
		assert.Equal(t, int32(0), publishRan.Load(), "publish never runs when the workspace is missing")
		assert.True(t, redWorkspaceMissing(t, home, wsID), "nothing is created")
	})

	t.Run("a membership-write failure fails before publish", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})
		errBoom := errors.New("membership write failed")
		o := redMemberOptions(home)
		o.WriteWorkspace = func(dir, id string, w workspacepkg.Workspace) error { return errBoom }

		done := make(chan redJoinOutcome, 1)
		var publishRan atomic.Int32
		go func() {
			res, err := systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
				publishRan.Add(1)
				return nil
			})
			done <- redJoinOutcome{res: res, err: err}
		}()
		out := redWaitJoin(t, done)
		require.ErrorIs(t, out.err, errBoom)
		assert.Equal(t, int32(0), publishRan.Load(), "publish never runs after a failed membership write")
		assert.False(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID))
		assert.Empty(t, redEdgesInvolving(t, home, wsID, agentID), "no self-row is seeded")
	})

	t.Run("a self-row seed failure fails before publish and C1 still undoes the membership", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})
		errSeed := errors.New("self-row seed failed")
		var seedCalls atomic.Int32
		o := redMemberOptions(home)
		o.SaveDelegation = func(homeDir, id string, edges []workspacepkg.DelegationEdge) error {
			if seedCalls.Add(1) == 1 {
				return errSeed // the join's seed; any later call is C1's save
			}
			return nil
		}

		done := make(chan redJoinOutcome, 1)
		var publishRan atomic.Int32
		go func() {
			res, err := systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
				publishRan.Add(1)
				return nil
			})
			done <- redJoinOutcome{res: res, err: err}
		}()
		out := redWaitJoin(t, done)
		require.ErrorIs(t, out.err, errSeed, "the seed failure is the failure the caller sees")
		assert.Equal(t, int32(0), publishRan.Load(), "publish never runs after a failed seed")
		assert.False(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID),
			"C1 undoes the membership the join wrote before the seed failed")
		assert.Empty(t, redEdgesInvolving(t, home, wsID, agentID), "no self-row remains")
	})

	t.Run("the membership hook and the re-read run before publish", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})
		events := make(chan string, 2)
		o := redMemberOptions(home)
		o.AfterMembershipWrite = func(ws, agent string) { events <- "hook" }
		go func() {
			_, _ = systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
				events <- "publish"
				return nil
			})
			close(events)
		}()
		assert.Equal(t, "hook", redRecvEvent(t, events, "the membership hook to run"),
			"the membership hook runs before publish")
		assert.Equal(t, "publish", redRecvEvent(t, events, "publish to run after the hook"),
			"publish runs after the hook")
	})
}

// redRecvEvent receives the next ordered event with a hard deadlock bound.
func redRecvEvent(t *testing.T, events chan string, what string) string {
	t.Helper()
	select {
	case got := <-events:
		return got
	case <-time.After(2 * time.Second): // hard deadlock bound, not a sleep
		t.Fatalf("timeout waiting for %s", what)
		return ""
	}
}

// --- LeaveWorkspaceTeam (sign-off LeaveStage/LeaveResult, D7 rows 13/14) ---

// TestLeaveWorkspaceTeam_TypedStages pins the typed leave: what it removed,
// which stage failed, and the partial-C1 shape ("membership removed, but the
// delegation half failed") that FR-037's C1 reporting needs.
func TestLeaveWorkspaceTeam_TypedStages(t *testing.T) {
	wsID, agentID, lead := "wsleave1", "leaveagt1", "lead"

	t.Run("a member with two edges leaves cleanly", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{lead, agentID}})
		depth := 0
		require.NoError(t, workspacepkg.SaveDelegation(home, wsID, []workspacepkg.DelegationEdge{
			{FromAgent: agentID, ToAgent: agentID, Modes: []workspacepkg.DelegationMode{"direct"}, Depth: &depth},
			{FromAgent: lead, ToAgent: agentID, Modes: []workspacepkg.DelegationMode{"task"}, Depth: &depth},
		}))

		res, err := systools.LeaveWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.NoError(t, err)
		assert.Equal(t, systools.LeaveResult{MembershipRemoved: true, EdgesRemoved: 2}, res,
			"both edges involving the agent are counted and removed")

		w := redLoadWorkspace(t, home, wsID)
		assert.False(t, redCoreTeamHas(w, agentID))
		assert.True(t, redCoreTeamHas(w, lead), "other members stay")
		assert.Empty(t, redEdgesInvolving(t, home, wsID, agentID), "no edge involving the agent remains")
	})

	t.Run("a non-member is a clean typed no-op", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{lead}})

		res, err := systools.LeaveWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.NoError(t, err)
		assert.Equal(t, systools.LeaveResult{MembershipRemoved: false, EdgesRemoved: 0}, res)

		w := redLoadWorkspace(t, home, wsID)
		assert.True(t, redCoreTeamHas(w, lead), "the record is unchanged")
	})

	t.Run("a missing workspace is the typed C-7 no-op, not an error", func(t *testing.T) {
		home := t.TempDir()

		res, err := systools.LeaveWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.NoError(t, err, "C-7: a workspace record that no longer exists leaves nothing to remove")
		assert.Equal(t, systools.LeaveResult{WorkspaceMissing: true}, res)
	})

	t.Run("a corrupt delegation store is the partial C1 shape read_delegation", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{lead, agentID}})
		path, err := workspacepkg.DelegationStorePath(home, wsID)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

		res, err := systools.LeaveWorkspaceTeam(redMemberOptions(home), wsID, agentID)
		require.Error(t, err, "an unreadable delegation store fails the leave at read_delegation")
		assert.Equal(t, systools.LeaveStageReadDelegation, res.FailedStage)
		assert.True(t, res.MembershipRemoved, "partial C1: the membership removal already landed")

		w := redLoadWorkspace(t, home, wsID)
		assert.False(t, redCoreTeamHas(w, agentID), "the membership removal stands despite the later failure")
	})

	t.Run("a delegation-save failure is the partial C1 shape save_delegation", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{lead, agentID}})
		depth := 0
		require.NoError(t, workspacepkg.SaveDelegation(home, wsID, []workspacepkg.DelegationEdge{
			{FromAgent: agentID, ToAgent: agentID, Modes: []workspacepkg.DelegationMode{"direct"}, Depth: &depth},
		}))
		errSave := errors.New("delegation save failed")
		o := redMemberOptions(home)
		o.SaveDelegation = func(homeDir, id string, edges []workspacepkg.DelegationEdge) error { return errSave }

		res, err := systools.LeaveWorkspaceTeam(o, wsID, agentID)
		require.ErrorIs(t, err, errSave)
		assert.Equal(t, systools.LeaveStageSaveDelegation, res.FailedStage)
		assert.True(t, res.MembershipRemoved, "partial C1: membership removed, delegation half failed")

		assert.False(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID))
		assert.Len(t, redEdgesInvolving(t, home, wsID, agentID), 1, "a failed save leaves the delegation store unchanged")
	})

	t.Run("a membership-write failure fails at write_membership", func(t *testing.T) {
		home := t.TempDir()
		redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{lead, agentID}})
		errBoom := errors.New("workspace write failed")
		o := redMemberOptions(home)
		o.WriteWorkspace = func(dir, id string, w workspacepkg.Workspace) error { return errBoom }

		res, err := systools.LeaveWorkspaceTeam(o, wsID, agentID)
		require.ErrorIs(t, err, errBoom)
		assert.Equal(t, systools.LeaveStageWriteMembership, res.FailedStage)
		assert.False(t, res.MembershipRemoved, "nothing was removed when the write failed")

		assert.True(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID), "the record is unchanged")
	})
}

// --- Rollback order C1 then C2 (FR-037, BDD "A failed membership removal…")-

// TestRollbackOrder_C1ThenC2 pins revision 7's rollback sequence end to end:
// a failed commit runs C1 (leave, inside the hold), C1's failure — here the
// delegation save — is REPORTED (MembershipRemoved true, FailedStage
// save_delegation) but does not stop the sequence, and the caller can then
// run C2 (DiscardPending of the pending record the import created before the
// commit). The C2 leg uses the real store API (T-11).
func TestRollbackOrder_C1ThenC2(t *testing.T) {
	home := t.TempDir()
	wsID, agentID, pendingID := "wsorder1", "ordagent1", "ordpend1"
	redSeedWorkspace(t, home, wsID, workspacepkg.Workspace{ID: wsID, Name: "ws", Status: "active", CoreTeam: []string{"lead"}})

	errCommit := errors.New("commit failed")
	errSave := errors.New("C1 delegation save failed")
	var saveCalls atomic.Int32
	o := redMemberOptions(home)
	o.SaveDelegation = func(homeDir, id string, edges []workspacepkg.DelegationEdge) error {
		if saveCalls.Add(1) == 1 {
			return workspacepkg.SaveDelegation(homeDir, id, edges) // D7 row 14: actually persist the seed
		}
		return errSave // C1's rollback save fails: a partial C1
	}

	done := make(chan redJoinOutcome, 1)
	go func() {
		// The publish callback is import's commit step: by then the pending
		// record exists; the commit itself fails.
		res, err := systools.JoinWorkspaceTeamAndPublish(o, wsID, agentID, func() error {
			_, cerr := agentstore.New(home).CreatePendingState(pendingID, &config.AgentConfig{ID: pendingID, Name: "Pending"}, "soul", "")
			if cerr != nil {
				return cerr
			}
			return errCommit
		})
		done <- redJoinOutcome{res: res, err: err}
	}()

	out := redWaitJoin(t, done)

	require.ErrorIs(t, out.err, errCommit, "the commit failure is what the helper reports")
	assert.Equal(t, int32(2), saveCalls.Load(), "D7 row 14: the seed succeeds, then C1's delegation save is reached and fails")
	assert.True(t, redSelfRowPresent(t, home, wsID, agentID), "the failed C1 save must leave the real seeded self-row on disk")
	assert.Equal(t, systools.LeaveResult{MembershipRemoved: true, FailedStage: systools.LeaveStageSaveDelegation},
		out.res, "a partial C1 is reported (membership removed, delegation save failed) so the caller knows what C1 undid")
	assert.False(t, redCoreTeamHas(redLoadWorkspace(t, home, wsID), agentID),
		"C1's membership removal landed despite the save failure")

	// C2 MUST still run after a failed (partial) C1 (FR-037). The pending
	// record the commit step created is discarded now.
	pendingPath := filepath.Join(home, "entities", "agents", pendingID+".pending")
	_, statErr := os.Stat(pendingPath)
	require.NoError(t, statErr, "the pending record exists when C2 starts")
	require.NoError(t, agentstore.New(home).DiscardPending(pendingID), "C2 runs even though C1 only partially succeeded")
	_, statErr = os.Stat(pendingPath)
	require.True(t, errors.Is(statErr, os.ErrNotExist), "C2 removed the pending record")
	_, statErr = os.Stat(filepath.Join(home, "entities", "agents", pendingID+".json"))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "C2 must not publish the record as a side effect")
}

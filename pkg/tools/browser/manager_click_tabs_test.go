package browser

import (
	"context"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"
)

// ADR-041 requires browser_click to report the popup even when the passive
// listener already adopted it. Only Chrome target listing/attachment is faked.
func TestClickTabsReportsCompletedPassiveAdoption(t *testing.T) {
	for _, ordering := range []string{"before_list", "after_list"} {
		t.Run(ordering, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(int) (bool, bool) { return false, true }
			t.Cleanup(m.Shutdown)
			clicked, err := m.Session(testSessionID)
			require.NoError(t, err)
			before, err := m.snapshotClickTabs(testSessionID, clicked)
			require.NoError(t, err)
			m.listTargets = func(context.Context) ([]*target.Info, error) {
				return []*target.Info{{TargetID: "popup", Type: "page", OpenerID: before.opener}}, nil
			}
			var infos []*target.Info
			var tracked map[target.ID]struct{}
			var owner *sessionEntry
			if ordering == "after_list" {
				infos, tracked, owner, err = m.reconcileTargetSnapshot(testSessionID)
				require.NoError(t, err)
			}
			adopted, err := m.adoptTargetForSession(testSessionID, "popup", before.owner)
			require.NoError(t, err)
			require.NotNil(t, adopted.Adopted)
			var outcome ReconcileOutcome
			if ordering == "after_list" {
				// Retain the actual list snapshot across the passive winner. The real
				// production loop must recover a completed adoption, not just a pending one.
				outcome, err = m.reconcileListedTabs(testSessionID, infos, tracked, owner, before)
			} else {
				outcome, err = m.reconcileClickTabs(testSessionID, before)
			}
			require.NoError(t, err)
			require.Equal(t, ReconcileOutcome{Adopted: true, NewActive: &Tab{Index: 1, Active: true}}, outcome)
			result := map[string]any{"success": true}
			applyReconcileOutcome(result, outcome)
			require.Equal(t, true, result["opened_new_tab"])
			require.Equal(t, 1, result["new_tab_index"])
			// Ordinary reconciliation must remain idempotent after reporting the click.
			outcome, err = m.ReconcileTabs(testSessionID)
			require.NoError(t, err)
			require.Equal(t, ReconcileOutcome{}, outcome)
		})
	}
}

func TestClickTabsDoesNotReportUnrelatedOrExistingTargets(t *testing.T) {
	for _, scenario := range []string{"existing", "foreign_opener", "no_opener", "other_owned_opener"} {
		t.Run(scenario, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(int) (bool, bool) { return false, true }
			t.Cleanup(m.Shutdown)
			clicked, err := m.Session(testSessionID)
			require.NoError(t, err)
			_, clickedID, err := m.activeTargetSnapshot(testSessionID)
			require.NoError(t, err)
			if scenario == "existing" || scenario == "other_owned_opener" {
				_, err = m.adoptTarget(testSessionID, "existing")
				require.NoError(t, err)
				_, err = m.SwitchTab(testSessionID, 0)
				require.NoError(t, err)
			}
			before, err := m.snapshotClickTabs(testSessionID, clicked)
			require.NoError(t, err)
			info := &target.Info{TargetID: "popup", Type: "page", OpenerID: clickedID}
			switch scenario {
			case "existing":
				info.TargetID = "existing"
			case "foreign_opener":
				info.OpenerID = "foreign"
			case "no_opener":
				info.OpenerID = ""
			case "other_owned_opener":
				info.OpenerID = "existing"
			}
			m.listTargets = func(context.Context) ([]*target.Info, error) { return []*target.Info{info}, nil }
			outcome, err := m.reconcileClickTabs(testSessionID, before)
			require.NoError(t, err)
			require.Equal(t, ReconcileOutcome{}, outcome)
		})
	}
}

func TestClickTabsRejectsReplacementSession(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	clicked, err := m.Session(testSessionID)
	require.NoError(t, err)
	before, err := m.snapshotClickTabs(testSessionID, clicked)
	require.NoError(t, err)
	m.CloseSession(testSessionID)
	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	m.listTargets = func(context.Context) ([]*target.Info, error) { return nil, nil }
	outcome, err := m.reconcileClickTabs(testSessionID, before)
	require.ErrorIs(t, err, errBrowserSessionChanged)
	require.Equal(t, ReconcileOutcome{}, outcome)
}

func TestClickTabsPreservesFreshAndMixedAdoptionReporting(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "mixed_memory_refusal"}[mixed], func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(open int) (bool, bool) { return mixed && open >= 2, true }
			t.Cleanup(m.Shutdown)
			clicked, err := m.Session(testSessionID)
			require.NoError(t, err)
			before, err := m.snapshotClickTabs(testSessionID, clicked)
			require.NoError(t, err)
			infos := []*target.Info{{TargetID: "popup", Type: "page", OpenerID: before.opener}}
			if mixed {
				infos = append(infos, &target.Info{TargetID: "stranded", Type: "page", OpenerID: before.opener})
			}
			m.listTargets = func(context.Context) ([]*target.Info, error) { return infos, nil }
			outcome, err := m.reconcileClickTabs(testSessionID, before)
			require.NoError(t, err)
			want := ReconcileOutcome{Adopted: true, NewActive: &Tab{Index: 1, Active: true}}
			if mixed {
				want.Unadopted, want.UnadoptedCount, want.Reason = true, 1, tabAdoptReasonMemoryPressure
			}
			require.Equal(t, want, outcome)
		})
	}
}

func TestClickTabsDoesNotClaimAdoptionAlreadyPendingBeforeClick(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	clicked, err := m.Session(testSessionID)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	base := m.createTabFn
	m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
		if id == "already-opening" {
			close(entered)
			<-release
		}
		return base(ctx, id)
	}
	done := make(chan error, 1)
	go func() { _, adoptErr := m.adoptTarget(testSessionID, "already-opening"); done <- adoptErr }()
	<-entered
	before, snapshotErr := m.snapshotClickTabs(testSessionID, clicked)
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, snapshotErr)
	m.listTargets = func(context.Context) ([]*target.Info, error) {
		return []*target.Info{{TargetID: "already-opening", Type: "page", OpenerID: before.opener}}, nil
	}
	outcome, err := m.reconcileClickTabs(testSessionID, before)
	require.NoError(t, err)
	require.Equal(t, ReconcileOutcome{}, outcome)
}

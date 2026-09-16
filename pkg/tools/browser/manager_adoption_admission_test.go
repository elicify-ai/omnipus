package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A slow attachment to a new popup must not consume the existing tab's command
// budget. Only native attachment is held; manager admission and switching are real.
func TestPopupAttachDoesNotBlockExistingTabCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestManagerWithFakeTabs(t)
		t.Cleanup(m.Shutdown)
		_, err := m.Session(testSessionID)
		require.NoError(t, err)
		entered, finish := make(chan struct{}), make(chan struct{})
		base := m.createTabFn
		m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
			if id == "slow-popup" {
				close(entered)
				<-finish
			}
			return base(ctx, id)
		}
		done := make(chan error, 1)
		go func() { _, adoptionErr := m.adoptTarget(testSessionID, "slow-popup"); done <- adoptionErr }()
		<-entered
		caller, cancel := context.WithTimeout(context.Background(), time.Second)
		_, commandErr := m.SwitchTabContext(caller, testSessionID, 0)
		cancel()
		// Always drain the native boundary, including the expected RED failure.
		close(finish)
		require.NoError(t, <-done)
		require.NoError(t, commandErr, "existing tab command expired behind unrelated popup attachment")
		tabs, active, err := m.ListTabs(testSessionID)
		require.NoError(t, err)
		require.Len(t, tabs, 2)
		require.Equal(t, 1, active, "successful popup adoption must still become active")
	})
}

// Preparing a target is allowed concurrently; publishing it must still wait for
// the current target operation. Two callers must share one native attachment.
func TestPopupAttachCommitsUnderGateAndCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestManagerWithFakeTabs(t)
		t.Cleanup(m.Shutdown)
		_, err := m.Session(testSessionID)
		require.NoError(t, err)
		entered, finish := make(chan struct{}), make(chan struct{})
		base := m.createTabFn
		calls := 0
		m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
			calls++
			if calls == 1 {
				close(entered)
			}
			<-finish
			return base(ctx, id)
		}
		type outcome struct {
			result tabAdoptResult
			err    error
		}
		done := make(chan outcome, 2)
		adopt := func() {
			result, adoptionErr := m.adoptTarget(testSessionID, "shared-popup")
			done <- outcome{result, adoptionErr}
		}
		go adopt()
		<-entered
		go adopt()
		synctest.Wait()
		require.Equal(t, 1, calls, "duplicate event must not attach twice")
		caller, cancel := context.WithTimeout(context.Background(), time.Second)
		release, gateErr := m.acquireLiveTabCommand(caller, testSessionID)
		cancel()
		if gateErr != nil {
			close(finish)
			<-done
			<-done
			require.NoError(t, gateErr, "native popup preparation must not own publication gate")
			return
		}
		close(finish)
		synctest.Wait()
		m.mu.Lock()
		count, active := len(m.sessions[testSessionID].tabs), m.sessions[testSessionID].activeIdx
		m.mu.Unlock()
		assert.Equal(t, 1, count, "popup published while an existing operation owns admission")
		assert.Equal(t, 0, active)
		assert.Empty(t, done, "adoption must not report success before publication")
		release()
		first, second := <-done, <-done
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.Equal(t, first.result, second.result, "both callers must receive the same completed adoption")
		require.NotNil(t, first.result.Adopted)
		require.Equal(t, 1, calls)
		tabs, active, err := m.ListTabs(testSessionID)
		require.NoError(t, err)
		require.Len(t, tabs, 2)
		require.Equal(t, 1, active)
	})
}

func TestPopupAttachCannotPublishIntoReplacementSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestManagerWithFakeTabs(t)
		t.Cleanup(m.Shutdown)
		_, err := m.Session(testSessionID)
		require.NoError(t, err)
		entered, finish := make(chan struct{}), make(chan struct{})
		base := m.createTabFn
		var prepared *tabEntry
		m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
			tab, createErr := base(ctx, id)
			if id == "retired-popup" {
				prepared = tab
				close(entered)
				<-finish
			}
			return tab, createErr
		}
		done := make(chan error, 1)
		go func() { _, adoptionErr := m.adoptTarget(testSessionID, "retired-popup"); done <- adoptionErr }()
		<-entered
		m.CloseSession(testSessionID)
		replacement, err := m.Session(testSessionID)
		require.NoError(t, err)
		close(finish)
		require.ErrorIs(t, <-done, errBrowserSessionChanged)
		synctest.Wait()
		require.ErrorIs(t, prepared.ctx.Err(), context.Canceled, "unpublished target must be retired")
		require.NoError(t, replacement.Err(), "late cleanup must not cancel replacement target")
		tabs, active, err := m.ListTabs(testSessionID)
		require.NoError(t, err)
		require.Len(t, tabs, 1, "old popup must never join replacement session")
		require.Equal(t, 0, active)
	})
}

func TestPopupAttachPublicationDeadlineDiscardsOnlyPreparedTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestManagerWithFakeTabs(t)
		t.Cleanup(m.Shutdown)
		original, err := m.Session(testSessionID)
		require.NoError(t, err)
		entered, finish := make(chan struct{}), make(chan struct{})
		base := m.createTabFn
		var prepared *tabEntry
		m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
			tab, createErr := base(ctx, id)
			prepared = tab
			close(entered)
			<-finish
			return tab, createErr
		}
		done := make(chan error, 1)
		go func() { _, adoptionErr := m.adoptTarget(testSessionID, "unpublished-popup"); done <- adoptionErr }()
		<-entered
		caller, cancel := context.WithTimeout(context.Background(), time.Second)
		release, err := m.acquireLiveTabCommand(caller, testSessionID)
		cancel()
		if err != nil {
			close(finish)
			<-done
			require.NoError(t, err, "native preparation must leave existing commands usable")
			return
		}
		close(finish)
		synctest.Wait() // Adoption is now waiting for publication admission.
		time.Sleep(m.PageTimeout())
		adoptionErr := <-done
		synctest.Wait() // Drain bounded target cleanup before reading its context.
		release()
		require.ErrorIs(t, adoptionErr, context.DeadlineExceeded)
		require.ErrorIs(t, prepared.ctx.Err(), context.Canceled, "unpublished target must not leak after admission expires")
		require.NoError(t, original.Err(), "discard must not cancel the currently displayed tab")
		tabs, active, err := m.ListTabs(testSessionID)
		require.NoError(t, err)
		require.Len(t, tabs, 1)
		require.Equal(t, 0, active)
		m.mu.Lock()
		pending := len(m.pendingAdopt)
		m.mu.Unlock()
		require.Equal(t, 0, pending, "failed publication must release its coalescing reservation")
	})
}

package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestViewportQueuedCommandCannotUseReplacementAttachment(t *testing.T) {
	h, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)
	mgr, session, panel := state.attachment()
	require.True(t, mgr.Live().TakeControl(panel, "controller"))
	entered, releaseJob := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseJob) }) }
	t.Cleanup(release)
	state.work.submit(&h.activeConns, workKindAttach, func() { close(entered); <-releaseJob })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preceding queue work did not enter")
	}
	h.dispatchViewport(wc, state, "other-viewer", marshalViewportFrame(t, 900, 700))
	epoch := state.beginAttach()
	require.True(t, state.bindAttachment(epoch, mgr, session, panel))
	release()
	h.activeConns.Wait()
	select {
	case <-wc.sendCh:
		t.Fatal("retired viewport command produced a result for replacement attachment")
	default:
	}
}

func TestViewportCurrentRefusalRetainsOperationScope(t *testing.T) {
	h, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)
	original := state.commandAttachment()
	require.True(t, original.mgr.Live().TakeControl(original.panelSessionID, "controller"))
	h.handleViewport(wc, state, "other-viewer", marshalViewportFrame(t, 900, 700))
	select {
	case queued := <-wc.sendCh:
		var body map[string]any
		require.NoError(t, json.Unmarshal(queued.data, &body))
		require.Equal(t, true, body["operation_only"], "viewport refusal must not tear down healthy video")
		require.Equal(t, original.sessionID, body["session_id"])
		epoch := state.beginAttach()
		require.True(t, state.bindAttachment(epoch, original.mgr, original.sessionID, original.panelSessionID))
		require.False(t, wc.canSendFrame(queued), "queued refusal survived replacement")
	case <-time.After(time.Second):
		t.Fatal("current refusal was not admitted")
	}
}

func TestViewportColdRefreshUsesMeasuredScale(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.state.rememberViewportScale(2)
	f.observeViewport(800, 600, 1.25)
	before := f.capture.FrameState()
	require.NoError(t, f.handler.applyColdStartRecapture(context.Background(), f.state.commandAttachment(), f.capture))
	measured := f.capture.FrameState()
	require.Equal(t, 800, measured.Width)
	require.Equal(t, 600, measured.Height)
	require.Equal(t, 1.25, measured.Scale)
	require.Greater(t, measured.Generation, before.Generation)
	require.False(t, measured.Ready)
}

func TestViewportColdRefreshPreservesUnchangedSource(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	before := f.capture.FrameState()
	require.NoError(t, f.handler.applyColdStartRecapture(context.Background(), f.state.commandAttachment(), f.capture))
	require.Equal(t, before, f.capture.FrameState())
}

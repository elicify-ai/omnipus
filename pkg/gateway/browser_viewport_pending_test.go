package gateway

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestViewportPendingAttachmentKeepsOriginalQueueOrder(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "original attachment completes"
		if replace {
			name = "replacement cannot inherit viewport"
		}
		t.Run(name, func(t *testing.T) {
			h, _ := newBrowserWSTestHandler(t, nil)
			wc, state := newControlTestFixtures(t)
			mgr, session, panel := state.attachment()
			require.True(t, mgr.Live().TakeControl(panel, "controller"))
			epoch := state.beginAttach()
			entered, releaseJob := make(chan struct{}), make(chan struct{})
			committed := make(chan bool, 1)
			var once sync.Once
			release := func() { once.Do(func() { close(releaseJob) }) }
			t.Cleanup(release)
			state.work.submit(&h.activeConns, workKindAttach, func() {
				close(entered)
				<-releaseJob
				committed <- state.bindAttachment(epoch, mgr, session, panel)
			})
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("attachment work did not enter")
			}
			h.dispatchViewport(wc, state, "other-viewer", marshalViewportFrame(t, 900, 700))
			if replace {
				replacement := state.beginAttach()
				require.True(t, state.bindAttachment(replacement, mgr, session, panel))
			}
			release()
			h.activeConns.Wait()
			require.Equal(t, !replace, <-committed)
			if replace {
				require.Empty(t, wc.sendCh, "retired pending viewport reached replacement route")
				return
			}
			select {
			case queued := <-wc.sendCh:
				var body map[string]any
				require.NoError(t, json.Unmarshal(queued.data, &body))
				require.Equal(t, session, body["session_id"])
				require.Equal(t, true, body["operation_only"], "real viewport control policy must run after original attachment commits")
				require.True(t, wc.canSendFrame(queued))
			default:
				t.Fatal("viewport queued after pending attachment was silently discarded")
			}
		})
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func latestTestConn() *browserWSConn {
	return &browserWSConn{sendCh: make(chan browserOutboundFrame, 64), doneCh: make(chan struct{})}
}

func latestTestFrame(kind browserLatestKind, n int) any {
	if kind == browserLatestTabs {
		return generated.BrowserTabsFrame{Type: "browser_tabs", ActiveIndex: n}
	}
	return generated.BrowserVideoHealthFrame{Type: "browser_video_health", State: "recovered", CaptureGeneration: &n}
}

func TestBrowserLatestStateDoesNotWaitForSlowViewer(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	for i := 0; i < 64; i++ {
		wc.sendCh <- browserOutboundFrame{data: []byte("reserved critical transition")}
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			if !wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, i), context.Background()) {
				done <- fmt.Errorf("publish %d refused", i)
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(250 * time.Millisecond):
		wc.close()
		<-done
		t.Fatal("latest state publication waited for a slow viewer's critical queue")
	}
	require.Len(t, wc.sendCh, 64, "latest snapshots must not consume critical transition capacity")
}

func TestBrowserLatestStateKeepsOnlyNewestSnapshotOfEachKind(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	for i := 0; i < 3; i++ {
		require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, i), context.Background()))
		require.True(t, wc.sendLatestGen(browserLatestVideoState, latestTestFrame(browserLatestVideoState, i+10), context.Background()))
	}
	got := make(map[string]int)
	count := 0
	for {
		data, ok := wc.takeLatest()
		if !ok {
			break
		}
		count++
		var frame struct {
			Type       string `json:"type"`
			Active     int    `json:"active_index"`
			Generation int    `json:"capture_generation"`
		}
		require.NoError(t, json.Unmarshal(data, &frame))
		if frame.Type == "browser_tabs" {
			got[frame.Type] = frame.Active
		} else {
			got[frame.Type] = frame.Generation
		}
	}
	require.Equal(t, 2, count, "only two replaceable snapshots may remain pending")
	require.Equal(t, map[string]int{"browser_tabs": 2, "browser_video_health": 12}, got)
	require.Empty(t, wc.sendCh, "replaceable state must leave discrete critical transitions untouched")
}

func TestBrowserLatestStateFencesAttachmentAtStoreAndDequeue(t *testing.T) {
	for _, when := range []string{"store", "dequeue"} {
		t.Run(when, func(t *testing.T) {
			wc := latestTestConn()
			t.Cleanup(wc.close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if when == "store" {
				cancel()
				require.False(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 1), ctx))
			} else {
				require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 1), ctx))
				cancel()
			}
			_, ok := wc.takeLatest()
			require.False(t, ok, "obsolete attachment must not publish pending state")
			require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 2), context.Background()))
			data, ok := wc.takeLatest()
			require.True(t, ok)
			var frame generated.BrowserTabsFrame
			require.NoError(t, json.Unmarshal(data, &frame))
			require.Equal(t, 2, frame.ActiveIndex)
		})
	}
}

func TestBrowserLatestStateCoalescesWakeupsAndRearmsForSecondKind(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	wake := wc.latestWake()
	require.Equal(t, 1, cap(wake), "one bounded wake signal covers both slots")
	for i := 0; i < 10; i++ {
		require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, i), context.Background()))
	}
	require.True(t, wc.sendLatestGen(browserLatestVideoState, latestTestFrame(browserLatestVideoState, 99), context.Background()))
	select {
	case <-wake:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("pending snapshots did not wake writer")
	}
	_, ok := wc.takeLatest()
	require.True(t, ok)
	select {
	case <-wake:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second pending kind did not rearm writer")
	}
	_, ok = wc.takeLatest()
	require.True(t, ok)
}

func TestBrowserLatestStateRejectsClosedConnection(t *testing.T) {
	wc := latestTestConn()
	wc.close()
	require.False(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 1), context.Background()))
	_, ok := wc.takeLatest()
	require.False(t, ok)
}

func TestBrowserLatestStateWriterDeliversAndKeepsCriticalTransitions(t *testing.T) {
	ready := make(chan *browserWSConn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wc := latestTestConn()
		wc.conn = conn
		ready <- wc
		(&BrowserWSHandler{}).writePump(wc)
	}))
	t.Cleanup(srv.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	wc := <-ready
	t.Cleanup(wc.close)
	require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 7), context.Background()))
	wc.sendCriticalGen(generated.BrowserStatusFrame{Type: "browser_status", State: "controlling"}, "test-control")
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	got := make(map[string]bool)
	for i := 0; i < 2; i++ {
		_, data, err := client.ReadMessage()
		require.NoError(t, err)
		var f struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(data, &f))
		got[f.Type] = true
	}
	require.Equal(t, map[string]bool{"browser_tabs": true, "browser_status": true}, got)
}

func TestBrowserLatestStateRejectsUnknownKindsAndUnmarshalableFrames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  browserLatestKind
		frame any
	}{
		{"first-unknown-kind", 2, latestTestFrame(browserLatestTabs, 1)},
		{"largest-kind", 255, latestTestFrame(browserLatestTabs, 1)},
		{"unmarshalable", browserLatestTabs, make(chan int)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wc := latestTestConn()
			t.Cleanup(wc.close)
			require.False(t, wc.sendLatestGen(tc.kind, tc.frame, context.Background()), "invalid snapshots must not be reported as accepted")
			_, ok := wc.takeLatest()
			require.False(t, ok)
		})
	}
}

func TestBrowserLatestStateTabUpdatesCannotStarveVideoState(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 1), context.Background()))
	require.True(t, wc.sendLatestGen(browserLatestVideoState, latestTestFrame(browserLatestVideoState, 9), context.Background()))
	seenVideo := false
	for i := 0; i < 2; i++ {
		require.True(t, wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, i+2), context.Background()))
		data, ok := wc.takeLatest()
		require.True(t, ok)
		var frame struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(data, &frame))
		seenVideo = seenVideo || frame.Type == "browser_video_health"
	}
	require.True(t, seenVideo, "a pending video state must be served within two writes despite continuous tab updates")
}

type latestCancelOnMarshal struct {
	cancel  context.CancelFunc
	publish func()
}

func (f latestCancelOnMarshal) MarshalJSON() ([]byte, error) {
	f.cancel()
	if f.publish != nil {
		f.publish()
	}
	return []byte(`{"type":"browser_tabs","active_index":1,"tabs":[]}`), nil
}

func TestBrowserLatestStateCancellationDuringEncodingRejectsStore(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	freshAccepted := false
	frame := latestCancelOnMarshal{cancel: cancel, publish: func() {
		freshAccepted = wc.sendLatestGen(browserLatestTabs, latestTestFrame(browserLatestTabs, 2), context.Background())
	}}
	require.False(t, wc.sendLatestGen(browserLatestTabs, frame, ctx), "attachment canceled during encoding must not overwrite a replacement snapshot")
	require.True(t, freshAccepted)
	data, ok := wc.takeLatest()
	require.True(t, ok, "replacement snapshot must survive obsolete publisher completion")
	var current generated.BrowserTabsFrame
	require.NoError(t, json.Unmarshal(data, &current))
	require.Equal(t, 2, current.ActiveIndex)
}

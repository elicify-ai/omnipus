package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestBrowserScopedCriticalRejectsInvalidOrigin(t *testing.T) {
	for _, kind := range []string{"missing", "canceled", "superseded"} {
		t.Run(kind, func(t *testing.T) {
			wc := latestTestConn()
			t.Cleanup(wc.close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var source = ctx
			current := func() bool { return true }
			switch kind {
			case "missing":
				source = nil
			case "canceled":
				cancel()
			case "superseded":
				current = func() bool { return false }
			}
			accepted := wc.sendCriticalScopedGen(map[string]string{"type": "stale"}, "test", source, current)
			if accepted || len(wc.sendCh) != 0 {
				t.Fatalf("invalid %s origin entered critical queue: accepted=%t queued=%d", kind, accepted, len(wc.sendCh))
			}
		})
	}
}

type scopeCancelMarshaler struct{ cancel context.CancelFunc }

func (f scopeCancelMarshaler) MarshalJSON() ([]byte, error) {
	f.cancel()
	return []byte(`{"type":"stale"}`), nil
}

func TestBrowserScopedCriticalRejectsCancellationDuringEncoding(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if wc.sendCriticalScopedGen(scopeCancelMarshaler{cancel}, "test", ctx, nil) || len(wc.sendCh) != 0 {
		t.Fatal("cancellation during encoding entered critical queue")
	}
}

type scopeQueueWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *scopeQueueWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestBrowserScopedCriticalCancellationReleasesFullQueue(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	for i := 0; i < cap(wc.sendCh); i++ {
		wc.sendCriticalGen(map[string]int{"reserved": i}, "fixture")
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &scopeQueueWaitContext{Context: parent, waiting: make(chan struct{})}
	finished := make(chan bool, 1)
	go func() {
		finished <- wc.sendCriticalScopedGen(map[string]string{"type": "stale"}, "test", ctx, nil)
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(500 * time.Millisecond):
		wc.close()
		<-finished
		t.Fatal("full critical queue never reached cancelable source wait")
	}
	cancel()
	select {
	case accepted := <-finished:
		if accepted {
			t.Fatal("canceled queue wait reported acceptance")
		}
	case <-time.After(500 * time.Millisecond):
		wc.close()
		<-finished
		t.Fatal("canceled source remained blocked on critical queue capacity")
	}
}

func TestBrowserScopedLatestRejectsSupersededState(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.True(t, wc.sendLatestScopedGen(browserLatestVideoState, map[string]string{"state": "recovered"}, ctx, func() bool { return true }))
	accepted := wc.sendLatestScopedGen(browserLatestVideoState, map[string]string{"state": "lost"}, ctx, func() bool { return false })
	if accepted {
		t.Error("superseded health event replaced current latest state")
	}
	data, ok := wc.takeLatest()
	require.True(t, ok)
	var got map[string]string
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "recovered", got["state"], "old event overwrote newer recovery")
}

func TestBrowserScopedWriterRejectsRetiredQueuedMessages(t *testing.T) {
	for _, kind := range []string{"critical-source", "critical-state", "latest-state"} {
		t.Run(kind, func(t *testing.T) {
			ready := make(chan *browserWSConn, 1)
			start := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(start) }) }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				wc := latestTestConn()
				wc.conn = conn
				ready <- wc
				<-start
				(&BrowserWSHandler{}).writePump(wc)
			}))
			t.Cleanup(srv.Close)
			client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if response != nil {
				response.Body.Close()
			}
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })
			wc := <-ready
			t.Cleanup(wc.close)
			t.Cleanup(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var current atomic.Bool
			current.Store(true)
			stale := map[string]string{"type": "stale"}
			if kind == "latest-state" {
				require.True(t, wc.sendLatestScopedGen(browserLatestVideoState, stale, ctx, current.Load))
			} else {
				require.True(t, wc.sendCriticalScopedGen(stale, "test", ctx, current.Load))
			}
			if kind == "critical-source" {
				cancel()
			} else {
				current.Store(false)
			}
			wc.sendCriticalGen(map[string]string{"type": "marker"}, "test")
			release()
			require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
			_, data, err := client.ReadMessage()
			require.NoError(t, err)
			var got map[string]string
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, "marker", got["type"], "retired queued message reached transport")
			require.NoError(t, client.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
			_, data, err = client.ReadMessage()
			var timeout net.Error
			if err == nil || !strings.Contains(err.Error(), "timeout") {
				t.Fatalf("unexpected post-marker frame or error: data=%s err=%v", data, err)
			}
			require.ErrorAs(t, err, &timeout)
			require.True(t, timeout.Timeout())
		})
	}
}

func TestBrowserScopedAdmissionRechecksOriginAfterValidatorWait(t *testing.T) {
	for _, retire := range []string{"source", "connection"} {
		t.Run(retire, func(t *testing.T) {
			wc := latestTestConn()
			t.Cleanup(wc.close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			finished := make(chan bool, 1)
			go func() {
				finished <- wc.canSendFrame(browserOutboundFrame{source: ctx, current: func() bool {
					close(entered)
					<-release
					return true
				}})
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("current-state validation never reached its barrier")
			}
			if retire == "source" {
				cancel()
			} else {
				wc.close()
			}
			unblock()
			select {
			case allowed := <-finished:
				if allowed {
					t.Fatalf("retired %s admitted after current-state validator wait", retire)
				}
			case <-time.After(time.Second):
				t.Fatal("validator failed to return after release")
			}
		})
	}
}

type scopeEncodingCallback func()

func (f scopeEncodingCallback) MarshalJSON() ([]byte, error) {
	f()
	return []byte(`{"state":"lost"}`), nil
}

func TestBrowserScopedLatestRechecksStateAfterEncoding(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := true
	frame := scopeEncodingCallback(func() {
		current = false
		require.True(t, wc.sendLatestScopedGen(browserLatestVideoState, map[string]string{"state": "recovered"}, ctx, nil))
	})
	if wc.sendLatestScopedGen(browserLatestVideoState, frame, ctx, func() bool { return current }) {
		t.Error("old event accepted after newer recovery enqueued during encoding")
	}
	data, ok := wc.takeLatest()
	require.True(t, ok, "newer recovery disappeared behind a superseded callback")
	var got map[string]string
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "recovered", got["state"], "old encoding completion replaced newer recovery")
}

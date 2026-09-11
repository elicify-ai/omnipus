package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestBrowserQueuedCommandCannotMigrateAfterAdmission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      browserConnWorkKind
		typ, data string
	}{
		{"input", workKindInput, string(generated.WsFrameTypeBrowserInput), `{"type":"browser_input","kind":"text","text":"old attachment"}`},
		{"tab", workKindTabAction, string(generated.WsFrameTypeBrowserTabAction), `{"type":"browser_tab_action","action":"switch","index":99}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wc, state := newTabActionTestFixtures(t)
			h := &BrowserWSHandler{}
			entered, release := blockSlowHandler(t, tc.kind)
			h.dispatchBrowserCommand(wc, state, "viewer", "user", []byte(tc.data), tc.typ, &config.Config{})
			<-entered
			mgr, _, panel := state.attachment()
			epoch := state.beginAttach()
			require.True(t, state.bindAttachment(epoch, mgr, "replacement-chat", panel))
			release()
			h.activeConns.Wait()
			select {
			case queued := <-wc.sendCh:
				frame := queued.data
				t.Fatalf("obsolete command executed against replacement attachment: %s", frame)
			default:
			}
		})
	}
}

func TestBrowserCommandQueueAgeConsumesExecutionBudget(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered, release := make(chan struct{}), make(chan struct{})
	require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { close(entered); <-release }}))
	<-entered
	var remaining time.Duration
	require.True(t, q.submit(&wg, browserCommand{run: func(ctx context.Context) { deadline, _ := ctx.Deadline(); remaining = time.Until(deadline) }}))
	q.mu.Lock()
	q.jobs[0].enqueued = q.jobs[0].enqueued.Add(-4 * time.Second)
	q.mu.Unlock()
	close(release)
	wg.Wait()
	require.LessOrEqual(t, remaining, time.Second, "four seconds in queue leave at most one second of the five-second budget")
}

func TestBrowserAttachmentLifetimeEndsBeforeReplacement(t *testing.T) {
	_, state := newTabActionTestFixtures(t)
	old := state.commandAttachment()
	mgr, _, panel := state.attachment()
	epoch := state.beginAttach()
	require.ErrorIs(t, old.ctx.Err(), context.Canceled, "new attachment request must immediately cancel admitted work")
	require.True(t, state.bindAttachment(epoch, mgr, "replacement", panel))
	current := state.commandAttachment()
	require.NoError(t, current.ctx.Err())
	require.Equal(t, "tab-action-test-session", old.sessionID)
	state.clearAttachment()
	require.ErrorIs(t, current.ctx.Err(), context.Canceled, "clear must stop this attachment's active operations")
}

func TestBrowserConnectionCloseUnblocksReaderWithoutPeerCooperation(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	readResult := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ready <- conn
		_, _, err = conn.ReadMessage()
		readResult <- err
	}))
	t.Cleanup(srv.Close)
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if response != nil {
		response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	wc := &browserWSConn{conn: <-ready, doneCh: make(chan struct{})}
	wc.close()
	select {
	case err := <-readResult:
		require.Error(t, err)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("connection close left reader and detach cleanup waiting for peer")
	}
}

func TestBrowserInputConverterPreservesPositiveCaptureClaims(t *testing.T) {
	id := "capture-proof"
	for _, n := range []int{-1, 0, 37} {
		frame := generated.BrowserInputFrame{Kind: "mouse_move", CaptureId: &id, CaptureGeneration: &n}
		in := browserInputFrameToLiveInput(frame)
		require.Equal(t, id, in.CaptureID)
		want := uint64(0)
		if n == 37 {
			want = 37
		}
		require.Equal(t, want, in.CaptureGeneration, "negative generation must not wrap to a large unsigned value")
	}
	missing := browserInputFrameToLiveInput(generated.BrowserInputFrame{Kind: "text"})
	require.Zero(t, missing.CaptureGeneration)
	require.Empty(t, missing.CaptureID)
}

func TestBrowserCommandFailurePreservesAttachmentWithTypedStatus(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	before, chat, panel := state.attachment()
	h := &BrowserWSHandler{}
	h.handleInput(wc, state, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"test"}`))
	var status generated.BrowserStatusFrame
	select {
	case queued := <-wc.sendCh:
		data := queued.data
		require.NoError(t, json.Unmarshal(data, &status))
	default:
		t.Fatal("missing command failure status")
	}
	require.Equal(t, "error", status.State)
	require.NotNil(t, status.OperationOnly)
	require.True(t, *status.OperationOnly)
	after, afterChat, afterPanel := state.attachment()
	require.True(t, before == after)
	require.Equal(t, chat, afterChat)
	require.Equal(t, panel, afterPanel)
	select {
	case <-wc.doneCh:
		t.Fatal("operation failure closed healthy attachment")
	default:
	}
	require.Nil(t, sessionErrorStatus(chat, "browser died").OperationOnly, "lifecycle errors must retain lifecycle meaning")
}

func TestBrowserCommandQueueFullCoalescesOnlyTrailingMove(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered, release := make(chan struct{}), make(chan struct{})
	require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { close(entered); <-release }}))
	<-entered
	var got []int
	// The connection contract permits 512 pending commands, excluding active work.
	for i := 0; i < 511; i++ {
		n := i
		require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { got = append(got, n) }}))
	}
	require.True(t, q.submit(&wg, browserCommand{move: true, run: func(context.Context) { got = append(got, -1) }}))
	require.True(t, q.submit(&wg, browserCommand{move: true, run: func(context.Context) { got = append(got, 511) }}))
	require.False(t, q.submit(&wg, browserCommand{run: func(context.Context) { got = append(got, 999) }}), "discrete overflow cannot overwrite motion or an earlier discrete command")
	close(release)
	wg.Wait()
	want := make([]int, 512)
	for i := range want {
		want[i] = i
	}
	require.Equal(t, want, got)
}

func TestBrowserCommandQueueDiscardCancelsActiveAndAcceptsFreshWork(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered := make(chan struct{})
	var canceled error
	require.True(t, q.submit(&wg, browserCommand{run: func(ctx context.Context) { close(entered); <-ctx.Done(); canceled = ctx.Err() }}))
	<-entered
	var stale, fresh bool
	require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { stale = true }}))
	q.discard()
	require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { fresh = true }}))
	wg.Wait()
	require.ErrorIs(t, canceled, context.Canceled)
	require.False(t, stale, "discarded uncertain commands must never replay")
	require.True(t, fresh, "a fresh attachment may use the same connection")
}

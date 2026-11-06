package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 memory (founder: be resourceful with server memory): after a history
// page is prepared, each carrier keeps only what it still sends. A REST page
// keeps the encoded body; a WebSocket attach keeps the frames it has not yet
// handed to the connection. The source page, generated messages and other
// carrier's copy are released before delivery can block on a slow client.
//
// Measured on a page stopped near the 8 MiB ceiling (rows of ~100 KB that
// are never shortened: inline result <= 51,200 B plus tool arguments, which
// A2.1 leaves whole) and on the near-admission set (100 x ~8 MiB results).
// Retained = live heap, after GC, while delivery is blocked.

func historyCarrierHeapNow() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// historyCarrierPeak samples HeapAlloc until stop is called (A2.6 method).
func historyCarrierPeak() (stop func() (peak uint64, samples uint64)) {
	var peak, samples atomic.Uint64
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			samples.Add(1)
			for old := peak.Load(); m.HeapAlloc > old && !peak.CompareAndSwap(old, m.HeapAlloc); old = peak.Load() {
			}
			select {
			case <-done:
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	return func() (uint64, uint64) {
		close(done)
		<-finished
		return peak.Load(), samples.Load()
	}
}

func historyNearCeilingCorpus(t *testing.T, s *session.UnifiedStore, sid string) {
	t.Helper()
	args := strings.Repeat("a", 50000)
	result := strings.Repeat("r", 50000)
	for i := 0; i < 100; i++ {
		e := session.TranscriptEntry{ID: fmt.Sprintf("ceil-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("ceil-call-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{"blob": args}, Result: map[string]any{"text": result}}}}
		require.NoError(t, s.AppendTranscriptStrict(sid, e))
	}
}

func historyNearAdmissionCorpus(t *testing.T, s *session.UnifiedStore, sid string) {
	t.Helper()
	payload := strings.Repeat("x", 8*1024*1024-4096)
	for i := 0; i < 100; i++ {
		e := session.TranscriptEntry{ID: fmt.Sprintf("near-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("call-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": payload}}}}
		require.NoError(t, s.AppendTranscriptStrict(sid, e))
	}
}

// historyStallWriter is a slow REST client: its first body Write blocks until
// released. It counts bytes and keeps none of them.
type historyStallWriter struct {
	header  http.Header
	status  int
	n       atomic.Int64
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *historyStallWriter) Header() http.Header  { return w.header }
func (w *historyStallWriter) WriteHeader(code int) { w.status = code }
func (w *historyStallWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.blocked)
		<-w.release
	})
	w.n.Add(int64(len(p)))
	return len(p), nil
}

func historyRESTRetained(t *testing.T, corpus func(*testing.T, *session.UnifiedStore, string)) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "carrier-release-token")
	sid := createTestSession(t, api)
	corpus(t, api.agentLoop.GetSessionStore(), sid)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/sessions/", api.withAuth(api.HandleSessions))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?limit=100&min_rows=100", nil)
	req.Header.Set("Authorization", "Bearer carrier-release-token")
	w := &historyStallWriter{header: http.Header{}, blocked: make(chan struct{}), release: make(chan struct{})}

	base := historyCarrierHeapNow()
	stopPeak := historyCarrierPeak()
	served := make(chan struct{})
	go func() { defer close(served); mux.ServeHTTP(w, req) }()
	select {
	case <-w.blocked:
	case <-served:
		t.Fatalf("history page was not written (status %d)", w.status)
	}
	blocked := historyCarrierHeapNow()
	close(w.release)
	<-served
	peak, samples := stopPeak()
	require.Equal(t, http.StatusOK, w.status)
	body := w.n.Load()
	retained := int64(blocked) - int64(base)
	t.Logf("history_carrier_release carrier=rest baseline_bytes=%d retained_while_blocked_bytes=%d body_bytes=%d peak_heap_bytes=%d attributable_peak_bytes=%d samples=%d", base, retained, body, peak, int64(peak)-int64(base), samples)
	require.Greater(t, body, int64(0))
	// Only the encoded body (plus small request state) may stay reachable
	// while the client is slow; every other carrier copy is released.
	require.Less(t, retained, body+body/2+(1<<20), "REST delivery keeps duplicate page copies alive")
}

func historyWSRetained(t *testing.T, corpus func(*testing.T, *session.UnifiedStore, string)) {
	orig := attachReplayStallTimeout
	attachReplayStallTimeout = 2 * time.Minute
	t.Cleanup(func() { attachReplayStallTimeout = orig })
	f := newAttachFixture(t, "turn-carrier-release", "msg-carrier-release")
	corpus(t, f.store, f.sid)
	wc := f.newConn("chat-carrier-release")
	frame := []byte(`{"type":"attach_session","session_id":"` + f.sid + `","history_limit":100,"history_min_rows":100}`)

	base := historyCarrierHeapNow()
	stopPeak := historyCarrierPeak()
	attached := make(chan struct{})
	go func() {
		defer close(attached)
		// No cursor: a fresh attach is answered with the prepared snapshot.
		f.h.handleAttachSessionWithAck(context.Background(), "chat-carrier-release", f.sid, nil, nil, wc, frame)
	}()
	stalled := false
	deadline := time.Now().Add(3 * time.Minute)
	for !stalled && time.Now().Before(deadline) {
		select {
		case <-attached:
			deadline = time.Now()
		case <-time.After(20 * time.Millisecond):
			wc.qmu.Lock()
			stalled = wc.qDrained != nil
			wc.qmu.Unlock()
		}
	}
	require.True(t, stalled, "the slow WebSocket client must stall history delivery")
	blocked := historyCarrierHeapNow()
	wc.qmu.Lock()
	queued := int64(wc.qBytes)
	wc.qmu.Unlock()
	inChannel := int64(0)
	for i := len(wc.sendCh); i > 0; i-- {
		inChannel += int64(len(<-wc.sendCh))
	}
	// Let the client catch up so the attach completes normally.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-wc.sendCh:
			case <-attached:
				return
			}
		}
	}()
	<-attached
	<-drained
	peak, samples := stopPeak()
	retained := int64(blocked) - int64(base)
	t.Logf("history_carrier_release carrier=ws baseline_bytes=%d retained_while_blocked_bytes=%d queued_frame_bytes=%d channel_frame_bytes=%d peak_heap_bytes=%d attributable_peak_bytes=%d samples=%d", base, retained, queued, inChannel, peak, int64(peak)-int64(base), samples)
	// Frames already handed to the connection and frames still unsent make
	// up the page's WS bytes (at most the 8 MiB ceiling here); the REST
	// encoding, generated messages and source page must not also be held.
	require.Less(t, retained, session.DefaultChatHistoryByteCeiling+session.DefaultChatHistoryByteCeiling/2, "WebSocket delivery keeps duplicate page copies alive")
}

func TestChatHistoryCarrierRelease_RESTNearCeiling(t *testing.T) {
	historyRESTRetained(t, historyNearCeilingCorpus)
}
func TestChatHistoryCarrierRelease_RESTNearAdmission(t *testing.T) {
	historyRESTRetained(t, historyNearAdmissionCorpus)
}
func TestChatHistoryCarrierRelease_WSNearCeiling(t *testing.T) {
	historyWSRetained(t, historyNearCeilingCorpus)
}

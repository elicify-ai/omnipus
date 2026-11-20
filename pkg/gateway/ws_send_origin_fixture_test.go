package gateway

// Test-only observation and mint boundaries for spec r9 §§4.3/6.1.
// None of these helpers substitutes for the gateway's origin check or queue.

import (
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type soReadResult struct {
	raw []byte
	err error
}

type soReader struct {
	frames chan soReadResult
	stop   chan struct{}
	done   chan struct{}
}

var (
	soReaders sync.Map // *websocket.Conn -> *soReader, removed by the owning test
	soMintSeq atomic.Uint64
)

// Start only when observation windows begin: setup readers may still drain
// admission/mint directly before this point. Once started, all later reads on
// this connection must use soReadNext/soWindow (one Gorilla reader per socket).
func soReaderFor(t *testing.T, conn *websocket.Conn) *soReader {
	t.Helper()
	if existing, ok := soReaders.Load(conn); ok {
		reader, valid := existing.(*soReader)
		require.True(t, valid, "instrument: connection registry must contain a real observation reader")
		return reader
	}
	require.NoError(t, conn.SetReadDeadline(time.Time{}), "clear setup's read failsafe before the persistent reader starts")
	r := &soReader{frames: make(chan soReadResult, 128), stop: make(chan struct{}), done: make(chan struct{})}
	soReaders.Store(conn, r)
	go func() {
		defer close(r.done)
		for {
			_, raw, err := conn.ReadMessage()
			select {
			case r.frames <- soReadResult{raw: raw, err: err}:
			case <-r.stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(r.stop)
		_ = conn.Close()
		<-r.done
		soReaders.Delete(conn)
	})
	return r
}

func soReadNext(t *testing.T, conn *websocket.Conn, timeout time.Duration) []byte {
	t.Helper()
	r := soReaderFor(t, conn)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-r.frames:
		require.NoError(t, result.err, "live observation must receive a frame")
		return result.raw
	case <-timer.C:
		t.Fatal("live observation timed out waiting for its expected frame")
		return nil
	}
}

// mintLiveChat intentionally returns at session_started. Streaming fixtures
// must not wait for their deliberately paused/never-ending provider to finish.
func (s *sendOriginSrv) mintLiveChat(t *testing.T, conn *websocket.Conn, key, content string) string {
	t.Helper()
	id := "so-mint-" + strconv.FormatUint(soMintSeq.Add(1), 10)
	soSend(t, conn, soMessage(nil, soNewChatOrigin(key), id, content))
	started := readFrameOfType(t, conn, "session_started", lifecycleEW)
	require.NotEmpty(t, started.SessionID, "SETUP: the keyed mint must acknowledge")
	return started.SessionID
}

// soServerConn selects the actual requester, not the last map entry (cleanup
// probes must not accidentally inspect a second connection's holding flag).
func soServerConn(t *testing.T, s *sendOriginSrv, conn *websocket.Conn) *wsConn {
	t.Helper()
	s.handler.mu.Lock()
	defer s.handler.mu.Unlock()
	for _, wc := range s.handler.sessions {
		if wc.conn.RemoteAddr().String() == conn.LocalAddr().String() {
			return wc
		}
	}
	t.Fatal("SETUP: the exact browser connection must still be registered")
	return nil
}

func soRequireBound(t *testing.T, s *sendOriginSrv, conn *websocket.Conn, sid string) *wsConn {
	t.Helper()
	wc := soServerConn(t, s, conn)
	require.True(t, s.handler.connBoundToSession(wc, sid), "SETUP: SV-19 connection must be bound to its target")
	return wc
}

func soRequireCompletedMint(t *testing.T, s *sendOriginSrv, conn *websocket.Conn, sid string) {
	t.Helper()
	done := readFrameOfType(t, conn, "done", lifecycleEW)
	require.Equal(t, sid, done.SessionID, "SETUP: drain the keyed mint's own terminal frame")
	waitForLifecycleState(t, s.lc, sid, session.LifecycleCompleted)
	turnID, agentID, startedAt, active := s.al.ActiveForegroundTurnInfo(sid)
	require.False(t, active, "SETUP: settled mint must not contaminate the next transcript/window baseline (turn=%s agent=%s started=%s)", turnID, agentID, startedAt)
}

// A quiet observation must not poison the next one. The positive control goes
// over the same socket and proves a real minted turn reaches done afterwards.
func TestSendOriginWindow_ContinuesAfterQuietWindow(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	require.Empty(t, soWindow(t, conn, 30*time.Millisecond), "instrument: no input, quiet connection")
	soSend(t, conn, soMessage(nil, soNewChatOrigin(soKeyA), "so-window-control", "window live-positive control"))
	frames := soWindow(t, conn, lifecycleEW, "done")
	require.Contains(t, soTypes(frames), "session_started", "instrument: the second window must read the real acknowledgement")
	require.Contains(t, soTypes(frames), "done", "instrument: the same connection must observe a completed live turn")
	var received []generated.MessageStatusFrame
	for _, frame := range frames {
		if frame.Type == "message_status" {
			var status generated.MessageStatusFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &status))
			if status.State == "received" {
				received = append(received, status)
			}
		}
	}
	require.Len(t, received, 1, "instrument: exactly one admission for the live-positive input")
	require.Equal(t, "so-window-control", received[0].ClientMessageId)
}

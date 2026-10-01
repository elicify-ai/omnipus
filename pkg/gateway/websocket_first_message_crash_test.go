package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Issue #1090, founder-approved T1/T2: a new chat must persist its first user
// message before acknowledging the session, and an append failure must be
// visible to the client and prevent turn admission. Oracles come from that
// brief and the investigation report ("Causal chain established by the
// observation and code" and "Proposed fix and risks — not implemented"), not
// from the current intake's output. Retry/dedup design is out of scope.

// issue1090EntropyGate pauses only the external UUID entropy source. The real
// intake, session creation, transcript append, outbound queue and writer run
// unchanged. NewSession uses ULID/crypto-rand, not this UUID reader; the first
// intake UUID read is the transcript-entry ID. No parallel tests: SetRand is
// process-global, restored only after the intake goroutine has been joined.
// This gate supplies a discrete pre-append event, not an elapsed-time oracle.
type issue1090EntropyGate struct {
	entered     chan struct{}
	released    chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func (g *issue1090EntropyGate) Read(p []byte) (int, error) {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.released
	return rand.Read(p)
}

func (g *issue1090EntropyGate) release() {
	g.releaseOnce.Do(func() { close(g.released) })
}

func issue1090Await(t *testing.T, done <-chan struct{}, event string) {
	t.Helper()
	// Outer liveness guard only; no behaviour is inferred from this duration.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("#1090 harness did not finish %s", event)
	}
}

// newIssue1090ChatHarness uses the existing real-agent-loop fixture. Only its
// external model provider is replaced; the loop is not started, so the real
// buffered bus retains every admitted turn for inspection. A real WebSocket
// handshake and production writePump carry outbound frames to a real client.
// The writer starts on demand: at the entropy gate the intake is paused, so
// an already-queued acknowledgement cannot race ahead of the disk inspection.
// This is not an auth/readLoop test; intake is invoked at its existing boundary.
func newIssue1090ChatHarness(t *testing.T) (*WSHandler, *bus.MessageBus, *wsConn, *websocket.Conn, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	handler, _ := newTestWSHandlerForModelName(t, msgBus)

	ready := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade reports handshake errors to the real dialer.
		}
		ready <- conn
	}))
	t.Cleanup(server.Close)
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err, "#1090 harness WebSocket handshake")

	wc := makeTestConn()
	wc.conn = <-ready
	writerDone := make(chan struct{})
	var writerOnce sync.Once
	startWriter := func() {
		writerOnce.Do(func() {
			go func() {
				defer close(writerDone)
				handler.writePump(wc, "")
			}()
		})
	}
	t.Cleanup(func() {
		_ = client.Close()
		wc.close()
		startWriter()
		issue1090Await(t, writerDone, "WebSocket writer cleanup")
	})
	return handler, msgBus, wc, client, startWriter
}

func issue1090PauseFirstAppend(t *testing.T, handler *WSHandler, wc *wsConn, content, clientMessageID string) (*issue1090EntropyGate, <-chan struct{}) {
	t.Helper()
	gate := &issue1090EntropyGate{
		entered:  make(chan struct{}),
		released: make(chan struct{}),
	}
	uuid.SetRand(gate)
	done := make(chan struct{})
	t.Cleanup(func() {
		gate.release()
		issue1090Await(t, done, "first-message intake cleanup")
		uuid.SetRand(nil)
	})
	go func() {
		defer close(done)
		handler.handleChatMessageWithClientID(
			context.Background(), "issue-1090-chat", "", content, "mia", nil,
			"", "", false, clientMessageID, nil, wc,
		)
	}()
	issue1090Await(t, gate.entered, "transcript-entry entropy gate")
	return gate, done
}

func issue1090OnlyTranscript(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	entries, err := os.ReadDir(store.BaseDir())
	require.NoError(t, err, "#1090 harness session directory")
	var transcripts []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session_") {
			transcripts = append(transcripts, filepath.Join(store.BaseDir(), entry.Name(), "transcript.jsonl"))
		}
	}
	require.Len(t, transcripts, 1, "#1090 fixture must mint exactly one NEW session")
	return transcripts[0]
}

func issue1090DiskUserContents(t *testing.T, transcript string) []string {
	t.Helper()
	data, err := os.ReadFile(transcript)
	require.NoError(t, err, "#1090 reads the real transcript, not the store cache")
	decoder := json.NewDecoder(bytes.NewReader(data))
	contents := []string{}
	for {
		var entry session.TranscriptEntry
		err := decoder.Decode(&entry)
		if err == io.EOF {
			return contents
		}
		require.NoError(t, err, "#1090 transcript must contain complete JSONL entries")
		if entry.Role == "user" {
			contents = append(contents, entry.Content)
		}
	}
}

func issue1090ReadClientFrame(t *testing.T, client *websocket.Conn) []byte {
	t.Helper()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(10*time.Second)), "#1090 client liveness guard")
	messageType, data, err := client.ReadMessage()
	require.NoError(t, err, "#1090 client must receive a real WebSocket frame")
	require.Equal(t, websocket.TextMessage, messageType, "#1090 gateway frames use the text transport")
	return data
}

func TestFirstUserMessage_DurableBeforeSessionStarted(t *testing.T) {
	// T1: NEW session, ordinary user message, no retry or kickoff path.
	const content = "T1 first message must survive the gateway crash"
	handler, _, wc, client, startWriter := newIssue1090ChatHarness(t)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "#1090 harness needs the real session store")
	gate, intakeDone := issue1090PauseFirstAppend(t, handler, wc, content, "1090-t1-first")
	transcript := issue1090OnlyTranscript(t, store)

	// If nothing has been acknowledged at this discrete pre-append point, let
	// a corrected implementation append and then acknowledge. Otherwise keep
	// the append paused until the real client receives the queued ack. The
	// queue is stable here: intake is blocked, no writer/agent loop is running.
	if len(wc.sendCh) == 0 {
		gate.release()
	}
	startWriter()
	for {
		var started generated.SessionStartedFrame
		require.NoError(t, json.Unmarshal(issue1090ReadClientFrame(t, client), &started))
		if started.Type != string(generated.WsFrameTypeSessionStarted) {
			continue
		}
		require.Equal(t, filepath.Base(filepath.Dir(transcript)), started.SessionId,
			"T1 must inspect the transcript belonging to the acknowledged session")
		assert.Equal(t, []string{content}, issue1090DiskUserContents(t, transcript),
			"T1: session_started reached the client before the first user message was durably appended")
		break
	}

	gate.release()
	issue1090Await(t, intakeDone, "first-message intake")
	assert.Equal(t, []string{content}, issue1090DiskUserContents(t, transcript),
		"T1 instrument control: releasing entropy must allow the SAME real append to complete")
	t.Log("T1 instrument control: the same disk transcript contains the exact first message after intake completes")
}

func TestFirstUserMessage_AppendFailureIsVisibleAndStopsTurn(t *testing.T) {
	// T2: session minting succeeds; only the first transcript append fails.
	const content = "T2 unsaved first message must not start a turn"
	handler, msgBus, wc, client, startWriter := newIssue1090ChatHarness(t)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "#1090 harness needs the real session store")
	gate, intakeDone := issue1090PauseFirstAppend(t, handler, wc, content, "1090-t2-first")
	transcript := issue1090OnlyTranscript(t, store)
	require.Equal(t, []string{}, issue1090DiskUserContents(t, transcript),
		"T2 fixture: a new session exists but its first entry has not been appended")

	// A directory cannot be opened for an append. Unlike chmod, this real
	// filesystem failure also works for privileged users; no persistence
	// function, fileutil call or gateway method is mocked.
	require.NoError(t, os.Remove(transcript), "T2 fixture replaces only the empty transcript")
	require.NoError(t, os.Mkdir(transcript, 0o700), "T2 fixture creates an invalid append target")
	opened, appendErr := os.OpenFile(transcript, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if opened != nil {
		require.NoError(t, opened.Close())
	}
	var pathErr *os.PathError
	require.ErrorAs(t, appendErr, &pathErr, "T2 instrument must actually reject a writable append open")
	require.Equal(t, "open", pathErr.Op, "T2 fails at the real filesystem-open edge")
	require.Equal(t, transcript, pathErr.Path, "T2 failure targets the new session transcript")
	t.Logf("T2 instrument control: real transcript append-open fails: %v", appendErr)

	gate.release()
	issue1090Await(t, intakeDone, "failed-append intake")
	// Intake is joined and no agent consumes the bus. Thus this snapshot
	// covers all its outbound frames without a sleep or a missed-frame timeout.
	frameCount := len(wc.sendCh)
	startWriter()
	frameTypes := make([]string, 0, frameCount)
	var errorMessages []string
	for range frameCount {
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(issue1090ReadClientFrame(t, client), &frame))
		frameTypes = append(frameTypes, frame.Type)
		if frame.Type == string(generated.WsFrameTypeError) {
			assert.NotEmpty(t, strings.TrimSpace(frame.Message), "T2 error frame must give the user a visible description")
			errorMessages = append(errorMessages, frame.Message)
		}
	}
	// The approved brief does not prescribe new copy or an error code. Assert
	// the EXISTING error-frame contract, not an invented retry/dedup shape.
	assert.NotEmpty(t, errorMessages,
		"T2: transcript append failed but the client received no visible error frame; frames=%v", frameTypes)

	inboundContents := []string{}
	for draining := true; draining; {
		select {
		case message := <-msgBus.InboundChan():
			inboundContents = append(inboundContents, message.Content)
		default:
			draining = false
		}
	}
	assert.Equal(t, []string{}, inboundContents,
		"T2: an unsaved first user message was admitted to the real turn bus")
}

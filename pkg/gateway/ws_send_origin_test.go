package gateway

// ws_send_origin_test.go — RED (#1428 step 2 option C, qa-lead): the
// checkSendOrigin decision table of spec §4.3.1, driven through the REAL WS
// wire (browser / non-browser / CLI connection kinds × origin shapes ×
// targets). SV-9, SV-9b, SV-10 and the P2 acceptance row live here.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence): §4.3.1 (the decision table, quoted
// in the table below), §4.3.2 P1a/P2, §6.1 SV-9, SV-9b, SV-10, SV-5.
//
// RED STATE: no origin check exists today (F-h: the origin is ignored), so
// every REFUSAL row below fails on the frames that arrive instead (the
// message is processed: user_message/message_status; the redirect/cancel
// runs) — the right reason. The ACCEPT rows are guards (P2 legacy, the
// follow-up SV-10) that pass today and must keep passing; their failability
// proof is CHECK's mutation run.
//
// The GREEN reconciliation: 4.3.1 names a pure function (new file
// ws_send_origin.go::checkSendOrigin). The spec's §12.3 command shape
// `^TestCheckSendOrigin$` is kept as the entry point; at RED it drives the
// decision table through the wire (the only surface that exists), and the
// same test author reconciles it onto the pure function at GREEN without
// changing any expected value.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Send-origin keys follow the CONTRACT bounds (SendOrigin.yaml: 16..128
// chars, ^[A-Za-z0-9_-]+$).
const (
	soKeyA = "K-so-origin-chat-a1"
	soKeyB = "K-so-origin-chat-b2"
	soKeyC = "K-so-origin-chat-c3"
	soCLI  = "so-cli-token-fixed-0001"
)

// sendOriginSrv is the shared harness for the whole send-origin family:
// one human account (cookie auth), one CLI token (machine auth), the agent
// loop running, and the provider the caller picks (restMock for idle chats,
// iron for a live turn).
type sendOriginSrv struct {
	srv     *httptest.Server
	handler *WSHandler
	msgBus  *bus.MessageBus
	al      *agent.AgentLoop
	cookie  string
	lc      *session.LifecycleStore
	home    string // the OMNIPUS_HOME this whole stack (WS + rest) shares
}

func newSendOriginSrv(t *testing.T, provider providers.LLMProvider) *sendOriginSrv {
	return newSendOriginSrvMode(t, provider, false)
}

// newSendOriginSrvMode is newSendOriginSrv with gateway.validate_inbound set
// explicitly — SV-12 drives the SAME frame dataset against both modes and
// requires byte-identical replies.
func newSendOriginSrvMode(t *testing.T, provider providers.LLMProvider, validate bool) *sendOriginSrv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cookie, hash, err := middleware.MintSessionToken()
	require.NoError(t, err, "SETUP: mint cookie + hash")
	// The CLI token follows the production double-hash shape (see
	// wu3_1063_mcp_cli_token_test.go): Hash = bcrypt(bcrypt(plain)).
	cliPlain := []byte(soCLI)
	cliHash, cliErr := bcrypt.GenerateFromPassword(cliPlain, bcrypt.MinCost)
	require.NoError(t, cliErr, "SETUP: bcrypt the CLI token")
	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cliToken := config.TokenEntry{Hash: config.BcryptHash(cliHash)}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 0,
			Users:           []config.UserConfig{{Username: "so-user", SessionTokenHash: config.BcryptHash(hash)}},
			CLIToken:        &cliToken,
			ValidateInbound: validate,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, DefaultModel: config.DefaultModel{Model: "send-origin"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	boot := session.NewBootEpochStore(home)
	_, err = boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel).
		SetRevivalStateWriter(al.WriteSteerRevivalState))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
			t.Log("agent loop Run did not exit within 30s")
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)
	return &sendOriginSrv{srv: srv, handler: handler, msgBus: msgBus, al: al, cookie: cookie, lc: lifecycle, home: home}
}

// dialBrowserConn dials as a BROWSER connection (Origin header + cookie).
func (s *sendOriginSrv) dialBrowserConn(t *testing.T) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + trimHTTPForWS(s.srv.URL) + "/api/v1/chat/ws"
	header := http.Header{}
	header.Set("Origin", s.srv.URL)
	header.Set("Cookie", middleware.SessionCookieName+"="+s.cookie)
	conn, httpResp, err := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).Dial(wsURL, header)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "SETUP: browser dial must upgrade")
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// dialCLIConn dials as a programmatic CLI-token connection (no Origin
// header; legacy auth frame carries the CLI token).
func (s *sendOriginSrv) dialCLIConn(t *testing.T) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + trimHTTPForWS(s.srv.URL) + "/api/v1/chat/ws"
	conn, httpResp, err := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).Dial(wsURL, nil)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "SETUP: programmatic dial must upgrade")
	t.Cleanup(func() { _ = conn.Close() })
	raw, marshalErr := json.Marshal(generated.AuthFrame{Type: "auth", Token: soCLI})
	require.NoError(t, marshalErr)
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
	return conn
}

func trimHTTPForWS(url string) string {
	if len(url) > 7 && url[:7] == "http://" {
		return url[4:]
	}
	return url
}

// soMessage builds a message frame with optional origin.
func soMessage(sessionID *string, origin *generated.SendOrigin, clientMsgID, content string) generated.MessageFrame {
	f := generated.MessageFrame{Type: "message", AgentId: strPtr("mia"), Content: content}
	if sessionID != nil {
		f.SessionId = sessionID
	}
	f.Origin = origin
	if clientMsgID != "" {
		f.ClientMessageId = strPtr(clientMsgID)
	}
	return f
}

// soSend writes raw []byte frames verbatim; typed frames are marshalled once.
// Re-marshalling []byte would send a base64 JSON string instead of the frame.
// TestSendOriginRawFramesOnWire verifies the actual bytes at a live receiver.
func soSend(t *testing.T, conn *websocket.Conn, frame any) {
	t.Helper()
	raw, isRaw := frame.([]byte)
	if !isRaw {
		var err error
		raw, err = json.Marshal(frame)
		require.NoError(t, err)
	}
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
}

// soChatOrigin is origin {chat, sid}.
func soChatOrigin(sid string) *generated.SendOrigin {
	return &generated.SendOrigin{Kind: "chat", SessionId: &sid}
}

// soNewChatOrigin is origin {new_chat, key}.
func soNewChatOrigin(key string) *generated.SendOrigin {
	return &generated.SendOrigin{Kind: "new_chat", NewChatKey: &key}
}

// soFrame is one observed wire frame: its type plus the raw bytes, so
// exact-shape assertions decode the REAL payload.
type soFrame struct {
	Type string
	Raw  []byte
}

// soWindow observes a bounded interval without expiring the socket's read.
// A single reader survives quiet intervals; Gorilla documents that a timed-out
// socket read permanently corrupts all subsequent reads on that connection.
func soWindow(t *testing.T, conn *websocket.Conn, window time.Duration, stopTypes ...string) []soFrame {
	t.Helper()
	reader := soReaderFor(t, conn)
	timer := time.NewTimer(window)
	defer timer.Stop()
	var out []soFrame
	for {
		select {
		case result := <-reader.frames:
			require.NoError(t, result.err, "observation reader must remain live")
			var peek wsTypeOnly
			require.NoError(t, json.Unmarshal(result.raw, &peek), "every frame must decode")
			out = append(out, soFrame{Type: peek.Type, Raw: result.raw})
			for _, stop := range stopTypes {
				if peek.Type == stop {
					return out
				}
			}
		case <-timer.C:
			return out
		}
	}
}

// soBarrierPings remembers when soSettle last received a barrier pong on each
// connection: the server answers at most one ping per 100 ms per connection
// (websocket.go::dispatchFrame), so a faster second ping would get no pong.
var soBarrierPings sync.Map // *websocket.Conn -> time.Time

// soBarrierGap keeps consecutive barrier pings outside that debounce.
const soBarrierGap = 110 * time.Millisecond

// soSettle returns every frame the server sends on conn in reply to the frames
// already written to it, without a fixed wait. The server reads one
// connection's frames one at a time and handles each fully in its read loop
// (websocket.go::dispatchFrame) before reading the next; the pong to a ping
// sent now is queued on the same outbound queue after everything that
// handling queued. So the frames before that pong are the complete synchronous
// reply — refusals, old-tab notices, refusal snapshots, receipts, the first
// cancel stage, cancel_idle, and anything a hub publish already returned —
// and the pong itself is not returned. Use soWindow instead when a check needs
// frames produced later by a running turn (tokens, done, a forced stop).
// A missing pong fails the test instead of passing an empty window.
func soSettle(t *testing.T, conn *websocket.Conn) []soFrame {
	t.Helper()
	if last, ok := soBarrierPings.Load(conn); ok {
		if wait := soBarrierGap - time.Since(last.(time.Time)); wait > 0 {
			time.Sleep(wait)
		}
	} else {
		t.Cleanup(func() { soBarrierPings.Delete(conn) })
	}
	soSend(t, conn, generated.PingFrame{Type: string(generated.WsFrameTypePing)})
	reader := soReaderFor(t, conn)
	timer := time.NewTimer(lifecycleEW)
	defer timer.Stop()
	var out []soFrame
	for {
		select {
		case result := <-reader.frames:
			require.NoError(t, result.err, "observation reader must remain live")
			var peek wsTypeOnly
			require.NoError(t, json.Unmarshal(result.raw, &peek), "every frame must decode")
			if peek.Type == string(generated.WsFrameTypePong) {
				// The server's debounce runs from when it SENT this pong, which
				// can be long after the ping was written (the read loop first
				// finishes the frames ahead of it); receipt is never earlier.
				soBarrierPings.Store(conn, time.Now())
				return out
			}
			out = append(out, soFrame{Type: peek.Type, Raw: result.raw})
		case <-timer.C:
			t.Fatalf("barrier: no pong within %s; frames so far: %v", lifecycleEW, soTypes(out))
			return nil
		}
	}
}

func soTypes(frames []soFrame) []string {
	types := make([]string, len(frames))
	for i, f := range frames {
		types[i] = f.Type
	}
	return types
}

// soRefusedFrame finds and decodes the window's send_refused frame, or fails
// naming what arrived instead.
func soRefusedFrame(t *testing.T, frames []soFrame, phase string) generated.SendRefusedFrame {
	t.Helper()
	for _, f := range frames {
		if f.Type == "send_refused" {
			var refused generated.SendRefusedFrame
			require.NoError(t, json.Unmarshal(f.Raw, &refused), "%s: send_refused must decode", phase)
			return refused
		}
	}
	t.Fatalf("%s: no send_refused arrived; window: %v", phase, soTypes(frames))
	return generated.SendRefusedFrame{}
}

// TestCheckSendOrigin drives the §4.3.1 decision table through the wire.
func TestCheckSendOrigin(t *testing.T) {
	t.Run("chat origin matching target accepted", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "check-accept mint")
		soSend(t, conn, soMessage(&b, soChatOrigin(b), "so-accept-1", "matching origin is accepted"))
		frames := soWindow(t, conn, 4*time.Second, "message_status")
		require.Contains(t, soTypes(frames), "message_status",
			"accept row (kind chat, target == origin.session_id): the message must be accepted; window: %v", soTypes(frames))
	})

	t.Run("chat origin mismatched target refused wrong_chat", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "check-mismatch mint")
		a := s.mintChat(t, conn, soKeyA, "check-mismatch other")
		before := s.transcriptLen(t, b)
		soSend(t, conn, soMessage(&b, soChatOrigin(a), "so-mismatch-1", "borrowed origin"))
		frames := soSettle(t, conn)
		refused := soRefusedFrame(t, frames, "4.3.1 kind chat, target != origin.session_id")
		require.Equal(t, "wrong_chat", refused.Reason)
		require.Equal(t, "message", refused.RefusedFrame, "refused_frame must name the message")
		require.NotNil(t, refused.SessionId)
		require.Equal(t, b, *refused.SessionId, "the target must be echoed (valid)")
		require.NotNil(t, refused.ClientMessageId)
		require.Equal(t, "so-mismatch-1", *refused.ClientMessageId, "the correlation id must be echoed (valid)")
		require.NotNil(t, refused.Origin, "a valid origin must be echoed")
		require.Equal(t, 0, s.transcriptLen(t, b)-before,
			"the refused message must not touch B's transcript")
	})

	t.Run("malformed origin refused origin_malformed", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		mintConn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, mintConn)
		b := s.mintChat(t, mintConn, soKeyB, "check-malformed mint")
		// Spec r9 §4.3.2 P1a/M-1/M-2: present-but-malformed origin gets
		// send_refused{origin_malformed}, never the §4.3.4 old-tab error.
		// The live wire control uses this same dataset and sender.
		for _, row := range soMalformedOriginFrames(b) {
			t.Run(row.name, func(t *testing.T) {
				// Each shape has its own connection: a read-window timeout or
				// failed assertion must not prevent the other shapes executing.
				conn := s.dialBrowserConn(t)
				drainAdmissionOneShot(t, conn)
				bindIdleCancelFixture(t, conn, b)
				before := s.transcriptLen(t, b)
				soSend(t, conn, row.raw)
				frames := soSettle(t, conn)
				refused := soRefusedFrame(t, frames, "P1a row: "+row.name)
				require.Equal(t, "origin_malformed", refused.Reason, "P1a row %s: wrong reason (frames: %v)", row.name, soTypes(frames))
				require.Equal(t, "message", refused.RefusedFrame, "P1a row %s: wrong refused_frame", row.name)
				require.Equal(t, []string{"send_refused"}, soTypes(frames), "P1a: exactly one refusal, no side-effect frames or snapshot")
				expected, err := json.Marshal(generated.SendRefusedFrame{
					Type: "send_refused", Reason: "origin_malformed", RefusedFrame: "message",
					SessionId: &b, ClientMessageId: strPtr("so-malformed-1"),
				})
				require.NoError(t, err)
				require.JSONEq(t, string(expected), string(frames[0].Raw), "P1a/C-4: exact valid echoes, malformed origin omitted, unsequenced")
				require.Equal(t, before, s.transcriptLen(t, b), "P1a: refused input must not change the transcript")
			})
		}
	})

	t.Run("new_chat mint with no target accepted", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		soSend(t, conn, soMessage(nil, soNewChatOrigin(soKeyC), "so-mint-1", "mint with key"))
		frames := soWindow(t, conn, 4*time.Second, "session_started")
		require.Contains(t, soTypes(frames), "session_started",
			"accept row (kind new_chat, target empty): the mint must be acknowledged; window: %v", soTypes(frames))
	})

	t.Run("new_chat key matches existing chat accepted follow-up", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sv10 := s.mintChat(t, conn, soKeyC, "follow-up mint")
		soSend(t, conn, soMessage(&sv10, soNewChatOrigin(soKeyC), "so-followup-1", "follow-up by key"))
		frames := soWindow(t, conn, 4*time.Second, "message_status")
		require.Contains(t, soTypes(frames), "message_status",
			"SV-10 follow-up (kind new_chat, metaKey(target) == key) must be accepted; window: %v", soTypes(frames))
	})

	t.Run("new_chat wrong key refused wrong_chat", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sv9 := s.mintChat(t, conn, soKeyA, "SV-9 mint")
		soSend(t, conn, soMessage(&sv9, soNewChatOrigin(soKeyB), "so-sv9-1", "borrowed creation"))
		frames := soSettle(t, conn)
		refused := soRefusedFrame(t, frames, "SV-9 (origin {new_chat, K2} against S keyed K1)")
		require.Equal(t, "wrong_chat", refused.Reason)
	})

	t.Run("new_chat against keyless chat refused wrong_chat", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		// A chat with NO key (store-created, the pre-#1428 shape).
		meta, err := s.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
		require.NoError(t, err, "SETUP: keyless chat")
		soSend(t, conn, soMessage(&meta.ID, soNewChatOrigin(soKeyB), "so-keyless-1", "key against keyless"))
		frames := soSettle(t, conn)
		refused := soRefusedFrame(t, frames, "SV-9 second row (S with no key)")
		require.Equal(t, "wrong_chat", refused.Reason)
	})

	t.Run("SV9b metadata read error refused origin_unverified without stopping unrelated stream", testSV9bMetaReadError)

	t.Run("origin absent non-browser accepted legacy", func(t *testing.T) {
		s := newSendOriginSrv(t, &restMockProvider{})
		conn := s.dialCLIConn(t)
		drainAdmissionOneShot(t, conn)
		b := s.mintChat(t, conn, soKeyB, "P2 mint")
		acceptedBefore := WSOriginLessAcceptedCount()
		soSend(t, conn, soMessage(&b, nil, "so-p2-1", "legacy origin-less send"))
		frames := soWindow(t, conn, 4*time.Second, "message_status")
		require.Contains(t, soTypes(frames), "message_status",
			"P2 acceptance row (origin absent, non-browser): processed as today; window: %v", soTypes(frames))
		require.Equal(t, acceptedBefore+1, WSOriginLessAcceptedCount(), "SV-5/§4.3.8: accepted origin-less programmatic frame increments counter exactly once")
	})
}

// mintChat returns only after the setup turn's own done and lifecycle settle.
// Refusal tests take their transcript baseline AFTER this boundary, never while
// the mint can still append its assistant response. Live fixtures explicitly
// use mintLiveChat instead (their provider is intentionally paused).
func (s *sendOriginSrv) mintChat(t *testing.T, conn *websocket.Conn, key, content string) string {
	t.Helper()
	sid := s.mintLiveChat(t, conn, key, content)
	soRequireCompletedMint(t, s, conn, sid)
	return sid
}

// transcriptLen counts the target chat's transcript entries (the
// nothing-changed oracle for refusals).
func (s *sendOriginSrv) transcriptLen(t *testing.T, sessionID string) int {
	t.Helper()
	entries, err := s.al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err, "read transcript")
	return len(entries)
}

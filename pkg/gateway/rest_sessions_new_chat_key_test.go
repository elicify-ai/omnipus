package gateway

// rest_sessions_new_chat_key_test.go — RED (#1428 step 2 option C, qa-lead):
// the new-chat key (K) is saved in the CREATION WRITE on both creators
// (S-5), never mapped onto the generated Session wire shape, and a failed
// creation write yields no success signal.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - SPEC-C-server-binds-send-to-chat-r8.md §4.2 C-6: SessionCreateRequest
//     gains optional new_chat_key (C-1 format); "Never in Session".
//   - §4.3.3: saved in chat metadata (session.UnifiedMeta.NewChatKey) and
//     carried through the identity codecs; written in the creation write
//     (store.NewSession takes the key; used by createSessionHTTP and
//     websocket_chat.go::mintSession); "Creation-write failure: REST returns
//     the storage error (no 201)"; SV-6/SV-8 scenarios of §6.1.
//   - Contract commit 32802e2a6: SessionCreateRequest.new_chat_key exists on
//     the generated request type; SendOrigin.yaml pins the key format
//     (16..128 chars, ^[A-Za-z0-9_-]+$).
//
// RED STATE: both creators call store.NewSession without any key argument
// today, so the created meta carries no new_chat_key — the tests fail at
// their key assertions (the right reason), not at a build error.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const newChatKeyRestKey = "K-rest-0123456789ab" // 16 chars, ^[A-Za-z0-9_-]+$ (SendOrigin.yaml)

// createSessionWithKey POSTs /api/v1/sessions through the production
// HandleSessions route with a new_chat_key body field.
func createSessionWithKey(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	api.HandleSessions(w, r)
	return w
}

// assertSessionMetaCarriesKey cold-opens a fresh UnifiedStore over the store's
// directory (the §4.3.3 Proof: a fresh store, not the cached object) and
// asserts the persisted identity carries exactly the given key.
func assertSessionMetaCarriesKey(t *testing.T, api *restAPI, sessionID, wantKey, phase string) {
	t.Helper()
	dir := api.agentLoop.GetSessionStore().BaseDir()
	cold, err := session.NewUnifiedStore(dir)
	require.NoError(t, err, "%s: cold-open the store over %s", phase, dir)
	meta, err := cold.GetMeta(sessionID)
	require.NoError(t, err, "%s: GetMeta on cold store", phase)
	require.NotNil(t, meta, "%s: session %s must exist after cold open", phase, sessionID)
	raw, err := json.Marshal(meta)
	require.NoError(t, err, "%s: marshal persisted meta", phase)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire), "%s: re-parse persisted meta", phase)
	got, present := wire["new_chat_key"]
	require.True(t, present,
		"%s: S-5/4.3.3 violated: the creation write saved no new_chat_key at all — "+
			"the persisted identity carries none (persisted shape: %s)", phase, string(raw))
	require.Equal(t, wantKey, got,
		"%s: persisted new_chat_key = %v, want the exact key the creator sent", phase, got)
}

// TestNewChatKey_SV8_RESTCreatePersistsKey covers SV-8 (F2 B): POST /sessions
// with new_chat_key K1 → the created chat's persisted identity carries K1
// after a cold open; GET /sessions/S never returns the key (C-6: "Never in
// Session"); and a follow-up message carrying origin {new_chat, K1} against
// that chat is accepted (the borrow-control of SV-9 must refuse a DIFFERENT
// key — SV-9 is the unit table's row in ws_send_origin_test.go).
func TestNewChatKey_SV8_RESTCreatePersistsKey(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"agent_id": "mia", "workspace_id": "", "new_chat_key": "` + newChatKeyRestKey + `"}`
	w := createSessionWithKey(t, api, body)
	require.Equal(t, http.StatusCreated, w.Code,
		"SETUP: POST /sessions with a valid new_chat_key must create the chat; got %d body=%s", w.Code, w.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created), "201 body must decode")
	require.NotEmpty(t, created.ID, "201 body must carry the session id")

	// §4.3.3 Proof, cold-open leg: the key is on disk, in the identity group.
	assertSessionMetaCarriesKey(t, api, created.ID, newChatKeyRestKey, "SV-8 cold open")

	// C-6: the generated Session wire shape never carries the key.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+created.ID, nil)
	getR.URL.Path = "/api/v1/sessions/" + created.ID
	api.HandleSessions(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code, "GET /sessions/S must return the created chat")
	require.NotContains(t, getW.Body.String(), newChatKeyRestKey,
		"C-6: GET /sessions/S must never return the new-chat key")

	// SV-8's acceptance leg: a message naming S with origin {new_chat, K1}
	// is accepted once the chat exists (GREEN: metaKey(S) == K1; today the
	// origin is ignored and the message is accepted anyway — this leg is the
	// paired positive control and must keep passing).
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler := newWSHandler(msgBus, api.agentLoop, "")
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	originSessionID := newChatKeyRestKey
	followUp := generated.MessageFrame{
		Type:      "message",
		AgentId:   strPtr("mia"),
		SessionId: &created.ID,
		Origin:    &generated.SendOrigin{Kind: "new_chat", NewChatKey: &originSessionID},
		Content:   "follow-up for the keyed chat",
	}
	raw, err := json.Marshal(followUp)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
	msg := awaitInboundMessage(t, msgBus, "SV-8 control: the keyed-chat follow-up must be accepted")
	require.Equal(t, "follow-up for the keyed chat", msg.Content)

	// SV-8's injected-failure leg: an injected creation-write failure before
	// identity publication must yield NO 201. The failure-injection seam the
	// spec's scenario requires does not exist yet — fail loudly, never skip.
	t.Run("injected write failure yields no 201", func(t *testing.T) {
		t.Fatal("BLOCKED: creation-write failure injection seam not implemented — " +
			"required by SV-8 (spec 4.3.3: a failed write yields no success signal)")
	})
}

// TestNewChatKey_SV6_FirstMessageMintPersistsKey covers SV-6 (F2 A): the WS
// mint path — message{origin: {new_chat, K}}, no target — creates S with
// session_started, and K is present after a cold open, survives an unrelated
// identity update, and survives a second cold open.
func TestNewChatKey_SV6_FirstMessageMintPersistsKey(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	mintKey := newChatKeyRestKey
	first := generated.MessageFrame{
		Type:    "message",
		AgentId: strPtr("mia"),
		Origin:  &generated.SendOrigin{Kind: "new_chat", NewChatKey: &mintKey},
		Content: "first message minting the keyed chat",
	}
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))

	started := readFrameOfType(t, conn, "session_started", 10*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID, "SV-6: the mint must acknowledge with session_started")

	// §4.3.3 Proof: cold open #1.
	dir := store.BaseDir()
	cold1, err := session.NewUnifiedStore(dir)
	require.NoError(t, err, "cold open #1")
	meta1, err := cold1.GetMeta(sessionID)
	require.NoError(t, err, "cold open #1: GetMeta")
	require.NotNil(t, meta1, "cold open #1: session %s must exist", sessionID)
	raw1, err := json.Marshal(meta1)
	require.NoError(t, err)
	var wire1 map[string]any
	require.NoError(t, json.Unmarshal(raw1, &wire1))
	got1, present := wire1["new_chat_key"]
	require.True(t, present,
		"SV-6: S-5/4.3.3 violated: the WS mint's creation write saved no new_chat_key — "+
			"the persisted identity carries none (persisted shape: %s)", string(raw1))
	require.Equal(t, mintKey, got1, "SV-6: cold open #1 must read the exact minted key")

	// Unrelated identity update, then cold open #2.
	newTitle := "renamed after mint"
	require.NoError(t, cold1.SetMeta(sessionID, session.MetaPatch{Title: &newTitle}),
		"the unrelated identity update must land")
	cold2, err := session.NewUnifiedStore(dir)
	require.NoError(t, err, "cold open #2")
	meta2, err := cold2.GetMeta(sessionID)
	require.NoError(t, err, "cold open #2: GetMeta")
	require.NotNil(t, meta2)
	raw2, err := json.Marshal(meta2)
	require.NoError(t, err)
	var wire2 map[string]any
	require.NoError(t, json.Unmarshal(raw2, &wire2))
	got2, present := wire2["new_chat_key"]
	require.True(t, present,
		"SV-6: the key did not survive the unrelated identity update + cold open — "+
			"the identity rewrite dropped it (persisted shape: %s)", string(raw2))
	require.Equal(t, mintKey, got2, "SV-6: cold open #2 must still read the exact minted key")

	// Paired positive control: the mint was accepted — the first message
	// reached the bus and the agent loop admits a turn for it (SV-6's "S,
	// session_started" already asserted above; this pins that nothing about
	// the origin-bearing first send refused it).
	msg := awaitInboundMessage(t, msgBus, "SV-6 control: the origin-bearing first message must be accepted")
	require.Equal(t, "first message minting the keyed chat", msg.Content)

	// SV-6's GET leg (the key never appears on the session wire shape) is
	// asserted on the same production route in
	// TestNewChatKey_SV8_RESTCreatePersistsKey; the mint and REST creators
	// share one wire mapping (unifiedMetaToGenSession), so one route check
	// pins the mapping for both.
}

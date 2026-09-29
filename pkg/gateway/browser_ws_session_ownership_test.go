// browser_ws_session_ownership_test.go covers browser-panel session ownership.
// The fixture uses real chat metadata, two user accounts, two workspaces, and
// headless Chrome. Assertions follow the gateway's browser_status response
// channel and the panel binding in ADR-075 FR-017 and ADR-038.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Session-ownership fixture identities. Two accounts, two workspaces, one
// registered agent that sits on both workspace teams.
const (
	soAgentID    = "mia"
	soAliceUser  = "alice"
	soBobUser    = "bob"
	soAliceWS    = "ws-alice"
	soBobWS      = "ws-bob"
	soRefusalTMO = 8 * time.Second
	// soWebrtcTMO covers a real WebRTC negotiation attempt on the owner's
	// browser — the repo's own e2e budget for an answer is 20s (e2eWait,
	// browser_webrtc_e2e_test.go), so the refusal window here must be longer,
	// or a granted answer lands after the oracle gave up.
	soWebrtcTMO = 25 * time.Second
	soQuietTMO  = 1500 * time.Millisecond
)

// soSuccessStates are browser_status states that represent a successful request.
var soSuccessStates = map[string]string{
	"attached":    "live view attached",
	"controlling": "control lock taken",
	"detached":    "detach honored",
}

// soSkipIfNoChrome probes for a working Chrome/Chromium binary (PATH names,
// then the macOS application bundles the PATH probe misses) and skips the
// calling test where none executes — mirroring browserWSSkipIfNoBrowser's
// convention but extended for darwin, where Chrome is never on PATH. Returns
// the working binary path; the caller pins cfg.Tools.Browser.ExecPath to it so
// the resolver trusts a verified binary without flipping trust_path_chrome.
func soSkipIfNoChrome(t *testing.T) string {
	t.Helper()
	if os.Getenv("CI") != "" && os.Getenv("OMNIPUS_BROWSER_E2E") == "" {
		t.Skip("skipping browser-backed ownership tests in CI — set OMNIPUS_BROWSER_E2E=1 to enable")
	}
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	case "linux":
		candidates = append(candidates,
			"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
		)
	}
	for _, name := range candidates {
		probe := exec.Command(name, "--version")
		if probe.Run() != nil {
			continue
		}
		if filepath.IsAbs(name) {
			return name
		}
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("skipping: no working Chrome/Chromium binary found (PATH + platform bundles probed)")
	return ""
}

// soFixture is the two-account real-gateway fixture for one ownership row.
type soFixture struct {
	handler    *BrowserWSHandler
	al         *agent.AgentLoop
	mgr        *browser.BrowserManager // the owner's (ws-alice) manager
	alicePanel string                  // PanelTabSetID for the owner's session
	aliceSess  string
	bobSess    string
	aliceToken string
	bobToken   string
	aliceConn  *websocket.Conn
	bobConn    *websocket.Conn
	srv        *httptest.Server
}

// soUserToken derives a deterministic two-account bearer token in the
// harness's established shape ("omnipus_" + 64×<digit>, cf.
// TestBrowserWS_Auth_ValidUserToken_ConnectionProceeds) plus its bcrypt hash.
func soUserToken(t *testing.T, digit string) (string, string) {
	t.Helper()
	token := "omnipus_" + strings.Repeat(digit, 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)
	return token, string(hash)
}

// newSessionOwnershipFixture builds the fixture WITHOUT connecting anyone.
func newSessionOwnershipFixture(t *testing.T, mutate func(cfg *config.Config)) *soFixture {
	t.Helper()
	chrome := soSkipIfNoChrome(t)
	t.Cleanup(config.SetMemoryProviderForTest(
		func() (bool, bool) { return false, true },
		func() (uint64, bool) { return 8 << 30, true },
	))

	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	aliceToken, aliceHash := soUserToken(t, "7")
	bobToken, bobHash := soUserToken(t, "8")

	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Users = []config.UserConfig{
			{Username: soAliceUser, Tokens: []config.TokenEntry{{Hash: config.BcryptHash(aliceHash)}}},
			{Username: soBobUser, Tokens: []config.TokenEntry{{Hash: config.BcryptHash(bobHash)}}},
		}
		cfg.Tools.Browser.Headless = true
		cfg.Tools.Browser.ExecPath = chrome
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.PageTimeoutSec = 30
		if mutate != nil {
			mutate(cfg)
		}
	})

	// Two workspaces, one per account, agent "mia" on both core teams. The
	// harness seed (mustAgentLoop) already put "mia" in its own default
	// workspace, so every attach below MUST name a session whose meta carries
	// workspace_id — the FR-033 ambiguity guard otherwise refuses, exactly as
	// in production.
	home := config.OmnipusHomeDir()
	for id := range map[string]bool{soAliceWS: true, soBobWS: true} {
		require.NoError(t, writeWorkspaceFile(home, storedWorkspace{
			ID:       id,
			Name:     id,
			Status:   "active",
			CoreTeam: []string{soAgentID},
		}))
	}

	// Two real chat sessions with Owner + WorkspaceID — the fields the ruling
	// turns into the ownership boundary.
	store := al.GetSessionStore()
	require.NotNil(t, store, "AgentLoop must expose the shared session store")
	aliceMeta, err := store.NewSession(session.SessionTypeChat, "webchat", soAgentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(aliceMeta.ID, session.MetaPatch{
		Owner:       strPtrSO(soAliceUser),
		WorkspaceID: strPtrSO(soAliceWS),
	}))
	bobMeta, err := store.NewSession(session.SessionTypeChat, "webchat", soAgentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(bobMeta.ID, session.MetaPatch{
		Owner:       strPtrSO(soBobUser),
		WorkspaceID: strPtrSO(soBobWS),
	}))

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), soAgentID, soAliceWS)
	require.Equal(t, agent.BrowserResolveOK, outcome, "owner's workspace must resolve a browser manager")
	t.Cleanup(mgr.Shutdown)

	f := &soFixture{
		handler:    handler,
		al:         al,
		mgr:        mgr,
		alicePanel: mgr.PanelTabSetID(aliceMeta.ID),
		aliceSess:  aliceMeta.ID,
		bobSess:    bobMeta.ID,
		aliceToken: aliceToken,
		bobToken:   bobToken,
	}
	t.Cleanup(f.closeConns)
	return f
}

// start serves the handler and dials+authenticates one connection per
// account. Attach calls stay with the individual rows.
func (f *soFixture) start(t *testing.T) {
	t.Helper()
	t.Cleanup(f.handler.Wait)
	f.srv = httptest.NewServer(f.handler)
	t.Cleanup(f.srv.Close)
	f.aliceConn = f.dialAuth(t, soAliceUser)
	f.bobConn = f.dialAuth(t, soBobUser)
}

func (f *soFixture) dialAuth(t *testing.T, user string) *websocket.Conn {
	t.Helper()
	conn := dialBrowserTestWS(t, f.srv)
	t.Cleanup(func() { _ = conn.Close() })
	token := f.aliceToken
	if user == soBobUser {
		token = f.bobToken
	}
	writeBrowserAuthFrame(t, conn, token)
	return conn
}

func (f *soFixture) closeConns() {
	for _, c := range []*websocket.Conn{f.aliceConn, f.bobConn} {
		if c != nil {
			_ = c.Close()
		}
	}
}

// attachAndRequire sends browser_attach for (agentID, sessionID) and requires
// the "attached" success status — the owner-allowed path every row leans on.
//
// Every successful attach unconditionally follows with a browser_webrtc_state
// announcement (ADR-047, handleAttach's call to
// announceWebRTCAvailabilityContext) — a real, deterministic side effect of
// the OWNER's own attach, not something a later non-owner request could have
// triggered. Drained here, so a subsequent soExpectQuiet on this same
// connection cannot mistake this connection's own queued frame for a leak
// caused by someone else's refused request.
func (f *soFixture) attachAndRequire(t *testing.T, conn *websocket.Conn, agentID, sessionID string) {
	t.Helper()
	soSendAttach(t, conn, agentID, sessionID, false)
	resp := readBrowserStatusFrame(t, conn, 20*time.Second)
	require.Equal(t, "attached", resp.State,
		"owner attach must succeed against real headless Chrome: %+v", resp)
	state := readBrowserFrame(t, conn, soRefusalTMO)
	require.Equal(t, "browser_webrtc_state", state.Type,
		"the post-attach WebRTC-availability announcement must follow immediately: %+v", state)
}

// --- frame senders (one per frame type in the ruling's scope) ---

func soSendAttach(t *testing.T, conn *websocket.Conn, agentID, sessionID string, dedicated bool) {
	t.Helper()
	frame := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   agentID,
		SessionId: sessionID,
	}
	if dedicated {
		mode := "dedicated"
		frame.InputMode = &mode
	}
	soWrite(t, conn, frame)
}

func soSendInput(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	soWrite(t, conn, generated.BrowserInputFrame{
		Type: string(generated.WsFrameTypeBrowserInput),
		Kind: "reload",
	})
}

func soSendControlTake(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	soWrite(t, conn, generated.BrowserControlFrame{
		Type:   string(generated.WsFrameTypeBrowserControl),
		Action: "take",
	})
}

func soSendTabAction(t *testing.T, conn *websocket.Conn, agentID, sessionID string) {
	t.Helper()
	// action=open needs no index and succeeds silently when honored.
	soWrite(t, conn, generated.BrowserTabActionFrame{
		Type:      string(generated.WsFrameTypeBrowserTabAction),
		Action:    "open",
		AgentId:   &agentID,
		SessionId: &sessionID,
	})
}

func soSendViewport(t *testing.T, conn *websocket.Conn, agentID, sessionID string) {
	t.Helper()
	soWrite(t, conn, generated.BrowserViewportFrame{
		Type:      string(generated.WsFrameTypeBrowserViewport),
		AgentId:   &agentID,
		SessionId: &sessionID,
		Width:     801,
		Height:    601,
	})
}

func soSendDetach(t *testing.T, conn *websocket.Conn, sessionID string) {
	t.Helper()
	soWrite(t, conn, generated.BrowserDetachFrame{
		Type:      string(generated.WsFrameTypeBrowserDetach),
		SessionId: &sessionID,
	})
}

func soSendInputOffer(t *testing.T, conn *websocket.Conn, agentID, sessionID, sdp string) {
	t.Helper()
	soWrite(t, conn, generated.BrowserInputOfferFrame{
		Type:         string(generated.WsFrameTypeBrowserInputOffer),
		AgentId:      agentID,
		SessionId:    sessionID,
		Sdp:          sdp,
		OfferId:      1,
		InputEpoch:   1,
		ControlEpoch: 0,
	})
}

func soSendWebrtcOffer(t *testing.T, conn *websocket.Conn, agentID, sessionID, sdp string) {
	t.Helper()
	soWrite(t, conn, generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   agentID,
		SessionId: sessionID,
		Sdp:       sdp,
		OfferId:   intPtrSO(1),
	})
}

func soWrite(t *testing.T, conn *websocket.Conn, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

// --- response readers (the oracle) ---

// soExpectRefusal checks the browser_status refusal and excludes success states.
func soExpectRefusal(t *testing.T, conn *websocket.Conn, label string) {
	t.Helper()
	resp := readBrowserStatusFrame(t, conn, soRefusalTMO)
	if state, ok := soSuccessStates[resp.State]; ok {
		t.Fatalf("%s: non-owner was GRANTED %s — browser_status{state:%q session_id:%q message:%q}; "+
			"the ownership ruling requires a refusal (browser_status error) here",
			label, state, resp.State, resp.SessionID, resp.Message)
	}
	require.Equal(t, "error", resp.State,
		"%s: non-owner frame must be refused with a browser_status error frame: %+v", label, resp)
	if label == "browser_control" {
		require.Equal(t, "browser_control: attach before requesting control", resp.Message)
	} else {
		require.Equal(t, label+": session not available", resp.Message,
			"%s: only the ownership refusal qualifies: %+v", label, resp)
	}
}

func soExpectOwnerRefusal(t *testing.T, conn *websocket.Conn, operation, sessionID string) {
	t.Helper()
	resp := readBrowserStatusFrame(t, conn, soRefusalTMO)
	require.Equal(t, "error", resp.State, "%s: expected ownership refusal: %+v", operation, resp)
	require.Equal(t, sessionID, resp.SessionID, "%s: refusal must name the requested session", operation)
	require.Equal(t, operation+": session not available", resp.Message,
		"%s: a different error must not count as an ownership refusal", operation)
}

// soExpectQuiet requires NO frame of any kind within the window. The owner's
// channel must carry nothing triggered by the non-owner's frame; a close is
// also a failure (the owner must stay connected).
func soExpectQuiet(t *testing.T, conn *websocket.Conn, label string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(soQuietTMO)) // errcheck rationale: test-only conn deadline
	_, raw, err := conn.ReadMessage()
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return // quiet — the required outcome
		}
		t.Fatalf("%s: owner's connection closed or errored while expected quiet: %v", label, err)
	}
	t.Fatalf("%s: owner's connection received a frame triggered by the non-owner's request: %s", label, raw)
}

// soMintOfferSDP builds a real, gather-complete SDP offer with pion — the
// browser_webrtc_offer / browser_input_offer claims validation demands a
// parseable offer before any refusal path that sits behind it.
func soMintOfferSDP(t *testing.T) string {
	t.Helper()
	pc, err := pion.NewPeerConnection(pion.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	recvonly := pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}
	_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, recvonly)
	require.NoError(t, err)
	_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeAudio, recvonly)
	require.NoError(t, err)
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	gatherComplete := pion.GatheringCompletePromise(pc)
	require.NoError(t, pc.SetLocalDescription(offer))
	select {
	case <-gatherComplete:
	case <-time.After(10 * time.Second):
		t.Fatal("ICE gathering did not complete within 10s")
	}
	local := pc.LocalDescription()
	require.NotNil(t, local, "LocalDescription must be present after gathering")
	return local.SDP
}

func strPtrSO(s string) *string { return &s }

func intPtrSO(i int) *int { return &i }

// ---------------------------------------------------------------------------
// Rows — one per frame type that names or addresses a session.
// ---------------------------------------------------------------------------

// Positive control: BOTH owners attach to their OWN sessions. Pins the
// fixture (two accounts, two workspaces, real Chrome) and the property any
// fix must preserve: ownership checks refuse exactly the foreign frames.
func TestBrowserWS_SessionOwnership_Attach_OwnerAllowed(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)
}

func TestBrowserWS_SessionOwnership_RESTCreatedSession_OwnerCanAttach(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	api := &restAPI{agentLoop: f.al, homePath: config.OmnipusHomeDir()}
	req := generated.SessionCreateRequest{AgentId: strPtrSO(soAgentID), WorkspaceId: strPtrSO(soAliceWS)}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.aliceToken)
	w := httptest.NewRecorder()
	api.withAuth(api.HandleSessions)(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "REST create failed: %s", w.Body.String())

	var created generated.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)
	meta, err := f.al.GetSessionStore().GetMeta(created.Id)
	require.NoError(t, err)
	require.Equal(t, soAliceUser, meta.Owner, "REST must persist the authenticated account")
	require.Equal(t, soAliceWS, meta.WorkspaceID)

	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, created.Id)
}

func soLimitAgentToWorkspace(t *testing.T, workspaceID string) {
	t.Helper()
	home := config.OmnipusHomeDir()
	ids, _ := workspace.FindAllForAgent(home, soAgentID)
	require.Contains(t, ids, workspaceID)
	for _, id := range ids {
		if id != workspaceID {
			seedBindingWorkspace(t, home, id)
		}
	}
	ids, _ = workspace.FindAllForAgent(home, soAgentID)
	require.Equal(t, []string{workspaceID}, ids)
}

func soInspectSession(t *testing.T, f *soFixture, token, sessionID string) generated.BrowserInspectResponse {
	t.Helper()
	api := &restAPI{agentLoop: f.al, homePath: config.OmnipusHomeDir()}
	body := inspectRequestBody(t, generated.BrowserInspectRequest{
		AgentId: soAgentID, SessionId: sessionID, X: 10, Y: 10,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	api.withAuth(api.HandleBrowserInspect)(w, r)
	require.Equal(t, http.StatusOK, w.Code, "inspect response: %s", w.Body.String())
	var response generated.BrowserInspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	return response
}

func soRequireInspectRefusal(t *testing.T, response generated.BrowserInspectResponse) {
	t.Helper()
	require.False(t, response.Ok)
	require.NotNil(t, response.Reason)
	require.Equal(t, "session not available", *response.Reason)
	require.Nil(t, response.Tag)
	require.Nil(t, response.Text)
	require.Nil(t, response.Html)
}

func TestBrowserWS_SessionOwnership_Inspect_UnknownSessionRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	soLimitAgentToWorkspace(t, soAliceWS)
	soRequireInspectRefusal(t, soInspectSession(t, f, f.bobToken, "01JZZZZZZZZZZZZZZZZZZZZZZZ"))
}

func TestBrowserWS_SessionOwnership_Inspect_ResolvedWorkspaceMustMatchChat(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	soLimitAgentToWorkspace(t, soBobWS)
	soRequireInspectRefusal(t, soInspectSession(t, f, f.aliceToken, f.aliceSess))
}

func TestBrowserWS_SessionOwnership_Attach_ResolvedWorkspaceMustMatchChat(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	soLimitAgentToWorkspace(t, soBobWS)
	f.start(t)
	soSendAttach(t, f.aliceConn, soAgentID, f.aliceSess, false)
	soExpectOwnerRefusal(t, f.aliceConn, "browser_attach", f.aliceSess)
}

func TestBrowserWS_SessionOwnership_Attach_UnknownSessionRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	const missingSession = "01JZZZZZZZZZZZZZZZZZZZZZZZ"
	soSendAttach(t, f.aliceConn, soAgentID, missingSession, false)
	soExpectOwnerRefusal(t, f.aliceConn, "browser_attach", missingSession)
}

// An attach naming a session outside the caller's account is refused.
func TestBrowserWS_SessionOwnership_Attach_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess) // owner pinned

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, false)
	soExpectRefusal(t, f.bobConn, "browser_attach")

	// Side effect: no control lock appeared on the owner's tab set, and the
	// owner's channel carries nothing from the attempt.
	require.Equal(t, "", f.mgr.Live().Controller(f.alicePanel),
		"non-owner attach attempt must leave the owner's control state untouched")
	soExpectQuiet(t, f.aliceConn, "browser_attach")
}

func TestBrowserWS_SessionOwnership_Attach_RefusalPreservesCurrentView(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)
	mgr, outcome := f.al.BrowserManagerForAgent(context.Background(), soAgentID, soBobWS)
	require.Equal(t, agent.BrowserResolveOK, outcome)
	panel := mgr.PanelTabSetID(f.bobSess)

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, false)
	soExpectOwnerRefusal(t, f.bobConn, "browser_attach", f.aliceSess)
	soSendControlTake(t, f.bobConn)
	controlled := readBrowserStatusFrame(t, f.bobConn, soRefusalTMO)
	require.Equal(t, "controlling", controlled.State,
		"refused attach must leave the previous attachment usable: %+v", controlled)
	require.Equal(t, f.bobSess, controlled.SessionID)
	require.NotEmpty(t, mgr.Live().Controller(panel), "the original viewer must still hold its own control lock")
}

// Browser input follows the connection's authorized attachment.
func TestBrowserWS_SessionOwnership_Input_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, false)
	soExpectOwnerRefusal(t, f.bobConn, "browser_attach", f.aliceSess)

	// A refused attachment leaves input without a bound session.
	soSendInput(t, f.bobConn)
	soExpectQuiet(t, f.bobConn, "browser_input after refused attach")
	soExpectQuiet(t, f.aliceConn, "browser_input")
}

// Browser control is available only through an authorized attachment.
func TestBrowserWS_SessionOwnership_Control_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, false)
	soExpectOwnerRefusal(t, f.bobConn, "browser_attach", f.aliceSess)

	soSendControlTake(t, f.bobConn)
	soExpectRefusal(t, f.bobConn, "browser_control")

	require.Equal(t, "", f.mgr.Live().Controller(f.alicePanel),
		"non-owner control take must leave the owner's control lock unset")
	soExpectQuiet(t, f.aliceConn, "browser_control")
}

// A named tab action respects the current attachment's ownership.
func TestBrowserWS_SessionOwnership_TabAction_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)

	soSendTabAction(t, f.bobConn, soAgentID, f.aliceSess)
	soExpectRefusal(t, f.bobConn, "browser_tab_action")
	soExpectQuiet(t, f.aliceConn, "browser_tab_action")
}

// A named viewport update respects the current attachment's ownership.
func TestBrowserWS_SessionOwnership_Viewport_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)

	beforeW, beforeH, beforeOK := f.mgr.Live().CSSViewport(f.alicePanel)
	soSendViewport(t, f.bobConn, soAgentID, f.aliceSess)
	soExpectRefusal(t, f.bobConn, "browser_viewport")

	afterW, afterH, afterOK := f.mgr.Live().CSSViewport(f.alicePanel)
	require.Equal(t, beforeOK, afterOK, "owner's viewport state must be untouched")
	if beforeOK {
		require.Equal(t, beforeW, afterW, "owner's viewport width must be untouched")
		require.Equal(t, beforeH, afterH, "owner's viewport height must be untouched")
	}
	soExpectQuiet(t, f.aliceConn, "browser_viewport")
}

// Refusing a named detach leaves the connection's attachment intact.
func TestBrowserWS_SessionOwnership_Detach_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)

	soSendDetach(t, f.bobConn, f.aliceSess)
	soExpectRefusal(t, f.bobConn, "browser_detach")
	soExpectQuiet(t, f.aliceConn, "browser_detach")
}

func TestBrowserWS_SessionOwnership_Detach_RefusalPreservesCurrentView(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.bobConn, soAgentID, f.bobSess)
	mgr, outcome := f.al.BrowserManagerForAgent(context.Background(), soAgentID, soBobWS)
	require.Equal(t, agent.BrowserResolveOK, outcome)
	panel := mgr.PanelTabSetID(f.bobSess)

	entered, release := blockSlowHandler(t, workKindTabAction)
	soWrite(t, f.bobConn, generated.BrowserTabActionFrame{
		Type: string(generated.WsFrameTypeBrowserTabAction), Action: "open",
	})
	select {
	case <-entered:
	case <-time.After(soRefusalTMO):
		t.Fatal("own tab command did not begin before the refused detach")
	}
	soSendControlTake(t, f.bobConn)
	soSendDetach(t, f.bobConn, f.aliceSess)
	soExpectOwnerRefusal(t, f.bobConn, "browser_detach", f.aliceSess)
	release()
	require.Eventually(t, func() bool { return mgr.Live().Controller(panel) != "" },
		soRefusalTMO, 10*time.Millisecond,
		"refused detach discarded Bob's queued control request or canceled his active command")
	controlled := readBrowserStatusFrame(t, f.bobConn, soRefusalTMO)
	require.Equal(t, "controlling", controlled.State,
		"refused detach must leave the queued request and attachment usable: %+v", controlled)
	require.Equal(t, f.bobSess, controlled.SessionID)
}

// A dedicated-input offer requires an authorized dedicated attachment.
func TestBrowserWS_SessionOwnership_InputOffer_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, nil)
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, true)
	soExpectOwnerRefusal(t, f.bobConn, "browser_attach", f.aliceSess)

	sdp := soMintOfferSDP(t)
	soSendInputOffer(t, f.bobConn, soAgentID, f.aliceSess, sdp)
	assertNoInputStateGranted(t, f.bobConn)
	soExpectQuiet(t, f.aliceConn, "browser_input_offer")
}

// A WebRTC offer naming another account's session receives an explicit refusal.
func TestBrowserWS_SessionOwnership_WebrtcOffer_NonOwnerRefused(t *testing.T) {
	f := newSessionOwnershipFixture(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true // the row exercises the real webrtc offer path
	})
	f.start(t)
	f.attachAndRequire(t, f.aliceConn, soAgentID, f.aliceSess)

	soSendAttach(t, f.bobConn, soAgentID, f.aliceSess, false)
	soExpectOwnerRefusal(t, f.bobConn, "browser_attach", f.aliceSess)

	cfg := f.al.GetConfig()
	require.True(t, cfg.Tools.Browser.WebRTCEnabled, "fixture must enable the webrtc path this row exercises")

	sdp := soMintOfferSDP(t)
	soSendWebrtcOffer(t, f.bobConn, soAgentID, f.aliceSess, sdp)
	soExpectRefusalNoAnswer(t, f.bobConn, "browser_webrtc_offer", f.aliceSess)
	soExpectQuiet(t, f.aliceConn, "browser_webrtc_offer")
}

// soExpectRefusalNoAnswer requires a status refusal and excludes a media answer.
func soExpectRefusalNoAnswer(t *testing.T, conn *websocket.Conn, label, sessionID string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(soWebrtcTMO)) // errcheck rationale: test-only conn deadline
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s: no refusal arrived on the non-owner's channel: %v", label, err)
		}
		var f browserFrameDecoder
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if f.Type == string(generated.WsFrameTypeBrowserWebrtcAnswer) {
			t.Fatalf("%s: non-owner was granted a WebRTC answer (media path): %s", label, raw)
		}
		if f.Type != "browser_status" {
			continue
		}
		if state, ok := soSuccessStates[f.State]; ok {
			t.Fatalf("%s: non-owner was GRANTED %s — browser_status{state:%q message:%q}; "+
				"the ownership ruling requires a refusal", label, state, f.State, f.Message)
		}
		require.Equal(t, "error", f.State,
			"%s: expected browser_status refusal: %s", label, raw)
		require.Equal(t, sessionID, f.SessionID, "%s: refusal must name the requested session", label)
		require.Equal(t, label+": session not available", f.Message,
			"%s: a different error must not count as an ownership refusal", label)
		return
	}
}

// assertNoInputStateGranted reads frames after a refused-mode input offer and
// fails if dedicated-input machinery answers the non-owner at all.
func assertNoInputStateGranted(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(soQuietTMO)) // errcheck rationale: test-only conn deadline
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return
			}
			t.Fatalf("dedicated input observer closed before the quiet window ended: %v", err)
		}
		var f browserFrameDecoder
		if json.Unmarshal(raw, &f) == nil && f.Type == "browser_input_state" {
			t.Fatalf("non-owner received dedicated-input machinery frames: %s", raw)
		}
	}
}

// assertNoWebRTCAnswer fails the row the moment a browser_webrtc_answer — a
// granted media path — reaches the non-owner.
func assertNoWebRTCAnswer(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(soQuietTMO)) // errcheck rationale: test-only conn deadline
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return // quiet window elapsed — no answer ever came
		}
		var f browserFrameDecoder
		if json.Unmarshal(raw, &f) == nil && f.Type == "browser_webrtc_answer" {
			t.Fatalf("non-owner was granted a WebRTC answer (media path): %s", raw)
		}
	}
}

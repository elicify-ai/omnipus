// browser_webrtc_media_port_test.go — round-2 finding F6.
//
// FIX WAVE B finding C made a taken fixed media UDP port fall back to a
// neighbouring free port instead of collapsing to nil. That is the right
// behaviour and stays (TestSharedMediaConn_ConfiguredPortTaken_FallsBackTo
// NextFreePort in browser_ws_fixwaveb_test.go still pins it, unchanged) — but
// it turned a loud failure into a quiet one for the ONLY person who can fix
// it. tools.browser.webrtc_media_udp_port has no default, so a non-zero value
// is always an operator's explicit instruction, and a hosted provider routes
// ONLY the port that operator declared: after the fallback, live video works
// on localhost and is dead for every remote viewer, with the panel claiming
// nothing is wrong. That is precisely the invisible-degradation shape ADR-061
// deleted the JPEG screencast fallback to eliminate.
//
// These tests pin the two halves of the fix that make the degradation
// impossible to miss:
//   - the fallback is RECORDED (h.mediaPortFallback) and rendered into an
//     operator-facing sentence naming BOTH ports and the consequence, and
//   - that sentence is actually PUSHED to the panel as browser_status(error),
//     within the wire contract's 512-char message limit so the SPA's zod edge
//     validation cannot silently drop it.
package gateway

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ---------------------------------------------------------------------------
// The degradation is recorded, and names both ports
// ---------------------------------------------------------------------------

// TestSharedMediaConn_TakenPort_RecordsOperatorVisibleDegradation
//
// BDD: Given tools.browser.webrtc_media_udp_port names a port another process
// already holds,
// When the gateway binds its shared media socket and falls back,
// Then the fallback is recorded and rendered as a sentence that names the
// configured port, the port actually bound, and the consequence for a remote
// viewer — so the panel can say WHY video will not reach anyone off this
// host, instead of leaving that to one WARN line among hundreds.
func TestSharedMediaConn_TakenPort_RecordsOperatorVisibleDegradation(t *testing.T) {
	taken := occupyUDPPort(t)

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaUDPPort = taken

	h := &BrowserWSHandler{}
	conn := h.sharedMediaConn(cfg)
	require.NotNil(t, conn, "the fallback itself must survive — F6 keeps it, it only makes it visible")
	t.Cleanup(func() { _ = conn.Close() })
	bound := mediaConnPort(t, conn)

	notice := h.mediaPortFallbackNotice()
	require.NotEmpty(t, notice,
		"a fallback off the operator's declared port MUST produce a user-visible notice — a log line alone "+
			"is invisible to the person who has to free the port or fix the config")
	assert.Contains(t, notice, strconv.Itoa(taken), "the notice must name the CONFIGURED port")
	assert.Contains(t, notice, strconv.Itoa(bound), "the notice must name the port actually BOUND")
	assert.Contains(t, strings.ToLower(notice), "remote viewer",
		"the notice must state the CONSEQUENCE (a remote viewer gets no picture), not just the port swap")
}

// TestSharedMediaConn_ConfiguredPortFree_NoDegradationNotice is the other
// direction, and it is what keeps this fix from becoming noise: an ordinary
// install that binds exactly the port it asked for must say nothing at all.
func TestSharedMediaConn_ConfiguredPortFree_NoDegradationNotice(t *testing.T) {
	probe, err := net.ListenPacket("udp", ":0")
	require.NoError(t, err)
	free := mediaConnPort(t, probe)
	require.NoError(t, probe.Close())

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaUDPPort = free

	h := &BrowserWSHandler{}
	conn := h.sharedMediaConn(cfg)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close() })
	require.Equal(t, free, mediaConnPort(t, conn))

	assert.Empty(t, h.mediaPortFallbackNotice(),
		"nothing degraded, so nothing may be reported — a warning on a healthy install trains operators to "+
			"ignore the one that matters")
}

// TestSharedMediaConn_Unconfigured_NoDegradationNotice pins the laptop
// default (port 0 = ephemeral, pre-ADR-062): there is no operator intent to
// override, so there is nothing to report.
func TestSharedMediaConn_Unconfigured_NoDegradationNotice(t *testing.T) {
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedMediaConn(&config.Config{}))
	assert.Empty(t, h.mediaPortFallbackNotice(),
		"fixed-port media is opt-in; the untouched default must not raise an operator alarm")
}

// ---------------------------------------------------------------------------
// The degradation actually reaches the panel
// ---------------------------------------------------------------------------

// TestNotifyMediaPortDegraded_PushesStatusErrorToThePanel is the ADR-061 half:
// the notice is worthless unless it crosses the wire to the viewer.
//
// BDD: Given the shared media socket fell back off the configured port,
// When a viewer's WebRTC offer succeeds,
// Then that viewer is sent a browser_status(error) carrying the notice — which
// the SPA renders as a persistent strip under the (locally working) video,
// rather than a dead panel with no explanation.
func TestNotifyMediaPortDegraded_PushesStatusErrorToThePanel(t *testing.T) {
	h := &BrowserWSHandler{mediaPortFallback: &mediaPortFallbackState{
		configured: 50000,
		bound:      50001,
		lastProbed: 50001,
	}}
	wc := &browserWSConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}

	h.notifyMediaPortDegraded(wc, "sess-1", "viewer-1")

	var raw []byte
	select {
	case raw = <-wc.sendCh:
	default:
		t.Fatal("no frame was sent — the viewer would sit in front of a panel that can never show video " +
			"remotely, with nothing on screen saying why (the exact ADR-061 failure this fixes)")
	}

	var frame generated.BrowserStatusFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	assert.Equal(t, string(generated.WsFrameTypeBrowserStatus), frame.Type)
	assert.Equal(t, "error", frame.State,
		"it must arrive on the surface the SPA already renders as a visible error, not an informational state "+
			"it drops on the floor")
	require.NotNil(t, frame.SessionId)
	assert.Equal(t, "sess-1", *frame.SessionId)
	require.NotNil(t, frame.Message)
	assert.Contains(t, *frame.Message, "50000", "the panel copy must name the configured port")
	assert.Contains(t, *frame.Message, "50001", "the panel copy must name the port actually bound")
}

// TestNotifyMediaPortDegraded_SilentWhenHealthy — the ordinary install must
// see no frame at all, so this can never become a banner people learn to
// dismiss.
func TestNotifyMediaPortDegraded_SilentWhenHealthy(t *testing.T) {
	h := &BrowserWSHandler{}
	wc := &browserWSConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}

	h.notifyMediaPortDegraded(wc, "sess-1", "viewer-1")

	select {
	case raw := <-wc.sendCh:
		t.Fatalf("a healthy install must send nothing, got %s", raw)
	default:
	}
}

// TestMediaPortFallbackNotice_TotalFailureNamesEphemeralConsequence covers the
// worse branch: not even the probe range could be bound, so every Session is
// on an ephemeral port and a hosted install has no chance whatsoever.
func TestMediaPortFallbackNotice_TotalFailureNamesEphemeralConsequence(t *testing.T) {
	notice := mediaPortFallbackState{configured: 50000, bound: 0, lastProbed: 50016}.notice()

	require.NotEmpty(t, notice, "the total-failure branch is the WORST case and must not be the silent one")
	assert.Contains(t, notice, "50000")
	assert.Contains(t, notice, "50016", "the notice must say how far the probe walked before giving up")
	assert.Contains(t, strings.ToLower(notice), "random udp port",
		"the user must be told video is on an unpredictable port, not merely a different one")
}

// ---------------------------------------------------------------------------
// The notice survives the wire
// ---------------------------------------------------------------------------

// TestMediaPortFallbackNotice_FitsContractMaxLength is the trap this fix could
// most easily fall into: BrowserStatusFrame.message is maxLength 512, and the
// SPA's zod edge validation DROPS a non-conforming payload. An over-length
// notice would restore the exact silence F6 exists to remove — visibly fixed
// in Go, invisible in the product. The limit is read from the contract rather
// than hardcoded so it tracks the schema instead of drifting from it.
func TestMediaPortFallbackNotice_FitsContractMaxLength(t *testing.T) {
	limit := browserStatusMessageMaxLength(t)

	cases := map[string]mediaPortFallbackState{
		"fallback port":  {configured: 65534, bound: 65535, lastProbed: 65535},
		"no port at all": {configured: 65535, bound: 0, lastProbed: 65535},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			notice := state.notice()
			assert.LessOrEqual(t, len(notice), limit,
				"an over-length message is dropped by the SPA's zod edge validation, which would make this "+
					"degradation invisible again — the whole point of the fix")
		})
	}
}

// browserStatusMessageMaxLength reads the `message` maxLength straight out of
// the wire contract (Constraint #8's single source of truth) so this test
// cannot pass against a stale hardcoded number.
func browserStatusMessageMaxLength(t *testing.T) int {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "components", "schemas", "BrowserStatusFrame.yaml")
	raw, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): fixed, repo-relative contract path
	require.NoError(t, err, "the contract must be readable — it is the authority on this limit")

	re := regexp.MustCompile(`(?m)^  message:\n    type: string\n    maxLength: (\d+)`)
	m := re.FindSubmatch(raw)
	require.NotNil(t, m, "BrowserStatusFrame.message must still declare a maxLength for this test to mean anything")
	limit, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	return limit
}

// TestMediaPortFallbackNotice_StaysOutOfTheSPATranslator guards a cross-
// boundary constraint that no compiler checks: src/lib/browserLiveWs.ts's
// translateBrowserErrorMessage rewrites recognised Go-internal error strings
// into plain language and passes everything else through verbatim. This copy
// is already plain language written FOR the operator, so a stray "blocked" or
// "could not resolve" in it would get the whole sentence replaced client-side
// by something about a blocked address — losing the port numbers and the
// actual instruction.
func TestMediaPortFallbackNotice_StaysOutOfTheSPATranslator(t *testing.T) {
	// The literal trigger substrings from translateBrowserErrorMessage's
	// patterns that plausible copy here could collide with.
	triggers := []string{
		"blocked", "ssrf", "could not resolve", "dns resolution failed",
		"no addresses found", "too many redirects", "cannot extract host",
		"invalid character", "browser_attach:", "browser_control:",
		"browser_tab_action:", "frame schema validation failed",
		"unknown frame type", "invalid frame: not json",
	}
	notices := []string{
		mediaPortFallbackState{configured: 50000, bound: 50001, lastProbed: 50001}.notice(),
		mediaPortFallbackState{configured: 50000, bound: 0, lastProbed: 50016}.notice(),
	}
	for _, notice := range notices {
		lower := strings.ToLower(notice)
		for _, trigger := range triggers {
			assert.NotContains(t, lower, trigger,
				"this phrase makes the SPA translator swallow the whole notice and show unrelated copy instead")
		}
	}
}

// ---------------------------------------------------------------------------
// The notice is wired into the real offer path, not just reachable in theory
// ---------------------------------------------------------------------------

// TestHandleWebRTCOffer_MediaPortFallback_TellsTheViewerInThePanel is the
// call-site half. The helper above proves the frame is BUILT correctly; this
// proves handleWebRTCOffer actually CALLS it on the success path — the single
// line whose absence would restore the silent degradation with every other
// test still green.
//
// BDD: Given the shared media socket fell back off the operator's declared
// UDP port,
// When a viewer's WebRTC offer succeeds and it is handed an answer,
// Then that same viewer also receives a browser_status(error) naming both
// ports — so a panel that will never reach a remote viewer says why, instead
// of looking perfectly healthy.
//
// Linux-gated for the same pre-existing reason as the other full-offer-path
// tests in this package (browser_webrtc_fixwave_test.go's ingest-timeout
// pair): BrowserManager.VideoCapability only ever reports Capable=true on
// linux, so webrtcUnavailableReason short-circuits the whole handler
// elsewhere. That gate is not this fix's doing, and the notice itself is
// platform-independent — every other test in this file runs on every OS.
func TestHandleWebRTCOffer_MediaPortFallback_TellsTheViewerInThePanel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	handler, al := newBrowserWSTestHandler(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	// The socket already fell back before this viewer ever opened the panel —
	// sharedMediaConn binds once per process, so every later viewer inherits
	// the degradation and must be told about it too.
	handler.mediaConnMu.Lock()
	handler.mediaPortFallback = &mediaPortFallbackState{configured: 50000, bound: 50003, lastProbed: 50003}
	handler.mediaConnMu.Unlock()

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	var encoderCalls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, defaultAgent.ID, &fakeRelay{},
		fakeEncoderStarter(&encoderCalls, nil), nil)
	require.NoError(t, err)
	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	t.Cleanup(cs.Stop)

	wc := newTestBrowserWSConn()
	var state browserConnState
	data, err := json.Marshal(generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-media-port",
	})
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-media-port", "user-1", data, al.GetConfig(), 0)
	t.Cleanup(func() { handler.detachWebRTCViewer(&state, "viewer-media-port") })

	var status *generated.BrowserStatusFrame
	for range 4 {
		select {
		case raw := <-wc.sendCh:
			var probe struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(raw, &probe))
			if probe.Type != string(generated.WsFrameTypeBrowserStatus) {
				continue
			}
			var frame generated.BrowserStatusFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			status = &frame
		default:
		}
		if status != nil {
			break
		}
	}
	require.NotNil(t, status,
		"a successful offer on a fallen-back media port sent NO status frame — the viewer is left watching a "+
			"panel that can never show video remotely, with nothing saying why (round-2 F6)")
	assert.Equal(t, "error", status.State)
	require.NotNil(t, status.Message)
	assert.Contains(t, *status.Message, "50000", "the panel must name the port the operator configured")
	assert.Contains(t, *status.Message, "50003", "the panel must name the port live video actually uses")
}

func TestSharedMediaTCP_ConfiguredPortBinds(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	probeAddr, probeAddrOk := probe.Addr().(*net.TCPAddr)
	require.True(t, probeAddrOk, "listener address must be a *net.TCPAddr")
	free := probeAddr.Port
	require.NoError(t, probe.Close())

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaTCPPort = free
	cfg.Tools.Browser.WebRTCMediaUDPBindAddress = "127.0.0.1"

	h := &BrowserWSHandler{}
	ln := h.sharedMediaTCP(cfg)
	require.NotNil(t, ln)
	t.Cleanup(func() { _ = ln.Close() })
	lnAddr, lnAddrOk := ln.Addr().(*net.TCPAddr)
	require.True(t, lnAddrOk, "listener address must be a *net.TCPAddr")
	got := lnAddr.Port
	require.Equal(t, free, got)
	require.Same(t, ln, h.sharedMediaTCP(cfg), "ICE-TCP listener must be bound once and reused")
}

func TestSharedMediaTCP_Unconfigured_ReturnsNil(t *testing.T) {
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedMediaTCP(&config.Config{}))
}

// TestSharedMediaTCP_BindFailure_IsUserVisible pins ADR-061's rule for the
// tier-2 socket: a configured ICE-TCP port that cannot be bound must reach the
// PANEL, not only the log. Without this an operator who declared a TCP media
// port and silently got nothing has no way to discover it.
func TestSharedMediaTCP_BindFailure_IsUserVisible(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = blocker.Close() }()
	blockerAddr, blockerAddrOk := blocker.Addr().(*net.TCPAddr)
	require.True(t, blockerAddrOk, "listener address must be a *net.TCPAddr")
	taken := blockerAddr.Port

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaTCPPort = taken

	h := &BrowserWSHandler{}
	// Bind the same port on all interfaces first so the handler's own listen
	// collides deterministically.
	hog, err := net.Listen("tcp", ":"+strconv.Itoa(taken))
	if err == nil {
		defer func() { _ = hog.Close() }()
	}

	if ln := h.sharedMediaTCP(cfg); ln != nil {
		_ = ln.Close()
		t.Skip("port could not be made unavailable on this machine; nothing to assert")
	}
	notice := h.iceTCPUnavailableNotice()
	require.NotEmpty(t, notice, "a failed ICE-TCP bind must produce an operator-facing notice")
	require.Contains(t, notice, "webrtc_media_tcp_port")
	require.LessOrEqual(t, len(notice), 512, "BrowserStatusFrame.message is capped at 512")
}

// TestSharedMediaTCP_Unconfigured_SaysNothing keeps the notice from becoming
// noise on the default install, where ICE-TCP is off.
func TestSharedMediaTCP_Unconfigured_SaysNothing(t *testing.T) {
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedMediaTCP(&config.Config{}))
	require.Empty(t, h.iceTCPUnavailableNotice())
}

// TestTURNUnavailableNotice_SilentWhenOff keeps the tier-3 notice from
// becoming noise on the default install, where TURN is not configured.
func TestTURNUnavailableNotice_SilentWhenOff(t *testing.T) {
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedTURN(&config.Config{}))
	require.Empty(t, h.turnUnavailableNotice())
}

// TestTURNUnavailableNotice_ReportsAConfiguredButFailedRelay is the ADR-061
// discipline applied to tier 3: an operator who declared a relay port and
// silently got nothing has no surface to find it on.
func TestTURNUnavailableNotice_ReportsAConfiguredButFailedRelay(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCTurnUDPPort = 30000
	// No public address configured, so StartTURN refuses: a relay that
	// advertises a private address is useless to every remote viewer.
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedTURN(cfg))
	notice := h.turnUnavailableNotice()
	require.NotEmpty(t, notice)
	require.Contains(t, notice, "webrtc_turn_udp_port")
	require.LessOrEqual(t, len(notice), 512)
}

// Command wv1spike is a THROWAWAY spike (WV1 / Spike Q1) to answer one
// question: can a Pion (pure-Go WebRTC) <-> browser PeerConnection establish
// over this Fly.io devpod's network WITHOUT a TURN server?
//
// This is not product code. It is not wired into the Omnipus module. See
// /home/dev/omnipus3/docs/internal/design/live-browser-webrtc-context.md for
// the full background.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

// ---------------------------------------------------------------------
// Server-side log buffer, exposed at GET /serverlog so the browser page can
// render both perspectives (browser console + server) side by side.
// ---------------------------------------------------------------------

type logBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (lb *logBuffer) Add(format string, args ...interface{}) {
	ts := time.Now().Format("15:04:05.000")
	line := fmt.Sprintf("[%s] %s", ts, fmt.Sprintf(format, args...))
	lb.mu.Lock()
	lb.lines = append(lb.lines, line)
	lb.mu.Unlock()
	log.Println(line)
}

func (lb *logBuffer) Text() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := ""
	for _, l := range lb.lines {
		out += l + "\n"
	}
	return out
}

var serverLog = &logBuffer{}

// connID is a simple monotonically increasing counter used to tag log lines
// per PeerConnection so concurrent connect attempts don't interleave
// unreadably.
var connCounter int64
var connCounterMu sync.Mutex

func nextConnID() int64 {
	connCounterMu.Lock()
	defer connCounterMu.Unlock()
	connCounter++
	return connCounter
}

// iceTCPMux is the shared passive ICE-TCP listener on :8081, used only by
// the "icetcp" mode. Created once at startup.
var iceTCPMux ice.TCPMux

// ---------------------------------------------------------------------
// Wire types for the plain-HTTP signaling exchange (non-trickle: client and
// server each wait for ICE gathering to complete before sending the SDP).
// ---------------------------------------------------------------------

type offerRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
	Mode string `json:"mode"`
}

type answerResponse struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// buildPeerConnection constructs a *webrtc.PeerConnection configured
// according to the requested spike mode:
//
//   - "default":  public STUN (stun.l.google.com:19302), normal UDP ICE.
//   - "hostonly": no STUN configured at all -> host candidates only.
//   - "icetcp":   STUN + passive ICE-TCP on :8081 additionally offered
//     (NewICETCPMux/SetICETCPMux + NetworkTypeTCP4), so the answer SDP
//     carries tcp-type host candidates alongside the udp ones.
func buildPeerConnection(mode string) (*webrtc.PeerConnection, error) {
	se := webrtc.SettingEngine{}
	config := webrtc.Configuration{}

	switch mode {
	case "default":
		config.ICEServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
	case "hostonly":
		// Intentionally no ICEServers: only host candidates will be
		// gathered on either side.
	case "icetcp":
		config.ICEServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
		if iceTCPMux == nil {
			return nil, fmt.Errorf("icetcp mode requested but ICE-TCP mux was not initialized at startup")
		}
		se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4})
		se.SetICETCPMux(iceTCPMux)
	default:
		return nil, fmt.Errorf("unknown mode %q (want default|hostonly|icetcp)", mode)
	}

	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("api.NewPeerConnection: %w", err)
	}
	return pc, nil
}

// logSelectedPair walks pc.SCTP().Transport().ICETransport() to find the
// candidate pair ICE actually nominated, and logs both sides (type +
// protocol + address) -- the single most important line of evidence this
// spike produces.
func logSelectedPair(pc *webrtc.PeerConnection, prefix string) {
	// Give ICE a brief moment to settle the nomination before reading it.
	time.Sleep(300 * time.Millisecond)

	sctp := pc.SCTP()
	if sctp == nil {
		serverLog.Add("%s selected-pair lookup: SCTP transport is nil", prefix)
		return
	}
	dtls := sctp.Transport()
	if dtls == nil {
		serverLog.Add("%s selected-pair lookup: DTLS transport is nil", prefix)
		return
	}
	iceTransport := dtls.ICETransport()
	if iceTransport == nil {
		serverLog.Add("%s selected-pair lookup: ICE transport is nil", prefix)
		return
	}
	pair, err := iceTransport.GetSelectedCandidatePair()
	if err != nil {
		serverLog.Add("%s selected-pair lookup failed: %v", prefix, err)
		return
	}
	if pair == nil || pair.Local == nil || pair.Remote == nil {
		serverLog.Add("%s selected-pair lookup: no pair available yet", prefix)
		return
	}
	serverLog.Add(
		"%s *** SELECTED CANDIDATE PAIR *** local={type=%s proto=%s addr=%s:%d} remote={type=%s proto=%s addr=%s:%d}",
		prefix,
		pair.Local.Typ.String(), pair.Local.Protocol.String(), pair.Local.Address, pair.Local.Port,
		pair.Remote.Typ.String(), pair.Remote.Protocol.String(), pair.Remote.Address, pair.Remote.Port,
	)
}

func offerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed, want POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req offerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.SDP == "" {
		http.Error(w, "bad request: missing sdp", http.StatusBadRequest)
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "default"
	}

	id := nextConnID()
	prefix := fmt.Sprintf("[conn-%d mode=%s]", id, mode)
	serverLog.Add("%s offer received (%d bytes SDP)", prefix, len(req.SDP))

	pc, err := buildPeerConnection(mode)
	if err != nil {
		serverLog.Add("%s buildPeerConnection failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("peer connection setup failed: %v", err), http.StatusBadRequest)
		return
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			serverLog.Add("%s server-side candidate gathering COMPLETE", prefix)
			return
		}
		serverLog.Add(
			"%s server-side local candidate: type=%s proto=%s addr=%s port=%d related=%s:%d",
			prefix, c.Typ.String(), c.Protocol.String(), c.Address, c.Port, c.RelatedAddress, c.RelatedPort,
		)
	})

	pc.OnICEGatheringStateChange(func(s webrtc.ICEGatheringState) {
		serverLog.Add("%s ICE gathering state -> %s", prefix, s.String())
	})

	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		serverLog.Add("%s ICE connection state -> %s", prefix, s.String())
		if s == webrtc.ICEConnectionStateConnected || s == webrtc.ICEConnectionStateCompleted {
			go logSelectedPair(pc, prefix)
		}
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		serverLog.Add("%s peer connection state -> %s", prefix, s.String())
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		idStr := "?"
		if dcID := dc.ID(); dcID != nil {
			idStr = strconv.Itoa(int(*dcID))
		}
		serverLog.Add("%s data channel %q opened (id=%s)", prefix, dc.Label(), idStr)

		dc.OnOpen(func() {
			serverLog.Add("%s data channel %q ready (OnOpen)", prefix, dc.Label())
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			// Echo every message straight back to the browser, preserving
			// the original message type. The browser's dc.send(string)
			// produces a text-typed frame; if we echo via dc.Send([]byte)
			// (binary-typed) the browser receives a Blob instead of a
			// string and any string-keyed RTT correlation on the client
			// silently breaks.
			var err error
			if msg.IsString {
				err = dc.SendText(string(msg.Data))
			} else {
				err = dc.Send(msg.Data)
			}
			if err != nil {
				serverLog.Add("%s echo send failed: %v", prefix, err)
			}
		})
		dc.OnClose(func() {
			serverLog.Add("%s data channel %q closed", prefix, dc.Label())
		})
		dc.OnError(func(err error) {
			serverLog.Add("%s data channel %q error: %v", prefix, dc.Label(), err)
		})
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP}
	if err := pc.SetRemoteDescription(offer); err != nil {
		serverLog.Add("%s SetRemoteDescription failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("set remote description failed: %v", err), http.StatusBadRequest)
		return
	}

	// Non-trickle signaling: wait for the server's own ICE gathering to
	// finish before answering, so the answer SDP carries every candidate.
	gatherComplete := webrtc.GatheringCompletePromise(pc)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		serverLog.Add("%s CreateAnswer failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("create answer failed: %v", err), http.StatusInternalServerError)
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		serverLog.Add("%s SetLocalDescription failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("set local description failed: %v", err), http.StatusInternalServerError)
		return
	}

	select {
	case <-gatherComplete:
		serverLog.Add("%s server gathering complete, sending answer", prefix)
	case <-time.After(10 * time.Second):
		serverLog.Add("%s WARNING: server gathering did not complete within 10s, sending partial answer", prefix)
	}

	local := pc.LocalDescription()
	if local == nil {
		serverLog.Add("%s BUG: LocalDescription is nil after SetLocalDescription", prefix)
		http.Error(w, "internal error: no local description", http.StatusInternalServerError)
		return
	}

	resp := answerResponse{SDP: local.SDP, Type: local.Type.String()}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		serverLog.Add("%s failed to encode/send answer: %v", prefix, err)
		return
	}
	serverLog.Add("%s answer sent to browser", prefix)
}

func serverLogHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(serverLog.Text())); err != nil {
		log.Printf("serverlog write failed: %v", err)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(pageHTML); err != nil {
		log.Printf("index write failed: %v", err)
	}
}

func main() {
	// Set up the shared passive ICE-TCP mux on :8081 once at startup. This
	// is only consumed by "icetcp" mode requests, but binding it up front
	// keeps buildPeerConnection() simple and fails fast if 8081 is unusable.
	tcpListener, err := net.Listen("tcp", "0.0.0.0:8081")
	if err != nil {
		log.Fatalf("failed to bind ICE-TCP listener on :8081: %v", err)
	}
	loggerFactory := logging.NewDefaultLoggerFactory()
	iceTCPMux = webrtc.NewICETCPMux(loggerFactory.NewLogger("ice-tcp-mux"), tcpListener, 8192)
	log.Println("ICE-TCP passive mux listening on 0.0.0.0:8081 (used only by icetcp mode)")

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/offer", offerHandler)
	mux.HandleFunc("/serverlog", serverLogHandler)

	srv := &http.Server{
		Addr:              "0.0.0.0:8080",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverLog.Add("wv1spike starting: HTTP signaling on 0.0.0.0:8080, ICE-TCP mux on 0.0.0.0:8081")
	log.Println("listening on 0.0.0.0:8080")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

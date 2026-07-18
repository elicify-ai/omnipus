package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	_ "embed"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

// q1compat carries over the FULL Q1 spike server (data-channel echo,
// three ICE modes, ICE-TCP mux on :8081) unchanged in behavior so the
// operator's still-pending Q1 DEFAULT-mode external traversal test keeps
// working at /q1 on this superseding server. Logic is copied from
// q1-connectivity/main.go, scoped under /q1/*.

//go:embed pages/q1.html
var q1PageHTML []byte

var q1ICETCPMux ice.TCPMux

type q1OfferRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
	Mode string `json:"mode"`
}

// initQ1ICETCPMux binds the passive ICE-TCP listener on :8081, same as
// q1-connectivity/main.go. Only consumed by "icetcp" mode requests.
func initQ1ICETCPMux() error {
	tcpListener, err := net.Listen("tcp", "0.0.0.0:8081")
	if err != nil {
		return fmt.Errorf("bind ICE-TCP listener on :8081: %w", err)
	}
	loggerFactory := logging.NewDefaultLoggerFactory()
	q1ICETCPMux = webrtc.NewICETCPMux(loggerFactory.NewLogger("ice-tcp-mux"), tcpListener, 8192)
	return nil
}

func q1BuildPeerConnection(mode string) (*webrtc.PeerConnection, error) {
	se := webrtc.SettingEngine{}
	config := webrtc.Configuration{}

	switch mode {
	case "default":
		config.ICEServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
	case "hostonly":
		// no ICEServers -> host candidates only
	case "icetcp":
		config.ICEServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
		if q1ICETCPMux == nil {
			return nil, fmt.Errorf("icetcp mode requested but ICE-TCP mux was not initialized at startup")
		}
		se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4})
		se.SetICETCPMux(q1ICETCPMux)
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

func q1LogSelectedPair(pc *webrtc.PeerConnection, prefix string) {
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

func q1OfferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed, want POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req q1OfferRequest
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
	prefix := fmt.Sprintf("[q1-conn-%d mode=%s]", id, mode)
	serverLog.Add("%s offer received (%d bytes SDP)", prefix, len(req.SDP))

	pc, err := q1BuildPeerConnection(mode)
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
			go q1LogSelectedPair(pc, prefix)
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

	resp := sdpResponse{SDP: local.SDP, Type: local.Type.String()}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		serverLog.Add("%s failed to encode/send answer: %v", prefix, err)
		return
	}
	serverLog.Add("%s answer sent to browser", prefix)
}

func q1IndexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(q1PageHTML); err != nil {
		serverLog.Add("q1 index write failed: %v", err)
	}
}

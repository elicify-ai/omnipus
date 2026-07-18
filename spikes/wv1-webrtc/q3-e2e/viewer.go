package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// viewerOfferHandler is the signaling endpoint for the VIEWER page. The
// browser creates a recvonly PeerConnection (addTransceiver('video'|'audio',
// {direction:'recvonly'}) for both kinds) and POSTs its offer here. The
// server waits for the relay's two local tracks to exist (the encoder may
// not have connected yet), attaches them as this PeerConnection's outgoing
// tracks, and answers.
func (rl *relay) viewerOfferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed, want POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req sdpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.SDP == "" {
		http.Error(w, "bad request: missing sdp", http.StatusBadRequest)
		return
	}

	id := nextConnID()
	prefix := fmt.Sprintf("[viewer-%d]", id)
	serverLog.Add("%s offer received (%d bytes SDP)", prefix, len(req.SDP))

	videoTrack, audioTrack, ok := rl.waitForTracks(5 * time.Second)
	if !ok {
		serverLog.Add("%s no encoder connected yet (waited 5s), rejecting", prefix)
		http.Error(w, "no encoder connected yet, retry shortly", http.StatusServiceUnavailable)
		return
	}

	pc, err := rl.buildPeerConnection()
	if err != nil {
		serverLog.Add("%s buildPeerConnection failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("peer connection setup failed: %v", err), http.StatusInternalServerError)
		return
	}

	if sender, err := pc.AddTrack(videoTrack); err != nil {
		serverLog.Add("%s AddTrack(video) failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("add video track failed: %v", err), http.StatusInternalServerError)
		return
	} else {
		go drainRTCP(sender)
	}
	if sender, err := pc.AddTrack(audioTrack); err != nil {
		serverLog.Add("%s AddTrack(audio) failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("add audio track failed: %v", err), http.StatusInternalServerError)
		return
	} else {
		go drainRTCP(sender)
	}

	var closeOnce sync.Once
	rl.viewerCount.Add(1)
	serverLog.Add("%s viewer count now %d", prefix, rl.viewerCount.Load())

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			serverLog.Add("%s server-side candidate gathering COMPLETE", prefix)
			return
		}
		serverLog.Add("%s server-side local candidate: type=%s proto=%s addr=%s port=%d",
			prefix, c.Typ.String(), c.Protocol.String(), c.Address, c.Port)
	})
	pc.OnICEGatheringStateChange(func(s webrtc.ICEGatheringState) {
		serverLog.Add("%s ICE gathering state -> %s", prefix, s.String())
	})
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		serverLog.Add("%s ICE connection state -> %s", prefix, s.String())
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		serverLog.Add("%s peer connection state -> %s", prefix, s.String())
		if s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateDisconnected {
			closeOnce.Do(func() {
				n := rl.viewerCount.Add(-1)
				serverLog.Add("%s viewer disconnected, count now %d", prefix, n)
			})
		}
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
	serverLog.Add("%s answer sent to viewer", prefix)

	rl.pliBurstForNewViewer(prefix)
}

// drainRTCP reads and discards RTCP on an outgoing RTPSender so its buffer
// never blocks -- standard Pion requirement for any sender you don't
// otherwise inspect.
func drainRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

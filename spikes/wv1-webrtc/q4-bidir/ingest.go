package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
)

// sdpRequest/sdpResponse are the plain-HTTP, non-trickle signaling wire
// shapes shared by /ingest and /viewer-offer (and /q1/offer) -- same
// pattern as q1-connectivity/main.go.
type sdpRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type sdpResponse struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// ingestHandler is the signaling endpoint for the ENCODER: the headless
// Chrome page holding the captured-tab MediaStream POSTs its offer here.
// Pion answers, receives the video+audio tracks via OnTrack, and the relay
// starts forwarding RTP to any connected/future viewers.
func (rl *relay) ingestHandler(w http.ResponseWriter, r *http.Request) {
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
	prefix := fmt.Sprintf("[ingest-%d]", id)
	serverLog.Add("%s offer received (%d bytes SDP)", prefix, len(req.SDP))

	pc, err := rl.buildPeerConnection()
	if err != nil {
		serverLog.Add("%s buildPeerConnection failed: %v", prefix, err)
		http.Error(w, fmt.Sprintf("peer connection setup failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Only one active encoder is modeled for this spike. If a previous
	// ingest connection is still around (e.g. the encoder's watchdog
	// reconnected), close it so it stops holding stale state/goroutines.
	rl.mu.Lock()
	old := rl.ingestPC
	rl.ingestPC = pc
	rl.mu.Unlock()
	if old != nil {
		serverLog.Add("%s replacing previous ingest connection", prefix)
		go func() {
			if cerr := old.Close(); cerr != nil {
				serverLog.Add("%s closing previous ingest connection: %v", prefix, cerr)
			}
		}()
	}

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
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		go rl.attachIngestTrack(prefix, track, receiver)
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
	serverLog.Add("%s answer sent to encoder", prefix)
}

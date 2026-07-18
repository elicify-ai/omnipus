// Command wv1spike4 is a THROWAWAY spike (WV1 / Spike Q4) proving
// BIDIRECTIONAL operation: responsive input (mouse/keyboard over a WebRTC
// data channel) driving the captured tab WHILE ~30fps video + audio stream
// out, all on ONE PeerConnection per peer. It extends Q3's working e2e
// pipeline (video+audio only) with:
//   - an "input" data channel on the viewer's PeerConnection (inputdc.go)
//   - a localhost-only WS bridge (/inputbridge, bridge.go) to run.js's CDP
//     pipe, which dispatches Input.dispatchMouseEvent/dispatchKeyEvent on
//     the captured tab and answers Runtime.evaluate for verification
//   - /debug/evaluate, a verification-only HTTP endpoint over that bridge
//
// The point: CDP now carries ONLY input (video never touches it -- it's
// tabCapture+WebRTC), so input dispatch can never contend with the video
// ack loop on a shared CDP command queue. That contention was the exact
// regression that killed the old WebCodecs-over-CDP-screencast design
// (5s+ input timeouts under load) -- see wv1-spike-results.md and
// live-browser-webrtc-context.md §1.
//
// Not product code, not wired into the Omnipus module.
package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	if err := initQ1ICETCPMux(); err != nil {
		log.Fatalf("%v", err)
	}
	log.Println("ICE-TCP passive mux listening on 0.0.0.0:8081 (used only by /q1 icetcp mode)")

	rl, err := newRelay()
	if err != nil {
		log.Fatalf("newRelay: %v", err)
	}

	mux := http.NewServeMux()

	// Q1 compatibility surface (data-channel connectivity spike).
	mux.HandleFunc("/q1", q1IndexHandler)
	mux.HandleFunc("/q1/offer", q1OfferHandler)

	// Q3 end-to-end surfaces.
	mux.HandleFunc("/ingest", rl.ingestHandler)
	mux.HandleFunc("/viewer-offer", rl.viewerOfferHandler)
	mux.HandleFunc("/view", pageHandler("pages/view.html"))
	mux.HandleFunc("/metronome", pageHandler("pages/metronome.html"))
	mux.HandleFunc("/", rootHandler)
	mux.HandleFunc("/serverlog", serverLogHandler)

	// Q4 bidirectional-input surfaces.
	mux.HandleFunc("/inputbridge", rl.bridge.handler)
	mux.HandleFunc("/debug/evaluate", debugEvaluateHandler(rl.bridge))
	mux.HandleFunc("/debug/bridge-status", debugBridgeStatusHandler(rl.bridge))

	srv := &http.Server{
		Addr:              "0.0.0.0:8080",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverLog.Add("wv1spike4 starting: HTTP on 0.0.0.0:8080 (/, /q1, /view, /metronome, /ingest, /viewer-offer, /serverlog, /inputbridge, /debug/evaluate, /debug/bridge-status), ICE-TCP mux on 0.0.0.0:8081")
	log.Println("listening on 0.0.0.0:8080")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

func serverLogHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(serverLog.Text())); err != nil {
		log.Printf("serverlog write failed: %v", err)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body := `<!doctype html><html><head><meta charset="utf-8"><title>WV1 Spike Q4</title></head>
<body style="font-family:sans-serif;background:#111;color:#eee;padding:24px">
<h1>WV1 Spike Q4 — bidirectional WebRTC (video+audio out, input in, one PeerConnection)</h1>
<ul>
<li><a href="/view" style="color:#6cf">/view</a> — viewer page (video+audio playback, drive the tab, input stats, stress test, report)</li>
<li><a href="/metronome" style="color:#6cf">/metronome</a> — the capture+drive target (beep+flash sync proof + interactive canvas/text/wheel)</li>
<li><a href="/q1" style="color:#6cf">/q1</a> — Q1 connectivity spike (data-channel echo, 3 ICE modes)</li>
<li><a href="/serverlog" style="color:#6cf">/serverlog</a> — raw server log</li>
<li><a href="/debug/bridge-status" style="color:#6cf">/debug/bridge-status</a> — is run.js's CDP input bridge connected?</li>
</ul>
</body></html>`
	if _, err := w.Write([]byte(body)); err != nil {
		log.Printf("root write failed: %v", err)
	}
}

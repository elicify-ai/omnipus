// Command wv1spike3 is a THROWAWAY spike (WV1 / Spike Q3) proving the FULL
// target pipeline end to end: headless-Chrome tab capture (video+audio
// MediaStream) -> WebRTC -> Pion relay (this server) -> a viewer browser
// page that plays both tracks back with live stats.
//
// This is a superset of the Q1 connectivity spike (kept reachable at /q1)
// plus new ingest/viewer/metronome surfaces. Not product code, not wired
// into the Omnipus module -- see
// /home/dev/omnipus3/docs/internal/design/live-browser-webrtc-context.md.
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

	srv := &http.Server{
		Addr:              "0.0.0.0:8080",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverLog.Add("wv1spike3 starting: HTTP on 0.0.0.0:8080 (/, /q1, /view, /metronome, /ingest, /viewer-offer, /serverlog), ICE-TCP mux on 0.0.0.0:8081")
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
	body := `<!doctype html><html><head><meta charset="utf-8"><title>WV1 Spike Q3</title></head>
<body style="font-family:sans-serif;background:#111;color:#eee;padding:24px">
<h1>WV1 Spike Q3 — E2E WebRTC A/V pipeline</h1>
<ul>
<li><a href="/view" style="color:#6cf">/view</a> — viewer page (video+audio playback, stats, report)</li>
<li><a href="/metronome" style="color:#6cf">/metronome</a> — the capture target (beep+flash sync proof)</li>
<li><a href="/q1" style="color:#6cf">/q1</a> — Q1 connectivity spike (data-channel echo, 3 ICE modes)</li>
<li><a href="/serverlog" style="color:#6cf">/serverlog</a> — raw server log</li>
</ul>
</body></html>`
	if _, err := w.Write([]byte(body)); err != nil {
		log.Printf("root write failed: %v", err)
	}
}

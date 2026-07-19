package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// debug.go: verification-only HTTP endpoints layered on top of the input
// bridge. Not part of the "real" viewer<->relay<->encoder flow -- these
// exist so the spike's own verification harness (and the operator, if they
// want a raw look) can pull ground truth (window.__events on the captured
// tab) without needing a full browser. Localhost-guarded for the same
// reason /inputbridge is: port 8080 is reachable from the public internet
// via the Fly proxy, and Runtime.evaluate access must never be exposed
// there, even in throwaway spike code.

type debugEvaluateRequest struct {
	Expression string `json:"expression"`
}

func debugEvaluateHandler(bridge *inputBridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhostRequest(r) {
			http.Error(w, "forbidden: /debug/evaluate is localhost-only", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed, want POST", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		var req debugEvaluateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("bad request body: %v", err), http.StatusBadRequest)
			return
		}
		if req.Expression == "" {
			http.Error(w, "bad request: missing expression", http.StatusBadRequest)
			return
		}

		result, err := bridge.evaluate(req.Expression, 5*time.Second)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			if encErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encErr != nil {
				serverLog.Add("/debug/evaluate: failed to encode error response: %v", encErr)
			}
			return
		}
		if _, werr := w.Write(result); werr != nil {
			serverLog.Add("/debug/evaluate: failed to write result: %v", werr)
		}
	}
}

func debugBridgeStatusHandler(bridge *inputBridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhostRequest(r) {
			http.Error(w, "forbidden: /debug/bridge-status is localhost-only", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"connected": bridge.isConnected()}); err != nil {
			serverLog.Add("/debug/bridge-status: failed to encode: %v", err)
		}
	}
}

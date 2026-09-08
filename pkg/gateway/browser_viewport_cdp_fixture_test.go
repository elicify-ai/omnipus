package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// newViewportCDPEndpoint supplies browser-protocol observations, leaving the
// manager, live view, measured refresh and capture frame machinery real.
func newViewportCDPEndpoint(t *testing.T, pending bool) (string, func(int, int, float64)) {
	t.Helper()
	lifetime, cancel := context.WithCancel(context.Background())
	var observationMu sync.Mutex
	width, height, scale := 800, 600, 1.0
	ready := make(chan struct{})
	var readyOnce sync.Once
	if !pending {
		readyOnce.Do(func() { close(ready) })
	}
	observe := func(w, h int, dpr float64) {
		observationMu.Lock()
		width, height, scale = w, h, dpr
		observationMu.Unlock()
		readyOnce.Do(func() { close(ready) })
	}
	var mu sync.Mutex
	var connections []*websocket.Conn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mu.Lock()
		connections = append(connections, conn)
		mu.Unlock()
		nextSession := 0
		for {
			var command struct {
				ID      int64                      `json:"id"`
				Method  string                     `json:"method"`
				Session string                     `json:"sessionId"`
				Params  map[string]json.RawMessage `json:"params"`
			}
			if err := conn.ReadJSON(&command); err != nil {
				return
			}
			result := map[string]any{}
			switch command.Method {
			case "Target.createTarget":
				result["targetId"] = "verified-target"
			case "Target.createBrowserContext":
				result["browserContextId"] = "fixture-context"
			case "Target.attachToTarget":
				nextSession++
				result["sessionId"] = fmt.Sprintf("fixture-session-%d", nextSession)
			case "Target.getTargets":
				result["targetInfos"] = []any{}
			case "Page.addScriptToEvaluateOnNewDocument":
				result["identifier"] = "fixture-script"
			case "Page.getFrameTree":
				result["frameTree"] = map[string]any{"frame": map[string]any{"id": "fixture-frame", "loaderId": "fixture-loader", "url": "about:blank", "securityOrigin": "://", "mimeType": "text/html"}}
			case "DOM.getDocument":
				result["root"] = map[string]any{"nodeId": 1, "backendNodeId": 1, "nodeType": 9, "nodeName": "#document", "localName": "", "nodeValue": ""}
			case "Page.getLayoutMetrics":
				select {
				case <-ready:
				case <-lifetime.Done():
					return
				}
				observationMu.Lock()
				result["cssLayoutViewport"] = map[string]any{"pageX": 0, "pageY": 0, "clientWidth": width, "clientHeight": height}
				observationMu.Unlock()
			case "Runtime.evaluate":
				var expression string
				_ = json.Unmarshal(command.Params["expression"], &expression)
				value := map[string]any{"type": "undefined"}
				switch expression {
				case "self":
					value = map[string]any{"type": "object", "className": "Window"}
				case "window.devicePixelRatio":
					observationMu.Lock()
					value = map[string]any{"type": "number", "value": scale}
					observationMu.Unlock()
				case "document.title":
					value = map[string]any{"type": "string", "value": "Fixture"}
				case "document.location.href", "window.location.href":
					value = map[string]any{"type": "string", "value": "about:blank"}
				}
				result["result"] = value
			}
			response := map[string]any{"id": command.ID, "result": result}
			if command.Session != "" {
				response["sessionId"] = command.Session
			}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
			if command.Method == "Target.setDiscoverTargets" && command.Session == "" {
				if err := conn.WriteJSON(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": "verified-target", "type": "page", "title": "Fixture", "url": "about:blank", "attached": false}}}); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(func() {
		cancel()
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		server.Close()
	})
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/viewport-fixture", observe
}

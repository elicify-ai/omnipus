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
//
// It reports two discovery signals. The first, `discovered`, closes on the
// endpoint's FIRST Page.getFrameTree — which chromedp's own session handshake
// sends during manager bootstrap, before any document watch exists; it proves
// the endpoint is up, nothing more. The second, `watchQueried`, closes on the
// SECOND Page.getFrameTree of a session: the attach-time live document watch's
// initialize goroutine queries the frame tree immediately AFTER it has looked
// the panel capture up (program order in initialize), so once this query is
// observed, that lookup has provably run. A fixture that registers its capture
// only after `watchQueried` makes the watch's picture-initialization lookup
// deterministically see no capture, instead of racing the registration by
// microseconds.
func newViewportCDPEndpoint(t *testing.T, pending bool, metricsHooks ...func(int, int, float64)) (string, func(int, int, float64), <-chan struct{}, <-chan struct{}) {
	t.Helper()
	lifetime, cancel := context.WithCancel(context.Background())
	var observationMu sync.Mutex
	width, height, scale := 800, 600, 1.0
	ready := make(chan struct{})
	var readyOnce sync.Once
	discovered := make(chan struct{})
	var discoveryOnce sync.Once
	watchQueried := make(chan struct{})
	var watchOnce sync.Once
	frameTreeQueries := make(map[string]int)
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
				// Fresh fixture targets need no attach-time frame query. The first
				// query per session is chromedp's own attach handshake; a SECOND
				// query on the same session is the live document watch's initialize
				// probe, which ran its capture lookup right before sending it.
				discoveryOnce.Do(func() { close(discovered) })
				mu.Lock()
				frameTreeQueries[command.Session]++
				second := frameTreeQueries[command.Session] == 2
				mu.Unlock()
				if second {
					watchOnce.Do(func() { close(watchQueried) })
				}
				result["frameTree"] = map[string]any{"frame": map[string]any{"id": "fixture-frame", "loaderId": "fixture-loader", "url": "about:blank", "securityOrigin": "://", "mimeType": "text/html"}}
			case "DOM.getDocument":
				result["root"] = map[string]any{"nodeId": 1, "backendNodeId": 1, "nodeType": 9, "nodeName": "#document", "localName": "", "nodeValue": ""}
			case "Browser.getWindowForTarget":
				result["windowId"] = 1
				result["bounds"] = map[string]any{"windowState": "normal"}
			case "Browser.setWindowBounds":
				if len(metricsHooks) > 0 {
					var bounds struct {
						Width  int `json:"width"`
						Height int `json:"height"`
					}
					_ = json.Unmarshal(command.Params["bounds"], &bounds)
					observationMu.Lock()
					width, height = bounds.Width, bounds.Height
					observationMu.Unlock()
				}
			case "Emulation.setDeviceMetricsOverride", "Emulation.clearDeviceMetricsOverride":
				if len(metricsHooks) > 0 {
					var w, h int
					dpr := 1.0
					if command.Method == "Emulation.setDeviceMetricsOverride" {
						_ = json.Unmarshal(command.Params["width"], &w)
						_ = json.Unmarshal(command.Params["height"], &h)
						_ = json.Unmarshal(command.Params["deviceScaleFactor"], &dpr)
					}
					observationMu.Lock()
					// CDP zero dimensions disable the size override; they do not resize the page to zero.
					if w > 0 {
						width = w
					}
					if h > 0 {
						height = h
					}
					scale = dpr
					actualW, actualH := width, height
					observationMu.Unlock()
					for _, hook := range metricsHooks {
						hook(actualW, actualH, dpr)
					}
				}
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
				result["result"] = viewportRuntimeEvaluate(expression, func() (int, int, float64) {
					observationMu.Lock()
					defer observationMu.Unlock()
					return width, height, scale
				})
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
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/viewport-fixture", observe, discovered, watchQueried
}

// viewportRuntimeEvaluate answers only the browser JavaScript queries used by
// the viewport lifecycle, keeping the CDP endpoint's main command loop small.
func viewportRuntimeEvaluate(expression string, metrics func() (int, int, float64)) map[string]any {
	switch expression {
	case "self":
		return map[string]any{"type": "object", "className": "Window"}
	case `({width:window.innerWidth,height:window.innerHeight})`:
		width, height, _ := metrics()
		return map[string]any{
			"type":  "object",
			"value": map[string]any{"width": width, "height": height},
		}
	case "window.devicePixelRatio":
		_, _, scale := metrics()
		return map[string]any{"type": "number", "value": scale}
	case "document.title":
		return map[string]any{"type": "string", "value": "Fixture"}
	case "document.location.href", "window.location.href":
		return map[string]any{"type": "string", "value": "about:blank"}
	}
	return map[string]any{"type": "undefined"}
}

// newViewportCDPEndpointURL returns only the WebSocket URL of a measured CDP
// fixture endpoint. The companion discover / watch signals exist for callers
// that need to await specific lifecycle barriers; this caller does not — the
// fixture's t.Cleanup closes them regardless of whether anyone awaits, and
// the endpoint's internal observation logic is independent of the receive.
// The wrapper keeps the call site free of blank identifiers without changing
// the multi-return contract that newViewportCDPEndpoint uses for tests that
// need the full result.
func newViewportCDPEndpointURL(t *testing.T) string {
	t.Helper()
	endpoint, observe, discovered, watchQueried := newViewportCDPEndpoint(t, false)
	_ = observe
	_ = discovered
	_ = watchQueried
	return endpoint
}

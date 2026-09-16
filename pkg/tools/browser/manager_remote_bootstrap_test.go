package browser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gorilla/websocket"
)

// Exercise the real remote allocator and tab bootstrap, faking only Chrome's
// wire responses. A browser must be connected before target creation, and a
// newly created target must be activated before any target attachment.
func TestRemoteBootstrapConnectsBeforeActivatedAttachment(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var command struct {
				ID      int64  `json:"id"`
				Method  string `json:"method"`
				Session string `json:"sessionId"`
			}
			if conn.ReadJSON(&command) != nil {
				return
			}
			mu.Lock()
			methods = append(methods, command.Method)
			mu.Unlock()
			result := map[string]any{}
			switch command.Method {
			case "Target.createTarget":
				result["targetId"] = "remote-page"
			case "Target.attachToTarget":
				result["sessionId"] = "remote-session"
			case "Runtime.evaluate":
				result["result"] = map[string]any{"type": "object", "className": "Window"}
			case "Page.getFrameTree":
				result["frameTree"] = map[string]any{"frame": map[string]any{"id": "remote-frame", "url": "about:blank"}}
			case "DOM.getDocument":
				result["root"] = map[string]any{"nodeId": 1, "nodeType": 9, "nodeName": "#document", "localName": "", "nodeValue": ""}
			}
			response := map[string]any{"id": command.ID, "result": result}
			if command.Session != "" {
				response["sessionId"] = command.Session
			}
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	}))
	defer server.Close()
	alloc, cancelAllocator := chromedp.NewRemoteAllocator(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/devtools/browser/fixture", chromedp.NoModifyURL)
	defer cancelAllocator()
	caller, cancelCaller := context.WithCancel(context.Background())
	mgr := &BrowserManager{}
	ctx, cancel, err := mgr.bootstrapBrowserCtxContext(caller, alloc)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	cancelCaller()
	if ctx.Err() != nil {
		t.Fatal("accepted remote session inherited caller cancellation")
	}
	mu.Lock()
	got := append([]string(nil), methods...)
	mu.Unlock()
	positions := map[string]int{}
	for i, method := range got {
		if _, exists := positions[method]; !exists {
			positions[method] = i
		}
	}
	create, created := positions["Target.createTarget"]
	activate, activated := positions["Target.activateTarget"]
	attach, attached := positions["Target.attachToTarget"]
	if !created || !activated || !attached || create >= activate || activate >= attach {
		t.Fatalf("remote tab was not created, activated, then attached: %v", got)
	}
	if chromedp.FromContext(ctx).Browser == nil || chromedp.FromContext(ctx).Target == nil {
		t.Fatal("remote session was not fully initialized")
	}
}

func TestRemoteBootstrapCancelledDialReturnsWithoutTab(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	alloc, cancelAllocator := chromedp.NewRemoteAllocator(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/devtools/browser/fixture", chromedp.NoModifyURL)
	defer cancelAllocator()
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	done := make(chan error, 1)
	go func() {
		ctx, cleanup, err := (&BrowserManager{}).bootstrapBrowserCtxContext(caller, alloc)
		if cleanup != nil {
			cleanup()
		}
		if ctx != nil && err == nil {
			err = errors.New("cancelled startup returned a tab")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("remote dial did not start")
	}
	cancelCaller()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled remote dial did not return")
	}
}

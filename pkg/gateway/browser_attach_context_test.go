package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/gorilla/websocket"
)

// The real handler reaches a real remote CDP connection; only the browser
// protocol endpoint is held. No Chrome process or internal registry is mocked.
func TestBrowserAttachReplacementCancelsPendingCDP(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	connections := make(chan *websocket.Conn, 1)
	requested := make(chan struct{})
	drained := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(drained)
		connections <- conn
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		close(requested)
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	h, loop := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.CDPURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/attach"
	})
	defaultAgent := loop.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("missing fixture agent")
	}
	mgr, outcome := loop.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	if outcome != agent.BrowserResolveOK {
		t.Fatalf("manager resolution=%v", outcome)
	}
	t.Cleanup(mgr.Shutdown)
	wc := latestTestConn()
	t.Cleanup(wc.close)
	state := &browserConnState{}
	t.Cleanup(func() { state.clearAttachment() })
	epoch := state.beginAttach()
	data, err := json.Marshal(map[string]string{"type": "browser_attach", "agent_id": defaultAgent.ID, "session_id": "cold-attach"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.handleAttach(wc, state, "viewer", "user", data, loop.GetConfig(), epoch) }()
	t.Cleanup(func() { state.clearAttachment(); mgr.Shutdown(); <-done })
	var conn *websocket.Conn
	select {
	case conn = <-connections:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not connect to controlled CDP endpoint")
	}
	t.Cleanup(func() { conn.Close() })
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		conn.Close()
		<-done
		t.Fatal("handler sent no CDP command")
	}
	state.beginAttach()
	prompt := false
	select {
	case <-done:
		prompt = true
	case <-time.After(250 * time.Millisecond):
	}
	conn.Close()
	<-drained
	if !prompt {
		<-done
	}
	if !prompt {
		t.Error("replaced attachment waited for stalled browser response")
	}
	a := state.commandAttachment()
	if a.mgr != nil {
		t.Error("replaced attachment installed an obsolete route")
	}
	for len(wc.sendCh) > 0 {
		frame := <-wc.sendCh
		if wc.canSendFrame(frame) {
			t.Errorf("replaced attachment published a frame: %s", frame.data)
		}
	}
}

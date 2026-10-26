package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// fakeWeComServer answers the subscribe frame: errcode != 0 rejects it, 0
// accepts it, and silent never answers.
func fakeWeComServer(t *testing.T, mode string) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd wecomCommand
			if json.Unmarshal(raw, &cmd) != nil || mode == "silent" {
				continue
			}
			reply := map[string]any{"headers": map[string]string{"req_id": cmd.Headers.ReqID}, "errcode": 0, "errmsg": "ok"}
			if mode == "reject" && cmd.Cmd == wecomCmdSubscribe {
				reply["errcode"], reply["errmsg"] = 40001, "invalid secret"
			}
			if conn.WriteJSON(reply) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func newGateWeComChannel(t *testing.T, wsURL string) *WeComChannel {
	t.Helper()
	ch, err := NewChannel(config.WeComConfig{BotID: "bot-1", SecretRef: "WECOM_GATE", WebSocketURL: wsURL},
		credentials.SecretBundle{"WECOM_GATE": "secret-1"}, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}
	return ch
}

// TestWeComStart_RejectedCredentialsFailTheStart is Train 2 gate r1
// (BDD-8.3 channel survey): a subscribe WeCom answers with a non-zero errcode
// was only logged ("connection lost") and retried forever while the channel
// showed running. Start must fail on it; an accepted subscribe, an
// unreachable server and a server that stays silent past the documented 10 s
// first-handshake limit all keep the channel running (no rejection).
func TestWeComStart_RejectedCredentialsFailTheStart(t *testing.T) {
	rejected := newGateWeComChannel(t, fakeWeComServer(t, "reject"))
	if err := rejected.Start(context.Background()); err == nil {
		_ = rejected.Stop(context.Background())
		t.Fatal("a rejected subscribe must fail Start")
	}
	if rejected.IsRunning() {
		t.Fatal("a rejected channel must not be running")
	}

	controls := map[string]string{
		"accepted":    fakeWeComServer(t, "ok"),
		"unreachable": "ws://127.0.0.1:1/unreachable",
		"silent":      fakeWeComServer(t, "silent"),
	}
	for name, wsURL := range controls {
		ch := newGateWeComChannel(t, wsURL)
		began := time.Now()
		err := ch.Start(context.Background())
		elapsed := time.Since(began)
		if err != nil {
			t.Fatalf("%s: Start must not fail, got %v", name, err)
		}
		if !ch.IsRunning() {
			t.Fatalf("%s: the channel must keep running", name)
		}
		if elapsed > 12*time.Second {
			t.Fatalf("%s: Start must return within the 10 s first-handshake limit, took %s", name, elapsed)
		}
		_ = ch.Stop(context.Background())
	}
}

package weixin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// fakeILinkServer answers getupdates: "http401" with an HTTP 401, "expired"
// with ret=-14 (the bot session is no longer valid), "ok" with an empty
// batch, and "silent" by holding the long poll until the client gives up.
func fakeILinkServer(t *testing.T, mode string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/getupdates" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "http401":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errmsg":"invalid token"}`))
		case "expired":
			_, _ = w.Write([]byte(`{"ret":-14,"errmsg":"session expired"}`))
		case "silent":
			// Drain the body first: the server only notices the client
			// hanging up (Stop cancelling the poll) once the body is read.
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		default:
			_, _ = w.Write([]byte(`{"ret":0,"msgs":[]}`))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/"
}

func newGateWeixinChannel(t *testing.T, baseURL string) *WeixinChannel {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir()) // sync and context-token files stay in the test's own home
	ch, err := NewWeixinChannel(config.WeixinConfig{TokenRef: "WEIXIN_GATE", BaseURL: baseURL},
		credentials.SecretBundle{"WEIXIN_GATE": "gate-token"}, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewWeixinChannel: %v", err)
	}
	return ch
}

// TestWeixinStart_RejectedTokenFailsTheStart is Train 2 gate r1 (BDD-8.3
// channel survey): Start set the channel running and the poll loop only
// logged a rejected token. An HTTP 401 or a ret=-14 answer to the first poll
// must fail Start; an accepted poll, an unreachable server and a server that
// holds the long poll past the documented 3 s limit keep the channel running.
func TestWeixinStart_RejectedTokenFailsTheStart(t *testing.T) {
	for _, mode := range []string{"http401", "expired"} {
		ch := newGateWeixinChannel(t, fakeILinkServer(t, mode))
		if err := ch.Start(context.Background()); err == nil {
			_ = ch.Stop(context.Background())
			t.Fatalf("%s: a rejected token must fail Start", mode)
		}
		if ch.IsRunning() {
			t.Fatalf("%s: a rejected channel must not be running", mode)
		}
	}
	controls := map[string]string{
		"accepted":    fakeILinkServer(t, "ok"),
		"unreachable": "http://127.0.0.1:1/",
		"silent":      fakeILinkServer(t, "silent"),
	}
	for name, baseURL := range controls {
		ch := newGateWeixinChannel(t, baseURL)
		began := time.Now()
		err := ch.Start(context.Background())
		elapsed := time.Since(began)
		if err != nil {
			t.Fatalf("%s: Start must not fail, got %v", name, err)
		}
		if !ch.IsRunning() {
			t.Fatalf("%s: the channel must keep running", name)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("%s: Start must return within the 3 s first-poll limit, took %s", name, elapsed)
		}
		_ = ch.Stop(context.Background())
	}
}

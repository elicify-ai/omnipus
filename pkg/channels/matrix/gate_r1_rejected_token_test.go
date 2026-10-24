//go:build goolm

package matrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func newTokenTestMatrix(t *testing.T, reject bool) *MatrixChannel {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if reject {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"Invalid access token passed."}`))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/account/whoami"):
			_, _ = w.Write([]byte(`{"user_id":"@bot:matrix.test","device_id":"DEV"}`))
		case strings.Contains(r.URL.Path, "/sync"):
			<-r.Context().Done() // a long-poll sync that never returns data
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)
	ch, err := NewMatrixChannel(config.MatrixConfig{Homeserver: server.URL, UserID: "@bot:matrix.test", AccessTokenRef: "MATRIX_TOKEN"},
		credentials.SecretBundle{"MATRIX_TOKEN": "syt_gate_r1_token"}, bus.NewMessageBus(), "")
	if err != nil {
		t.Fatalf("NewMatrixChannel: %v", err)
	}
	return ch
}

// TestMatrixStart_RejectedTokenFailsTheStart is Train 2 gate r1 (BDD-8.3
// channel survey): a token the homeserver rejects (401 M_UNKNOWN_TOKEN) was
// only seen by the background sync, which logs; Start reported the channel
// running. It must fail Start instead. A valid token starts (control).
func TestMatrixStart_RejectedTokenFailsTheStart(t *testing.T) {
	ch := newTokenTestMatrix(t, true)
	if err := ch.Start(context.Background()); err == nil {
		_ = ch.Stop(context.Background())
		t.Fatal("a rejected token must fail Start")
	}
	if ch.IsRunning() {
		t.Fatal("a rejected token must leave the channel not running")
	}

	ok := newTokenTestMatrix(t, false)
	if err := ok.Start(context.Background()); err != nil {
		t.Fatalf("a valid token must start, got %v", err)
	}
	if !ok.IsRunning() {
		t.Fatal("a valid token must leave the channel running")
	}
	_ = ok.Stop(context.Background())
}

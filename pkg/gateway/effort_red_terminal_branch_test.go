package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-70's real server half, paired with pkg/app/internal/run's actual socket
// sender. No effort option is set on this incoming webchat/terminal request.
func TestT_GW_S70_TerminalTextUsesRealWebCommandBranch(t *testing.T) {
	var calls atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"MODEL-MUST-NOT-HANDLE-COMMAND"},"finish_reason":"stop"}]}`)
	}))
	defer peer.Close()
	cfg := effortProductionConfig(t, peer.URL, peer.URL)
	f := newEffortProductionTurn(t, cfg)
	path := filepath.Join(config.OmnipusHomeDir(), "config.json")
	require.NoError(t, config.SaveConfig(path, cfg))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.handler.handleChatMessage(ctx, "effort-production", f.sid, "/effort high", "mia", nil, "", "", f.wc)
	select {
	case answer := <-f.msgBus.OutboundChan():
		assert.Equal(t, "Use the effort chip next to the model in the web chat. Effort isn't available in omnipus run yet.", answer.Content)
	case <-ctx.Done():
		t.Fatal("actual terminal/web command produced no reply")
	}
	assert.Equal(t, int64(0), calls.Load(), "server handles the slash command without an LLM request")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	meta, err := f.al.GetSessionStore().GetMeta(f.sid)
	require.NoError(t, err)
	assert.NotContains(t, effortPublicJSON(t, meta), "reasoning_effort", "terminal command does not write a conversation value")
}

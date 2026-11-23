package agent

// Rev8 S108: actual background producer's carrier and actual system-message
// continuation. No fabricated callback, no inferred model-text provenance.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualBackgroundHandoff(t *testing.T) {
	t.Run("S108_background_completion_continues_a_telegram_turn", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, "tok_valid_12345")
		started := programsConnectorBash(t, f, sid, "s108-start", map[string]any{"command": "sleep 1; printf 'S108_CHILD_RAN'", "run_in_background": true})
		require.NotEmpty(t, started.SessionID)
		completion := programsCompletion(t, events, sid)
		require.Contains(t, completion.Content, "S108_CHILD_RAN", "positive instrument: a real child triggered the actual notifier")
		var wake bus.InboundMessage
		select {
		case wake = <-f.bus.InboundChan():
		case <-time.After(12 * time.Second):
			t.Fatal("S108: actual background notifier published no continuation")
		}
		require.Equal(t, "async:bash", wake.Sender.CanonicalID)
		require.Equal(t, sid, wake.AsyncTranscriptSessionID)
		require.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), wake.Provenance, "S108: the real background producer carries the original input origin, never web or empty")
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		path := filepath.Join(work, "s108-continuation-count")
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S108_CONTINUATION_CHILD_RAN'", path)
		f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall("s108-next", command), wu5Stop("actual continuation completed")})
		_, _, err := f.al.processMessage(context.Background(), wake)
		require.NoError(t, err)
		f.waitForCalls(sid, 2, "real background-origin continuation child")
		result := f.provider.resultForCall(sid, "s108-next")
		require.Contains(t, result, "S108_CONTINUATION_CHILD_RAN")
		count, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "0", strings.TrimSpace(string(count)), "S108: Telegram-off provenance remains restrictive in the real continuation")
		require.Contains(t, result, programsTelegramNotice, "S108: exact original-origin notice, not a generic unresolved-origin fallback")
		f.provider.mu.Lock()
		requests, err := json.Marshal(f.provider.prompts[sid])
		f.provider.mu.Unlock()
		require.NoError(t, err)
		require.NotContains(t, string(requests), "tok_valid_12345")
	})
}

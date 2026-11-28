package agent

// Spec rev8 D4 row14 / S104 / FR-020: a real retained-output read BEFORE the
// agent's downstream result limit. The actual registered tool owns this buffer.
import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_ActualRetentionCap(t *testing.T) {
	t.Run("S104_background_retention_cap", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		const capBytes = 1 << 20 // Dataset D4 row14 names the actual 1MiB retention boundary.
		// The harmless trailing 64KiB forces the cap branch even after source
		// masking shortens the token. Without it, the masked boundary can fit
		// exactly, which would not prove that retention actually truncated.
		command := "sleep 1; dd if=/dev/zero bs=1048566 count=1 2>/dev/null | tr '\\000' a; printf '%s' \"$GH_TOKEN\"; dd if=/dev/zero bs=65536 count=1 2>/dev/null | tr '\\000' Z"
		child := programsStartBackground(t, f, sid, "s104-retention", command)
		completion := programsCompletion(t, events, sid)
		programsNoTokenWindow(t, programsBackgroundSentinel, completion.Content, "actual cap-boundary completion")
		inst, ok := f.al.GetRegistry().GetAgent("dev")
		require.True(t, ok)
		tool, ok := inst.Tools.Get("bash")
		require.True(t, ok)
		ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "dev"), sid)
		result := tool.Execute(ctx, map[string]any{"action": "read", "session_id": child})
		require.False(t, result.IsError, "the real tool's retained read must succeed before any agent output-budget processing")
		var read tools.ExecResponse
		raw := result.ContentForLLM()
		require.NoError(t, json.Unmarshal([]byte(raw), &read))
		require.Equal(t, child, read.SessionID)
		require.Empty(t, read.Error)
		leading := len(read.Output) - len(strings.TrimLeft(read.Output, "a"))
		require.Equal(t, capBytes-10, leading, "instrument: exact real child prefix places the sentinel across the actual retention boundary")
		require.Contains(t, read.Output, "output truncated, exceeded 1MB", "instrument: the actual retention-cap branch ran; downstream tool truncation is not this boundary")
		require.GreaterOrEqual(t, len(read.Output), leading+len("[FILTERED]"))
		require.Equal(t, "[FILTERED]", read.Output[leading:leading+len("[FILTERED]")], "source masking precedes retention at the actual token position")
		programsNoTokenWindow(t, programsBackgroundSentinel, read.Output, "actual full retained output before agent limit")
		programsNoTokenWindow(t, programsBackgroundSentinel, raw, "actual retained-read JSON before agent limit")
	})
}

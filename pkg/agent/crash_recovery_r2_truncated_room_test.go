//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// C3/A2 / DESIGN 3 correlation, model view and every-later-entry: a real
// output-limit continuation flushed before tools has the same causal scope as
// ordinary narration. No fabricated accumulator, recovery selector or launcher.
func TestCrashRecovery_R2TruncatedRoomPrefixRetiresBeforeNextModel(t *testing.T) {
	const prefix = closureRoomPlan + "_TRUNCATED_PREFIX: finish the old room demand"
	f := crashConsumedRoomContext(t, true, func(h *crashHarness, p *crashScript) {
		h.al.GetRegistry().primaryChatAgentForTest().MaxIterations = 4
		ordinary := p.next
		p.next = func(i int, input []providers.Message) (*providers.LLMResponse, error) {
			if i == 0 {
				return &providers.LLMResponse{Content: prefix, FinishReason: "length"}, nil
			}
			return ordinary(i-1, input)
		}
	})
	var flushed []session.ModelSlot
	for _, slot := range f.view.Live {
		if slot.Message.Role == "assistant" && slot.Message.Content == prefix {
			flushed = append(flushed, slot)
		}
	}
	require.Len(t, flushed, 1, "instrument: native truncated→tool-call path flushed its prefix exactly once")
	assert.NotNil(t, flushed[0].Source.RoomScope, "flushed derived text must carry trusted request retirement scope")
	snapshot := f.h.snapshot(t)
	f.release()
	require.NoError(t, crashAwait(t, f.done))
	p := &crashScript{}
	f.h.restore(t, snapshot, p)
	f.h.recover(t)
	// The ordinary next human entry drives actual retirement and assembly; this
	// is not a synthetic call to roomProjection.apply.
	_, err := f.h.human(t, f.id, "NEW_INDEPENDENT_HUMAN_REQUEST")
	require.NoError(t, err)
	require.NotEmpty(t, p.calls(), "positive instrument: the new ordinary request reached its real provider")
	for _, input := range p.calls() {
		crashAssertNoCausalRoomDemand(t, input)
		for _, m := range input {
			if m.Role == "assistant" || m.Role == "user" {
				assert.NotContains(t, m.Content, prefix, "flushed room plan must not become active work after retirement")
			}
		}
	}
	entries, err := f.h.al.GetSessionStore().ReadTranscript(f.id)
	require.NoError(t, err)
	var preserved int
	for _, entry := range entries {
		if strings.Contains(entry.Content, prefix) {
			preserved++
		}
	}
	assert.Greater(t, preserved, 0, "retirement excludes model input but preserves the actual chat/archive prefix")
	view, err := f.h.al.GetSessionStore().WindowView(context.Background(), f.id)
	require.NoError(t, err)
	var settled int
	for _, slot := range view.Live {
		if slot.Message.Role == "tool" && slot.Message.ToolCallID == "causal-effect" {
			settled++
		}
	}
	assert.Equal(t, 1, settled, "retirement never removes completed file-effect facts")
	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, "G1\n", string(data))
}

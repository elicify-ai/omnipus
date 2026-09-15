// loop_truncation_test.go: tests for detect a truncated model reply and decide whether to continue it

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestIsTruncatedFinishReason pins the finish_reason spellings that mean
// "ran out of output tokens". OpenAI says "length"; the shared normaliser
// (providers/common.normalizeFinishReason) rewrites that to "truncated"; not
// every adapter routes through it.
func TestIsTruncatedFinishReason(t *testing.T) {
	for _, r := range []string{"truncated", "length", "max_tokens", "LENGTH", " truncated "} {
		assert.True(t, isTruncatedFinishReason(r), "%q means the output was cut off", r)
	}
	for _, r := range []string{"stop", "tool_calls", "", "unknown", "content_filter"} {
		assert.False(t, isTruncatedFinishReason(r), "%q does not mean the output was cut off", r)
	}
}

// TestStripOrphanToolCallMarkup_LeavesCleanResponsesAlone is the negative
// control: a normal answer must pass through byte-identical, and a round that
// carries no residue must not be flagged.
func TestStripOrphanToolCallMarkup_LeavesCleanResponsesAlone(t *testing.T) {
	clean := "I wrote count.txt with ONE, TWO, THREE, FOUR, FIVE — one per line.\n\n```\nONE\nTWO\n```"
	resp := &providers.LLMResponse{Content: clean, ReasoningContent: "The user wants five numbers."}

	markup, found := stripOrphanToolCallMarkup(resp)
	assert.False(t, found, "a clean response must not be flagged; got marker %q", markup.Marker)
	assert.Equal(t, clean, resp.Content, "a clean response must pass through untouched")
	assert.Equal(t, "The user wants five numbers.", resp.ReasoningContent)

	var nilResp *providers.LLMResponse
	_, found = stripOrphanToolCallMarkup(nilResp)
	assert.False(t, found, "a nil response must be handled without panicking")
}

// TestStripOrphanToolCallMarkup_CoversReasoningContent pins that the fallback
// field is stripped too. The no-tool-calls branch uses ReasoningContent as the
// answer when Content is empty, so leaving it alone would relocate the leak
// rather than close it.
func TestStripOrphanToolCallMarkup_CoversReasoningContent(t *testing.T) {
	resp := &providers.LLMResponse{
		Content:          "",
		ReasoningContent: "I should write the file." + uatOrphanToolMarkupLeaks[2].content,
	}
	markup, found := stripOrphanToolCallMarkup(resp)
	require.True(t, found)
	assert.Equal(t, "</arg_key>", markup.Marker)
	assert.Equal(t, "I should write the file.", resp.ReasoningContent)
	assertNoToolMarkup(t, "the stripped reasoning", resp.ReasoningContent)
}

// TestOrphanToolMarkupRepairMessage_DoesNotEchoTheResidue pins the deliberate
// choice not to quote the model's own malformed output back at it.
func TestOrphanToolMarkupRepairMessage_DoesNotEchoTheResidue(t *testing.T) {
	msg := orphanToolMarkupRepairMessage("stop")
	assert.Equal(t, "user", msg.Role)
	assertNoToolMarkup(t, "the repair note", msg.Content)
	assert.NotContains(t, strings.ToLower(msg.Content), "count.txt",
		"the note must be generic — it never carries the failed call's contents")
}

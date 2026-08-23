// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// projection_test.go — ADR-066 FR-019: the ONE pure projection function
// (spec test 14, B-21 / B-22 / B-12). Both views — capped and emptied —
// come from the same function; it never mutates its input and it is
// deterministic, so the live window and a reload agree byte for byte.
package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestProjection_PureFunction(t *testing.T) {
	big := strings.Repeat("r", 100_000)
	small := "small result"
	archive := []memory.ArchivedMessage{
		{Message: providers.Message{Role: "user", Content: "turn one"}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "c1", Name: "read_file", Function: &providers.FunctionCall{Name: "read_file"}},
		}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c1", Content: big}},
		{Message: providers.Message{Role: "user", Content: "turn two"}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "c2", Name: "bash", Function: &providers.FunctionCall{Name: "bash"}},
			{ID: "c3", Name: "bash", Function: &providers.FunctionCall{Name: "bash"}},
		}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c2", Content: small}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c3", Content: big}},
	}
	// Window = archive from Skip = 0.
	history := make([]providers.Message, len(archive))
	for i := range archive {
		history[i] = archive[i].Message
	}
	lineOf := func(i int) int { return i }
	pc := projectionContext{
		policy:  capPolicyFor(config.DefaultContextSettings(), 400_000),
		archive: archive,
	}

	t.Run("empty set is the identity", func(t *testing.T) {
		out := projectMessages(history, lineOf, memory.ProjectionSet{}, pc)
		assert.Equal(t, history, out)
		out = projectMessages(history, lineOf, nil, pc)
		assert.Equal(t, history, out)
	})

	t.Run("capped view: head-and-tail with the cap mark, input untouched", func(t *testing.T) {
		set := memory.ProjectionSet{{ToolCallID: "c1", ArchiveLine: 2}: memory.ProjectionCapped}
		out := projectMessages(history, lineOf, set, pc)
		require.Len(t, out, len(history))
		assert.Equal(t, big, history[2].Content, "pure: the input slice is never mutated")
		assert.NotEqual(t, big, out[2].Content)
		assert.LessOrEqual(t, len([]rune(out[2].Content)), 64_000)
		assert.Contains(t, out[2].Content, `"content_state":"capped"`)
		assert.Contains(t, out[2].Content, `"archive_line":2`)
		assert.Contains(t, out[2].Content, `"turn":2`, "1 + the one role:user line before line 2 (T066-04's definition)")
		assert.Contains(t, out[2].Content, `"tool":"read_file"`)
		assert.Equal(t, "c1", out[2].ToolCallID, "role, id and slot unchanged")
		assert.Equal(t, "tool", out[2].Role)
		// Everything else is identical.
		for i := range out {
			if i != 2 {
				assert.Equal(t, history[i], out[i], "message %d", i)
			}
		}
		// Deterministic: same inputs, same bytes (the B-12/B-22 guarantee).
		again := projectMessages(history, lineOf, set, pc)
		assert.Equal(t, out, again)
	})

	t.Run("emptied view: the content becomes the recall mark only", func(t *testing.T) {
		set := memory.ProjectionSet{{ToolCallID: "c3", ArchiveLine: 6}: memory.ProjectionEmptied}
		out := projectMessages(history, lineOf, set, pc)
		assert.Equal(t, big, history[6].Content, "pure")
		assert.Contains(t, out[6].Content, `"content_state":"emptied"`)
		assert.Contains(t, out[6].Content, `"tool":"bash"`)
		assert.Contains(t, out[6].Content, `"turn":3`, "1 + the two role:user lines before line 6")
		assert.Contains(t, out[6].Content, `"size_chars":100000`)
		assert.NotContains(t, out[6].Content, "rrrr", "no original content survives an empty")
		assert.Equal(t, "c3", out[6].ToolCallID)
		assert.Equal(t, small, out[5].Content, "sibling result untouched")
	})

	t.Run("composite key: same id on another line is not projected", func(t *testing.T) {
		set := memory.ProjectionSet{{ToolCallID: "c3", ArchiveLine: 99}: memory.ProjectionEmptied}
		out := projectMessages(history, lineOf, set, pc)
		assert.Equal(t, history, out)
	})

	t.Run("parallel calls use the /N cap on reload exactly as live", func(t *testing.T) {
		smallBudget := projectionContext{policy: capPolicyFor(config.DefaultContextSettings(), 3_000), archive: archive}
		set := memory.ProjectionSet{{ToolCallID: "c3", ArchiveLine: 6}: memory.ProjectionCapped}
		out := projectMessages(history, lineOf, set, smallBudget)
		// Two parallel bash calls on B = 3,000: effective 3,750; 2 × 3,750 × 0.4 = 3,000 is not > B → 3,750.
		assert.LessOrEqual(t, len([]rune(out[6].Content)), 3_750)
		assert.Greater(t, len([]rune(out[6].Content)), 1_875, "not split when the pair already fits")
	})

	t.Run("skip offset: lineOf maps window index to archive line", func(t *testing.T) {
		window := history[3:] // Skip = 3
		set := memory.ProjectionSet{{ToolCallID: "c3", ArchiveLine: 6}: memory.ProjectionCapped}
		out := projectMessages(window, func(i int) int { return 3 + i }, set, pc)
		assert.Contains(t, out[3].Content, `"archive_line":6`)
		assert.Contains(t, out[3].Content, `"turn":3`, "turn number counts user lines in the whole archive, including evicted ones")
	})
}

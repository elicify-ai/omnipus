package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestRecallMark_SingleProducerSanitised is spec test 13 (ADR-066 D5,
// FR-018, B-21, edge case E6): the mark that replaces an emptied or capped
// tool result is built by ONE typed producer, names the tool and the
// tool_call_id sanitised to at most 64 printable runes, carries the archive
// line, the full size in characters, the turn number (1 + the count of
// role:user archive lines preceding the result's line) and the recall hint.
func TestRecallMark_SingleProducerSanitised(t *testing.T) {
	hostileName := "mcp_evil\x00\x1b[2J\r\n" + strings.Repeat("Z", 200)
	hostileID := "call\x07\x08" + strings.Repeat("9", 200)
	content := strings.Repeat("résumé ", 1000) // 7,000 runes, 9,000 bytes

	// Archive: turn 1 (user, assistant+call, tool), turn 2 (user, assistant+call,
	// tool@5, tool@6), turn 3 (user). A system line at 0 must not count.
	archive := []memory.ArchivedMessage{
		{Message: providers.Message{Role: "system", Content: "soul"}},
		{Message: providers.Message{Role: "user", Content: "hi"}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c1"}}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c1", Content: "r1"}},
		{Message: providers.Message{Role: "user", Content: "again"}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: hostileID}}}},
		{Message: providers.Message{Role: "tool", ToolCallID: hostileID, Content: content}},
		{Message: providers.Message{Role: "user", Content: "third"}},
	}

	for _, state := range []string{"emptied", "capped"} {
		t.Run(state, func(t *testing.T) {
			mark, err := buildRecallMark(state, hostileName, hostileID, 6, content, turnNumberForArchiveLine(archive, 6))
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(mark), &parsed), "mark must be the JSON payload: %s", mark)
			assert.Equal(t, "tool_result_recall_mark", parsed["error"])
			assert.Equal(t, state, parsed["content_state"])

			tool, _ := parsed["tool"].(string)
			id, _ := parsed["tool_call_id"].(string)
			for name, v := range map[string]string{"tool": tool, "tool_call_id": id} {
				assert.LessOrEqual(t, len([]rune(v)), 64, name)
				for _, r := range v {
					assert.Truef(t, r >= 0x20 && r != 0x7f, "%s carries non-printable %q", name, r)
				}
			}
			assert.True(t, strings.HasPrefix(tool, "mcp_evil[2JZZZ"), "non-printables stripped, head kept: %q", tool)
			assert.True(t, strings.HasPrefix(id, "call999"), "non-printables stripped, head kept: %q", id)

			assert.EqualValues(t, 6, parsed["archive_line"])
			assert.EqualValues(t, len([]rune(content)), parsed["size_chars"], "size counts runes, not bytes")
			assert.EqualValues(t, 3, parsed["turn"], "1 + the two role:user lines before line 6 (the system line and the later user line do not count)")

			hint, _ := parsed["hint"].(string)
			assert.Contains(t, hint, "recall_conversation")
			assert.Contains(t, hint, "tool_call_id")
			assert.Contains(t, hint, "archive_line")
			assert.Contains(t, hint, id, "hint quotes the SANITISED id")
			assert.NotContains(t, hint, "\x07")
			assert.Contains(t, hint, "6")
		})
	}

	t.Run("turn number counts only preceding user lines", func(t *testing.T) {
		assert.Equal(t, 1, turnNumberForArchiveLine(archive, 0))
		assert.Equal(t, 1, turnNumberForArchiveLine(archive, 1), "the user line itself is not preceding")
		assert.Equal(t, 2, turnNumberForArchiveLine(archive, 3))
		assert.Equal(t, 3, turnNumberForArchiveLine(archive, 6))
		assert.Equal(t, 3, turnNumberForArchiveLine(archive, 7), "the user line at 7 is not preceding itself")
		assert.Equal(t, 4, turnNumberForArchiveLine(archive, 99), "a line past the end counts every user line")
		assert.Equal(t, 1, turnNumberForArchiveLine(archive, -1))
	})

	t.Run("unknown state is refused", func(t *testing.T) {
		_, err := buildRecallMark("full", "t", "c", 0, "x", 1)
		require.Error(t, err)
	})

	t.Run("no fmt.Sprintf assembles the mark", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		src, err := os.ReadFile(filepath.Join(filepath.Dir(file), "recall_mark.go"))
		require.NoError(t, err)
		assert.NotContains(t, string(src), "fmt.Sprintf", "the mark is a typed payload via marshalWithinBudget, never string-assembled (T066-04 DoD)")
	})
}

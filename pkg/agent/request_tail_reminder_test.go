package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestTailOmitsSkillReminder pins the founder's 2026-10-21 removal
// decision. The clock and other request-only facts remain; the repeated skill
// directive does not. Its literal is independent of the deleted production symbol.
func TestRequestTailOmitsSkillReminder(t *testing.T) {
	const retiredReminder = `When starting a new task or acting on a new request, check the "# Skills" list for one whose description matches, call Skill before proceeding, and do this silently -- never narrate it.`

	t.Run("clock renderer contains facts only", func(t *testing.T) {
		text := buildVolatileContext()
		assert.NotContains(t, text, retiredReminder, "the retired skill reminder must not be rendered")
		assert.NotContains(t, text, "Short request reminder")
		require.Len(t, strings.Split(strings.TrimSpace(text), "\n"), 3,
			"the clock renderer must contain only its heading, local clock and UTC clock")
		requireRequestTailClockFacts(t, text)
	})

	t.Run("assembled tail retains clock and other facts", func(t *testing.T) {
		t.Setenv(config.EnvHome, t.TempDir())
		t.Setenv(config.EnvBuiltinSkills, t.TempDir())
		home := t.TempDir()
		skillDir := filepath.Join(home, "skills", "release-notes")
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
			"---\nname: release-notes\ndescription: Use when drafting release notes\n---\n\nSKILL-BODY-MARKER\n"), 0o600))
		cb := NewContextBuilder(home).WithMemoryEnabled(false).WithSkillAllowlist([]string{"release-notes"})
		history := []providers.Message{
			{Role: "user", Content: "earlier request"},
			{Role: "assistant", Content: "earlier answer"},
		}
		messages := cb.BuildMessages(0, history, "current request", nil, "", "test", "chat-1", "user-1", "Alice", "", nil)
		const scratchFact = "## Scratchpad\nTAIL-SCRATCHPAD-FACT"
		const manifestFact = "## More tools\nTAIL-TOOL-SUMMARY-FACT"
		messages = appendVolatileNote(messages, scratchFact)
		messages = injectManifestNote(messages, manifestFact)
		messages = consolidateVolatileNotes(messages)

		require.Len(t, messages, len(history)+3, "system, unchanged history, current request, and one fact tail")
		require.Equal(t, "system", messages[0].Role)
		assert.Equal(t, history, messages[1:len(history)+1], "removing the reminder must not change history")
		assert.Equal(t, providers.Message{Role: "user", Content: "current request"}, messages[len(messages)-2])
		tail := messages[len(messages)-1]
		assert.Equal(t, "user", tail.Role)
		assert.True(t, tail.Volatile, "request-only facts remain outside the stable prefix")
		assert.Empty(t, tail.Media)
		assert.Empty(t, tail.ToolCalls)
		assert.Empty(t, tail.ToolCallID)
		assert.Empty(t, tail.SystemParts)
		assert.NotContains(t, tail.Content, retiredReminder, "the actual request tail must omit the retired directive")
		assert.NotContains(t, tail.Content, "Short request reminder")
		assert.Equal(t, 1, strings.Count(tail.Content, scratchFact), "scratchpad facts must survive exactly once")
		assert.Equal(t, 1, strings.Count(tail.Content, manifestFact), "tool-summary facts must survive exactly once")
		requireRequestTailClockFacts(t, tail.Content)

		require.NotEmpty(t, messages[0].SystemParts)
		static := messages[0].SystemParts[0]
		require.NotNil(t, static.CacheControl, "the Skills menu keeps its stable cache boundary")
		assert.Equal(t, "ephemeral", static.CacheControl.Type)
		assert.Contains(t, static.Text, "# Skills", "the existing Skills menu is not removed")
		assert.Contains(t, static.Text, "release-notes", "the granted skill stays in the menu")
		assert.NotContains(t, tail.Content, "release-notes", "the menu is not repeated in the fact tail")
		for _, message := range messages[:len(messages)-1] {
			assert.NotContains(t, message.Content, "## Current Time")
			assert.NotContains(t, message.Content, scratchFact)
			assert.NotContains(t, message.Content, manifestFact)
		}
	})
}

func requireRequestTailClockFacts(t *testing.T, text string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.GreaterOrEqual(t, len(lines), 3, "local and UTC clock facts must both be present")
	assert.Equal(t, "## Current Time", lines[0])
	assert.Equal(t, 1, strings.Count(text, "## Current Time"))
	require.True(t, strings.HasPrefix(lines[2], "UTC: "), "the UTC clock must remain explicit")
	assert.Equal(t, 1, strings.Count(text, "UTC: "))
	local, err := time.Parse("2006-01-02 15:04 MST (-07:00) Monday", lines[1])
	require.NoError(t, err, "the existing local date, time, zone, offset and weekday remain valid")
	utc, err := time.Parse("2006-01-02 15:04:05Z", strings.TrimPrefix(lines[2], "UTC: "))
	require.NoError(t, err, "the existing UTC date and seconds remain valid")
	assert.Equal(t, local.UTC().Truncate(time.Minute), utc.UTC().Truncate(time.Minute),
		"the two clock facts must describe the same instant at local-clock precision")
}

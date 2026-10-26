package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are the founder-approved #1399 instructions, before the interim gate.
// Gating changes their presence, never their wording.
const approvedMemoryRule = `### Memory
14. Use the memory guide to choose correct recall sources and appropriate saved-note audiences; if the guide is unavailable, do not invent its contents.`

const approvedMemoryGuide = `<memory_guide>
- Use the four dedicated memory tools, never write_file to memory files.
- Before starting a task or answering about past work, decisions, people, preferences, or project facts, check recall_memory first.
- Recall is cheap; use it often.
- Use recall_conversation(query | turn_range | time) when the user refers to earlier content in THIS conversation that is no longer in view.
- Use recall_memory(query[, room]) for durable cross-session notes and past retrospectives; room='both' is the default and searches both rooms.
- Save with remember(content, category, room); category is key_decision, reference, or lesson_learned.
- Set room on every remember call; there is no default.
- Remember immediately, not only at the end, whenever you learn:
  - a decision made;
  - a user preference or correction;
  - a stable fact about the user, workspace, project, people, or systems;
  - a lesson from a mistake or a fix;
  - a reference a future session needs, such as where something lives or how it works.
- Choose room='shared' when a teammate could need the note; choose room='private' for your own working notes.
- Shared notes are visible to the workspace team; private notes are visible only to you.
- Check the returned receipt for the room actually used and any warning.
- Do not save transient progress, chatter, things already in files or docs, guesses, or secrets (keys, passwords, tokens).
- Prefer one precise note over many vague ones.
- Update existing notes rather than duplicate them; report a missing update capability instead of inventing an operation.
- After finishing a task, check whether anything is worth remembering.
- REFLECT: run_retrospective(went_well, needs_improvement) at the end of a productive session, after the user has reviewed the summary.
</memory_guide>`

func TestContextBuilder_MemoryGuidanceFollowsMemoryEnabled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setFlag bool
		enabled bool
	}{
		{name: "default includes approved guidance", enabled: true},
		{name: "explicit true includes approved guidance", setFlag: true, enabled: true},
		{name: "disabled suppresses the whole guidance", setFlag: true, enabled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb := NewContextBuilder(t.TempDir())
			t.Cleanup(cb.Memory().Close)
			if tc.setFlag {
				cb.WithMemoryEnabled(tc.enabled)
			}
			prompt := cb.BuildSystemPrompt()
			assert.Contains(t, prompt, "<operating_rules>", "the interim memory gate preserves the other operating rules")
			if !tc.enabled {
				assert.NotContains(t, prompt, "### Memory")
				assert.NotContains(t, prompt, "14. Use the memory guide")
				assert.NotContains(t, prompt, "<memory_guide>")
				assert.NotContains(t, prompt, "</memory_guide>")
				return
			}
			assert.Contains(t, prompt, approvedMemoryRule)
			start := strings.Index(prompt, "<memory_guide>")
			require.NotEqual(t, -1, start, "enabled memory includes the guide")
			end := strings.Index(prompt[start:], "</memory_guide>")
			require.NotEqual(t, -1, end, "the full guide is framed")
			assert.Equal(t, approvedMemoryGuide, prompt[start:start+end+len("</memory_guide>")])
		})
	}
}

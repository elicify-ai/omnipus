package tools

// Oracle: r17 §7.2 TK-03 and D-1 rows 6a–6d. The task prompt is
// independent of its paired goal's 4,000-character limit. Real stores/tools;
// neither prompt persistence nor goal synchronisation is mocked.
import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateTask_D1_PromptGoalBoundaries(t *testing.T) {
	for _, n := range []int{0, 3999, 4000, 4001, 9999, 10000} {
		t.Run(fmt.Sprintf("prompt_%d_characters", n), func(t *testing.T) {
			store, goals := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, true)
			prompt := strings.Repeat("A", n)
			wantGoal := prompt
			if n == 0 {
				wantGoal = tk.Title
			} else if n > 4000 {
				wantGoal = prompt[:4000]
			}
			result := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "prompt": prompt})
			require.False(t, result.IsError, "D-1 within task bound: %s", result.ForLLM)
			fresh, err := store.Get(tk.ID)
			require.NoError(t, err)
			assert.Equal(t, prompt, fresh.Prompt, "task keeps the whole supplied prompt, including explicit empty")
			assert.Equal(t, wantGoal, parityGoalPrompt(t, goals, tk.ID), "paired goal keeps at most 4,000 characters; empty falls back to title")
		})
	}
}

//go:build goolm && stdjson

package agent

// Order 61 — TestTimedAllowance_Unattended (S-123; C-26 points 3 and 5).
// Oracle: skills-tools-redesign-spec.md —
//   S-123: delete_task at global Ask, Auto-approve off, a scheduled task of
//        Mia that calls it with arguments A, Mia's card for that call
//        answered with "Allow for 1 hour". The task run within the hour
//        deletes the task with no card; the run after the test clock passes
//        the hour is refused with "Not run: needs approval, and task and
//        plan runs never ask.", no card appears and the run does not wait.
//   C-26 point 5: unattended task, plan and scheduled runs honour a timed
//        allowance that is still running (replacing today's per-chat
//        "Always Allow grant" wording), and they still never ask or wait.
//   C-26 Unattended row: resolveAskPolicy consults Auto-approve and
//        standing allowances before the unattended refusal, as it does
//        today with grants; a running allowance lets the call run; nothing
//        asks or waits.
//
// Leg 1 is the real control: the refusal text and its permanence come from
// the sole source of denial semantics (pkg/agent/tool_denial.go::
// ClassifyDenial on the fixed task-plan reason), so the S-123 text is
// pinned at its production source today. Leg 2 is the decision the package
// does not make yet: nothing consults a timed allowance before the
// unattended refusal — resolveUnattendedAsk below is that seam; GREEN wires
// it to the unattended branch of resolveAskPolicy reading the C-26 store.
// The allowance store halves ride order 52's controlled-clock seams
// (timed_allowance_scheduled_red_test.go). Nil keeps the RED run loud
// (BLOCKED), never a skip.
import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// resolveUnattendedAsk resolves one ask-policy call the way an unattended
// task, plan or scheduled run does: Auto-approve and operator rules first,
// then the C-26 standing allowances, and — when nothing settles it — the
// fixed refusal, immediately (never a pending ask, never a wait). It
// returns whether the call runs and, when refused, the refusal text.
// GREEN wires it to the unattended decision against the store the
// grantTimedAllowance seam writes; nil keeps the RED run loud (BLOCKED),
// never a skip.
var resolveUnattendedAsk func(t testing.TB, home, agentID, tool string, args map[string]any, at time.Time) (settled bool, refusal string)

func TestTimedAllowance_Unattended(t *testing.T) {
	t.Run("control: the unattended refusal is the fixed task-plan text and never waits", func(t *testing.T) {
		class, ok := ClassifyDenial(autoDenyTaskPlanReason)
		require.True(t, ok, "the fixed task-plan reason classifies (tool_denial.go)")
		require.True(t, class.Permanent,
			"S-123: the refusal is permanent — the run does not wait and does not retry")
		require.Equal(t, "Not run: needs approval, and task and plan runs never ask.",
			class.TranscriptText,
			"S-123: the refusal text is the spec's exact line (tool_denial.go::denialTable)")
	})

	t.Run("S-123: a running allowance lets the unattended call run; after the end it is refused", func(t *testing.T) {
		if resolveUnattendedAsk == nil || grantTimedAllowance == nil {
			t.Fatal("BLOCKED: the unattended decision that consults a timed allowance " +
				"before the unattended refusal (C-26 point 5 and its Unattended row, " +
				"resolveAskPolicy's standing-allowance check) is not implemented — " +
				"required by S-123")
		}
		home := t.TempDir()
		args := map[string]any{"task_id": "t1"}
		grantTimedAllowance(t, home, "mia", "delete_task", "1h")

		settled, refusal := resolveUnattendedAsk(t, home, "mia", "delete_task", args, time.Now())
		require.True(t, settled,
			"S-123: within the hour the scheduled run deletes the task with no card")
		require.Empty(t, refusal,
			"S-123: nothing is refused while the allowance runs")

		settled, refusal = resolveUnattendedAsk(t, home, "mia", "delete_task", args, time.Now().Add(time.Hour))
		require.False(t, settled,
			"S-123: after the hour the call is not settled by the ended allowance")
		require.Equal(t, "Not run: needs approval, and task and plan runs never ask.", refusal,
			"S-123: the refusal carries the spec's text — no card appears and the run does not wait")

		// Another agent's scheduled run was never covered by Mia's answer.
		settled, _ = resolveUnattendedAsk(t, home, "jim", "delete_task", args, time.Now())
		require.False(t, settled,
			"C-26 point 2: the allowance is bound to the agent — Jim's unattended run still refuses")
	})
}

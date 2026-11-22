//go:build goolm && stdjson

package agent

// Order 52 — TestTimedAllowance_ReachesScheduledRun (S-109, S-113; C-26
// points 2, 3, 5). Oracle: skills-tools-redesign-spec.md —
//   S-109: trust Allow, verified `weather` at global Ask, granted to Mia
//        and Ray, Auto-approve off, scheduled tasks of both. Before the
//        answer the tasks skip with the unattended text. The person answers
//        Mia's card with "Allow for 1 day": Mia's task then loads `weather`
//        with no card, Ray's task still skips it, the global permission of
//        `weather` is still Ask with no `sandbox.skill_policies` audit
//        entry, and after the day Mia's task skips it again.
//   C-26 point 2: the allowance is bound to the agent — other agents are
//        unaffected. Point 3: it ends at its time or when the gateway
//        restarts; it is not saved as a setting. Point 5: it works the same
//        way under the agent's own Ask.
//   S-113: the restart ends the allowance; the version approval persists
//        (that half is D10#30 in the consent table — the card after a
//        restart is a permission card, never a trust card).
//   C-24/C-26 audit: a timed allowance writes one audit entry (agent,
//        subject, duration, end time, actor); no stored permission change,
//        so no `sandbox.skill_policies` entry.
//
// The decision legs (which card a load opens, the unattended skip text) are
// the consent table's D10 rows; this test covers the ALLOWANCE STORE's
// lifecycle reaching an unattended run. The store does not exist — the
// seams below are it; GREEN wires them to the real store and audit. Nil
// keeps the RED run loud (BLOCKED), never a skip.
import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimedAllowance_ReachesScheduledRun(t *testing.T) {
	if grantTimedAllowance == nil || allowanceActiveFor == nil || restartAllowanceStore == nil ||
		storedGlobalSkillPolicyOf == nil || readAllowanceAudit == nil {
		t.Fatal("BLOCKED: the C-26 timed-allowance store (in memory, per agent, ends at its " +
			"time or on restart) is not implemented — required by S-109, S-113 and C-26 " +
			"points 2, 3 and 5")
	}
	home := t.TempDir()

	t.Run("S-109: before the answer the allowance store is empty", func(t *testing.T) {
		require.False(t, allowanceActiveFor(t, home, "mia", "weather", time.Now()),
			"S-109: no allowance exists before the person answers")
	})

	miaExpiry := grantTimedAllowance(t, home, "mia", "weather", "1d")

	t.Run("S-109: the answer records the allowance for Mia with its audit entry and no policy write", func(t *testing.T) {
		require.True(t, miaExpiry.After(time.Now()), "C-26: the grant reports an end time in the future")
		allowances, policyEntries := readAllowanceAudit(t, home)
		if allowances != 1 {
			t.Fatalf("C-24/C-26: exactly one allowance audit entry expected, got %d", allowances)
		}
		if policyEntries != 0 {
			t.Fatalf("S-109: a card must write no sandbox.skill_policies audit entry, got %d", policyEntries)
		}
		if got := storedGlobalSkillPolicyOf(t, home, "weather"); got != "ask" {
			t.Fatalf("S-109: the global permission of weather must still be Ask, got %q", got)
		}
	})

	t.Run("S-109: Mia's scheduled run is covered until the day ends", func(t *testing.T) {
		require.True(t, allowanceActiveFor(t, home, "mia", "weather", time.Now()),
			"S-109: Mia's task loads weather with no card while the allowance runs")
	})

	t.Run("C-26 point 2: the allowance is bound to Mia — Ray's task still skips", func(t *testing.T) {
		require.False(t, allowanceActiveFor(t, home, "ray", "weather", time.Now()),
			"C-26 point 2: other agents are unaffected")
	})

	t.Run("S-109: after the day Mia's task skips again", func(t *testing.T) {
		require.False(t, allowanceActiveFor(t, home, "mia", "weather", miaExpiry.Add(time.Minute)),
			"S-109: the allowance ends at its time")
	})

	t.Run("S-113 C-26 point 3: a restart ends the allowance before its time", func(t *testing.T) {
		home2 := t.TempDir()
		grantTimedAllowance(t, home2, "mia", "weather", "1d")
		require.True(t, allowanceActiveFor(t, home2, "mia", "weather", time.Now()))
		restartAllowanceStore(t, home2)
		require.False(t, allowanceActiveFor(t, home2, "mia", "weather", time.Now()),
			"S-113: the restart ended the allowance")
	})

	t.Run("C-26 point 5: the allowance works the same way under the agent's own Ask", func(t *testing.T) {
		home3 := t.TempDir()
		grantTimedAllowance(t, home3, "mia", "weather", "3h")
		require.True(t, allowanceActiveFor(t, home3, "mia", "weather", time.Now()),
			"C-26 point 5: a time choice settles the agent's own Ask the same way")
		require.False(t, allowanceActiveFor(t, home3, "ray", "weather", time.Now()))
	})
}

// The seams GREEN wires to the real allowance store and its audit. Declared
// after the test so the RED failure above names the missing behaviour first.
var (
	grantTimedAllowance       func(t testing.TB, home, agentID, subject, duration string) (expiresAt time.Time)
	allowanceActiveFor        func(t testing.TB, home, agentID, subject string, at time.Time) bool
	restartAllowanceStore     func(t testing.TB, home string)
	storedGlobalSkillPolicyOf func(t testing.TB, home, key string) string
	readAllowanceAudit        func(t testing.TB, home string) (allowanceEntries, policyEntries int)
)

//go:build goolm && stdjson

package security

// Order 57 — TestApprovalGrantStore_AgentScopedTimed (S-120, S-122; C-26
// points 2 and 3; Dataset D17 rows 12 and 13). Oracle:
// skills-tools-redesign-spec.md, C-26 "Lifetime and scope":
//   Key: (agent id, kind, subject); the session id is no longer part of the
//        key. Subject: the tool name for an ordinary tool card; the command
//        or command family, the path with its access, or the address set
//        for the shell cards.
//   Covers: every session of that agent — its chats in every workspace,
//        group-chat steps, channel conversations, task, plan and scheduled
//        runs, and helpers it starts for itself. A helper of another agent,
//        or a delegation to another agent, is not covered (S-120).
//   Length: 1h, 3h or 1d (24 hours) on the server's monotonic clock. A
//        second answer for the same subject: the later end time wins; an
//        answer never shortens a running allowance (D17 row 12).
//   End: at the end time, at a gateway stop or restart, or when the agent
//        is deleted (S-122, ClearAgent at pkg/gateway/rest_agents.go::
//        deleteAgent).
//   Storage: none — no file, no config key, no agent field (S-122's
//        checksums of config.json, every agent file and the system folder).
//   Ordinary card scope: the whole tool, whatever the arguments (founder
//        Q4 B; D17 row 13). Narrow subjects stay exactly today's narrow
//        things: one shell command or command family (ShellPrefixGrant:
//        resolved program, argument prefix, foreground or background), one
//        path with its access, the named addresses, or the usual web and
//        name-lookup ports.
//
// The C-26 store does not exist: ApprovalGrantStore is still session-keyed
// with no end times and no ClearAgent (pkg/security/approvalgrants.go).
// GREEN re-keys it per C-26 and wires the seams below onto the re-keyed
// store. Nil keeps the RED run loud (BLOCKED), never a skip.
//
// RED today: every store leg is BLOCKED at its seam. The one control leg
// pins the in-memory, fail-safe contract C-26 keeps (a fresh store holds
// nothing; a nil store asks) on today's real methods — if GREEN's re-key
// reshapes those method signatures, this leg is reconciled with the store,
// never deleted.
import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"

	"github.com/stretchr/testify/require"
)

// timedSubject describes the subject half of a C-26 allowance key for the
// seam record call: which kind of card answered, and that card's subject.
type timedSubject struct {
	Kind   string // "tool" | "shell_exact" | "shell_family" | "path" | "network_hosts" | "network_ports"
	Tool   string
	Args   map[string]any     // kind tool: the answered call's arguments (recorded, never matched)
	Family ShellPrefixGrant   // kind shell_family
	Path   fspolicy.PathGrant // kind path
	Hosts  []string           // kind network_hosts
}

// timedCall describes the call an allowance is looked up against: what the
// agent is about to run right now.
type timedCall struct {
	Binary          string         // shell kinds: the resolved program
	Args            []string       // shell kinds: the argument words
	RunInBackground bool           // shell kinds: the D4 background dimension
	ToolArgs        map[string]any // kind tool: the new call's arguments
}

// The seams GREEN wires to the re-keyed C-26 store. recordTimedAllowance
// records one allowance for agentID and returns its end time (and, for the
// network kinds, the live egress token it pushed). allowanceCoversAt looks
// a new call up at an arbitrary moment on the controlled test clock — the
// monotonic-clock measurement of C-26 without waiting. clearAgentAllowances
// is the agent-deleted end. (The end-time egress-token revocation is order
// 59's seam in shell_timed_allowances_narrow_subjects_red_test.go, next to
// its row.) Declared after the test so
// the RED failure names the missing behaviour first.
var (
	recordTimedAllowance func(t testing.TB, s *ApprovalGrantStore, agentID string, subj timedSubject, duration string, now time.Time) (end time.Time, token string)
	allowanceCoversAt    func(t testing.TB, s *ApprovalGrantStore, agentID string, subj timedSubject, call timedCall, at time.Time) bool
	clearAgentAllowances func(t testing.TB, s *ApprovalGrantStore, agentID string)
)

func TestApprovalGrantStore_AgentScopedTimed(t *testing.T) {
	t.Run("control: the store stays in-memory and fail-safe", func(t *testing.T) {
		// C-26 point 3 keeps the fail-safe consent boundary: a nil store and a
		// fresh store both ask (never approve). Today's real methods; GREEN's
		// re-key must keep the contract, reshaping only the key.
		var nilStore *ApprovalGrantStore
		require.False(t, nilStore.IsAllowed("sess", "mia", "delete_task", map[string]any{"id": "t1"}),
			"C-26: a nil store asks (fail-safe consent boundary kept)")
		require.False(t, NewApprovalGrantStore().IsAllowed("sess", "mia", "delete_task", map[string]any{"id": "t1"}),
			"C-26: a fresh store holds nothing — every call asks")
	})

	t.Run("C-26 key: the allowance belongs to the agent, not the chat (S-120)", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil {
			t.Fatal("BLOCKED: the C-26 timed, agent-keyed allowance store is not implemented — " +
				"required by S-120 and C-26 point 2 (the key is (agent id, kind, subject); " +
				"the session id is no longer part of the key)")
		}
		s := NewApprovalGrantStore()
		argsA := map[string]any{"task_id": "t1"}
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task", Args: argsA}, "3h", time.Now())

		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{ToolArgs: argsA}, time.Now()),
			"S-120: Mia's call in a SECOND chat runs with no card — the key is the agent")
		require.False(t, allowanceCoversAt(t, s, "jim", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{ToolArgs: argsA}, time.Now()),
			"S-120: Jim is unaffected and asks")
	})

	t.Run("D17#13: an ordinary card covers the whole tool, any arguments", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil {
			t.Fatal("BLOCKED: the C-26 store is not implemented — required by D17 row 13 " +
				"(founder Q4 B: an ordinary card's time choice covers the whole tool)")
		}
		s := NewApprovalGrantStore()
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task",
			Args: map[string]any{"task_id": "t1"}}, "1h", time.Now())

		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{ToolArgs: map[string]any{"task_id": "t2"}}, time.Now()),
			"D17#13: the next call with different arguments B runs with no card — the subject is the tool name, never the arguments")
	})

	t.Run("C-26: the narrow subjects stay narrow (D17 rows 1–7)", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil {
			t.Fatal("BLOCKED: the C-26 store is not implemented — required by S-121/D17 " +
				"rows 1–7 (the narrow cards keep today's narrow subject, now per agent and timed)")
		}
		s := NewApprovalGrantStore()
		now := time.Now()

		// Row 1: the exact command — the same command in another chat runs.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "shell_exact", Tool: "bash",
			Args: map[string]any{"command": "npm run test"}}, "1h", now)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_exact"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test"}}, now.Add(time.Minute)),
			"D17#1: the exact command runs in another chat with no card")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_exact"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "build"}}, now.Add(time.Minute)),
			"D17#1: another exact command still asks — the subject stays the exact command")

		// Rows 2–3: the command family covers its prefix, not past the token
		// boundary; the background dimension stays separate.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "shell_family", Tool: "bash",
			Family: ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}}, "1h", now)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_family"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test", "--", "--watch"}}, now.Add(time.Minute)),
			"D17#2: the command family runs — ShellPrefixGrant keeps its argument prefix")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_family"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "testfoo"}}, now.Add(time.Minute)),
			"D17#3: past the token boundary the card appears again")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_family"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test"}, RunInBackground: true}, now.Add(time.Minute)),
			"C-26: the family keeps today's foreground/background dimension (a foreground grant does not cover run_in_background)")

		// Rows 4–5: one path with its access.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "path", Tool: "bash",
			Path: fspolicy.PathGrant{Path: "/home/p/Desktop/out.pdf", Access: fspolicy.PathGrantAccessWrite}}, "1h", now)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "path"},
			timedCall{}, now.Add(time.Minute)),
			"D17#4: writing the same path in another chat runs with no card")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "path", Path: fspolicy.PathGrant{Path: "/home/p/Desktop/other.pdf", Access: fspolicy.PathGrantAccessWrite}},
			timedCall{}, now.Add(time.Minute)),
			"D17#5: another path asks — the subject is that one path with its access")

		// Row 6: the named addresses only.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "network_hosts", Tool: "bash",
			Hosts: []string{"example.com"}}, "1h", now)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "network_hosts", Hosts: []string{"example.com"}},
			timedCall{}, now.Add(time.Minute)),
			"D17#6: curl https://example.com runs with no card")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "network_hosts", Hosts: []string{"example.org"}},
			timedCall{}, now.Add(time.Minute)),
			"D17#6: curl https://example.org asks — exactly the named addresses")

		// Row 7: the no-address card — the usual ports to any address.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "network_ports", Tool: "bash"}, "1h", now)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "network_ports"},
			timedCall{}, now.Add(time.Minute)),
			"D17#7: the ports grant covers ports 80, 443 and 53 to any address for Mia until the end")
	})

	t.Run("C-26 length: the three end times, and the later end wins (D17#12)", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil {
			t.Fatal("BLOCKED: the C-26 store is not implemented — required by C-26 Length " +
				"(1h, 3h, 1d on the monotonic clock) and D17 row 12 (the later end wins)")
		}
		s := NewApprovalGrantStore()
		start := time.Now()

		end1h, _ := recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "1h", start)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, start.Add(time.Hour-time.Second)),
			"C-26: one second before its end the 1h allowance still runs")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, end1h),
			"C-26: at its end time the allowance has ended — the next call asks")

		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "3h", start)
		end3h := start.Add(3 * time.Hour)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, end3h.Add(-time.Second)), "C-26: the 3h allowance runs until its end")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, end3h), "C-26: the 3h allowance ends at three hours")

		end1d, _ := recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "1d", start)
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, start.Add(24*time.Hour-time.Second)), "C-26: the 1d allowance runs until its end")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, end1d), "C-26: the 1d allowance ends at 24 hours")

		// D17#12: a 1h answer after a running 3h allowance never shortens it.
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "3h", start)
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "1h", start.Add(30*time.Minute))
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, start.Add(2*time.Hour)),
			"D17#12: the end stays at 3 hours — an answer never shortens a running allowance")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"},
			timedCall{}, start.Add(3*time.Hour)),
			"D17#12: the 3h end is the one that holds")
	})

	t.Run("S-122: a restart ends every allowance and nothing is written", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil || clearAgentAllowances == nil {
			t.Fatal("BLOCKED: the C-26 store is not implemented — required by S-122 (the " +
				"restart ends every allowance; no storage object)")
		}
		home := t.TempDir()
		writeHome := func(rel, body string) {
			t.Helper()
			path := filepath.Join(home, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		}
		writeHome("config.json", `{"sandbox":{"tool_policies":{"delete_task":"ask"}}}`)
		writeHome("agents/mia.json", `{"id":"mia"}`)
		writeHome("system/skill-trust-approvals.json", `{"approvals":[]}`)
		digest := func() map[string][32]byte {
			t.Helper()
			out := map[string][32]byte{}
			for _, rel := range []string{"config.json", "agents/mia.json", "system/skill-trust-approvals.json"} {
				raw, err := os.ReadFile(filepath.Join(home, rel))
				require.NoError(t, err)
				out[rel] = sha256.Sum256(raw)
			}
			return out
		}
		before := digest()

		s := NewApprovalGrantStore()
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "1h", time.Now())
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "shell_family", Tool: "bash",
			Family: ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}}, "1h", time.Now())
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "path", Tool: "bash",
			Path: fspolicy.PathGrant{Path: "/home/p/Desktop/out.pdf", Access: fspolicy.PathGrantAccessWrite}}, "1h", time.Now())
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "network_hosts", Tool: "bash",
			Hosts: []string{"example.com"}}, "1h", time.Now())

		require.Equal(t, before, digest(),
			"S-122: recording allowances writes nothing — the checksums of config.json, every agent file and the system folder are unchanged")

		// The restart: a fresh store (in memory only) holds nothing, and the
		// answers after it still wrote nothing.
		s = NewApprovalGrantStore()
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, timedCall{}, time.Now()),
			"S-122: after the restart the tool call asks again")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "shell_family"}, timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test"}}, time.Now()),
			"S-122: after the restart the shell family asks again")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "path"}, timedCall{}, time.Now()),
			"S-122: after the restart the path asks again")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "network_hosts", Hosts: []string{"example.com"}}, timedCall{}, time.Now()),
			"S-122: after the restart the address asks again")
		require.Equal(t, before, digest(),
			"S-122: the restart answers wrote nothing either — no storage object (R2-Q1 point 3)")
	})

	t.Run("C-26: deleting the agent ends its allowances", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil || clearAgentAllowances == nil {
			t.Fatal("BLOCKED: the C-26 store is not implemented — required by C-26 End " +
				"(when the agent is deleted its allowances drop; deleteAgent)")
		}
		s := NewApprovalGrantStore()
		_, _ = recordTimedAllowance(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, "3h", time.Now())
		require.True(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, timedCall{}, time.Now()))

		clearAgentAllowances(t, s, "mia")
		require.False(t, allowanceCoversAt(t, s, "mia", timedSubject{Kind: "tool", Tool: "delete_task"}, timedCall{}, time.Now()),
			"C-26: the deleted agent's allowance is gone")
		require.False(t, allowanceCoversAt(t, s, "jim", timedSubject{Kind: "tool", Tool: "delete_task"}, timedCall{}, time.Now()),
			"C-26: deleting Mia never granted Jim anything")
	})
}

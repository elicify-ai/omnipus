//go:build goolm && stdjson

package security

// Order 59 — TestShellTimedAllowances_NarrowSubjects (S-121; Dataset D17
// rows 1–9, 12 and 13). Oracle: skills-tools-redesign-spec.md —
//   S-121 / C-26 point 2: a timed allowance is bound to the agent. "On the
//        narrow cards it covers exactly the same narrow thing as today, now
//        per agent and timed: one file path (with its access), one network
//        address, or one shell-command family." Every row: Mia, the
//        allowance answered with "Allow for 1 hour" in chat 1, the next
//        call in chat 2 within the hour.
//   D17 rows 1–8: exact command, command family (ShellPrefixGrant), token
//        boundary, one path, named addresses, the ports grant, and another
//        agent (Jim) unaffected.
//   Row 9: at the end, the agent's live egress-proxy tokens are revoked so
//        a running command opens no new connection and is not killed (the
//        kernel-enforcing-sandbox command half is the hands-on rows
//        ST-29/ST-30; this order covers the store halves: the end time and
//        the token revocation).
//   Row 12: the later end wins. Row 13: an ordinary card covers the whole
//        tool, whatever the arguments.
//
// This order drives the REAL store APIs (pkg/security/approvalgrants.go) —
// today's session-keyed, untimed methods. RED is real, not seam-blocked:
// recording under Mia's chat 1 and looking up from her chat 2 misses today,
// and C-26 requires a hit. GREEN's re-key per C-26 makes these pass; the
// timed legs ride order 57's controlled-clock seams plus the token
// revocation seam below (nil keeps the RED run loud, BLOCKED, never a
// skip). GREEN reconciliation note: C-26 gives every Record* a duration and
// an end-time return; these calls pin the subject and cross-session
// semantics, and their call shapes are reconciled with the re-keyed store,
// never deleted.
import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"

	"github.com/stretchr/testify/require"
)

// revokeAgentEgressTokens ends the agent's live egress-proxy tokens (C-26
// "Decision time": at the end Omnipus revokes the agent's live tokens, so a
// running command opens no new connection to an expired address and the
// agent's next command gets a fresh token). GREEN wires it to the re-keyed
// store's token bookkeeping plus sandbox.EgressProxy.RevokeRunToken; nil
// keeps the RED run loud (BLOCKED), never a skip.
var revokeAgentEgressTokens func(t testing.TB, s *ApprovalGrantStore, agentID string) []string

func TestShellTimedAllowances_NarrowSubjects(t *testing.T) {
	// Chat 1 records Mia's answer; chat 2 is another chat of the same agent
	// (another workspace's chat, per the dataset). The C-26 key is the agent,
	// so every chat-2 lookup must hit what chat 1 answered.
	const chat1 = "chat-1-mia"
	const chat2 = "chat-2-mia"
	const mia = "mia"
	const jim = "jim"

	t.Run("D17#1: the exact command follows the agent to another chat", func(t *testing.T) {
		s := NewApprovalGrantStore()
		args := map[string]any{"command": "npm run test"}
		require.True(t, s.Record(chat1, mia, "bash", args),
			"setup: the chat-1 exact answer records a grant")
		require.True(t, s.IsAllowed(chat2, mia, "bash", args),
			"D17#1: npm run test in chat 2 runs with no card — the key is the agent, not the chat")
		require.False(t, s.IsAllowed(chat1, mia, "bash", map[string]any{"command": "npm run build"}),
			"D17#1: another exact command still asks — the exact subject stays exact")
	})

	t.Run("D17#2 and #3: the command family follows the agent; the token boundary holds", func(t *testing.T) {
		s := NewApprovalGrantStore()
		grant := ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}
		require.True(t, s.RecordPrefixGrant(chat1, mia, "bash", grant), "setup: the family answer records")
		require.True(t, s.IsPrefixAllowed(chat2, mia, "bash", "/usr/bin/npm", []string{"run", "test", "--", "--watch"}, false),
			"D17#2: npm run test -- --watch in chat 2 runs with no card — ShellPrefixGrant follows the agent")
		require.False(t, s.IsPrefixAllowed(chat2, jim, "bash", "/usr/bin/npm", []string{"run", "test"}, false),
			"D17#2: Jim asks — other agents are unaffected")
		require.False(t, s.IsPrefixAllowed(chat1, mia, "bash", "/usr/bin/npm", []string{"run", "testfoo"}, false),
			"D17#3: past the token boundary the card appears again (narrowness kept, now timed)")
	})

	t.Run("D17#4 and #5: the path grant follows the agent; another path does not", func(t *testing.T) {
		s := NewApprovalGrantStore()
		require.True(t, s.RecordPathGrant(chat1, mia, fspolicy.PathGrant{
			Path: "/home/p/Desktop/out.pdf", Access: fspolicy.PathGrantAccessWrite,
		}), "setup: the file-access answer records")
		grants := s.PathGrantsFor(chat2, mia)
		require.Len(t, grants, 1,
			"D17#4: writing /home/p/Desktop/out.pdf in chat 2 runs with no card — the path grant follows the agent, got %+v", grants)
		require.Equal(t, "/home/p/Desktop/out.pdf", grants[0].Path, "D17#4: the same path")
		require.Equal(t, fspolicy.PathGrantAccessWrite, grants[0].Access,
			"D17#4: the same access the command needed")
		require.Nil(t, s.PathGrantsFor(chat2, jim), "D17#8: Jim's path card still appears (nothing follows another agent)")
		require.False(t, pathGrantsCover(t, s, chat2, mia, "/home/p/Desktop/other.pdf"),
			"D17#5: another path asks — the subject is that one path")
	})

	t.Run("D17#6: the named address follows the agent; another address does not", func(t *testing.T) {
		s := NewApprovalGrantStore()
		require.True(t, s.RecordNetworkGrantWithHosts(chat1, mia, []string{"example.com"}),
			"setup: the named-address answer records")
		require.Equal(t, []string{"example.com"}, s.NetworkHostsFor(chat2, mia),
			"D17#6: curl https://example.com in chat 2 runs with no card — the named address follows the agent")
		require.Nil(t, s.NetworkHostsFor(chat2, jim), "D17#8: Jim's network card still appears")
		require.False(t, hostsCover(t, s, chat2, mia, "example.org"),
			"D17#6: curl https://example.org asks — exactly the named addresses")
	})

	t.Run("D17#7: the ports grant follows the agent", func(t *testing.T) {
		s := NewApprovalGrantStore()
		require.True(t, s.RecordNetworkGrant(chat1, mia), "setup: the no-address answer records")
		require.True(t, s.HasNetworkGrant(chat2, mia),
			"D17#7: npm install in chat 2 runs — ports 80, 443 and 53 to any address follow the agent until the end")
		require.False(t, s.HasNetworkGrant(chat2, jim), "D17#8: Jim asks")
	})

	t.Run("D17#9: the end revokes the agent's live egress tokens", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil || revokeAgentEgressTokens == nil {
			t.Fatal("BLOCKED: the timed half of the address allowance (C-26 Length and " +
				"Decision time: at the end the agent's live egress tokens are revoked, " +
				"pkg/sandbox/egress_proxy.go::EgressProxy.RevokeRunToken) is not implemented — " +
				"required by D17 row 9. The enforcing-sandbox command half (new connection " +
				"refused, command not killed) is the hands-on rows ST-29/ST-30")
		}
		s := NewApprovalGrantStore()
		start := time.Now()
		_, token := recordTimedAllowance(t, s, mia, timedSubject{Kind: "network_hosts", Tool: "bash",
			Hosts: []string{"example.com"}}, "1h", start)
		require.NotEmpty(t, token, "D17#9: the running allowance carries the agent's live egress token")

		require.True(t, allowanceCoversAt(t, s, mia, timedSubject{Kind: "network_hosts", Hosts: []string{"example.com"}},
			timedCall{}, start.Add(59*time.Minute)),
			"D17#9: within the hour the new connections open")
		revoked := revokeAgentEgressTokens(t, s, mia)
		require.NotEmpty(t, revoked, "D17#9: the end revokes the live token — a running command opens no new connection and is not killed")
		require.False(t, allowanceCoversAt(t, s, mia, timedSubject{Kind: "network_hosts", Hosts: []string{"example.com"}},
			timedCall{}, start.Add(time.Hour)),
			"D17#9: at the end the address asks again (the next command gets a fresh token)")
	})

	t.Run("D17#12: the later end wins", func(t *testing.T) {
		if recordTimedAllowance == nil || allowanceCoversAt == nil {
			t.Fatal("BLOCKED: the timed allowance store is not implemented — required by " +
				"D17 row 12 (a second answer never shortens a running allowance)")
		}
		s := NewApprovalGrantStore()
		start := time.Now()
		_, _ = recordTimedAllowance(t, s, mia, timedSubject{Kind: "shell_family", Tool: "bash",
			Family: ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}}, "3h", start)
		_, _ = recordTimedAllowance(t, s, mia, timedSubject{Kind: "shell_family", Tool: "bash",
			Family: ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}}, "1h", start.Add(30*time.Minute))
		require.True(t, allowanceCoversAt(t, s, mia, timedSubject{Kind: "shell_family"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test"}}, start.Add(2*time.Hour)),
			"D17#12: the family still runs at 2 hours — the end stays at 3 hours")
		require.False(t, allowanceCoversAt(t, s, mia, timedSubject{Kind: "shell_family"},
			timedCall{Binary: "/usr/bin/npm", Args: []string{"run", "test"}}, start.Add(3*time.Hour)),
			"D17#12: the 3h end is the one that holds")
	})

	t.Run("D17#13: an ordinary card covers the whole tool, other arguments", func(t *testing.T) {
		s := NewApprovalGrantStore()
		require.True(t, s.Record(chat1, mia, "delete_task", map[string]any{"task_id": "t1"}),
			"setup: the ordinary card's answer records")
		require.True(t, s.IsAllowed(chat2, mia, "delete_task", map[string]any{"task_id": "t2"}),
			"D17#13: delete_task with arguments B runs with no card — the whole tool (founder Q4 B)")
		require.False(t, s.IsAllowed(chat1, jim, "delete_task", map[string]any{"task_id": "t1"}),
			"D17#8: Jim's card still appears")
	})
}

// pathGrantsCover reports whether the grants a session holds name the path
// (the access matching is ResolveTurnFSPolicy's; the store-level narrowness
// this order pins is the recorded path set itself).
func pathGrantsCover(t *testing.T, s *ApprovalGrantStore, sessionID, agentID, path string) bool {
	t.Helper()
	for _, g := range s.PathGrantsFor(sessionID, agentID) {
		if g.Path == path {
			return true
		}
	}
	return false
}

// hostsCover reports whether the host set a session holds names the host.
func hostsCover(t *testing.T, s *ApprovalGrantStore, sessionID, agentID, host string) bool {
	t.Helper()
	for _, h := range s.NetworkHostsFor(sessionID, agentID) {
		if h == host {
			return true
		}
	}
	return false
}

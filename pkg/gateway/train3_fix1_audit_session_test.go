package gateway

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Gate round 1, security S1: the task/plan refusal's tool.policy.ask.denied
// audit row must name the ACTING session (the concrete transcript the call
// was refused in), not the task history scope agent:<agent>:task:<task>
// (adr-057 session-unification spec, regression dataset row 6).
func l1AskDeniedSessionIDs(t *testing.T, h *l11263Harness, tool string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(h.api.homePath, "system", "audit.jsonl"))
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	var ids []string
	for scan.Scan() {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(scan.Bytes(), &rec))
		if rec["event"] != "tool.policy.ask.denied" {
			continue
		}
		fields, ok := rec["fields"].(map[string]any)
		require.True(t, ok)
		if fields["tool_name"] != tool {
			continue
		}
		id, _ := fields["session_id"].(string)
		ids = append(ids, id)
	}
	require.NoError(t, scan.Err())
	return ids
}

func TestTrain3Fix1_TaskRefusalAuditNamesTheActingSession(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	h.startBoard(t, created)
	final := h.awaitTask(t, created.Id)
	h.requireRefusal(t, final.SessionID, worker)
	require.Equal(t, []string{final.SessionID}, l1AskDeniedSessionIDs(t, h, "write_file"),
		"the audit row names the task run's own session, not the agent:<agent>:task:<task> scope")
}

func TestTrain3Fix1_TaskHelperRefusalAuditNamesTheHelperSession(t *testing.T) {
	gate := newL11263Latch()
	root := l11263Script(l11263Delegate(l11263Leaf), l11263Claim())
	root.steps[1].wait = gate.ch
	leaf := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: root, l11263Leaf: leaf})
	t.Cleanup(gate.release)
	created := h.createTask(t, "")
	h.startBoard(t, created)
	h.awaitObservedWithoutPrompt(t, leaf)
	leafID := l11263DelegateSession(t, root, l11263Leaf)
	h.requireRefusal(t, leafID, leaf)
	h.awaitHelperTerminal(t, leafID)
	gate.release()
	h.awaitTask(t, created.Id)
	require.Equal(t, []string{leafID}, l1AskDeniedSessionIDs(t, h, "write_file"),
		"a helper's refusal is audited against the helper's own session")
}

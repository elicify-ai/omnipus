package gateway

// S106 / I-1: an environment_setup child's stdout and stderr reach the
// session's merged output in the order the child wrote them, and a program
// token is masked in place. The child writes a stdout line, pauses, writes a
// stderr line, then the token on stdout: with one masking writer per stream,
// the stdout writer still held its last 7 bytes ("_LINE\n" and one more) while
// the stderr line landed, so the read output came back with the stderr line
// spliced into the middle of the stdout line. The pause makes that ordering
// deterministic rather than load-dependent (CI run 4's intermittent S106 RED).
import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_SetupOutputKeepsTheChildsWriteOrder(t *testing.T) {
	x := newProgramsSentinelFixture(t, false, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["environment_setup"] = "allow"
		cfg.Agents.List[0].Tools.Builtin.Policies["environment_setup"] = config.ToolPolicyAllow
	})
	loaded := pgOtherToolTurn(t, x, "order-load", "ToolSearch", map[string]any{"names": []string{"environment_setup"}})
	require.Contains(t, loaded, "environment_setup", "real discovery exposes the permitted setup tool")
	command := "printf 'S106_ORDER_OUT_LINE\\n'; sleep 0.3; printf 'S106_ORDER_ERR_LINE\\n' >&2; echo $GH_TOKEN"
	started := pgOtherToolTurn(t, x, "order-start", "environment_setup", map[string]any{
		"action": "run", "purpose": "check the setup child's output order", "command": command,
	})
	var session tools.ExecResponse
	require.NoError(t, json.NewDecoder(strings.NewReader(started)).Decode(&session))
	require.Equal(t, "running", session.Status)
	require.NotEmpty(t, session.SessionID)

	// Poll (status only, never drains) until done; then one read takes the
	// whole merged output.
	require.Eventually(t, func() bool {
		polled := pgOtherToolTurn(t, x, "order-poll-"+time.Now().Format("150405.000000000"), "environment_setup",
			map[string]any{"action": "poll", "session_id": session.SessionID})
		var status tools.ExecResponse
		return json.NewDecoder(strings.NewReader(polled)).Decode(&status) == nil && status.Status == "done"
	}, 20*time.Second, 200*time.Millisecond, "the setup child finishes")
	read := pgOtherToolTurn(t, x, "order-read", "environment_setup", map[string]any{"action": "read", "session_id": session.SessionID})
	var output tools.ExecResponse
	require.NoError(t, json.NewDecoder(strings.NewReader(read)).Decode(&output))
	require.Equal(t, "done", output.Status)
	require.Zero(t, output.ExitCode)
	require.Contains(t, output.Output, "S106_ORDER_OUT_LINE", "instrument: the read returned the whole output, not a drained tail")
	want := "S106_ORDER_OUT_LINE\nS106_ORDER_ERR_LINE\n[FILTERED]"
	require.True(t, strings.Contains(output.Output, want),
		"the merged setup output keeps the child's write order with the token masked in place (content deliberately not printed)")
	pgSinkAssertValueFree(t, []byte(output.Output), "setup read output")
}

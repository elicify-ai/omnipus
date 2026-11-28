package agent

// S106 / I-1 for bash run_in_background: the child's stdout and stderr reach
// the session's merged output in the order the child wrote them, with a
// program token masked in place. The child writes a stdout line, pauses,
// writes a stderr line, then the token on stdout. With one masking writer per
// stream the stdout writer still held its last 7 bytes when the stderr line
// landed, so the stderr line was spliced into the stdout line; the pause makes
// that deterministic rather than load-dependent.
import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramMasking_BackgroundOutputKeepsTheChildsWriteOrder(t *testing.T) {
	f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
	command := "printf 'S106_ORDER_OUT_LINE\\n'; sleep 0.3; printf 'S106_ORDER_ERR_LINE\\n' >&2; echo $GH_TOKEN"
	child := programsStartBackground(t, f, sid, "order-bg-start", command)
	programsCompletion(t, events, sid)
	read, raw := programsBashAction(t, f, sid, "order-bg-read", map[string]any{"action": "read", "session_id": child})
	require.Contains(t, read.Output, "S106_ORDER_OUT_LINE", "instrument: the read returned the child's output")
	require.True(t, strings.Contains(read.Output, "S106_ORDER_OUT_LINE\nS106_ORDER_ERR_LINE\n[FILTERED]"),
		"the merged background output keeps the child's write order with the token masked in place (content deliberately not printed)")
	programsNoTokenWindow(t, programsBackgroundSentinel, raw, "background read in write order")
}

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test1534Shown_EndTurnAtStopCodePointEdges(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		keep             int
		warning          string
	}{
		{"zero", "a😀", "", 0, ""},
		{"one", "😀é", "😀", 1, ""},
		{"at_end", "😀é", "😀é", 3, ""},
		{"ahead_clamps_and_warns", "😀é", "😀é", 4, "stop_checkpoint_ahead"},
		{"invalid_utf8_each_byte_is_one", "a\xff\xfeb€😀TAIL", "a\xff\xfeb€😀", 6, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := wu22NewHarness(t, wu22HealthyProvider())
			readLog := checkpoint1534Log(t)
			id := h.ts.nextRoundMessageID()
			h.ts.setStopShown(tc.text, false)
			require.True(t, checkpoint1534Claim(t, h.ts, checkpoint1534Scope(t, h.ts, id, h.ts.turnID, tc.keep)))
			rx := &agentLoopRunTurnTools{ctx: context.Background(), rr: h.responseStage()}
			cn := &agentLoopRunTurnConductorRunIterations{rc: &agentLoopRunTurnConductor{rx: rx}}
			cn.endTurnAtStop()
			assert.Equal(t, tc.want, rx.finalContent, "§2: a range-loop cut never grows text and counts invalid bytes individually")
			if tc.warning != "" {
				assert.Contains(t, readLog(), tc.warning, "ahead checkpoints must warn, not add text")
				assert.NotContains(t, readLog(), tc.text, "checkpoint warning must not log answer text")
			}
		})
	}
}

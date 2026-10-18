package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// R1's lost-origin fallback, reached through an actual system re-entry, not
// a direct call to a proposed classifier. Remove only disposable fixture
// data AFTER a genuine task run settles. No production source is changed.
func TestL11263B_UnresolvedAncestryNeverPrompts(t *testing.T) {
	for _, evidence := range []string{"task_id_retained", "no_lifecycle_or_task_id"} {
		t.Run(evidence, func(t *testing.T) {
			initial := l11263Script(l11263Claim())
			h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: initial})
			id := h.startRoute(t, "board")
			final := h.awaitTask(t, id)
			h.requireTaskOrigin(t, final.SessionID)
			lifecycle := h.api.agentLoop.GetSessionLifecycleStore()
			require.NoError(t, os.Remove(filepath.Join(lifecycle.Dir(), final.SessionID+".jsonl")))
			_, err := lifecycle.Load(final.SessionID)
			require.ErrorIs(t, err, session.ErrLifecycleNotFound, "prove the origin-loss instrument removed the real record")
			if evidence == "no_lifecycle_or_task_id" {
				empty := ""
				require.NoError(t, h.api.agentLoop.GetSessionStore().SetMeta(final.SessionID, session.MetaPatch{TaskID: &empty}))
			}
			probe := l11263Script(l11263Write())
			inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
			require.True(t, ok)
			inst.Provider = probe
			h.startPump(t)
			h.reenter(t, "system", final.SessionID, l11263Agent)
			h.awaitObservedWithoutPrompt(t, probe)
			h.awaitTurnSettled(t, final.SessionID)
			h.requireNoApprovals(t)
			h.requireRefusal(t, final.SessionID, probe)
		})
	}
}

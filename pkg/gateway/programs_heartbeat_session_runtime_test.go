package gateway

// Spec rev8 S144 / A-1 / I-2 / FR-041: the heartbeat kind of S144 through its
// real producer. The saved workspace heartbeat is turned into a cron job by the
// real reconciler, the real cron lane fires it and the real runner runs the
// member's turn; a command in that turn counts the program token. No origin,
// record or session is written by the test. Oracle (A-1, founder Q1 = A): a
// heartbeat's input is {independent}, which follows the web-chat setting, so the
// count is 16 with the web chat on and 0 with it off.
// The connected identity is Ops (this fixture assigns GitHub to Ops, not Dev).
import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// programsRealHeartbeatCount fires Ops's real workspace heartbeat once and
// returns the counted token length, the command result and the provenance of the
// two model requests the heartbeat turn made.
func programsRealHeartbeatCount(t *testing.T, x *programsTaskFixture, call string) (string, string, []bus.Provenance) {
	t.Helper()
	ws, err := readWorkspaceFile(x.home, "sales")
	require.NoError(t, err)
	ws.MemberConfigs = map[string]workspace.MemberConfig{"ops": {Heartbeat: &workspace.MemberHeartbeat{Enabled: true, IntervalMinutes: 15, Body: "Count the connected token."}}}
	require.NoError(t, workspace.SaveRecord(x.home, ws))
	persisted, err := listWorkspaceFiles(x.home)
	require.NoError(t, err)
	cs := cron.NewCronService(filepath.Join(x.home, "cron", call+".json"))
	t.Cleanup(cs.Stop)
	runner := newScheduledRunner(x.al, agentCheckerFunc(func(id string) bool {
		inst, ok := x.al.GetRegistry().GetAgent(id)
		return ok && inst != nil
	}), x.bus, nil, x.al.GetConfig)
	runner.home = x.home
	cs.SetRunner(runner)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, persisted, configOnlyIsWorker(x.al.GetConfig())))
	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 1, "instrument: the saved workspace heartbeat created exactly one real cron job")
	require.Equal(t, "heartbeat:sales:ops", jobs[0].Name)
	require.Equal(t, "heartbeat", jobs[0].Payload.Kind)
	require.Equal(t, cron.SessionModeMain, jobs[0].SessionMode)
	path := x.scriptCount(x.ops, call)
	// The fixture's own setup already ran a web turn in this session, so only
	// the requests made after this point belong to the heartbeat.
	before := len(x.provider.observedOrigins(x.ops))
	status, sid, err := cs.RunNow(jobs[0].ID)
	require.NoError(t, err, "instrument: the real heartbeat completed, not a setup or admission failure")
	require.Equal(t, "ok", status)
	require.Equal(t, x.ops, sid, "instrument: the real heartbeat ran in Sales/Ops's own session")
	x.waitEnds(t, sid)
	count, result := programsPeerCount(t, x.programsPeerFixture, path, call)
	origins := x.provider.observedOrigins(sid)[before:]
	require.GreaterOrEqual(t, len(origins), 2, "instrument: the heartbeat requested the command and consumed its real result")
	return count, result, origins[:2]
}

func TestProgramProvenance_ActualHeartbeatSession(t *testing.T) {
	for _, tc := range []struct {
		name       string
		webAllowed bool
		count      string
		notice     string
	}{
		{"S144_heartbeat_independent_with_web_on_prints_16", true, "16", ""},
		{"S144_heartbeat_independent_with_web_off_prints_0", false, "0", "Connected programs were not used for this command: programs are off for the web chat. A person can switch them on in Settings → Programs."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t) // Its own setup proves Ops receives the token on trusted web input.
			x.al.GetConfig().Programs.WebChatAllowed = tc.webAllowed
			count, result, origins := programsRealHeartbeatCount(t, x, "s144-heartbeat")
			for _, actual := range origins {
				assert.Equal(t, bus.NewProvenance(bus.OriginIndependent), actual, "S144: the real heartbeat's turn is {independent}, never web or a connector")
			}
			assert.Equal(t, tc.count, count, "S144/A-1: the heartbeat follows the web-chat setting")
			if tc.notice == "" {
				assert.NotContains(t, result, "Connected programs were not used for this command:", "S144/A-1: allowed, so nothing is withheld")
			} else {
				assert.Contains(t, result, tc.notice, "S144/A-1: independent is governed by the web setting, not unresolved")
			}
		})
	}
}

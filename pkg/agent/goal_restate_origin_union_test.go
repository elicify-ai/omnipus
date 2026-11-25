package agent

// Round 13, CHECK finding F2 (/workspace/logs/1960/check-s1-delta3/check-report.md):
// both goal restatements are agent instruction writers of the goal record: the
// prose restate (applyGoalCommandPrompt, LLM-compile branch) and the marker-only
// restate (applyGoalMarkerRestate). Oracle: spec I-2 ("an agent write that
// changes a durable record's instructions joins the writer's provenance into
// the record in the same write", "provenance never shrinks", unresolved
// absorbs) and ruling-check-delta.md S-111 writers ("The goal-definition paths
// (set_goal/restate in pkg/agent/goal_loop_command.go) pass the writer turn's
// provenance"). The keeper-wake observation is goalWakeProvenance, the value
// every goal wake (nudge, continue push, follow-up) stamps on its turn.
// Wrong implementations killed: (a) a restate that does not pass the writer's
// provenance (CHECK mutant m5 for prose, today's code for the marker path), so
// a record trusted for A silently keeps A after a B-origin instruction;
// (b) a restate that REPLACES the record's origins with the writer's.
// The existing business and anchor assertions of the restate tests are left
// as they are in goal_restate_supersede_test.go and goal_record_anchor_test.go;
// these rows assert the same business outcome beside the origin.
import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestGoalRestateProvenance_WriterOriginJoinsTheRecord(t *testing.T) {
	telegram := bus.ConnectorOrigin("telegram")
	for _, restate := range []struct {
		name     string
		command  string
		prompt   string // the working prompt / record prompt the restate must install
		criteria int    // exact criteria count after the restate (business assertion)
	}{
		{"prose_restate", "/goal " + e1BetaIntent, e1BetaIntent, 0},
		{"marker_only_restate", "/goal [search: 2] [tests pass]", "", 2},
	} {
		for _, row := range []struct {
			name       string
			recorded   []string
			writer     bus.Provenance
			wantRecord []string // stored form: sorted union, nil when unresolved
			wantWake   bus.Provenance
		}{
			{"web_goal_restated_from_telegram_stores_the_union", []string{"web"}, bus.NewProvenance(telegram),
				[]string{"connector:telegram", "web"}, bus.NewProvenance(bus.OriginWeb, telegram)},
			{"telegram_goal_restated_from_web_stores_the_union", []string{"connector:telegram"}, bus.NewProvenance(bus.OriginWeb),
				[]string{"connector:telegram", "web"}, bus.NewProvenance(bus.OriginWeb, telegram)},
			{"unresolved_writer_makes_the_record_unresolved", []string{"web"}, bus.UnresolvedProvenance(),
				nil, bus.UnresolvedProvenance()},
			{"instrument_same_origin_keeps_the_record", []string{"web"}, bus.NewProvenance(bus.OriginWeb),
				[]string{"web"}, bus.NewProvenance(bus.OriginWeb)},
		} {
			t.Run(restate.name+"/"+row.name, func(t *testing.T) {
				al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
				agentInst, _ := al.GetRegistry().GetAgent("native-agent")
				agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
					"bash": config.ToolPolicyAllow, "search_web": config.ToolPolicyAllow,
				}})
				store, sid := newGoalTestSession(t, al, agentInst.ID)
				// A standing chat is a ROOT (FR-050): its keeper wake takes the
				// goal record's origins. A record-less session would be lineage
				// with nothing stored, hence unresolved whatever the goal holds.
				rootRec, rerr := al.ensureOrdinaryRootRecord(al.GetSessionLifecycleStore(), sid, store)
				require.NoError(t, rerr)
				require.Equal(t, session.ProvenanceModeRoot, rootRec.ProvenanceMode, "instrument: the session is a root")
				opts := processOptions{
					TranscriptStore: store, TranscriptSessionID: sid,
					Channel: "webchat", ChatID: "c-prov", SessionKey: "sk-prov", UserInitiated: true,
				}
				first := e1AlphaIntent
				if restate.criteria != 0 {
					first = "[tests pass]"
				}
				matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
					bus.InboundMessage{Content: "/goal " + first, UserInitiated: true}, agentInst, &opts)
				require.True(t, matched, "instrument: the first /goal activates")
				require.False(t, handled)
				g := goalRecordForSession(t, sid)

				// Record the goal with origin A, as a registered goal carries it
				// (the goal's first registration seeds the origin set).
				_, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, goal.Write{Mutate: func(cur *goal.Goal) error {
					cur.Provenance = append([]string(nil), row.recorded...)
					return nil
				}})
				require.NoError(t, err)

				// The restating message's origin B is the turn's input origin.
				opts.InputOrigin = inputOrigin{provenance: row.writer}
				matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
					bus.InboundMessage{Content: restate.command, UserInitiated: true}, agentInst, &opts)
				require.True(t, matched)
				if restate.criteria != 0 {
					require.True(t, handled, "instrument: the marker-only restate answers synchronously, reply=%q", reply)
				} else {
					require.False(t, handled, "instrument: the prose restate continues into the turn, reply=%q", reply)
				}

				// Durable reload: a fresh store over the same home, nothing cached.
				after, err := goal.NewStore(config.OmnipusHomeDir()).Get(g.GoalID)
				require.NoError(t, err)
				assert.Equal(t, g.GoalID, after.GoalID, "business: a restate keeps the goal id")
				if restate.prompt != "" {
					assert.Equal(t, restate.prompt, after.Prompt, "business: the restated intent replaces the prompt")
				}
				assert.Len(t, after.Criteria, restate.criteria, "business: restated criteria")
				assert.Equal(t, row.wantRecord, after.Provenance, "I-2/S-111: the restate joins the writer's origins into the record in the same write")

				// Keeper-wake observation (what every goal wake turn is stamped with).
				assert.Equal(t, row.wantWake, al.goalWakeProvenance(sid, g.GoalID),
					"the keeper wake after the restate carries the record's origin union")
			})
		}
	}
}

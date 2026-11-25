package agent

// Round 13, CHECK finding F1 (/workspace/logs/1960/check-s1-delta3/check-report.md):
// the registered update_plan tool (pkg/tools/plan_update.go, wired by
// wirePlanToolsForAgent) is an AGENT instruction writer of a plan. Spec I-2:
// "An agent write that changes a durable record's instructions joins the
// writer's provenance into the record in the same write", and S-111 writers
// (/workspace/logs/1960/spec-s1/ruling-check-delta.md): every agent instruction
// writer passes non-nil Patch.Provenance; join is the union, and `unresolved`
// absorbs (an unresolved writer makes the record unresolved, never a no-op).
// Oracle: those two texts, plus S-144 (a plan owner or supervision turn carries
// the plan's stored provenance) and FR-012 (the notice names the origin).
// Wrong implementation killed: update_plan builds a Patch with nil Provenance
// (today's code), so the stored origin set stays {web} and a Telegram-written
// instruction is later run as trusted web work.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const programsPlanUpdateCall = "s111-plan-update"

// newProgramsPlanUpdateFixture is newProgramsPlanFixture with update_plan
// allowed as well; same real stores, engine and loop pump.
func newProgramsPlanUpdateFixture(t *testing.T) *programsPlanFixture {
	t.Helper()
	f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", ProgramsAllowed: false}}
		cfg.Sandbox.ToolPolicies["create_plan"] = "allow"
		cfg.Sandbox.ToolPolicies["update_plan"] = "allow"
	})
	programsWireMessaging(t, f)
	plans := plan.New(filepath.Join(f.home, "plans"))
	f.al.SetPlanStore(plans)
	tasks := GetTaskStore(f.al)
	require.NotNil(t, tasks)
	pe := NewPlanEngine(f.al, plans, tasks, nil)
	f.al.SetPlanEngine(pe)
	return &programsPlanFixture{wu5Fixture: f, plans: plans, pe: pe}
}

// updatePlanTitle drives the REGISTERED update_plan tool through one real turn
// of the given origin and returns the tool's answer. The title change is an
// instruction field (S-111: title, objective, description, DoD, bounds, owner).
func (x *programsPlanFixture) updatePlanTitle(planID, title string, input bus.InboundMessage) string {
	x.t.Helper()
	sid := x.mainSession("sales", "dev")
	call := providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: programsPlanUpdateCall, Type: "function", Name: "update_plan", Arguments: map[string]any{"plan_id": planID, "title": title}}}}
	x.provider.setScript(sid, []providers.LLMResponse{call, wu5Stop("plan updated")})
	input.SessionID, input.ChatID, input.Content = sid, sid, "rename the plan"
	_, _, err := x.al.processMessage(context.Background(), input)
	require.NoError(x.t, err)
	x.waitForCalls(sid, 2, "the real update_plan call") // setScript restarts the counter
	result := x.provider.resultForCall(sid, programsPlanUpdateCall)
	var out struct {
		PlanID string `json:"plan_id"`
	}
	require.NoError(x.t, json.Unmarshal([]byte(result), &out), "instrument: update_plan answered with its success object, a refusal cannot satisfy the row: %q", result)
	require.Equal(x.t, planID, out.PlanID)
	return result
}

func TestPlanUpdateProvenance_AgentInstructionWriterJoinsTheWritersOrigin(t *testing.T) {
	webAndTelegram := bus.NewProvenance(bus.OriginWeb, bus.ConnectorOrigin("telegram"))

	t.Run("telegram_turn_updating_a_web_plan_stores_the_union_and_owner_and_supervision_reads_see_it", func(t *testing.T) {
		x := newProgramsPlanUpdateFixture(t)
		planID := x.createPlan(programsWebInput())
		created, err := x.plans.Get(planID)
		require.NoError(t, err)
		require.Equal(t, []string{"web"}, created.Provenance, "instrument: the plan starts as web only")

		x.updatePlanTitle(planID, "Renamed from Telegram", programsConnectorInput())
		stored, err := x.plans.Get(planID)
		require.NoError(t, err)
		require.Equal(t, "Renamed from Telegram", stored.Title, "instrument: the instruction change itself was applied")
		// join is the sorted union (task.JoinOrigins): "connector:telegram" < "web".
		assert.Equal(t, []string{"connector:telegram", "web"}, stored.Provenance,
			"I-2/S-111: the Telegram turn's origin joins the plan in the same write; it neither replaces nor is dropped")

		// Re-drive the owner and supervision session reads (S-144) over the stored plan.
		ownerID, err := x.pe.mintPlanSession(testPlanOwnerAgentID, "Plan: owner")
		require.NoError(t, err)
		supID, err := x.pe.mintPlanSession(testPlanOwnerAgentID, "Plan supervision")
		require.NoError(t, err)
		_, err = x.plans.Update(planID, plan.Patch{OwnerSessionID: &ownerID, SupervisionSessionID: &supID})
		require.NoError(t, err)
		for name, sid := range map[string]string{"owner": ownerID, "supervision": supID} {
			lineage, isLineage := x.al.sessionLineageProvenance(sid, "")
			require.True(t, isLineage, "%s session of a plan is lineage (FR-050 row 4)", name)
			assert.Equal(t, webAndTelegram, lineage, "%s session lineage sees the Telegram origin", name)
			assert.Equal(t, webAndTelegram, x.al.sessionInstructionOrigin(sid).provenance, "%s turn's instruction origin sees the Telegram origin", name)
		}
	})

	t.Run("owner_wake_after_a_telegram_update_of_a_web_plan_prints_0_with_the_telegram_notice", func(t *testing.T) {
		x := newProgramsPlanUpdateFixture(t)
		planID := x.createPlan(programsWebInput())
		x.updatePlanTitle(planID, "Renamed from Telegram", programsConnectorInput())

		count, result := x.ownerCount(planID)
		assert.Equal(t, "0", count, "S144/I-2: the owner of a plan whose instructions Telegram changed prints 0 (web alone would print 16)")
		assert.Contains(t, result, programsTelegramNotice, "FR-012: the notice names Telegram, not a trusted web origin")
	})

	t.Run("unresolved_writer_turn_makes_the_plan_unresolved_because_unresolved_absorbs", func(t *testing.T) {
		x := newProgramsPlanUpdateFixture(t)
		planID := x.createPlan(programsWebInput())
		unresolvedTurn := bus.InboundMessage{Channel: "webchat"} // zero Provenance is unresolved
		x.updatePlanTitle(planID, "Renamed by an unconfirmed turn", unresolvedTurn)
		stored, err := x.plans.Get(planID)
		require.NoError(t, err)
		require.Equal(t, "Renamed by an unconfirmed turn", stored.Title, "instrument: the instruction change itself was applied")
		assert.Empty(t, stored.Provenance, "I-2: join with unresolved is unresolved; the record must not keep {web}")

		count, result := x.ownerCount(planID)
		assert.Equal(t, "0", count)
		assert.Contains(t, result, programsUnresolvedNotice)
	})

	t.Run("instrument_same_origin_update_keeps_the_plan_web_and_the_owner_prints_16", func(t *testing.T) {
		x := newProgramsPlanUpdateFixture(t)
		planID := x.createPlan(programsWebInput())
		x.updatePlanTitle(planID, "Renamed from web", programsWebInput())
		stored, err := x.plans.Get(planID)
		require.NoError(t, err)
		assert.Equal(t, []string{"web"}, stored.Provenance, "joining the same origin adds nothing")

		count, result := x.ownerCount(planID)
		assert.Equal(t, "16", count, "monotone restriction only: a web-only plan stays eligible")
		assert.NotContains(t, result, "Connected programs were not used for this command:")
	})
}

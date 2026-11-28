package agent

// Spec rev8 S144 / FR-050 classification row 4 / I-2, handoff "Task, plan,
// schedule, goal created by a tool call | the record, at creation | every run
// joins it", dataset row 31: the plan owner kind of S144 through its real
// producers, not a seeded record. A turn of Dev calls the real create_plan tool
// (a Telegram-started turn, and the same producer from a web turn as the
// contrast); a person approves the draft (the same plan.Store write the REST
// approve makes) and stops it through PlanEngine.StopPlan (the engine entry the
// REST stop and the stop_plan tool use); the engine wakes the plan's owner over
// the plan's origin chat, and the owner's turn counts the program token. The
// test writes no origin, record or provenance of its own.
// Oracle: the S-144 Then clause (the plan owner of a Telegram-created plan is
// lineage and prints 0) and dataset row 31 (withheld). The web-created plan is
// the instrument that separates "carries the plan's provenance" from "always
// withheld": the same producer from a trusted web turn prints 16, web being on.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	programsPlanCreateCall = "s144-plan-create"
	programsPlanCountCall  = "s144-plan-owner-count"
)

// programsPlanFixture is the programs fixture with the plan surface the gateway
// boot installs: the plan store, which re-wires Dev's create_plan, and the plan
// engine, whose owner wake is a bus message the loop pump turns into a turn.
type programsPlanFixture struct {
	*wu5Fixture
	plans *plan.Store
	pe    *PlanEngine
}

func newProgramsPlanFixture(t *testing.T) *programsPlanFixture {
	t.Helper()
	f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", ProgramsAllowed: false}}
		cfg.Sandbox.ToolPolicies["create_plan"] = "allow"
	})
	programsWireMessaging(t, f)
	plans := plan.New(filepath.Join(f.home, "plans"))
	f.al.SetPlanStore(plans)
	tasks := GetTaskStore(f.al)
	require.NotNil(t, tasks, "instrument: the loop's task store the plan engine lists members from")
	pe := NewPlanEngine(f.al, plans, tasks, nil)
	f.al.SetPlanEngine(pe)
	return &programsPlanFixture{wu5Fixture: f, plans: plans, pe: pe}
}

// startPump runs the loop's inbound pump, the part of the real wake path that
// turns the engine's published wake into the owner's turn. It starts only after
// the owner's script is installed, so the first model call is never unscripted;
// the wake waits on the bus until then.
func (x *programsPlanFixture) startPump() {
	x.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- x.al.Run(ctx) }()
	x.t.Cleanup(func() {
		x.pe.wakeWG.Wait()
		cancel()
		assert.NoError(x.t, <-runErr, "the loop pump ends cleanly")
		x.al.WaitForActiveRequests()
	})
}

// createPlan is Dev's real create_plan call in one turn of the given origin; it
// returns the id of the plan the tool stored.
func (x *programsPlanFixture) createPlan(input bus.InboundMessage) string {
	x.t.Helper()
	sid := x.mainSession("sales", "dev")
	args := map[string]any{
		"title": "S144 plan owner probe", "owner_agent_id": "dev", "workspace_id": "sales",
		"dod":       []any{map[string]any{"kind": "prose", "text": "the probe is stopped by a person"}},
		"rationale": "S144 needs a plan whose owner session a real wake reaches",
	}
	call := providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: programsPlanCreateCall, Type: "function", Name: "create_plan", Arguments: args}}}
	x.provider.setScript(sid, []providers.LLMResponse{call, wu5Stop("plan created")})
	input.SessionID, input.ChatID, input.Content = sid, sid, "make a plan for the probe"
	_, _, err := x.al.processMessage(context.Background(), input)
	require.NoError(x.t, err)
	x.waitForCalls(sid, 2, "the real create_plan call")
	result := x.provider.resultForCall(sid, programsPlanCreateCall)
	var created struct {
		PlanID string `json:"plan_id"`
	}
	require.NoError(x.t, json.Unmarshal([]byte(result), &created), "instrument: create_plan answered with a plan id, a refusal cannot satisfy the row: %q", result)
	require.NotEmpty(x.t, created.PlanID)
	return created.PlanID
}

// ownerCount approves the draft plan and stops it as a person, lets the engine's
// wake reach the owner's turn, and returns the owner's command output: the
// program-token count and the tool result the model read.
func (x *programsPlanFixture) ownerCount(planID string) (count, result string) {
	x.t.Helper()
	approved := plan.StateApproved
	var personApproval *task.Initiator
	_, err := x.plans.Update(planID, plan.Patch{State: &approved, InitiatedBy: &personApproval})
	require.NoError(x.t, err, "a person's approval of the draft")
	_, err = x.pe.StopPlan(context.Background(), planID, "s1-person", "system")
	require.NoError(x.t, err)
	stopped, err := x.plans.Get(planID)
	require.NoError(x.t, err)
	owner := stopped.OwnerSessionID
	require.NotEmpty(x.t, owner, "instrument: the engine opened the owner session its wake runs in")

	path := filepath.Join(x.home, "workspaces", "sales", "work", "s144-plan-owner-count")
	require.NoError(x.t, os.MkdirAll(filepath.Dir(path), 0o700))
	command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROVENANCE_CHILD_RAN'", path)
	x.provider.setScript(owner, []providers.LLMResponse{wu5BashCall(programsPlanCountCall, command), wu5Stop("owner probe completed")})
	x.startPump()
	x.waitForCalls(owner, 2, "the plan owner's actual command")
	result = x.provider.resultForCall(owner, programsPlanCountCall)
	require.Contains(x.t, result, "PROVENANCE_CHILD_RAN", "instrument: the owner's command ran; a refusal cannot establish zero")
	raw, err := os.ReadFile(path)
	require.NoError(x.t, err)
	return strings.TrimSpace(string(raw)), result
}

func TestProgramProvenance_ActualPlanOwnerSession(t *testing.T) {
	t.Run("S144_plan_owner_session_of_telegram_created_plan_is_withheld_and_prints_0", func(t *testing.T) {
		x := newProgramsPlanFixture(t)
		control, _ := programsCountInput(t, x.wu5Fixture, x.mainSession("sales", "dev"), "s144-plan-control", programsWebInput())
		require.Equal(t, "16", control, "instrument: this fixture's actual connected identity receives the token on trusted web input")

		planID := x.createPlan(programsConnectorInput())
		stored, err := x.plans.Get(planID)
		require.NoError(t, err)
		assert.Equal(t, []string{"connector:telegram"}, stored.Provenance, "handoff table: the plan record stores its creating turn's provenance at creation")

		count, result := x.ownerCount(planID)
		assert.Equal(t, "0", count, "S144: the plan owner session of a Telegram-created plan prints 0")
		assert.Contains(t, result, programsTelegramNotice, "S144/I-2: the owner's turn carries the plan's Telegram origin, so the notice names Telegram; unresolved is not the specified outcome")
	})

	t.Run("S144_instrument_plan_owner_session_of_web_created_plan_prints_16", func(t *testing.T) {
		x := newProgramsPlanFixture(t)
		planID := x.createPlan(programsWebInput())
		stored, err := x.plans.Get(planID)
		require.NoError(t, err)
		require.Equal(t, []string{"web"}, stored.Provenance, "handoff table: the plan record stores its creating turn's provenance at creation")

		count, result := x.ownerCount(planID)
		assert.Equal(t, "16", count, "I-2: the owner of a plan created from a trusted web turn joins that web origin, and web chat is on")
		assert.NotContains(t, result, "Connected programs were not used for this command:", "a web-origin owner turn is not told programs were withheld")
	})
}

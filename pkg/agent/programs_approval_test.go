package agent

// FR-007/S14: the REAL loop must hold the Programs write until approval,
// preserve the whole record on refusal, and persist only programs on approval.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/require"
)

type programsApprovalBarrier struct {
	entered  chan PolicyApprovalReq
	decision chan bool
}

func (b *programsApprovalBarrier) RequestApproval(ctx context.Context, req PolicyApprovalReq) (bool, string, bool) {
	select {
	case b.entered <- req:
	case <-ctx.Done():
		return false, "cancelled", false
	}
	select {
	case approved := <-b.decision:
		return approved, "person", false
	case <-ctx.Done():
		return false, "cancelled", false
	}
}

func TestAgentPrograms_RealLoopApproval(t *testing.T) {
	for _, row := range []struct {
		name     string
		approved bool
	}{{"S14_rejected_write_changes_nothing", false}, {"S14_approved_write_after_hold", true}} {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
			cfg.Agents.Defaults.MaxTokens = 4096
			cfg.Agents.Defaults.MaxToolIterations = 10
			cfg.Agents.List = nil
			require.True(t, coreagent.SeedConfig(cfg))
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Home = filepath.Join(home, "agents", cfg.Agents.List[i].ID)
			}
			target := config.AgentConfig{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
			cfg.Agents.List = append(cfg.Agents.List, target)
			store := agentstore.New(home)
			_, err := store.CreateState("dev", &target, "Dev soul")
			require.NoError(t, err)
			before, err := store.ReadState("dev")
			require.NoError(t, err)
			args, err := json.Marshal(map[string]any{"id": "dev", "revision": before.Revision, "programs": []string{"github"}})
			require.NoError(t, err)
			provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["set_agent_permissions"]}`).WithToolCall("set_agent_permissions", string(args)).WithText("permission change checked")
			loop := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
			t.Cleanup(loop.Close)
			t8WireRealAgentTools(t, loop, home)
			barrier := &programsApprovalBarrier{make(chan PolicyApprovalReq, 1), make(chan bool, 1)}
			loop.SetToolApprover(barrier)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, turnErr := loop.ProcessDirectAs(ctx, "omnipus", "switch GitHub on for Dev", "s14-programs")
				done <- turnErr
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("approval turn did not join")
				}
			})
			select {
			case req := <-barrier.entered:
				require.Equal(t, "set_agent_permissions", req.ToolName)
				require.Equal(t, "omnipus", req.AgentID)
			case endedErr := <-done:
				done <- endedErr
				t.Fatalf("approval was never requested (turn ended: %v)", endedErr)
			case <-time.After(5 * time.Second):
				t.Fatal("real Programs write did not reach approval")
			}
			held, err := store.ReadState("dev")
			require.NoError(t, err)
			require.Equal(t, before, held, "no write while real approval is held")
			barrier.decision <- row.approved
			select {
			case endedErr := <-done:
				done <- endedErr
				require.NoError(t, endedErr)
			case <-time.After(5 * time.Second):
				t.Fatal("approved/rejected turn did not finish")
			}
			after, err := store.ReadState("dev")
			require.NoError(t, err)
			if !row.approved {
				require.Equal(t, before, after, "refusal preserves every persisted field")
				return
			}
			require.Equal(t, []string{"github"}, after.Agent.Programs)
			after.Agent.Programs = before.Agent.Programs
			require.Equal(t, before.Agent, after.Agent, "approval changes no other agent setting")
		})
	}
}

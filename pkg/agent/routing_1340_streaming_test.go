package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

type routing1340StreamClient struct {
	chatModels   []string
	streamModels []string
}

func (p *routing1340StreamClient) GetDefaultModel() string { return "selected" }
func (p *routing1340StreamClient) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.chatModels = append(p.chatModels, model)
	return &providers.LLMResponse{Content: "selected-pool-answer"}, nil
}
func (p *routing1340StreamClient) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	p.streamModels = append(p.streamModels, model)
	chunk("selected-pool-answer")
	return &providers.LLMResponse{Content: "selected-pool-answer"}, nil
}

// Selection must not trade away streaming, the light tier, or a model
// override applied after selection (the existing BeforeLLM hook boundary).
func TestRouting1340_SelectedPoolKeepsStreamingAndModelOverride(t *testing.T) {
	for _, light := range []bool{false, true} {
		for _, chain := range []bool{false, true} {
			name := "primary"
			if light {
				name = "light"
			}
			if chain {
				name += "_chain"
			} else {
				name += "_plain"
			}
			t.Run(name, func(t *testing.T) {
				global := &routing1340DefaultClient{}
				borrowedLight := &routing1340DefaultClient{}
				selected := &routing1340StreamClient{}
				a := &AgentInstance{ID: "routing1340", Provider: global, Model: "selected", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "selected"}}}
				if light {
					a.Router = routing.New(routing.RouterConfig{LightModel: "light-selected", Threshold: 0.99})
					a.LightCandidates = []providers.FallbackCandidate{{Provider: "openrouter", Model: "light-selected"}}
					a.LightProvider = borrowedLight
				}
				a.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": selected})
				msgBus := bus.NewMessageBus()
				streamer := &identityStampRecordingStreamer{}
				msgBus.SetStreamDelegate(&msgIDStreamDelegate{streamer: streamer})
				al := &AgentLoop{bus: msgBus}
				if chain {
					al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker())
				}
				ts := newTurnState(a, processOptions{NoHistory: true}, turnEventScope{})
				ts.userMessage = "hi"
				ts.channel = "webchat"
				rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
				ri := &agentLoopRunTurnIteration{rf: &agentLoopRunTurnFallbacks{rt: rt}, messages: []providers.Message{{Role: "user", Content: "hi"}}}
				rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: ri}}}}}
				if rp.selectTurnProvider() != agentLoopRunTurnConductorNext {
					t.Fatal("selection did not continue")
				}
				wantModel := "selected"
				if light {
					wantModel = "light-selected"
				}
				if ri.activeModel != wantModel {
					t.Fatalf("selected model=%q want%q", ri.activeModel, wantModel)
				}
				rt.llmModel = "hook-overridden"
				resp, err := rt.callProviderOnce(ri.messages, nil)
				if err != nil || resp == nil || resp.Content != "selected-pool-answer" {
					t.Fatalf("response=%+v err=%v", resp, err)
				}
				if !reflect.DeepEqual(selected.streamModels, []string{"hook-overridden"}) || len(selected.chatModels) != 0 || global.calls != 0 || borrowedLight.calls != 0 {
					t.Fatalf("selected stream=%v chat=%v global=%d borrowedLight=%d", selected.streamModels, selected.chatModels, global.calls, borrowedLight.calls)
				}
				if !reflect.DeepEqual(streamer.updates, []string{"selected-pool-answer"}) {
					t.Fatalf("streamed chunks=%v", streamer.updates)
				}
			})
		}
	}
}

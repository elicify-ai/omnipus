package agent

// CHECK-05 / S-19/S-48/S-49/S-91: state is established through the real
// conversation command or entity codec; the next actual HTTP request and the
// completed answer are observed, not another standalone mapping call.
import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_R0_S19_CompletedProviderDefaultAndUnchosenRecords(t *testing.T) {
	for _, choice := range []string{"provider_default", ""} {
		t.Run("choice="+choice, func(t *testing.T) {
			installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
			ep, url := newRejectionEndpoint(t, 0, 0)
			al, store := fullEffortRequestLoop(t, url, choice, nil)
			answer, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("answer once"))
			require.NoError(t, err)
			require.Equal(t, "ok", answer)
			bodies := ep.snapshot()
			require.Len(t, bodies, 1)
			for _, key := range []string{"reasoning_effort", "reasoning", "thinking", "output_config"} {
				assert.NotContains(t, bodies[0], key)
			}
			saved := fullEffortAnswer(t, store)
			if choice == "" {
				assert.NotContains(t, saved, "effort", "nothing chosen anywhere produces no record")
			} else {
				assert.Equal(t, map[string]any{"chosen": "provider_default"}, saved["effort"], "no sent, reason, provider or model on explicit Provider default")
			}
		})
	}
}

func TestT_S3_S91_OwnOffSurvivesExternalModelChange(t *testing.T) {
	installConversationCatalog(t, []string{"none", "low", "high"}, false, true)
	ep, url := newRejectionEndpoint(t, 0, 0)
	al, store := fullEffortRequestLoop(t, url, "high", nil)
	sid := containmentSessionID
	// S-91 specifies the durable Off and request/record, not this command's
	// success sentence. Prove it was handled by state and the exact HTTP count
	// below rather than inventing an additional wording contract.
	runConversationCommand(t, al, sid, "/effort off")
	require.Equal(t, "off", conversationValue(t, store, sid))
	// This is an external profile change, not the blocked UI switch path.
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true, "changed-m")
	cfg := al.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "changed-m", APIBase: url})
	cfg.Agents.List[0].Model = &config.AgentModelConfig{Primary: "changed-m", Provider: "openrouter"}
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"}, cfg))
	require.Equal(t, "ok", runConversationCommand(t, al, sid, "serve after external model change"))
	bodies := ep.snapshot()
	require.Len(t, bodies, 1, "commands made no provider calls")
	assert.Equal(t, "changed-m", bodies[0]["model"])
	assert.False(t, bodyHasEffort(bodies[0]))
	assert.Equal(t, "off", conversationValue(t, store, sid), "stored own Off is never rewritten")
	answer := fullEffortAnswer(t, store)
	assert.Equal(t, map[string]any{"chosen": "off", "reason": "not_supported", "provider": "openrouter", "model": "changed-m"}, answer["effort"])
}

func TestT_S3_S48_S49_CatalogRefreshPreservesStoredChoice(t *testing.T) {
	for _, choice := range []string{"off", "max"} {
		t.Run(choice, func(t *testing.T) {
			installConversationCatalog(t, []string{"none", "low", "medium", "high", "max"}, false, true)
			ep, url := newRejectionEndpoint(t, 0, 0)
			al, store := fullEffortRequestLoop(t, url, choice, nil)
			before, err := json.Marshal(al.GetConfig().Agents.List[0])
			require.NoError(t, err)
			installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
			require.Equal(t, "ok", runConversationCommand(t, al, containmentSessionID, "serve after refresh"))
			bodies := ep.snapshot()
			require.Len(t, bodies, 1)
			expected := map[string]any{"chosen": choice, "provider": "openrouter", "model": "primary-m"}
			if choice == "off" {
				assert.False(t, bodyHasEffort(bodies[0]))
				expected["reason"] = "not_supported"
			} else {
				assertOpenRouterLevelBody(t, bodies[0], "high")
				expected["sent"] = "high"
				expected["reason"] = "mapped"
			}
			assert.Equal(t, expected, fullEffortAnswer(t, store)["effort"])
			after, err := json.Marshal(al.GetConfig().Agents.List[0])
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

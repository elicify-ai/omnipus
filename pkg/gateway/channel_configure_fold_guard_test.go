package gateway

// T-19 integration: S-34 and FR-023. The real registered Configure route
// rejects every spelling encoding/json binds to the protected fields, with
// the normative JSON error message and no disk/live change. Both the bare
// discord instance and namespaced discord.b are covered. The untouched-list
// control uses a healthy credential-backed LINE adapter; it pins omission,
// not permission to add targets to that unsupported platform.

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestT19_ConfigureRejectsOutboundInAnySpelling_S34(t *testing.T) {
	cases := []struct {
		name, body, wantText string
	}{
		{"lower-case", `{"allow_from":["qa"],"outbound":{"policy":"agent_may_start","targets":["discord:999"]}}`,
			"outbound cannot be set via configure; use PUT /api/v1/channels/{id}/outbound"},
		{"title-case", `{"allow_from":["qa"],"Outbound":{"policy":"agent_may_start","targets":["discord:999"]}}`,
			"outbound cannot be set via configure; use PUT /api/v1/channels/{id}/outbound"},
		{"upper-case", `{"allow_from":["qa"],"OUTBOUND":{"policy":"agent_may_start","targets":["discord:999"]}}`,
			"outbound cannot be set via configure; use PUT /api/v1/channels/{id}/outbound"},
		{"unicode-fold", `{"allow_from":["qa"],"workſpace_id":"ws-other"}`,
			"workspace_id cannot be set via configure; use PUT /api/v1/channels/{id}/routing"},
		{"workspace-case", `{"allow_from":["qa"],"Workspace_ID":"ws-other"}`,
			"workspace_id cannot be set via configure; use PUT /api/v1/channels/{id}/routing"},
		{"identity-case", `{"allow_from":["qa"],"Identity":{"kind":"agent","id":"attacker"}}`,
			"identity cannot be set via configure; use PUT /api/v1/channels/{id}/routing"},
	}
	for _, id := range []string{"discord", "discord.b"} {
		for _, tc := range cases {
			t.Run(id+"/"+tc.name, func(t *testing.T) {
				f, _ := t14Fixture(t)
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
					inst := cfg.Channels["discord"]
					// This disabled adapter never connects. Reuse a real readable
					// synthetic fixture reference so completeness validation is
					// not an unrelated reason to refuse the request.
					inst.TokenRef = cfg.Channels["line.healthy"].ChannelAccessTokenRef
					cfg.Channels[id] = inst
					return config.SaveConfig(f.f1277.configPath, cfg)
				}))
				control := t14Request(t, f, http.MethodPut, "/api/v1/channels/"+id+"/configure", `{"allow_from":["control"]}`, true)
				require.Equal(t, http.StatusOK, control.Code, "instrument: this complete disabled instance can save ordinary fields: %s", control.Body.String())
				before, err := os.ReadFile(f.f1277.configPath)
				require.NoError(t, err)
				liveBefore, err := f.al.GetConfig().Clone()
				require.NoError(t, err)

				rec := t14Request(t, f, http.MethodPut, "/api/v1/channels/"+id+"/configure", tc.body, true)
				require.Equal(t, http.StatusBadRequest, rec.Code, "S-34: reject before merging: %s", rec.Body.String())
				var body gen.ErrorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.Equal(t, tc.wantText, body.Error, "S-34: exact error message, not serialized envelope")
				after, err := os.ReadFile(f.f1277.configPath)
				require.NoError(t, err)
				require.Equal(t, before, after, "refused Configure writes no file bytes")
				require.Equal(t, liveBefore.Channels, f.al.GetConfig().Channels, "refused Configure publishes no change")
			})
		}
	}
}

func TestT19_ConfigureSaveWithoutOutboundKeepsStoredList_GuardRule3(t *testing.T) {
	f, _ := t14Fixture(t)
	// Seed the pre-existing operator setting directly as fixture state. A
	// generic config commit cannot legitimately introduce it once T-8 lands.
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["line.healthy"]
		inst.Outbound = &config.ChannelOutboundConfig{Policy: "reply_only", Targets: []string{}}
		cfg.Channels["line.healthy"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	before := readOutboundConfigChannelsFile(t, f)
	beforeInst, ok := before["line.healthy"].(map[string]any)
	require.True(t, ok)

	rec := t14Request(t, f, http.MethodPut, "/api/v1/channels/line.healthy/configure", `{"allow_from":["qa1277-user"]}`, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	after := readOutboundConfigChannelsFile(t, f)
	inst, ok := after["line.healthy"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, beforeInst["outbound"], inst["outbound"], "T-8 rule 3: omission preserves the stored setting")
	require.Equal(t, []any{"qa1277-user"}, inst["allow_from"], "the unrelated change still saves")
	require.Equal(t, "reply_only", f.al.GetConfig().Channels["line.healthy"].Outbound.Policy)
}

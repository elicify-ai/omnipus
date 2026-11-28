// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// Oracle: cli-credentials-s1-s2-spec.md revision 8, S-45 / FR-009.
// An EXISTING false setting must survive the actual refused set_config true.
// Missing setting is loud BLOCKED, not proof of one-writer enforcement. S-45
// pins refusal and unchanged state, not a particular error code or UI wording.

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

func TestConfigSet_ChannelProgramsAllowed_OneWriter(t *testing.T) {
	deps, path := newTestDepsWithRealSave(t)
	cfg := deps.GetCfg()
	inst := config.ChannelInstanceConfig{Type: "telegram", Enabled: true}
	v := reflect.ValueOf(&inst).Elem()
	var allowed reflect.Value
	for i := 0; i < v.NumField(); i++ {
		if strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0] == "programs_allowed" {
			allowed = v.Field(i)
		}
	}
	if !allowed.IsValid() {
		t.Fatal("BLOCKED: ChannelInstanceConfig.programs_allowed not implemented — required by FR-009/S-45; generic missing-key refusal is not the one-writer oracle")
	}
	require.Equal(t, reflect.Bool, allowed.Kind(), "programs_allowed is a boolean setting (FR-009)")
	allowed.SetBool(false)
	cfg.Channels["telegram"] = inst
	require.NoError(t, config.SaveConfig(path, cfg))
	before, err := json.Marshal(cfg)
	require.NoError(t, err)
	diskBefore, err := os.ReadFile(path)
	require.NoError(t, err)

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key": "channels.telegram.programs_allowed", "value": true,
	})
	require.True(t, result.IsError, "S-45: actual set_config true must be refused: %s", result.ForLLM)
	// Read the same live config after this request, not an unrelated sibling.
	after, err := json.Marshal(deps.GetCfg())
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "refused write preserves the entire live config, including false")
	diskAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, diskBefore, diskAfter, "refused write preserves the persisted config byte-for-byte")
}

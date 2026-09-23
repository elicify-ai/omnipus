// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// tier13_reload_config_test.go pins the same stale-config-on-reload bug
// class 42657cc64 fixed for the bash tool (wireExecToolDepsOn): a helper
// that rebuilds a tool for a freshly-loaded registry must take that config
// as a parameter, not read it back out of al.GetConfig() — because
// ReloadProviderAndConfig runs the wiring pass BEFORE it publishes the new
// config (the al.cfg swap happens last), so a live read during that window
// returns the PREVIOUS config. wireTier13DepsLocked had exactly this bug for
// web_serve's ServeWorkspace duration bounds, dev-server port range/
// concurrency cap, Tier3Commands and EgressAllowList.

package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// webServeToolFields returns the unexported fields of the web_serve
// (serve_web) tool wireTier13DepsLocked registered for agentID, via
// reflection — mirrors bashToolFields in shell_permission_wiring_test.go.
func webServeToolFields(t *testing.T, al *AgentLoop, agentID string) reflect.Value {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must be registered", agentID)
	tool, ok := inst.Tools.Get(tools.ToolNameWebServe)
	require.True(t, ok, "%s must be registered for %q", tools.ToolNameWebServe, agentID)
	ws, ok := tool.(*tools.WebServeTool)
	require.True(t, ok, "%s must be the *tools.WebServeTool built by wireTier13DepsLocked, got %T",
		tools.ToolNameWebServe, tool)
	return reflect.ValueOf(ws).Elem()
}

// TestWireTier13DepsLocked_ConfigReloadReachesMinDuration proves a config
// reload's NEWLY loaded ServeWorkspace.MinDurationSeconds reaches the
// rebuilt web_serve tool, not whatever al.cfg still held at the moment
// wireTier13DepsLocked ran during that same reload.
func TestWireTier13DepsLocked_ConfigReloadReachesMinDuration(t *testing.T) {
	al := newGateTestLoopCfg(t, func(c *config.Config) {
		c.Tools.ServeWorkspace.MinDurationSeconds = 60
	})
	al.WireTier13Deps(Tier13Deps{ServedSubdirs: NewServedSubdirs()})

	f := webServeToolFields(t, al, testDefaultAgentID)
	require.Equal(t, 60*time.Second, time.Duration(f.FieldByName("minDuration").Int()),
		"setup: boot-time wiring must apply the initial config's MinDurationSeconds")

	next, err := al.GetConfig().Clone()
	require.NoError(t, err)
	next.Tools.ServeWorkspace.MinDurationSeconds = 5
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, next))

	f = webServeToolFields(t, al, testDefaultAgentID)
	assert.Equal(t, 5*time.Second, time.Duration(f.FieldByName("minDuration").Int()),
		"a config reload must rebuild web_serve from the NEWLY loaded config, not whatever "+
			"al.cfg still held while wireTier13DepsLocked ran during this same reload "+
			"(al.cfg is swapped in only AFTER the wiring pass completes)")
}

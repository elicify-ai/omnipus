// registry_reserved_default_id_test.go — ADR-071 §5.1.3 part 3: a
// PRE-EXISTING agent already id'd literally "default" at boot/reload gets a
// startup WARN, not a hard abort. This covers the upgrade-time half of the
// "default" collision fix — the create/update rejections in
// pkg/gateway/rest_agent_reserved_default_name_test.go only stop the
// collision going forward; a hand-edited config.json is the one path that
// still reaches NewAgentRegistry with an agent id of "default".

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestNewAgentRegistry_AgentIDLiterallyDefault_LogsWarnAndBootSucceeds proves
// three things in one pass: (1) boot does NOT abort — NewAgentRegistry
// returns a usable registry rather than panicking or erroring; (2) a WARN
// naming the agent is logged; (3) the agent remains fully reachable by every
// OTHER route (GetAgent by id) — only switch_agent's literal target:"default"
// path is shadowed (proven separately, at the tool layer, by
// TestSwitchAgentTool_DefaultSentinel_WinsOverRealAgentNamedDefault in
// pkg/tools/handoff_test.go).
func TestNewAgentRegistry_AgentIDLiterallyDefault_LogsWarnAndBootSucceeds(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)

	cfg := testCfg([]config.AgentConfig{
		{ID: "default", Name: "A Real Agent Named default", Type: config.AgentTypeCustom},
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
	})

	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	if registry == nil {
		t.Fatal("NewAgentRegistry must not abort/panic on an agent id of \"default\" — boot must succeed with a WARN, not abort")
	}

	// Reachable by every other route: direct id lookup still works.
	got, ok := registry.GetAgent("default")
	if !ok || got == nil {
		t.Fatal("the agent id'd \"default\" must remain reachable via GetAgent — only switch_agent's literal sentinel path is shadowed")
	}
	// The unrelated agent must also have registered normally.
	if _, ok := registry.GetAgent("mia"); !ok {
		t.Fatal("registering a same-boot agent id'd \"default\" must not prevent other agents from registering")
	}

	logged := readLog()
	// The captured log is JSON (zerolog), so the message's own embedded
	// quotes come back backslash-escaped — match without them.
	if !strings.Contains(logged, "agent id is literally") || !strings.Contains(logged, "default") {
		t.Errorf("expected a WARN naming the reserved-id collision, got log output: %q", logged)
	}
	if !strings.Contains(logged, "A Real Agent Named default") {
		t.Errorf("expected the WARN to name the specific agent (by its Name field), got log output: %q", logged)
	}
}

// TestNewAgentRegistry_NoAgentNamedDefault_NoWarn is the negative control:
// an ordinary config with no agent id'd "default" must not produce the WARN.
func TestNewAgentRegistry_NoAgentNamedDefault_NoWarn(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)

	cfg := testCfg([]config.AgentConfig{
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
		{ID: "default-assistant", Name: "Default Assistant", Type: config.AgentTypeCustom},
	})

	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	if registry == nil {
		t.Fatal("NewAgentRegistry must succeed for an ordinary config")
	}

	logged := readLog()
	if strings.Contains(logged, "agent id is literally") {
		t.Errorf("did not expect the reserved-id WARN for a config with no agent id'd exactly \"default\" "+
			"(an id-PREFIX match must not false-positive), got log output: %q", logged)
	}
}

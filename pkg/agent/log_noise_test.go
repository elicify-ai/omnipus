package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const duplicateRegistrationWarning = "Tool registration rejected: name already registered, keeping existing tool"

func TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	al := &AgentLoop{homePath: t.TempDir()}

	for _, agentID := range []string{"agent-a", "agent-b"} {
		agent := &AgentInstance{ID: agentID, Tools: tools.NewToolRegistry()}
		al.wirePlanToolsForAgent(agent, nil)
	}

	const noisyWarning = "plan store not yet installed"
	if got := strings.Count(readLog(), noisyWarning); got != 0 {
		t.Fatalf("expected the documented nil-store boot pass to emit no per-agent WARNs, got %d", got)
	}
}

func TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	agent := newEmailTestAgent()
	cfg := &config.Config{}

	registerEmailToolsForAgent(cfg, agent.ID, agent)
	before, ok := agent.Tools.Get("read_inbox")
	if !ok {
		t.Fatal("read_inbox must be present after initial wiring")
	}
	registerEmailToolsForAgent(cfg, agent.ID, agent)

	if got := strings.Count(readLog(), duplicateRegistrationWarning); got != 0 {
		t.Fatalf("expected normal email-tool re-wiring to emit no duplicate-registration WARNs, got %d", got)
	}
	after, ok := agent.Tools.Get("read_inbox")
	if !ok {
		t.Fatal("read_inbox must remain present after re-wiring")
	}
	if after == before {
		t.Fatal("expected re-wiring to install the refreshed read_inbox instance")
	}
}

func TestToolRegistry_UnexpectedDuplicateStillWarns(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	registry := tools.NewToolRegistry()
	incumbent := tools.EmailToolset(nil)[0]
	collision := tools.EmailToolset(nil)[0]

	registry.Register(incumbent)
	registry.Register(collision)

	logs := readLog()
	if got := strings.Count(logs, duplicateRegistrationWarning); got != 1 {
		t.Fatalf("expected one WARN for a genuine unexpected duplicate, got %d; logs:\n%s", got, logs)
	}
	if !strings.Contains(logs, `"name":"read_inbox"`) {
		t.Fatalf("unexpected-duplicate WARN must identify read_inbox; logs:\n%s", logs)
	}
	registered, ok := registry.Get("read_inbox")
	if !ok {
		t.Fatal("read_inbox must remain registered after a rejected collision")
	}
	if registered != incumbent {
		t.Fatal("unexpected duplicate must not replace the incumbent read_inbox tool")
	}
}

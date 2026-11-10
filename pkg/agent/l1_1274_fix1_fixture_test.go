package agent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Chief fix round 1 / CHECK F01-F03: refusals are before any resource write,
// not just an error flag returned after storing a broken worker.
type l11274Fix1Resources struct {
	workspace workspace.State
	config    []byte
	agents    []config.AgentConfig
}

func l11274Fix1ReadResources(t *testing.T, f *l11274Fixture) l11274Fix1Resources {
	t.Helper()
	state, err := workspace.ReadState(f.home, l11274WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	agents, skipped, err := agentstore.New(f.home).List()
	if err != nil || len(skipped) != 0 {
		t.Fatalf("read real refusal-boundary snapshot: err=%v, skipped=%v", err, skipped)
	}
	return l11274Fix1Resources{workspace: state, config: data, agents: agents}
}

func l11274Fix1AssertNoWrites(t *testing.T, f *l11274Fixture, id string, before l11274Fix1Resources) {
	t.Helper()
	l11274AssertAbsent(t, f, id)
	after := l11274Fix1ReadResources(t, f)
	if !reflect.DeepEqual(before.workspace, after.workspace) {
		t.Errorf("refusal mutated workspace/team/delegation: before=%+v, after=%+v", before.workspace, after.workspace)
	}
	if !bytes.Equal(before.config, after.config) {
		t.Error("refusal changed the real persisted config")
	}
	if !reflect.DeepEqual(before.agents, after.agents) {
		t.Error("refusal changed the real agent entity roster")
	}
	if _, err := os.Stat(filepath.Join(f.home, "agents", id)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refusal left an agent home for %q, stat error=%v", id, err)
	}
}

func l11274Fix1NativeArgs(agentType string) map[string]any {
	return map[string]any{
		"name": "Native soul boundary", "description": "Native soul contract control",
		"agent_type": agentType, "model": l11274Model, "color": "#6E8BA8",
	}
}

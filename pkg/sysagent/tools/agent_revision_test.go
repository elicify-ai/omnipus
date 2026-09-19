package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func currentAgentRevision(t *testing.T, deps *systools.Deps, id string) string {
	t.Helper()
	state, err := agentstore.New(deps.Home).ReadState(id)
	if err != nil {
		t.Fatalf("read agent revision %s: %v", id, err)
	}
	return state.Revision
}

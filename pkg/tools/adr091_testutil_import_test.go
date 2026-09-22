package tools_test

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestDelegationTree_UsableFromToolsTest(t *testing.T) {
	home := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	tree := testutil.DelegationTree(t, steer.Deps{
		LifecycleStore: session.NewLifecycleStore(filepath.Join(home, "lifecycle")),
		SessionStore:   sessions,
	}, 2)
	if tree.B.SessionID == "" {
		t.Fatal("depth-two fixture did not create B")
	}
}

package fspolicy_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// T-INV: real bootstrap and real record stores, not a synthetic directory list.
func TestWU23_FreshHomeInventoryClassifiedAndProtected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", home)
	if err := datamodel.Init(home); err != nil {
		t.Fatal(err)
	}
	if err := datamodel.InitAgentHome(home, "self"); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveRecord(home, workspace.Workspace{ID: "alpha", Name: "Alpha", Status: "active", CoreTeam: []string{"self"}}); err != nil {
		t.Fatal(err)
	}
	work, err := workspace.EnsureWorkDir(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close sessions: %v", closeErr)
		}
	}()
	main, err := store.GetOrCreateMainSession("alpha", "self")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.New(filepath.Join(home, "tasks"))
	tk := &task.Task{ID: "wu23-task", Title: "Inventory task", Prompt: "fixture", WorkspaceID: "alpha", AgentID: "self", Action: task.ActionLLM, Status: task.StatusNext}
	if createErr := tasks.Create(tk); createErr != nil {
		t.Fatal(createErr)
	}
	if _, _, runErr := tasks.OpenRun(tk.ID, nil, task.RunKindManual, main.ID, nil); runErr != nil {
		t.Fatal(runErr)
	}
	classified := map[string]bool{"agents": true, "workspaces": true, "media": true, "uploads": true, "toolchains": true}
	for _, rel := range fixture.ProtectedRelative() {
		classified[strings.Split(rel, "/")[0]] = true
	}
	t.Run("classification_instrument", func(t *testing.T) {
		if !classified["sessions"] || classified["deliberately-unclassified-fixture"] {
			t.Fatal("classification checker cannot distinguish known and newly invented top-level names")
		}
	})
	p, err := fspolicy.EffectiveFSPolicy(context.Background(), filepath.Join(home, "agents", "self"), work, false, home, "self", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == home {
			return err
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if !classified[top] {
			t.Errorf("T-INV unclassified new gateway entry: %s; classify it in the amendment inventory/allow-list", rel)
		}
		count++
		if !d.IsDir() && !fspolicy.CoversForGrant(p.WorkDir, path) && !fspolicy.IsCarveOut(path, p) {
			t.Errorf("T-INV gateway file outside work remains reachable: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("T-INV instrument: bootstrap created no entries")
	}
	t.Logf("classified %d fresh-home entries after bootstrap, agent, workspace, session and task run", count)
	// D1 is separate from classification: a future name is still denied even
	// before it is classified by the anti-drift table.
	future := filepath.Join(home, "deliberately-unclassified-fixture", "new.txt")
	if err := os.MkdirAll(filepath.Dir(future), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(future, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fspolicy.IsCarveOut(future, p) {
		t.Error("D1/T-INV: deliberately unclassified future file is not protected by default")
	}
}

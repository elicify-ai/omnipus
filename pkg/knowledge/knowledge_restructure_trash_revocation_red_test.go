// Omnipus — FR-VA-036 / TDD row 85 agent path: trashing a folder
// revokes views it contains even when their managing base remains outside.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeRestructureTrash_RevokesMembershipsBeforeTrashing(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	plantKnowledgeTrackedView(t, home, root, "Projects.base", "Sub/Open.view")
	res := NewRestructureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
		"op": "trash", "collection": "KB", "path": "Sub", "folder": true,
	})
	require.False(t, res.IsError, "agent trash must land through the membership-aware path: %s", res.ForLLM)
	require.FileExists(t, filepath.Join(root, "Projects.base"))
	require.NoDirExists(t, filepath.Join(root, "Sub"))
	require.Empty(t, loadKnowledgeMembers(t, home, root).Bases,
		"a view inside the trashed folder must have no management authority afterward")
	copies, err := filepath.Glob(filepath.Join(root, MarkerDirName, "trash", "*", "Sub", "Open.view"))
	require.NoError(t, err)
	require.Len(t, copies, 1, "the view should be in recoverable trash, not permanently deleted")
	body := a4Read(t, filepath.Dir(copies[0]), filepath.Base(copies[0]))
	require.False(t, strings.Contains(body, "derived_from:"),
		"the trashed copy must be released before trash moves it")
}

func TestKnowledgeRestructureTrash_AcquiresEnclosingMembershipLockBeforeNestedRoot(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	plantKnowledgeTrackedView(t, home, root, "Projects.base", "Outer/Open.view")
	nested := filepath.Join(root, "Outer", "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	a4Vault(t, nested, "Nested KB")
	plantKnowledgeTrackedView(t, home, nested, "Nested.base", "Nested.view")

	// Hold the enclosing lock while trash discovers both roots. The private
	// lock table counts holders AND waiters: a correctly ordered acquisition
	// first waits on root (refs=2), without borrowing the nested lock.
	result := make(chan string, 1)
	first := ""
	err := WithViewMembershipLock(home, root, func() error {
		go func() {
			res := NewRestructureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
				"op": "trash", "collection": "KB", "path": "Outer", "folder": true,
			})
			if res.IsError {
				result <- res.ForLLM
			} else {
				result <- ""
			}
		}()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			viewMembershipLocks.Lock()
			outerEntry := viewMembershipLocks.entries[root]
			nestedEntry := viewMembershipLocks.entries[nested]
			outerRefs, nestedRefs := 0, 0
			if outerEntry != nil {
				outerRefs = outerEntry.refs
			}
			if nestedEntry != nil {
				nestedRefs = nestedEntry.refs
			}
			viewMembershipLocks.Unlock()
			if nestedRefs > 0 {
				first = "nested"
				return nil
			}
			if outerRefs >= 2 {
				first = "enclosing"
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		return fmt.Errorf("trash did not attempt either membership lock before deadline")
	})
	require.NoError(t, err)
	select {
	case failure := <-result:
		require.Empty(t, failure, "agent trash of both roots must complete after the held lock releases")
	case <-time.After(10 * time.Second):
		t.Fatal("trash never completed after the enclosing lock released")
	}
	require.Equal(t, "enclosing", first, "nested-root trash must acquire membership locks in enclosing-first sorted order")
	require.Empty(t, loadKnowledgeMembers(t, home, root).Bases)
	require.Empty(t, loadKnowledgeMembers(t, home, nested).Bases)
}

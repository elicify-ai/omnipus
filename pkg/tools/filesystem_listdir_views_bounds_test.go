//go:build !windows

// Resource-bound coverage for implicit view loading during an agent listing
// (security round 5, item 3): the bounds are asserted as BEHAVIOUR, never as
// limit values — no product byte/count budget exists to pin a number against.
// Guarded off Windows because syscall.Mkfifo is unix-only, matching the
// existing precedent in pkg/gateway/rest_clivalidate_fifo_test.go.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boundsListTimeout is the test's patience for one listing, not a product
// limit: an ordinary listing of this fixture completes in milliseconds, so a
// few seconds is generous headroom to observe a hang. The test fails when the
// bound fires; it never passes BECAUSE of the bound.
const boundsListTimeout = 10 * time.Second

// TestListDirTool_ListingSurvivesUnreadableViewCandidates pins two bounds of
// implicit view loading at once:
//  1. a candidate in the views directory that cannot be meaningfully read
//     must never strand the listing — the listing completes within the
//     bounded wait;
//  2. a non-regular candidate is skipped: no line renders for it, while the
//     folder's ordinary rows and the candidates that do read still render.
//
// RED on the current tree: the loader opens every candidate with an ordinary
// blocking read, so the listing does not come back within the bound.
func TestListDirTool_ListingSurvivesUnreadableViewCandidates(t *testing.T) {
	ws := t.TempDir()
	vault := filepath.Join(ws, "Vault")
	writeListdirVault(t, vault, "Finance vault", map[string]string{
		"working--view.yaml": "name: working-view\ntype: invoice\nlabel: Working view\nkind: summary\nproperties: [client]\nsource: CRM/Invoices.base\n",
	})
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "CRM"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(vault, "CRM", "invoice-1.md"), []byte("# Invoice\n"), 0o600))
	require.NoError(t, syscall.Mkfifo(
		filepath.Join(vault, ".omnipus-vault", "views", "piped--view.yaml"), 0o600))

	type listing struct {
		out *ToolResult
	}
	done := make(chan listing, 1)
	go func() {
		done <- listing{NewListDirTool(ws, true).Execute(context.Background(),
			map[string]any{"path": "Vault/CRM"})}
	}()
	select {
	case got := <-done:
		out := got.out
		require.False(t, out.IsError, "a candidate the loader cannot read must never error the listing: %s", out.ForLLM)
		assert.Contains(t, out.ForLLM, "FILE: invoice-1.md",
			"the folder's ordinary rows survive")
		assert.Contains(t, out.ForLLM, "VIEW: Working view (kind summary, view working-view, knowledge base Finance vault)",
			"the candidates that do read still render")
		assert.NotContains(t, out.ForLLM, "piped--view",
			"a non-regular candidate is skipped: no line renders for it")
	case <-time.After(boundsListTimeout):
		t.Fatalf("the listing did not complete within %s: a candidate the loader "+
			"cannot read must never strand an ordinary listing (the bound is the "+
			"test's patience, not a product limit)", boundsListTimeout)
	}
}

// TestListDirTool_UnreadableCandidateDegradesToOrdinaryRow pins the degrade
// half of the S3 bounds: a view candidate the process cannot read yields no
// VIEW line and no listing error, while the storage file itself still lists
// as an ordinary row and the candidates that do read still render.
//
// GUARD RULE: expected green on the current tree — the loader already reports
// an unreadable candidate as a per-file rejection instead of failing the set
// (pkg/records/view.go, RejectViewUnreadable); this test keeps that property
// from regressing while the loader gains its bounds.
func TestListDirTool_UnreadableCandidateDegradesToOrdinaryRow(t *testing.T) {
	ws := t.TempDir()
	vault := filepath.Join(ws, "Vault")
	writeListdirVault(t, vault, "Finance vault", map[string]string{
		"working--view.yaml": "name: working-view\ntype: invoice\nlabel: Working view\nproperties: [client]\n",
		"locked--view.yaml":  "name: locked-view\ntype: invoice\nlabel: Locked view\nproperties: [client]\n",
	})
	locked := filepath.Join(vault, ".omnipus-vault", "views", "locked--view.yaml")
	require.NoError(t, os.Chmod(locked, 0o000))

	out := NewListDirTool(ws, true).Execute(context.Background(), map[string]any{"path": "Vault"})
	require.False(t, out.IsError, "an unreadable candidate must not error the listing: %s", out.ForLLM)
	assert.Contains(t, out.ForLLM, "VIEW: Working view",
		"the candidates that do read still render")
	assert.NotContains(t, out.ForLLM, "locked-view",
		"an unreadable candidate degrades: no view line renders for it")

	store := NewListDirTool(ws, true).Execute(context.Background(),
		map[string]any{"path": "Vault/.omnipus-vault/views"})
	require.False(t, store.IsError, store.ForLLM)
	assert.Contains(t, store.ForLLM, "FILE: locked--view.yaml",
		"an unreadable candidate degrades to an ordinary row, not an error")
}

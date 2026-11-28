// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestSecretTools_ProgramOwned (order 6, Unit) — S-129: the
// remove_secret agent tool cannot remove a program token.
//
// Oracle: FR-048 ("every other exported mutating method of credentials.Store
// refuses a program-owned name with ErrProgramOwned") and S-129's Then: the
// tool returns the program-owned refusal, GitHub stays connected, and a
// private-variable reference is still removable as today. The refusal text
// rides the store's program-owned message (the S-128 wording the spec pins:
// "<ref> belongs to the program <program>. Change it in Settings →
// Programs."). Expected values come from the spec, never from the tool.
//
// RED expectation (CORRECTED, CHECK F19 — the original header wrongly said
// remove_secret "DELETES the program-owned reference R ... with the deletion
// in hand"): at RED CommitProgramCredential is the no-op stub, so R never
// exists and the tool answers NOT_FOUND ("no stored reference R; nothing was
// removed") — the row fails on the missing program-owned refusal, not on an
// observed deletion. The corrected receipt is the saved log
// /workspace/logs/1960/s1-red/fix1/f19-s129-receipt.log.

package systools_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

func s129RemoveTool(t *testing.T) (*credentials.Store, *systools.RemoveSecretTool) {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x73}, 32)))
	t.Cleanup(store.Close)
	tool := systools.NewRemoveSecretTool(&systools.Deps{
		CredStore: store,
		GetCfg:    func() *config.Config { return &config.Config{} },
	})
	return store, tool
}

func TestSecretTools_ProgramOwned(t *testing.T) {
	t.Run("S129_remove_secret_refuses_a_program_token", func(t *testing.T) {
		store, tool := s129RemoveTool(t)
		// Given: GitHub connected under reference R — the program-scoped
		// method is I-3's only writer for a program-owned name.
		_, err := store.CommitProgramCredential("R", "tok_valid_12345",
			&credentials.CardReceipt{RequestID: "req-s129", SessionID: "sess-s129",
				References: []string{"R"}, SubmittedAt: time.Now()})
		require.NoError(t, err, "instrument: the reference is committed as program-owned")
		before := programstore.RequireOwned(t, store, "R", "tok_valid_12345", "req-s129", "sess-s129")

		// When: Omnipus calls remove_secret with reference R.
		result := tool.Execute(context.Background(), map[string]any{"reference_name": "R"})

		// Then: the program-owned refusal; GitHub stays connected.
		require.True(t, result.IsError,
			"S-129: remove_secret must refuse a program-owned reference: %s", result.ForLLM)
		require.Contains(t, result.ForLLM, "belongs to the program",
			"the refusal is the program-owned message: %s", result.ForLLM)
		value, gerr := store.Get("R")
		require.NoError(t, gerr, "GitHub stays connected")
		require.Equal(t, "tok_valid_12345", value, "the stored token is unchanged")
		gen, ok := store.ProgramEntryGeneration("R")
		require.True(t, ok, "the generation metadata survives")
		require.Equal(t, int64(1), gen, "no new generation was written")
		require.Equal(t, before, programstore.Snapshot(t, store, "req-s129"), "value, kind, private selections, generation, metadata and receipt survive the same attack")
	})

	t.Run("S129_private_variable_reference_still_removable", func(t *testing.T) {
		store, tool := s129RemoveTool(t)
		receipt, err := store.CommitCard("req-private-s129", "sess-private-s129", []credentials.CardValue{{
			BaseName: "SVC_TOKEN", Recipient: credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: "home", AgentID: "dev", Field: "SVC_TOKEN"}, Value: "svc_private_12345678",
		}}, time.Now())
		require.NoError(t, err)
		require.Len(t, receipt.References, 1)
		ref := receipt.References[0]
		inventory, err := store.CardInventory()
		require.NoError(t, err)
		require.Len(t, inventory, 1)
		require.Equal(t, credentials.KindAgentPrivate, inventory[0].Secret.Recipient.Kind)
		require.True(t, inventory[0].Selected, "the regression is a selected agent-private variable, not an ordinary Set")
		env, err := store.PrivateEnvironment("home", "dev", false)
		require.NoError(t, err)
		require.Equal(t, []credentials.PrivateValue{{Name: "SVC_TOKEN", Value: "svc_private_12345678"}}, env)

		result := tool.Execute(context.Background(), map[string]any{"reference_name": ref})
		require.False(t, result.IsError,
			"a private-variable reference is still removable as today: %s", result.ForLLM)
		names, lerr := store.List()
		require.NoError(t, lerr)
		require.NotContains(t, names, ref, "the private variable is removed")
		env, err = store.PrivateEnvironment("home", "dev", false)
		require.NoError(t, err)
		require.Empty(t, env, "the removed selection no longer injects the private variable")
		afterReceipt, found, err := store.CardReceipt("req-private-s129")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, receipt, afterReceipt, "removing the value preserves its historical receipt")
	})
}

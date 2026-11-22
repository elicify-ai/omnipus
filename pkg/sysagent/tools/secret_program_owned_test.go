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
// RED expectation: the tool's removal path reaches the store before the
// store guard exists, so remove_secret DELETES the program-owned reference R
// — the assertions fail with the deletion in hand (the exact corruption
// S-129 refuses).

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
	})

	t.Run("S129_private_variable_reference_still_removable", func(t *testing.T) {
		store, tool := s129RemoveTool(t)
		require.NoError(t, store.Set("SVC_TOKEN", "svc_private_12345678"),
			"instrument: the private variable exists")

		result := tool.Execute(context.Background(), map[string]any{"reference_name": "SVC_TOKEN"})
		require.False(t, result.IsError,
			"a private-variable reference is still removable as today: %s", result.ForLLM)
		names, lerr := store.List()
		require.NoError(t, lerr)
		require.NotContains(t, names, "SVC_TOKEN", "the private variable is removed")
	})
}

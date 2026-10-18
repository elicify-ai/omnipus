// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 external-program worker exclusion (BDD-7.4, US-7 AS-2): an
// external-program worker that is delegated work gets NO private secret in
// its child environment. The dispatch site
// (pkg/agent/external_dispatch.go::runExternalCLISubTurn) composes the child
// env through the runner filter; a control runner-credential key proves the
// env slice is real and reachable.
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestWU5PrivateSecret_ExternalWorker_Excluded is BDD-7.4: Mia's Sales
// WEATHER_TOKEN is stored and selected, an external-program worker is
// delegated work — the worker's child environment must not contain it, while
// the env slice itself is proven real (it carries the scrubbed baseline's
// PATH).
func TestWU5PrivateSecret_ExternalWorker_Excluded(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")

	// Store Mia's secret through the real card flow (positive leg of the
	// scoping contract; without it the exclusion below is vacuous).
	f.provider.setScript(sales, []providers.LLMResponse{wu5RequestPrivateSecret(wu5Variable), wu5Stop("stored")})
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "card raised")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)

	// An external-program worker in the SAME workspace, dispatched through
	// the real dispatch site with the fake driver recording the real
	// RunOptions (the child env slice the driver would launch with).
	extAgent := &AgentInstance{
		ID:   "ext-worker",
		Name: "External Worker",
		Home: filepath.Join(f.home, "agents", "ext-worker"),
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	seedTestWorkspaceMembershipForIDs(t, []string{extAgent.ID})
	ts := &turnState{
		agent:               extAgent,
		agentID:             extAgent.ID,
		turnID:              "wu5-ext-run-1",
		transcriptSessionID: sales,
	}
	fr, restore := withFakeDriver(t)
	defer restore()

	// The driver needs one output event and an end, or the dispatch waits.
	go func() {
		_ = fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "worker ran"}})
		_ = fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	_, err := runExternalCLISubTurn(context.Background(), f.al, ts, "do the delegated task")
	require.NoError(t, err, "the external dispatch must run (control: the worker is delegated work)")

	opts := fr.RecordedRunOpts()
	require.Len(t, opts, 1, "the fake driver recorded exactly one run")
	env := opts[0].Env
	require.NotEmpty(t, env, "instrument: the recorded child env slice must be non-empty — an empty slice would make the exclusion below vacuous")

	foundPATH := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			foundPATH = true
		}
		if strings.HasPrefix(kv, wu5Variable+"=") {
			t.Fatalf("the external worker's child env contains %s — BDD-7.4 forbids private secrets in external-program workers (got %q)", wu5Variable, kv)
		}
	}
	require.True(t, foundPATH, "instrument: the recorded env must carry the scrubbed baseline PATH; without it the slice is not the real child env")
}

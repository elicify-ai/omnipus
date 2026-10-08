// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_dispatch_transcript_redact_test.go — #1222: an external-CLI child's
// tool results, diffs and permission descriptions must be credential-filtered
// before they are STORED, as native results are, not only when broadcast live.

package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// A secret containing a quote and a newline: in JSON output it only appears
// ESCAPED (\" and \n), so a raw-bytes replace of the plain secret misses it.
const redactSecret1222 = "sk-\"quoted\"\nsecret-1222-value"

// runExternalAndReadStore runs an external-CLI sub-turn emitting evs with the
// secret registered, and returns every byte persisted under the session store.
func runExternalAndReadStore(t *testing.T, evs []runner.RunEvent) (stored string, positiveControlSeen bool) {
	t.Helper()
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "codex", "")
	cfg := al.GetConfig()
	cfg.Tools.FilterSensitiveData = true
	cfg.RegisterSensitiveValues([]string{redactSecret1222})

	dir := t.TempDir() + "/sessions"
	store, err := session.NewUnifiedStore(dir)
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "ext-agent") // AppendTranscript is strict: the session must exist
	require.NoError(t, err)
	ts.transcriptStore = store
	ts.transcriptSessionID = meta.ID

	fr, restore := withFakeDriver(t)
	defer restore()
	go func() {
		for _, ev := range evs {
			fr.InjectEvent(ev)
		}
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()
	_, _ = runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.NoError(t, store.Close())

	var sb strings.Builder
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sb.Write(b)
		return nil
	}))
	stored = sb.String()
	return stored, strings.Contains(stored, "bash")
}

func requireNoSecret(t *testing.T, stored string) {
	t.Helper()
	escaped, err := json.Marshal(redactSecret1222)
	require.NoError(t, err)
	inner := strings.Trim(string(escaped), `"`)
	require.NotContains(t, stored, redactSecret1222, "plain secret persisted")
	require.NotContains(t, stored, inner, "JSON-escaped secret persisted")
	require.NotContains(t, stored, "secret-1222-value", "secret tail persisted")
}

func TestExternalDispatch_StoredResultsAreRedacted(t *testing.T) {
	for name, isErr := range map[string]bool{"normal result": false, "error result": true} {
		t.Run(name, func(t *testing.T) {
			out, err := json.Marshal(map[string]any{"stdout": "key=" + redactSecret1222, "n": 7})
			require.NoError(t, err)
			stored, control := runExternalAndReadStore(t, []runner.RunEvent{
				{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{CallID: "c1", ToolName: "bash"}},
				{Kind: runner.EventKindToolResult, ToolResult: &runner.ToolResultEvent{
					CallID: "c1", ToolName: "bash", Output: out, IsError: isErr}},
			})
			require.True(t, control, "instrument check: the stored transcript must contain the tool call")
			requireNoSecret(t, stored)
			require.Contains(t, stored, "[FILTERED]")
		})
	}
}

func TestExternalDispatch_StoredNonJSONResultIsRedacted(t *testing.T) {
	stored, control := runExternalAndReadStore(t, []runner.RunEvent{
		{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{CallID: "c1", ToolName: "bash"}},
		{Kind: runner.EventKindToolResult, ToolResult: &runner.ToolResultEvent{
			CallID: "c1", ToolName: "bash", Output: []byte("plain text with " + redactSecret1222)}},
	})
	require.True(t, control)
	requireNoSecret(t, stored)
}

func TestExternalDispatch_StoredDiffAndPermissionAreRedacted(t *testing.T) {
	stored, control := runExternalAndReadStore(t, []runner.RunEvent{
		{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{CallID: "c1", ToolName: "bash"}},
		{Kind: runner.EventKindDiff, Diff: &runner.DiffEvent{Path: "a.env", Diff: "+TOKEN=" + redactSecret1222}},
		{Kind: runner.EventKindPermissionRequest, PermissionRequest: &runner.PermissionRequestEvent{
			RequestID: "r1", ToolName: "bash", Description: "run curl -H " + redactSecret1222}},
	})
	require.True(t, control)
	requireNoSecret(t, stored)
}

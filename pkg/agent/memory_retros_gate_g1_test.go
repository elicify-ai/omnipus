package agent

// B4/SFH-12 (Settings #1519 gate G1): real filesystem failures must not be
// presented as complete recall. Keep readable hits and propagate a typed
// partial diagnostic through the real adapter and recall_memory tool.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestGateG1_B4_PartialRetroReadKeepsHitsAndWarnsRecallCaller(t *testing.T) {
	f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":730}`)
	root := f.adapter.ms.privateRoom.Root
	readable := plantRetro(t, filepath.Dir(root), 1, "partialzeppelin")
	broken := filepath.Join(filepath.Dir(readable), "disappeared_retro.md")
	// A dangling link deterministically fails os.ReadFile, including when the
	// runner is root. Unlike chmod, this fault cannot accidentally be readable.
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing-reflection.md"), broken))
	_, err := os.ReadFile(broken)
	require.ErrorIs(t, err, os.ErrNotExist, "instrument: the injected file must really fail to read")

	hits, err := f.adapter.SearchRetros("partialzeppelin", 20)
	assert.Len(t, hits, 1, "B4: a partial read must retain the readable matching reflection")
	var partial interface {
		error
		Partial() bool
	}
	assert.True(t, errors.As(err, &partial), "B4/SFH-12: SearchRetros must expose a typed partial-read diagnostic, not nil or a total failure")
	if partial != nil {
		assert.True(t, partial.Partial(), "B4: the diagnostic must explicitly identify incomplete results")
	}
	result := tools.NewRecallMemoryTool(f.adapter).Execute(context.Background(), map[string]any{"query": "partialzeppelin", "limit": float64(20)})
	assert.Contains(t, result.ForLLM, "partialzeppelin", "B4: the real recall tool must keep the readable hit when it adds the warning")
	assert.Regexp(t, `(?i)(retrospect[^\n]*(partial|incomplete|fail|unavailable)|(partial|incomplete)[^\n]*retrospect)`, result.ForLLM,
		"B4/SFH-12: the caller must be told retrospective search is incomplete, not given complete-looking success")
}

func TestGateG1_B4_MissingRetroRootIsOrdinaryEmptyRecall(t *testing.T) {
	home := t.TempDir()
	ms := NewMemoryStore(filepath.Join(home, "agent"), home)
	adapter := NewMemoryStoreAdapter(ms)
	hits, err := adapter.SearchRetros("absentzeppelin", 20)
	require.NoError(t, err, "B4/SFH-12: an ordinary missing retrospective root is not a failed read")
	assert.Empty(t, hits)
	result := tools.NewRecallMemoryTool(adapter).Execute(context.Background(), map[string]any{"query": "absentzeppelin", "limit": float64(20)})
	assert.False(t, result.IsError)
	assert.Equal(t, "no matching entries", result.ForLLM, "B4: preserve the legitimate empty-search result")
}

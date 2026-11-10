package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// N07: PLAN-omnipus-L1-v2-20261013.md section 11, founder "N07 -> stage 2".
// The independent protected-name inventory is amendment section 2.2 D4.
// Do not derive these fixtures or expected names from the policy's own arrays:
// removing an internal name from production must not remove it from this test.
func TestTrain1N07AgentHomeListingsHideProtectedNames(t *testing.T) {
	for _, restrict := range []bool{true, false} {
		t.Run(fmt.Sprintf("restrict_%t", restrict), func(t *testing.T) {
			registry, ctx, agentHome := train1N07ListingFixture(t, restrict)
			for _, row := range []struct {
				name string
				args map[string]any
			}{
				{"default_path", map[string]any{}},
				{"relative_path", map[string]any{"path": "."}},
				{"absolute_path", map[string]any{"path": agentHome}},
			} {
				t.Run(row.name, func(t *testing.T) {
					result := registry.Execute(ctx, "list_directory", row.args)
					require.False(t, result.IsError, "N07 must filter the listing, not refuse the working folder: %s", result.ForLLM)
					assert.Equal(t, []string{"notes.txt", "projects"}, train1N07ListedNames(t, result.ForLLM),
						"N07: only ordinary entries may be sent to the agent")
					for _, name := range []string{".omnipus", "memory", "sessions", "skills", "SOUL.md", "AGENT.md", "HEARTBEAT.md", "MEMORY.md"} {
						assert.NotContains(t, result.ForLLM, name, "N07: protected name leaked in model-facing output")
						assert.NotContains(t, result.ForUser, name, "N07: protected name leaked in user-facing output")
					}
				})
			}

			t.Run("ordinary_nested_names_are_not_censored", func(t *testing.T) {
				result := registry.Execute(ctx, "list_directory", map[string]any{"path": "projects"})
				require.False(t, result.IsError, "ordinary directory control: %s", result.ForLLM)
				assert.Equal(t, []string{"memory", "skills"}, train1N07ListedNames(t, result.ForLLM),
					"D4 protects internal paths, not every occurrence of an internal basename")
			})

			t.Run("ordinary_file_read_control", func(t *testing.T) {
				result := registry.Execute(ctx, "read_file", map[string]any{"path": "notes.txt"})
				require.False(t, result.IsError, "ordinary read proves the real reader works: %s", result.ForLLM)
				_, body, ok := strings.Cut(result.ForLLM, "\n\n")
				require.True(t, ok, "read_file must return its framing and file body")
				assert.Equal(t, "ordinary task notes\n", body)
			})

			t.Run("protected_file_read_still_refused", func(t *testing.T) {
				result := registry.Execute(ctx, "read_file", map[string]any{"path": filepath.Join(".omnipus", "state.txt")})
				assert.True(t, result.IsError, "N07 must not reopen protected contents: %s", result.ForLLM)
				assert.Same(t, ErrCarveOut, result.Err, "the refusal must be filesystem protection, not missing-file or setup failure")
				assert.Contains(t, result.ForLLM, ErrCarveOut.Error(), "specific protection refusal")
				assert.NotContains(t, result.ForLLM, "N07_PRIVATE_TEST_CONTENT", "protected bytes must not reach the agent")
				assert.NotContains(t, result.ForUser, "N07_PRIVATE_TEST_CONTENT", "protected bytes must not reach the user result")
			})
		})
	}
}

func train1N07ListingFixture(t *testing.T, restrict bool) (*ToolRegistry, context.Context, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, ".omnipus")
	agentHome := filepath.Join(home, "agents", "listing-test-agent")
	t.Setenv("HOME", root)
	t.Setenv("OMNIPUS_HOME", home)

	for _, name := range []string{".omnipus", "memory", "sessions", "skills", "projects/memory", "projects/skills"} {
		require.NoError(t, os.MkdirAll(filepath.Join(agentHome, filepath.FromSlash(name)), 0o700))
	}
	for _, name := range []string{"SOUL.md", "AGENT.md", "HEARTBEAT.md", "MEMORY.md", ".omnipus/state.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(agentHome, filepath.FromSlash(name)), []byte("N07_PRIVATE_TEST_CONTENT\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "notes.txt"), []byte("ordinary task notes\n"), 0o600))

	registry := NewToolRegistry()
	registry.Register(NewListDirTool(agentHome, restrict))
	registry.Register(NewReadFileTool(agentHome, restrict, MaxReadFileSize))
	ctx := WithAgentID(context.Background(), "listing-test-agent")
	return registry, ctx, agentHome
}

func train1N07ListedNames(t *testing.T, listing string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(listing), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		kind, name, ok := strings.Cut(line, ":")
		require.True(t, ok, "expected an actual directory entry, got %q", line)
		require.Contains(t, []string{"FILE", "DIR", "KB"}, kind, "unexpected directory entry %q", line)
		names = append(names, strings.TrimSpace(name))
	}
	return names
}

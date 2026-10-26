//go:build goolm && stdjson

package email_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC.md Q25 / SEC-03: a permitted download must be reachable through the
// normal agent registry, with a configured mailbox and real workspace save.
func TestQ25RegisteredMainDownloadsAttachment(t *testing.T) {
	fixture := q25RegisteredMain(t)
	args := q25AttachmentArgs(t, fixture)
	download, ok := fixture.instance.Tools.Get("download_email_attachment")
	require.True(t, ok)
	assert.Equal(t, "allow", tools.EffectiveToolPolicy(fixture.instance.LoadToolPolicy(), download.Scope(), fixture.instance.AgentType, download.Name()),
		"SPEC.md Table: Main attachment downloads are permitted by default")
	monthBefore := time.Now().UTC().Format("2006-01")
	result := download.Execute(fixture.ctx, args)
	require.False(t, result.IsError, "SEC-03: the normally registered Main download must save, not report missing transfer wiring: %s", result.ForLLM)
	var receipt struct {
		Saved        bool   `json:"saved"`
		Path         string `json:"path"`
		AbsolutePath string `json:"absolute_path"`
		SizeBytes    int64  `json:"size_bytes"`
		AuditStatus  string `json:"audit_status"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &receipt))
	assert.True(t, receipt.Saved, "SEC-03: successful result must truthfully report a saved file")
	assert.Equal(t, int64(len(q25Payload)), receipt.SizeBytes)
	assert.Equal(t, "recorded", receipt.AuditStatus, "Q25 keeps the normal attachment-save audit with audit logging enabled")
	parts := strings.Split(receipt.Path, "/")
	require.Len(t, parts, 4, "normal attachment naming is mail/<mailbox>/<year-month>/<filename>")
	assert.Equal(t, "mail", parts[0])
	assert.Equal(t, q25Mailbox, parts[1])
	assert.Contains(t, []string{monthBefore, time.Now().UTC().Format("2006-01")}, parts[2], "the save month comes from this call, including a real month-boundary crossing")
	assert.Equal(t, "report.txt", parts[3])
	require.True(t, filepath.IsAbs(receipt.AbsolutePath))
	actual, err := os.ReadFile(receipt.AbsolutePath)
	require.NoError(t, err)
	assert.Equal(t, q25Payload, string(actual), "SEC-03: the returned path must contain the actual received attachment bytes")
	workRoot, err := filepath.EvalSymlinks(fixture.workA)
	require.NoError(t, err)
	savedPath, err := filepath.EvalSymlinks(receipt.AbsolutePath)
	require.NoError(t, err)
	relative, err := filepath.Rel(workRoot, savedPath)
	require.NoError(t, err)
	assert.False(t, relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)), "save must stay inside workspace A's allowed work root")
}

// Chief's SEC-03 addition: even a valid attachment/ref cannot turn an A-bound
// call into a write through workspace B (or an unrelated filesystem root).
func TestQ25RegisteredMainRefusesCrossWorkspaceAttachmentSave(t *testing.T) {
	for _, target := range []string{"workspace_b", "outside_allowed_roots"} {
		t.Run(target, func(t *testing.T) {
			fixture := q25RegisteredMain(t)
			args := q25AttachmentArgs(t, fixture)
			root := fixture.workB
			if target == "outside_allowed_roots" {
				root = t.TempDir()
			}
			beforeA := q25DirectoryNames(t, fixture.workA)
			beforeTarget := q25DirectoryNames(t, root)
			// The trusted turn remains workspace A; a mismatched destination
			// root must be rejected by the normal per-call filesystem policy.
			ctx := tools.WithTurnWorkspaceDir(fixture.ctx, root)
			download, ok := fixture.instance.Tools.Get("download_email_attachment")
			require.True(t, ok)
			result := download.Execute(ctx, args)
			require.True(t, result.IsError, "SEC-03: an A-bound call must refuse the %s root", target)
			assert.Contains(t, result.ForLLM, "download_email_attachment", "refusal must identify the failed operation")
			assert.Regexp(t, `(?i)(workspace|outside|denied|not (permitted|allowed)|authoriz|mismatch|unsafe|escape)`, result.ForLLM,
				"SEC-03: a clear authorization/root refusal is required, not an unrelated transport failure")
			assert.NotContains(t, result.ForLLM, "not wired", "missing service wiring is not proof of cross-workspace protection")
			assert.Equal(t, beforeA, q25DirectoryNames(t, fixture.workA), "refused save must not create output in workspace A")
			assert.Equal(t, beforeTarget, q25DirectoryNames(t, root), "refused save must not create output or directories in the forbidden root")
		})
	}
}

func q25DirectoryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	names := slices.Grow([]string(nil), len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

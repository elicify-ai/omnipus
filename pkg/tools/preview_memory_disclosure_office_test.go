package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/stretchr/testify/require"
)

// T-80 / §3.1 D1.4: the enforcement DECISION is unit-tested in pkg/preview on
// injected kernel/parameter values; this file pins the two disclosure
// surfaces that carry its reason to operators — the preview_document result
// text and the preview_document.run audit field — against an independently
// spelled disclosure string, plus the enforced negative control for both.
func TestPreviewMemoryUnenforcedDisclosureTextAndAudit(t *testing.T) {
	reason := "the Linux kernel 4.6.0-1-amd64 is older than 4.7"

	tool := NewPreviewDocumentTool(t.TempDir(), true)
	disclosed := tool.buildResult(testContext(t), "report.pdf", &preview.Result{
		Validity:         "structural",
		RenderDetail:     "structural (Office without a renderer)",
		MemoryUnenforced: reason,
	}, nil)
	require.False(t, disclosed.IsError)
	require.Contains(t, disclosed.ForLLM, "memory limit not enforced: "+reason+"\n",
		"§3.1 D1.4: the result text carries the disclosure with its reason")

	enforced := tool.buildResult(testContext(t), "report.pdf", &preview.Result{
		Validity:     "structural",
		RenderDetail: "structural (Office without a renderer)",
	}, nil)
	require.NotContains(t, enforced.ForLLM, "memory limit not enforced",
		"4.7 and N produce no disclosure line")

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	emitPreviewRunAudit(testContext(t), logger, "preview_document", "report.pdf", &preview.Result{
		Validity:         "structural",
		MemoryUnenforced: reason,
	})
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Event   string         `json:"event"`
			Details map[string]any `json:"details"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		if entry.Event != "preview_document.run" {
			continue
		}
		value, ok := entry.Details["memory_limit_not_enforced"]
		require.True(t, ok, "§3.1 D1.4: the audit record carries the disclosure field")
		require.Equal(t, reason, value)
		found = true
	}
	require.True(t, found, "the preview_document.run audit entry was written")

	enforcedDir := t.TempDir()
	enforcedLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: enforcedDir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = enforcedLogger.Close() })
	emitPreviewRunAudit(testContext(t), enforcedLogger, "preview_document", "report.pdf", &preview.Result{
		Validity: "structural",
	})
	enforcedData, err := os.ReadFile(filepath.Join(enforcedDir, "audit.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(enforcedData), "memory_limit_not_enforced",
		"4.7 and N produce no audit field")
}

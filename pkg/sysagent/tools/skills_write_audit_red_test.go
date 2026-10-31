package systools_test

// r15 §5.12, FR-033; CHECK-12. Normal mutation uses real adapters and the
// installed process audit logger. No fabricated Entry or event emitter.
import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillWriteAudit_NormalMutationCompletePayload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	dir := validSkillFixture(t, root, "foo", "audit fixture")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("delete me\n"), 0o644))
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "audit-agent", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	ctx := skillSession(t, deps, "audit-agent")
	// The acting workspace is empty in this fixture and must be represented as
	// such, not guessed. All other actor fields have nonempty fixture values.
	logs := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: logs, RedactEnabled: true})
	require.NoError(t, err)
	tools.SetSkillsWriteAuditLogger(logger)
	t.Cleanup(func() { tools.SetSkillsWriteAuditLogger(nil); require.NoError(t, logger.Close()) })
	read := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "foo", "fields": []any{"instructions"}})
	m := parseToolJSON(t, read.ForLLM)
	require.NotContains(t, m, "error")
	before, ok := m["revision"].(string)
	require.True(t, ok)
	res := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	require.False(t, res.IsError, "normal approved adapter mutation must commit: %s", res.ForLLM)
	_, err = os.Stat(filepath.Join(dir, "notes.md"))
	require.True(t, os.IsNotExist(err))
	after, err := skills.ContentRevision(dir)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	require.NoError(t, logger.Close())
	paths, err := filepath.Glob(filepath.Join(logs, "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "real logger wrote audit bytes")
	committed := []audit.Entry{}
	for _, p := range paths {
		f, err := os.Open(p)
		require.NoError(t, err)
		scan := bufio.NewScanner(f)
		for scan.Scan() {
			var row audit.Entry
			require.NoError(t, json.Unmarshal(scan.Bytes(), &row))
			if row.Event == "skill.write" && row.Details["outcome"] == "committed" {
				committed = append(committed, row)
			}
		}
		require.NoError(t, scan.Err())
		require.NoError(t, f.Close())
	}
	require.Len(t, committed, 1, "exactly one committed event for the one mutation")
	row := committed[0]
	assert.Equal(t, "audit-agent", row.AgentID)
	assert.Equal(t, tools.ToolTranscriptSessionID(ctx), row.SessionID)
	assert.Equal(t, "edit_skill", row.Tool)
	assert.Equal(t, "", row.Details["workspace_id"])
	assert.Equal(t, "foo", row.Details["slug"])
	assert.Equal(t, "registry", row.Details["shelf"])
	assert.Equal(t, "delete_file", row.Details["kind"])
	assert.Equal(t, []any{"notes.md"}, row.Details["paths"])
	assert.Equal(t, before, row.Details["revision_before"])
	assert.Equal(t, after, row.Details["revision_after"])
	assert.Equal(t, "committed", row.Details["outcome"])
	assert.Contains(t, row.Details, "reason")
	assert.Empty(t, row.Details["reason"])
	version, ok := row.Details["version_id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, version)
	raw, err := os.ReadFile(filepath.Join(skills.SkillHistoryRoot(root), "foo", "versions", version+".json"))
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(raw, &manifest))
	assert.Equal(t, version, manifest["id"])
	assert.Equal(t, after, manifest["revision"])
	assert.Equal(t, []any{"notes.md"}, manifest["paths"])
}

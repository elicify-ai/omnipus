package gateway

// D8 T-FT: the real protected-path probe for edit_skill's `path` parameter
// (action=delete_file and its siblings). The path addresses a file inside one
// skill folder; a traversal, an absolute path or a sibling skill's file must
// be refused and must never delete anything outside the folder, while an
// in-folder file is deleted (positive control). Registered through the real
// boot wiring, like the agent sees it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWU23_EditSkillPathProtectsOutsideFiles(t *testing.T) {
	api := l1WU2WiredPermissionAPI(t)
	rc := &runContextWithOptions{
		homePath: api.homePath, configPath: api.configPath(), cfg: api.agentLoop.GetConfig(), agentLoop: api.agentLoop,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools()
	tool, ok := rc.centralBuiltinReg.Get("edit_skill")
	require.True(t, ok, "edit_skill is registered by the real boot wiring")

	root := filepath.Join(api.homePath, "skills")
	create := func(slug string) string {
		_, err := skills.ApplyChange(root, skills.ChangeRequest{
			Kind: skills.KindCreate, Slug: slug,
			Content: []byte("---\nname: " + slug + "\ndescription: probe\n---\n\nBody.\n"),
			Expect:  skills.Expectation{Absent: true},
			Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-create"},
		})
		require.NoError(t, err)
		return filepath.Join(root, slug)
	}
	dir := create("wu23-probe")
	sibling := create("wu23-sibling")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes\n"), 0o644))
	outside := filepath.Join(api.homePath, "wu23-outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("must survive\n"), 0o644))

	const session = "wu23-session"
	call := func(path string) *tools.ToolResult {
		rev, err := skills.ContentRevision(dir)
		require.NoError(t, err)
		skills.RecordSeen(session, "wu23-probe", rev) // the session read the skill (§7.3)
		ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "coverage-main"), session)
		return tool.Execute(ctx, map[string]any{"action": "delete_file", "skill": "wu23-probe", "path": path})
	}

	for _, tc := range []struct{ name, path, survivor string }{
		{"relative traversal", "../../wu23-outside.txt", outside},
		{"absolute path", outside, outside},
		{"sibling skill file", "../wu23-sibling/SKILL.md", filepath.Join(sibling, "SKILL.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := call(tc.path)
			assert.True(t, res.IsError, "an escaping path is refused: %s", res.ForLLM)
			_, statErr := os.Stat(tc.survivor)
			assert.NoError(t, statErr, "the file outside the skill folder survives")
		})
	}

	res := call("notes.md")
	require.False(t, res.IsError, "positive control: an in-folder file is deleted: %s", res.ForLLM)
	_, statErr := os.Stat(filepath.Join(dir, "notes.md"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the in-folder file is gone")
}

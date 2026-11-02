package gateway

// CHECK1 CK4 on REST and CK5 on the agent path.
//
// CK4 (r15 §5.6/§5.10, FR-045): POST /api/v1/skills/install onto a built-in
// name answers 409 BUILTIN_NAME_RESERVED, with and without a revision, even
// though the built-in folder is present on disk. Failability: before commit
// 4b9ff54b1 the handler answered its own "already installed; revision is
// required" 400 first (no revision) and BUILTIN_READ_ONLY 403 (with revision).
//
// CK5 (founder Q-S3): edit_skill action=delete, through the gateway's real
// system-tool wiring, removes the deleted skill's grant from stored agent
// records, like the REST delete. Failability: before commit 0d0abfc54 the
// agent path passed no grant editor, so the stale row stayed.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestInstall_BuiltinNameAnswersReserved(t *testing.T) {
	for _, tc := range []struct {
		name     string
		slug     string
		revision bool
	}{
		{"without revision", "plan", false},
		{"with revision", "plan", true},
		{"case alias", "PLAN", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithClawHub(t, true, "")
			api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Version: "1.0.0"}}
			root := filepath.Join(api.homePath, "skills")
			dir := filepath.Join(root, "plan")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			original := []byte("---\nname: plan\ndescription: built-in plan\n---\nBUILT-IN BODY\n")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), original, 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-builtin"), []byte("omnipus\n"), 0o644))

			body := `{"slug":"` + tc.slug + `"`
			if tc.revision {
				rev, err := skills.ContentRevision(dir)
				require.NoError(t, err)
				body += `,"revision":"` + rev + `"`
			}
			body += `}`
			w := t9Do(api.HandleSkills, http.MethodPost, "/api/v1/skills/install", "", body)

			require.Equal(t, http.StatusConflict, w.Code, "built-in name is reserved for every install: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "BUILTIN_NAME_RESERVED")
			assert.Contains(t, w.Body.String(), "Choose a different name.")
			after, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			require.NoError(t, err)
			assert.Equal(t, original, after, "the built-in folder is untouched")
			_, statErr := os.Stat(filepath.Join(root, "PLAN"))
			if tc.slug == "PLAN" {
				// A case-insensitive volume resolves PLAN to plan; a case-sensitive one must not gain a twin.
				entries, readErr := os.ReadDir(root)
				require.NoError(t, readErr)
				count := 0
				for _, e := range entries {
					if e.IsDir() && (e.Name() == "plan" || e.Name() == "PLAN") {
						count++
					}
				}
				assert.Equal(t, 1, count, "no second folder for the alias (stat err: %v)", statErr)
			}
		})
	}
}

func TestEditSkillDelete_AgentPathRemovesGrantThroughGatewayWiring(t *testing.T) {
	api := l1WU2WiredPermissionAPI(t)
	rc := &runContextWithOptions{
		homePath: api.homePath, configPath: api.configPath(), cfg: api.agentLoop.GetConfig(), agentLoop: api.agentLoop,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools() // actual production wiring, including Deps.SkillGrants

	root := filepath.Join(api.homePath, "skills")
	out, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: "ck5-doomed",
		Content: []byte("---\nname: ck5-doomed\ndescription: to be deleted\n---\n\nBody.\n"),
		Expect:  skills.Expectation{Absent: true},
		Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-create"},
	})
	require.NoError(t, err)
	_, err = skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: "ck5-keeper",
		Content: []byte("---\nname: ck5-keeper\ndescription: stays\n---\n\nBody.\n"),
		Expect:  skills.Expectation{Absent: true},
		Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-create"},
	})
	require.NoError(t, err)

	store := agentstore.New(api.homePath)
	_, err = store.Update("coverage-main", func(a *config.AgentConfig) error {
		a.Skills = []string{"ck5-doomed", "ck5-keeper"}
		return nil
	})
	require.NoError(t, err)
	before, err := store.Get("coverage-main")
	require.NoError(t, err)
	require.Contains(t, before.Skills, "ck5-doomed", "positive control: the stored record holds the grant")

	tool, ok := rc.centralBuiltinReg.Get("edit_skill")
	require.True(t, ok, "edit_skill is registered by the real boot wiring")
	const session = "ck5-session"
	skills.RecordSeen(session, "ck5-doomed", out.Revision) // the session read the skill (§7.3)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "coverage-main"), session)
	res := tool.Execute(ctx, map[string]any{"action": "delete", "skill": "ck5-doomed"})
	require.False(t, res.IsError, "the agent delete must commit: %s", res.ForLLM)
	_, statErr := os.Stat(filepath.Join(root, "ck5-doomed"))
	require.True(t, os.IsNotExist(statErr), "the folder is gone")

	after, err := store.Get("coverage-main")
	require.NoError(t, err)
	assert.NotContains(t, after.Skills, "ck5-doomed", "Q-S3: the deleted skill's grant is removed on the agent path too")
	assert.Contains(t, after.Skills, "ck5-keeper", "unrelated grants are preserved")
}

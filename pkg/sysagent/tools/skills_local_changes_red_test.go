package systools_test

// r15 §9.2, S-9.2/4: get_skill computes local state at read time without
// recording detection history. Real installed/edit/restore history and real
// reader; outside/no-history inputs are deliberately external fixture input.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSkill_HasLocalChangesReadTimeMatrix(t *testing.T) {
	for _, state := range []string{"installed", "edited", "restored-install", "outside-undetected", "no-history"} {
		t.Run(state, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "skills")
			if state == "no-history" {
				validSkillFixture(t, root, "foo", "outside fixture")
			} else {
				staged := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"), []byte("---\nname: foo\ndescription: installed fixture\n---\nINSTALLED\n"), 0o644))
				out, id := projectionCommit(t, root, skills.ChangeRequest{Kind: skills.KindInstall, Slug: "foo", StagedDir: staged, Expect: skills.Expectation{Absent: true}, Actor: skills.ChangeActor{Kind: "person", Tool: "rest-install"}})
				switch state {
				case "edited":
					projectionCommit(t, root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "notes.md", Content: []byte("managed edit\n"), Expect: skills.Expectation{Revision: out.Revision}, Actor: skills.ChangeActor{Kind: "person", Tool: "write_file"}})
				case "restored-install":
					projectionCommit(t, root, skills.ChangeRequest{Kind: skills.KindRestore, Slug: "foo", Version: id, Expect: skills.Expectation{Revision: out.Revision}, Actor: skills.ChangeActor{Kind: "person", Tool: "edit_skill"}})
				case "outside-undetected":
					require.NoError(t, os.WriteFile(filepath.Join(root, "foo", "notes.md"), []byte("outside edit\n"), 0o644))
				}
			}
			manifests := func() []string {
				paths, err := filepath.Glob(filepath.Join(skills.SkillHistoryRoot(root), "foo", "versions", "*.json"))
				require.NoError(t, err)
				return paths
			}
			before := manifests()
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
			res := systools.NewGetSkillTool(deps).Execute(readCtx("editor"), map[string]any{"id": "foo", "fields": []any{"has_local_changes"}})
			m := parseToolJSON(t, res.ForLLM)
			require.NotContains(t, m, "error", "successful authorized local-state read: %s", res.ForLLM)
			assert.Equal(t, state != "installed", m["has_local_changes"])
			assert.Equal(t, before, manifests(), "ordinary read never writes baseline/external_change history")
		})
	}
}

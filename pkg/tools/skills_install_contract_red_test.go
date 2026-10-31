package tools

// r15 §5.6/§9.2, S-6.11 and S-9.3; CHECK-12. Real install_skill and
// ApplyChange; only the external registry transport is fake. No prior Skill
// read or seeded seen record is used: install owns its request expectation.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallSkill_RequestExpectationAndLocalConfirmation(t *testing.T) {
	for _, state := range []string{"absent", "installed-current-no-read", "stale", "missing-revision", "local-refused", "local-confirmed"} {
		t.Run(state, func(t *testing.T) {
			home := realTempDir(t)
			root := filepath.Join(home, "skills")
			mgr := skills.NewRegistryManager()
			mgr.AddRegistry(fakeSkillRegistry{})
			tool := NewInstallSkillTool(mgr, root)
			rev := ""
			if state != "absent" {
				stage := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte("---\nname: x\ndescription: installed prerequisite\n---\nORIGINAL\n"), 0o644))
				out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindInstall, Slug: "x", StagedDir: stage, Expect: skills.Expectation{Absent: true}, Actor: skills.ChangeActor{Kind: "person", Tool: "rest-install"}})
				require.NoError(t, err, "BLOCKED: real installed-state prerequisite not implemented — r15 §7.1")
				rev = out.Revision
				if strings.HasPrefix(state, "local-") {
					out, err = skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "x", Path: "notes.md", Content: []byte("LOCAL CONTENT\n"), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "person", Tool: "write_file"}})
					require.NoError(t, err)
					rev = out.Revision
				}
			}
			args := map[string]any{"slug": "x", "registry": "fake"}
			if state != "absent" {
				args["force"] = true
				if state != "missing-revision" {
					args["revision"] = rev
				}
			}
			if state == "stale" {
				args["revision"] = strings.Repeat("0", 64)
			}
			if state == "local-confirmed" {
				args["replace_local_changes"] = true
			}
			var before []byte
			if state != "absent" {
				var err error
				before, err = os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
				require.NoError(t, err)
			}
			result := tool.Execute(WithAgentID(context.Background(), "installer"), args)
			switch state {
			case "stale", "missing-revision", "local-refused":
				require.True(t, result.IsError, "request must be refused: %s", result.ForLLM)
				if state == "stale" {
					assert.Contains(t, result.ForLLM, "SKILL_CHANGED")
				}
				if state == "missing-revision" {
					assert.Contains(t, result.ForLLM, "revision is required")
				}
				if state == "local-refused" {
					assert.Contains(t, result.ForLLM, "LOCAL_CHANGES_CONFIRMATION_REQUIRED")
				}
				raw, err := os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
				require.NoError(t, err)
				assert.Equal(t, before, raw, "refused request preserves the package")
			default:
				require.False(t, result.IsError, "request expectation, not seen, authorizes installation: %s", result.ForLLM)
				assert.NotContains(t, result.ForLLM, "SKILL_NOT_READ")
				raw, err := os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
				require.NoError(t, err)
				assert.Equal(t, "---\nname: x\ndescription: a fake test skill for install_skill's global-dir test\n---\n\nBody.\n", string(raw))
				paths, err := filepath.Glob(filepath.Join(skills.SkillHistoryRoot(root), "x", "versions", "*.json"))
				require.NoError(t, err)
				require.NotEmpty(t, paths, "successful real adapter must commit its version, not use legacy publication")
				raw, err = os.ReadFile(paths[len(paths)-1])
				require.NoError(t, err)
				var last map[string]any
				require.NoError(t, json.Unmarshal(raw, &last))
				kind := "install_replace"
				if state == "absent" {
					kind = "install"
				}
				assert.Equal(t, kind, last["kind"])
				if state == "local-confirmed" {
					assert.GreaterOrEqual(t, len(paths), 3, "the local predecessor remains in history")
				}
				_, err = os.Stat(filepath.Join(root, "x", ".skill-origin.json"))
				assert.True(t, os.IsNotExist(err), "no legacy origin metadata is written")
			}
		})
	}
}

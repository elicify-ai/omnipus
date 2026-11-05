package systools_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func skillTreeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = string(content)
		return nil
	}))
	return files
}

// r15 replaces legacy create/edit/remove with copy/delete_file/delete.
// Delegation and unattended context do not impose an owner-session gate.
// Missing session is now explicitly distinguished by the §7.3 seen rule.
func TestR15SkillActions_ContextAndSeenRequirements(t *testing.T) {
	for _, operation := range []string{"copy", "delete_file", "delete"} {
		for _, mode := range []string{"attended", "delegated", "unattended", "missing-session"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				global := filepath.Join(root, "skills")
				require.NoError(t, os.MkdirAll(global, 0o755))
				deps := newAuthoringDeps(t, global, "")
				const name = "guard-test"
				validSkillFixture(t, global, name, "A context safety fixture")
				helper := filepath.Join(global, name, "helper.txt")
				require.NoError(t, os.WriteFile(helper, []byte("helper"), 0o600))
				ctx := tools.WithAgentID(context.Background(), "editor")
				if mode != "missing-session" {
					ctx = tools.WithTranscriptSessionID(ctx, t.Name())
				}
				if mode == "delegated" {
					ctx = tools.WithDelegationDepth(ctx, 1)
				}
				if mode == "unattended" {
					ctx = tools.WithAutoDenyAsk(ctx, true)
				}
				if operation != "copy" {
					ctx = observeSkillForMutation(t, deps, ctx, name)
				}
				args := map[string]any{"action": operation, "skill": name}
				if operation == "copy" {
					args["to"] = "guard-copy"
					deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
				}
				if operation == "delete_file" {
					args["path"] = "helper.txt"
				}
				before := skillTreeBytes(t, root)
				result := systools.NewSkillEditTool(deps).Execute(ctx, args)
				after := skillTreeBytes(t, root)
				if mode == "missing-session" && operation != "copy" {
					wu3Error(t, result, "SKILL_NOT_READ")
					require.Equal(t, before, after, "missing seen record must refuse without any disk changes")
					return
				}
				wu3Success(t, result)
				require.NotEqual(t, before, after, "an allowed supported action must change real bytes")
				switch operation {
				case "copy":
					require.Equal(t, validSkillBody(t, global, name), validSkillBody(t, global, "guard-copy"))
					require.FileExists(t, helper)
				case "delete_file":
					require.NoFileExists(t, helper)
					require.FileExists(t, filepath.Join(global, name, "SKILL.md"))
				case "delete":
					require.NoDirExists(t, filepath.Join(global, name))
				}
			})
		}
	}
}

func validSkillBody(t *testing.T, root, slug string) string {
	t.Helper()
	body, ok := skills.NewSkillsLoader("", root, "").LoadSkill(slug)
	require.True(t, ok, "committed source/copy must load")
	return body
}

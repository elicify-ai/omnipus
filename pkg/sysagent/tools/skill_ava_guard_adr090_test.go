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

func TestADR090_AvaSkillWritesRequireOwnerSession(t *testing.T) {
	for _, operation := range []string{"create", "edit", "remove"} {
		for _, mode := range []string{"attended", "delegated", "unattended", "missing-session"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				global := filepath.Join(root, "skills")
				require.NoError(t, os.MkdirAll(global, 0755))
				deps := newAuthoringDeps(t, global, "")
				installer, err := skills.NewSkillInstaller(root, "", "")
				require.NoError(t, err)
				deps.SkillInstaller = installer
				const name = "guard-test"
				if operation != "create" {
					_, err := deps.SkillWriter.CreateSkill(name, validSkill(name))
					require.NoError(t, err)
				}
				ctx := tools.WithAgentID(context.Background(), "ava")
				if mode != "missing-session" {
					ctx = tools.WithTranscriptSessionID(ctx, "owner-session")
				}
				if mode == "delegated" {
					ctx = tools.WithDelegationDepth(ctx, 1)
				}
				if mode == "unattended" {
					ctx = tools.WithAutoDenyAsk(ctx, true)
				}
				var tool tools.Tool
				switch operation {
				case "create":
					tool = systools.NewSkillCreateTool(deps)
				case "edit":
					tool = systools.NewSkillEditTool(deps)
				case "remove":
					tool = systools.NewSkillRemoveTool(deps)
				}
				before := skillTreeBytes(t, root)
				result := tool.Execute(ctx, map[string]any{"name": name, "content": validSkill(name) + "\nChanged.\n", "confirm": true})
				after := skillTreeBytes(t, root)
				if mode == "attended" {
					require.False(t, result.IsError, result.ForLLM)
					require.NotEqual(t, before, after, "positive control must perform a real write")
				} else {
					require.True(t, result.IsError, result.ForLLM)
					require.Contains(t, result.ForLLM, "DELEGATED_WRITE_FORBIDDEN")
					require.Equal(t, before, after, "forbidden Ava must perform zero file changes")
				}
			})
		}
	}
}

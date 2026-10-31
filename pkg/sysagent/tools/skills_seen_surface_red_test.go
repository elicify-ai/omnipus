package systools_test

// r15 §5.4, §7.3, FR-022/FR-026; CHECK-01/02/12.
// Plan: exercise real get_skill and edit_skill in two real sessions. No seen
// map is seeded or mocked: only a successful read may establish the record.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillSession(t *testing.T, deps *systools.Deps, agent string) context.Context {
	t.Helper()
	store := liveSessionStore(deps)
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agent)
	require.NoError(t, err)
	return tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), agent), meta.ID)
}

func TestEditSkill_PublicActionSet(t *testing.T) {
	tool := systools.NewSkillEditTool(nil)
	// Round-trip only the parameter schema, not a handwritten product carrier.
	raw, err := json.Marshal(tool.Parameters())
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"action", "skill", "to", "path", "version"}, mapKeys(props), "r15 §5.4 exact public parameter set")
	require.Contains(t, props, "action", "BLOCKED: edit_skill action schema not implemented — r15 §5.4")
	action, ok := props["action"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"copy", "delete_file", "delete", "restore"}, action["enum"])
	assert.ElementsMatch(t, []any{"action", "skill"}, schema["required"])
	assert.Equal(t, false, schema["additionalProperties"], "unknown parameters are not accepted by the public schema")
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestEditSkill_RejectsUnknownAndMisplacedParameters(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	validSkillFixture(t, root, "foo", "fixture")
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", grants: []string{"foo"}, policies: map[string]string{"edit_skill": "allow", "get_skill": "allow"}})
	for _, tc := range []struct {
		action, key string
		value       any
	}{
		{"copy", "content", "body"}, {"copy", "revision", "obsolete"}, {"copy", "path", "notes.md"},
		{"delete_file", "to", "dst"}, {"delete", "version", "v000001-20260101T000000Z"},
		{"restore", "mystery", true},
	} {
		t.Run(tc.action+"/"+tc.key, func(t *testing.T) {
			args := map[string]any{"action": tc.action, "skill": "foo", tc.key: tc.value}
			if tc.action == "copy" {
				args["to"] = "dst"
			}
			if tc.action == "delete_file" {
				args["path"] = "notes.md"
			}
			if tc.action == "restore" {
				args["version"] = "v000001-20260101T000000Z"
			}
			res := systools.NewSkillEditTool(deps).Execute(readCtx("editor"), args)
			code, msg := errorBlock(t, res)
			assert.Equal(t, "INVALID_INPUT", code)
			assert.Contains(t, msg, tc.key, "the actual misplaced parameter is named")
		})
	}
}

func TestSkillSeen_ReadThenMutation_RevisionAndSessionBound(t *testing.T) {
	for _, mode := range []string{"matching-read", "other-session", "stale-read", "partial-clears-seen"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "skills")
			dir := validSkillFixture(t, root, "foo", "before")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("remove me\n"), 0o644))
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", grants: []string{"foo"}, policies: map[string]string{"edit_skill": "allow", "get_skill": "allow"}})
			ctx := skillSession(t, deps, "editor")
			edit := systools.NewSkillEditTool(deps)
			mutate := func(ctx context.Context, path string) *tools.ToolResult {
				return edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": path})
			}
			code, msg := errorBlock(t, mutate(ctx, "notes.md"))
			require.Equal(t, "SKILL_NOT_READ", code, "a mutation cannot invent a seen revision")
			assert.Equal(t, "read skill 'foo' first (Skill, get_skill, read_file or list_directory), then retry", msg)
			read := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "foo", "fields": []any{"instructions", "description"}})
			m := parseToolJSON(t, read.ForLLM)
			require.NotContains(t, m, "error")
			assert.Equal(t, "before", m["description"])
			assert.Contains(t, m["instructions"], "Body of foo.")
			observed, err := skills.ContentRevision(dir)
			require.NoError(t, err)
			assert.Equal(t, observed, m["revision"])
			switch mode {
			case "other-session":
				code, _ = errorBlock(t, mutate(skillSession(t, deps, "editor"), "notes.md"))
				assert.Equal(t, "SKILL_NOT_READ", code)
				raw, err := os.ReadFile(filepath.Join(dir, "notes.md"))
				require.NoError(t, err)
				assert.Equal(t, "remove me\n", string(raw))
			case "stale-read":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "outside.md"), []byte("outside change\n"), 0o644))
				code, msg = errorBlock(t, mutate(ctx, "notes.md"))
				assert.Equal(t, "SKILL_CHANGED", code)
				assert.Contains(t, msg, "changed since you read it")
				assert.FileExists(t, filepath.Join(dir, "notes.md"))
			case "partial-clears-seen":
				fired := false
				old := skills.FaultManifestWrite
				t.Cleanup(func() { skills.FaultManifestWrite = old })
				skills.FaultManifestWrite = func(string) error {
					if _, err := os.Stat(filepath.Join(dir, "notes.md")); os.IsNotExist(err) {
						fired = true
						return os.ErrPermission
					}
					return nil
				}
				code, _ = errorBlock(t, mutate(ctx, "notes.md"))
				require.True(t, fired, "post-delete-file manifest hook fires only after publication")
				require.Equal(t, "SKILL_PARTIALLY_SAVED", code)
				skills.FaultManifestWrite = old
				code, _ = errorBlock(t, mutate(ctx, "outside.md"))
				assert.Equal(t, "SKILL_NOT_READ", code, "partial clears the matching seen record, rather than leaving a stale revision")
			default:
				out := mutate(ctx, "notes.md")
				require.False(t, out.IsError, "recorded revision must equal the captured read: %s", out.ForLLM)
				_, err := os.Stat(filepath.Join(dir, "notes.md"))
				assert.True(t, os.IsNotExist(err))
				assert.Contains(t, out.ForLLM, "saved as skill version")
			}
		})
	}
}

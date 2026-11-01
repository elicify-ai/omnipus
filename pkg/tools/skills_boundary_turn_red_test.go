package tools

// RED — r15 §6.1, S-2.1–S-2.4 and CHECK-08. Real persisted agent, real
// ResolveTurnFSPolicy and real read/list/grep/write tools. Predicate-only
// tests in fspolicy are supplementary, never evidence for this wiring.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillsTurnFixture(t *testing.T, editor bool) (context.Context, string, string) {
	t.Helper()
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)
	agentHome := filepath.Join(home, "agents", "skills-reader")
	require.NoError(t, os.MkdirAll(agentHome, 0o755))
	for _, slug := range []string{"granted", "other"} {
		dir := filepath.Join(home, "skills", slug)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: fixture\n---\nBODY-"+slug+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "helper.md"), []byte("MARKER-"+slug+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte("11111111-2222-3333-4444-555555555555\n"), 0o644))
	}
	a := config.AgentConfig{ID: "skills-reader", Skills: []string{"granted"}, Tools: &config.AgentToolsCfg{}}
	a.Tools.Builtin.Policies = map[string]config.ToolPolicy{"edit_skill": config.ToolPolicyDeny, "read_file": config.ToolPolicyAllow, "write_file": config.ToolPolicyAllow, "list_directory": config.ToolPolicyAllow, "grep": config.ToolPolicyAllow}
	if editor {
		a.Tools.Builtin.Policies["edit_skill"] = config.ToolPolicyAsk
	}
	require.NoError(t, agentstore.New(home).Create(a.ID, &a))
	// Input record carries the r15 field even before the Go struct grows it.
	// This is fixture setup, not a mock of the resolver or any file tool.
	p := filepath.Join(home, "entities", "agents", a.ID+".json")
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record))
	record["skill_grants"] = map[string]string{"granted": "11111111-2222-3333-4444-555555555555"}
	raw, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, raw, 0o600))
	require.NoError(t, json.Unmarshal(raw, &a)) // config snapshot and durable fixture describe the same binding
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{a}
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	ctx := WithTranscriptSessionID(WithAgentID(context.Background(), a.ID), "skills-boundary-session")
	return ctx, home, agentHome
}

func TestSkillsBoundary_RealTurnReadListGrep(t *testing.T) {
	for _, editor := range []bool{false, true} {
		label := "non-editor"
		if editor {
			label = "editor"
		}
		t.Run(label, func(t *testing.T) {
			ctx, home, agentHome := skillsTurnFixture(t, editor)
			pol, err := ResolveTurnFSPolicy(ctx, agentHome, true)
			require.NoError(t, err)
			require.NotEmpty(t, pol.CarveOuts, "real constructor must populate protection")
			for _, slug := range []string{"granted", "other"} {
				wantAllowed := slug == "granted" || editor
				for _, kind := range []string{"read_file", "list_directory", "grep"} {
					t.Run(slug+"/"+kind, func(t *testing.T) {
						dir := filepath.Join(home, "skills", slug)
						var res *ToolResult
						switch kind {
						case "read_file":
							res = NewReadFileTool(agentHome, true, 1<<20).Execute(ctx, map[string]any{"path": filepath.Join(dir, "SKILL.md")})
						case "list_directory":
							res = NewListDirTool(agentHome, true).Execute(ctx, map[string]any{"path": dir})
						case "grep":
							res = NewGrepTool(agentHome, true).Execute(ctx, map[string]any{"path": filepath.Join(dir, "helper.md"), "pattern": "MARKER-" + slug})
						}
						require.NotNil(t, res)
						if wantAllowed {
							require.False(t, res.IsError, "authorized %s must reach real content: %s", kind, res.ForLLM)
							if kind == "read_file" {
								assert.Contains(t, res.ForLLM, "BODY-"+slug)
							} else if kind == "grep" {
								assert.Contains(t, res.ForLLM, "MARKER-"+slug)
							} else {
								assert.Contains(t, res.ForLLM, "helper.md")
							}
						} else {
							assert.NotContains(t, res.ForLLM, "MARKER-other", "ungranted data never leaks")
							assert.True(t, res.IsError, "ungranted ordinary folder must be refused: %s", res.ForLLM)
						}
					})
				}
			}
			guard := reflect.ValueOf(pol.HomeGuard)
			folders := guard.FieldByName("SkillFolders")
			all := guard.FieldByName("SkillsReadAll")
			if !folders.IsValid() || !all.IsValid() {
				t.Fatal("BLOCKED: real turn skill authority construction not implemented — required by r15 §6.1")
			}
			assert.Contains(t, folders.Interface(), filepath.Join(home, "skills", "granted"))
			assert.Equal(t, editor, all.Bool())
		})
	}
}

func TestSkillsBoundary_RealToolsCannotBypassWithOperatorPattern(t *testing.T) {
	ctx, home, agentHome := skillsTurnFixture(t, false)
	broad := []*regexp.Regexp{regexp.MustCompile(".*")}
	path := filepath.Join(home, "skills", "other", "helper.md")
	res := NewReadFileTool(agentHome, true, 1<<20, broad).Execute(ctx, map[string]any{"path": path})
	assert.True(t, res.IsError, "operator allow pattern cannot re-admit an ungranted ordinary skill: %s", res.ForLLM)
	res = NewWriteFileTool(agentHome, true, broad).Execute(ctx, map[string]any{"path": path, "content": "UNAUTHORIZED"})
	assert.True(t, res.IsError, "file allow cannot bypass edit_skill deny: %s", res.ForLLM)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "MARKER-other\n", string(raw))
}

// r15 §10.1 item 7 / T-P2(e): resolution alone is not an approved write.
// The production handle, not a stub, must enforce the loop-context backstop.
func TestSkillPolicy_PathHandleWithoutCompositionMarkerRefused(t *testing.T) {
	ctx, home, agentHome := skillsTurnFixture(t, true)
	pol, err := ResolveTurnFSPolicy(ctx, agentHome, true)
	require.NoError(t, err)
	path := filepath.Join(home, "skills", "granted", "helper.md")
	handle, err := ResolvePath(ctx, pol, "write_file", "uncomposed-call", FSOpWrite, path)
	require.NoError(t, err, "BLOCKED: editor registry-target resolution not implemented — r15 §6.1/§10.1")
	require.NotNil(t, handle)
	t.Cleanup(func() { require.NoError(t, handle.Close()) })
	err = handle.WriteFile([]byte("UNAPPROVED CONTENT"))
	require.ErrorContains(t, err, "SKILL_WRITE_NOT_APPROVED", "only the loop can attest the composed skill key")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "MARKER-granted\n", string(raw), "direct caller changed no bytes")
	_, err = os.Stat(filepath.Join(home, "system", "skill-history", "granted"))
	assert.True(t, os.IsNotExist(err), "backstop refusal runs before the mutation pipeline")
}

func TestSkillsBoundary_InventoryWinsEveryEnclosingAuthority(t *testing.T) {
	ctx, home, agentHome := skillsTurnFixture(t, false)
	base, err := ResolveTurnFSPolicy(ctx, agentHome, true)
	require.NoError(t, err)
	path := filepath.Join(home, "skills", "other", "helper.md")
	control := filepath.Join(home, "ordinary-control.txt")
	require.NoError(t, os.WriteFile(control, []byte("ENCLOSING-MOUNT-CONTROL\n"), 0o600))
	require.True(t, fspolicy.IsCarveOut(control, base), "control starts outside every base allowance")
	for _, authority := range []string{"mount", "operator-path-grant", "setup-root", "workdir"} {
		t.Run(authority, func(t *testing.T) {
			pol := base
			var validationCategory string
			switch authority {
			case "mount":
				pol.AllowedRoots = []string{home}
				pol.HomeGuard.Allow = append(append([]fspolicy.AllowRoot(nil), base.HomeGuard.Allow...), fspolicy.AllowRoot{Path: home, Writable: true})
			case "operator-path-grant":
				pol.PathGrants = []fspolicy.PathGrant{{Path: home, Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite}}
				validationCategory = "PathGrants entry"
			case "setup-root":
				pol.SetupRoots = []string{home}
				validationCategory = "SetupRoots entry"
			case "workdir":
				pol.WorkDir = home
				validationCategory = "WorkDir"
			}
			// §6.1 inventory precedence is supplementary for malformed policies:
			// FSPolicy.Validate rejects these enclosing grants before resolution.
			assert.True(t, fspolicy.IsCarveOut(path, pol), "inventory must veto the ungranted skill under %s", authority)
			wantErr := ErrCarveOut
			if validationCategory != "" {
				require.ErrorContains(t, pol.Validate(), validationCategory)
				wantErr = ErrPathInvalid
			} else {
				require.NoError(t, pol.Validate(), "valid mount reaches the inventory decision")
				h, controlErr := ResolvePath(ctx, pol, "read_file", "mount-control", FSOpRead, control)
				require.NoError(t, controlErr, "enclosing mount must actually admit an ordinary file")
				require.NotNil(t, h)
				b, readErr := h.ReadFile()
				require.NoError(t, h.Close())
				require.NoError(t, readErr)
				assert.Equal(t, "ENCLOSING-MOUNT-CONTROL\n", string(b))
			}
			handle, err := ResolvePath(ctx, pol, "read_file", "boundary-call", FSOpRead, path)
			if handle != nil {
				require.NoError(t, handle.Close())
			}
			assert.Nil(t, handle, "refusal must not expose a handle under %s", authority)
			assert.ErrorIs(t, err, wantErr, "enclosing authority cannot admit an ungranted skill under %s", authority)
		})
	}
}

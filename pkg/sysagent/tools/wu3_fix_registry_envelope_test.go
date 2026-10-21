package systools_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wu3ChangeFamilies is every change tool whose result carries the FR-030
// envelope (WU3 rulings Q2; CHECK Q2=A adds create/delete_workspace).
var wu3ChangeFamilies = []string{
	"create_agent", "update_agent", "delete_agent", "set_agent_permissions",
	"create_workspace", "update_workspace", "delete_workspace",
	"configure_provider", "add_mcp_server", "remove_mcp_server",
	"configure_channel", "enable_channel", "disable_channel",
	"create_skill", "edit_skill", "remove_skill", "install_skill", "environment_setup",
}

// wu3DurableInventory snapshots every file under the stores a change tool can
// write (entities, agent homes, workspaces, skills) plus the agent store's own
// listing. A refusal that saved nothing leaves it identical. An unreadable
// record fails the snapshot instead of hiding in it.
func wu3DurableInventory(t *testing.T, home string) []string {
	t.Helper()
	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "an unreadable agent record would hide a new entity")
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, "agent:"+a.ID)
	}
	out = append(out, wu3FileInventory(t, home)...)
	sort.Strings(out)
	return out
}

// wu3FileInventory is every file (with its bytes) and folder under the
// durable stores a change tool can write.
func wu3FileInventory(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"entities", "agents", "workspaces", "skills"} {
		root := filepath.Join(home, dir)
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && path == root {
					return nil
				}
				return err
			}
			rel, _ := filepath.Rel(home, path)
			if d.IsDir() {
				out = append(out, "dir:"+rel)
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out = append(out, "file:"+rel+":"+string(data))
			return nil
		})
		require.NoError(t, walkErr)
	}
	sort.Strings(out)
	return out
}

func wu3FullChangeRegistry(d *systools.Deps) *tools.ToolRegistry {
	reg := systools.BuildRegistry(d)
	reg.Register(tools.NewInstallSkillTool(skills.NewRegistryManager(), filepath.Join(d.Home, "skills")))
	reg.Register(tools.NewEnvironmentSetupTool(tools.EnvironmentSetupToolDeps{Home: d.Home}))
	return reg
}

// wrongTypeArgs returns a call whose one string property carries a number,
// preferring a required one, so the registry's schema check refuses it.
func wrongTypeArgs(t *testing.T, tool tools.Tool) map[string]any {
	t.Helper()
	params := tool.Parameters()
	props, _ := params["properties"].(map[string]any)
	var candidates []string
	if req, ok := params["required"].([]string); ok {
		candidates = append(candidates, req...)
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	candidates = append(candidates, names...)
	for _, name := range candidates {
		if p, ok := props[name].(map[string]any); ok && p["type"] == "string" {
			return map[string]any{name: float64(42)}
		}
	}
	t.Fatalf("%s declares no string property to mistype", tool.Name())
	return nil
}

// CHECK F01 / type-design T5: the real registry refuses a schema-invalid call
// before the tool's Execute runs. That refusal is still a change-tool result:
// it must carry the FR-030 nothing-saved envelope, keep the registry's own
// diagnostic, and leave every durable store unchanged.
func TestWU3RegistrySchemaRefusalCarriesNothingSavedEnvelope(t *testing.T) {
	d, _ := wu3Deps(t)
	wu3Workspace(t, d, "default")
	reg := wu3FullChangeRegistry(d)
	beforeCfg := wu3ReadFile(t, d.ConfigPath)
	beforeStores := wu3DurableInventory(t, d.Home)
	for _, name := range wu3ChangeFamilies {
		tool, ok := reg.Get(name)
		require.True(t, ok, "%s must be registered", name)
		cases := map[string]map[string]any{"wrong type": wrongTypeArgs(t, tool)}
		if req, _ := tool.Parameters()["required"].([]string); len(req) > 0 {
			cases["missing required"] = map[string]any{}
		}
		for label, args := range cases {
			t.Run(name+"/"+label, func(t *testing.T) {
				result := reg.ExecuteWithContext(context.Background(), name, args, "", "", nil)
				require.True(t, result.IsError, "schema-invalid call must be refused: %s", result.ForLLM)
				body := wu3Outcome(t, result, "failed: nothing saved")
				assert.Equal(t, []any{}, body["changed_fields"], "a schema refusal has no durable parts")
				assert.Contains(t, result.ForLLM, "invalid arguments for tool", "the registry's diagnostic is kept")
				assert.Equal(t, beforeCfg, wu3ReadFile(t, d.ConfigPath), "configuration unchanged")
				assert.Equal(t, beforeStores, wu3DurableInventory(t, d.Home), "durable stores unchanged")
			})
		}
	}
}

// Controls for the registry envelope: a valid change call through the same
// registry still reaches Execute and saves; a read tool's schema refusal is not
// a change result and stays the registry's plain diagnostic.
func TestWU3RegistrySchemaEnvelopeControls(t *testing.T) {
	d, _ := wu3Deps(t)
	wu3Workspace(t, d, "default")
	reg := wu3FullChangeRegistry(d)
	valid := reg.ExecuteWithContext(context.Background(), "create_workspace",
		map[string]any{"name": "Registry Control"}, "", "", nil)
	require.False(t, valid.IsError, valid.ForLLM)
	body := wu3JSON(t, valid)
	assert.NotEqual(t, "failed: nothing saved", body["outcome"], "a valid call is executed, not refused")
	assert.NotEmpty(t, body["changed_fields"], "the valid control really saved")

	read := reg.ExecuteWithContext(context.Background(), "get_agent", map[string]any{"id": float64(1)}, "", "", nil)
	require.True(t, read.IsError, "control: the read call is schema-invalid")
	assert.True(t, strings.HasPrefix(read.ForLLM, "invalid arguments for tool"),
		"a read tool's refusal is not wrapped in a change verdict: %s", read.ForLLM)
	assert.NotContains(t, read.ForLLM, "outcome")
}

// requireNoDurableChange fails when a refusal left any durable trace: with
// generated UUID ids a lookup by a name-derived slug can no longer prove that
// no agent was saved (WU-3 CHECK F03), so the whole inventory is compared.
func requireNoDurableChange(t *testing.T, home string, before []string, msg string) {
	t.Helper()
	assert.Equal(t, before, wu3DurableInventory(t, home), msg)
}

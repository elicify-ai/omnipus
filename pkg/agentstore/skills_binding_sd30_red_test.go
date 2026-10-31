package agentstore

// RED — r15 §5.13a SD-30 / S-6.18. Real entity persistence, not a map supplied
// to a loader. All four public create/update paths derive bindings wholesale
// from their own observation. Q-S3 recommended A affects UI/error wording,
// not these identity mechanics. No deletion-time cleanup is expected.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sd30G1 = "11111111-2222-3333-4444-555555555555"
const sd30G2 = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func sd30Folder(t *testing.T, home, slug, generation string) string {
	t.Helper()
	dir := filepath.Join(home, "skills", slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: generation fixture\n---\nbody\n"), 0o644))
	if generation != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
	}
	return dir
}

func sd30Bindings(t *testing.T, home, id string) map[string]string {
	t.Helper()
	// Independent disk read: a cache-only update cannot satisfy this oracle.
	raw, err := os.ReadFile(filepath.Join(home, "entities", "agents", id+".json"))
	require.NoError(t, err)
	var row map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &row))
	bindings := map[string]string{}
	if b, ok := row["skill_grants"]; ok {
		require.NoError(t, json.Unmarshal(b, &bindings))
	}
	return bindings
}

func TestSD30_BindAtPersist_CreatePaths(t *testing.T) {
	for _, path := range []string{"Create", "CreateState"} {
		t.Run(path, func(t *testing.T) {
			for _, observation := range []string{"live-G1", "absent", "unstamped", "recreated-G2"} {
				t.Run(observation, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv(config.EnvHome, home)
					want := ""
					switch observation {
					case "live-G1":
						sd30Folder(t, home, "foo", sd30G1)
						want = sd30G1
					case "recreated-G2":
						d := sd30Folder(t, home, "foo", sd30G1)
						require.NoError(t, os.RemoveAll(d))
						sd30Folder(t, home, "foo", sd30G2)
						want = sd30G2
					case "unstamped":
						sd30Folder(t, home, "foo", "")
					}
					agent := &config.AgentConfig{ID: "binding-agent", Name: "binding fixture", Skills: []string{"FoO"}}
					store := New(home)
					if path == "Create" {
						require.NoError(t, store.Create(agent.ID, agent))
					} else {
						out, err := store.CreateState(agent.ID, agent, "")
						require.NoError(t, err)
						require.Equal(t, PersistenceComplete, out.PersistenceStatus)
					}
					got := sd30Bindings(t, home, agent.ID)
					if observation == "unstamped" {
						stamp, err := os.ReadFile(filepath.Join(home, "skills", "foo", ".omnipus-identity"))
						require.NoError(t, err, "grant persist stamps the outside-created folder")
						require.Regexp(t, regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\n$`), string(stamp))
						want = string(stamp[:len(stamp)-1])
					}
					if observation == "absent" {
						assert.Empty(t, got["foo"], "absent persists unbound, never refuses")
						sd30Folder(t, home, "foo", sd30G2)
						assert.Empty(t, sd30Bindings(t, home, agent.ID)["foo"], "later creation cannot retroactively bind a row")
					} else {
						assert.Equal(t, map[string]string{"foo": want}, got, "binding is normalized and contemporaneous")
					}
				})
			}
		})
	}
}

func TestSD30_BindAtPersist_UpdatePathsReplaceWholeMap(t *testing.T) {
	for _, path := range []string{"Update", "MutateState"} {
		t.Run(path, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			foo := sd30Folder(t, home, "foo", sd30G1)
			sd30Folder(t, home, "removed", sd30G1)
			store := New(home)
			require.NoError(t, store.Create("binding-agent", &config.AgentConfig{ID: "binding-agent", Skills: []string{"foo", "removed"}}))
			// Rotation without touching the agent record models a delete/recreation.
			require.NoError(t, os.RemoveAll(foo))
			sd30Folder(t, home, "foo", sd30G2)
			mutate := func(a *config.AgentConfig) error { a.Skills = []string{"FoO", "absent"}; return nil }
			if path == "Update" {
				_, err := store.Update("binding-agent", mutate)
				require.NoError(t, err)
			} else {
				rev, err := store.PeekRevision("binding-agent")
				require.NoError(t, err)
				out, err := store.MutateState("binding-agent", rev, mutate, nil)
				require.NoError(t, err)
				assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
			}
			got := sd30Bindings(t, home, "binding-agent")
			assert.Equal(t, sd30G2, got["foo"], "explicit persist binds the new live generation, never preserves G1")
			assert.Empty(t, got["absent"], "absent stays unbound")
			assert.NotContains(t, got, "removed", "wholesale write removes obsolete bindings")
			assert.NotContains(t, got, "FoO", "keys are normalized")
		})
	}
}

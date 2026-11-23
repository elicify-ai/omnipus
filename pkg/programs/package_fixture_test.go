package programs

// FR-022/S49: use the actual package engine and persisted agent records. Only
// the engine's documented source/target lookup edges are supplied by this fixture.
import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

type programPackageSource struct{ home string }

func (s programPackageSource) AgentRecord(id string) ([]byte, error) {
	agent, err := agentstore.New(s.home).Get(id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(agent)
}
func (s programPackageSource) AgentHomeDir(id string) (string, error) {
	return filepath.Join(s.home, "agents", id), nil
}
func (s programPackageSource) IsCoreAgent(string) bool { return false }

type programPackageTarget struct{}

func (programPackageTarget) InstalledSkill(string) bool                { return false }
func (programPackageTarget) KnownTool(string) bool                     { return false }
func (programPackageTarget) AlwaysAskTool(string) bool                 { return false }
func (programPackageTarget) ProviderConfigured(string) bool            { return false }
func (programPackageTarget) GlobalMaxToolIterations() int              { return 40 }
func (programPackageTarget) SeedToolPolicies(string) map[string]string { return map[string]string{} }

func assertProgramPackageRoundTrip(t *testing.T) {
	t.Helper()
	f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat"}})
	_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
	require.NoError(t, err)
	f.await(t, "github", "connected")
	home := t.TempDir()
	store := agentstore.New(home)
	agent := &config.AgentConfig{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Programs: []string{"github"}}
	_, err = store.CreateState("dev", agent, "Write clear answers.")
	require.NoError(t, err)
	var bundle bytes.Buffer
	require.NoError(t, omnpkg.Export(context.Background(), programPackageSource{home}, omnpkg.Selection{AgentIDs: []string{"dev"}}, &bundle))
	manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(bundle.Bytes()))
	require.NoError(t, err, "the actual exported bundle can be parsed by the importer")
	require.Equal(t, []omnpkg.NeedsSecret{{Name: "github", UsedBy: "dev", Kind: "program"}}, manifest.NeedsSecrets, "S49: the shared login is declared by name/kind, never exported as a value")
	for path, body := range payload {
		require.NotContains(t, string(body), "tok_valid_12345", "no token in export payload %s", path)
	}
	record, ok := payload["omnipus/agents/dev/agent.json"]
	require.True(t, ok, "the exported persisted record is present")
	plan, err := omnpkg.MapAgentForImport(record, "dev", programPackageTarget{})
	require.NoError(t, err)
	var imported config.AgentConfig
	require.NoError(t, json.Unmarshal(plan.Record, &imported))
	imported.ID, imported.Name = "dev2", "Dev2"
	target := agentstore.New(t.TempDir())
	_, err = target.CreateState("dev2", &imported, "Write clear answers.")
	require.NoError(t, err)
	after, err := target.Get("dev2")
	require.NoError(t, err)
	require.Empty(t, after.Programs, "S49: the imported agent must not inherit a program switch")
}

func TestProgramImport_DefaultsOff(t *testing.T) {
	t.Run("S49_import_mapping_clears_the_exported_programs", func(t *testing.T) {
		// Independent import assertion: a missing export declaration must not
		// prevent this half of FR-022 from executing and detecting a carried switch.
		raw, err := json.Marshal(config.AgentConfig{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Programs: []string{"github"}})
		require.NoError(t, err)
		plan, err := omnpkg.MapAgentForImport(raw, "dev", programPackageTarget{})
		require.NoError(t, err)
		var after config.AgentConfig
		require.NoError(t, json.Unmarshal(plan.Record, &after))
		require.Empty(t, after.Programs, "FR-022: importing a switched-on source starts with programs off")
	})
}

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1064 A2: force a real prepare-stage I/O failure rather than a long-name
// validation problem. The result cannot ship "persistence= stage= revision=".
func TestWU3AgentSaveFailureHasNonblankDiagnosticFacts(t *testing.T) {
	d, _ := wu3Deps(t)
	require.NoError(t, os.MkdirAll(filepath.Join(d.Home, "entities"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(d.Home, "entities", "agents"), []byte("not-a-directory"), 0o600))
	result := systools.NewAgentCreateTool(d).Execute(context.Background(), wu3CreateArgs("Valid bounded label"))
	body := wu3Error(t, result, "SAVE_FAILED")
	assert.NotContains(t, body["message"], "persistence= stage= revision=", "blank diagnostics are not an explanation")
	root := wu3JSON(t, result)
	assert.Equal(t, "none", root["persistence_status"], "failure happened before any entity publication")
	stage, ok := root["error_stage"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, stage)
	wu3Outcome(t, result, "failed: nothing saved")
}

// FR-030/BDD-9.4: the member write commits, then a filesystem error prevents
// the delegation publication. An entity and member are durable, not nothing.
func TestWU3CreateAgentReportsDurablePartsWhenMembershipPublicationIsIncomplete(t *testing.T) {
	d, _ := wu3Deps(t)
	wu3Workspace(t, d, "sales")
	// An unreadable delegation store is a filesystem fault edge. The tool's
	// real join/read-back paths remain unmocked.
	require.NoError(t, os.MkdirAll(filepath.Join(d.Home, "entities", "delegation", "sales.json"), 0o700))
	ctx := tools.WithWorkspaceID(context.Background(), "sales")
	result := systools.NewAgentCreateTool(d).Execute(ctx, wu3CreateArgs("Partial colleague"))
	body := wu3Outcome(t, result, "partly saved")
	id, ok := body["id"].(string)
	require.True(t, ok)
	read := wu3AgentRead(t, d, id)
	assert.Equal(t, "Partial colleague", read["name"], "entity survives partial publication")
	assert.Equal(t, read["revision"], body["revision"])
	assert.NotEmpty(t, body["changed_fields"], "durable parts preserved in existing structured fields")
	assert.NotEmpty(t, body["missing_parts"], "failed step named, not swallowed")
	assert.Contains(t, strings.ToLower(result.ForLLM), "delegation")
}

// Q1-A translation: card Submit previously saved the version. A later provider
// selection whose config publication fails saves nothing in THIS call; the
// card's value/binding must survive unused, never be overwritten or rolled back.
func TestWU4B_ProviderConfigFailureAfterCardSubmitRetainsValueWithoutSelection(t *testing.T) {
	f := newWU4BFixture(t)
	reg, sid, _ := wu4bCardRegistry(t, f)
	card, err := reg.Create(context.Background(), sid, "omnipus", "wu4b-webchat", "", secretrequest.Input{Purpose: "Provider credential before failing selection", Provider: "openai"})
	require.NoError(t, err)
	values := map[string]string{"api_key": "wu3-partial-value"}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Len(t, submitted.Card.ReferenceNames, 1)
	ref := submitted.Card.ReferenceNames[0]
	credentialsBefore := wu3ReadFile(t, filepath.Join(f.d.Home, "credentials.json"))
	backup := filepath.Join(f.d.Home, "config-before.json")
	configBefore := wu3ReadFile(t, f.d.ConfigPath)
	require.NoError(t, os.Rename(f.d.ConfigPath, backup))
	require.NoError(t, os.Mkdir(f.d.ConfigPath, 0o700))
	result := systools.NewProviderConfigureTool(f.d).Execute(context.Background(), map[string]any{"name": "openai", "api_key_ref": ref, "expected_revision": "absent"})
	require.True(t, result.IsError, result.ForLLM)
	assert.Equal(t, 1, f.saves, "instrument: valid reference selection actually attempted the failing config publication")
	body := wu3Outcome(t, result, "failed: nothing saved")
	assert.Equal(t, []any{}, body["changed_fields"])
	assert.Empty(t, f.cfg.Providers, "failed publication leaves no selected consumer")
	assert.Equal(t, configBefore, wu3ReadFile(t, backup))
	assert.Equal(t, credentialsBefore, wu3ReadFile(t, filepath.Join(f.d.Home, "credentials.json")), "failure cannot delete or rewrite a prior submission")
	value, err := f.d.CredStore.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, "wu3-partial-value", value)
	assert.Zero(t, f.reloads)
	assert.Zero(t, f.listings)
	assert.NotContains(t, result.ForLLM, "wu3-partial-value")
}

// FR-030/Q2: a validation refusal is exactly failed: nothing saved, across
// every WU-3 change family. No outcome expectation comes from observed output.
func TestWU3EveryChangeFamilyValidationFailureReportsNothingSaved(t *testing.T) {
	d, _ := wu3Deps(t)
	family := append(systools.AllTools(d), tools.NewInstallSkillTool(skills.NewRegistryManager(), filepath.Join(d.Home, "skills")), tools.NewEnvironmentSetupTool(tools.EnvironmentSetupToolDeps{Home: d.Home}))
	wanted := map[string]bool{"create_agent": true, "update_agent": true, "delete_agent": true, "update_workspace": true, "configure_provider": true, "add_mcp_server": true, "remove_mcp_server": true, "configure_channel": true, "enable_channel": true, "disable_channel": true, "create_skill": true, "edit_skill": true, "remove_skill": true, "set_agent_permissions": true, "install_skill": true, "environment_setup": true}
	seen := map[string]bool{}
	before := wu3ReadFile(t, d.ConfigPath)
	for _, tool := range family {
		if !wanted[tool.Name()] {
			continue
		}
		seen[tool.Name()] = true
		t.Run(tool.Name(), func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]any{})
			assert.True(t, result.IsError, "missing required arguments must be refused")
			body := wu3Outcome(t, result, "failed: nothing saved")
			assert.Equal(t, []any{}, body["changed_fields"], "validation failure has no durable parts")
			assert.Equal(t, before, wu3ReadFile(t, d.ConfigPath), "validation refusal must not rewrite configuration")
		})
	}
	assert.Equal(t, wanted, seen, "each registered change family must actually have executed")
}

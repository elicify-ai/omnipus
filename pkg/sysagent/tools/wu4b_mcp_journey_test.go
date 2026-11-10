package systools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wu4bCardRegistry(t *testing.T, f *wu4bFixture) (*secretrequest.Registry, string, *[]secretrequest.Record) {
	t.Helper()
	sessions, err := session.NewUnifiedStore(filepath.Join(f.d.Home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	meta, err := sessions.NewSession(session.SessionTypeChat, "webchat", "omnipus")
	require.NoError(t, err)
	owner, workspace := "wu4b-owner", "wu4b-workspace"
	require.NoError(t, sessions.SetMeta(meta.ID, session.MetaPatch{Owner: &owner, WorkspaceID: &workspace}))
	resumes := &[]secretrequest.Record{}
	reg := secretrequest.NewRegistry(secretrequest.Options{
		Sessions: sessions, Config: f.d.GetCfg,
		Credentials: func() (*credentials.Store, error) { return f.d.CredStore, nil },
		Resume:      func(rec secretrequest.Record) error { *resumes = append(*resumes, rec); return nil },
	})
	return reg, meta.ID, resumes
}

// Architect CONFIRM-A's entire reachable tool/card seam: no proposed draft,
// no invented operation flag, two actual config writes, real card Submit and
// reference-only continuation. Live connector/Ask UI certification is separate.
func TestWU4B_NewMCPCreateCardSubmitThenBoundedAttachJourney(t *testing.T) {
	f := newWU4BFixture(t)
	tool := systools.NewMCPAddTool(f.d)
	f.d.MCPStatus = func(string) (string, int, string) { return "error", 0, "credential required" }
	create := tool.Execute(context.Background(), map[string]any{
		"name": "new-server", "transport": "stdio", "command": "fixture-command",
		"args": []any{"--read-only", "--scope=example"}, "expected_revision": "absent",
	})
	wu3Success(t, create)
	created := wu3Outcome(t, create, "saved but not working")
	assert.Contains(t, strings.ToLower(create.ForLLM), "credential")
	assert.Regexp(t, `(?i)(not finished|unfinished|not complete)`, created["reason"], "provisional credential setup cannot be called complete")
	require.Equal(t, 1, f.saves, "first config write creates the actual descriptor")
	descriptor := f.cfg.Tools.MCP.Servers["new-server"]
	require.Equal(t, "fixture-command", descriptor.Command)
	require.Equal(t, []string{"--read-only", "--scope=example"}, descriptor.Args)
	require.Empty(t, descriptor.Env)
	require.Empty(t, descriptor.EnvRefs)
	revision := wu3SharedRow(t, f.d, "mcp", "new-server")["revision"]
	require.NotEqual(t, "absent", revision)
	beforeCard := f.snapshot(t)
	reg, sid, resumes := wu4bCardRegistry(t, f)
	requestTool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return reg }, nil)
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	ctx = tools.WithToolContext(ctx, "webchat", "wu4b-webchat")
	request := requestTool.Execute(ctx, map[string]any{"purpose": "API access for the saved read-only server", "server": "new-server", "env_key": "API_TOKEN"})
	require.False(t, request.IsError, request.ForLLM)
	require.True(t, request.ParksTurn, "the actual tool parks on the protected card")
	pending := reg.Snapshot("wu4b-owner", sid)
	require.Len(t, pending, 1)
	card, err := reg.Get(pending[0].RequestId, "wu4b-owner")
	require.NoError(t, err)
	require.Equal(t, []gen.SecretRequestField{{Key: "value", Label: "API_TOKEN"}}, card.Card.Fields, "server chooses the protected field list")
	wantRecipient := credentials.CardRecipient{Kind: "mcp_env", Server: "new-server", Transport: "stdio", Command: "fixture-command", Args: []string{"--read-only", "--scope=example"}, Field: "API_TOKEN"}
	require.Equal(t, []credentials.CardRecipient{wantRecipient}, card.Recipients)
	values := map[string]string{"value": "WU4B-JOURNEY-SUBMITTED-VALUE"}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Equal(t, "submitted", submitted.Card.State)
	require.Len(t, submitted.Card.ReferenceNames, 1)
	ref := submitted.Card.ReferenceNames[0]
	assert.Equal(t, descriptor, f.cfg.Tools.MCP.Servers["new-server"], "storage consent is not consumer-selection consent")
	assert.Equal(t, beforeCard.config, f.snapshot(t).config, "Submit must not rewrite integration config")
	assert.Equal(t, 1, f.saves)
	require.Len(t, *resumes, 1)
	assert.Equal(t, []string{ref}, (*resumes)[0].Card.ReferenceNames)
	assert.Contains(t, secretrequest.ResumeMessage((*resumes)[0]), ref)
	encoded, err := json.Marshal((*resumes)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), values["value"])
	assert.NotContains(t, secretrequest.ResumeMessage((*resumes)[0]), values["value"])
	metadata, err := f.d.CredStore.CardSecretMetadata()
	require.NoError(t, err)
	assert.Equal(t, wantRecipient, metadata[ref].Recipient)
	stored, err := f.d.CredStore.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, values["value"], stored)
	beforeAttach := f.snapshot(t)
	f.d.MCPStatus = func(string) (string, int, string) { return "connected", 2, "" }
	attach := tool.Execute(context.Background(), map[string]any{"name": "new-server", "expected_revision": revision, "env_refs": map[string]any{"API_TOKEN": ref}})
	wu3Success(t, attach)
	wu3Outcome(t, attach, "working")
	assert.Equal(t, 2, f.saves, "bounded attachment is the second separate config write")
	assert.Equal(t, 2, f.reconciles, "each persisted operation reports its real runtime check")
	want := descriptor
	want.EnvRefs = map[string]string{"API_TOKEN": ref}
	assert.Equal(t, want, f.cfg.Tools.MCP.Servers["new-server"])
	assert.NotEqual(t, revision, wu3SharedRow(t, f.d, "mcp", "new-server")["revision"])
	assert.Equal(t, beforeAttach.credentials, f.snapshot(t).credentials, "attach never stores a value")
	assert.NotContains(t, string(f.snapshot(t).config), values["value"])
	assert.NotContains(t, attach.ForLLM, values["value"])
}

func TestWU4B_ReplacementCardLeavesSelectedMCPVersionUntilApprovedAttachment(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, _ := wu4bMatchingSelection(t, f, "add_mcp_server")
	oldRef := systools.IssueCardReferencesForTest(t, f.d, secretrequest.Input{Server: "fixture", EnvKey: "TOKEN"}, "WU4B-SELECTED-VALUE")[0]
	args["env_refs"] = map[string]any{"TOKEN": oldRef}
	wu3Success(t, tool.Execute(context.Background(), args))
	before := f.snapshot(t)
	reg, sid, _ := wu4bCardRegistry(t, f)
	card, err := reg.Create(context.Background(), sid, "omnipus", "wu4b-webchat", "", secretrequest.Input{Purpose: "Rotate the saved server token", Server: "fixture", EnvKey: "TOKEN"})
	require.NoError(t, err)
	values := map[string]string{"value": "WU4B-NEW-UNSELECTED-VALUE"}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Len(t, submitted.Card.ReferenceNames, 1)
	newRef := submitted.Card.ReferenceNames[0]
	require.NotEqual(t, oldRef, newRef)
	bindings, err := f.d.CredStore.CardSecretMetadata()
	require.NoError(t, err)
	assert.Equal(t, 1, bindings[oldRef].Version)
	assert.Equal(t, 2, bindings[newRef].Version, "the second submission creates v2 in the same recipient family")
	assert.Equal(t, bindings[oldRef].Recipient, bindings[newRef].Recipient)
	assert.Equal(t, oldRef, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
	assert.Equal(t, before.config, f.snapshot(t).config)
	oldValue, err := f.d.CredStore.Get(oldRef)
	require.NoError(t, err)
	assert.Equal(t, "WU4B-SELECTED-VALUE", oldValue)
	newValue, err := f.d.CredStore.Get(newRef)
	require.NoError(t, err)
	assert.Equal(t, values["value"], newValue)
	beforeAttach := f.snapshot(t)
	attach := tool.Execute(context.Background(), map[string]any{"name": "fixture", "expected_revision": wu3SharedRow(t, f.d, "mcp", "fixture")["revision"], "env_refs": map[string]any{"TOKEN": newRef}})
	wu3Success(t, attach)
	assert.Equal(t, newRef, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
	assert.Equal(t, beforeAttach.credentials, f.snapshot(t).credentials)
	oldValue, err = f.d.CredStore.Get(oldRef)
	require.NoError(t, err)
	assert.Equal(t, "WU4B-SELECTED-VALUE", oldValue, "replacement never overwrites the old stored version")
}

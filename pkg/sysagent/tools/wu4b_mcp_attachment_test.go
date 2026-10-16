package systools_test

import (
	"context"
	"maps"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWU4B_MCPAttachmentRejectsEachChangedRecipientDimension(t *testing.T) {
	for _, dimension := range []string{"server", "transport", "command", "ordered_args", "url", "env_key"} {
		t.Run(dimension, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "add_mcp_server")
			r := credentials.CardRecipient{Kind: "mcp_env", Server: "fixture", Transport: "stdio", Command: "fixture-command", Args: []string{"--one", "--two"}, Field: "TOKEN"}
			s := f.cfg.Tools.MCP.Servers["fixture"]
			s.Args = []string{"--one", "--two"}
			f.cfg.Tools.MCP.Servers["fixture"] = s
			switch dimension {
			case "server":
				r.Server = "other-server"
			case "transport":
				r.Transport = "http"
			case "command":
				r.Command = "other-command"
			case "ordered_args":
				r.Args = []string{"--two", "--one"}
			case "url":
				r.URL = "https://other.wu4b.invalid/mcp"
			case "env_key":
				r.Field = "OTHER_TOKEN"
			}
			args["env_refs"] = map[string]any{"TOKEN": f.bound(t, r, "WU4B-MCP-WRONG-DESTINATION")}
			args = systools.WithCurrentRevisionForTest(tool, args)
			f.persist(t)
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			wu4bRefusal(t, result, "recipient")
		})
	}
}

// CONFIRM-A: this is a bounded reference-map patch, not an upsert. Omitted
// transport/command/args/url cannot take create-time defaults or erase state.
func TestWU4B_MCPMinimalAttachPreservesEveryOtherFieldAndGrant(t *testing.T) {
	for _, explicitDescriptor := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted_descriptor", true: "matching_effective_descriptor"}[explicitDescriptor], func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, ref := wu4bMatchingSelection(t, f, "add_mcp_server")
			deferred := false
			s := f.cfg.Tools.MCP.Servers["fixture"]
			s.Type, s.Enabled, s.Deferred = "", false, &deferred // effective stdio, disabled
			s.Env = map[string]string{"NON_SECRET_SETTING": "literal-control"}
			s.EnvFile = "/fixture/non-secret-env-file"
			s.Headers = map[string]string{"X-Fixture-Setting": "unchanged"}
			keepRef := f.bound(t, credentials.CardRecipient{Kind: "mcp_env", Server: "fixture", Transport: "stdio", Command: "fixture-command", Args: []string{"--read-only"}, Field: "KEEP"}, "WU4B-UNMENTIONED-CONTROL")
			s.EnvRefs = map[string]string{"KEEP": keepRef}
			f.cfg.Tools.MCP.Servers["fixture"] = s
			f.cfg.Tools.MCP.Enabled = false
			f.cfg.Sandbox.ToolPolicies["add_mcp_server"] = "ask"
			f.cfg.Agents.List[0].Tools.MCP.Servers = []config.AgentMCPServerBinding{{ID: "other", ToolsSpecified: true, Tools: []string{"read"}}}
			f.cfg.Agents.List[0].Tools.Builtin.Policies = map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}
			if explicitDescriptor {
				args["transport"], args["command"], args["args"], args["url"] = "stdio", s.Command, []any{"--read-only"}, ""
			}
			args = systools.WithCurrentRevisionForTest(tool, args)
			f.persist(t)
			before := f.snapshot(t)
			want := s
			want.EnvRefs = maps.Clone(s.EnvRefs)
			want.EnvRefs["TOKEN"] = ref
			wantMCPBindings := append([]config.AgentMCPServerBinding(nil), f.cfg.Agents.List[0].Tools.MCP.Servers...)
			result := tool.Execute(context.Background(), args)
			wu3Success(t, result)
			assert.Equal(t, want, f.cfg.Tools.MCP.Servers["fixture"], "only the named EnvRefs key may change")
			assert.False(t, f.cfg.Tools.MCP.Enabled, "attachment never flips the global kill switch")
			assert.Equal(t, wantMCPBindings, f.cfg.Agents.List[0].Tools.MCP.Servers)
			assert.Equal(t, map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}, f.cfg.Agents.List[0].Tools.Builtin.Policies)
			assert.Equal(t, "ask", f.cfg.Sandbox.ToolPolicies["add_mcp_server"])
			assert.Equal(t, before.credentials, f.snapshot(t).credentials)
			assert.Equal(t, before.saves+1, f.saves)
			assert.Equal(t, []string{"config-lock", "loop-lock", "save", "loop-unlock", "config-unlock"}, f.events)
		})
	}
}

func TestWU4B_MCPAttachmentCannotEditDescriptorOrAcceptEmptyPatch(t *testing.T) {
	for _, dimension := range []string{"transport", "command", "args", "url", "empty_map", "missing_map"} {
		t.Run(dimension, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "add_mcp_server")
			switch dimension {
			case "transport":
				args["transport"] = "http"
			case "command":
				args["command"] = "different-command"
			case "args":
				args["args"] = []any{"--different"}
			case "url":
				args["url"] = "https://different.wu4b.invalid/mcp"
			case "empty_map":
				args["env_refs"] = map[string]any{}
			case "missing_map":
				delete(args, "env_refs")
			}
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			require.True(t, result.IsError, "attachment cannot be a descriptor update: %s", result.ForLLM)
			wu3Outcome(t, result, "failed: nothing saved")
		})
	}
}

func TestWU4B_MCPLaterInvalidReferenceRefusesWholePatch(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, valid := wu4bMatchingSelection(t, f, "add_mcp_server")
	// TOKEN is valid and alphabetically precedes ZZZ_BAD: a writer that mutates
	// as it validates exposes a real first-key partial write.
	args["env_refs"] = map[string]any{"TOKEN": valid, "ZZZ_BAD": "missing-reference.v1"}
	before := f.snapshot(t)
	result := tool.Execute(context.Background(), args)
	f.unchanged(t, before)
	wu4bRefusal(t, result, "ref")
}

func TestWU4B_MCPIdenticalAttachmentIsNoOp(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, ref := wu4bMatchingSelection(t, f, "add_mcp_server")
	s := f.cfg.Tools.MCP.Servers["fixture"]
	s.EnvRefs = map[string]string{"TOKEN": ref}
	f.cfg.Tools.MCP.Servers["fixture"] = s
	args = systools.WithCurrentRevisionForTest(tool, args)
	f.persist(t)
	revision := wu3SharedRow(t, f.d, "mcp", "fixture")["revision"]
	before := f.snapshot(t)
	result := tool.Execute(context.Background(), args)
	wu3Success(t, result)
	f.unchanged(t, before)
	assert.Equal(t, revision, wu3SharedRow(t, f.d, "mcp", "fixture")["revision"])
}

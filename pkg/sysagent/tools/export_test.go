// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// WithCurrentRevisionForTest returns a copy of args carrying the live
// expected_revision of the shared resource the write tool targets (FR-085).
// It lets tests written before the precondition existed exercise their own
// behaviour unchanged: the token equals the live one, so the precondition
// passes and the rest of the call is what the test observes. Tests OF the
// precondition pass expected_revision explicitly instead.
func WithCurrentRevisionForTest(tool tools.Tool, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	var d *Deps
	var kind sharedResourceKind
	var key string
	switch tt := tool.(type) {
	case *ProviderConfigureTool:
		d, kind = tt.deps, sharedProvider
		key, _ = args["name"].(string)
	case *MCPAddTool:
		d, kind = tt.deps, sharedMCPServer
		key, _ = args["name"].(string)
	case *MCPRemoveTool:
		d, kind = tt.deps, sharedMCPServer
		key, _ = args["name"].(string)
	case *ChannelEnableTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	case *ChannelConfigureTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	case *ChannelDisableTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	default:
		panic("WithCurrentRevisionForTest: not a shared-resource write tool")
	}
	out[expectedRevisionParam] = sharedRevision(d.GetCfg(), kind, key)
	return out
}

// SetAgentIDGeneratorForTest makes create_agent use gen (given the requested
// name) for new agent ids for the rest of the test, so a test whose fixture
// is keyed by a known id, or that must plant a fault at the new agent's
// paths, knows the id in advance. Production ids are random UUIDs and never
// depend on the name. Not safe with t.Parallel.
func SetAgentIDGeneratorForTest(t interface{ Cleanup(fn func()) }, gen func(name string) string) {
	prev := newAgentID
	newAgentID = gen
	t.Cleanup(func() { newAgentID = prev })
}

// UseNameKeyedAgentIDsForTest gives each created agent the id its tests were
// written against before ids became generated UUIDs (founder decision
// 2026-10-14): the lowercase name with runs of other characters turned into
// single hyphens. Test fixtures only — production never derives an id from a
// name. Tests OF the id rule do not call this.
func UseNameKeyedAgentIDsForTest(t interface{ Cleanup(fn func()) }) {
	SetAgentIDGeneratorForTest(t, func(name string) string {
		var b strings.Builder
		hyphen := false
		for _, r := range strings.ToLower(strings.TrimSpace(name)) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
				hyphen = false
			} else if !hyphen && b.Len() > 0 {
				b.WriteByte('-')
				hyphen = true
			}
		}
		return strings.TrimSuffix(b.String(), "-")
	})
}

// FailMCPCredentialRollbackForTest makes add_mcp_server's rollback delete of
// a stored env credential fail, restoring the real delete when t ends.
func FailMCPCredentialRollbackForTest(t interface{ Cleanup(f func()) }) {
	prev := mcpCredentialDelete
	mcpCredentialDelete = func(*credentials.Store, string) error { return errors.New("injected rollback delete failure") }
	t.Cleanup(func() { mcpCredentialDelete = prev })
}

// FailMCPCredentialWriteAfterForTest makes add_mcp_server's env credential
// writes succeed `ok` times and fail from then on, restoring the real writer
// when t ends.
func FailMCPCredentialWriteAfterForTest(t interface{ Cleanup(f func()) }, ok int) {
	prev := mcpCredentialSet
	calls := 0
	mcpCredentialSet = func(store *credentials.Store, name, value string) error {
		calls++
		if calls > ok {
			return errors.New("injected credential write failure")
		}
		return prev(store, name, value)
	}
	t.Cleanup(func() { mcpCredentialSet = prev })
}

// FailAgentMetadataReadBackForTest makes update_agent's metadata read-back
// fail (the write itself still happens), restoring the real reader when t ends.
func FailAgentMetadataReadBackForTest(t interface{ Cleanup(f func()) }) {
	prev := readAgentMetadataBack
	readAgentMetadataBack = func(string) ([]byte, error) { return nil, errors.New("injected read-back failure") }
	t.Cleanup(func() { readAgentMetadataBack = prev })
}

// FailWorkspaceReadBackForTest makes the workspace tools' post-write read-back
// fail (the write itself still happens) until RestoreWorkspaceReadBackForTest
// or the end of t.
func FailWorkspaceReadBackForTest(t interface{ Cleanup(f func()) }) {
	readWorkspaceStateBack = func(string, string) (workspacepkg.State, error) {
		return workspacepkg.State{}, errors.New("injected read-back failure")
	}
	t.Cleanup(RestoreWorkspaceReadBackForTest)
}

// RestoreWorkspaceReadBackForTest restores the real read-back.
func RestoreWorkspaceReadBackForTest() { readWorkspaceStateBack = workspacepkg.ReadStateLocked }

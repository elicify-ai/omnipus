// active_agent_deleted_test.go — deletion guard for session-core DEL-11: the
// retired handover owner (SessionMeta.ActiveAgentID, wire/disk key
// active_agent_id) is gone. A session's agent is its immutable owner,
// SessionMeta.AgentID; nothing writes or reads a second one.

package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// activeAgentCodeRef reports whether a CODE line (comments exempt) spells the
// retired field: the Go identifier as a selector or composite key, or the
// persisted JSON key.
func activeAgentCodeRef(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
		return false
	}
	return strings.Contains(line, ".ActiveAgentID") || strings.Contains(line, "ActiveAgentID:") ||
		strings.Contains(line, "ActiveAgentID ") && strings.Contains(line, "json:") ||
		strings.Contains(line, `"active_agent_id"`)
}

func TestActiveAgentID_NoProductionReferences(t *testing.T) {
	// Instrument check: the matcher sees the failure it guards, and ignores prose.
	require.True(t, activeAgentCodeRef("\tagent := meta.ActiveAgentID"))
	require.True(t, activeAgentCodeRef("\tActiveAgentID: x,"))
	require.True(t, activeAgentCodeRef(`	k := "active_agent_id"`))
	require.False(t, activeAgentCodeRef("\t// ActiveAgentID was retired"))
	require.False(t, activeAgentCodeRef("\tids := al.GetActiveAgentIDs()"), "GetActiveAgentIDs is the running-turn list, a different thing")

	scanned := 0
	for _, root := range []string{"../../pkg", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "spa" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			scanned++
			for i, line := range strings.Split(string(data), "\n") {
				if activeAgentCodeRef(line) {
					t.Errorf("%s:%d reintroduces the retired handover owner: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.Greater(t, scanned, 500, "scanner must walk the real source tree")
}

func TestActiveAgentID_FieldAbsentFromSessionMeta(t *testing.T) {
	typ := reflect.TypeOf(session.SessionMeta{})
	_, ok := typ.FieldByName("AgentID")
	require.True(t, ok, "instrument check: reflection must see the owner field AgentID")
	_, has := typ.FieldByName("ActiveAgentID")
	assert.False(t, has, "SessionMeta.ActiveAgentID must stay deleted (DEL-11)")
	for i := 0; i < typ.NumField(); i++ {
		assert.NotContains(t, typ.Field(i).Tag.Get("json"), "active_agent_id")
	}
}

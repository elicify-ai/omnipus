// Omnipus — real trusted-record fixtures for FR-VA-032/034/036/037.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func plantKnowledgeTrackedView(t *testing.T, home, root, baseRel, viewRel string) {
	t.Helper()
	basePath := filepath.Join(root, filepath.FromSlash(baseRel))
	viewPath := filepath.Join(root, filepath.FromSlash(viewRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(viewPath), 0o755))
	require.NoError(t, os.WriteFile(basePath,
		[]byte("views:\n  - type: table\n    name: Open\n"), 0o600))
	require.NoError(t, os.WriteFile(viewPath,
		[]byte("name: open\nkind: table\nsource: "+baseRel+"\nderived_from: "+baseRel+"\n"), 0o600))
	require.NoError(t, WithViewMembership(home, root, func(m *ViewMembership) error {
		if m.Bases[baseRel] == nil {
			m.Bases[baseRel] = make(map[string]string)
		}
		m.Bases[baseRel]["open"] = viewRel
		return SaveViewMembership(m)
	}))
}

func loadKnowledgeMembers(t *testing.T, home, root string) *ViewMembership {
	t.Helper()
	m, err := LoadViewMembership(home, root)
	require.NoError(t, err)
	return m
}

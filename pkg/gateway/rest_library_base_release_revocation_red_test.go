// Omnipus — FR-VA-033 / TDD rows 81–82: revocation precedes marker writes;
// editable markers never create authority without a private record entry.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/stretchr/testify/require"
)

func TestBaseRelease_RevokesAllMembershipsInOneRecordSaveBeforeMarkerStripOrTrash(t *testing.T) {
	api, _, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "One.view")
	// A second real member of the SAME base must be revoked in the same save.
	second := filepath.Join(vault, "Two.view")
	require.NoError(t, os.WriteFile(second,
		[]byte("name: two\nlabel: Two\nkind: table\nsource: Projects.base\nderived_from: Projects.base\n"), 0o644))
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		m.Bases["Projects.base"]["two"] = "Two.view"
		return knowledge.SaveViewMembership(m)
	}))

	// Flow-style YAML is a valid view, but stripping its provenance cannot
	// safely preserve formatting. Unlike chmod, this failure is deterministic
	// even if the tests run as root. It happens AFTER the record save.
	flowView := []byte("{name: open, label: Open, kind: table, source: Projects.base, derived_from: Projects.base}\n")
	require.NoError(t, os.WriteFile(filepath.Join(vault, "One.view"), flowView, 0o644))
	parsed, rejection := records.ParseView("One.view", flowView)
	require.Nil(t, rejection, "the fixture must pass the read-before-revoke validation")
	require.NotNil(t, parsed)
	_, stripErr := records.StripCopiedViewProvenance(flowView)
	require.Error(t, stripErr, "the fixture must cause the post-revocation rewrite to fail")
	err := knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		return m.ReleaseBase("Projects.base")
	})
	require.Error(t, err, "the injected marker-write failure must be visible after revocation")
	m := loadRecordedViews(t, api.homePath, vault)
	require.NotContains(t, m.Bases, "Projects.base", "ALL members must be revoked before a marker write")
	for _, name := range []string{"One.view", "Two.view"} {
		body, readErr := os.ReadFile(filepath.Join(vault, name))
		require.NoError(t, readErr)
		require.Contains(t, string(body), "derived_from: Projects.base",
			"the untrusted marker can remain after revocation without recreating authority")
	}
}

func TestLibraryBaseTrash_RevokesAllMembershipsBeforeMarkerFailure(t *testing.T) {
	api, ws, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "One.view")
	second := filepath.Join(vault, "Two.view")
	require.NoError(t, os.WriteFile(second,
		[]byte("name: two\nkind: table\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		m.Bases["Projects.base"]["two"] = "Two.view"
		return knowledge.SaveViewMembership(m)
	}))
	flowView := []byte("{name: open, kind: table, source: Projects.base, derived_from: Projects.base}\n")
	require.NoError(t, os.WriteFile(filepath.Join(vault, "One.view"), flowView, 0o600))
	_, rejection := records.ParseView("One.view", flowView)
	require.Nil(t, rejection, "preflight must accept the injected view")
	_, stripErr := records.StripCopiedViewProvenance(flowView)
	require.Error(t, stripErr, "marker stripping must fail only after the record save")

	// Exercise the real Library delete door, not just ReleaseBase directly:
	// neither the base nor its views may remain managed after strip fails.
	requireTrashIncomplete(t, api, ws, "vault-a/Projects.base", "vault-a/One.view")
	require.NotContains(t, loadRecordedViews(t, api.homePath, vault).Bases, "Projects.base")
	require.FileExists(t, filepath.Join(vault, "Projects.base"))
	require.FileExists(t, filepath.Join(vault, "One.view"))
	require.FileExists(t, second)
}

func TestViewReadOnlyAndDerivedBadge_FollowTheRecordNotTheMarker(t *testing.T) {
	api, _, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "Open.view")
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		delete(m.Bases, "Projects.base")
		return knowledge.SaveViewMembership(m)
	}))
	body, err := os.ReadFile(filepath.Join(vault, "Open.view"))
	require.NoError(t, err)
	require.Contains(t, string(body), "derived_from: Projects.base", "the marker-only fixture must exist")
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).Bases,
		"a planted marker must never create a private membership")
	t.Fatal("BLOCKED: FR-VA-033/row 82 badge and read-only assertion still needs a runtime LibraryEntry.IsView/View annotation and a Library preview derived/read-only branch; the generated LibraryEntry.View field exists but no gateway IsView assignment or frontend branch exists. The record-vs-marker fixture above is real; do not treat this missing presentation seam as a passing badge test")
}

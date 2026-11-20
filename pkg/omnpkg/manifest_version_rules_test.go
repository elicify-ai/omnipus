package omnpkg_test

// RED T-2 — TestManifest_VersionRules (spec TDD plan order 1).
// Spec: export-import-s1-spec.md rev 6 — FR-004 (version rules, unknown fields
// ignored), FR-005 (agent element schema too high), SC-003 (format_version 2
// refused by inspect, verify AND upload); Datasets D1 rows 7–10; BDD "Newer
// package refused offline".

import (
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func manifestWithVersion(t *testing.T, version int, withVersionKey bool) []byte {
	t.Helper()
	m, files := validManifest(testSourceID, false)
	m.FormatVersion = version
	if !withVersionKey {
		// "missing" is modelled by rendering without the key (D1 8).
		return buildPackagePatched(t, m, files, nil, []string{"format_version"}, 0)
	}
	return buildPackage(t, m, files)
}

func TestManifest_VersionRules(t *testing.T) {
	t.Run("format_version 1 accepted by upload reader", func(t *testing.T) {
		pkg := manifestWithVersion(t, omnpkg.FormatVersion, true)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "the current format_version is accepted (FR-004)")
	})

	// SC-003: format_version 2 is refused by inspect, verify and upload.
	t.Run("format_version 2 refused by inspect", func(t *testing.T) {
		pkg := manifestWithVersion(t, omnpkg.FormatVersion+1, true)
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooNew)
	})
	t.Run("format_version 2 refused by verify", func(t *testing.T) {
		pkg := manifestWithVersion(t, omnpkg.FormatVersion+1, true)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooNew)
	})
	t.Run("format_version 2 refused by upload reader", func(t *testing.T) {
		pkg := manifestWithVersion(t, omnpkg.FormatVersion+1, true)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooNew)
	})

	// D1 8: below min (0) / missing → not_a_package.
	t.Run("format_version 0 refused as not_a_package", func(t *testing.T) {
		pkg := manifestWithVersion(t, 0, true)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})
	t.Run("missing format_version refused as not_a_package", func(t *testing.T) {
		pkg := manifestWithVersion(t, 0, false)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})

	// D1 9: an unknown manifest field is forward-compatible — accepted.
	t.Run("unknown manifest field ignored (forward compatible)", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackagePatched(t, m, files, map[string]any{"vendor_extension": map[string]any{"future": true}}, nil, 0)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "unknown manifest fields are ignored (FR-004)")
	})

	// D1 10: an agent element schema higher than known → element_schema_too_new.
	t.Run("agent schema 2 refused as element_schema_too_new", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		m.Elements[0].Schema = omnpkg.AgentSchemaVersion + 1
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeElementSchemaTooNew)
		_, _, err = omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeElementSchemaTooNew)
	})
	t.Run("agent schema 1 accepted", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		m.Elements[0].Schema = omnpkg.AgentSchemaVersion
		pkg := buildPackage(t, m, files)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err)
	})
}

package generated

// RED T-1 — TestPackageContracts_S1Schemas (spec TDD plan T-1; slices table
// "Contracts: five schemas, three operations, error codes, regenerate").
// Contract-lint test in this repo's established style (schema_harness_test.go
// loaders): the S1 package-format wire contract must exist in contracts/
// BEFORE the Go/TS code that speaks it (root CLAUDE.md, Hard Constraint #8).
//
// Spec: export-import-s1-spec.md §Contract — five new component schemas
// (PackageManifest, PackageExportRequest, PackageImportPreview,
// PackageImportApplyRequest, PackageImportResult), three openapi operations
// (POST /api/v1/packages/export, POST /api/v1/packages/import,
// POST /api/v1/packages/import/{staging_id}/apply), and ErrorResponse's code
// description extended with the new codes (§Machine-Verifiable Constraints).
//
// Fixtures are built from the spec's field tables, never from the engine's
// Go types — oracle independence. RED state: the five schema files do not
// exist yet, so every schema subtest fails with the BLOCKED marker naming the
// missing file and the spec section that requires it (never t.Skip).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// s1ManifestFixture is the §Contract PackageManifest fixture; the preview's
// manifest field carries the same shape.
var s1ManifestFixture = map[string]any{
	"format":         "omnpkg",
	"format_version": 1,
	"scope":          "custom",
	"elements": []any{map[string]any{
		"type":      "agent",
		"schema":    1,
		"source_id": "5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c",
		"name":      "Writer",
		"files":     []any{"omnipus/agents/5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c/agent.json"},
	}},
	"counts":            map[string]any{"agent": 1},
	"files":             map[string]any{"omnipus/agents/5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c/agent.json": map[string]any{"sha256": s1TestSHA, "size": 1}},
	"source_build":      "v0.1.1",
	"created_at":        "2026-11-19T12:00:00Z",
	"includes_sessions": false,
	"needs_secrets":     []any{},
}

// s1Schemas are the five new component schemas §Contract requires, each with
// a minimal valid fixture built from the spec's field table.
var s1Schemas = []struct {
	name    string
	fixture map[string]any
}{
	{
		name:    "PackageManifest",
		fixture: s1ManifestFixture,
	},
	{
		name:    "PackageExportRequest",
		fixture: map[string]any{"agent_ids": []any{"5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c"}},
	},
	{
		name: "PackageImportPreview",
		fixture: map[string]any{
			"staging_id": "stg-1",
			"expires_at": "2026-11-19T12:30:00Z",
			"manifest":   s1ManifestFixture, // the preview carries the full manifest
			"will_add": []any{map[string]any{
				"type": "agent", "source_id": "src-1", "source_name": "Writer",
				"proposed_name": "Writer", "renamed": false,
			}},
			"skipped_elements": []any{},
			"warnings":         []any{},
			"needs_secrets":    []any{},
		},
	},
	{
		name:    "PackageImportApplyRequest",
		fixture: map[string]any{"mode": "new", "workspace_id": "ws-1"},
	},
	{
		name: "PackageImportResult",
		fixture: map[string]any{
			"mode": "new",
			"created": []any{map[string]any{
				"type": "agent", "source_id": "src-1", "new_id": "new-1",
				"name": "Writer", "workspace_id": "ws-1",
			}},
			"warnings":      []any{},
			"needs_secrets": []any{},
			"activation":    "active",
		},
	},
}

// s1ErrorCodes are the new codes §Machine-Verifiable Constraints adds to
// ErrorResponse's code description.
var s1ErrorCodes = []string{
	"not_a_package", "package_too_new", "element_schema_too_new",
	"package_too_large", "checksum_mismatch", "manifest_mismatch",
	"unsafe_path", "unsafe_entry_type", "no_supported_element",
	"too_many_agents", "element_type_not_supported", "invalid_element",
	"element_not_creatable", "external_worker_not_supported_yet",
	"core_agent_not_exportable", "agent_file_unreadable",
	"agent_prompt_missing", "staging_not_found", "too_many_staged_imports",
	"import_in_progress", "name_suffix_exhausted", "import_rolled_back",
	"import_rollback_incomplete",
}

// s1TestSHA is a well-formed 64-hex sha256 for fixtures (the schema may pin
// the shape; the spec only names the field).
const s1TestSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// s1SchemaDoc parses a component schema file into a YAML document.
func s1SchemaDoc(t *testing.T, name string) map[string]any {
	t.Helper()
	initSchemas(t) // sets componentSchemaDir (lazy package-level state)
	path := filepath.Join(componentSchemaDir, name+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("BLOCKED: contracts/components/schemas/%s.yaml does not exist — required by export-import-s1-spec.md §Contract (T-1)", name)
	}
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc), "schema %s must parse as YAML", name)
	return doc
}

func TestPackageContracts_S1Schemas(t *testing.T) {
	t.Run("five component schemas exist, compile and accept the spec fixture", func(t *testing.T) {
		for _, s := range s1Schemas {
			s1SchemaDoc(t, s.name) // exists and parses
			err := validateAgainstComponentSchema(t, s.name, s.fixture)
			require.NoError(t, err, "the §Contract fixture must validate against schema %s", s.name)
		}
	})

	// FR-004 / §Contract PackageManifest row: format const "omnpkg",
	// format_version integer ≥ 1, scope enum, elements[].schema ≥ 1 — and
	// extra unknown properties allowed.
	t.Run("PackageManifest pins format, format_version and scope", func(t *testing.T) {
		base := s1Schemas[0].fixture

		wrongFormat := deepCopyMap(base)
		wrongFormat["format"] = "tarbackup"
		require.Error(t, validateAgainstComponentSchema(t, "PackageManifest", wrongFormat),
			"format is const omnpkg — another format must be schema-invalid")

		for _, bad := range []int{0, -1} {
			v0 := deepCopyMap(base)
			v0["format_version"] = bad
			require.Error(t, validateAgainstComponentSchema(t, "PackageManifest", v0),
				"format_version %d violates integer ≥ 1", bad)
		}
		noVersion := deepCopyMap(base)
		delete(noVersion, "format_version")
		require.Error(t, validateAgainstComponentSchema(t, "PackageManifest", noVersion),
			"format_version is required")

		badScope := deepCopyMap(base)
		badScope["scope"] = "galaxy"
		require.Error(t, validateAgainstComponentSchema(t, "PackageManifest", badScope),
			"scope is the enum workspace|system|custom")

		lowSchema := deepCopyMap(base)
		lowSchema["elements"] = []any{map[string]any{
			"type": "agent", "schema": 0, "source_id": "s", "name": "n", "files": []any{},
		}}
		require.Error(t, validateAgainstComponentSchema(t, "PackageManifest", lowSchema),
			"elements[].schema violates integer ≥ 1")

		extra := deepCopyMap(base)
		extra["vendor_extension"] = map[string]any{"anything": true}
		require.NoError(t, validateAgainstComponentSchema(t, "PackageManifest", extra),
			"extra unknown properties are allowed (§Contract PackageManifest row)")
	})

	t.Run("PackageImportApplyRequest pins the mode enum", func(t *testing.T) {
		badMode := map[string]any{"mode": "import"}
		require.Error(t, validateAgainstComponentSchema(t, "PackageImportApplyRequest", badMode),
			"mode is the enum replace|merge|new")
		noMode := map[string]any{"workspace_id": "ws-1"}
		require.Error(t, validateAgainstComponentSchema(t, "PackageImportApplyRequest", noMode),
			"mode is required")
	})

	t.Run("openapi.yaml carries the three package operations", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(contractsDir(), "openapi.yaml"))
		require.NoError(t, err, "openapi.yaml must be readable")
		var doc map[string]any
		require.NoError(t, yaml.Unmarshal(raw, &doc), "openapi.yaml must parse as YAML")
		paths, ok := doc["paths"].(map[string]any)
		require.True(t, ok, "openapi.yaml must have paths")
		for _, p := range []string{
			"/api/v1/packages/export",
			"/api/v1/packages/import",
			"/api/v1/packages/import/{staging_id}/apply",
		} {
			ops, has := paths[p].(map[string]any)
			require.True(t, has, "openapi.yaml must declare path %s (§Contract)", p)
			_, hasPost := ops["post"]
			require.True(t, hasPost, "path %s must be a POST operation (§Contract)", p)
		}
	})

	t.Run("ErrorResponse code description lists every new S1 code", func(t *testing.T) {
		doc := s1SchemaDoc(t, "ErrorResponse") // exists today; BLOCKs if removed
		props, ok := doc["properties"].(map[string]any)
		require.True(t, ok, "ErrorResponse must have properties")
		code, ok := props["code"].(map[string]any)
		require.True(t, ok, "ErrorResponse.properties.code must exist")
		desc, _ := code["description"].(string)
		for _, c := range s1ErrorCodes {
			require.Contains(t, desc, c,
				"ErrorResponse's code description must list the new code %s (§Machine-Verifiable Constraints)", c)
		}
	})
}

// deepCopyMap round-trips through YAML for fixture mutation.
func deepCopyMap(m map[string]any) map[string]any {
	raw, err := yaml.Marshal(m)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

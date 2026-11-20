// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// contracts_backup_removal_1919_test.go — RED test for issue #1919
// (founder decision P-Q3b B, 2026-11-19): Backup and Restore is removed
// from Settings, Data, and the OpenAPI contracts must stop listing the
// backup endpoints and their schemas. This test fails today (the paths and
// schemas exist) and goes green only when the spec edit + regeneration
// land together — the repo's contract-first rule (#8).
package generated

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestContracts_OpenAPIDropsBackupRestore_1919(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(contractsDir(), "openapi.yaml"))
	require.NoError(t, err, "contracts/openapi.yaml must be readable — a missing file is a harness failure, not a pass")

	var doc struct {
		Paths      map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc), "openapi.yaml must parse as YAML")
	require.NotNil(t, doc.Paths, "openapi.yaml must have a paths section — an empty parse would make every absence below vacuous")
	require.NotNil(t, doc.Components.Schemas, "openapi.yaml must have a components.schemas section — an empty parse would make every absence below vacuous")

	// Sanity control for the instrument: a path that MUST exist. If the
	// paths map were parsed empty, every banned-path check below would pass
	// without seeing anything.
	_, ok := doc.Paths["/state"]
	require.Truef(t, ok, "instrument check: /state must be present in paths — the parsed section is empty or wrong, so the assertions below cannot see anything")

	for _, banned := range []string{"/backup", "/backups", "/restore"} {
		_, ok := doc.Paths[banned]
		require.Falsef(t, ok,
			"contracts/openapi.yaml still lists path %s — Backup and Restore is removed from Settings, Data (issue #1919); delete the path from the spec and regenerate the contracts", banned)
	}
	for _, banned := range []string{"BackupCreateResponse", "BackupEntry", "RestoreBackupRequest"} {
		_, ok := doc.Components.Schemas[banned]
		require.Falsef(t, ok,
			"contracts/openapi.yaml still references or defines schema %s — Backup and Restore is removed from Settings, Data (issue #1919); delete the schema from the spec and regenerate the contracts", banned)
	}

	// The external schema component files must be gone too (they are the
	// definitions behind the $refs asserted above).
	schemaDir := filepath.Join(contractsDir(), "components", "schemas")
	for _, name := range []string{"BackupCreateResponse.yaml", "BackupEntry.yaml", "RestoreBackupRequest.yaml"} {
		_, statErr := os.Stat(filepath.Join(schemaDir, name))
		require.Truef(t, errors.Is(statErr, fs.ErrNotExist),
			"contracts/components/schemas/%s must be deleted — Backup and Restore is removed from Settings, Data (issue #1919)", name)
	}
}

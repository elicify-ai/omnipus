// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-37 (a preset is data — a new data file lists with no
// Go change), S-93 (a second data-only recipe's check is parsed from data),
// S-38's catalog half (presets carry the read-only kind) — FR-001.
//
// The oracle is the SPEC's "Preset data (initial values)" table, verbatim.
// The data-file FORMAT is the RED proposal recorded in test-disputes.md
// (one JSON file per preset, field names = the contract schema names);
// GREEN ships the real files under the agreed directory and this test keeps
// the same table.

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/elicify-ai/omnipus/tests/testutil/presetbuild"

	"github.com/stretchr/testify/require"
)

const githubPresetJSON = `{
  "id": "github",
  "kind": "preset",
  "display_name": "GitHub",
  "program_words": ["gh", "git"],
  "token_variables": ["GH_TOKEN", "GITHUB_TOKEN"],
  "network_sites": ["github.com", "api.github.com", "uploads.github.com",
                    "codeload.github.com", "objects.githubusercontent.com",
                    "raw.githubusercontent.com"],
  "git": {"host": "github.com", "username": "x-access-token"},
  "identity_check": {"program_word": "gh", "args": ["api", "user", "--jq", ".login"],
                     "timeout_seconds": 20, "max_output_bytes": 4096}
}`

const flyPresetJSON = `{
  "id": "fly",
  "kind": "preset",
  "display_name": "Fly",
  "program_words": ["fly", "flyctl"],
  "token_variables": ["FLY_API_TOKEN"],
  "network_sites": ["api.fly.io", "api.machines.dev"],
  "identity_check": {"program_word": "fly", "args": ["auth", "whoami"],
                     "timeout_seconds": 20, "max_output_bytes": 4096}
}`

// S-37/S-93: one ADDITIONAL data-only preset, added with no Go change.
const examplePresetJSON = `{
  "id": "example",
  "kind": "preset",
  "display_name": "Example",
  "program_words": ["exampletool"],
  "token_variables": ["EXAMPLE_TOKEN"],
  "network_sites": ["api.example.invalid"],
  "identity_check": {"program_word": "exampletool", "args": ["whoami"],
                     "timeout_seconds": 20, "max_output_bytes": 4096}
}`

func presetTestFS() fstest.MapFS {
	return fstest.MapFS{
		"github.json":  &fstest.MapFile{Data: []byte(githubPresetJSON)},
		"fly.json":     &fstest.MapFile{Data: []byte(flyPresetJSON)},
		"example.json": &fstest.MapFile{Data: []byte(examplePresetJSON)},
	}
}

func findEntry(t *testing.T, entries []ProgramEntry, id string) ProgramEntry {
	t.Helper()
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %q not listed in %d entries", id, len(entries))
	return ProgramEntry{}
}

func TestProgramCatalog_PresetIsData(t *testing.T) {
	t.Run("S37_presets_are_listed_from_data", func(t *testing.T) {
		entries, err := CatalogFromFS(presetTestFS())
		require.NoError(t, err)
		require.Len(t, entries, 3, "three preset data files → three listed entries")

		gh := findEntry(t, entries, "github")
		// The spec preset table, field by field:
		require.Equal(t, []string{"gh", "git"}, gh.ProgramWords, "GitHub program words")
		require.Equal(t, []string{"GH_TOKEN", "GITHUB_TOKEN"}, gh.TokenVariables, "GitHub token variables")
		require.Equal(t, []string{
			"github.com", "api.github.com", "uploads.github.com",
			"codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com",
		}, gh.NetworkSites, "GitHub network sites")
		require.NotNil(t, gh.Git, "GitHub has a git section")
		require.Equal(t, "github.com", gh.Git.Host)
		require.Equal(t, "x-access-token", gh.Git.Username)
		require.NotNil(t, gh.IdentityCheck, "GitHub has an identity check")
		require.Equal(t, "gh", gh.IdentityCheck.ProgramWord)
		require.Equal(t, []string{"api", "user", "--jq", ".login"}, gh.IdentityCheck.Args)

		fly := findEntry(t, entries, "fly")
		require.Equal(t, []string{"fly", "flyctl"}, fly.ProgramWords, "Fly program words")
		require.Equal(t, []string{"FLY_API_TOKEN"}, fly.TokenVariables, "Fly token variables")
		require.Equal(t, []string{"api.fly.io", "api.machines.dev"}, fly.NetworkSites, "Fly network sites")
		require.Nil(t, fly.Git, "Fly has no git section (spec preset table)")
		require.NotNil(t, fly.IdentityCheck)
		require.Equal(t, "fly", fly.IdentityCheck.ProgramWord)
		require.Equal(t, []string{"auth", "whoami"}, fly.IdentityCheck.Args)
	})

	t.Run("S37_added_data_file_lists_with_no_go_change", func(t *testing.T) {
		// The Example preset exists ONLY as a data file in the FS; listing
		// it must work like every other preset.
		entries, err := CatalogFromFS(presetTestFS())
		require.NoError(t, err)
		ex := findEntry(t, entries, "example")
		require.Equal(t, "Example", ex.DisplayName)
		require.Equal(t, []string{"exampletool"}, ex.ProgramWords, "Example's words, from data")
		require.Equal(t, []string{"EXAMPLE_TOKEN"}, ex.TokenVariables, "Example's variables, from data")
		require.Equal(t, []string{"api.example.invalid"}, ex.NetworkSites, "Example's sites, from data")
	})

	t.Run("S93_data_only_check_definition_parses", func(t *testing.T) {
		// S-93's setup: example.json's account check runs `exampletool
		// whoami`; the check definition comes from the data file. (The
		// executed check — `connected` with `ex-user` — runs at the
		// integration level with the fake program.)
		entries, err := CatalogFromFS(presetTestFS())
		require.NoError(t, err)
		ex := findEntry(t, entries, "example")
		require.NotNil(t, ex.IdentityCheck, "the check is defined in the data file")
		require.Equal(t, "exampletool", ex.IdentityCheck.ProgramWord)
		require.Equal(t, []string{"whoami"}, ex.IdentityCheck.Args)
	})

	t.Run("S37_actual_embedded_catalog_lists_added_data_file", func(t *testing.T) {
		// Build the real Presets embed with precisely one added JSON input.
		// No environment-gated omission and no MapFS surrogate.
		var probe struct { // test-build output, not a gateway wire format
			Entries    []ProgramEntry `json:"Entries"`
			Connection Connection     `json:"Connection"`
		}
		require.NoError(t, json.Unmarshal(presetbuild.Probe(t), &probe))
		require.Len(t, probe.Entries, 3, "GitHub, Fly and the added Example are actually embedded")
		require.Equal(t, "GitHub", findEntry(t, probe.Entries, "github").DisplayName)
		require.Equal(t, "Fly", findEntry(t, probe.Entries, "fly").DisplayName)
		ex := findEntry(t, probe.Entries, "example")
		require.Equal(t, "preset", ex.Kind)
		require.Equal(t, "Example", ex.DisplayName)
		require.Equal(t, []string{"exampletool"}, ex.ProgramWords)
		require.Equal(t, []string{"EXAMPLE_TOKEN"}, ex.TokenVariables)
		require.Equal(t, []string{"api.example.invalid"}, ex.NetworkSites)
		require.NotNil(t, ex.IdentityCheck)
		require.Equal(t, "exampletool", ex.IdentityCheck.ProgramWord)
		require.Equal(t, []string{"whoami"}, ex.IdentityCheck.Args)
		require.Equal(t, "connected", probe.Connection.State)
		require.Equal(t, "ex-user", probe.Connection.AccountLabel)
		require.Equal(t, int64(1), probe.Connection.Generation)
	})

	t.Run("S38_presets_carry_the_read_only_kind", func(t *testing.T) {
		// The catalog half of S-38 (presets are read-only): every loaded
		// preset is kind "preset"; the 422 refusal of DELETE on a preset is
		// covered by the gateway slice.
		entries, err := CatalogFromFS(presetTestFS())
		require.NoError(t, err)
		// FR-001 ships GitHub and Fly; S-37 adds Example in this fixture.
		require.Len(t, entries, 3, "GitHub, Fly and the added Example must be listed before checking kind")
		for _, e := range entries {
			require.Equal(t, "preset", e.Kind, "loaded preset %s must carry kind preset", e.ID)
		}
	})

	t.Run("check_bounds_are_the_contract_defaults", func(t *testing.T) {
		// Contract ProgramIdentityCheck: timeout 1–30 default 20; output
		// fixed 4,096 bytes.
		entries, err := CatalogFromFS(presetTestFS())
		require.NoError(t, err)
		gh := findEntry(t, entries, "github")
		require.Equal(t, 20, gh.IdentityCheck.TimeoutSeconds, "the default check timeout")
		require.Equal(t, 4096, gh.IdentityCheck.MaxOutputBytes, "the fixed check output cap")
	})
}

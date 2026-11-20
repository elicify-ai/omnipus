package omnpkg_test

// RED T-2 — element validation for the upload path.
// Spec: FR-020 (upload runs every format check plus the element rules before
// answering; an external-worker record is refused before `ready`, with or
// without runtime values); error table rows invalid_element (bad JSON, type
// core/system) and external_worker_not_supported_yet; D1 rows 20, 22, 30, 31;
// BDD "Package declares a core agent", "[S1a only] External-worker package
// refused at upload".

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func TestAgentElement_S1aRefusals(t *testing.T) {
	t.Run("D1-30/31 external worker refused at upload, with and without runtime values", func(t *testing.T) {
		for _, withRuntime := range []bool{false, true} {
			m, files := validManifest(testSourceID, false)
			rec := baseRecord(testSourceID)
			rec["type"] = "worker"
			rec["description"] = "Runs codex tasks"
			exec := map[string]any{"kind": "external-cli", "cli": "codex"}
			if withRuntime {
				exec["cli_path"] = "/usr/local/bin/codex"
				exec["cli_args"] = "--key sk-TEST-123"
				exec["env_overrides"] = map[string]any{"OPENAI_API_KEY": "sk-TEST-456"}
			}
			rec["subagents"] = map[string]any{"executor": exec}
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			files[recordPath(testSourceID)] = raw
			m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
			pkg := buildPackage(t, m, files)

			manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
			require.NoError(t, err, "the package format itself is valid")
			err = omnpkg.ValidateElements(manifest, payload)
			wantCode(t, err, omnpkg.CodeExternalWorkerNotSupported)
		}
	})

	t.Run("D1-22 type core refused as invalid_element", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		rec := baseRecord(testSourceID)
		rec["type"] = "core"
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		files[recordPath(testSourceID)] = raw
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
		pkg := buildPackage(t, m, files)
		manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err)
		err = omnpkg.ValidateElements(manifest, payload)
		wantCode(t, err, omnpkg.CodeInvalidElement)
	})

	t.Run("D1-22b type system refused as invalid_element", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		rec := baseRecord(testSourceID)
		rec["type"] = "system"
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		files[recordPath(testSourceID)] = raw
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
		pkg := buildPackage(t, m, files)
		manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err)
		wantCode(t, omnpkg.ValidateElements(manifest, payload), omnpkg.CodeInvalidElement)
	})

	t.Run("D1-20 record not JSON refused as invalid_element", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		files[recordPath(testSourceID)] = []byte("{not json at all")
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{
			SHA256: sha256hex(files[recordPath(testSourceID)]), Size: int64(len(files[recordPath(testSourceID)])),
		}
		pkg := buildPackage(t, m, files)
		manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "a non-JSON record is a content problem, not a format problem")
		wantCode(t, omnpkg.ValidateElements(manifest, payload), omnpkg.CodeInvalidElement)
	})

	t.Run("valid custom record accepted", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackage(t, m, files)
		manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err)
		require.NoError(t, omnpkg.ValidateElements(manifest, payload), "a Main agent record is creatable")
	})
}

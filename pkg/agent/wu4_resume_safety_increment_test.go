package agent

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wu4ResumeSafetyWitnessRef   = "WU4_DISPATCH_UNRELATED_WITNESS"
	wu4ResumeSafetyWitnessValue = "synthetic-wu4-dispatch-unrelated-readable-witness"
)

func wu4ResumeSafetyCorruptSubmittedNonce(t *testing.T, path, reference string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var entries map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["credentials"], &entries))
	require.Contains(t, entries, reference)
	// One decoded byte cannot be the AES-GCM nonce. Only THIS submitted
	// reference is altered; the real unrelated encrypted witness is untouched.
	entries[reference]["nonce"] = json.RawMessage(`"AQ=="`)
	document["credentials"], err = json.Marshal(entries)
	require.NoError(t, err)
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

func wu4ResumeSafetyRequireError(t *testing.T, fault, path, reference string, err error) {
	t.Helper()
	switch fault {
	case "healthy":
		require.NoError(t, err)
	case "locked":
		require.ErrorIs(t, err, credentials.ErrStoreLocked)
		require.Equal(t, credentials.ErrStoreLocked.Error(), err.Error())
	case "whole_file_read":
		var pathErr *os.PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, path, pathErr.Path)
		require.ErrorContains(t, err, "credentials: read store file")
	case "whole_file_json":
		var syntaxErr *json.SyntaxError
		require.ErrorAs(t, err, &syntaxErr)
		require.ErrorContains(t, err, "credentials: store file corrupted (manual fix required)")
	case "submitted_ref":
		var entryErr *credentials.EntryEncodingError
		require.ErrorAs(t, err, &entryErr)
		require.Equal(t, reference, entryErr.Name)
		require.ErrorContains(t, err, "nonce must be 12 bytes, got 1")
	default:
		t.Fatalf("unknown safety fault %q", fault)
	}
}

// Independent safety control for P0-b's partial-read fix: FR-069 / FR-089 and
// the brief prohibit dispatch on whole-store or THIS submitted-ref failure.
func TestWU4P0BResumeDispatchRefusesUnusableStoreOrSubmittedReference(t *testing.T) {
	for _, fault := range []string{"healthy", "locked", "whole_file_read", "whole_file_json", "submitted_ref"} {
		t.Run(fault, func(t *testing.T) {
			h := newWU4PreclaimFixture(t, false)
			store := h.f.store
			require.NoError(t, store.Set(wu4ResumeSafetyWitnessRef, wu4ResumeSafetyWitnessValue))
			witness, err := store.Get(wu4ResumeSafetyWitnessRef)
			require.NoError(t, err)
			require.Equal(t, wu4ResumeSafetyWitnessValue, witness)
			values, err := store.SensitiveValues()
			require.NoError(t, err)
			require.ElementsMatch(t, []string{secretWakeAcceptanceValue, wu4ResumeSafetyWitnessValue}, values,
				"instrument: both the submitted reference and unrelated witness initially decrypt")
			path := store.Path()
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			ref := h.record.Card.ReferenceNames[0]
			var cut *wu4PreclaimFileCut
			locked := false
			restore := func() {
				if locked {
					// The shared fixture's documented synthetic key, never a real credential.
					require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
					locked = false
				}
				if cut != nil {
					require.NoError(t, cut.restore())
				}
			}
			t.Cleanup(restore)
			switch fault {
			case "locked":
				store.Close()
				locked = true
			case "whole_file_read":
				cut = wu4PreclaimBlockFile(t, path)
			case "whole_file_json":
				cut = wu4PreclaimBlockFile(t, path)
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
			case "submitted_ref":
				cut = wu4PreclaimBlockFile(t, path)
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.WriteFile(path, before, 0o600))
				wu4ResumeSafetyCorruptSubmittedNonce(t, path, ref)
				readable, scanErr := store.SensitiveValues()
				var damage *credentials.DamagedEntriesError
				require.ErrorAs(t, scanErr, &damage)
				require.Equal(t, []string{ref}, damage.Names)
				require.Equal(t, []string{wu4ResumeSafetyWitnessValue}, readable,
					"instrument: a real unrelated witness still decrypts during the submitted-ref fault")
			}
			if fault == "locked" || fault == "whole_file_read" || fault == "whole_file_json" {
				_, scanErr := store.SensitiveValues()
				wu4ResumeSafetyRequireError(t, fault, path, ref, scanErr)
			}
			// Invoke the actual dispatch boundary, not a mock resume callback or
			// a RetryResumes call that the already-accepted transport would skip.
			dispatchErr := h.f.al.dispatchSecretResume(h.record)
			if dispatchErr != nil {
				h.diagnostics = append(h.diagnostics, dispatchErr.Error())
			}
			wu4ResumeSafetyRequireError(t, fault, path, ref, dispatchErr)
			wakes := h.takeRetryWakes()
			if fault == "healthy" {
				require.Len(t, wakes, 1, "positive control: healthy dispatch really publishes a reference-only wake")
				require.Equal(t, h.record.Card.RequestId, wakes[0].Metadata[secretResumeMetadataKey])
				require.NotContains(t, wakes[0].Content, secretWakeAcceptanceValue)
				require.NotContains(t, wakes[0].Content, wu4ResumeSafetyWitnessValue)
			} else {
				require.Empty(t, wakes, "a readable unrelated witness cannot authorize an unsafe submitted continuation")
			}
			restore()
			require.Zero(t, secretWakeClaimCount(t, h.f.al, h.record))
			require.Len(t, h.f.provider.calls(h.f.root), 1, "dispatch checks never run a model continuation")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after, "refusal must preserve all previously saved values/versions and the submit receipt")
			value, err := store.Get(ref)
			require.NoError(t, err)
			require.Equal(t, secretWakeAcceptanceValue, value)
			witness, err = store.Get(wu4ResumeSafetyWitnessRef)
			require.NoError(t, err)
			require.Equal(t, wu4ResumeSafetyWitnessValue, witness)
			walkErr := filepath.WalkDir(h.f.home, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.Type().IsRegular() {
					return nil
				}
				raw, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				assert.NotContains(t, string(raw), wu4ResumeSafetyWitnessValue, "no saved file/log/transcript may expose the unrelated witness")
				return nil
			})
			require.NoError(t, walkErr)
		})
	}
}

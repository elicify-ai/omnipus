package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wu4P0UnrelatedRef  = "WU4_P0B_UNRELATED_DAMAGED"
	wu4P0UnrelatedVal  = "wu4-p0b-unrelated-synthetic-value"
	wu4P0WitnessRef    = "WU4_P0B_READABLE_WITNESS"
	wu4P0WitnessVal    = "wu4-p0b-readable-synthetic-witness"
	wu4P0DamagedNonce  = "WU4_P0B_MALFORMED_NONCE%%%"
	wu4P0LogInstrument = "WU4 P0-b/c public capture control"
)

// Capture both the native application sink and structured credential errors.
// The returned check is deferred, so an early refusal cannot skip no-leak proof.
func wu4P0CaptureResumeLogs(t *testing.T, f *secretWakeAcceptanceFixture, diagnostics *[]string) func() {
	t.Helper()
	path := filepath.Join(f.home, "p0-resume.log")
	require.NoError(t, logger.EnableFileLogging(path))
	previous := slog.Default()
	slog.SetDefault(slog.New(logger.NewSlogHandler()))
	t.Cleanup(func() { slog.SetDefault(previous); logger.DisableFileLogging() })
	slog.Info(wu4P0LogInstrument)
	return func() {
		raw, err := os.ReadFile(path)
		assert.NoError(t, err)
		assert.Contains(t, string(raw), wu4P0LogInstrument, "instrument: the real log sink must be active")
		text := string(raw) + strings.Join(*diagnostics, "\n")
		for _, value := range []string{secretWakeAcceptanceValue, wu4P0UnrelatedVal, wu4P0WitnessVal, wu4P0DamagedNonce} {
			assert.NotContains(t, text, value, "P0-b/c logs and returned errors must be value-free")
		}
		walkErr := filepath.WalkDir(f.home, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, value := range []string{secretWakeAcceptanceValue, wu4P0UnrelatedVal, wu4P0WitnessVal} {
				assert.NotContains(t, string(data), value, "P0-b/c durable file %s must not expose plaintext", path)
			}
			return nil
		})
		assert.NoError(t, walkErr)
	}
}

// Only the unrelated entry's encoding is damaged. Submit must still commit its
// own healthy value and the complete readable masking set must include it.
func wu4P0DamageUnrelatedEntry(t *testing.T, f *secretWakeAcceptanceFixture) {
	t.Helper()
	require.NoError(t, f.store.Set(wu4P0UnrelatedRef, wu4P0UnrelatedVal))
	require.NoError(t, f.store.Set(wu4P0WitnessRef, wu4P0WitnessVal))
	values, err := f.store.SensitiveValues()
	require.NoError(t, err, "instrument: both original entries are healthy")
	require.ElementsMatch(t, []string{wu4P0UnrelatedVal, wu4P0WitnessVal}, values)
	raw, err := os.ReadFile(f.store.Path())
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var entries map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["credentials"], &entries))
	require.Contains(t, entries, wu4P0UnrelatedRef)
	entries[wu4P0UnrelatedRef]["nonce"], err = json.Marshal(wu4P0DamagedNonce)
	require.NoError(t, err)
	document["credentials"], err = json.Marshal(entries)
	require.NoError(t, err)
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.store.Path(), raw, 0o600))
	value, err := f.store.Get(wu4P0UnrelatedRef)
	require.ErrorContains(t, err, wu4P0UnrelatedRef, "instrument: only the selected unrelated entry is unreadable")
	require.Empty(t, value)
	value, err = f.store.Get(wu4P0WitnessRef)
	require.NoError(t, err)
	require.Equal(t, wu4P0WitnessVal, value)
}

func wu4P0RequireSavedReferences(t *testing.T, f *secretWakeAcceptanceFixture, rec secretrequest.Record) {
	t.Helper()
	got, err := f.al.SecretRequests().Get(rec.Card.RequestId, rec.Owner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State, "a resume fault must not roll back the committed receipt")
	require.Equal(t, rec.Card.ReferenceNames, got.Card.ReferenceNames)
	require.Len(t, got.Card.ReferenceNames, 1)
	for _, ref := range got.Card.ReferenceNames {
		value, readErr := f.store.Get(ref)
		require.NoError(t, readErr)
		require.Equal(t, secretWakeAcceptanceValue, value)
	}
}

func wu4P0RequireNoGenericMarker(t *testing.T, f *secretWakeAcceptanceFixture, rec secretrequest.Record, messageID string) {
	t.Helper()
	entries, err := f.al.GetSessionStore().ReadTranscript(rec.Card.SessionId)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "consumed-"+messageID, entry.ID, "binding B: the secret claim is the only consumption record")
		require.NotEqual(t, "consumed "+messageID, entry.Content, "binding B: never persist a redundant generic wake marker")
	}
}

func wu4P0ProcessWake(t *testing.T, f *secretWakeAcceptanceFixture, wake bus.InboundMessage, diagnostics *[]string) error {
	t.Helper()
	wake.Metadata = maps.Clone(wake.Metadata)
	_, _, err := f.al.processMessage(context.Background(), wake)
	if err != nil {
		*diagnostics = append(*diagnostics, err.Error())
	}
	joinGoalFixtureRuns(t, f.al)
	return err
}

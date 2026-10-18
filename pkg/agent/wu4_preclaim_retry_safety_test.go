package agent

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wu4PreclaimNativeControl     = "WU4 SF02 native log capture is live"
	wu4PreclaimStructuredControl = "WU4 SF02 structured log capture is live"
	wu4PreclaimMaskControl       = "WU4 SF02 masking control"
)

// Own the two level controls as well as the sink: a prior suite member's log
// threshold must not turn an empty log into false no-plaintext evidence.
func (h *wu4PreclaimFixture) captureSafety(t *testing.T) {
	t.Helper()
	oldLevel, oldGlobal, oldSlog := logger.GetLevel(), zerolog.GlobalLevel(), slog.Default()
	logger.SetLevel(logger.DEBUG)
	logPath := filepath.Join(h.f.home, "sf02-native-structured.log")
	t.Cleanup(func() {
		slog.SetDefault(oldSlog)
		logger.DisableFileLogging()
		logger.SetLevel(oldLevel)
		zerolog.SetGlobalLevel(oldGlobal)
	})
	require.NoError(t, logger.EnableFileLogging(logPath))
	slog.SetDefault(slog.New(logger.NewSlogHandler()))
	logger.InfoCF("wu4.sf02", wu4PreclaimNativeControl, map[string]any{"stage": "instrument"})
	slog.Info(wu4PreclaimStructuredControl, "stage", "instrument")
	instrument, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(instrument), wu4PreclaimNativeControl)
	require.Contains(t, string(instrument), wu4PreclaimStructuredControl)
	var mu sync.Mutex
	var events []Event
	h.f.al.SetEventSyncTap(func(event Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	t.Cleanup(func() {
		h.f.al.SetEventSyncTap(nil)
		mu.Lock()
		captured := append([]Event(nil), events...)
		mu.Unlock()
		require.NotEmpty(t, captured, "instrument: capture actual event-frame producer inputs")
		rawEvents, marshalErr := json.Marshal(captured)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(rawEvents), "request_secret", "instrument: actual original tool events were observed")
		assert.NotContains(t, string(rawEvents), secretWakeAcceptanceValue, "event-frame inputs must not expose plaintext")
		if h.record.Card.RequestId != "" {
			cards := h.f.al.SecretRequests().Snapshot(h.record.Owner, h.f.root)
			rawCards, cardErr := json.Marshal(cards)
			require.NoError(t, cardErr)
			assert.Contains(t, string(rawCards), h.record.Card.RequestId, "instrument: generated session-state card data is nonempty")
			assert.NotContains(t, string(rawCards), secretWakeAcceptanceValue, "generated card/snapshot data must be value-free")
			rawWake, wakeErr := json.Marshal(h.original)
			require.NoError(t, wakeErr)
			assert.Contains(t, string(rawWake), h.record.Card.RequestId)
			assert.NotContains(t, string(rawWake), secretWakeAcceptanceValue, "actual accepted bus wake must be reference-only")
		}
		rawPrompts, promptErr := json.Marshal(h.f.provider.calls(h.f.root))
		require.NoError(t, promptErr)
		assert.NotContains(t, string(rawPrompts), secretWakeAcceptanceValue, "no native model input may expose the submitted value")
		rawLogs, readErr := os.ReadFile(logPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(rawLogs), wu4PreclaimNativeControl)
		assert.Contains(t, string(rawLogs), wu4PreclaimStructuredControl)
		if h.record.Card.RequestId != "" {
			assert.Contains(t, string(rawLogs), wu4PreclaimMaskControl, "masking control must really reach both sinks")
			assert.Contains(t, string(rawLogs), "[FILTERED]", "the plaintext-bearing masking instrument must actually be scrubbed")
		}
		assert.NotContains(t, string(rawLogs)+strings.Join(h.diagnostics, "\n"), secretWakeAcceptanceValue,
			"native/structured logs and returned errors must never disclose the secret")
		walkErr := filepath.WalkDir(h.f.home, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			assert.NotContains(t, string(raw), secretWakeAcceptanceValue, "durable transcript/archive/log/credential file %s must not contain plaintext", path)
			return nil
		})
		require.NoError(t, walkErr)
	})
}

func (h *wu4PreclaimFixture) emitMaskControls(t *testing.T) {
	t.Helper()
	require.Equal(t, "[FILTERED]", h.f.al.GetConfig().FilterSensitiveData(secretWakeAcceptanceValue),
		"successful dispatch must have registered the saved value before consumption")
	logger.InfoCF("wu4.sf02", wu4PreclaimMaskControl, map[string]any{"value": secretWakeAcceptanceValue})
	slog.Info(wu4PreclaimMaskControl, "value", secretWakeAcceptanceValue)
}

func (h *wu4PreclaimFixture) requireSavedUnchanged(t *testing.T) {
	t.Helper()
	got, err := h.f.al.SecretRequests().Get(h.record.Card.RequestId, h.record.Owner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State, "pre-claim faults, retry and Stop never roll back the committed value")
	require.Equal(t, h.record.Card.ReferenceNames, got.Card.ReferenceNames)
	for _, ref := range got.Card.ReferenceNames {
		value, readErr := h.f.store.Get(ref)
		require.NoError(t, readErr)
		require.Equal(t, secretWakeAcceptanceValue, value, "retry must not overwrite the saved version")
	}
	names, err := h.f.store.List()
	require.NoError(t, err)
	require.Equal(t, h.credentialNames, names, "retry must not create another saved version")
	metadata, err := h.f.store.CardSecretMetadata()
	require.NoError(t, err)
	require.Equal(t, h.metadata, metadata, "all version numbers and recipient bindings must survive")
	receipt, exists, err := h.f.store.CardReceipt(h.record.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, h.receipt, receipt, "original atomic commit fact must remain unchanged")
	raw, err := os.ReadFile(h.f.store.Path())
	require.NoError(t, err)
	require.Equal(t, h.credentialBytes, raw, "accepted wake retries must not rewrite encrypted storage")
}

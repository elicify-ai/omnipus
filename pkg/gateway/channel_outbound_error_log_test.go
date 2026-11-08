package gateway

// Oracle: PR #1581, CodeQL 2972/2981, the chief's no-user-target logging
// condition and team-lead's two-case ruling (2026-11-02). The authenticated
// REST handler, schema/target validation and saved-config reader stay real.
// Only the log destination is replaced; a directory at the config-file path
// supplies a deterministic, real post-validation filesystem failure.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type outboundSaveLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *outboundSaveLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *outboundSaveLogBuffer) take() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := append([]byte(nil), b.buffer.Bytes()...)
	b.buffer.Reset()
	return data
}

func outboundSaveLoggedRecords(t *testing.T, raw []byte) ([]map[string]any, []string) {
	t.Helper()
	var records []map[string]any
	var values []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var recordJSON json.RawMessage
		err := decoder.Decode(&recordJSON)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err, "instrument: each captured log record must decode")
		var record map[string]any
		require.NoError(t, json.Unmarshal(recordJSON, &record))
		records = append(records, record)

		// Tokens preserve duplicate keys and nested attributes; UseNumber avoids
		// rounding a 19-digit target. Decoding also restores escaped newlines:
		// JSON escaping is not proof that the target was absent from the log.
		tokens := json.NewDecoder(bytes.NewReader(recordJSON))
		tokens.UseNumber()
		for {
			token, err := tokens.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "instrument: every log token must be observable")
			switch value := token.(type) {
			case string:
				values = append(values, value)
			case json.Number:
				values = append(values, value.String())
			}
		}
	}
	return records, values
}

func TestChannelOutboundSaveErrorLogNeverCarriesUserTargets(t *testing.T) {
	f, _ := t14Fixture(t)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.ValidateInbound = true
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	require.True(t, f.al.GetConfig().Gateway.ValidateInbound, "instrument: schema validation is enabled, not bypassed")
	before := t13Before(t, f)
	revision := t14Read(t, f, "telegram").Revision
	path, backup := f.f1277.configPath, f.f1277.configPath+".codeql-log-fixture"
	require.NoError(t, os.Rename(path, backup))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	require.NoError(t, os.Mkdir(path, 0o700))

	capture := &outboundSaveLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	// The first marker is a valid signed-64-bit Telegram id. Its padding is
	// trimmed by the contract. The original printable attack is invalid, and
	// must not be forced past target validation just to reach the 500 branch.
	const numericMarker = "7347297229811234567"
	printableMarker := "INJECTED-2972-2981-" + strings.Repeat("x", 200)
	cases := []struct {
		name, target string
		status       int
	}{
		{"post-validation save failure", "\n" + strings.Repeat(" ", 200) + numericMarker, http.StatusInternalServerError},
		{"printable target rejected before save", "evil\n" + printableMarker, http.StatusBadRequest},
	}
	for _, tc := range cases {
		// ChannelOutboundUpdateRequest.yaml: raw entries have maxLength 256.
		require.LessOrEqual(t, len([]rune(tc.target)), 256, "instrument: %s stays within the raw request bound", tc.name)
		rec := t14Put(t, f, "telegram", "reply_only", []string{tc.target}, revision)
		require.Equal(t, tc.status, rec.Code, "%s: exercise the intended branch, not an early unrelated rejection: %s", tc.name, rec.Body.String())
		records, values := outboundSaveLoggedRecords(t, capture.take())
		var saveErrors []map[string]any
		for _, record := range records {
			if record["msg"] == "rest: save channel outbound" {
				saveErrors = append(saveErrors, record)
			}
		}
		if tc.status == http.StatusInternalServerError {
			require.JSONEq(t, `{"error":"could not save the setting; nothing was changed"}`, rec.Body.String())
			require.Len(t, saveErrors, 1, "instrument: the real default 500 error record must be captured")
			require.Equal(t, "ERROR", saveErrors[0]["level"])
			require.Equal(t, "telegram", saveErrors[0]["channel"])
			require.Contains(t, saveErrors[0]["error"], path, "instrument: the error attribute carries the real filesystem failure")
		} else {
			t14Rejection(t, rec, http.StatusBadRequest, "invalid_targets")
			require.Empty(t, saveErrors, "the malformed target must not reach the poisoned save phase or its error log")
		}
		for _, value := range values {
			assert.NotContains(t, value, numericMarker, "%s: no logged message or attribute may carry the user target", tc.name)
			assert.NotContains(t, value, printableMarker, "%s: no logged message or attribute may carry the printable attack marker", tc.name)
			assert.NotContains(t, value, "\n", "%s: no raw target newline may enter a logged message or attribute", tc.name)
		}
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "%s: neither request may replace the failing config-path sentinel", tc.name)
	}
	restore()
	t13Unchanged(t, f, before)
}

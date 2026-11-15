package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

// #1666 CHECK finding 1. ORACLE, independent of today's code: a credential
// registered for masking never reaches a log sink in plain form or in an
// escaped form. Escaped forms are the two spellings that hide a character
// from an exact-plaintext replacer: Go's %q inner spelling (strconv.Quote
// without the surrounding quotes — the spelling safeLogValue uses) and a
// \uNNNN spelling of every non-plain character. The mask that must stand in
// its place is [FILTERED] (pkg/config/security.go::sensitiveReplacementPairs).
//
// The values below are synthetic. They are not real credentials. None of
// these tests may run in parallel: they install the process-wide slog bridge,
// the process-wide file sink and the process-wide credential replacer.

const codeQL1666MaskToken = "[FILTERED]"

const codeQL1666PlainCredential = "plain-needle-4K"

type codeQL1666CredentialShape struct {
	name  string
	value string
}

func codeQL1666CredentialShapes() []codeQL1666CredentialShape {
	return []codeQL1666CredentialShape{
		{name: "quote", value: "qa\"needle-7Q"},
		{name: "backslash", value: "back\\slash-9Z"},
		{name: "control", value: "tab\tsecret-3K"},
		{name: "non_ascii", value: "na\u2028dle-4N"},
	}
}

// codeQL1666WithMaskedFileSink is the production logging path:
// logger.EnableFileLogging, gateway_boot.go::installSlogBridge, and
// config.Config.RegisterSensitiveValues (the boot step that publishes the
// process-wide replacer). It returns the gateway.log path.
func codeQL1666WithMaskedFileSink(t *testing.T, credentials ...string) string {
	t.Helper()
	path := enableGatewayLogFile(t)
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	installSlogBridge()
	restoreReplacer := logger.SetSensitiveValueReplacer(nil)
	t.Cleanup(restoreReplacer)
	cfg := &config.Config{}
	cfg.RegisterSensitiveValues(credentials)
	return path
}

func codeQL1666CredentialForms(value string) (plain, quotedInner, unicodeEscaped string) {
	plain = value
	quoted := strconv.Quote(value)
	quotedInner = quoted[1 : len(quoted)-1]
	var b strings.Builder
	for _, r := range value {
		if r < 0x80 && strconv.IsPrint(r) && r != '"' && r != '\\' {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return plain, quotedInner, b.String()
}

func codeQL1666DecodedLog(t *testing.T, raw []byte) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event),
			"fixture: the file sink must emit one JSON object per line")
		codeQL1666WriteJSONStrings(&b, event)
	}
	return b.String()
}

func codeQL1666WriteJSONStrings(b *strings.Builder, v any) {
	switch val := v.(type) {
	case string:
		b.WriteString(val)
		b.WriteByte('\n')
	case map[string]any:
		for _, item := range val {
			codeQL1666WriteJSONStrings(b, item)
		}
	case []any:
		for _, item := range val {
			codeQL1666WriteJSONStrings(b, item)
		}
	}
}

// codeQL1666AssertCredentialMasked fails when any registered form of the
// credential survives in the raw file or in the JSON-decoded field values.
// JSON transport escapes backslashes a second time, so the %q spelling is
// visible only after decoding; both views are the sink.
func codeQL1666AssertCredentialMasked(t *testing.T, logPath, shape, cred, siteMessage string) {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), siteMessage,
		"fixture: the real log site %q must have emitted a record", siteMessage)
	decoded := codeQL1666DecodedLog(t, raw)
	plain, quotedInner, unicodeEscaped := codeQL1666CredentialForms(cred)
	forms := []struct{ kind, form string }{
		{"plaintext", plain},
		{"percent-q inner", quotedInner},
		{"unicode-u", unicodeEscaped},
	}
	for _, form := range forms {
		if form.form == "" {
			continue
		}
		if strings.Contains(string(raw), form.form) || strings.Contains(decoded, form.form) {
			t.Errorf("shape %s: file sink still contains the %s form of the registered credential (site %q); want %s and none of the plain, percent-q or \\u forms",
				shape, form.kind, siteMessage, codeQL1666MaskToken)
		}
	}
	if !strings.Contains(decoded, codeQL1666MaskToken) {
		t.Errorf("shape %s: file sink does not contain the mask token %s (site %q)",
			shape, codeQL1666MaskToken, siteMessage)
	}
}

func codeQL1666DriveServerFailure(t *testing.T, cred string) {
	t.Helper()
	api := &restAPI{homePath: t.TempDir()}
	response := httptest.NewRecorder()
	upload := &restAPIHandleUpload{a: api, w: response, workspaceID: "ws-1666"}
	cause := fmt.Errorf("provider echoed %s", cred)
	flow := upload.finishWorkspaceUpload("item.txt", "media://unused", gen.MediaLibraryEntry{}, cause)
	require.Equal(t, restAPIHandleUploadReturn, flow, "fixture: the store-failure branch must return")
	require.Equal(t, http.StatusInternalServerError, response.Code, "fixture: serverFailureText must have run")
	require.NotContains(t, response.Body.String(), cred,
		"fixture: the client body must stay free of the cause (jsonServerFailure contract)")
}

// TestCodeQL1666_QuotedCredentialNeverReachesFileSink is the #1666 site.
// finishWorkspaceUpload's store-failure branch calls jsonServerFailure, which
// logs the cause through serverFailureText. The cause contains the synthetic
// credential. Each shape is one %q equivalence class.
func TestCodeQL1666_QuotedCredentialNeverReachesFileSink(t *testing.T) {
	for _, shape := range codeQL1666CredentialShapes() {
		t.Run(shape.name, func(t *testing.T) {
			logPath := codeQL1666WithMaskedFileSink(t, shape.value)
			codeQL1666DriveServerFailure(t, shape.value)
			codeQL1666AssertCredentialMasked(t, logPath, shape.name, shape.value, "rest: server-side failure")
		})
	}
}

// TestCodeQL1666_PlainCredentialIsMaskedInFileSink is the harness control.
// A credential with no character %q escapes is still a substring after
// quoting, so today's exact replacer masks it. This must PASS now: a red
// here would mean the bridge, the registration or the file sink is not
// actually masking, and the leak tests above would be failing for that
// reason instead.
func TestCodeQL1666_PlainCredentialIsMaskedInFileSink(t *testing.T) {
	logPath := codeQL1666WithMaskedFileSink(t, codeQL1666PlainCredential)
	codeQL1666DriveServerFailure(t, codeQL1666PlainCredential)
	codeQL1666AssertCredentialMasked(t, logPath, "plain", codeQL1666PlainCredential, "rest: server-side failure")
}

//go:build linux || darwin

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/stretchr/testify/require"
)

// ORACLE, independent of the product code: a credential registered for
// masking never reaches a log sink in plain form or in an escaped form,
// where an escaped form is any Go spelling a log reader could undo by
// unquoting or unescaping (the decode closure below reads up to its own
// bound). The tests drive real gateway log sites whose error text is
// composed from quoted fragments, and read the real file sink.
//
// The values below are synthetic; they are not real credentials. None of
// these tests may run in parallel: they install the process-wide slog
// bridge, file sink and credential replacer.

// codeQL1666UnescapeGo decodes the Go escape sequences in arbitrary text with
// strconv.UnquoteChar, the standard library's own per-character decoder, so
// the oracle reads an escape the way Go does. A sequence that is not a valid
// escape is left as written. The oracle's independence from the product lies
// in the closure built around this decoder (codeQL1666DecodeClosure), not in
// the decoder itself.
func codeQL1666UnescapeGo(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, false
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			value, multibyte, tail, err := strconv.UnquoteChar(s[i:], '"')
			if err != nil {
				// \' is valid only with a single-quote delimiter.
				value, multibyte, tail, err = strconv.UnquoteChar(s[i:], '\'')
			}
			if err == nil {
				if multibyte {
					b.WriteRune(value)
				} else {
					b.WriteByte(byte(value))
				}
				i = len(s) - len(tail)
				changed = true
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), changed
}

// codeQL1666UnquoteWhole adapts strconv.Unquote to the closure's (string, ok)
// shape: only a successfully decoded whole quoted string counts.
func codeQL1666UnquoteWhole(s string) (string, bool) {
	decoded, err := strconv.Unquote(s)
	return decoded, err == nil
}

// codeQL1666DecodeClosure returns every string reachable from seed by
// repeatedly unquoting whole strings and decoding escape sequences. Its
// bound is deeper than the product's own decoding limit, so a field the
// product leaves encoded can still be unwound here. It models a log reader
// undoing whatever escaping sits over the credential.
func codeQL1666DecodeClosure(seed string) []string {
	seen := map[string]bool{seed: true}
	frontier := []string{seed}
	for level := 0; level < 16 && len(frontier) > 0; level++ {
		var next []string
		for _, current := range frontier {
			for _, decode := range []func(string) (string, bool){
				codeQL1666UnquoteWhole,
				codeQL1666UnescapeGo,
			} {
				if variant, ok := decode(current); ok && variant != current && !seen[variant] {
					seen[variant] = true
					next = append(next, variant)
				}
			}
		}
		frontier = next
	}
	out := make([]string, 0, len(seen))
	for variant := range seen {
		out = append(out, variant)
	}
	return out
}

func codeQL1666FileStringValues(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event),
			"fixture: the file sink must emit one JSON object per line")
		out = append(out, codeQL1666CollectStrings(event)...)
	}
	return out
}

func codeQL1666CollectStrings(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case map[string]any:
		var out []string
		for _, item := range val {
			out = append(out, codeQL1666CollectStrings(item)...)
		}
		return out
	case []any:
		var out []string
		for _, item := range val {
			out = append(out, codeQL1666CollectStrings(item)...)
		}
		return out
	}
	return nil
}

// codeQL1666AssertComposedCredentialMasked is the composed-field oracle: no
// decoding path of any decoded field value may yield the registered
// credential's plaintext, and the mask token must stand in for it.
func codeQL1666AssertComposedCredentialMasked(t *testing.T, logPath, shape, cred, siteMessage string) {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), siteMessage,
		"fixture: the real log site %q must have emitted a record", siteMessage)
	values := codeQL1666FileStringValues(t, raw)
	require.NotEmpty(t, values, "fixture: the file sink must hold decoded string values")
	for _, value := range values {
		for _, variant := range codeQL1666DecodeClosure(value) {
			require.NotContains(t, variant, cred,
				"shape %s: a reversible spelling of the registered credential survives the file sink (decoding variant of field %q); want %s",
				shape, value, codeQL1666MaskToken)
		}
	}
	require.Contains(t, strings.Join(values, "\n"), codeQL1666MaskToken,
		"shape %s: the mask token %s must stand in for the credential", shape, codeQL1666MaskToken)
}

// TestCodeQL1666_ComposedErrorQuotedCredentialNeverReachesFileSink drives a
// real media-by-reference request whose workspace id carries the credential.
// The workspace-library error is composed from quoted fragments of that id
// and is logged by the media handler; the logged record must hold no
// decodable copy of the credential and must hold the mask token. The
// backslash shape is skipped because validateEntityID rejects a workspace id
// that holds one; the guard test below covers that shape.
func TestCodeQL1666_ComposedErrorQuotedCredentialNeverReachesFileSink(t *testing.T) {
	for _, shape := range codeQL1666CredentialShapes() {
		if shape.name == "backslash" {
			continue
		}
		t.Run(shape.name, func(t *testing.T) {
			workspaceID := "ws-1666" + shape.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID),
				"fixture: validation must let the credential-bearing workspace id reach the log site")
			logPath := codeQL1666WithMaskedFileSink(t, shape.value)
			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				// Same message shape as the gateway boot's provider error.
				return nil, fmt.Errorf("workspace library unavailable for %q", id)
			})
			api := &restAPI{homePath: t.TempDir(), mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, "media-1666-id"))

			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"internal server error"}`, response.Body.String())
			require.NotContains(t, response.Body.String(), shape.value,
				"fixture: the client body must stay free of the credential")
			codeQL1666AssertComposedCredentialMasked(t, logPath, shape.name, shape.value, "rest: media: resolve failed")
		})
	}
}

// TestCodeQL1666_GuardComposedQuotedCredentialNeverReachesFileSink covers
// the workspace guard's denial, which quotes the caller and referenced
// workspace ids into its message before serveMedia logs it. The registered
// media route derives both ids from the same path segment, so the test
// drives serveMedia directly with a caller workspace that differs from the
// referenced one. The caller workspace carries the credential; the logged
// record must hold no decodable copy of it and must hold the mask token.
// All four credential shapes are used, including the backslash shape.
func TestCodeQL1666_GuardComposedQuotedCredentialNeverReachesFileSink(t *testing.T) {
	for _, shape := range codeQL1666CredentialShapes() {
		t.Run(shape.name, func(t *testing.T) {
			logPath := codeQL1666WithMaskedFileSink(t, shape.value)
			api := &restAPI{homePath: t.TempDir(), mediaStore: media.NewFileMediaStore()}
			ref := media.WorkspaceRefPrefix + "ws-1666-elsewhere/media-1666-id"

			response := httptest.NewRecorder()
			api.serveMedia(response,
				httptest.NewRequest(http.MethodGet, "/api/v1/media/workspace/", nil),
				ref, media.WithCallerWorkspace("ws-1666"+shape.value+"-caller"), ref)

			require.Equal(t, http.StatusForbidden, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"media access denied"}`, response.Body.String())
			codeQL1666AssertComposedCredentialMasked(t, logPath, shape.name, shape.value, "rest: media ref not found")
		})
	}
}

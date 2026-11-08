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

// #1666 CHECK R2-1, round 3. ORACLE, independent of today's code: a
// credential registered for masking never reaches a log sink in plain form
// or in an escaped form — and an escaped form is any reversible Go spelling
// at any depth, because a log reader can undo it. Composed errors carry %q
// fragments INSIDE a longer error text, so the escaping sits at depth 2+,
// where a single whole-field unquote cannot see the credential.
//
// The values below are synthetic; they are not real credentials. None of
// these tests may run in parallel: they install the process-wide slog
// bridge, file sink and credential replacer.

// codeQL1666UnescapeGo decodes the Go escape sequences that can appear in
// arbitrary log text: n r t a b f v, \\, \", \', \xHH, \uNNNN, \UNNNNNNNN
// and octal. Invalid or unknown sequences pass through unchanged. This is
// the reader side of "escaped form": whatever a Go formatter could emit, a
// log reader can decode.
func codeQL1666UnescapeGo(s string) (string, bool) {
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch e := s[i+1]; e {
		case 'a':
			b.WriteByte(7)
			i += 2
			changed = true
		case 'b':
			b.WriteByte(8)
			i += 2
			changed = true
		case 'f':
			b.WriteByte(12)
			i += 2
			changed = true
		case 'n':
			b.WriteByte('\n')
			i += 2
			changed = true
		case 'r':
			b.WriteByte('\r')
			i += 2
			changed = true
		case 't':
			b.WriteByte('\t')
			i += 2
			changed = true
		case 'v':
			b.WriteByte(11)
			i += 2
			changed = true
		case '\\', '"', '\'':
			b.WriteByte(e)
			i += 2
			changed = true
		case 'x', 'u', 'U':
			width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			if i+2+width <= len(s) && codeQL1666HexDigits(s[i+2:i+2+width]) {
				value, _ := strconv.ParseUint(s[i+2:i+2+width], 16, 32)
				b.WriteRune(rune(value))
				i += 2 + width
				changed = true
			} else {
				b.WriteByte(s[i])
				i++
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			digits := 1
			for digits < 3 && i+1+digits < len(s) && s[i+1+digits] >= '0' && s[i+1+digits] <= '7' {
				digits++
			}
			value, _ := strconv.ParseUint(s[i+1:i+1+digits], 8, 8)
			b.WriteByte(byte(value))
			i += 1 + digits
			changed = true
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), changed
}

func codeQL1666HexDigits(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return len(s) > 0
}

// codeQL1666UnquoteWhole adapts strconv.Unquote to the closure's (string, ok)
// shape: only a successfully decoded whole quoted string counts.
func codeQL1666UnquoteWhole(s string) (string, bool) {
	decoded, err := strconv.Unquote(s)
	return decoded, err == nil
}

// codeQL1666DecodeClosure returns every string reachable from seed by
// repeatedly unquoting whole strings and decoding escape sequences, bounded
// at six levels (the composed fixtures nest at most four). It is the test's
// model of a log reader unwinding whatever escaping sits over the
// credential.
func codeQL1666DecodeClosure(seed string) []string {
	seen := map[string]bool{seed: true}
	frontier := []string{seed}
	for level := 0; level < 6 && len(frontier) > 0; level++ {
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

// TestCodeQL1666_ComposedErrorQuotedCredentialNeverReachesFileSink drives
// the composed chain through the real split route: HandleMediaByRef
// (rest_uploads.go:646) -> store.go::resolveWorkspaceRef:386, which wraps
// the provider error in `media store: workspace library %q unavailable: %w`
// around gateway_boot.go:589's `workspace library unavailable for %q` (the
// fixture's provider closure copies that boot line verbatim — the round-1
// resolve-failed precedent), -> serveMedia's catch-all at rest_uploads.go:737.
// The workspace id IS the credential carrier, so the credential sits inside
// TWO %q fragments before safeLogValue quotes the whole error.
// Backslash is excluded: validateEntityID (rest_tasks.go:2104) and
// ParseWorkspaceRef (resolve.go:120) reject it, so it cannot ride a
// workspace id — its real composed carrier is the guard chain below.
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
				return nil, fmt.Errorf("workspace library unavailable for %q", id) // gateway_boot.go:589 shape
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
// the backslash shape's real composed carrier: ValidateCallerWorkspace
// (resolve.go:136) formats `caller=%q ref_workspace=%q` into the denial it
// returns, and serveMedia logs that error at rest_uploads.go:684. The two
// registered routes derive caller and ref workspace from the same path
// segments, so the mismatch is unreachable through them — the test drives
// the real serveMedia method with the mismatched opts a caller in another
// workspace would produce, the same direct-seam precedent the landed #1618
// tests set for finishWorkspaceUpload and round 1 set for the 403 branch.
// The caller workspace has no validator, so all four shapes ride it.
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

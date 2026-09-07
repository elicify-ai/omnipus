// Tests for PUT /api/v1/library/{workspace_id}/content-binary — the
// binary-capable sibling of PUT .../content (which is UTF-8 text only and
// would corrupt arbitrary bytes). See LibraryBinaryContentRequest.yaml and
// handleLibraryContentBinaryPut's doc comment for the design.

package gateway

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLibraryContentBinary_RoundTrip proves the route carries arbitrary
// bytes unmodified — including bytes that are not valid UTF-8, which is
// exactly the case the text route (PUT .../content, string content decoded
// as UTF-8) cannot handle without corruption.
func TestLibraryContentBinary_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	// A minimal PDF-ish blob: real PDFs open with "%PDF-" and are binary
	// throughout: 0x00, 0xFE and 0xFF are not valid standalone UTF-8 bytes.
	raw := append([]byte("%PDF-1.4\n"), 0x00, 0x01, 0xFE, 0xFF, 0x80, 0x7F)
	encoded := base64.StdEncoding.EncodeToString(raw)

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"report.pdf","content_base64":"`+encoded+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "report.pdf", entry.Path)
	assert.Equal(t, int64(len(raw)), entry.Size)
	assert.False(t, entry.IsDir)

	got, err := os.ReadFile(filepath.Join(workDir(api, id), "report.pdf"))
	require.NoError(t, err)
	assert.Equal(t, raw, got, "decoded bytes must round-trip identically — no UTF-8 mangling")
}

// TestLibraryContentBinary_OverwritesExistingFile proves the same
// full-replacement semantics as the text route: a second PUT replaces the
// file's content entirely rather than appending.
func TestLibraryContentBinary_OverwritesExistingFile(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	first := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03})
	w1 := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"blob.bin","content_base64":"`+first+`"}`)
	require.Equal(t, http.StatusOK, w1.Code, "body: %s", w1.Body.String())

	second := base64.StdEncoding.EncodeToString([]byte{0xAA, 0xBB})
	w2 := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"blob.bin","content_base64":"`+second+`"}`)
	require.Equal(t, http.StatusOK, w2.Code, "body: %s", w2.Body.String())

	got, err := os.ReadFile(filepath.Join(workDir(api, id), "blob.bin"))
	require.NoError(t, err)
	assert.Equal(t, []byte{0xAA, 0xBB}, got)
}

func TestLibraryContentBinary_InvalidBase64_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"a.bin","content_base64":"not-valid-base64!!"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestLibraryContentBinary_OversizedDecoded_400 proves the 25 MB decoded-size
// cap is enforced on the DECODED bytes, not merely on some proxy for it.
func TestLibraryContentBinary_OversizedDecoded_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	oversized := make([]byte, maxLibraryBinaryContentBytes+1)
	encoded := base64.StdEncoding.EncodeToString(oversized)
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"big.bin","content_base64":"`+encoded+`"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLibraryContentBinary_MissingParentDir_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"nope/report.pdf","content_base64":"`+encoded+`"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryContentBinary_InvalidPath_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"../escape.bin","content_base64":"`+encoded+`"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

func TestLibraryContentBinary_UnknownWorkspace_404(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	w := libPutJSON(t, api, "/api/v1/library/ws-nope/content-binary",
		`{"path":"a.bin","content_base64":"`+encoded+`"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

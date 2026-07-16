package encoderpage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEncoderPage_EmbedNonEmpty asserts the go:embed picked up a real,
// non-trivial encoder page carrying the expected runtime contract and wire
// markers (Definition of Done: "the Go embed test passes").
func TestEncoderPage_EmbedNonEmpty(t *testing.T) {
	html := EncoderHTML()
	if len(html) == 0 {
		t.Fatal("EncoderHTML() returned empty bytes")
	}
	s := string(html)

	requiredMarkers := []string{
		"<!doctype html>",
		"__OMNIPUS_INGEST__",   // FR-013 injected global
		"browser_ingest_init",  // upstream auth handshake (BrowserIngestInitFrame)
		"browser_frame_feed",   // downstream envelope name (BrowserFrameFeedEnvelope)
		"BrowserChunkEnvelope", // upstream/shared envelope schema this page conforms to
		"video_codec",          // BrowserIngestInitFrame required field
		"has_audio",            // BrowserIngestInitFrame required field
		"VideoEncoder",         // WebCodecs video encode
		"VideoFrame",           // WebCodecs frame type
		"AudioEncoder",         // WebCodecs audio encode (FR-023)
		"MediaStreamTrackProcessor",
		"avc1.4D4028",       // H.264 main codec string (FR-006)
		"'vp8'",             // VP8 fallback codec string
		"createImageBitmap", // FR-001 pipeline step
		"getUserMedia",      // FR-016 audio-only consent surface
		"binaryType",        // arraybuffer WS framing
		"arraybuffer",
		"isConfigSupported", // codec negotiation
		"latencyMode",
		"realtime",
		"CHUNK_KIND_VIDEO", // BrowserChunkEnvelope kind(u8) discriminator (video vs audio mux)
		"CHUNK_KIND_AUDIO",
		"OMNIPUS_TESTABLE_LOGIC_START",
		"OMNIPUS_TESTABLE_LOGIC_END",
	}
	for _, m := range requiredMarkers {
		if !strings.Contains(s, m) {
			t.Errorf("EncoderHTML() missing expected marker %q", m)
		}
	}

	// Self-contained: no external asset references. A remote-origin
	// reference here would violate FR-016's non-navigable/no-remote-fetch
	// requirement for the encoder page.
	forbiddenSubstrings := []string{
		"<script src=",
		"<link ",
		"http://",
		"https://",
		"cdn.",
	}
	for _, f := range forbiddenSubstrings {
		if strings.Contains(s, f) {
			t.Errorf("EncoderHTML() contains forbidden external reference %q — page must be fully self-contained", f)
		}
	}
}

func TestHandler_ServesEmbeddedPage(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, ContentType)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
	if xcto := rec.Header().Get("X-Content-Type-Options"); xcto != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", xcto, "nosniff")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty response body")
	}
	if rec.Body.String() != string(EncoderHTML()) {
		t.Error("response body does not match EncoderHTML()")
	}
}

func TestHandler_HeadHasNoBody(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD response body length = %d, want 0", rec.Body.Len())
	}
}

func TestHandler_RejectsNonGetHead(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/", nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}

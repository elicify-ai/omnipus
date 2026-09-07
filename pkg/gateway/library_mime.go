// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ADR-067 stage 1 — the compiled-in Library type table.
//
// Two questions are answered here and nowhere else (FR-015a, FR-015b):
//
//	1. What Content-Type does the Library send for a file?  Derived from the
//	   EXTENSION only — never from the bytes, and never from the machine the
//	   binary happens to be running on.
//	2. May those bytes be served inline on the preview-token path?  Only for
//	   the extensions on the §10.4 allow-list; everything else, .pdf included,
//	   is an attachment.
//
// Why the host MIME registry is not consulted, at all (FR-015b). The stdlib's
// mime.TypeByExtension answers from Go's small built-in table PLUS whatever
// MIME databases the host has installed (on Unix /etc/mime.types,
// /etc/apache2/mime.types, …). Verified against Go 1.26's
// mime.builtinTypesLower: .aac, .ttf, .otf, .woff, .woff2 are absent from it.
// So on a developer Mac .aac resolves from /etc/apache2/mime.types and in a
// scratch container it does not — it falls through to sniffing and is served
// application/octet-stream, which browsers refuse to play. Same binary,
// different answers, and the test written on the Mac passes while the shipped
// container is broken. Hence: this table is the ONLY source of the type.
//
// Two further traps this table exists to avoid, both real values in Go's
// built-in table: .webm is "audio/webm" there (a video file would be typed as
// audio), and .opus/.ogg both come back as the container type. Any fallback
// to the registry re-introduces both.
//
// Adding an extension to libraryInlineExtensions requires a type-confusion
// case in the same commit (FR-016) — enforced by
// TestInlineAllowList_EveryExtensionHasATypeConfusionCase.

// libraryDefaultContentType is the one stated default for an extension that is
// not in the table: no second guess, no sniff, and always an attachment
// (FR-015c, MV-24).
const libraryDefaultContentType = "application/octet-stream"

// libraryExtContentTypes maps a lower-cased extension (leading dot included)
// to the exact Content-Type the Library sends. Values are the registered IANA
// / MDN types; text types carry an explicit charset because the Library serves
// UTF-8 bytes and nosniff forbids the browser from working it out.
//
// Coverage is fixed by FR-015c: every extension in §10.4 plus .pdf. The set is
// asserted against §10.4 by TestLibraryTypeTable_MatchesInlineAllowList.
var libraryExtContentTypes = map[string]string{
	// §10.4 "Active documents" — the class the isolation policy exists for.
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",

	// §10.4 "Bundle code". Omitting these was the round-4 defect: inline-allowed
	// with no type means octet-stream + nosniff, and every browser refuses a
	// bundle's own stylesheet and script (US-1 AS-4).
	".css": "text/css; charset=utf-8",
	".js":  "text/javascript; charset=utf-8",

	// §10.4 "Fonts" (RFC 8081). None of the four is in Go's built-in table.
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",

	// §10.4 "Images" — the eight raster extensions.
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".ico":  "image/vnd.microsoft.icon",
	".bmp":  "image/bmp",

	// §10.4 ".svg" — scriptable, and on the allow-list anyway because the token
	// path applies one policy to every byte it serves.
	".svg": "image/svg+xml",

	// §10.4 "Media" — seven audio (FR-009, MV-14: a specific type each, never
	// application/octet-stream) and three video.
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",

	// §10.4 "Inert text".
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".json": "application/json",

	// Typed but NOT inline (FR-015c). PDF.js fetches these bytes over the
	// authenticated Library endpoint, which keeps Content-Disposition:
	// attachment, so a PDF never becomes a browser document at all (FR-018).
	// It still needs a correct type for the SPA's own renderer.
	".pdf": "application/pdf",
}

// libraryInlineExtensions is the §10.4 inline allow-list, written out
// explicitly rather than derived from libraryExtContentTypes — the two sets are
// deliberately not the same thing (.pdf is typed and NOT inline), and deriving
// one from the other would make that distinction unexpressible.
//
// Inline means exactly one thing: the preview-token path serves these
// extensions without Content-Disposition: attachment, and every one of those
// responses carries the §10.3 policy.
var libraryInlineExtensions = map[string]struct{}{
	// Active documents
	".html": {}, ".htm": {},
	// Bundle code
	".css": {}, ".js": {},
	// Fonts
	".woff2": {}, ".woff": {}, ".ttf": {}, ".otf": {},
	// Images
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {},
	".webp": {}, ".avif": {}, ".ico": {}, ".bmp": {},
	// SVG
	".svg": {},
	// Media
	".mp3": {}, ".m4a": {}, ".aac": {}, ".ogg": {}, ".opus": {},
	".wav": {}, ".flac": {}, ".mp4": {}, ".webm": {}, ".mov": {},
	// Inert text
	".txt": {}, ".md": {}, ".json": {},
}

// libraryExtOf returns the lower-cased extension of a file name, leading dot
// included, or "" when the name has none.
//
// A dot-leading name with no further dot (".gitignore", ".env") has NO
// extension here, even though path.Ext calls the whole name one. That matters:
// treating a file named ".html" as an HTML document would let a name that is
// really a dotfile pick up the one type that makes the bytes a browser
// document. No extension means octet-stream and attachment, which is the safe
// direction.
func libraryExtOf(name string) string {
	base := name
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		// -1: no dot at all. 0: a dotfile with no further dot.
		return ""
	}
	return strings.ToLower(base[dot:])
}

// libraryContentTypeForExt returns the Content-Type for a lower-cased
// extension, or libraryDefaultContentType when the table does not have it.
// It consults nothing else — not the host registry, not the file's bytes.
func libraryContentTypeForExt(ext string) string {
	if ct, ok := libraryExtContentTypes[strings.ToLower(ext)]; ok {
		return ct
	}
	return libraryDefaultContentType
}

// libraryContentTypeForName is libraryContentTypeForExt over a file name.
func libraryContentTypeForName(name string) string {
	return libraryContentTypeForExt(libraryExtOf(name))
}

// libraryExtIsInline reports whether the extension is on the §10.4 inline
// allow-list.
func libraryExtIsInline(ext string) bool {
	_, ok := libraryInlineExtensions[strings.ToLower(ext)]
	return ok
}

// libraryDispositionForExt maps an extension to the wire disposition value.
// Attachment is the default and covers everything not on the allow-list,
// .pdf included (FR-008).
func libraryDispositionForExt(ext string) gen.LibraryInlineDispositionDisposition {
	if libraryExtIsInline(ext) {
		return gen.LibraryInlineDispositionDispositionInline
	}
	return gen.LibraryInlineDispositionDispositionAttachment
}

// libraryDispositionForName is libraryDispositionForExt over a file name.
func libraryDispositionForName(name string) gen.LibraryInlineDispositionDisposition {
	return libraryDispositionForExt(libraryExtOf(name))
}

// --- The renderer answer (ADR-067 D15, FR-014, FR-017) ---------------------

// libraryRendererForExt names the SPA surface that should draw a file.
//
// WHY THE SERVER ANSWERS THIS AT ALL. The allow-list and the extension→type
// table are compiled into the binary and are the single source of truth
// (FR-015a, FR-015b). A second copy of either in TypeScript is a second answer,
// and the two disagree the first time an extension is added to one of them —
// with the SPA mounting a surface the server will not serve the bytes for,
// which is D15.4's type-confusion case arriving through the front door.
//
// ".svg" RETURNS "image", NOT "html", AND THAT IS THE WHOLE POINT OF §10.4's
// third context. An SVG is reachable three ways and only one of them is a
// document:
//
//	at its token URL          — a document, contained by the §10.3 policy
//	as <img src="logo.svg">   — secure static mode; its scripts never run
//	inside the SPA            — classified as an IMAGE, drawn in an <img>, and
//	                            fetched over the AUTHENTICATED path, which
//	                            serves attachments
//
// So displaying an .svg in the SPA does not make the browser execute it, and
// requires_sandbox is correspondingly false. Answering "html"/true here would
// tell the SPA to mount an active-document surface for every logo in the
// Library — turning the reader into a script host, which is the refactor §10.4
// warns "nothing else notices". The wire schema's requires_sandbox description
// reads the other way ("True for renderer html — including .svg"); §10.4's
// three-context table and test 123 are the authority followed here, and the
// divergence is flagged rather than silently resolved.
func libraryRendererForExt(ext string) gen.LibraryInlineDispositionRenderer {
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		// The ONLY value that makes the bytes a browser document (FR-014).
		return gen.LibraryInlineDispositionRendererHtml
	case ".pdf":
		return gen.LibraryInlineDispositionRendererPdf
	case ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".flac":
		return gen.LibraryInlineDispositionRendererAudio
	case ".mp4", ".webm", ".mov":
		return gen.LibraryInlineDispositionRendererVideo
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".bmp", ".svg":
		return gen.LibraryInlineDispositionRendererImage
	case ".md":
		return gen.LibraryInlineDispositionRendererMarkdown
	case ".txt":
		return gen.LibraryInlineDispositionRendererText
	case ".css", ".js", ".json":
		return gen.LibraryInlineDispositionRendererCode
	default:
		// "none" means offer a download card — the existing behaviour for an
		// unsupported binary (US-1 AS-7).
		return gen.LibraryInlineDispositionRendererNone
	}
}

// libraryRequiresSandbox reports whether DISPLAYING a file makes the browser
// execute it, and therefore whether the SPA must load it through the
// preview-token path inside a sandboxed iframe.
//
// True for exactly one renderer. FR-014: "the system MUST sandbox content the
// BROWSER executes (HTML and bundles). Formats Omnipus renders itself — images,
// video, audio, markdown, Mermaid, code and PDF — are drawn by SPA components,
// never become browser documents, and therefore have no sandbox to apply."
func libraryRequiresSandbox(renderer gen.LibraryInlineDispositionRenderer) bool {
	return renderer == gen.LibraryInlineDispositionRendererHtml
}

// libraryAttachmentReason explains an "attachment" answer to the reader.
//
// Present only when the disposition IS attachment (the schema says so), because
// a reason attached to an inline answer is a field the SPA would have to learn
// to ignore.
func libraryAttachmentReason(ext string) string {
	if ext == ".pdf" {
		// Not an omission and not a limitation: it is FR-018's mechanism.
		return "PDF.js fetches these bytes over the authenticated Library endpoint, " +
			"so a PDF never becomes a browser document at all"
	}
	if ext == "" {
		return "the file has no extension, so its type cannot be derived without " +
			"sniffing its contents, which this path never does"
	}
	return "the extension " + ext + " is not on the inline allow-list"
}

// LibraryInlineDispositionFor builds the full answer for one workspace-relative
// path. Extension-derived from end to end: nothing here opens the file.
func libraryInlineDispositionFor(relPath string) gen.LibraryInlineDisposition {
	ext := libraryExtOf(relPath)
	disposition := libraryDispositionForExt(ext)
	renderer := libraryRendererForExt(ext)

	answer := gen.LibraryInlineDisposition{
		Path:            relPath,
		Extension:       ext,
		Disposition:     disposition,
		ContentType:     libraryContentTypeForExt(ext),
		Renderer:        renderer,
		RequiresSandbox: libraryRequiresSandbox(renderer),
	}
	if disposition == gen.LibraryInlineDispositionDispositionAttachment {
		reason := libraryAttachmentReason(ext)
		answer.Reason = &reason
	}
	return answer
}

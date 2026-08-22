// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 stage 1 — ONE policy on EVERY route that serves Library-resolved
// bytes (FR-008, FR-008b, FR-008c, FR-015, FR-015a).
//
// WHY THIS FILE EXISTS, and it is not the preview feature. Round 4 of the spec
// review found a LIVE exposure that predates ADR-067 entirely:
//
//	/api/v1/media/workspace/{workspace}/{id} is registered withOptionalAuth,
//	resolves a workspace-library entry, and served it with
//	"Content-Disposition: inline" through http.ServeFile carrying NO policy at
//	all. pkg/library/entries.go maps .html -> text/html and .svg ->
//	image/svg+xml. So an HTML file in a workspace media library was served
//	inline, as a real document, ON THE GATEWAY ORIGIN — same-origin with the
//	session cookie and the whole authenticated API.
//
// The second half of the same defect is the type: http.ServeFile and
// http.ServeContent, called with no Content-Type set, ask the HOST operating
// system's MIME registry and then SNIFF the first 512 bytes. FR-015 forbids
// both, and FR-015b explains why the registry half is worse than it looks —
// the same binary answers differently on different machines, so the test
// written on a developer Mac passes while the shipped container is broken.
//
// THE RULE. Every response in pkg/gateway that carries file bytes out of a
// workspace goes through applyLibraryByteHeaders, and therefore carries:
//
//	Content-Type            from libraryExtContentTypes (library_mime.go) —
//	                        the EXTENSION decides, never the bytes, never the
//	                        host registry. Unknown extension =>
//	                        application/octet-stream, one stated default
//	                        (FR-015c, MV-24).
//	X-Content-Type-Options  nosniff, on every response — inline or not
//	                        (FR-015).
//	Content-Disposition     inline ONLY for the §10.4 allow-list; everything
//	                        else, .pdf included, is an attachment (FR-008).
//	Content-Security-Policy the §10.3 literal string below, on every INLINE
//	                        response and only those (FR-008b, MV-13). An
//	                        attachment response carries none — MV-13 asserts
//	                        that half too.
//
// HOW IT IS KEPT TRUE. inline_serving_test.go parses the package's own source
// and fails when (a) any file other than this one writes a Content-Disposition
// header, or (b) the set of functions calling into these helpers changes
// without a matching behavioural case (FR-008c). §10.5's two-URL table was
// written from an incomplete enumeration and was wrong; an enumeration nobody
// re-derives goes stale the same way.
//
// WHAT THIS FILE DOES NOT DO. It does not authenticate, authorise, or contain
// a path. Callers resolve and confine the path FIRST — os.Root for the Library
// routes, the media store's workspace-membership guard for media refs — and
// hand this code an already-authorised absolute path or an already-opened
// file. Setting a good header on bytes the caller should never have opened
// protects nobody.

import (
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// libraryIsolationPolicy is the ADR-067 §10.3 measured isolation policy,
// reproduced BYTE FOR BYTE. It is the whole of the P0 control for stage 1.
//
// Provenance: docs/internal/experiments/preview-isolation/server.py's
// POLICIES["self"], byte-identical to server2.py's `active`. Measured on
// Chromium 151, Firefox 153 and WebKit 26.5, twice each, top-level and
// embedded, with server-side request logs as ground truth: zero of seven
// egress vectors out, opaque origin, cookie unreadable, CSS and JS working.
//
// DO NOT "TIDY" THIS STRING. Three parts of it are load-bearing in ways that
// are invisible from reading it:
//
//   - `allow-same-origin` is ABSENT, deliberately. Withholding it is what
//     makes the origin opaque, which is what makes document.cookie and
//     localStorage THROW on all three engines. Granting it beside
//     allow-scripts hands the page the session cookie and undoes everything
//     else here.
//   - `allow-popups`/`allow-forms`/`allow-downloads` are ABSENT. Measured:
//     with the source directives but no sandbox, window.open still reached
//     the external origin on EVERY engine. No CSP directive covers popup
//     navigation; the sandbox is the only thing that closes it.
//   - BOTH mechanisms are required. sandbox alone let five of seven vectors
//     out; source directives alone let window.open out. Shipping either half
//     satisfies neither FR-005 nor FR-006 — and would still pass any
//     requirement that merely asks for "a policy".
//
// `'unsafe-inline'` is deliberate and is NOT the boundary: a previewed report
// normally contains inline scripts and styles, and removing it breaks ordinary
// documents without adding protection. What contains the page is the opaque
// origin plus zero egress.
//
// `frame-ancestors` is deliberately NOT here and has never been measured. Do
// not add it on reasoning alone (§10.3).
const libraryIsolationPolicy = "sandbox allow-scripts; default-src 'none'; " +
	"script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self'; media-src 'self'; " +
	"frame-src 'self'; connect-src 'none'; form-action 'none'; " +
	"base-uri 'none'; object-src 'none'"

// Header names, written once so the source scan in inline_serving_test.go has
// exactly one legal place to find the Content-Disposition literal.
const (
	headerContentType           = "Content-Type"
	headerContentDisposition    = "Content-Disposition"
	headerContentTypeOptions    = "X-Content-Type-Options"
	headerContentSecurityPolicy = "Content-Security-Policy"
	nosniffValue                = "nosniff"
)

// errLibraryBytesNotAFile is returned when a caller hands a directory to
// serveLibraryPath. Directory listings are not a Library response shape;
// http.ServeFile would have produced one.
var errLibraryBytesNotAFile = errors.New("library: not a regular file")

// applyLibraryByteHeaders sets every header a Library byte response must carry
// and reports the disposition it chose.
//
// displayName is the name the READER knows the file by — the Library-relative
// filename, or the media entry's recorded filename. Its extension, and nothing
// else, decides the Content-Type and whether the response may be inline. The
// on-disk storage path is deliberately NOT consulted: workspace-library bytes
// live at <libdir>/<mediaID> with no extension at all
// (media/library.Library.rawPath), so typing from the storage name would send
// every workspace media file as application/octet-stream.
//
// forceAttachment=true pins the response to an attachment whatever the
// extension says. The authenticated Library path passes true because FR-003g
// requires it to keep serving attachments unchanged: inline serving is the
// token path's job, and only the token path carries a credential scoped to one
// file.
//
// Callers MUST call this BEFORE writing any bytes. It is not a decoration on
// the way out — http.ServeContent consults the Content-Type header it finds and
// only sniffs when there is none, so setting the type here is what makes the
// sniff structurally unreachable rather than merely unlikely.
func applyLibraryByteHeaders(
	w http.ResponseWriter,
	displayName string,
	forceAttachment bool,
) gen.LibraryInlineDispositionDisposition {
	ext := libraryExtOf(displayName)
	disposition := libraryDispositionForExt(ext)
	if forceAttachment {
		disposition = gen.LibraryInlineDispositionDispositionAttachment
	}

	h := w.Header()
	h.Set(headerContentType, libraryContentTypeForExt(ext))
	h.Set(headerContentTypeOptions, nosniffValue)

	if disposition == gen.LibraryInlineDispositionDispositionInline {
		h.Set(headerContentDisposition, "inline")
		// FR-008b / MV-13: every inline response, whatever the file's type.
		h.Set(headerContentSecurityPolicy, libraryIsolationPolicy)
		return disposition
	}

	// RFC 6266 with both the ASCII fallback and filename* — see
	// contentDispositionAttachment (rest_library.go) for why fmt.Sprintf("%q")
	// was not good enough.
	h.Set(headerContentDisposition, contentDispositionAttachment(displayName))
	// MV-13's second half: an attachment response carries NO policy. Deleting
	// nothing here is the point — a stray Set would make the "attachment
	// responses carry none" assertion fail, which is what it is for.
	return disposition
}

// applyLibraryPreviewByteHeaders is applyLibraryByteHeaders for the
// preview-token path, where §10.3 is stricter: "Every response on the
// preview-token path MUST carry exactly this Content-Security-Policy, byte for
// byte, WHATEVER THE FILE'S TYPE" — so an attachment response there carries it
// too, unlike one on the authenticated Library path (MV-13).
//
// That is the whole difference. Type, nosniff and the §10.4 allow-list
// decision are identical, and stay identical, because they are made in one
// place. A second copy of the policy string or of the disposition rule
// anywhere in this package is a defect: two copies drift, and the copy that
// drifts silently is the one nobody re-reads.
//
// The caller still owns Content-Length, Last-Modified, Access-Control-Allow-
// Origin (FR-019's webfont case) and the body write.
func applyLibraryPreviewByteHeaders(
	w http.ResponseWriter,
	displayName string,
) gen.LibraryInlineDispositionDisposition {
	disposition := applyLibraryByteHeaders(w, displayName, false)
	w.Header().Set(headerContentSecurityPolicy, libraryIsolationPolicy)
	return disposition
}

// serveLibraryContent serves already-opened bytes with the headers above.
//
// The name argument to http.ServeContent is deliberately empty. That argument
// exists only so ServeContent can guess a type from its extension, which is
// exactly the guess FR-015b forbids; passing "" removes the code path rather
// than merely stepping around it. ServeContent is still used, and only used,
// for what it is genuinely good at: Range requests (audio and video seeking)
// and conditional GETs. It cannot sniff here, because the Content-Type header
// is already set — that is the one and only condition under which its sniffing
// branch is skipped, so the ordering above is a correctness requirement, not a
// style choice.
func serveLibraryContent(
	w http.ResponseWriter,
	r *http.Request,
	content io.ReadSeeker,
	modTime time.Time,
	displayName string,
	forceAttachment bool,
) {
	applyLibraryByteHeaders(w, displayName, forceAttachment)
	http.ServeContent(w, r, "", modTime, content)
}

// serveLibraryPath opens absPath and serves it through serveLibraryContent.
//
// absPath MUST already be resolved and confined by the caller. The file is
// opened and stat'ed BEFORE any header is written so that an open failure can
// still produce a real HTTP error status instead of a 200 with an empty body.
func serveLibraryPath(
	w http.ResponseWriter,
	r *http.Request,
	absPath string,
	displayName string,
) error {
	f, err := os.Open(absPath) //nolint:gosec // caller-confined path; see doc comment
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return errLibraryBytesNotAFile
	}

	serveLibraryContent(w, r, f, fi.ModTime(), displayName, false)
	return nil
}

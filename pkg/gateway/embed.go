package gateway

import (
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// spaContentSecurityPolicy is the ADR-067 §10.7 policy for the SPA shell
// (FR-019b), reproduced from the specification.
//
// WHY THE SPA NEEDS ONE AT ALL. It was served with no policy whatsoever, which
// was tolerable while it rendered only its own code. It is not tolerable once
// it parses arbitrary PDFs — from disk, and from agents — in the same origin as
// the session cookie and the whole authenticated API.
//
// `frame-ancestors 'none'` is FR-006b and is part of THIS string, not a second
// header: the measured preview policy (§10.3) contains `frame-src 'self'`, so a
// previewed page may embed any gateway page including the real SPA. The nested
// context reads no cookie and reaches no network, so the residual is interface
// deception rather than data disclosure — genuine Omnipus chrome rendered inside
// attacker-authored content. Narrowing `frame-src` on the preview side instead
// would invalidate the three-engine measurement the entire P0 control rests on,
// so the control belongs on the FRAMED resource, which is here.
//
// NON-NEGOTIABLE: no 'unsafe-eval'. If a bundled library ever needs it, the
// library is reconfigured or replaced — FR-019a is exactly that move for PDF.js,
// whose scripting interpreter is excluded from the build rather than disabled by
// a flag. TestSpaCsp_DirectiveFloor asserts this and the rest of MV-25's floor
// with a checker it also proves can fail.
//
// UNLIKE §10.3, THIS STRING IS NOT MEASURED. §10.7 says so itself: it is a
// proposal, frozen only after a headed run in Chromium, Firefox and Safari with
// zero violations while exercising initial load, the WebSocket, a Mermaid
// diagram, a highlighted code block and a PDF. Its assumptions, each with the
// symptom if wrong:
//
//	no inline bootstrap script          → white screen at boot
//	worker-src covers the built PDF.js
//	  worker URL                        → PDF.js does NOT fail; it falls back to
//	                                      parsing on the main thread, which
//	                                      FR-019c forbids, with a console warning
//	                                      as the only symptom (test 96 asserts the
//	                                      THREAD, not the configuration)
//	Tailwind and Radix need inline
//	  styles                            → broken layout
//	same-origin WebSocket matches 'self'→ the live connection silently fails
//	Shiki needs no WebAssembly          → MEASURED FALSE, 2026-09-05, and the
//	                                      symptom was worse than predicted.
//	                                      Shiki's DEFAULT engine is Oniguruma
//	                                      compiled to WebAssembly, and
//	                                      `script-src 'self'` refuses
//	                                      WebAssembly.instantiate without
//	                                      'wasm-unsafe-eval'. react-shiki
//	                                      catches the CompileError and renders
//	                                      NOTHING — an empty box, no plain-text
//	                                      fallback — for EVERY language,
//	                                      including 'text'. So code blocks did
//	                                      not "stop highlighting"; the code
//	                                      disappeared. Found through the .base
//	                                      "View raw" pane (view-kinds UAT D2),
//	                                      but it was never a .base problem.
//	                                      RESOLVED THE WAY THIS COMMENT SAYS TO
//	                                      resolve it — the library is
//	                                      reconfigured, not the policy widened:
//	                                      markdown-shared.tsx now passes Shiki's
//	                                      pure-JavaScript regex engine
//	                                      (createJavaScriptRegexEngine, the same
//	                                      move FR-019a made for PDF.js), so the
//	                                      assumption is true again by
//	                                      construction rather than by hope. Do
//	                                      not "fix" a future Shiki regression by
//	                                      adding 'wasm-unsafe-eval' here.
//	nothing embeds the SPA              → any embedding surface goes blank
const spaContentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"worker-src 'self' blob:; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; " +
	// connect-src carries the ICE SCHEMES as well as 'self'. Not a wildcard:
	// ordinary HTTP and WebSocket egress stays pinned to the gateway, and only
	// stun:/turn:/turns: are opened — schemes that cannot carry a document or
	// a fetch, so this costs none of what the policy is for.
	//
	// It is here because `connect-src 'self'` alone shipped once and broke the
	// live browser view: browserWebRTC.ts configures stun:stun.l.google.com
	// unconditionally in both peer-connection factories, and ADR-062 tier 3
	// adds gateway-minted turn:/turns: relays. See §10.7's 2026-08-23
	// amendment for the measurement — note the symptom was a SERVER-side
	// "capture/encoder/ICE" error frame, with nothing anywhere naming CSP.
	"connect-src 'self' stun: turn: turns:; frame-src 'self'; object-src 'none'; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'"

// pdfJSAssetPathPrefix is the SPA-relative prefix PDF.js's runtime assets and
// worker are served from (FR-018a, FR-018b).
//
// It MUST equal vite.config.ts's PDFJS_ASSET_PREFIX. The two live in different
// languages with no shared source, so TestSpaEmbed_PdfJsPrefixMatchesTheBuild
// reads the TypeScript and compares — a rename on either side would otherwise
// leave the 404 branch guarding a prefix nothing is served under, which restores
// the silent-blank-PDF failure with no other symptom.
const pdfJSAssetPathPrefix = "pdfjs/"

// spaFS is the embedded SPA filesystem.
// Requires the spa/ directory to exist at build time (run 'pnpm build' first).
//
//go:embed all:spa
var spaFS embed.FS

// newSPAHandler returns an http.Handler that serves the embedded SPA,
// or nil if no SPA was embedded at build time.
func newSPAHandler() http.Handler {
	sub, err := fs.Sub(spaFS, "spa")
	if err != nil {
		// No embedded SPA - return nil to signal gateway to skip registration
		return nil
	}
	fileServer := http.FileServer(http.FS(sub))

	spaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ADR-067 FR-019b / FR-006b — the SPA's own Content-Security-Policy.
		//
		// ONE header, never two. Multiple Content-Security-Policy headers are
		// INTERSECTED rather than merged, so a second one is not an error — it
		// silently makes the effective policy something neither string states.
		// FR-006b's `frame-ancestors 'none'` is therefore absorbed into the
		// §10.7 string below rather than sent alongside it.
		w.Header().Set(headerContentSecurityPolicy, spaContentSecurityPolicy)

		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if the file exists in the embedded FS
		cleanPath := strings.TrimPrefix(path, "/")
		if _, err := fs.Stat(sub, cleanPath); err == nil {
			switch {
			case cleanPath == "index.html" || cleanPath == "":
				// index.html must never be cached — it references hashed JS/CSS bundles.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			case cleanPath == "sw.js" || cleanPath == "manifest.json":
				// M14: service worker and manifest must always be fresh.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			case strings.HasPrefix(cleanPath, "assets/"):
				// M4: Vite hashes asset filenames (e.g. index-Abc123.js).
				// These can be cached indefinitely by the browser.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// ADR-067 FR-018b — a missing runtime asset must fail VISIBLY.
		//
		// Everything below this line is the SPA-routing fallback: an unmatched
		// path is answered with index.html and HTTP 200, which is correct for
		// /library, /chat and every other client-side route. It is catastrophic
		// for an ASSET request. PDF.js fetches its character maps, standard
		// fonts, WASM and colour profiles per DOCUMENT, over plain HTTP, and it
		// checks the status: a 404 it can report, a 200 carrying an HTML page it
		// cannot. Without cmaps/ a Japanese, Chinese or Korean PDF then renders
		// BLANK, with nothing anywhere naming the cause — which is precisely the
		// failure FR-018b exists to prevent, reached by a fallback that looks
		// like a success.
		//
		// So the fallback stops at the PDF.js prefix. A request under it either
		// finds a real embedded file above, or gets a real 404.
		if strings.HasPrefix(cleanPath, pdfJSAssetPathPrefix) {
			http.NotFound(w, r)
			return
		}

		// File not found — serve index.html for SPA routing (no-cache)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return gzipHandler(spaHandler)
}

// HasSPA returns true if a SPA was embedded at build time.
func HasSPA() bool {
	if _, err := fs.Sub(spaFS, "spa"); err != nil {
		return false
	}
	return true
}

// gzipPool reuses gzip writers to reduce allocation pressure.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// gzipResponseWriter wraps http.ResponseWriter to transparently gzip the response.
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

// gzipHandler wraps an http.Handler to add gzip compression for compressible content types.
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only compress JS, CSS, HTML, JSON, SVG
		path := r.URL.Path
		compressible := strings.HasSuffix(path, ".js") ||
			strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".html") ||
			strings.HasSuffix(path, ".json") ||
			strings.HasSuffix(path, ".svg") ||
			path == "/" || path == ""

		if !compressible || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			gz.Close()
			gzipPool.Put(gz)
		}()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length") // length changes with compression
		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

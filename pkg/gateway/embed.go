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
// THIS STRING WAS SHIPPED UNMEASURED, AND IS NOW MEASURED — but not yet
// FROZEN. §10.7's freeze condition is a headed run in Chromium, Firefox AND
// Safari with zero violations while exercising initial load, the WebSocket, a
// Mermaid diagram, a highlighted code block and a PDF. What exists is
// tests/e2e/csp-assumptions.spec.ts, which drives every journey below against
// the real embedded build on CHROMIUM ONLY, with the browser's own
// securitypolicyviolation events AND the console as its two oracles, and a
// positive control (test A0) proving both channels fire. Engines genuinely
// differ on CSP, so Firefox and WebKit remain outstanding, and the audit report
// (docs/internal/architecture/csp-audit-2026-09-05.md) says so rather than
// claiming the freeze.
//
// Each assumption, with the symptom if wrong and the verdict when driven:
//
//	no inline bootstrap script          → white screen at boot.
//	                                      MEASURED 2026-09-05 (Chromium 151,
//	                                      test A1): HOLDS. The built index.html
//	                                      carries no inline script; the shell
//	                                      mounts with zero script-src violations,
//	                                      while A0 proves an inline script IS
//	                                      refused and IS reported under the same
//	                                      policy.
//	worker-src covers the built PDF.js
//	  worker URL                        → PDF.js does NOT fail; it falls back to
//	                                      parsing on the main thread, which
//	                                      FR-019c forbids, with a console warning
//	                                      as the only symptom (test 96 asserts the
//	                                      THREAD, not the configuration).
//	                                      MEASURED 2026-09-05 (test A2): HOLDS —
//	                                      a real PDF renders and Playwright's own
//	                                      CDP view of live workers contains
//	                                      /pdfjs/pdf.worker.min.mjs.
//	the worker realm can run what the
//	  build ships it                    → NOT ON THE ORIGINAL LIST, and the
//	                                      defect this audit found. MEASURED FALSE
//	                                      2026-09-05 (test A2b): the worker took
//	                                      `script-src 'self'` from its own
//	                                      response, so pdfjs/wasm/ was shipped and
//	                                      unusable and every ICC/DeviceCMYK colour
//	                                      was silently wrong. Fixed by
//	                                      spaPdfWorkerContentSecurityPolicy below
//	                                      — read its comment before touching
//	                                      either string.
//	Tailwind and Radix need inline
//	  styles                            → broken layout.
//	                                      MEASURED 2026-09-05 (test A3, plus a
//	                                      necessity probe that rewrote the header
//	                                      to `style-src 'self'` and re-ran the
//	                                      same journey): HOLDS, and the allowance
//	                                      is load-bearing — Settings alone then
//	                                      reported 13 style-src-attr violations,
//	                                      12 of them from DOMPurify re-applying
//	                                      sanitised style= attributes. Note the
//	                                      violations are all ATTRIBUTES: zero
//	                                      inline <style> elements exist, so
//	                                      narrowing to
//	                                      `style-src-elem 'self'` +
//	                                      `style-src-attr 'unsafe-inline'` is a
//	                                      real tightening opportunity, left
//	                                      undone because it needs its own sweep
//	                                      of every screen.
//	same-origin WebSocket matches 'self'→ the live connection silently fails.
//	                                      MEASURED 2026-09-05 (test A4): HOLDS —
//	                                      ws://<host>/api/v1/chat/ws both opens on
//	                                      its own at boot and reaches OPEN when
//	                                      constructed directly, with zero
//	                                      connect-src violations.
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
//	                                      adding 'wasm-unsafe-eval' here. That
//	                                      instruction is unchanged and still
//	                                      binding: the WebAssembly allowance the
//	                                      PDF.js defect earned is scoped to the
//	                                      PDF.js WORKER's response and never
//	                                      reaches this string, which is what keeps
//	                                      it enforceable. RE-VERIFIED 2026-09-05
//	                                      (test A5): a `js` code block renders its
//	                                      text with zero WebAssembly console
//	                                      hits.
//	nothing embeds the SPA              → any embedding surface goes blank.
//	                                      MEASURED 2026-09-05 (test A6): HOLDS,
//	                                      with a caveat worth reading. The
//	                                      control itself works — a same-origin
//	                                      page framing "/" is refused, console
//	                                      naming frame-ancestors 'none'. But the
//	                                      app's ONLY embedding surface, the
//	                                      Library's sandboxed HTML frame, does not
//	                                      mount in a production build at all:
//	                                      LibraryPreviewPane.tsx's
//	                                      PREVIEW_TOKEN_MINTER is null, so the
//	                                      pane renders "Preview unavailable" and
//	                                      the DOM contains zero iframes. So this
//	                                      assumption is true today for a reason
//	                                      unrelated to the policy. When that
//	                                      minter lands, its frame's src is the
//	                                      ISOLATED preview endpoint (§10.3's
//	                                      policy, a different handler) rather than
//	                                      anything this handler serves — extend
//	                                      A6 to prove that rather than assuming
//	                                      it.
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

// pdfJSWorkerPath is the ONE embedded file whose response carries
// spaPdfWorkerContentSecurityPolicy instead of spaContentSecurityPolicy.
//
// It must stay in step with vite.config.ts's PDFJS_WORKER_FILE and with
// LibraryPdfPreview.tsx's `new Worker(...)` URL.
// TestSpaCsp_PdfWorkerPathIsTheOneVitePublishes reads the TypeScript and
// compares, because a rename on either side would silently move the worker back
// under the document policy and restore the defect below with no other symptom.
const pdfJSWorkerPath = pdfJSAssetPathPrefix + "pdf.worker.min.mjs"

// spaPdfWorkerContentSecurityPolicy is spaContentSecurityPolicy with
// 'wasm-unsafe-eval' added to script-src, and it is served on exactly one
// path: the PDF.js parsing worker.
//
// WHY THIS EXISTS — MEASURED 2026-09-05, Chromium 151, against the real
// embedded build (tests/e2e/csp-assumptions.spec.ts, test A2b).
//
// A dedicated worker loaded from a same-origin URL takes its policy from ITS
// OWN response headers, not from the document that created it. The SPA handler
// serves the worker script, so the worker realm was running under
// `script-src 'self'` — and `script-src 'self'` refuses
// WebAssembly.instantiate. Read directly inside the worker's realm, the verdict
// was:
//
//	CompileError: WebAssembly.instantiate(): Compiling or instantiating
//	WebAssembly module violates the following Content Security policy
//	directive because 'unsafe-eval' is not an allowed source of script in the
//	following Content Security Policy directive: "script-src 'self'".
//
// That is a direct contradiction inside our own build. vite.config.ts SHIPS
// `pdfjs/wasm/` deliberately and FAILS the build if it is missing, because
// (its words) "wasm/ missing -> a scanned PDF (JPEG 2000 / JBIG2) loses images"
// and "iccs/ missing -> colour profiles are ignored". The policy then refused to
// let any of it compile. The assets were shipped and unusable.
//
// THE USER-VISIBLE SYMPTOM, and why it never surfaced. PDF.js 6.2.108 ships a
// JavaScript fallback for two of the three modules — `jbig2_nowasm_fallback.js`
// and `openjpeg_nowasm_fallback.js` — reached through a same-origin dynamic
// import that `script-src 'self'` permits, so scanned images still decoded, on
// the slow path, after a wasted fetch-compile-throw. `qcms_bg.wasm` has NO
// fallback file. `IccColorSpace.isUsable` compiles it, catches the CompileError,
// `warn()`s once, and MEMOISES false. From then on every `/ICCBased` colour
// space and every DeviceCMYK colour in every document silently used the crude
// device conversion instead of the profile. Nothing was blank and nothing
// errored — the colours were just wrong, in every released build, with one
// console warning as the only trace:
//
//	Warning: ICCBased color space: "CompileError: WebAssembly.Module(): …
//	violates … "script-src 'self'"."
//
// WHY THE LIBRARY WAS NOT RECONFIGURED INSTEAD. That is this file's standing
// rule and it was tried first. PDF.js exposes exactly one knob here,
// `useWasm: false`. Setting it makes `IccColorSpace.isUsable` return false
// WITHOUT attempting the compile — identical wrong colours, minus the one
// warning that made the defect findable at all. It hides the symptom rather
// than fixing the cause. qcms is shipped only as WebAssembly; there is no
// pure-JavaScript qcms to switch to, so the Shiki move (FR-019a's move, and
// markdown-shared.tsx's) has no analogue here.
//
// WHY THIS IS NOT A WIDENING OF THE DOCUMENT'S POLICY. Two separate narrowings:
//
//  1. SCOPE. Only the worker script's response carries this string. The SPA
//     shell, every chunk, every other asset and the ws-parser worker keep
//     spaContentSecurityPolicy unchanged, byte for byte — so the main thread
//     still cannot compile WebAssembly, and Shiki's pure-JavaScript engine stays
//     a requirement enforced by the policy rather than by convention. Do NOT
//     "simplify" this by moving 'wasm-unsafe-eval' onto the shell policy: that
//     silently re-permits the exact thing the Shiki entry above tells you not to
//     re-permit.
//  2. KEYWORD. 'wasm-unsafe-eval' is not 'unsafe-eval' and does not imply it.
//     It permits WebAssembly compilation and NOTHING else — `eval`,
//     `new Function` and string-to-code timers stay refused in the worker too.
//     The NON-NEGOTIABLE above is about 'unsafe-eval', and it is untouched;
//     spaCSPFloorViolations' substring check distinguishes the two, and
//     TestSpaCsp_WasmKeywordIsNotUnsafeEval pins that it does.
//
// RESIDUAL RISK, stated rather than waved past. The worker parses hostile PDFs
// and can now compile WebAssembly. The bytes it compiles come from three
// same-origin URLs this build publishes (`qcms_bg.wasm`, `jbig2.wasm`,
// `openjpeg.wasm`); a document cannot introduce its own module, because nothing
// in the parser compiles bytes taken from the PDF. A worker has no DOM, no
// cookie access and no ambient authority, and it already runs the full
// JavaScript decoders over the same input. The marginal capability is
// "compile three files we shipped".
var spaPdfWorkerContentSecurityPolicy = withWasmCompilation(spaContentSecurityPolicy)

// withWasmCompilation returns policy with 'wasm-unsafe-eval' added to its
// script-src.
//
// It PANICS rather than returning the input unchanged when the expected
// directive is absent. A strings.Replace that matches nothing is the silent
// failure this whole audit exists to stop: the gateway would boot, serve a
// worker policy identical to the document's, and A2b would be the only thing
// that noticed — at which point the panic is strictly better, because it is at
// init, on every platform, with the reason attached.
func withWasmCompilation(policy string) string {
	const from = "script-src 'self';"
	const to = "script-src 'self' 'wasm-unsafe-eval';"
	if !strings.Contains(policy, from) {
		panic("gateway: the SPA policy no longer contains " + from + " — the PDF.js worker's " +
			"WebAssembly allowance cannot be derived from it, and PDF.js's ICC/DeviceCMYK " +
			"colour handling would silently regress. Update withWasmCompilation together " +
			"with spaContentSecurityPolicy.")
	}
	return strings.Replace(policy, from, to, 1)
}

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

		// The PDF.js worker, and ONLY the PDF.js worker, is served with the
		// WebAssembly-capable variant. A worker's realm takes its policy from
		// its own response, so this is what governs PDF.js's qcms/jbig2/openjpeg
		// modules — see spaPdfWorkerContentSecurityPolicy for the measurement.
		// Still exactly one header: Set replaces, never appends.
		if cleanPath == pdfJSWorkerPath {
			w.Header().Set(headerContentSecurityPolicy, spaPdfWorkerContentSecurityPolicy)
		}
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

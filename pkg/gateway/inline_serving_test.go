// ADR-067 stage 1 — FR-008, FR-008b, FR-008c, FR-015a.
//
// Three properties, and each catches what the others cannot:
//
//	1. BEHAVIOUR (TestInlineServingRoutes_PolicyTypeAndDisposition). Every
//	   route that carries workspace bytes is driven with real fixtures and
//	   real handlers. Asserts the extension-derived Content-Type, nosniff, the
//	   disposition the §10.4 allow-list requires, and — on an inline response
//	   and only there — the §10.3 policy byte for byte.
//
//	2. SOURCE (TestNoUnprotectedInlineRoute). A handler that grows its own
//	   "Content-Disposition: inline" tomorrow is invisible to (1), because (1)
//	   only ever drives routes somebody remembered to add. So the package's own
//	   source is parsed: exactly one file may write that header, and the set of
//	   functions calling the shared helpers must match an inventory that is
//	   itself tied to (1)'s case list. §10.5's two-URL table was written from an
//	   incomplete enumeration and was wrong; this is the mechanism that stops
//	   the next enumeration going stale in the same way.
//
//	3. ORACLE (TestInlineIsolationPolicy_MatchesSpecDocument). The expected
//	   policy string is read out of the spec markdown at test time and compared
//	   with both the production constant and this file's own literal. A test
//	   whose expected value is copied from the implementation cannot fail when
//	   the implementation is wrong, and "the policy" is exactly the value where
//	   a single dropped directive is both invisible and total.
//
// The round-4 finding this file exists for is not a preview feature at all:
// /api/v1/media/workspace/{workspace}/{id} is registered withOptionalAuth and
// served Library-resolved bytes inline, as real HTML, on the gateway origin,
// with no policy — today, before any of ADR-067 shipped.

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// ---------------------------------------------------------------------------
// 0. The oracle: the §10.3 policy, typed from the spec, not from the code
// ---------------------------------------------------------------------------

// inlinePolicyTemplateFromSpec is ADR-067 §10.3's TEMPLATE, transcribed by
// hand from the specification. It is deliberately a single unbroken literal —
// the production template is assembled from concatenated fragments, so a
// transcription error in either one shows up as a mismatch rather than as two
// copies of the same mistake.
//
// AMENDED 2026-08-23. §10.3 stopped being a fixed literal: the six source
// directives now name the gateway's origin BESIDE `'self'`, because `'self'`
// matches nothing inside an FR-005b attribute-sandboxed WebKit iframe. The
// byte-for-byte contract did not weaken — it binds the SUBSTITUTED string, and
// the substitution rules are §10.3's own.
const inlinePolicyTemplateFromSpec = "sandbox allow-scripts; default-src 'none'; script-src 'self' ${GATEWAY_ORIGIN} 'unsafe-inline'; style-src 'self' ${GATEWAY_ORIGIN} 'unsafe-inline'; img-src 'self' ${GATEWAY_ORIGIN} data: blob:; font-src 'self' ${GATEWAY_ORIGIN}; media-src 'self' ${GATEWAY_ORIGIN}; frame-src 'self' ${GATEWAY_ORIGIN}; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"

// inlineServingWantPolicy is the header these routes must serve once the
// gateway origin is frozen to previewFixtureCanonicalOrigin: §10.3's template,
// read from the spec markdown at test time, with the loopback source list
// substituted by §10.3's rules (specIsolationPolicy, rest_library_preview_test.go).
//
// Read from the spec rather than from inlinePolicyTemplateFromSpec on purpose —
// two independent transcriptions of the same string cannot cover for each
// other, which is what makes TestInlineIsolationPolicy_MatchesSpecDocument
// worth having.
func inlineServingWantPolicy(t *testing.T) string {
	t.Helper()
	return specIsolationPolicy(t, previewFixtureSources)
}

// specDocRelPath is the specification itself, read at test time.
const specDocRelPath = "../../docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md"

// TestInlineIsolationPolicy_MatchesSpecDocument reads §10.3's fenced block out
// of the spec and compares it with the production constant AND with this
// file's transcription.
//
// Why bother, when a constant is right there: the policy is the entire P0
// control, and every one of its parts is load-bearing in a way that produces
// no symptom when it is missing. Drop `sandbox` and five of seven measured
// egress vectors reopen. Add `allow-same-origin` and the page can read the
// session cookie. Neither shows up as a broken page, a failed request, or a
// log line — the preview keeps working, and looks identical. An expected value
// copied from the implementation would agree with any of those edits.
//
// Traces to: MV-13, FR-005a, §10.3.
func TestInlineIsolationPolicy_MatchesSpecDocument(t *testing.T) {
	fromDoc := readSpecPolicyString(t)

	assert.Equal(t, fromDoc, libraryIsolationPolicyTemplate,
		"the production template is not byte-identical to ADR-067 §10.3.\n"+
			"This is the whole P0 control: a dropped directive here has no visible "+
			"symptom and reopens measured egress vectors.")
	assert.Equal(t, fromDoc, inlinePolicyTemplateFromSpec,
		"this test file's transcription of §10.3 has drifted from the spec")
	assert.Contains(t, fromDoc, libraryIsolationOriginPlaceholder,
		"§10.3 is a template: the production code substitutes %s, so a spec that has lost "+
			"its placeholder would silently pin the pre-amendment literal and Safari would "+
			"render blank previews again", libraryIsolationOriginPlaceholder)
}

// readSpecPolicyString extracts the single fenced code block that follows the
// "### 10.3" heading in the spec.
func readSpecPolicyString(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(specDocRelPath)
	require.NoError(t, err, "the ADR-067 spec must be readable from the package dir; "+
		"a skip here would turn the only independent oracle into a no-op")

	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "### 10.3") {
			start = i
			break
		}
	}
	require.Greater(t, start, -1, "spec section 10.3 not found in %s", specDocRelPath)

	inFence := false
	var body []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "```") {
			if inFence {
				break
			}
			inFence = true
			continue
		}
		if inFence {
			body = append(body, line)
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			break // ran past the section without finding a fence
		}
	}
	require.NotEmpty(t, body, "no fenced policy block under §10.3")
	got := strings.TrimSpace(strings.Join(body, ""))
	require.NotEmpty(t, got)
	return got
}

// ---------------------------------------------------------------------------
// 1. The inventory — one row per function that serves workspace bytes
// ---------------------------------------------------------------------------

// inlineServingSite names one function in pkg/gateway that hands file bytes to
// a client through the shared helpers in inline_serving.go.
//
// The inventory is not a hand-maintained allow-list that a future author can
// forget: the source scan below DERIVES the same set from the package's AST
// and fails when the two disagree, in either direction. Adding a route makes
// the scan find a site with no row here; deleting a row makes it find a row
// with no site. Both are failures, and the failure message says which.
type inlineServingSite struct {
	file string // file within pkg/gateway
	fn   string // enclosing function name

	// forcesAttachment is true for a route that must NEVER serve inline,
	// whatever the extension. FR-003g pins the authenticated Library path
	// there: inline serving belongs to the preview-token path, whose
	// credential is scoped to one file.
	forcesAttachment bool

	// policyOnEveryResponse is true for the ONE route where §10.3 is stricter
	// than MV-13's general rule: "Every response on the preview-token path MUST
	// carry exactly this Content-Security-Policy, byte for byte, WHATEVER THE
	// FILE'S TYPE" — so an ATTACHMENT response there carries it too, where an
	// attachment on the authenticated Library path carries none.
	//
	// Expressed as a field rather than a special case in the assertion so the
	// asymmetry is visible in the inventory. It is exactly the kind of thing a
	// later reader tidies into consistency.
	policyOnEveryResponse bool
}

var expectedInlineServingSites = []inlineServingSite{
	{file: "rest.go", fn: "HandleServeUpload"},
	{file: "rest.go", fn: "serveMedia"},
	{file: "rest_library.go", fn: "handleLibraryDownload", forcesAttachment: true},
	{file: "rest_library_preview.go", fn: "handleServeLibraryPreview", policyOnEveryResponse: true},
}

// libraryIsolationPolicyImplFile is where the §10.3 policy is BUILT, and the
// only file permitted to contain the string.
//
// It is a SECOND exemption, separate from inlineServingImplFile, and the two
// are not interchangeable: inline_serving.go is the only file that may WRITE a
// Content-Disposition header, and library_isolation_policy.go is the only file
// that may CONTAIN the policy template. Collapsing them into one constant
// would quietly let the policy be redefined in inline_serving.go, which is
// where it used to live and is therefore exactly where a merge would put it
// back.
const libraryIsolationPolicyImplFile = "library_isolation_policy.go"

// inlineServingImplFile is where the helpers are DEFINED. Its own calls are the
// implementation, not serving sites.
const inlineServingImplFile = "inline_serving.go"

// inlineServingHelpers are the entry points every byte-serving route must go
// through. A route that calls none of them is invisible to the behavioural
// test, which is exactly why the Content-Disposition literal scan below exists
// alongside this one.
var inlineServingHelpers = map[string]bool{
	"applyLibraryByteHeaders":        true,
	"applyLibraryPreviewByteHeaders": true,
	"serveLibraryContent":            true,
	"serveLibraryPath":               true,
}

// inlineServingByteWriters are the stdlib entry points that write file bytes to
// a client and, when no Content-Type is already set, ask the HOST MIME registry
// and then SNIFF — both of which FR-015/FR-015b forbid.
//
// A handler can serve a document inline WITHOUT ever naming Content-Disposition:
// omit the header entirely and every browser renders the response inline by
// default. Such a handler writes no disposition literal, calls no shared helper,
// and was therefore invisible to both of the other subtests — a probe of exactly
// that shape (http.ServeFile with no headers at all) passed this gate. Pinning
// the call sites closes it.
var inlineServingByteWriters = map[string]bool{
	"ServeFile":    true,
	"ServeContent": true,
	"FileServer":   true,
}

// expectedInlineServingByteWriterSites is every place in pkg/gateway that may
// call one of the above, as "file:function".
//
// Each row is here because the bytes it serves are typed BEFORE the call, or
// are not workspace bytes at all:
//
//	inline_serving.go:serveLibraryContent      the shared helper itself; it sets
//	                                           the type from the compiled-in
//	                                           table first, which is the one
//	                                           condition under which
//	                                           ServeContent cannot sniff.
//	rest_library_preview.go:handleServeLibraryPreview
//	                                           calls applyLibraryPreviewByteHeaders
//	                                           first, for the same reason, and
//	                                           needs ServeContent's Range support
//	                                           so bundle audio and video can seek.
//	embed.go:newSPAHandlerFor                  the EMBEDDED SPA's own asset tree.
//	                                           Not Library-resolved bytes, not
//	                                           operator- or agent-authored, and
//	                                           not covered by §10.4. (The byte
//	                                           writer lives in newSPAHandlerFor,
//	                                           the fs.FS-parameterised body that
//	                                           newSPAHandler now delegates to.)
var expectedInlineServingByteWriterSites = []string{
	"embed.go:newSPAHandlerFor",
	"inline_serving.go:serveLibraryContent",
	"rest_library_preview.go:handleServeLibraryPreview",
}

// inlineServingScannedDirs are parsed by the source gate.
var inlineServingScannedDirs = []string{".", "middleware"}

// ---------------------------------------------------------------------------
// 2. FR-008c — the source gate
// ---------------------------------------------------------------------------

// TestNoUnprotectedInlineRoute fails if any handler can serve bytes inline
// without going through the shared policy-and-type helper.
//
// Traces to: FR-008b, FR-008c, spec test 99.
func TestNoUnprotectedInlineRoute(t *testing.T) {
	dispositionSites, helperSites, byteWriterSites := scanInlineServingSites(t)

	t.Run("only_the_shared_helper_writes_a_content_disposition_header", func(t *testing.T) {
		assert.Empty(t, dispositionSites,
			"FR-008c: these places write a Content-Disposition header outside %s.\n"+
				"A handler that sets its own disposition sets its own (absent) policy and its own\n"+
				"(sniffed) type — the round-4 exposure exactly. Route the response through\n"+
				"serveLibraryPath / serveLibraryContent / applyLibraryByteHeaders instead:\n%s",
			inlineServingImplFile, strings.Join(dispositionSites, "\n"))
	})

	t.Run("the_isolation_policy_is_defined_exactly_once", func(t *testing.T) {
		copies := scanIsolationPolicyLiterals(t)
		assert.Empty(t, copies,
			"FR-008b/MV-13: the §10.3 policy template is duplicated outside %s.\n"+
				"Two copies drift, and the copy that drifts is the one nobody re-reads — a\n"+
				"dropped directive has no visible symptom, so the stale copy keeps serving\n"+
				"pages that look identical and are no longer contained. Use\n"+
				"libraryIsolationPolicy() (or applyLibraryPreviewByteHeaders, which carries it on\n"+
				"every response including attachments, as the token path requires):\n%s",
			libraryIsolationPolicyImplFile, strings.Join(copies, "\n"))
	})

	t.Run("only_inventoried_functions_call_a_stdlib_byte_writer", func(t *testing.T) {
		sort.Strings(byteWriterSites)
		want := append([]string(nil), expectedInlineServingByteWriterSites...)
		sort.Strings(want)

		assert.Equal(t, want, byteWriterSites,
			"the set of functions calling http.ServeFile / http.ServeContent / http.FileServer changed.\n"+
				"A handler can serve a document INLINE without ever naming Content-Disposition —\n"+
				"omit the header and every browser renders the response inline by default — so the\n"+
				"disposition scan above cannot see it. Called with no Content-Type already set,\n"+
				"these functions then ask the HOST MIME registry and SNIFF the first 512 bytes,\n"+
				"which is what FR-015 and FR-015b forbid: the same binary answers differently on\n"+
				"different machines, and the test written on the developer's Mac passes while the\n"+
				"shipped container is broken.\n"+
				"Route the response through serveLibraryPath / serveLibraryContent instead, or —\n"+
				"if the bytes genuinely are not Library-resolved — add the site to\n"+
				"expectedInlineServingByteWriterSites with the reason.")
	})

	t.Run("byte_serving_sites_are_exactly_the_inventory", func(t *testing.T) {
		want := make([]string, 0, len(expectedInlineServingSites))
		for _, site := range expectedInlineServingSites {
			want = append(want, site.file+":"+site.fn)
		}
		sort.Strings(want)
		sort.Strings(helperSites)

		assert.Equal(t, want, helperSites,
			"the set of functions serving workspace bytes changed.\n"+
				"ADDED a route? Add it to expectedInlineServingSites AND give it a case in\n"+
				"inlineServingRouteCases — the next subtest fails until you do, which is the\n"+
				"point: an enumeration nobody re-derives is how §10.5's two-URL table came to\n"+
				"be wrong.\n"+
				"A row DISAPPEARED? A route stopped going through the shared helper and is now\n"+
				"serving bytes with no policy and a sniffed type.")
	})

	t.Run("every_inventoried_site_is_driven_by_a_behavioural_case", func(t *testing.T) {
		covered := map[string]bool{}
		for _, c := range inlineServingRouteCases() {
			covered[c.site.file+":"+c.site.fn] = true
		}
		for _, site := range expectedInlineServingSites {
			key := site.file + ":" + site.fn
			assert.True(t, covered[key],
				"%s is inventoried as a byte-serving route but no behavioural case drives it, "+
					"so nothing asserts its policy, type or disposition", key)
		}
		assert.Len(t, inlineServingRouteCases(), len(expectedInlineServingSites),
			"a behavioural case exists for a route that is not in the inventory")
	})
}

// scanInlineServingSites parses pkg/gateway (and middleware) and returns
//
//	dispositionSites — positions of every Content-Disposition header literal
//	                   written outside inline_serving.go;
//	helperSites      — "file:function" for every function calling a shared
//	                   helper.
//
// The scan is AST-based rather than a grep for two reasons that both bite in
// this package: several files DISCUSS Content-Disposition in doc comments
// (library_mime.go, rest_library.go) and a grep flags all of them, and
// rest_library.go's contentDispositionAttachment builds the header VALUE
// without ever naming the header, which a grep for the value would miss.
func scanInlineServingSites(t *testing.T) (dispositionSites, helperSites, byteWriterSites []string) {
	t.Helper()
	fset := token.NewFileSet()

	for _, dir := range inlineServingScannedDirs {
		for _, file := range inlineServingScanGoFiles(t, dir) {
			rel := strings.TrimPrefix(filepath.Clean(file), "./")
			isImpl := filepath.Base(rel) == inlineServingImplFile

			parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			require.NoError(t, err)

			for _, decl := range parsed.Decls {
				fn, _ := decl.(*ast.FuncDecl)
				ast.Inspect(decl, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if !isImpl && isContentDispositionLiteral(lit.Value) {
							dispositionSites = append(dispositionSites,
								fmt.Sprintf("%s: %s", fset.Position(lit.Pos()), lit.Value))
						}
						return true
					}
					// The header-name CONSTANT is the same thing as the literal
					// for this gate's purposes, and it is the bypass a scan for
					// the literal alone leaves wide open: inline_serving.go
					// introduces headerContentDisposition precisely so the
					// literal appears once, and every other file in the package
					// can then name the header without ever writing the string.
					// A probe doing exactly that passed the literal-only scan.
					if ident, ok := n.(*ast.Ident); ok && !isImpl &&
						ident.Name == "headerContentDisposition" {
						dispositionSites = append(dispositionSites,
							fmt.Sprintf("%s: headerContentDisposition", fset.Position(ident.Pos())))
						return true
					}
					call, ok := n.(*ast.CallExpr)
					if !ok || fn == nil {
						return true
					}
					if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel {
						if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == "http" &&
							inlineServingByteWriters[sel.Sel.Name] {
							key := rel + ":" + fn.Name.Name
							if !containsString(byteWriterSites, key) {
								byteWriterSites = append(byteWriterSites, key)
							}
						}
					}
					if isImpl {
						return true
					}
					if ident, isIdent := call.Fun.(*ast.Ident); isIdent && inlineServingHelpers[ident.Name] {
						key := rel + ":" + fn.Name.Name
						if !containsString(helperSites, key) {
							helperSites = append(helperSites, key)
						}
					}
					return true
				})
			}
		}
	}
	return dispositionSites, helperSites, byteWriterSites
}

// isContentDispositionLiteral reports whether a Go string literal names the
// Content-Disposition header. Matching the header NAME (not the value) is what
// makes the gate independent of how the value is spelled — "inline",
// fmt.Sprintf("inline; filename=%q", …) and a helper's return value are all
// caught by the one Set call that has to name the header.
func isContentDispositionLiteral(lit string) bool {
	return strings.Contains(strings.ToLower(lit), "content-disposition")
}

// scanIsolationPolicyLiterals returns the positions of every string constant
// that IS the §10.3 policy, outside inline_serving.go. A second definition is
// not a style problem: MV-13 asserts one byte-identical value, and the only way
// to keep that true across two literals is for a human to remember both.
//
// CONCATENATION IS FOLDED FIRST, and that is the whole difficulty. Go does not
// fold adjacent string literals in the AST, so a duplicate written as
//
//	"sandbox allow-" + "scripts; default-src 'none'; " + …
//
// contains no single literal holding the marker and was invisible to the
// earlier version of this scan — a byte-exact second copy of the policy passed
// it. Worse, the production constant is ITSELF a five-fragment concatenation,
// so that spelling is the house style a future author would copy.
func scanIsolationPolicyLiterals(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var found []string

	for _, dir := range inlineServingScannedDirs {
		for _, file := range inlineServingScanGoFiles(t, dir) {
			if filepath.Base(file) == libraryIsolationPolicyImplFile {
				continue // the definition
			}
			parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			require.NoError(t, err)
			ast.Inspect(parsed, func(n ast.Node) bool {
				expr, ok := n.(ast.Expr)
				if !ok {
					return true
				}
				value, isConst := inlineServingConstStringValue(expr)
				if !isConst {
					return true
				}
				if strings.Contains(value, "sandbox allow-scripts") {
					found = append(found, fset.Position(expr.Pos()).String())
				}
				// Pruned either way: the fragments of a folded expression are
				// not separate copies, and reporting each of them would turn one
				// duplicate into five unactionable positions.
				return false
			})
		}
	}
	return found
}

// inlineServingConstStringValue folds a compile-time string constant
// expression — a literal, or any tree of `+` over such expressions — into its
// value. Anything else (a variable, a function call, a numeric literal) reports
// ok=false.
func inlineServingConstStringValue(e ast.Expr) (string, bool) {
	switch node := e.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(node.Value)
		if err != nil {
			return "", false
		}
		return unquoted, true
	case *ast.ParenExpr:
		return inlineServingConstStringValue(node.X)
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, lok := inlineServingConstStringValue(node.X)
		right, rok := inlineServingConstStringValue(node.Y)
		if !lok || !rok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func inlineServingScanGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	require.NotEmpty(t, out, "no Go files found in %s — the scan would pass vacuously", dir)
	return out
}

// ---------------------------------------------------------------------------
// 3. FR-008 / FR-008b / FR-015a — the behaviour, through the real handlers
// ---------------------------------------------------------------------------

// inlineServingFixture is one file, and what §10.4 and the type table say the
// response for it must be. Expected values come from the spec's tables, never
// from running the code.
type inlineServingFixture struct {
	name        string
	body        []byte
	wantType    string // §10.4 + FR-015c type table
	allowListed bool   // on the §10.4 inline allow-list
	why         string
}

// scriptedHTML is a document that would run script and read the session cookie
// if a browser ever treated it as one. It doubles as the no-sniff oracle: under
// an unknown extension it must NOT come back as text/html.
var scriptedHTML = []byte(
	"<!doctype html><html><body><script>document.title=document.cookie</script></body></html>")

func inlineServingFixtures() []inlineServingFixture {
	return []inlineServingFixture{
		{
			name: "report.html", body: scriptedHTML,
			wantType: "text/html; charset=utf-8", allowListed: true,
			why: "§10.4 active documents — the class the policy exists for",
		},
		{
			name: "logo.svg", body: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`),
			wantType: "image/svg+xml", allowListed: true,
			why: "§10.4: .svg is scriptable and inline anyway, because the policy covers every byte",
		},
		{
			name: "clip.aac", body: []byte("\xff\xf1fake-aac"),
			wantType: "audio/aac", allowListed: true,
			why: "FR-015b: .aac is absent from Go's built-in table, so a host-registry answer differs per machine",
		},
		{
			name: "doc.pdf", body: []byte("%PDF-1.7\n"),
			wantType: "application/pdf", allowListed: false,
			why: "§10.4: .pdf is typed but deliberately NOT inline (FR-008, FR-018)",
		},
		{
			name: "payload.dat", body: scriptedHTML,
			wantType: "application/octet-stream", allowListed: false,
			why: "MV-24: HTML bytes under an unknown extension. A sniffing implementation returns text/html",
		},
	}
}

// inlineServingRouteCase drives one real handler end to end.
type inlineServingRouteCase struct {
	site  inlineServingSite
	label string
	serve func(t *testing.T, filename string, body []byte) *httptest.ResponseRecorder
}

func inlineServingRouteCases() []inlineServingRouteCase {
	return []inlineServingRouteCase{
		{
			site:  expectedInlineServingSites[0],
			label: "GET /api/v1/uploads/{session}/{filename}",
			serve: serveViaUploadsRoute,
		},
		{
			site:  expectedInlineServingSites[1],
			label: "GET /api/v1/media/workspace/{workspace}/{id}",
			serve: serveViaWorkspaceMediaRoute,
		},
		{
			site:  expectedInlineServingSites[2],
			label: "GET /api/v1/library/{workspace}/download?path=",
			serve: serveViaLibraryDownloadRoute,
		},
		{
			site:  expectedInlineServingSites[3],
			label: "GET /library-preview/{token}/{path}",
			serve: serveViaLibraryPreviewRoute,
		},
	}
}

// TestInlineServingRoutes_PolicyTypeAndDisposition drives every inventoried
// route with every fixture and asserts the full header contract.
//
// Traces to: FR-008 (non-allow-listed types stay attachments), FR-008b (every
// inline response carries the §10.3 policy, the extension-derived type and
// nosniff), FR-015/FR-015a/FR-015b (the type comes from the compiled-in table,
// never the host registry and never the bytes), MV-13, MV-24, spec test 99.
func TestInlineServingRoutes_PolicyTypeAndDisposition(t *testing.T) {
	// §10.3 is now substituted from the frozen gateway origin, so the expected
	// header is only defined once that origin is known. Freezing it here — and
	// restoring it afterwards — is what stops this test measuring whichever
	// origin some earlier test happened to leave behind.
	freezeLibraryIsolationPolicyForTest(t, previewFixtureCanonicalOrigin)
	wantPolicy := inlineServingWantPolicy(t)

	for _, tc := range inlineServingRouteCases() {
		for _, fx := range inlineServingFixtures() {
			t.Run(tc.site.fn+"/"+fx.name, func(t *testing.T) {
				rec := tc.serve(t, fx.name, fx.body)

				// A non-200 would make every header assertion below vacuous:
				// a 404 carries no disposition, so "not inline" would pass
				// having served nothing at all.
				require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
				require.Equal(t, string(fx.body), rec.Body.String(),
					"the route must have served the real bytes")

				h := rec.Header()
				assert.Equal(t, fx.wantType, h.Get("Content-Type"),
					"%s: %s", tc.label, fx.why)
				assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"),
					"FR-015: nosniff on every response")

				wantInline := fx.allowListed && !tc.site.forcesAttachment
				disposition := h.Get("Content-Disposition")
				policy := h.Values("Content-Security-Policy")

				if wantInline {
					// The filename is KEPT on inline responses. A bare "inline"
					// is not a hardening measure — the type comes from the
					// extension table plus nosniff, never from the name — and
					// dropping it changes what the browser offers when the
					// reader saves from an inline view. serveMedia sent
					// `inline; filename="…"` before this helper existed, and
					// tests/integration/media_store_swap_test.go asserts it
					// still does.
					assert.Equal(t, `inline; filename="`+fx.name+`"`, disposition,
						"§10.4 lists %q as inline, and the filename survives", filepath.Ext(fx.name))
					require.Len(t, policy, 1,
						"FR-008b: an inline response carries exactly one Content-Security-Policy")
					assert.Equal(t, wantPolicy, policy[0],
						"MV-13: the policy must be byte-identical to §10.3's substituted string. "+
							"A policy that merely exists is satisfied by default-src *")
					return
				}

				why := fmt.Sprintf("FR-008: %q is not on the §10.4 allow-list", filepath.Ext(fx.name))
				if tc.site.forcesAttachment {
					why = "FR-003g: the authenticated Library path serves attachments whatever the type"
				}
				assert.True(t, strings.HasPrefix(disposition, "attachment;"),
					"%s, so it stays an attachment; got %q", why, disposition)

				if tc.site.policyOnEveryResponse {
					require.Len(t, policy, 1,
						"§10.3: every response on the preview-token path carries the policy, "+
							"WHATEVER the file's type — an attachment there is not an exception")
					assert.Equal(t, wantPolicy, policy[0])
					return
				}
				assert.Empty(t, policy,
					"MV-13: an attachment response on the authenticated Library path carries no policy")
			})
		}
	}
}

// TestWorkspaceMediaRoute_HtmlNoLongerRendersOnTheGatewayOrigin pins the
// round-4 finding by name, separately from the table above.
//
// Before this change /api/v1/media/workspace/{workspace}/{id} — registered
// withOptionalAuth — resolved a workspace-library entry, took the type the
// library's own map gave it (.html -> text/html), set a bare
// "Content-Disposition: inline" and handed the file to http.ServeFile. An
// agent-written .html in a workspace media library was therefore a live
// document on the gateway origin, same-origin with the session cookie.
//
// The table test would catch a regression here too; this one exists so the
// failure names the exposure rather than a header.
//
// Traces to: FR-008b, US-2 AS-1.
func TestWorkspaceMediaRoute_HtmlNoLongerRendersOnTheGatewayOrigin(t *testing.T) {
	freezeLibraryIsolationPolicyForTest(t, previewFixtureCanonicalOrigin)
	rec := serveViaWorkspaceMediaRoute(t, "agent-report.html", scriptedHTML)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, string(scriptedHTML), rec.Body.String())

	assert.Equal(t, inlineServingWantPolicy(t), rec.Header().Get("Content-Security-Policy"),
		"an HTML entry in a workspace media library is served inline on the gateway origin; "+
			"without the §10.3 policy it has the session cookie and the whole authenticated API")
	assert.NotContains(t, rec.Header().Get("Content-Security-Policy"), "allow-same-origin",
		"granting allow-same-origin beside allow-scripts hands the page the session cookie")
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestApplyLibraryPreviewByteHeaders_PolicyOnEveryResponse covers the one
// place where the token path's rule differs from the authenticated path's.
//
// §10.3: "Every response on the preview-token path MUST carry exactly this
// Content-Security-Policy, byte for byte, WHATEVER THE FILE'S TYPE." So on that
// path an attachment carries it too — the opposite of MV-13's rule for the
// authenticated Library path, which carries none. The distinction is worth a
// test because it is the kind of asymmetry a later reader "tidies away".
//
// Traces to: §10.3, FR-008, FR-008b, MV-13.
func TestApplyLibraryPreviewByteHeaders_PolicyOnEveryResponse(t *testing.T) {
	freezeLibraryIsolationPolicyForTest(t, previewFixtureCanonicalOrigin)
	wantPolicy := inlineServingWantPolicy(t)

	t.Run("inline_type_still_inline_and_contained", func(t *testing.T) {
		rec := httptest.NewRecorder()
		got := applyLibraryPreviewByteHeaders(rec, "index.html")

		assert.Equal(t, gen.LibraryInlineDispositionDispositionInline, got)
		assert.Equal(t, `inline; filename="index.html"`, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, wantPolicy, rec.Header().Get("Content-Security-Policy"))
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})

	t.Run("attachment_type_still_carries_the_policy", func(t *testing.T) {
		rec := httptest.NewRecorder()
		got := applyLibraryPreviewByteHeaders(rec, "manual.pdf")

		assert.Equal(t, gen.LibraryInlineDispositionDispositionAttachment, got,
			"FR-008: .pdf is typed but not on the §10.4 allow-list")
		assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;"))
		assert.Equal(t, wantPolicy, rec.Header().Get("Content-Security-Policy"),
			"§10.3 applies to every response on the token path, whatever the file's type")
	})
}

// ---------------------------------------------------------------------------
// 4. Route drivers — real handlers, real fixtures, no mux stubs
// ---------------------------------------------------------------------------

func serveViaUploadsRoute(t *testing.T, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	api.homePath = t.TempDir()

	sessionID := "01JXINLINESERVINGSESSION1"
	dir := filepath.Join(api.homePath, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), body, 0o600))

	target := "/api/v1/uploads/" + sessionID + "/" + filename
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.URL.Path = target
	rec := httptest.NewRecorder()
	api.HandleServeUpload(rec, req)
	return rec
}

func serveViaWorkspaceMediaRoute(t *testing.T, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)

	workspaceID := "ws-inline-serving"
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	ref, _, err := lib.Upload(filename, gen.UserUpload, bytes.NewReader(body))
	require.NoError(t, err)
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		require.Equal(t, workspaceID, id)
		return lib, nil
	})
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)

	target := "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.URL.Path = target
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)
	return rec
}

// serveViaLibraryPreviewRoute mints a real file-scope token through the real
// mint handler and fetches the file through the real serving handler.
//
// The token is minted rather than fabricated because "a token never widens
// access" (FR-003b) is enforced at mint time, so a fabricated grant would
// exercise a path production never takes.
func serveViaLibraryPreviewRoute(t *testing.T, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	api, workspaceID := buildLibraryTestAPI(t)
	routes := newLibraryPreviewRoutes(api)

	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), body, 0o600))

	mintBody, err := json.Marshal(map[string]any{
		"workspace_id": workspaceID, "path": filename, "scope": "file",
	})
	require.NoError(t, err)
	mintReq := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, bytes.NewReader(mintBody))
	mintReq.AddCookie(&http.Cookie{
		Name: middleware.SessionCookieName, Value: "inline-serving-session"})
	mintRec := httptest.NewRecorder()
	routes.handleMintPreviewToken(mintRec, mintReq)
	require.Equal(t, http.StatusCreated, mintRec.Code, mintRec.Body.String())

	var minted gen.LibraryPreviewTokenResponse
	require.NoError(t, json.Unmarshal(mintRec.Body.Bytes(), &minted))

	rec := httptest.NewRecorder()
	routes.handleServeLibraryPreview(rec, httptest.NewRequest(http.MethodGet, minted.Url, nil))
	return rec
}

func serveViaLibraryDownloadRoute(t *testing.T, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	api, workspaceID := buildLibraryTestAPI(t)

	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), body, 0o600))

	return libGet(t, api,
		"/api/v1/library/"+workspaceID+"/download?path="+url.QueryEscape(filename))
}

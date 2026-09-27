// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

// T29 — ADR-067 US-11.AC1 / SC-009 / FR-011 / FR-030, the source-shape half of
// the greenfield exit proof.
//
// ADR-067 deleted provider identity mapping outright: there is no alias table,
// no `_migrated` marker, no deprecation shim and no rename ladder. A stored id
// is either a catalog id or an operator-named custom row; anything else is an
// unknown provider, named and unhinted (FR-011, FR-015).
//
// That is a property of the SOURCE, and a behavioural test cannot see it: a
// binary that carries a dormant alias map and a binary that carries none behave
// identically until somebody wires the map up. So this asserts the shape.
//
// Why an AST scan and not the literal `grep -rnE '_migrated|alias|deprecat|
// retired' pkg/providers pkg/config` of SC-009: a grep cannot tell an
// identifier from a comment, and both packages document at length WHICH
// mechanisms were retired — erasing that prose to satisfy a regex would delete
// the explanation of the very decision this test enforces. The literal grep
// form of SC-009 lives in scripts/check-greenfield-providers.sh, which strips
// comments with a real Go string/comment lexer before matching; the two are
// deliberately complementary, and this one is the stricter of the pair
// (case-INsensitive, so `Alias`/`Deprecated`/`Retired` are caught too).
//
// Scope is non-test files only: `_test.go` cannot put machinery in the binary,
// and the spec's own carve-out ("the catalog's own `status: retired` value …
// and its tests", A-3) treats tests as the softer surface. Test-file prose is
// still covered by the shell gate's own rules.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// greenfieldAliasRe is SC-009's regex, widened to case-insensitive.
var greenfieldAliasRe = regexp.MustCompile(`(?i)_migrated|alias|deprecat|retired`)

// catalogAllowedIdents are the two identifiers ADR-067 A-3 / FR-030 keep on
// purpose, and ONLY inside pkg/providers/catalog:
//
//   - Aliases — the search-only `aliases[]` field ADR-068's picker greps over.
//     It is carried and served; it never participates in resolution (FR-030).
//     TestGreenfield_AliasesAreCarriedNotResolved below pins that separately.
//   - StatusRetired — the model `status` enum value `retired` (A-3). A retired
//     model still resolves and still constructs (T20b / E8); the token names a
//     registry fact, not an Omnipus mapping.
var catalogAllowedIdents = map[string]string{
	"Aliases":       "the search-only aliases[] field (FR-030); never resolved",
	"StatusRetired": "the catalog status enum value `retired` (A-3)",
}

// catalogAllowedLiteralTokens are the substrings a string literal in
// pkg/providers/catalog may owe its match to. A literal is allowed only when
// removing every one of these leaves nothing the regex still matches — so
// `json:"aliases"` and `"%q is not one of active|retired"` pass while
// `"z-ai"->"zai"` in the same package would not.
var catalogAllowedLiteralTokens = []string{"aliases", "retired"}

// aliasesReadSites are the ONLY non-test files allowed to name the catalog
// `Aliases` field, one per legitimate lifecycle stage. FR-030 is not "aliases
// are unused" — ADR-068's picker searches them — it is "aliases never resolve".
// Pinning the read sites is what makes a resolution path impossible to add
// quietly: a new file touching Aliases fails here even if it looks harmless.
var aliasesReadSites = map[string]string{
	"catalog/document.go": "declare the field on the domain Provider struct",
	"catalog/parse.go":    "populate from the document DTO",
	"catalog/served.go":   "serialise into GET /providers/catalog",
	"catalog/catalog.go":  "copy into the caller-owned provider snapshot",
}

// rolesMarkerToken is the ONE non-provider-identity migration token ADR-067
// tolerates, and only in the two files below: ADR-096 US-3 requires the
// web-search roles migration to stamp a `tools.web.roles_migrated_at` marker
// on disk, so that upgrading an install does not change who answers. That is
// role-assignment initialisation, not provider-identity translation — the
// thing this guard was written against (founder ruling 2026-09-27: keep the
// migration, exempt the marker narrowly).
const rolesMarkerToken = "roles_migrated_at"

// rolesMarkerTokenRe strips every case-insensitive, WHOLE-WORD occurrence of
// the marker from a literal, so onlyRolesMarkerToken can require that NOTHING
// ELSE in the literal matches the greenfield regex. The \b anchors matter:
// `_` is a word character, so a longer marker that merely contains this one
// (old_roles_migrated_at, roles_migrated_at_shim) is not stripped and stays
// banned.
var rolesMarkerTokenRe = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(rolesMarkerToken) + `\b`)

// rolesMarkerExemptSites are the ONLY files allowed to carry the marker in a
// string literal, keyed by greenfieldSiteKey — one row per sanctioned use,
// the same allow-list discipline as aliasesReadSites: a row that outlives its
// entry fails TestGreenfield_RolesMarkerExemptSitesStayHonest, so the list
// stays exactly as wide as ADR-096 needs. A DIFFERENT `_migrated` token, an
// extra banned token, or the marker in any other file (including a same-named
// file in another tree) still trips the guard — see
// TestGreenfield_RolesMarkerExemptionIsNarrow. Identifiers are never exempted:
// the marker is a wire/disk string, and both sanctioned sites are literals.
var rolesMarkerExemptSites = map[string]string{
	"config/config.go":           "ADR-096 D11: the tools.web.roles_migrated_at struct tag on the persisted config struct",
	"config/web_search_roles.go": "ADR-096 US-3: read and write the roles_migrated_at marker in the on-disk tools.web section",
}

// greenfieldRoots are the two package trees US-11.AC1 names, as paths relative
// to pkg/providers (this test's own directory).
var greenfieldRoots = []string{".", "../config"}

type greenfieldHit struct {
	pos  string
	kind string
	text string
}

func TestGreenfield_NoAliasMachinery(t *testing.T) {
	hits := collectGreenfieldHits(t, greenfieldRoots)

	if len(hits) == 0 {
		return
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	for _, h := range hits {
		t.Errorf("%s: %s %s — ADR-067 US-11.AC1: pkg/providers and pkg/config carry no alias, "+
			"migration or deprecation machinery. A stored provider id is a catalog id or a "+
			"custom row; anything else is ErrUnknownProvider with no hint (FR-011, FR-015). "+
			"The only tokens kept are `aliases[]` and the `retired` status value, and only "+
			"inside pkg/providers/catalog (A-3, FR-030), plus ADR-096's `roles_migrated_at` "+
			"disk marker in exactly its two exempt pkg/config files.", h.pos, h.kind, h.text)
	}
}

// collectGreenfieldHits runs the US-11.AC1 scan over the given trees and returns
// every hit. Extracted from TestGreenfield_NoAliasMachinery (logic unchanged) so
// the ADR-096 exemption can be proven end to end on a throwaway tree.
func collectGreenfieldHits(t *testing.T, roots []string) []greenfieldHit {
	t.Helper()
	var hits []greenfieldHit

	for _, root := range roots {
		walkGreenfieldGoFiles(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
			// The encoding/json embed idiom — `type Alias Config` declared
			// INSIDE a Marshal/UnmarshalJSON body to shed the method set — is
			// not provider machinery and never was. It is exempted
			// structurally, not by name: the exemption exists only in a file
			// that actually declares such a function-local type, and only for
			// the bare identifier `Alias`.
			jsonIdiom := declaresFuncLocalJSONAliasType(file)

			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.Ident:
					if !greenfieldAliasRe.MatchString(v.Name) {
						return true
					}
					if _, ok := catalogAllowedIdents[v.Name]; ok && isCatalogFile(root, rel) {
						return true
					}
					if v.Name == "Alias" && jsonIdiom {
						return true
					}
					hits = append(hits, greenfieldHit{
						pos:  rel + ":" + posLine(fset, v.Pos()),
						kind: "identifier",
						text: v.Name,
					})
				case *ast.BasicLit:
					if v.Kind != token.STRING || !greenfieldAliasRe.MatchString(v.Value) {
						return true
					}
					if isCatalogFile(root, rel) && onlyCatalogTokens(v.Value) {
						return true
					}
					if rolesMarkerExempt(root, rel, v.Value) {
						return true
					}
					hits = append(hits, greenfieldHit{
						pos:  rel + ":" + posLine(fset, v.Pos()),
						kind: "string literal",
						text: v.Value,
					})
				}
				return true
			})
		})
	}
	return hits
}

// TestGreenfield_RolesMarkerExemptionEndToEnd proves the ADR-096 exemption
// (ADR-067 §8c) through the real scan, not only its predicate: markers are
// planted in a throwaway tree named "config" — the same site key the real
// pkg/config tree produces — and the guard must still flag every NEW, unrelated
// migration marker while passing the one sanctioned marker in its exempt file.
func TestGreenfield_RolesMarkerExemptionEndToEnd(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		literal  string
		wantHits int
	}{
		{"sanctioned marker in its exempt file passes", "web_search_roles.go", "roles_migrated_at", 0},
		{"NEW unrelated marker in the exempt file is flagged", "web_search_roles.go", "provider_migrated_at", 1},
		{"sanctioned marker in a non-exempt pkg/config file is flagged", "providerstate.go", "roles_migrated_at", 1},
		{"sanctioned marker beside another banned token is flagged", "web_search_roles.go", "roles_migrated_at deprecated", 1},
		{"a longer marker that merely contains the sanctioned one (prefix) is flagged", "web_search_roles.go", "old_roles_migrated_at", 1},
		{"a longer marker that merely contains the sanctioned one (suffix) is flagged", "web_search_roles.go", "roles_migrated_at_shim", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "config")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			src := "package config\n\nvar k = " + strconv.Quote(tc.literal) + "\n"
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			hits := collectGreenfieldHits(t, []string{root})
			if len(hits) != tc.wantHits {
				t.Fatalf("%s: literal %q in config/%s -> %d hits %v, want %d",
					tc.name, tc.literal, tc.file, len(hits), hits, tc.wantHits)
			}
		})
	}
}

// TestGreenfield_AliasesAreCarriedNotResolved is the FR-030 half: `aliases[]`
// exists for ADR-068's picker search box and for nothing else. It is populated
// by the parser, copied into the snapshot, and serialised — three sites, all in
// the catalog package. Resolution (catalog/resolve.go), construction
// (factory_provider.go) and validation (validate.go) must never see it, or
// `z-ai` starts working again by the back door and FR-011 is dead.
func TestGreenfield_AliasesAreCarriedNotResolved(t *testing.T) {
	found := map[string]bool{}

	for _, root := range greenfieldRoots {
		walkGreenfieldGoFiles(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || id.Name != "Aliases" {
					return true
				}
				key := greenfieldSiteKey(root, rel)
				if _, allowed := aliasesReadSites[key]; !allowed {
					t.Errorf("%s:%s names the catalog Aliases field. FR-030: aliases[] is "+
						"SEARCH-ONLY — it must never participate in resolution, validation or "+
						"construction. If this is a new legitimate lifecycle stage, add it to "+
						"aliasesReadSites with the reason; if it resolves an id, delete it.",
						rel, posLine(fset, id.Pos()))
					return true
				}
				found[key] = true
				return true
			})
		})
	}

	// The allow-list must not outlive its entries: a stale row would silently
	// widen the guard the next time somebody adds a file with that name.
	for site, why := range aliasesReadSites {
		if !found[site] {
			t.Errorf("aliasesReadSites lists %s (%s) but that file no longer names Aliases — "+
				"remove the row so the allow-list stays exactly as wide as the code needs", site, why)
		}
	}
}

// TestGreenfield_RolesMarkerExemptionIsNarrow pins the ADR-096 exemption at
// its exact sanctioned width, per the founder ruling of 2026-09-27: the token
// `roles_migrated_at`, as a string literal, in exactly config/config.go and
// config/web_search_roles.go of pkg/config — and nothing else. Every `want`
// below derives from that ruling, not from the predicate's behaviour. A
// mutation that widens the exemption (whole-file, any file, basename-only
// keying, token-family matching) must fail at least one case here.
func TestGreenfield_RolesMarkerExemptionIsNarrow(t *testing.T) {
	cases := []struct {
		name string
		root string
		rel  string
		lit  string
		want bool
	}{
		{
			name: "sanctioned struct tag in config/config.go",
			root: "../config",
			rel:  "config.go",
			lit:  "`json:\"roles_migrated_at\" yaml:\"-\" env:\"-\"`",
			want: true,
		},
		{
			name: "sanctioned marker literal in config/web_search_roles.go",
			root: "../config",
			rel:  "web_search_roles.go",
			lit:  `"roles_migrated_at"`,
			want: true,
		},
		{
			name: "marker literal in another pkg/config file is still machinery",
			root: "../config",
			rel:  "validate.go",
			lit:  `"roles_migrated_at"`,
			want: false,
		},
		{
			name: "same file name in the pkg/providers tree is still machinery",
			root: ".",
			rel:  "config.go",
			lit:  `"roles_migrated_at"`,
			want: false,
		},
		{
			name: "exempt basename in a pkg/config subdirectory is still machinery",
			root: "../config",
			rel:  "nested/config.go",
			lit:  `"roles_migrated_at"`,
			want: false,
		},
		{
			name: "a different _migrated token in an exempt file is still machinery",
			root: "../config",
			rel:  "config.go",
			lit:  `"provider_migrated"`,
			want: false,
		},
		{
			name: "the marker beside another banned token is still machinery",
			root: "../config",
			rel:  "web_search_roles.go",
			lit:  `"roles_migrated_at or deprecated shim"`,
			want: false,
		},
	}

	for _, tc := range cases {
		if got := rolesMarkerExempt(tc.root, tc.rel, tc.lit); got != tc.want {
			t.Errorf("%s: rolesMarkerExempt(%q, %q, %s) = %v, want %v",
				tc.name, tc.root, tc.rel, tc.lit, got, tc.want)
		}
	}
}

// TestGreenfield_RolesMarkerExemptSitesStayHonest keeps the ADR-096 allow-list
// from outliving its entries — the same rule aliasesReadSites is held to. The
// walk records every file whose string literal matches the greenfield regex
// ONLY because of the marker (i.e. every match the exemption actually
// absorbs); a listed file that no longer carries such a literal is a stale row
// and must fail, so the exemption cannot silently outlive ADR-096's marker.
func TestGreenfield_RolesMarkerExemptSitesStayHonest(t *testing.T) {
	found := map[string]bool{}

	for _, root := range greenfieldRoots {
		walkGreenfieldGoFiles(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !greenfieldAliasRe.MatchString(lit.Value) {
					return true
				}
				if onlyRolesMarkerToken(lit.Value) {
					found[greenfieldSiteKey(root, rel)] = true
				}
				return true
			})
		})
	}

	for site, why := range rolesMarkerExemptSites {
		if !found[site] {
			t.Errorf("rolesMarkerExemptSites lists %s (%s) but no string literal in that file "+
				"matches the roles marker any more — remove the row so the exemption stays "+
				"exactly as wide as ADR-096 needs", site, why)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// walkGreenfieldGoFiles visits every non-test .go file under root, parsed with
// comments DISCARDED (see the file header: retired mechanisms are documented on
// purpose and that prose is not machinery). `rel` is relative to root.
func walkGreenfieldGoFiles(t *testing.T, root string, fn func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	var seen int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		seen++
		fn(filepath.ToSlash(rel), fset, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	// A typo in a root, a moved package or a partial checkout must not report a
	// green verdict for a tree that was never scanned (false-green-patterns).
	if seen == 0 {
		t.Fatalf("no non-test .go files found under %s — the scan covered nothing", root)
	}
}

func posLine(fset *token.FileSet, p token.Pos) string {
	return strings.TrimPrefix(fset.Position(p).String(), fset.Position(p).Filename+":")
}

// isCatalogFile reports whether rel (relative to root) is in the catalog
// package — the one place A-3 and FR-030 keep their tokens.
func isCatalogFile(root, rel string) bool {
	return root == "." && strings.HasPrefix(filepath.ToSlash(rel), "catalog/")
}

// onlyCatalogTokens reports whether a string literal matches the greenfield
// regex ONLY because of the two sanctioned tokens.
func onlyCatalogTokens(lit string) bool {
	stripped := lit
	for _, tok := range catalogAllowedLiteralTokens {
		stripped = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(tok)).ReplaceAllString(stripped, "")
	}
	return !greenfieldAliasRe.MatchString(stripped)
}

// greenfieldSiteKey names a scanned file the way both allow-lists key it —
// tree name + slash path, e.g. "catalog/parse.go" for the pkg/providers tree
// and "config/config.go" for the pkg/config tree. One source of truth: the
// staleness guarantees of both lists depend on this exact form.
func greenfieldSiteKey(root, rel string) string {
	if root == "." {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Join(filepath.Base(root), rel))
}

// onlyRolesMarkerToken reports whether a string literal matches the greenfield
// regex ONLY because of the ADR-096 marker. Stripping is case-insensitive and
// every other banned token still counts — "roles_migrated_at or deprecated
// shim" is machinery, not the marker.
func onlyRolesMarkerToken(lit string) bool {
	return !greenfieldAliasRe.MatchString(rolesMarkerTokenRe.ReplaceAllString(lit, ""))
}

// rolesMarkerExempt is the ADR-096 exemption, and it is narrow on BOTH axes:
// the file must be one of rolesMarkerExemptSites (exact tree + exact path —
// a same-named file anywhere else, including a subdirectory of pkg/config,
// still trips the guard), and the literal must match ONLY because of the
// marker. Applies to string literals only; identifiers stay unexempted.
func rolesMarkerExempt(root, rel, lit string) bool {
	if _, ok := rolesMarkerExemptSites[greenfieldSiteKey(root, rel)]; !ok {
		return false
	}
	return onlyRolesMarkerToken(lit)
}

// declaresFuncLocalJSONAliasType reports whether the file contains a
// function-local `type Alias <Ident>` declaration — the standard encoding/json
// trick for dropping a type's MarshalJSON method inside its own MarshalJSON.
// It is function-local by construction (a package-level `type Alias …` does not
// qualify), and its underlying type must be a plain identifier, so it cannot be
// a map, slice or struct pretending to be the idiom.
func declaresFuncLocalJSONAliasType(file *ast.File) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || spec.Name.Name != "Alias" || spec.Assign.IsValid() {
				return true
			}
			if _, plain := spec.Type.(*ast.Ident); plain {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

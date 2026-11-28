// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || linux

package component_test

// Office installer packaging P2 (#1713) — RED pack for the packaged
// LibreOffice manifest writer.
//
// CONTRACT the developer implements to (packaging-sizing.md P2; office-preview
// spec §10 packaged trust anchor; ADR-090 native-component exception):
//
//	// PackagedOfficeOptions selects the target platform for a build-time writer.
//	type PackagedOfficeOptions struct {
//	    GOOS   string // "darwin" | "linux" | "windows"; empty = runtime.GOOS
//	    GOARCH string // recorded in Manifest.Platform;  empty = runtime.GOARCH
//	}
//	// WritePackagedOfficeManifest fingerprints the packaged LibreOffice folder
//	// at dir and writes dir/manifest.json with trust "packaged" (schema 1,
//	// component "office", version from the install's metadata, pinned 26.8.x,
//	// executable relative to dir). On darwin it also writes dir/services/ with
//	// the AD-3-filtered .rdb files (registry "services"), beside
//	// LibreOffice.app, never inside the signed bundle. The engine lives in
//	// pkg/preview/component (it reuses locateOffice, officeVersion and
//	// filteredServices); the P1 build job calls it from a build-time tool
//	// under internal/ (path is the developer's choice).
//	func WritePackagedOfficeManifest(dir string, opts PackagedOfficeOptions) error
//
// The behavioural suite lives in office_packaged_manifest_contract_test.go,
// gated behind the //go:build omnipus_packaged_office tag: Go cannot compile a
// call to a symbol that does not exist (repo precedent:
// pkg/tools/chat_links_show_to_user_red_test.go). Implementing P2 REMOVES
// that build tag in the same commit, activating the behavioural tests. Until
// then the RED proof for the missing behaviour is the BLOCKED probe below —
// never a skip, never a silent pass.
//
// RED reason on the pre-change code: nothing produces a packaged LibreOffice
// manifest (register.go::RegisterOffice writes TrustOperatorRegistered only,
// into $OMNIPUS_HOME/system/components — that operator path is pinned by the
// PIN test below and must survive the planned split).

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// probePackagedWriter finds the contract's declarations in the component
// package sources and fails with the missing or malformed contract element.
// It parses the package directory this test file lives in (non-test files
// only), so the check is compile-safe while the symbol is absent.
func probePackagedWriter(t *testing.T) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed: cannot locate the package sources")
	pkgDir := filepath.Dir(thisFile)

	entries, err := os.ReadDir(pkgDir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var writer *ast.FuncDecl
	var options *ast.TypeSpec
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || isTestFile(name) {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if parseErr != nil {
			continue // a broken package source is the compiler's finding, not this probe's
		}
		if file.Name.Name != "component" {
			continue
		}
		for _, decl := range file.Decls {
			if fn, isFn := decl.(*ast.FuncDecl); isFn && fn.Name.Name == "WritePackagedOfficeManifest" {
				writer = fn
			}
			if gen, isGen := decl.(*ast.GenDecl); isGen && gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					if ts, isTs := spec.(*ast.TypeSpec); isTs && ts.Name.Name == "PackagedOfficeOptions" {
						options = ts
					}
				}
			}
		}
	}
	if writer == nil || options == nil {
		missing := "component.WritePackagedOfficeManifest"
		if writer != nil {
			missing = "component.PackagedOfficeOptions"
		}
		t.Fatalf("BLOCKED: %s not implemented — required by packaging-sizing P2 / office-preview spec §10 packaged trust anchor (contract: office_packaged_manifest_contract_test.go header)", missing)
	}
	if writer.Recv != nil {
		t.Fatalf("BLOCKED: WritePackagedOfficeManifest must be a plain function, not a method — contract in office_packaged_manifest_contract_test.go header")
	}
	if writer.Type.Params.NumFields() < 1 {
		t.Fatalf("BLOCKED: WritePackagedOfficeManifest has no parameters; contract first parameter is `dir string`")
	}
	first := writer.Type.Params.List[0]
	if len(first.Names) == 0 || first.Names[0].Name != "dir" {
		t.Fatalf("BLOCKED: WritePackagedOfficeManifest first parameter must be named dir — contract in office_packaged_manifest_contract_test.go header")
	}
	firstIdent, isIdent := first.Type.(*ast.Ident)
	if !isIdent || firstIdent.Name != "string" {
		t.Fatalf("BLOCKED: WritePackagedOfficeManifest first parameter must be dir string — contract in office_packaged_manifest_contract_test.go header")
	}
	if !endsWithError(writer.Type.Results) {
		t.Fatalf("BLOCKED: WritePackagedOfficeManifest must return error — contract in office_packaged_manifest_contract_test.go header")
	}
}

func isTestFile(name string) bool {
	base := filepath.Base(name)
	return len(base) > 8 && base[len(base)-8:] == "_test.go"
}

func endsWithError(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, field := range results.List {
		if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "error" {
			return true
		}
	}
	return false
}

// TestOfficePackagedManifest_WriterContractExists (P2): the build-time
// packaged-manifest writer exists with the contract shape. Behavioural proof
// (resolve acceptance, filtered services, tamper) is the tag-gated contract
// file; this probe is the loud RED until the symbol lands.
func TestOfficePackagedManifest_WriterContractExists(t *testing.T) {
	probePackagedWriter(t)
}

// TestOfficeRegistrationPin_RegisterOfficeStillWritesOperatorManifestIntoHome
// (PIN): the existing operator-registration path keeps writing an
// operator-registered manifest into $OMNIPUS_HOME/system/components/office/
// and never writes into the component folder. Oracle: component.go package
// doc ("written only by the operator CLI") and spec §10 operator-registration
// row. Green today; must stay green through the planned RegisterOffice
// "build manifest"/"write" split — it is the split's regression guard.
func TestOfficeRegistrationPin_RegisterOfficeStillWritesOperatorManifestIntoHome(t *testing.T) {
	// §3.9 fixture move (sanctioned): the PIN registers through the Linux
	// model, so the folder must sit inside the default program path's own
	// folder. The assertions are untouched.
	self, selfErr := os.Executable()
	require.NoError(t, selfErr)
	resolvedSelf, resolveErr := filepath.EvalSymlinks(self)
	require.NoError(t, resolveErr)
	own := filepath.Clean(filepath.Join(filepath.Dir(resolvedSelf), "..", "share", "omnipus"))
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(own) })
	root := filepath.Join(own, "office-pin")
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("fixture program, never executed"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(program, "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o600))

	home := t.TempDir()
	manifest, err := component.RegisterOffice(component.RegisterOptions{
		Home:     home,
		Folder:   root,
		Operator: "qa-red-pin",
		GOOS:     "linux",
	})
	require.NoError(t, err)
	require.NotNil(t, manifest)

	registered, evalErr := filepath.EvalSymlinks(root)
	require.NoError(t, evalErr)
	require.Equal(t, "26.8.1.1", manifest.Version, "version comes from the install's metadata (spec §10 official 26.8.x)")
	require.Equal(t, component.TrustOperatorRegistered, manifest.Trust, "RegisterOffice records operator trust, never packaged")
	require.Equal(t, registered, manifest.Root, "registered manifest records the verified absolute root")

	manifestFile := filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json")
	if _, statErr := os.Stat(manifestFile); errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("BLOCKED: operator manifest missing at %s — required by component.go package doc / spec §10 operator-registration row", manifestFile)
	}
	data, readErr := os.ReadFile(manifestFile)
	require.NoError(t, readErr)
	parsed, parseErr := component.ParseManifest(data, component.KindOffice)
	require.NoError(t, parseErr)
	require.Equal(t, registered, parsed.Root, "the persisted manifest re-validates against the registered root")

	require.NoFileExists(t, filepath.Join(root, "manifest.json"),
		"operator registration never writes a manifest into the component folder — that is the packaged writer's contract")
}

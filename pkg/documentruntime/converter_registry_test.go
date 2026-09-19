package documentruntime

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const macSpellFixture = `<implementation name="org.openoffice.lingu.MacOSXSpellChecker" constructor="lingucomponent_MacSpellChecker_get_implementation" single-instance="true"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation>`

const mySpellFixture = `<implementation name="org.openoffice.lingu.MySpellSpellChecker" constructor="lingucomponent_MySpellChecker_get_implementation"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation>`

func registryXML(components ...string) string {
	return `<components xmlns="http://openoffice.org/2010/uno-components">` + strings.Join(components, "") + `</components>`
}

func componentXML(uri string, implementations ...string) string {
	return `<component loader="com.sun.star.loader.SharedLibrary" uri="` + uri + `">` + strings.Join(implementations, "") + `</component>`
}

func mustConverterServiceOverride(t *testing.T, layout Layout) string {
	t.Helper()
	override, err := ConverterServiceOverride(layout)
	if err != nil {
		t.Fatal(err)
	}
	return override
}

// Review concern 1: the shipped registry may drift from the exact byte
// layout the filter was authored against, so matching must go through a
// parsed identity, not a literal byte search.
func TestFilterMacSpellCheckerMatchesParsedIdentityDespiteFormattingDrift(t *testing.T) {
	drifted := "\n    <implementation\n      constructor=\"lingucomponent_MacSpellChecker_get_implementation\"\n      single-instance=\"true\"\n      name=\"org.openoffice.lingu.MacOSXSpellChecker\">\n      <service name=\"com.sun.star.linguistic2.SpellChecker\" />\n    </implementation>\n  "
	registry := registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", drifted, mySpellFixture))
	got, stripped, err := filterMacSpellChecker([]byte(registry))
	if err != nil || !stripped {
		t.Fatalf("stripped=%v err=%v", stripped, err)
	}
	if strings.Contains(string(got), "MacOSXSpellChecker") {
		t.Fatal("macOS spell-checker registration survived identity filtering")
	}
	if !strings.Contains(string(got), "MySpellSpellChecker") {
		t.Fatal("filtering removed the HunSpell spell checker")
	}
	if !strings.Contains(string(got), "libmergedlo") {
		t.Fatal("filtering dropped the shared component shell")
	}
	if again, againStripped, err := filterMacSpellChecker(got); err != nil || againStripped || !bytes.Equal(again, got) {
		t.Fatalf("re-filter of filtered output stripped=%v err=%v", againStripped, err)
	}
}

// Diagnostic v4 mechanism: removing only the implementation leaves an empty
// component that the UNO registry parser rejects
// (InvalidRegistryException), so a sole implementation must take its whole
// component with it; a shared component keeps its shell.
func TestFilterMacSpellCheckerExcisesWholeComponentOnlyWhenSole(t *testing.T) {
	solo := registryXML(
		componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmacspelllo.dylib", macSpellFixture),
		componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", mySpellFixture),
	)
	got, stripped, err := filterMacSpellChecker([]byte(solo))
	if err != nil || !stripped {
		t.Fatalf("stripped=%v err=%v", stripped, err)
	}
	if strings.Contains(string(got), "libmacspelllo") {
		t.Fatal("empty component shell survived instead of being excised with its sole implementation")
	}
	if !strings.Contains(string(got), "libmergedlo") || !strings.Contains(string(got), "MySpellSpellChecker") {
		t.Fatal("filtering removed an unrelated component or implementation")
	}
	shared := registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", macSpellFixture, mySpellFixture))
	gotShared, stripped, err := filterMacSpellChecker([]byte(shared))
	if err != nil || !stripped {
		t.Fatalf("shared stripped=%v err=%v", stripped, err)
	}
	if strings.Contains(string(gotShared), "MacOSXSpellChecker") || !strings.Contains(string(gotShared), "libmergedlo") || !strings.Contains(string(gotShared), "MySpellSpellChecker") {
		t.Fatal("shared component handling removed the wrong span")
	}
}

func TestFilterMacSpellCheckerPassthroughAndRefusals(t *testing.T) {
	plain := registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", mySpellFixture))
	got, stripped, err := filterMacSpellChecker([]byte(plain))
	if err != nil || stripped || !bytes.Equal(got, []byte(plain)) {
		t.Fatalf("passthrough stripped=%v err=%v", stripped, err)
	}
	duplicate := registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", macSpellFixture, macSpellFixture))
	if _, _, err := filterMacSpellChecker([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "registrations") {
		t.Fatalf("duplicate registration err=%v", err)
	}
	orphan := registryXML(`<nested>` + macSpellFixture + `</nested>`)
	if _, _, err := filterMacSpellChecker([]byte(orphan)); err == nil || !strings.Contains(err.Error(), "direct child") {
		t.Fatalf("non-component parent err=%v", err)
	}
	if _, _, err := filterMacSpellChecker([]byte(`<components>`)); err == nil {
		t.Fatal("malformed registry was accepted")
	}
	if _, _, err := filterMacSpellChecker([]byte(`<components xmlns="http://openoffice.org/2010/uno-components"><component></components></component>`)); err == nil {
		t.Fatal("mismatched XML end tags were accepted")
	}
	if _, _, err := filterMacSpellChecker([]byte(`<components></components><components></components>`)); err == nil {
		t.Fatal("multiple XML roots were accepted")
	}
}

func writeConverterBundleFixture(t *testing.T, layout Layout, servicesRdb string) {
	t.Helper()
	macOS := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	soffice := filepath.Join(macOS, "soffice")
	if err := os.WriteFile(soffice, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	services := filepath.Join(filepath.Dir(macOS), "Resources", "services")
	if err := os.MkdirAll(services, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(services, "services.rdb"), []byte(servicesRdb), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(services, "pyuno.rdb"), []byte(registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libpyuno.dylib", mySpellFixture))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(soffice, filepath.Join(layout.Bin, "soffice")); err != nil {
		t.Fatal(err)
	}
}

func assertNoStagingLeftovers(t *testing.T, layout Layout) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(ConverterServiceRegistryDir(layout)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".libreoffice-services.") {
			t.Fatalf("staging or backup leftover: %s", entry.Name())
		}
	}
}

// Review concern 2: a failed re-finalize must stage and swap, never remove
// the published directory before the replacement is complete — otherwise a
// partial registry gets advertised over a previously working one.
func TestMaterializeConverterServiceRegistryStagesAndPreservesPublishedCopy(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	source := registryXML(
		componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmacspelllo.dylib", macSpellFixture),
		componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", mySpellFixture),
	)
	writeConverterBundleFixture(t, layout, source)
	converter := filepath.Join(layout.Bin, "soffice")
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(activeRegistryForTest(t, layout), "services.rdb")
	first, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "MacOSXSpellChecker") || !strings.Contains(string(first), "MySpellSpellChecker") {
		t.Fatalf("filtered registry wrong: %s", first)
	}
	sibling, err := os.ReadFile(filepath.Join(activeRegistryForTest(t, layout), "pyuno.rdb"))
	if err != nil || !strings.Contains(string(sibling), "libpyuno") {
		t.Fatalf("sibling registry copied=%q err=%v", sibling, err)
	}
	override := mustConverterServiceOverride(t, layout)
	if !strings.HasPrefix(override, "<file://") || !strings.HasSuffix(override, ">*") {
		t.Fatalf("override=%q", override)
	}
	// Sabotage the shipped registry so the next finalize fails mid-copy; the
	// previously published registry must survive intact and still advertised.
	sabotaged := registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", macSpellFixture, macSpellFixture))
	if err = os.WriteFile(filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "Resources", "services", "services.rdb"), []byte(sabotaged), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = materializeConverterServiceRegistry(layout, converter, "darwin"); err == nil || !strings.Contains(err.Error(), "registrations") {
		t.Fatalf("sabotaged re-finalize err=%v", err)
	}
	second, err := os.ReadFile(published)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("failed re-finalize damaged published registry: %q err=%v", second, err)
	}
	if got := mustConverterServiceOverride(t, layout); got != override {
		t.Fatalf("failed re-finalize withdrew the working override: %q", got)
	}
	assertNoStagingLeftovers(t, layout)
}

// Review concern 3: directory existence alone must not advertise an
// override; only a complete managed registry (non-empty regular
// services.rdb plus at least one .rdb) is usable.
func TestConverterServiceOverrideRequiresCompleteRegistry(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	if got := mustConverterServiceOverride(t, layout); got != "" {
		t.Fatalf("absent registry override=%q", got)
	}
	dst := ConverterServiceRegistryDir(layout)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := converterServiceRegistryUsable(dst); err == nil || !strings.Contains(err.Error(), "no .rdb") {
		t.Fatalf("empty registry directory err=%v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "pyuno.rdb"), []byte(registryXML(componentXML("libpyuno", mySpellFixture))), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := converterServiceRegistryUsable(dst); err == nil || !strings.Contains(err.Error(), "services.rdb") {
		t.Fatalf("registry without services.rdb err=%v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "services.rdb"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := converterServiceRegistryUsable(dst); err == nil || !strings.Contains(err.Error(), "non-empty regular") {
		t.Fatalf("empty services.rdb err=%v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "services.rdb"), []byte(registryXML(componentXML("vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib", mySpellFixture))), 0o644); err != nil {
		t.Fatal(err)
	}
	if usable, err := converterServiceRegistryUsable(dst); !usable || err != nil {
		t.Fatal("complete registry was not advertised")
	}
	if err := os.WriteFile(filepath.Join(dst, "services.rdb"), []byte(`<components xmlns="http://openoffice.org/2010/uno-components"><component></components></component>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := converterServiceRegistryUsable(dst); err == nil || !strings.Contains(err.Error(), "parse managed converter registry") {
		t.Fatalf("malformed services.rdb err=%v", err)
	}
}

// Review concern 4: an inherited URE_MORE_SERVICES would end up duplicated
// (or hostile) in the worker environment; exactly one managed value may
// survive.

func TestMaterializeConverterServiceRegistryDistinguishesBundleFromFlat(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	if err := os.MkdirAll(layout.Bin, 0o755); err != nil {
		t.Fatal(err)
	}
	flat := filepath.Join(layout.Bin, "soffice")
	if err := os.WriteFile(flat, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeConverterServiceRegistry(layout, flat, "darwin"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ConverterServiceRegistryDir(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("flat install materialized a registry: %v", err)
	}
	if got := mustConverterServiceOverride(t, layout); got != "" {
		t.Fatalf("flat install override=%q", got)
	}
	macOS := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(macOS, "soffice")
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeConverterServiceRegistry(layout, bundle, "darwin"); err == nil || !strings.Contains(err.Error(), "services") {
		t.Fatalf("bundle without services registry err=%v", err)
	}
	services := filepath.Join(filepath.Dir(macOS), "Resources", "services")
	if err := os.MkdirAll(services, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeConverterServiceRegistry(layout, bundle, "darwin"); err == nil || !strings.Contains(err.Error(), ".rdb") {
		t.Fatalf("empty services directory err=%v", err)
	}
	if _, err := os.Stat(ConverterServiceRegistryDir(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken bundle published a registry: %v", err)
	}
	if err := materializeConverterServiceRegistry(layout, bundle, "linux"); err != nil {
		t.Fatalf("non-darwin bundle management err=%v", err)
	}
}

// Reachability of the registry wiring: a bundle converter managed by the
// application leaves an advertised filtered registry behind.

func TestConverterRegistryRejectsCorruptSibling(t *testing.T) {
	for _, bad := range []struct {
		name, data string
		directory  bool
	}{
		{"empty", "", false},
		{"malformed", "<components><component></components>", false},
		{"wrong-root", "<unrelated/>", false},
		{"directory", "", true},
	} {
		t.Run(bad.name, func(t *testing.T) {
			layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
			source := registryXML(componentXML("libmergedlo", mySpellFixture))
			writeConverterBundleFixture(t, layout, source)
			converter := filepath.Join(layout.Bin, "soffice")
			if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
				t.Fatal(err)
			}
			published := activeRegistryForTest(t, layout)
			before, err := os.ReadFile(filepath.Join(published, "services.rdb"))
			if err != nil {
				t.Fatal(err)
			}
			corrupt := func(dir string) {
				t.Helper()
				path := filepath.Join(dir, "pyuno.rdb")
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if bad.directory {
					if err = os.Mkdir(path, 0755); err != nil {
						t.Fatal(err)
					}
				} else if err = os.WriteFile(path, []byte(bad.data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			shipped := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "Resources", "services")
			corrupt(shipped)
			if err = materializeConverterServiceRegistry(layout, converter, "darwin"); err == nil || !strings.Contains(err.Error(), "pyuno.rdb") {
				t.Errorf("corrupt shipped sibling must fail by name, got %v", err)
			}
			after, err := os.ReadFile(filepath.Join(published, "services.rdb"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed staging damaged published registry: %v", err)
			}
			corrupt(published)
			if override, err := ConverterServiceOverride(layout); err == nil || override != "" || !strings.Contains(err.Error(), "pyuno.rdb") {
				t.Fatalf("corrupt published sibling advertised: override=%q err=%v", override, err)
			}
		})
	}
}

func TestConverterRegistryNamespaceIdentity(t *testing.T) {
	const uno = "http://openoffice.org/2010/uno-components"
	valid := registryXML(componentXML("libmergedlo", mySpellFixture))
	for _, tag := range []string{"components", "component", "implementation"} {
		t.Run("foreign-"+tag, func(t *testing.T) {
			foreign := strings.Replace(valid, "<"+tag+" ", "<"+tag+" xmlns=\"urn:foreign\" ", 1)
			if tag == "components" {
				foreign = strings.Replace(valid, uno, "urn:foreign", 1)
			}
			data := []byte(foreign)
			if _, _, err := filterMacSpellChecker(data); err == nil || !strings.Contains(err.Error(), "namespace") {
				t.Errorf("foreign element accepted by filter: %v", err)
			}
			if err := verifyFilteredRegistry(data, 1, 1); err == nil || !strings.Contains(err.Error(), "namespace") {
				t.Errorf("foreign element accepted by filtered validation: %v", err)
			}
			if err := validateManagedRegistry(data); err == nil || !strings.Contains(err.Error(), "namespace") {
				t.Errorf("foreign element accepted by readiness: %v", err)
			}
		})
	}
	t.Run("qualified-name-is-not-identity", func(t *testing.T) {
		data := []byte(strings.Replace(valid, `<implementation name=`, `<implementation xmlns:x="urn:foreign" x:name="org.openoffice.lingu.MacOSXSpellChecker" name=`, 1))
		got, stripped, err := filterMacSpellChecker(data)
		if err != nil || stripped || !bytes.Equal(got, data) {
			t.Fatalf("foreign attribute changed registry: stripped=%v err=%v", stripped, err)
		}
		if err := verifyFilteredRegistry(data, 1, 1); err != nil {
			t.Fatal(err)
		}
		if err := validateManagedRegistry(data); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("prefix-alias-resolves-same-namespace", func(t *testing.T) {
		data := registryXML(componentXML("libmergedlo", macSpellFixture, mySpellFixture))
		data = strings.Replace(data, `xmlns="`+uno+`"`, `xmlns:u="`+uno+`"`, 1)
		for _, tag := range []string{"components", "component", "implementation", "service"} {
			data = strings.ReplaceAll(data, "<"+tag+" ", "<u:"+tag+" ")
			data = strings.ReplaceAll(data, "</"+tag+">", "</u:"+tag+">")
		}
		got, stripped, err := filterMacSpellChecker([]byte(data))
		if err != nil || !stripped || strings.Contains(string(got), "MacOSXSpellChecker") || !strings.Contains(string(got), "MySpellSpellChecker") {
			t.Fatalf("valid prefix filtering failed: stripped=%v err=%v", stripped, err)
		}
	})
}

func activeRegistryForTest(t *testing.T, layout Layout) string {
	t.Helper()
	value := mustConverterServiceOverride(t, layout)
	u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">*"))
	if err != nil || u.Scheme != "file" {
		t.Fatalf("invalid override %q: %v", value, err)
	}
	return u.Path
}

func TestConverterRegistryPublicationPreservesInflightReader(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	oldXML := registryXML(componentXML("lib-old", mySpellFixture))
	newXML := registryXML(componentXML("lib-new", mySpellFixture))
	writeConverterBundleFixture(t, layout, oldXML)
	converter := filepath.Join(layout.Bin, "soffice")
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	oldDir := activeRegistryForTest(t, layout)
	source := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "Resources", "services", "services.rdb")
	if err := os.WriteFile(source, []byte(newXML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(oldDir, "services.rdb"))
	if err != nil || string(got) != oldXML {
		t.Fatalf("in-flight reader lost old complete registry: got %q err %v", got, err)
	}
	newDir := activeRegistryForTest(t, layout)
	if newDir == oldDir {
		t.Fatal("new content reused mutable old location")
	}
	got, err = os.ReadFile(filepath.Join(newDir, "services.rdb"))
	if err != nil || string(got) != newXML {
		t.Fatalf("new reader did not select new registry: %q %v", got, err)
	}
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	if got := activeRegistryForTest(t, layout); got != newDir {
		t.Fatalf("identical content did not reuse generation: %s", got)
	}
}

// An uncommitted generation models interruption between preparation and the
// final publication. The old URL and bytes must remain usable throughout.
func TestConverterRegistryPreparationDoesNotPublishAndCommitRevalidates(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	oldXML := registryXML(componentXML("lib-old", mySpellFixture))
	newXML := registryXML(componentXML("lib-new", mySpellFixture))
	writeConverterBundleFixture(t, layout, oldXML)
	converter := filepath.Join(layout.Bin, "soffice")
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	before := mustConverterServiceOverride(t, layout)
	source := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "Resources", "services", "services.rdb")
	if err := os.WriteFile(source, []byte(newXML), 0644); err != nil {
		t.Fatal(err)
	}
	commit, err := prepareConverterServiceRegistry(layout, converter, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustConverterServiceOverride(t, layout); got != before {
		t.Fatalf("preparation published without commit: %s", got)
	}
	oldDir := activeRegistryForTest(t, layout)
	entries, err := os.ReadDir(ConverterServiceRegistryDir(layout))
	if err != nil {
		t.Fatal(err)
	}
	var next string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != filepath.Base(oldDir) {
			next = filepath.Join(ConverterServiceRegistryDir(layout), entry.Name())
		}
	}
	if next == "" {
		t.Fatal("prepared generation missing")
	}
	// Even well-formed changed content must fail its digest, not be committed.
	if err := os.WriteFile(filepath.Join(next, "services.rdb"), []byte(oldXML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := commit(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("commit accepted modified generation: %v", err)
	}
	if got := mustConverterServiceOverride(t, layout); got != before {
		t.Fatalf("failed commit changed active selection: %s", got)
	}
}

func TestConverterRegistryInitializedStateNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"missing-pointer", "empty-pointer", "short-pointer", "long-pointer", "nonhex-pointer", "uppercase-pointer", "path-pointer", "missing-generation", "symlink-generation", "tampered-generation"} {
		t.Run(kind, func(t *testing.T) {
			layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
			source := registryXML(componentXML("lib-old", mySpellFixture))
			writeConverterBundleFixture(t, layout, source)
			if err := materializeConverterServiceRegistry(layout, filepath.Join(layout.Bin, "soffice"), "darwin"); err != nil {
				t.Fatal(err)
			}
			selected := activeRegistryForTest(t, layout)
			pointer := filepath.Join(ConverterServiceRegistryDir(layout), "active")
			var err error
			expected := "pointer"
			switch kind {
			case "missing-pointer":
				err = os.Remove(pointer)
				expected = "active converter registry"
			case "empty-pointer":
				err = os.WriteFile(pointer, nil, 0644)
			case "short-pointer":
				err = os.WriteFile(pointer, []byte(strings.Repeat("a", 63)), 0644)
			case "long-pointer":
				err = os.WriteFile(pointer, []byte(strings.Repeat("a", 65)), 0644)
			case "nonhex-pointer":
				err = os.WriteFile(pointer, []byte(strings.Repeat("z", 64)), 0644)
			case "uppercase-pointer":
				err = os.WriteFile(pointer, []byte(strings.Repeat("A", 64)), 0644)
			case "path-pointer":
				err = os.WriteFile(pointer, []byte("../"+strings.Repeat("a", 61)), 0644)
			case "missing-generation":
				err = os.RemoveAll(selected)
				expected = "generation"
			case "symlink-generation":
				outside := filepath.Join(t.TempDir(), "registry")
				if err = os.Rename(selected, outside); err == nil {
					err = os.Symlink(outside, selected)
				}
				expected = "generation is not a directory"
			case "tampered-generation":
				err = os.WriteFile(filepath.Join(selected, "services.rdb"), []byte(registryXML(componentXML("lib-tampered", mySpellFixture))), 0644)
				expected = "checksum mismatch"
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := ConverterServiceOverride(layout)
			if got != "" || err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("initialized corrupt state returned override %q error %v; want error containing %q", got, err, expected)
			}
		})
	}
}

func TestConverterRegistryConcurrentReadersKeepCompleteSelection(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	first := registryXML(componentXML("lib-first", mySpellFixture))
	second := registryXML(componentXML("lib-second", mySpellFixture))
	writeConverterBundleFixture(t, layout, first)
	converter := filepath.Join(layout.Bin, "soffice")
	if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(layout.Prefix, "libreoffice", "LibreOffice.app", "Contents", "Resources", "services", "services.rdb")
	finished := make(chan error, 1)
	start := make(chan struct{})
	go func() {
		close(start)
		for i := 0; i < 100; i++ {
			value, err := ConverterServiceOverride(layout)
			if err != nil {
				finished <- err
				return
			}
			u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">*"))
			if err != nil || u.Scheme != "file" {
				finished <- fmt.Errorf("reader lost managed selection: %q %w", value, err)
				return
			}
			data, err := os.ReadFile(filepath.Join(u.Path, "services.rdb"))
			if err != nil || (string(data) != first && string(data) != second) {
				finished <- fmt.Errorf("reader selected incomplete registry: %q %w", data, err)
				return
			}
		}
		finished <- nil
	}()
	<-start
	for i := 0; i < 10; i++ {
		content := first
		if i%2 == 0 {
			content = second
		}
		if err := os.WriteFile(source, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := materializeConverterServiceRegistry(layout, converter, "darwin"); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

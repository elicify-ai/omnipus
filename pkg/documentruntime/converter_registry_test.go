package documentruntime

// Office-preview AD-3 / T-42 / T-73 deletes the runtime registry store.
// Keep the packaging filter's identity, XML, idempotence and refusal tests;
// store publication, override and generation tests retire with that store.

import (
	"bytes"
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

// AD-3 keeps a packaging entrypoint, not a runtime store. Exercise its actual
// exported filter in addition to the lower-level XML/identity tests above.
func TestOfficePreview_PackagingFilterPreservesUnrelatedRegistry(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{
			name:  "shared_component",
			input: registryXML(componentXML("libmergedlo", macSpellFixture, mySpellFixture)),
			want:  registryXML(componentXML("libmergedlo", mySpellFixture)),
		},
		{
			name:  "sole_component",
			input: registryXML(componentXML("libmacspelllo", macSpellFixture), componentXML("libmergedlo", mySpellFixture)),
			want:  registryXML(componentXML("libmergedlo", mySpellFixture)),
		},
		{
			name:  "already_filtered",
			input: registryXML(componentXML("libmergedlo", mySpellFixture)),
			want:  registryXML(componentXML("libmergedlo", mySpellFixture)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FilterConverterRegistry([]byte(tc.input))
			if err != nil || string(got) != tc.want {
				t.Fatalf("packaging registry = %q, err=%v; want exact %q (AD-3)", got, err, tc.want)
			}
			again, err := FilterConverterRegistry(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("packaging filter not idempotent: err=%v got=%q", err, again)
			}
		})
	}
	duplicate := registryXML(componentXML("libmergedlo", macSpellFixture, macSpellFixture, mySpellFixture))
	if _, err := FilterConverterRegistry([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "registrations") {
		t.Fatalf("ambiguous spell-checker registrations must be rejected: %v", err)
	}
	t.Run("filtering_cannot_leave_empty_registry", func(t *testing.T) {
		// AD-3's packaged result must remain usable: deleting its only
		// implementation cannot produce a successful packaging artifact.
		// The spec requires refusal, not a particular error string/type.
		onlyRetired := registryXML(componentXML("libmacspelllo", macSpellFixture))
		got, err := FilterConverterRegistry([]byte(onlyRetired))
		if err == nil || got != nil {
			t.Fatalf("packaging accepted an implementation-free registry: result=%q err=%v", got, err)
		}
	})
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

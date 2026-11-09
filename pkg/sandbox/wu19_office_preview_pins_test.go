package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const wu19OfficeUpdateSwitch = "OMNIPUS_OFFICE_UPDATE_ENV_PINS"
const wu19OfficePinFile = "tests/officepreview/closed-env-source-pins.json"
const wu19OfficePinGuide = "tests/officepreview/CLOSED-ENV-PINS.md"

type wu19OfficeFilePin struct {
	WholeFile    string            `json:"whole_file,omitempty"`
	Declarations map[string]string `json:"declarations"`
}

type wu19OfficePinManifest struct {
	Algorithm string                       `json:"algorithm"`
	Reviewed  string                       `json:"reviewed_source"`
	Files     map[string]wu19OfficeFilePin `json:"files"`
}

// Nil selects the WHOLE launcher boundary file, including the set of
// declarations/imports. Every other file selects only named declarations.
// Changes to this approved scope/caller list require security-lead review.
func wu19OfficePinScope() map[string][]string {
	return map[string][]string{
		"pkg/preview/launcher/child_darwin.go":    nil,
		"pkg/preview/launcher/child_linux.go":     nil,
		"pkg/preview/launcher/child_unix.go":      nil,
		"pkg/preview/launcher/child_windows.go":   nil,
		"pkg/preview/launcher/launcher.go":        nil,
		"pkg/preview/launcher/proc_darwin.go":     nil,
		"pkg/preview/launcher/proc_linux.go":      nil,
		"pkg/preview/launcher/proc_unix.go":       nil,
		"pkg/preview/launcher/proc_windows.go":    nil,
		"pkg/preview/launcher/watchdog_darwin.go": nil,
		"pkg/preview/launcher/watchdog_other.go":  nil,
		"pkg/preview/programs.go":                 {"protocolProgram", "protocolProgram.run", "protocolProgram.publishAndGo", "officeConversion", "officeConversion.run", "officeConversion.startOnce", "officeStartHooks", "existingFolders", "fileURL"},
		"pkg/preview/confine.go":                  {"confinement", "spawnHooks", "converterStart", "testHookConverterStart", "spawnHooks.observedBy", "foldersAbove", "realRulePath"},
		"pkg/preview/confine_unix.go":             {"confinement.policy"},
		"pkg/preview/confine_darwin.go":           {"officeFontFolders", "hooksFor"},
		"pkg/preview/confine_linux.go":            {"linuxSystemReadExec", "officeFontFolders", "hooksFor"},
		"pkg/preview/confine_windows.go":          {"officeFontFolders", "hooksFor"},
		"pkg/sandbox/backend_darwin_seatbelt.go":  {"CurrentSeatbeltBackend", "currentSeatbeltBackend", "seatbeltDisableEnv", "seatbeltExecPath", "SeatbeltBackend.Available", "SeatbeltBackend.ApplyToCmd", "SeatbeltBackend.wrapUnderSandboxExec"},
		"pkg/sandbox/hardened_exec_held.go":       {"StartLockedWithPolicyHeld"},
		"pkg/app/app.go":                          {"Main", "runHiddenMode"},
		"cmd/omnipus/main.go":                     {"main"},
	}
}

func wu19OfficeLocation(name string) string {
	absolute, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(name)))
	if err != nil {
		panic(fmt.Sprintf("cannot resolve Office preview test location: %v", err))
	}
	return absolute
}

func wu19OfficeSorted[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (p *wu19Inventory) officePreviewCurrentPins() (wu19OfficePinManifest, string) {
	pins := wu19OfficePinManifest{Algorithm: wu19OfficeDigestVersion, Files: map[string]wu19OfficeFilePin{}}
	scope := wu19OfficePinScope()
	files := map[string]*wu19File{}
	for _, file := range p.files {
		name := wu19OfficeFileName(file)
		if _, known := scope[name]; file.pkg == wu19PreviewLauncher && !known {
			declarations, err := wu19OfficeDeclarations(file)
			if err != nil {
				return pins, wu19OfficeDrift("unreviewed launcher file %s: %v", wu19OfficeLocation(name), err)
			}
			return pins, wu19OfficeDrift("unreviewed launcher file %s (declarations: %s)", wu19OfficeLocation(name), strings.Join(wu19OfficeSorted(declarations), ", "))
		}
		if _, pinned := scope[name]; !pinned {
			continue
		}
		if files[name] != nil {
			return pins, wu19OfficeDrift("duplicate source identity %s", wu19OfficeLocation(name))
		}
		files[name] = file
	}
	for _, name := range wu19OfficeSorted(scope) {
		file := files[name]
		if file == nil {
			return pins, wu19OfficeDrift("reviewed boundary file missing: %s", wu19OfficeLocation(name))
		}
		nodes, err := wu19OfficeDeclarations(file)
		if err != nil {
			return pins, wu19OfficeDrift("invalid declarations in %s: %v", wu19OfficeLocation(name), err)
		}
		selected := append([]string(nil), scope[name]...)
		entry := wu19OfficeFilePin{Declarations: map[string]string{}}
		if scope[name] == nil {
			selected = wu19OfficeSorted(nodes)
			entry.WholeFile, err = wu19OfficeTokenDigest(file.source, nil)
			if err != nil {
				return pins, wu19OfficeDrift("invalid boundary file %s: %v", wu19OfficeLocation(name), err)
			}
		}
		for _, declaration := range selected {
			node, exists := nodes[declaration]
			if !exists {
				return pins, wu19OfficeDrift("reviewed declaration %s::%s missing", wu19OfficeLocation(name), declaration)
			}
			digest, err := wu19OfficeDeclarationDigest(file, node)
			if err != nil {
				return pins, wu19OfficeDrift("cannot digest declaration %s::%s: %v", wu19OfficeLocation(name), declaration, err)
			}
			entry.Declarations[declaration] = digest
		}
		pins.Files[name] = entry
	}
	return pins, ""
}

func wu19OfficeComparePins(want, got wu19OfficePinManifest) string {
	if want.Algorithm != wu19OfficeDigestVersion || len(want.Reviewed) != 40 || len(want.Files) != len(got.Files) {
		return wu19OfficeDrift("invalid reviewed pin manifest (algorithm/reviewed revision/scope)")
	}
	for _, name := range wu19OfficeSorted(got.Files) {
		actual := got.Files[name]
		expected, exists := want.Files[name]
		if !exists || (expected.WholeFile == "") != (actual.WholeFile == "") {
			return wu19OfficeDrift("reviewed pin scope changed for %s", wu19OfficeLocation(name))
		}
		for _, declaration := range wu19OfficeSorted(actual.Declarations) {
			digest := actual.Declarations[declaration]
			old, exists := expected.Declarations[declaration]
			if !exists {
				return wu19OfficeDrift("unreviewed declaration %s::%s in whole-file boundary", wu19OfficeLocation(name), declaration)
			}
			if old != digest {
				return wu19OfficeDrift("reviewed declaration %s::%s changed", wu19OfficeLocation(name), declaration)
			}
		}
		for _, declaration := range wu19OfficeSorted(expected.Declarations) {
			if _, exists := actual.Declarations[declaration]; !exists {
				return wu19OfficeDrift("reviewed declaration %s::%s missing or outside approved scope", wu19OfficeLocation(name), declaration)
			}
		}
		if actual.WholeFile != expected.WholeFile {
			return wu19OfficeDrift("reviewed declaration %s::<package/imports> changed in whole-file boundary", wu19OfficeLocation(name))
		}
	}
	return ""
}

// Called only by the explicitly selected fixture test and only with the switch
// set to exactly 1. Normal proof/inventory runs have no write path.
func wu19OfficeMaybeUpdatePins(t *testing.T, p *wu19Inventory) {
	t.Helper()
	requested := os.Getenv(wu19OfficeUpdateSwitch)
	if requested == "" {
		return
	}
	if requested != "1" {
		t.Fatalf("%s must be exactly 1 for an explicit post-security-lead-review pin update", wu19OfficeUpdateSwitch)
	}
	pins, reason := p.officePreviewCurrentPins()
	if reason != "" {
		t.Fatal(reason)
	}
	// A new boundary/caller or startup mutation still needs an approved list
	// change: regenerating digests alone must not bless it.
	if reason := p.officePreviewBoundaryReferences(); reason != "" {
		t.Fatal(reason)
	}
	if reason := p.officePreviewStartupEnvironment(); reason != "" {
		t.Fatal(reason)
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("read source revision for explicit pin update: %v", err)
	}
	pins.Reviewed = strings.TrimSpace(string(raw))
	encoded, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	location := wu19OfficeLocation(wu19OfficePinFile)
	temporary, err := os.CreateTemp(filepath.Dir(location), ".closed-env-pins-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(temporary.Name()) })
	if err := temporary.Chmod(0644); err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary.Name(), location); err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPLICIT PIN UPDATE: wrote %s for source %s; this records bytes, not a security-lead review receipt; review and commit the diff", location, pins.Reviewed)
}

//go:build linux

package component_test

// T-130 (a), (b), (d), (e), (f), (g), (h) — office-preview spec §3.9
// (2026-11-21, founder decision OF; FR-067): on Linux, registration accepts
// a component only inside Omnipus's own folder (OWN), judged first, on the
// folder as given AND on its resolved root, by whole path components. The
// oracles are the spec's two OF-3 error texts, never the code.
//
// This file uses the DEFAULT program path (the running program, resolved as
// component.NewResolver resolves it), so OWN is the running binary's
// dir(E)/../share/omnipus, created by the test. Its refusal rows register
// through the component API; its refusal texts here are the UNPREFIXED
// OF-3 sentences, because this package may not import the internal
// components package (Go's internal-package rule) — the command layer's
// prefixed error is pinned in pkg/app/internal/components instead. The
// acceptance guards also use the direct API because they assert manifest
// fields. The explicit-program-path
// shapes of T-130 (c) live in registration_own_folder_shapes_linux_test.go,
// which needs the new RegisterOptions field (spec §13 staging: it compiles
// red until the field stub lands).
//
// RED on the code before §3.9: (a), (b), (d) first two, (e), (f) and (g)
// fail on an assertion — the registration succeeds or returns another
// error. (h) passes before and after and guards against over-refusal.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// t130OwnFolder returns the default program path's OWN, creating it.
func t130OwnFolder(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	own := filepath.Clean(filepath.Join(filepath.Dir(resolved), "..", "share", "omnipus"))
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(own) })
	return own
}

// t130BaseDir returns the default program path's dir(E)/.. — the parent the
// outside fixtures stage under (they must stay outside OWN by construction,
// which every refusal case asserts before registering).
func t130BaseDir(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(filepath.Dir(resolved), ".."))
}

// t130OfficeFixture lays out a valid 26.8.1.1 LibreOffice (metadata-only
// version probe) at root.
func t130OfficeFixture(t *testing.T, root string) {
	t.Helper()
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
	registry := filepath.Join(root, "share", "registry")
	require.NoError(t, os.MkdirAll(registry, 0o755))
	const product = `<?xml version="1.0"?><oor:data xmlns:oor="http://openoffice.org/2001/registry"><oor:component-data oor:name="Setup" oor:package="org.openoffice"><node oor:name="Product"><prop oor:name="ooSetupVersionAboutBox"><value>26.8.1.1</value></prop></node></oor:component-data></oor:data>`
	require.NoError(t, os.WriteFile(filepath.Join(registry, "main.xcd"), []byte(product), 0o600))
}

// t130RendererFixture lays out a valid unpacked renderer release (with its
// own packaged manifest) at root.
func t130RendererFixture(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "omnipus-pdfium"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	sum, _, err := component.HashFile(filepath.Join(root, "omnipus-pdfium"))
	require.NoError(t, err)
	files := map[string]string{"omnipus-pdfium": sum}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: component.KindPDFRenderer,
		Version: "26.8.0.3", BuildID: "t130-renderer", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: "omnipus-pdfium",
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustPackaged,
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, component.ManifestFile), raw, 0o600))
}

func t130OfficeText(folder, own string) string {
	// Unprefixed: the component API's error text. The command layer returns
	// this with the `office not registered: ` prefix (spec line 972), which
	// register_own_folder_command_linux_test.go pins where the command
	// lives.
	return "LibreOffice folder " + folder + " is outside Omnipus's own folder (" + own +
		"); on Linux, Omnipus accepts only a LibreOffice inside that folder. Re-run the Omnipus installer, which puts LibreOffice in " +
		own + "/office, or put LibreOffice in a new folder inside " + own + " and register that folder"
}

func t130RendererText(folder, own string) string {
	// Unprefixed: see t130OfficeText.
	return "PDF renderer folder " + folder + " is outside Omnipus's own folder (" + own +
		"); on Linux, Omnipus accepts only a PDF renderer inside that folder. Re-run the Omnipus installer, which puts the renderer in " +
		own + "/pdfium, or unpack the renderer release in a new folder inside " + own + " and register that folder"
}

// t130Refused asserts the shared refusal shape: the exact OF-3 error and no
// manifest written.
func t130Refused(t *testing.T, err error, want string, home string, kind component.Kind) {
	t.Helper()
	require.EqualError(t, err, want, "T-130: the refusal must be the OF-3 text word for word")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, kind), "manifest.json"),
		"T-130: a refused registration writes nothing")
}

// t130CommandHome is retired (D12 second attempt): this package may not
// import pkg/app/internal/components — Go's internal-package rule — so the
// command path lives in the components package instead
// (register_own_folder_command_linux_test.go pins the person-visible
// prefixed text there, spec line 972 / behavior B-89).

// t130RegisterOffice and t130RegisterRenderer register through the
// component API (D12, team-lead 2026-11-21, sanctioned fallback: "add the
// prefix in the test" — the prefix cannot be driven here because the
// command package is internal to pkg/app). The texts below are therefore
// the component layer's UNPREFIXED OF-3 sentences; the
// `<kind> not registered: ` prefix that spec line 972 puts on the command's
// returned error is composed and pinned at the command layer.
func t130RegisterOffice(t *testing.T, home string, folder string) error {
	t.Helper()
	_, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: folder, Operator: "t130", GOOS: "linux"})
	return err
}

func t130RegisterRenderer(t *testing.T, home string, folder string) error {
	t.Helper()
	_, err := component.RegisterRenderer(component.RegisterOptions{Home: home, Folder: folder, Operator: "t130", GOOS: "linux"})
	return err
}

// t130Outside asserts the precondition that path lies outside own by whole
// path components (as written), so only the location rule can refuse it.
func t130Outside(t *testing.T, path, own, what string) {
	t.Helper()
	inside := path == own || (len(path) > len(own) && path[:len(own)] == own && path[len(own)] == filepath.Separator)
	require.False(t, inside, "T-130 precondition: %s (%s) must lie outside Omnipus's own folder %s", what, path, own)
}

// TestT130_RegistrationRefusedOutsideOwnFolder — OF-3 (a): fixtures under a
// folder outside OWN are refused with the exact OF-3 texts.
func TestT130_RegistrationRefusedOutsideOwnFolder(t *testing.T) {
	own := t130OwnFolder(t)
	base := t130BaseDir(t)
	home := t.TempDir()

	office := filepath.Join(base, "elsewhere", "opt", "libreoffice26.8")
	t130OfficeFixture(t, office)
	t130Outside(t, office, own, "the office fixture")
	err := t130RegisterOffice(t, home, office)
	t130Refused(t, err, t130OfficeText(filepath.Clean(office), own), home, component.KindOffice)

	renderer := filepath.Join(base, "elsewhere", "usr", "lib", "pdfium")
	t130RendererFixture(t, renderer)
	t130Outside(t, renderer, own, "the renderer fixture")
	err = t130RegisterRenderer(t, home, renderer)
	t130Refused(t, err, t130RendererText(filepath.Clean(renderer), own), home, component.KindPDFRenderer)
}

// TestT130_RegistrationRefusedInRealSystemFolders — OF-3 (b): copies at
// /opt and /usr/lib are refused like any other outside folder. Requires
// passwordless sudo on the runner; missing sudo fails the test, never skips.
func TestT130_RegistrationRefusedInRealSystemFolders(t *testing.T) {
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Fatalf("BLOCKED: T-130(b) requires sudo -n on the Linux runner — required by office-preview spec §3.9 T-130; it fails, never skips, when sudo is missing")
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Fatalf("BLOCKED: T-130(b) requires passwordless sudo -n — required by office-preview spec §3.9 T-130; probe failed: %v", err)
	}
	own := t130OwnFolder(t)
	home := t.TempDir()
	suffix := t130RandomSuffix(t)

	tmpOffice := t.TempDir()
	t130OfficeFixture(t, filepath.Join(tmpOffice, "libreoffice"))
	office := filepath.Join("/opt", "omnipus-t130-"+suffix, "libreoffice")
	out, err := exec.Command("sudo", "-n", "mkdir", "-p", filepath.Dir(office)).CombinedOutput()
	require.NoError(t, err, "sudo mkdir: %s", out)
	out, err = exec.Command("sudo", "-n", "cp", "-a", filepath.Join(tmpOffice, "libreoffice"), office).CombinedOutput()
	require.NoError(t, err, "sudo cp: %s", out)
	out, err = exec.Command("sudo", "-n", "chmod", "-R", "a+rX", office).CombinedOutput()
	require.NoError(t, err, "sudo chmod: %s", out)
	t.Cleanup(func() { _ = exec.Command("sudo", "-n", "rm", "-rf", filepath.Dir(office)).Run() })
	err = t130RegisterOffice(t, home, office)
	t130Refused(t, err, t130OfficeText(filepath.Clean(office), own), home, component.KindOffice)

	tmpRenderer := t.TempDir()
	t130RendererFixture(t, filepath.Join(tmpRenderer, "pdfium"))
	renderer := filepath.Join("/usr/lib", "omnipus-t130-"+suffix, "pdfium")
	out, err = exec.Command("sudo", "-n", "mkdir", "-p", filepath.Dir(renderer)).CombinedOutput()
	require.NoError(t, err, "sudo mkdir: %s", out)
	out, err = exec.Command("sudo", "-n", "cp", "-a", filepath.Join(tmpRenderer, "pdfium"), renderer).CombinedOutput()
	require.NoError(t, err, "sudo cp: %s", out)
	out, err = exec.Command("sudo", "-n", "chmod", "-R", "a+rX", renderer).CombinedOutput()
	require.NoError(t, err, "sudo chmod: %s", out)
	t.Cleanup(func() { _ = exec.Command("sudo", "-n", "rm", "-rf", filepath.Dir(renderer)).Run() })
	err = t130RegisterRenderer(t, home, renderer)
	t130Refused(t, err, t130RendererText(filepath.Clean(renderer), own), home, component.KindPDFRenderer)
}

// t130RandomSuffix gives the real-system folders their per-run name.
func t130RandomSuffix(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "t130-suffix-")
	require.NoError(t, err)
	name := filepath.Base(dir)
	require.NoError(t, os.RemoveAll(dir))
	return strings.TrimPrefix(name, "t130-suffix-")
}

// TestT130_LinksAreJudgedByBothForms — OF-3 (d): a link outside OWN that
// leads inside is refused and its text carries the resolution; a link
// inside OWN that leads outside is refused; OWN itself being a link to
// another real folder keeps a fixture inside it accepted.
func TestT130_LinksAreJudgedByBothForms(t *testing.T) {
	own := t130OwnFolder(t)
	base := t130BaseDir(t)
	home := t.TempDir()

	// A real fixture inside OWN, reached through a link that lives outside.
	insideOwn := filepath.Join(own, "libreoffice-real")
	t130OfficeFixture(t, insideOwn)
	outsideLink := filepath.Join(base, "elsewhere", "office-link")
	require.NoError(t, os.MkdirAll(filepath.Dir(outsideLink), 0o755))
	require.NoError(t, os.Symlink(insideOwn, outsideLink))
	resolved, err := filepath.EvalSymlinks(outsideLink)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(insideOwn), filepath.Clean(resolved), "instrument: the link resolves into OWN")
	err = t130RegisterOffice(t, home, outsideLink)
	t130Refused(t, err, t130OfficeText(filepath.Clean(outsideLink)+" (links lead to "+resolved+")", own), home, component.KindOffice)

	// A link INSIDE OWN leading to a real folder outside.
	away := filepath.Join(base, "elsewhere", "office-away")
	t130OfficeFixture(t, away)
	insideLink := filepath.Join(own, "office-out-link")
	require.NoError(t, os.Symlink(away, insideLink))
	resolvedAway, err := filepath.EvalSymlinks(insideLink)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(away), filepath.Clean(resolvedAway), "instrument: the inside link resolves outside OWN")
	err = t130RegisterOffice(t, home, insideLink)
	t130Refused(t, err, t130OfficeText(filepath.Clean(insideLink)+" (links lead to "+resolvedAway+")", own), home, component.KindOffice)

	// OWN itself a link to another real folder; a fixture inside it is
	// accepted (guard). The acceptance keeps the direct API: it asserts the
	// manifest fields, which is what this guard row is about. RemoveAll (not
	// Remove): the earlier cases in this test left their fixtures inside
	// OWN, so the folder is not empty here.
	other := t.TempDir()
	require.NoError(t, os.RemoveAll(own))
	require.NoError(t, os.Symlink(other, own))
	inOwn := filepath.Join(own, "libreoffice-in-link")
	t130OfficeFixture(t, inOwn)
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: inOwn, Operator: "t130", GOOS: "linux"})
	require.NoError(t, err, "T-130(d): OWN as a link keeps a fixture inside it accepted")
	require.NotNil(t, manifest)
	require.NoError(t, os.Remove(own))
	require.NoError(t, os.MkdirAll(own, 0o755))
}

// TestT130_TraversalAndNamePrefixRefused — OF-3 (e): a traversal that
// cleans outside OWN, a sibling whose name only shares a prefix, and OWN
// itself are all refused.
func TestT130_TraversalAndNamePrefixRefused(t *testing.T) {
	own := t130OwnFolder(t)
	base := t130BaseDir(t)
	home := t.TempDir()

	sep := string(filepath.Separator)
	traversal := own + sep + "a" + sep + ".." + sep + ".." + sep + "elsewhere" + sep + "lo"
	t130OfficeFixture(t, filepath.Join(base, "elsewhere", "lo"))
	t130Outside(t, filepath.Clean(traversal), own, "the traversal target")
	err := t130RegisterOffice(t, home, traversal)
	t130Refused(t, err, t130OfficeText(filepath.Clean(traversal), own), home, component.KindOffice)

	sibling := filepath.Join(filepath.Dir(own), "omnipus-other", "lo")
	t130OfficeFixture(t, sibling)
	t130Outside(t, sibling, own, "the prefix-named sibling")
	err = t130RegisterOffice(t, home, sibling)
	t130Refused(t, err, t130OfficeText(filepath.Clean(sibling), own), home, component.KindOffice)

	err = t130RegisterOffice(t, home, own)
	t130Refused(t, err, t130OfficeText(filepath.Clean(own), own), home, component.KindOffice)
}

// TestT130_CheckRunsBeforeAnythingInsideIsRead — OF-3 (f): an outside
// fixture whose folder is mode 000 still gets the OF-3 text, not a layout,
// version or read error. Runs as a non-root user; as root the lock cannot
// hold and the row fails loudly.
func TestT130_CheckRunsBeforeAnythingInsideIsRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: T-130(f) runs as a non-root user — required by office-preview spec §3.9 T-130; as root a mode-000 folder is still readable and check-first cannot be observed")
	}
	own := t130OwnFolder(t)
	base := t130BaseDir(t)
	home := t.TempDir()
	locked := filepath.Join(base, "elsewhere", "locked")
	t130OfficeFixture(t, locked)
	t130Outside(t, locked, own, "the locked fixture")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	err := t130RegisterOffice(t, home, locked)
	t130Refused(t, err, t130OfficeText(filepath.Clean(locked), own), home, component.KindOffice)
}

// TestT130_MissingOwnFolderRefusesEverything — OF-3 (g): when OWN does not
// exist, every registration is refused with the OF-3 text naming it.
func TestT130_MissingOwnFolderRefusesEverything(t *testing.T) {
	own := t130OwnFolder(t)
	base := t130BaseDir(t)
	home := t.TempDir()
	office := filepath.Join(base, "elsewhere", "no-own", "libreoffice")
	t130OfficeFixture(t, office)
	require.NoError(t, os.RemoveAll(own))
	t.Cleanup(func() { _ = os.MkdirAll(own, 0o755) })
	err := t130RegisterOffice(t, home, office)
	t130Refused(t, err, t130OfficeText(filepath.Clean(office), own), home, component.KindOffice)
}

// TestT130_NonLinuxModelsRegisterAsBefore — OF-3 (h): the same outside
// folders register as today when the registration's GOOS is not Linux, each
// with the layout that GOOS expects (guard; passes before and after).
func TestT130_NonLinuxModelsRegisterAsBefore(t *testing.T) {
	own := t130OwnFolder(t)
	// The guard fixtures cannot live under an agent-writable folder (the
	// writer exclusion would refuse them for a different reason); the
	// package work directory is outside both OWN and every writable root.
	work, workErr := os.Getwd()
	require.NoError(t, workErr)
	staging, stageErr := os.MkdirTemp(work, "t130-h-")
	require.NoError(t, stageErr)
	t.Cleanup(func() { _ = os.RemoveAll(staging) })

	t.Run("darwin", func(t *testing.T) {
		office := filepath.Join(staging, "Applications", "LibreOffice.app")
		program := filepath.Join(office, "Contents", "MacOS")
		require.NoError(t, os.MkdirAll(program, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
		product := `<?xml version="1.0"?><oor:data xmlns:oor="http://openoffice.org/2001/registry"><oor:component-data oor:name="Setup" oor:package="org.openoffice"><node oor:name="Product"><prop oor:name="ooSetupVersionAboutBox"><value>26.8.1.1</value></prop></node></oor:component-data></oor:data>`
		plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>26.8.1.1</string></dict></plist>`
		require.NoError(t, os.WriteFile(filepath.Join(office, "Contents", "Info.plist"), []byte(plist), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(office, "Contents", "Resources", "services"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(office, "Contents", "Resources", "services", "services.rdb"), []byte(`<?xml version="1.0"?>
<components xmlns="http://openoffice.org/2010/uno-components"><component loader="com.sun.star.loader.SharedLibrary" environment="gcc3" uri="vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.so"><implementation name="com.sun.star.chart2.ExponentialScaling" constructor="com_sun_star_chart2_ExponentialScaling_get_implementation"><service name="com.sun.star.chart2.ExponentialScaling"/>
</implementation></component></components>`), 0o600))
		registry := filepath.Join(office, "Contents", "Resources", "registry")
		require.NoError(t, os.MkdirAll(registry, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(registry, "main.xcd"), []byte(product), 0o600))
		t130Outside(t, office, own, "the darwin fixture")
		home := t.TempDir()
		manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: office, Operator: "t130", GOOS: "darwin"})
		require.NoError(t, err, "T-130(h): darwin registration is unchanged")
		require.NotNil(t, manifest)
		require.FileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"),
			"T-130(h): the darwin registration wrote its manifest")
	})
	t.Run("windows", func(t *testing.T) {
		office := filepath.Join(staging, "windows", "LibreOffice")
		program := filepath.Join(office, "program")
		require.NoError(t, os.MkdirAll(program, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.exe"), []byte("fixture program, never executed"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(program, "version.ini"), []byte("[Bootstrap]\nbuildid=fixture\nProductVersion=26.8.1.1\n"), 0o600))
		t130Outside(t, office, own, "the windows fixture")
		home := t.TempDir()
		manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: office, Operator: "t130", GOOS: "windows"})
		require.NoError(t, err, "T-130(h): windows registration is unchanged")
		require.NotNil(t, manifest)
		require.FileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"),
			"T-130(h): the windows registration wrote its manifest")
	})
}

// TestT130_FinalRootNormalizationToOwnRefused — OF-3 (b), gate round 2
// TA-02: a valid LibreOffice staged directly AT Omnipus's own folder and
// registered through its program folder. The supplied folder is strictly
// inside OWN, so the first OF-3 check passes; the layout normalization steps
// from program to its parent, making the FINAL recorded root OWN itself —
// and a root counts as inside only strictly, so OF-3(b) must refuse it. The
// control row registers the same program-folder shape with the component one
// level deeper, which normalizes to a proper descendant and is accepted:
// only the final-root check can explain the subject's refusal.
func TestT130_FinalRootNormalizationToOwnRefused(t *testing.T) {
	own := t130OwnFolder(t)

	// Control: the accepted shape (component one level below OWN, folder
	// given as its program).
	controlHome := t.TempDir()
	control := filepath.Join(own, "libreoffice-controlled")
	t130OfficeFixture(t, control)
	manifest, ctrlErr := component.RegisterOffice(component.RegisterOptions{
		Home: controlHome, Folder: filepath.Join(control, "program"), Operator: "t130-ta02", GOOS: "linux",
	})
	require.NoError(t, ctrlErr,
		"TA-02 control: the program folder of a component one level below OWN normalizes to a descendant root and is accepted")
	require.NotNil(t, manifest)
	require.Equal(t, filepath.Clean(control), filepath.Clean(manifest.Root),
		"TA-02 control: the recorded root is the component folder, not the program folder")

	// Subject: the component sits AT OWN itself. The supplied program folder
	// is strictly inside OWN (precondition), so the first check passes and
	// the recorded root would be OWN — which is not strictly inside itself.
	subjectHome := t.TempDir()
	t130OfficeFixture(t, own)
	program := filepath.Join(own, "program")
	given, absErr := filepath.Abs(program)
	require.NoError(t, absErr)
	inside := given == own || (len(given) > len(own) && given[:len(own)] == own && given[len(own)] == filepath.Separator)
	require.True(t, inside, "TA-02 precondition: the supplied folder %s is strictly inside OWN %s, so only the final-root check can refuse it", given, own)
	err := t130RegisterOffice(t, subjectHome, program)
	require.EqualError(t, err, t130OfficeText(given, own),
		"TA-02: the final root after program normalization is OWN itself and OF-3(b) must refuse it")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(subjectHome, component.KindOffice), "manifest.json"),
		"TA-02: a refused registration writes nothing")
}

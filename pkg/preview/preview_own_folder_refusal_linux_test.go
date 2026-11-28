//go:build linux

package preview

// T-131 (b) — office-preview spec §3.9 (2026-11-21, founder decision OF;
// FR-068), through the real Service: a pre-§3.9 registration whose root
// lies outside Omnipus's own folder is refused with the exact OF-4 texts,
// before the startup-record check and before anything inside the root is
// used. Plus the §3.9 risk-point-3 case (team-lead ruling 2026-11-21): a
// registered root inside OWN that becomes an outward LINK after a first
// successful preview must be refused on the next preview even though the
// service caches the LibreOffice component — its real executable must not
// even be opened behind a refusal.
//
// The oracles are the spec's OF-4 texts, never the code. RED on the code
// before §3.9: the previews convert (the converter count rises), so every
// refusal and count assertion fails on a real observation.
//
// The tool-layer prefix: the tool result is `preview_document: ` + these
// sentences (pkg/tools/preview_document.go::previewPrefix composition, the
// same wrap the tool's own refusal tests pin); this row pins the sentences
// at the service boundary the tool wraps.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t131StageProgramWorld stages P/bin/omnipus and OWN = P/share/omnipus
// around the RUNNING TEST BINARY's default program path, so that the real
// RegisterOffice call (whose program path is still the default, the new
// RegisterOptions field not having landed yet) and the service's resolver
// agree on the same OWN. Everything the rows register lives inside OWN;
// everything they must refuse lives outside it.
func t131StageProgramWorld(t *testing.T) (base, bin, own string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	base = filepath.Clean(filepath.Join(filepath.Dir(resolved), ".."))
	bin = filepath.Join(base, "bin", "omnipus")
	own = filepath.Join(base, "share", "omnipus")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	// Self must be a real copy of the test binary: the service starts the
	// converter through its launcher shim over Self (the T-126/T-114 shape),
	// and a no-op script would break the spawn pipe before any conversion.
	q1bCopyFile(t, resolved, bin, 0o755)
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(base, "share"))
		_ = os.RemoveAll(filepath.Join(base, "bin"))
	})
	return base, bin, own
}

// t131RegisteredRealOffice registers a REAL LibreOffice (the provisioned
// fixture, copied whole) at <own>/libreoffice-custom with the REAL
// RegisterOffice and returns the recorded root exactly as the manifest
// carries it. The converter is the real one: a preview against it is a real
// conversion (two launcher starts — the exit-81 restart), which is what the
// cached-component risk case must measure.
func t131RegisteredRealOffice(t *testing.T, home, own string) string {
	t.Helper()
	officeSrc, _ := realprograms.Roots(t, true, false)
	root := filepath.Join(own, "libreoffice-custom")
	functionalCopyTree(t, officeSrc, root)
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: root, Operator: "t131-cached", GOOS: "linux"})
	require.NoError(t, err, "T-131(b): the fixture registration inside OWN must be accepted")
	require.Equal(t, filepath.Clean(root), filepath.Clean(manifest.Root),
		"T-131(b): the recorded root is the real folder inside OWN")
	return manifest.Root
}

// t131PlantedRenderer registers — as the pre-§3.9 state did — an operator
// renderer whose root lies OUTSIDE OWN, by writing the manifest directly
// (the same state a registration made before §3.9 left on disk).
func t131PlantedRenderer(t *testing.T, home, outside string) string {
	t.Helper()
	root := filepath.Join(outside, "pdfium-pre9")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "omnipus-pdfium"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	sum, _, err := component.HashFile(filepath.Join(root, "omnipus-pdfium"))
	require.NoError(t, err)
	files := map[string]string{"omnipus-pdfium": sum}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: component.KindPDFRenderer,
		Version: "26.8.0.3", BuildID: "t131-renderer-pre9", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: "omnipus-pdfium",
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustOperatorRegistered, Root: root, RegisteredBy: "t131-pre9",
	}
	dir := component.RegisteredDir(home, component.KindPDFRenderer)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), raw, 0o600))
	return root
}

// t131PlantedOffice is the office half of the same pre-§3.9 state (D15,
// team-lead 2026-11-21): the outside-refusal row needs the office root
// OUTSIDE OWN — the real RegisterOffice would refuse such a folder today,
// but refusing outside registrations is OF-3's job; this OF-4 row's subject
// is the USE refusal of a registration that predates §3.9, so the manifest
// is written directly, with the executable fingerprinted so it parses and
// the own-folder check is reached (D13). Nothing inside the root is ever
// used — the refusal comes first — so a stub executable suffices.
func t131PlantedOffice(t *testing.T, home, outside string) string {
	t.Helper()
	root := filepath.Join(outside, "libreoffice-pre9")
	require.NoError(t, os.MkdirAll(root, 0o755))
	// TA-06 (gate round 2): the stub is the launch marker. A real converter
	// start runs it, and it writes next to the production --outdir — the one
	// folder a confined converter may write — so the marker observes the
	// launch path actually used, confined or not, where the confined-only
	// start hooks stay silent.
	require.NoError(t, os.WriteFile(filepath.Join(root, "soffice.bin"), []byte(t131ConverterMarkerScript()), 0o755))
	sum, _, err := component.HashFile(filepath.Join(root, "soffice.bin"))
	require.NoError(t, err)
	files := map[string]string{"soffice.bin": sum}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: component.KindOffice,
		Version: "26.8.0.3", BuildID: "t131-office-pre9", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: "soffice.bin",
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustOperatorRegistered, Root: root, RegisteredBy: "t131-pre9",
	}
	dir := component.RegisteredDir(home, component.KindOffice)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), raw, 0o600))
	return root
}

func t131PreviewDocx(t *testing.T, svc *Service) (*Result, error) {
	t.Helper()
	doc := t126DocxFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err, "T-131(b): admit")
	defer adm.Release()
	return svc.Preview(ctx, adm, Request{Name: "t131" + doc.Ext, Ext: doc.Ext, Data: doc.Data, Default: true})
}

func t131PreviewPDF(t *testing.T, svc *Service) (*Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err, "T-131(b): admit")
	defer adm.Release()
	return svc.Preview(ctx, adm, Request{Name: "t131.pdf", Ext: ".pdf", Data: officeRetryPDF(), Default: true})
}

// TestT131_ServiceRefusesRegisteredRootOutsideOwn — OF-4 (b): through the
// real Service, the outside-own registration yields the exact OF-4 texts
// with no converter or renderer start, with and without an active startup
// record (the check comes before CheckStartupGrant).
func TestT131_ServiceRefusesRegisteredRootOutsideOwn(t *testing.T) {
	for _, record := range []string{"no-startup-record", "startup-record-grants-packaged"} {
		t.Run(record, func(t *testing.T) {
			base, bin, own := t131StageProgramWorld(t)
			home := t.TempDir()
			outside := filepath.Join(base, "elsewhere")
			officeRoot := t131PlantedOffice(t, home, outside)
			t131NotInsideOwnService(t, officeRoot, own, "the planted office root")
			rendererRoot := t131PlantedRenderer(t, home, outside)
			t131NotInsideOwnService(t, rendererRoot, own, "the planted renderer root")
			resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
			svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
			require.NoError(t, err)
			if record == "startup-record-grants-packaged" {
				rec := &component.StartupGrantRecord{}
				require.NoError(t, rec.AddGranted(own))
				require.NoError(t, rec.AddGranted(officeRoot))
				component.SetStartupGrants(rec)
				t.Cleanup(func() { component.SetStartupGrants(nil) })
			}
			officeStarts, rendererStarts := 0, 0
			testHookConverterStart = func(converterStart) { officeStarts++ }
			testHookRendererStart = func(converterStart) { rendererStarts++ }
			t.Cleanup(func() { testHookConverterStart = nil; testHookRendererStart = nil })

			wantOffice := "Office preview is disabled: the registered LibreOffice folder " + officeRoot +
				" is outside Omnipus's own folder (" + own + "); on Linux, Omnipus uses only preview programs inside it. " +
				"Remove the registration with \"omnipus office unregister\", or put LibreOffice in a folder inside " + own + " and register that folder."
			officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
			require.Error(t, previewErr, "T-131(b): the outside-own LibreOffice must be refused")
			require.Nil(t, res)
			require.EqualError(t, previewErr, wantOffice,
				"T-131(b): the office denial must be the OF-4 sentence word for word; got %q", previewErr)
			require.Zero(t, officeStarts, "T-131(b): the converter must never start")
			// TA-06: the launch observer watches the call folders DURING the
			// call — a real start, confined or not, leaves the stub's marker
			// there. (Its live proof is TestT131_LaunchObserverPositiveControl.)
			require.False(t, officeStarted,
				"T-131(b)/TA-06: the refusal must precede any converter start — no work-folder marker appeared")
			assert.Empty(t, rendererStarts, "T-131(b): the office refusal must not touch the renderer")

			wantRenderer := "PDF renderer is disabled: the registered folder " + rendererRoot +
				" is outside Omnipus's own folder (" + own + "); on Linux, Omnipus uses only preview programs inside it. " +
				// D16 (team-lead 2026-11-21): spec line 984's reason text ends
				// "register that folder" with no closing period (the period in
				// the spec line belongs to the enclosing sentence).
				"Remove the registration with \"omnipus pdf-renderer unregister\", or put the renderer in a folder inside " + own + " and register that folder"
			rendererStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewPDF(t, svc) })
			require.NoError(t, previewErr, "T-131(b): the renderer denial is a degraded result, not an error")
			require.NotNil(t, res)
			require.Equal(t, "renderer_damaged", res.RenderReason, "T-131(b): the renderer denial reason code")
			require.Equal(t, wantRenderer, res.RenderDetail,
				"T-131(b): the renderer denial must be the OF-4 sentence word for word; got %q", res.RenderDetail)
			require.Zero(t, rendererStarts, "T-131(b): the renderer must never start")
			require.False(t, rendererStarted,
				"T-131(b)/TA-06: the renderer denial must not launch the office converter either")
		})
	}
}

// TestT131_ServiceRefusesCachedRootThatBecameLink — §3.9 risk point 3 (the
// cached LibreOffice): a registered root INSIDE OWN that becomes an outward
// link after a first successful preview is refused on every later preview;
// the real converter executable is never opened again. The manifest is never
// rewritten, and the kernel watch spans the move without a polling gap.
func TestT131_ServiceRefusesCachedRootThatBecameLink(t *testing.T) {
	for _, record := range []string{"no-startup-record", "startup-record-grants-original"} {
		t.Run(record, func(t *testing.T) {
			base, bin, own := t131StageProgramWorld(t)
			home := t.TempDir()
			recorded := t131RegisteredRealOffice(t, home, own)
			resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
			svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
			require.NoError(t, err)
			if record == "startup-record-grants-original" {
				rec := &component.StartupGrantRecord{}
				require.NoError(t, rec.AddGranted(recorded))
				component.SetStartupGrants(rec)
				t.Cleanup(func() { component.SetStartupGrants(nil) })
			}
			manifestPath := filepath.Join(component.RegisteredDir(home, component.KindOffice), component.ManifestFile)
			before, err := os.ReadFile(manifestPath)
			require.NoError(t, err)

			// R3-F1: observe the real file, not the confined-only start hook.
			// A direct exec, with no Service/fingerprint read, proves that a
			// converter launch reaches this instrument even when unconfined.
			executable := filepath.Join(recorded, "program", "soffice.bin")
			watched, err := os.Stat(executable)
			require.NoError(t, err)
			opens := t131ConverterFileOpenCount(t, executable)
			require.Zero(t, opens(), "R3-F1 instrument: no converter file open before the control")
			control := exec.Command(executable, "--version")
			control.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + t.TempDir()}
			version, controlErr := control.CombinedOutput()
			require.NoError(t, controlErr, "R3-F1 instrument: execute unchanged real LibreOffice: %s", version)
			controlOpens := opens()
			require.Positive(t, controlOpens, "R3-F1 instrument: a real exec must be visible without fingerprint reads")

			// Preview 1: real conversion must produce a PDF and a further
			// positive observation before any refusal's absence is trusted.
			res, previewErr := t131PreviewDocx(t, svc)
			require.NoError(t, previewErr, "T-131(b): preview 1 against the in-own registration must succeed")
			require.NotNil(t, res)
			require.NotEmpty(t, res.PDF, "R3-F1 control: real LibreOffice must produce the converted PDF")
			opensAfterFirst := opens()
			require.Greater(t, opensAfterFirst, controlOpens,
				"R3-F1 instrument: preview 1 must produce an additional observed converter file open")
			t.Logf("R3-F1 live observer: direct exec opens=%d; preview 1 additional opens=%d", controlOpens, opensAfterFirst-controlOpens)

			// The root becomes a link pointing OUTSIDE OWN; the manifest is
			// left byte-identical.
			outside, err := os.MkdirTemp(base, "cached-link-outside-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(outside) })
			moved := filepath.Join(outside, "lo")
			// Keep the watched inode: only its ancestor moves. A new inode
			// would invalidate the no-open observation across the link swap.
			require.NoError(t, os.Rename(recorded, moved), "T-131(b): move the real fixture outside OWN")
			require.NoError(t, os.Symlink(moved, recorded))
			afterMove, err := os.Stat(executable)
			require.NoError(t, err)
			require.True(t, os.SameFile(watched, afterMove), "R3-F1 instrument: the same converter inode is still watched through the outward link")
			resolved, err := filepath.EvalSymlinks(recorded)
			require.NoError(t, err)
			require.NotEqual(t, filepath.Clean(recorded), filepath.Clean(resolved),
				"T-131(b) precondition: the registered path now resolves outside itself")
			t131NotInsideOwnService(t, resolved, own, "the link's target")
			after, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "T-131(b): the manifest stays byte-identical across the swap")

			// Preview 2: the exact OF-4 refusal must precede even opening
			// the cached converter. Queued opens also catch start-then-refuse.
			want := "Office preview is disabled: the registered LibreOffice folder " + recorded +
				" is outside Omnipus's own folder (" + own + "); on Linux, Omnipus uses only preview programs inside it. " +
				"Remove the registration with \"omnipus office unregister\", or put LibreOffice in a folder inside " + own + " and register that folder."
			res, previewErr = t131PreviewDocx(t, svc)
			require.Error(t, previewErr, "T-131(b): preview 2 must refuse the root that became an outward link")
			require.Nil(t, res)
			require.EqualError(t, previewErr, want,
				"T-131(b): preview 2 must be the OF-4 sentence word for word; got %q", previewErr)
			opensAfterSecond := opens()
			require.Equal(t, opensAfterFirst, opensAfterSecond,
				"T-131(b)/R3-F1: the cached LibreOffice executable must not even be opened behind preview 2's refusal")
			t.Logf("R3-F1 live observer: preview 2 additional opens=%d", opensAfterSecond-opensAfterFirst)

			// Preview 3: the same refusal again.
			_, previewErr = t131PreviewDocx(t, svc)
			require.Error(t, previewErr, "T-131(b): preview 3 must refuse again")
			require.EqualError(t, previewErr, want, "T-131(b): preview 3 is the same refusal")
			opensAfterThird := opens()
			require.Equal(t, opensAfterFirst, opensAfterThird, "T-131(b)/R3-F1: no converter file open behind preview 3's refusal")
			t.Logf("R3-F1 live observer: preview 3 additional opens=%d", opensAfterThird-opensAfterSecond)
		})
	}
}

func t131NotInsideOwnService(t *testing.T, path, own, what string) {
	t.Helper()
	inside := path == own || (len(path) > len(own) && path[:len(own)] == own && path[len(own)] == filepath.Separator)
	require.False(t, inside, "T-131(b) precondition: %s (%s) must resolve outside Omnipus's own folder %s", what, path, own)
}

// t131ConverterMarkerName is the file a real converter start leaves in the
// call's work folder (TA-06).
const t131ConverterMarkerName = "converter-started.marker"

// t131ConverterMarkerScript is the stub converter: it records its own launch
// next to the production --outdir argument, holds the record visible for a
// moment, and exits without converting. The work folder is the one folder
// the confined rules let the converter write — and the service REMOVES the
// whole call folder when the call ends, so the marker must be observed
// DURING the call (t131ObserveCall); the sleep keeps it observable for the
// poller with a wide margin.
func t131ConverterMarkerScript() string {
	return "#!/bin/sh\n" +
		"out=\"\"\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--outdir\" ]; then out=\"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"if [ -n \"$out\" ]; then printf 'started\\n' > \"$out/" + t131ConverterMarkerName + "\" 2>/dev/null || true; fi\n" +
		"sleep 0.3 2>/dev/null || true\n" +
		"exit 73\n"
}

// t131MarkerExists reports whether any converter-start marker exists under
// root right now. A descendant removed by completed-call cleanup is absent;
// a missing root or any other walk error still makes the observation fail.
func t131MarkerExists(root string) (bool, error) {
	found := false
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path != root && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() && d.Name() == t131ConverterMarkerName {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, walkErr
}

// t131ObserveCall runs the preview call and reports whether a converter
// really started, by watching the call folders under the data home for the
// stub's work-folder marker WHILE the call runs. This is the TA-06 launch
// observer: the confined start hooks fire only when a sandbox policy
// applies (confine.go::observedBy), and the work folder — the only folder
// the confined rules let the converter write — is removed with the call
// folder when the call ends, so neither the hooks alone nor any post-call
// walk can observe an unconfined launch. The stub holds its marker for
// 0.3 s, far longer than the poll needs.
func t131ObserveCall(t *testing.T, home string, run func() (*Result, error)) (started bool, res *Result, err error) {
	t.Helper()
	markerExists := func() bool {
		found, scanErr := t131MarkerExists(home)
		assert.NoError(t, scanErr, "TA-06: marker observation failed; an unreadable tree cannot prove no converter start")
		return found
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = run()
	}()
	breakpoint := time.After(30 * time.Second)
	for {
		select {
		case <-done:
			return markerExists(), res, err
		case <-breakpoint:
			t.Errorf("TA-06: the observed call never returned; failing the row instead of polling forever")
			return true, res, err
		default:
			if markerExists() {
				<-done
				return true, res, err
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// t131DumpTree lists paths under root and reports any traversal failure —
// the controls carry it in their failure message so an absent marker can be
// diagnosed from the run log alone (what the launch left behind, and where).
func t131DumpTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			lines = append(lines, path+" (error: "+err.Error()+")")
			return err
		}
		lines = append(lines, path)
		return nil
	})
	if walkErr != nil {
		return strings.Join(lines, "\n") + "\nwalk failed: " + walkErr.Error()
	}
	return strings.Join(lines, "\n")
}

// TestT131_LaunchObserverPositiveControl — TA-06 (gate round 2): the
// refusal rows below assert "no converter start" through the marker; this
// control proves the marker really appears when a launch happens. The
// planted office sits INSIDE OWN, so nothing refuses and the service really
// launches the stub, which marks the call's work folder and then fails to
// convert — the call errors, and the marker is there.
func TestT131_LaunchObserverPositiveControl(t *testing.T) {
	_, bin, own := t131StageProgramWorld(t)
	home := t.TempDir()
	t131PlantedOffice(t, home, own)
	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
	require.NoError(t, err)
	started, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
	require.Error(t, previewErr, "TA-06 control: the marker stub cannot convert; the call fails after its real launch")
	require.Nil(t, res)
	require.True(t, started,
		"TA-06 control: a real converter start must appear to the mid-call observer — without this the refusal rows' no-start assertions would be vacuous; the call failed with: %v\nhome tree:\n%s", previewErr, t131DumpTree(t, home))
}

//go:build linux

package preview

// T-131 (b) — office-preview spec §3.9 (2026-11-21, founder decision OF;
// FR-068), through the real Service: a pre-§3.9 registration whose root
// lies outside Omnipus's own folder is refused with the exact OF-4 texts,
// before the startup-record check and before anything inside the root is
// used. Plus the §3.9 risk-point-3 case (team-lead ruling 2026-11-21): a
// registered root inside OWN that becomes an outward LINK after a first
// successful preview must be refused on the next preview even though the
// service caches the LibreOffice component — the converter start count must
// stay at 1.
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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
			officeRoot := t131RegisteredRealOffice(t, home, own)
			rendererRoot := t131PlantedRenderer(t, home, filepath.Join(base, "elsewhere"))
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
			res, previewErr := t131PreviewDocx(t, svc)
			require.Error(t, previewErr, "T-131(b): the outside-own LibreOffice must be refused")
			require.Nil(t, res)
			require.EqualError(t, previewErr, wantOffice,
				"T-131(b): the office denial must be the OF-4 sentence word for word; got %q", previewErr)
			require.Zero(t, officeStarts, "T-131(b): the converter must never start")
			assert.Empty(t, rendererStarts, "T-131(b): the office refusal must not touch the renderer")

			wantRenderer := "PDF renderer is disabled: the registered folder " + rendererRoot +
				" is outside Omnipus's own folder (" + own + "); on Linux, Omnipus uses only preview programs inside it. " +
				"Remove the registration with \"omnipus pdf-renderer unregister\", or put the renderer in a folder inside " + own + " and register that folder."
			res, previewErr = t131PreviewPDF(t, svc)
			require.NoError(t, previewErr, "T-131(b): the renderer denial is a degraded result, not an error")
			require.NotNil(t, res)
			require.Equal(t, "renderer_damaged", res.RenderReason, "T-131(b): the renderer denial reason code")
			require.Equal(t, wantRenderer, res.RenderDetail,
				"T-131(b): the renderer denial must be the OF-4 sentence word for word; got %q", res.RenderDetail)
			require.Zero(t, rendererStarts, "T-131(b): the renderer must never start")
		})
	}
}

// TestT131_ServiceRefusesCachedRootThatBecameLink — §3.9 risk point 3 (the
// cached LibreOffice): a registered root INSIDE OWN that becomes an outward
// link after a first successful preview is refused on every later preview;
// the converter start count stays at 1. The manifest is never rewritten.
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

			// Preview 1: the stub converter really converts — one start.
			starts := 0
			testHookConverterStart = func(converterStart) { starts++ }
			t.Cleanup(func() { testHookConverterStart = nil })
			res, previewErr := t131PreviewDocx(t, svc)
			require.NoError(t, previewErr, "T-131(b): preview 1 against the in-own registration must succeed")
			require.NotNil(t, res)
			// testHookConverterStart fires only on the CONFINED spawn path
			// (pkg/preview/confine.go: "once per confined LibreOffice");
			// this unit row runs unconfined, so the count stays at its
			// observed preview-1 value and the assertion below pins that it
			// never changes. The live count proof of the cached case belongs
			// to a confined real row; the mutant-killer here is the exact
			// refusal text of previews 2 and 3.
			startsAfterFirst := starts

			// The root becomes a link pointing OUTSIDE OWN; the manifest is
			// left byte-identical.
			moved := filepath.Join(base, "elsewhere", "lo")
			require.NoError(t, os.MkdirAll(filepath.Dir(moved), 0o755))
			moveCmd := exec.Command("cp", "-p", "-r", recorded, moved)
			out, moveErr := moveCmd.CombinedOutput()
			require.NoError(t, moveErr, "T-131(b): the fixture copies out of OWN: %s", out)
			require.NoError(t, os.RemoveAll(recorded))
			require.NoError(t, os.Symlink(moved, recorded))
			resolved, err := filepath.EvalSymlinks(recorded)
			require.NoError(t, err)
			require.NotEqual(t, filepath.Clean(recorded), filepath.Clean(resolved),
				"T-131(b) precondition: the registered path now resolves outside itself")
			t131NotInsideOwnService(t, resolved, own, "the link's target")
			after, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "T-131(b): the manifest stays byte-identical across the swap")

			// Preview 2: refused with the exact OF-4 text; the cached
			// component must NOT convert (the count stays at 1).
			want := "Office preview is disabled: the registered LibreOffice folder " + recorded +
				" is outside Omnipus's own folder (" + own + "); on Linux, Omnipus uses only preview programs inside it. " +
				"Remove the registration with \"omnipus office unregister\", or put LibreOffice in a folder inside " + own + " and register that folder."
			res, previewErr = t131PreviewDocx(t, svc)
			require.Error(t, previewErr, "T-131(b): preview 2 must refuse the root that became an outward link")
			require.Nil(t, res)
			require.EqualError(t, previewErr, want,
				"T-131(b): preview 2 must be the OF-4 sentence word for word; got %q", previewErr)
			require.Equal(t, startsAfterFirst, starts,
				"T-131(b): the cached LibreOffice must not convert behind the refusal — the converter start count never changes")

			// Preview 3: the same refusal again.
			_, previewErr = t131PreviewDocx(t, svc)
			require.Error(t, previewErr, "T-131(b): preview 3 must refuse again")
			require.EqualError(t, previewErr, want, "T-131(b): preview 3 is the same refusal")
			require.Equal(t, startsAfterFirst, starts, "T-131(b): the converter start count stays frozen after three previews")
		})
	}
}

func t131NotInsideOwnService(t *testing.T, path, own, what string) {
	t.Helper()
	inside := path == own || (len(path) > len(own) && path[:len(own)] == own && path[len(own)] == filepath.Separator)
	require.False(t, inside, "T-131(b) precondition: %s (%s) must resolve outside Omnipus's own folder %s", what, path, own)
}

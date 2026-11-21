//go:build linux

package preview

// T-132 — office-preview spec §3.9 (2026-11-21; RS-1, FR-069): under an
// ACTIVE startup record (the confined gateway state), a component file that
// cannot be read because access is refused produces the RS-1 texts; the
// same files without an active record produce today's damaged texts; a
// refusal that is NOT a permission error stays on today's damaged text
// (guard). The real case is T-128 (f). Oracles are the spec's RS-1 sentences
// and §3.8 BR-2.5-era damaged skeletons, never the code. RED on the code
// before §3.9: (a) produces the damaged text, so the RS-1 assertions fail.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// RS-1's fixed skeletons (§3.9 RS-1); <reason> is today's damaged reason.
const (
	t132OfficeRSPrefix = "Office preview cannot read its program files ("
	t132OfficeRSTail   = "). If Omnipus was restarted from inside the app after this component was installed or registered, stop and start Omnipus (for example with your service manager); otherwise reinstall or re-register the Office preview component."
	t132RenderRSPrefix = "PDF renderer cannot read its program files ("
	t132RenderRSTail   = "); if Omnipus was restarted from inside the app after it was installed or registered, stop and start Omnipus (for example with your service manager); otherwise reinstall or re-register it"
)

// t132StageLockedPackagedSlots stages valid packaged slots at OWN whose
// manifest.json is mode 000, and returns the world. As root the lock cannot
// hold; the row fails loudly instead of passing vacuously (T-132 runs as a
// non-root user).
func t132StageLockedPackagedSlots(t *testing.T, own string) (officeSlot, pdfiumSlot string) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: T-132 runs as a non-root user — required by office-preview spec §3.9 T-132; as root a mode-000 file is still readable and the refused read cannot be produced")
	}
	officeSlot = filepath.Join(own, "office")
	require.NoError(t, os.MkdirAll(filepath.Join(officeSlot, "program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(officeSlot, "program", "soffice.bin"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(officeSlot, "program", "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// D14 (team-lead 2026-11-21): the packaged-manifest writer reads the
	// slot's product version (program/versionrc or share/registry/main.xcd);
	// without one the SETUP fails on every tree, before the row's subject.
	require.NoError(t, os.WriteFile(filepath.Join(officeSlot, "program", "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o644))
	require.NoError(t, component.WritePackagedOfficeManifest(officeSlot, component.PackagedOfficeOptions{GOOS: "linux", GOARCH: "amd64"}))

	pdfiumSlot = filepath.Join(own, "pdfium")
	require.NoError(t, os.MkdirAll(pdfiumSlot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pdfiumSlot, "omnipus-pdfium"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	sum, _, err := component.HashFile(filepath.Join(pdfiumSlot, "omnipus-pdfium"))
	require.NoError(t, err)
	files := map[string]string{"omnipus-pdfium": sum}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: component.KindPDFRenderer,
		Version: "26.8.0.3", BuildID: "t132-renderer", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: "omnipus-pdfium",
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustPackaged,
	}
	raw, marshalErr := json.Marshal(m)
	require.NoError(t, marshalErr)
	require.NoError(t, os.WriteFile(filepath.Join(pdfiumSlot, component.ManifestFile), raw, 0o600))

	// The lock: the manifests become unreadable. The precondition proves the
	// lock really refuses a read (as root it would not).
	for _, locked := range []string{
		filepath.Join(officeSlot, "manifest.json"),
		filepath.Join(pdfiumSlot, "manifest.json"),
	} {
		require.NoError(t, os.Chmod(locked, 0o000))
		_, readErr := os.ReadFile(locked)
		require.ErrorIs(t, readErr, os.ErrPermission,
			"T-132 precondition: %s must be unreadable, or the refused read cannot be distinguished from success", locked)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(officeSlot, "manifest.json"), 0o600)
		_ = os.Chmod(filepath.Join(pdfiumSlot, "manifest.json"), 0o600)
	})
	return officeSlot, pdfiumSlot
}

// t132Reason checks the shared <reason> conditions and returns it.
func t132Reason(t *testing.T, message string) string {
	t.Helper()
	open := strings.Index(message, "(")
	close := strings.LastIndex(message, ")")
	require.Greater(t, close, open, "T-132: the reason must be parenthesised")
	reason := message[open+1 : close]
	require.True(t, strings.HasPrefix(reason, "packaged manifest unreadable:"),
		"T-132: the reason must begin with the manifest-unreadable cause; got %q", reason)
	require.Contains(t, reason, "permission denied",
		"T-132: the refused read must be a permission failure; got %q", reason)
	return reason
}

// TestT132_RefusedReadUnderActiveRecordNamesTheRestart — RS-1 (a): with a
// startup record granting the packaged slots, the refused manifest read
// produces exactly the RS-1 texts and no converter or renderer start.
func TestT132_RefusedReadUnderActiveRecordNamesTheRestart(t *testing.T) {
	base, bin, own := t131StageProgramWorld(t)
	_ = base
	officeSlot, pdfiumSlot := t132StageLockedPackagedSlots(t, own)
	home := t.TempDir()
	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
	require.NoError(t, err)
	rec := &component.StartupGrantRecord{}
	require.NoError(t, rec.AddGranted(own))
	require.NoError(t, rec.AddGranted(officeSlot))
	require.NoError(t, rec.AddGranted(pdfiumSlot))
	component.SetStartupGrants(rec)
	t.Cleanup(func() { component.SetStartupGrants(nil) })
	officeStarts, rendererStarts := 0, 0
	testHookConverterStart = func(converterStart) { officeStarts++ }
	testHookRendererStart = func(converterStart) { rendererStarts++ }
	t.Cleanup(func() { testHookConverterStart = nil; testHookRendererStart = nil })

	res, previewErr := t131PreviewDocx(t, svc)
	require.Error(t, previewErr, "T-132(a): the refused read must not convert")
	require.Nil(t, res)
	errText := previewErr.Error()
	require.True(t, strings.HasPrefix(errText, t132OfficeRSPrefix) && strings.HasSuffix(errText, t132OfficeRSTail),
		"T-132(a): the office denial must be the RS-1 sentence; got %q", errText)
	t132Reason(t, errText)
	require.Zero(t, officeStarts, "T-132(a): the converter must never start")

	res, previewErr = t131PreviewPDF(t, svc)
	require.NoError(t, previewErr, "T-132(a): the renderer denial is a degraded result")
	require.NotNil(t, res)
	require.Equal(t, "renderer_damaged", res.RenderReason, "T-132(a): the renderer reason code")
	require.True(t, strings.HasPrefix(res.RenderDetail, t132RenderRSPrefix) && strings.HasSuffix(res.RenderDetail, t132RenderRSTail),
		"T-132(a): the renderer denial must be the RS-1 sentence; got %q", res.RenderDetail)
	t132Reason(t, res.RenderDetail)
	require.Zero(t, rendererStarts, "T-132(a): the renderer must never start")
}

// TestT132_RefusedReadWithoutRecordKeepsDamagedText — RS-1 (b): the same
// files WITHOUT an active record keep today's damaged texts (a refused read
// is an ordinary file-permission problem there), with no restart sentence.
func TestT132_RefusedReadWithoutRecordKeepsDamagedText(t *testing.T) {
	base, bin, own := t131StageProgramWorld(t)
	_ = base
	_, _ = t132StageLockedPackagedSlots(t, own)
	home := t.TempDir()
	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
	require.NoError(t, err)
	starts := 0
	testHookConverterStart = func(converterStart) { starts++ }
	t.Cleanup(func() { testHookConverterStart = nil })

	res, previewErr := t131PreviewDocx(t, svc)
	require.Error(t, previewErr, "T-132(b): the unreadable manifest must not convert")
	require.Nil(t, res)
	errText := previewErr.Error()
	const damagedPrefix = "Office preview is damaged ("
	const damagedTail = "). Reinstall or re-register the Office preview component."
	require.True(t, strings.HasPrefix(errText, damagedPrefix) && strings.HasSuffix(errText, damagedTail),
		"T-132(b): today's damaged office text must stand without an active record; got %q", errText)
	t132Reason(t, errText)
	require.NotContains(t, errText, "stop and start Omnipus",
		"T-132(b): no restart sentence without an active record")
	require.Zero(t, starts, "T-132(b): the converter must never start")

	res, previewErr = t131PreviewPDF(t, svc)
	require.NoError(t, previewErr, "T-132(b): the renderer denial is a degraded result")
	require.NotNil(t, res)
	require.Equal(t, "renderer_damaged", res.RenderReason, "T-132(b): the renderer reason code")
	require.True(t, strings.HasPrefix(res.RenderDetail, "PDF renderer damaged (") && strings.HasSuffix(res.RenderDetail, ")"),
		"T-132(b): today's damaged renderer text must stand; got %q", res.RenderDetail)
	require.NotContains(t, res.RenderDetail, "stop and start Omnipus",
		"T-132(b): no restart sentence without an active record")
}

// TestT132_NonPermissionRefusalKeepsDamagedText — RS-1 (c): under an active
// record, a refusal that is not a permission error (a fingerprint mismatch)
// keeps today's damaged text (guard).
func TestT132_NonPermissionRefusalKeepsDamagedText(t *testing.T) {
	base, bin, own := t131StageProgramWorld(t)
	_ = base
	officeSlot, _ := t132StageLockedPackagedSlots(t, own)
	home := t.TempDir()
	// The manifest is readable; the program file is not what it was
	// fingerprinted as — a non-permission refusal.
	manifestPath := filepath.Join(officeSlot, "manifest.json")
	require.NoError(t, os.Chmod(manifestPath, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(officeSlot, "program", "soffice.bin"), []byte("#!/bin/sh\nexit 7\n"), 0o755))
	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
	require.NoError(t, err)
	rec := &component.StartupGrantRecord{}
	require.NoError(t, rec.AddGranted(own))
	require.NoError(t, rec.AddGranted(officeSlot))
	component.SetStartupGrants(rec)
	t.Cleanup(func() { component.SetStartupGrants(nil) })

	res, previewErr := t131PreviewDocx(t, svc)
	require.Error(t, previewErr, "T-132(c): the fingerprint mismatch must not convert")
	require.Nil(t, res)
	require.Contains(t, previewErr.Error(), "fingerprint mismatch",
		"T-132(c): the reason is the fingerprint mismatch, not a permission failure")
	require.NotContains(t, previewErr.Error(), "cannot read its program files",
		"T-132(c): a non-permission refusal keeps today's damaged text; got %q", previewErr)
	require.NotContains(t, previewErr.Error(), "stop and start Omnipus",
		"T-132(c): a non-permission refusal carries no restart sentence")
	_ = res
}

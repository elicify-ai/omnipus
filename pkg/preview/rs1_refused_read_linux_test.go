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
	// TA-06: the converter is the marker stub — a real start writes the
	// work-folder marker (observed live by TestT132_LaunchObserverPositiveControl).
	require.NoError(t, os.WriteFile(filepath.Join(officeSlot, "program", "soffice.bin"), []byte(t131ConverterMarkerScript()), 0o755))
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
	return t132ReasonWithPrefix(t, message, "packaged manifest unreadable:")
}

// t132ReasonWithPrefix checks the reason conditions against a required
// prefix and returns it (TA-05 adds the registered-manifest and
// fingerprinted-file sources, whose causes name their own site).
func t132ReasonWithPrefix(t *testing.T, message, wantPrefix string) string {
	t.Helper()
	open := strings.Index(message, "(")
	closeIdx := strings.LastIndex(message, ")")
	require.Greater(t, closeIdx, open, "T-132: the reason must be parenthesised")
	reason := message[open+1 : closeIdx]
	require.True(t, strings.HasPrefix(reason, wantPrefix),
		"T-132: the reason must begin with %q; got %q", wantPrefix, reason)
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

	officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
	require.Error(t, previewErr, "T-132(a): the refused read must not convert")
	require.Nil(t, res)
	errText := previewErr.Error()
	require.True(t, strings.HasPrefix(errText, t132OfficeRSPrefix) && strings.HasSuffix(errText, t132OfficeRSTail),
		"T-132(a): the office denial must be the RS-1 sentence; got %q", errText)
	t132Reason(t, errText)
	require.Zero(t, officeStarts, "T-132(a): the converter must never start")
	require.False(t, officeStarted, "T-132/TA-06: the refusal must precede any converter start — the mid-call observer saw no work-folder marker")

	rendererStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewPDF(t, svc) })
	require.NoError(t, previewErr, "T-132(a): the renderer denial is a degraded result")
	require.NotNil(t, res)
	require.Equal(t, "renderer_damaged", res.RenderReason, "T-132(a): the renderer reason code")
	require.True(t, strings.HasPrefix(res.RenderDetail, t132RenderRSPrefix) && strings.HasSuffix(res.RenderDetail, t132RenderRSTail),
		"T-132(a): the renderer denial must be the RS-1 sentence; got %q", res.RenderDetail)
	t132Reason(t, res.RenderDetail)
	require.Zero(t, rendererStarts, "T-132(a): the renderer must never start")
	require.False(t, rendererStarted, "T-132/TA-06: the renderer denial must not launch the converter either")
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

	officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
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
	require.False(t, officeStarted, "T-132/TA-06: the refusal must precede any converter start — the mid-call observer saw no work-folder marker")

	rendererStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewPDF(t, svc) })
	require.NoError(t, previewErr, "T-132(b): the renderer denial is a degraded result")
	require.NotNil(t, res)
	require.Equal(t, "renderer_damaged", res.RenderReason, "T-132(b): the renderer reason code")
	require.True(t, strings.HasPrefix(res.RenderDetail, "PDF renderer damaged (") && strings.HasSuffix(res.RenderDetail, ")"),
		"T-132(b): today's damaged renderer text must stand; got %q", res.RenderDetail)
	require.NotContains(t, res.RenderDetail, "stop and start Omnipus",
		"T-132(b): no restart sentence without an active record")
	require.False(t, rendererStarted, "T-132/TA-06: the renderer denial must not launch the converter either")
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

// TestT132_RefusedRegisteredReadsNameTheRestart — TA-05 (gate round 2): the
// two permission sources the packaged-manifest rows do not cover, on a
// REGISTERED component inside OWN — (i) a fingerprinted file that cannot be
// read (Verify's hash read) and (ii) the registered manifest itself made
// unreadable. Under an ACTIVE startup record each source must produce the
// RS-1 texts; without a record, today's damaged texts; refusal always
// stands, the cause names its own site with "permission denied", and no
// converter or renderer starts (TA-06 marker + hooks).
func TestT132_RefusedRegisteredReadsNameTheRestart(t *testing.T) {
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: T-132 runs as a non-root user — required by office-preview spec §3.9 T-132; as root a mode-000 file is still readable and the refused read cannot be produced")
	}
	for _, source := range []string{"fingerprinted-file", "registered-manifest"} {
		for _, record := range []string{"active-startup-record", "no-startup-record"} {
			t.Run(source+"/"+record, func(t *testing.T) {
				base, bin, own := t131StageProgramWorld(t)
				_ = base
				home := t.TempDir()
				officeRoot := t131PlantedOffice(t, home, own)
				rendererRoot := t131PlantedRenderer(t, home, own)
				var officeReason, rendererReason string
				switch source {
				case "fingerprinted-file":
					officeExe := filepath.Join(officeRoot, "soffice.bin")
					require.NoError(t, os.Chmod(officeExe, 0o000))
					t.Cleanup(func() { _ = os.Chmod(officeExe, 0o600) })
					_, readErr := os.ReadFile(officeExe)
					require.ErrorIs(t, readErr, os.ErrPermission,
						"T-132 precondition: the fingerprinted file must be unreadable, or the refused read cannot be distinguished from success")
					rendererExe := filepath.Join(rendererRoot, "omnipus-pdfium")
					require.NoError(t, os.Chmod(rendererExe, 0o000))
					t.Cleanup(func() { _ = os.Chmod(rendererExe, 0o600) })
					_, readErr = os.ReadFile(rendererExe)
					require.ErrorIs(t, readErr, os.ErrPermission, "T-132 precondition: the renderer's fingerprinted file must be unreadable")
					officeReason, rendererReason = "soffice.bin unreadable:", "omnipus-pdfium unreadable:"
				case "registered-manifest":
					for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
						locked := filepath.Join(component.RegisteredDir(home, kind), component.ManifestFile)
						require.NoError(t, os.Chmod(locked, 0o000))
						t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
						_, readErr := os.ReadFile(locked)
						require.ErrorIs(t, readErr, os.ErrPermission, "T-132 precondition: the registered manifest must be unreadable")
					}
					officeReason, rendererReason = "registered manifest unreadable:", "registered manifest unreadable:"
				}
				resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
				svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
				require.NoError(t, err)
				if record == "active-startup-record" {
					// The record models the RS-1 premise: the components were
					// in the startup record, and NOW a read is refused. The
					// registered roots must therefore be granted too — §3.8
					// BR-2.5 refuses a root the record does not cover with
					// the after-start sentence BEFORE anything in it is read
					// (component.go::Resolve's CheckStartupGrant step), which
					// is that gate's own correct behaviour, not this row's.
					rec := &component.StartupGrantRecord{}
					require.NoError(t, rec.AddGranted(own))
					require.NoError(t, rec.AddGranted(officeRoot))
					require.NoError(t, rec.AddGranted(rendererRoot))
					component.SetStartupGrants(rec)
					t.Cleanup(func() { component.SetStartupGrants(nil) })
				}
				officeStarts, rendererStarts := 0, 0
				testHookConverterStart = func(converterStart) { officeStarts++ }
				testHookRendererStart = func(converterStart) { rendererStarts++ }
				t.Cleanup(func() { testHookConverterStart = nil; testHookRendererStart = nil })

				officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
				require.Error(t, previewErr, "T-132/TA-05: the refused registered read must not convert")
				require.Nil(t, res)
				errText := previewErr.Error()
				if record == "active-startup-record" {
					require.True(t, strings.HasPrefix(errText, t132OfficeRSPrefix) && strings.HasSuffix(errText, t132OfficeRSTail),
						"T-132/TA-05: the office denial must be the RS-1 sentence under the active record; got %q", errText)
				} else {
					require.True(t, strings.HasPrefix(errText, "Office preview is damaged (") && strings.HasSuffix(errText, "). Reinstall or re-register the Office preview component."),
						"T-132/TA-05: today's damaged office text must stand without an active record; got %q", errText)
					require.NotContains(t, errText, "stop and start Omnipus",
						"T-132/TA-05: no restart sentence without an active record")
				}
				t132ReasonWithPrefix(t, errText, officeReason)
				require.Zero(t, officeStarts, "T-132/TA-05: the converter must never start")
				require.False(t, officeStarted, "T-132/TA-05+TA-06: the refusal must precede any converter start — the mid-call observer saw no work-folder marker")

				rendererStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewPDF(t, svc) })
				require.NoError(t, previewErr, "T-132/TA-05: the renderer denial is a degraded result")
				require.NotNil(t, res)
				require.Equal(t, "renderer_damaged", res.RenderReason, "T-132/TA-05: the renderer reason code")
				if record == "active-startup-record" {
					require.True(t, strings.HasPrefix(res.RenderDetail, t132RenderRSPrefix) && strings.HasSuffix(res.RenderDetail, t132RenderRSTail),
						"T-132/TA-05: the renderer denial must be the RS-1 sentence under the active record; got %q", res.RenderDetail)
				} else {
					require.True(t, strings.HasPrefix(res.RenderDetail, "PDF renderer damaged (") && strings.HasSuffix(res.RenderDetail, ")"),
						"T-132/TA-05: today's damaged renderer text must stand; got %q", res.RenderDetail)
					require.NotContains(t, res.RenderDetail, "stop and start Omnipus",
						"T-132/TA-05: no restart sentence without an active record")
				}
				t132ReasonWithPrefix(t, res.RenderDetail, rendererReason)
				require.Zero(t, rendererStarts, "T-132/TA-05: the renderer must never start")
				require.False(t, rendererStarted, "T-132/TA-05+TA-06: the renderer denial must not launch the converter either")
			})
		}
	}
}

// TestT132_LaunchObserverPositiveControl — TA-06: proves the marker
// observer is live for the packaged-slot rows. With the office manifest
// made readable the slot resolves, the service really launches the marker
// stub, and the call fails on the stub's exit — with the marker present.
func TestT132_LaunchObserverPositiveControl(t *testing.T) {
	base, bin, own := t131StageProgramWorld(t)
	_ = base
	officeSlot, _ := t132StageLockedPackagedSlots(t, own)
	require.NoError(t, os.Chmod(filepath.Join(officeSlot, "manifest.json"), 0o600))
	home := t.TempDir()
	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
	require.NoError(t, err)
	rec := &component.StartupGrantRecord{}
	require.NoError(t, rec.AddGranted(own))
	require.NoError(t, rec.AddGranted(officeSlot))
	component.SetStartupGrants(rec)
	t.Cleanup(func() { component.SetStartupGrants(nil) })
	started, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
	require.Error(t, previewErr, "TA-06 control: the marker stub cannot convert; the call fails after its real launch")
	require.Nil(t, res)
	require.True(t, started,
		"TA-06 control: a real converter start must appear to the mid-call observer — without this the refusal rows' no-start assertions would be vacuous; the call failed with: %v", previewErr)
}

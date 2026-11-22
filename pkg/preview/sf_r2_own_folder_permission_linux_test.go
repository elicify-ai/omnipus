//go:build linux

package preview

// SF-R2-1 — gate round 2, silent-failure review (team-lead dispatch
// 2026-11-22): a NON-TRAVERSABLE ANCESTOR of Omnipus's own folder makes
// resolving OWN fail with a permission error. On 339477dab's production
// code that error is discarded, and an operator-registered component whose
// root lies INSIDE the own folder is reported as OUTSIDE it — the wrong
// diagnostic, wrong recovery advice (re-register a folder the account
// cannot even reach), and RS-1 skipped because StatusOutsideOwn is returned
// before the permission classification. Every subject subtest below asserts
// the behaviour the reviewers specified and the developer has approved, so
// the test FAILS on 339477dab (the RED log is the dispatch evidence) and
// passes once the fix lands.
//
// Approved oracles (team-lead, 2026-11-22):
//   - resolver reason:    `component folder unreadable: <cause>`
//   - registration error: `could not check Omnipus's own folder <own> (<cause>). Check that the account running this command can open every folder on that path, then register again` — the `<kind> not registered: ` prefix is the command layer's (spec line 972, pinned by the command rows); the component API returns the rest
//   - startup note:       `<kind> registered root <root> skipped: <real reason>`
//   - RS-1 with an active startup record, ordinary damaged text without one;
//     never the OF-3/OF-4 "outside Omnipus's own folder" text; no start and
//     no packaged substitution.
//
// The missing-OWN control at the end pins what §3.9 OF-3 genuinely requires
// — a genuinely absent OWN still refuses with the existing outside-folder
// text — so the fix cannot keep the permission cause by refusing
// everything. Runs as a non-root user; as root the lock cannot hold and the
// precondition fails the row loudly instead of passing vacuously.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

func TestSFR2_1_UnreadableOwnAncestorKeepsThePermissionCause(t *testing.T) {
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: SF-R2-1 runs as a non-root user — required by the gate round-2 dispatch; as root a mode-000 ancestor is still traversable and the permission failure cannot be produced")
	}
	_, bin, own := t131StageProgramWorld(t)
	home := t.TempDir()
	officeRoot := t131PlantedOffice(t, home, own)
	rendererRoot := t131PlantedRenderer(t, home, own)

	// The registration folder for the registration subtest: a valid Linux
	// layout inside OWN, created while the tree is still traversable.
	registerFolder := filepath.Join(own, "register-lo")
	program := filepath.Join(registerFolder, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o600))

	// The startup record for the RS-1 subtest is built BEFORE the lock:
	// StartupGrantRecord.AddGranted stats the granted paths, and the locked
	// ancestor must not fail test-side setup — the subject is the production
	// resolution under the lock, not record construction.
	rec := &component.StartupGrantRecord{}
	require.NoError(t, rec.AddGranted(own))
	require.NoError(t, rec.AddGranted(officeRoot))
	require.NoError(t, rec.AddGranted(rendererRoot))

	// The lock: OWN's parent becomes non-traversable. Cleanup restores the
	// permission before the staged world's own removal cleanup runs.
	share := filepath.Dir(own)
	require.NoError(t, os.Chmod(share, 0o000))
	t.Cleanup(func() { _ = os.Chmod(share, 0o700) })

	// Preconditions: the lock denies traversal (as root it would not), and
	// the registered manifest stays readable — the subject is the discarded
	// OWN error, not the manifest.
	_, evalErr := filepath.EvalSymlinks(own)
	require.ErrorIs(t, evalErr, os.ErrPermission,
		"SF-R2-1 precondition: resolving OWN must fail with the permission error the code must keep")
	_, manifestReadErr := os.ReadFile(filepath.Join(component.RegisteredDir(home, component.KindOffice), component.ManifestFile))
	require.NoError(t, manifestReadErr,
		"SF-R2-1 precondition: the registered manifest is readable; the defect under test is the discarded OWN error")

	t.Run("resolve-keeps-the-cause", func(t *testing.T) {
		resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
		for _, tc := range []struct {
			kind component.Kind
			root string
		}{
			{component.KindOffice, officeRoot},
			{component.KindPDFRenderer, rendererRoot},
		} {
			got, problem := resolver.Resolve(tc.kind)
			require.Nil(t, got, "SF-R2-1: no component may resolve while its own folder cannot be resolved")
			require.NotNil(t, problem, "SF-R2-1: %s must be refused", tc.kind)
			require.Equal(t, component.StatusDamaged, problem.Status,
				"SF-R2-1: the %s refusal must be the permission-bearing damaged result, not %v", tc.kind, problem.Status)
			require.True(t, problem.AccessRefused,
				"SF-R2-1: the %s permission classification must be kept so RS-1 can apply", tc.kind)
			require.True(t, strings.HasPrefix(problem.Reason, "component folder unreadable: "),
				"SF-R2-1: the %s resolver reason must begin with the folder-unreadable cause; got %q", tc.kind, problem.Reason)
			require.Contains(t, problem.Reason, "permission denied",
				"SF-R2-1: the %s cause is the permission failure; got %q", tc.kind, problem.Reason)
			require.Contains(t, problem.Reason, own,
				"SF-R2-1: the %s cause names the folder that cannot be resolved — the own folder the locked ancestor hides (the status, refused classification and prefix above are the discriminators)", tc.kind)
			require.NotContains(t, problem.Reason, "outside Omnipus's own folder",
				"SF-R2-1: never the OF-4 outside text — the %s root is inside the folder the account cannot traverse", tc.kind)
		}
	})

	t.Run("registration-reports-the-cause", func(t *testing.T) {
		regHome := t.TempDir()
		_, err := component.RegisterOffice(component.RegisterOptions{
			Home: regHome, Folder: registerFolder, Operator: "sf-r2-1", GOOS: "linux",
		})
		require.Error(t, err, "SF-R2-1: the registration must be refused while its own folder cannot be resolved")
		require.True(t, strings.HasPrefix(err.Error(), "could not check Omnipus's own folder "+own+" ("),
			"SF-R2-1: the registration error must name the failed own-folder check; got %q", err)
		require.Contains(t, err.Error(), "permission denied",
			"SF-R2-1: the registration error must carry the permission cause; got %q", err)
		require.True(t, strings.HasSuffix(err.Error(), "). Check that the account running this command can open every folder on that path, then register again"),
			"SF-R2-1: the registration error must end with the approved access-check action; got %q", err)
		require.NotContains(t, err.Error(), "outside Omnipus's own folder",
			"SF-R2-1: never the OF-3 outside text — the folder is inside the folder the account cannot traverse")
		require.NoFileExists(t, filepath.Join(component.RegisteredDir(regHome, component.KindOffice), component.ManifestFile),
			"SF-R2-1: a refused registration writes nothing")
	})

	t.Run("startup-note-keeps-the-cause", func(t *testing.T) {
		resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
		_, notes := component.ConfinementStartupGrants(resolver)
		for _, tc := range []struct {
			kind component.Kind
			root string
		}{
			{component.KindOffice, officeRoot},
			{component.KindPDFRenderer, rendererRoot},
		} {
			var note string
			for _, n := range notes {
				if strings.HasPrefix(n, string(tc.kind)+" registered root ") {
					note = n
					break
				}
			}
			require.NotEmpty(t, note, "SF-R2-1: the startup notes must carry the %s registered-root skip line; got %v", tc.kind, notes)
			require.True(t, strings.HasPrefix(note, string(tc.kind)+" registered root "+filepath.Clean(tc.root)+" skipped: "),
				"SF-R2-1: the %s note must be `<kind> registered root <root> skipped: <reason>`; got %q", tc.kind, note)
			require.Contains(t, note, "permission denied",
				"SF-R2-1: the %s note keeps the real reason; got %q", tc.kind, note)
			require.NotContains(t, note, "outside Omnipus's own folder",
				"SF-R2-1: never the outside-folder note for an inside-OWN %s root", tc.kind)
		}
	})

	t.Run("service-names-the-restart-with-a-record", func(t *testing.T) {
		resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
		svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
		require.NoError(t, err)
		// rec was built before the share lock (AddGranted stats its paths).
		component.SetStartupGrants(rec)
		t.Cleanup(func() { component.SetStartupGrants(nil) })
		officeStarts, rendererStarts := 0, 0
		testHookConverterStart = func(converterStart) { officeStarts++ }
		testHookRendererStart = func(converterStart) { rendererStarts++ }
		t.Cleanup(func() { testHookConverterStart = nil; testHookRendererStart = nil })

		officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
		require.Error(t, previewErr, "SF-R2-1: the unreadable own folder must refuse the preview")
		require.Nil(t, res)
		errText := previewErr.Error()
		require.True(t, strings.HasPrefix(errText, t132OfficeRSPrefix) && strings.HasSuffix(errText, t132OfficeRSTail),
			"SF-R2-1: with an active startup record the office denial must be the RS-1 sentence; got %q", errText)
		require.Contains(t, errText, "permission denied", "SF-R2-1: the RS-1 reason carries the permission cause")
		require.NotContains(t, errText, "outside Omnipus's own folder",
			"SF-R2-1: never the OF-4 outside text at the service boundary")
		require.Zero(t, officeStarts, "SF-R2-1: the converter must never start")
		require.False(t, officeStarted,
			"SF-R2-1/TA-06: no launch happened, confined or not — the mid-call observer saw no work-folder marker")

		rendererStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewPDF(t, svc) })
		require.NoError(t, previewErr, "SF-R2-1: the renderer denial is a degraded result")
		require.NotNil(t, res)
		require.Equal(t, "renderer_damaged", res.RenderReason, "SF-R2-1: the renderer denial reason code")
		require.True(t, strings.HasPrefix(res.RenderDetail, t132RenderRSPrefix) && strings.HasSuffix(res.RenderDetail, t132RenderRSTail),
			"SF-R2-1: the renderer denial must be the RS-1 sentence; got %q", res.RenderDetail)
		require.NotContains(t, res.RenderDetail, "outside Omnipus's own folder",
			"SF-R2-1: never the outside text on the renderer detail")
		require.Zero(t, rendererStarts, "SF-R2-1: the renderer must never start")
		require.False(t, rendererStarted, "SF-R2-1/TA-06: the renderer denial must not launch the converter either")
	})

	t.Run("service-keeps-damaged-text-without-a-record", func(t *testing.T) {
		resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
		svc, err := New(Config{Home: home, Self: bin, Resolver: resolver})
		require.NoError(t, err)
		officeStarted, res, previewErr := t131ObserveCall(t, home, func() (*Result, error) { return t131PreviewDocx(t, svc) })
		require.Error(t, previewErr, "SF-R2-1: the unreadable own folder must refuse the preview")
		require.Nil(t, res)
		errText := previewErr.Error()
		require.True(t, strings.HasPrefix(errText, "Office preview is damaged (") && strings.HasSuffix(errText, "). Reinstall or re-register the Office preview component."),
			"SF-R2-1: today's damaged text must stand without an active startup record; got %q", errText)
		require.Contains(t, errText, "component folder unreadable:",
			"SF-R2-1: the damaged reason is the folder-unreadable cause")
		require.Contains(t, errText, "permission denied", "SF-R2-1: the cause is the permission failure")
		require.NotContains(t, errText, "outside Omnipus's own folder",
			"SF-R2-1: never the OF-4 outside text without a record either")
		require.NotContains(t, errText, "stop and start Omnipus",
			"SF-R2-1: no restart sentence without an active record")
		require.False(t, officeStarted, "SF-R2-1/TA-06: the mid-call observer saw no converter start")
	})

	t.Run("missing-own-still-refuses-as-outside", func(t *testing.T) {
		// Control (green before and after the fix): a genuinely ABSENT own
		// folder keeps OF-4's refusal — the registered root cannot be inside
		// a folder that does not exist, and the cause there is ENOENT, not
		// the permission error, so the fix cannot keep the permission cause
		// by refusing every own-folder failure as damaged. The share
		// traversal lock would mask the missing-OWN scenario, so it is
		// lifted for this control world only (and re-applied at cleanup, in
		// case a later subtest is ever added after this one).
		require.NoError(t, os.Chmod(share, 0o700))
		t.Cleanup(func() { _ = os.Chmod(share, 0o000) })
		_, ctrlBin, ctrlOwn := t131StageProgramWorld(t)
		ctrlHome := t.TempDir()
		ctrlRoot := t131PlantedOffice(t, ctrlHome, ctrlOwn)
		require.NoError(t, os.RemoveAll(ctrlOwn))
		resolver := &component.Resolver{Home: ctrlHome, Executable: ctrlBin, GOOS: "linux"}
		got, problem := resolver.Resolve(component.KindOffice)
		require.Nil(t, got, "SF-R2-1 control: nothing resolves without the own folder")
		require.NotNil(t, problem)
		require.Equal(t, component.StatusOutsideOwn, problem.Status,
			"SF-R2-1 control: a genuinely missing own folder keeps the OF-4 outside status")
		require.Contains(t, problem.Reason, filepath.Clean(ctrlRoot),
			"SF-R2-1 control: the existing outside text names the registered root")
		require.Contains(t, problem.Reason, ctrlOwn,
			"SF-R2-1 control: the existing outside text names Omnipus's own folder")
		require.NotContains(t, problem.Reason, "permission denied",
			"SF-R2-1 control: a missing folder is an existence failure, not a permission one")
	})
}

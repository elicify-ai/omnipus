//go:build darwin

package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// T-105 / §3.4 D8c (R1), FR-055 (B-69): LibreOffice reads the verified
// registry folder IN PLACE, read-only. A fake soffice.bin (as T-78: the test
// binary at the process edge; the real launcher and the real confinement
// hooks do everything else) records, at each start, the folder
// URE_MORE_SERVICES names and the hash of every file in it, then tries to
// create, change and remove a file in that folder:
//
//	(a) the folder named is exactly Component.Registry as
//	    pkg/preview/component/component.go::load resolved it — for an
//	    operator-registered component (registry under
//	    <home>/system/components/office/services) and for a packaged one
//	    (registry inside the component root);
//	(b) the recorded hashes equal the manifest's registry;
//	(c) every write attempt fails, and the folder is byte-identical
//	    afterwards (same file list, same hashes);
//	(d) the same holds on an exit-81 second start;
//	(e) the captured policy grants that folder read only, and no other part
//	    of <home>/system — BLOCKED on the policy-capture seam (below).
//
// The component is resolved through the real Resolver (load + full Verify),
// and the conversion runs the component's own resolved executable path, so
// readExec and the registry grant apply exactly as production builds them.
// Not a pass if: the fake never attempts the write or never reads the folder
// (the recorded hashes and each attempt's error are required); the folder
// checked is not the one named in URE_MORE_SERVICES; only the packaged case
// runs, which never touches <home>/system (both cases run below; on the
// pre-D8c code the packaged case passes because its registry lies inside the
// already-granted component root — the operator-registered case is the one
// that discriminates, which is why its failure is this row's RED); the run
// is unconfined.
//
// (e) is checked on the accepted seam (§3.4 D8d ruling): the policy of a
// real start of the operator-registered component, captured by
// pkg/preview/confine.go::testHookConverterStart — never a policy built by
// hand — grants the registry folder read only, and no rule covers any other
// part of <home>/system. The case runs where the component that touches
// <home>/system is in scope (operator_registered).
func TestPreviewRegistryReadInPlace(t *testing.T) {
	primeKernelSandbox(t)

	// ure... — the operator-registered fixture: a registered manifest whose
	// executable is the test binary (hard-linked, so the fake soffice
	// dispatch applies) and whose registry lives where an operator-registered
	// LibreOffice's filtered registry lives, under <home>/system.
	t.Run("operator_registered", func(t *testing.T) {
		home := t.TempDir()
		regDir := filepath.Join(home, "system", "components", "office", "services")
		digest := regReadRegistryFixture(t, regDir)
		root := t.TempDir()
		exe := regReadLinkSelf(t, root)
		sum, _, hashErr := component.HashFile(exe)
		require.NoError(t, hashErr, "instrument: the fixture executable is hashable")
		files := map[string]string{"soffice.bin": sum}
		manifest := component.Manifest{
			Schema: component.ManifestSchema, Component: component.KindOffice,
			Version: "t105-fixture", Platform: component.Platform(),
			Executable: "soffice.bin", Files: files, FileListSHA256: component.FileListDigest(files),
			Registry: "services", RegistrySHA256: digest,
			Trust: component.TrustOperatorRegistered, Root: root,
			RegisteredBy: "office-preview-test", RegisteredAt: "2026-11-08T00:00:00Z",
		}
		regReadWriteManifest(t, home, manifest)
		self, selfErr := os.Executable()
		require.NoError(t, selfErr)
		comp, problem := (&component.Resolver{Home: home, Executable: self}).Resolve(component.KindOffice)
		require.Nil(t, problem, "instrument: the registered component resolves through the real load and verifies")
		realRegDir, linkErr := filepath.EvalSymlinks(regDir)
		require.NoError(t, linkErr, "instrument: the fixture registry folder resolves")
		require.Equal(t, realRegDir, comp.Registry,
			"instrument: load resolved the registry to the <home>/system folder — the case that touches <home>/system")

		t.Run("a_folder_named_is_component_registry", func(t *testing.T) {
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.Equal(t, comp.Registry, receipts[0].Services,
				"T-105 (a) §3.4 D8c R1: URE_MORE_SERVICES names Component.Registry as load resolved it (was %s: %s)", receipts[0].Services, receipts[0].Note)
		})

		t.Run("b_hashes_equal_the_manifest_registry", func(t *testing.T) {
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.True(t, receipts[0].ReadOK,
				"T-105 (b) §3.4 D8c R1: the fake must read the folder URE_MORE_SERVICES named and record the hashes — on the pre-D8c code the converter is granted no read on the registry under <home>/system, so LibreOffice cannot load it (%s)", receipts[0].Note)
			require.Equal(t, digest, receipts[0].Digest,
				"T-105 (b): the recorded hashes equal the manifest's registry")
		})

		t.Run("c_write_attempts_fail_folder_identical", func(t *testing.T) {
			before, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable before the call")
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.True(t, receipts[0].ReadOK,
				"instrument: read-only means the read succeeded and the writes failed — without the read this row proves nothing (%s)", receipts[0].Note)
			require.NotEmpty(t, receipts[0].CreateErr, "T-105 (c) §3.4 D8c R1: the create attempt in the registry folder must fail")
			require.NotEmpty(t, receipts[0].ChangeErr, "T-105 (c) §3.4 D8c R1: the change attempt in the registry folder must fail")
			require.NotEmpty(t, receipts[0].RemoveErr, "T-105 (c) §3.4 D8c R1: the remove attempt in the registry folder must fail")
			after, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable after the call")
			require.Equal(t, before, after,
				"T-105 (c) §3.4 D8c R1: the registry folder is byte-identical afterwards — same file list, same hashes")
		})

		t.Run("d_second_start_holds", func(t *testing.T) {
			before, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable before the call")
			conv, c := regReadCaseCall(t, comp, "regread_81")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 2, "instrument: the exit-81 first start and the second start")
			for _, receipt := range receipts {
				require.True(t, receipt.ReadOK,
					"T-105 (d) §3.4 D8c R1: start %d reads the folder URE_MORE_SERVICES named (%s)", receipt.Start, receipt.Note)
				require.Equal(t, comp.Registry, receipt.Services,
					"T-105 (d): start %d's URE_MORE_SERVICES names Component.Registry (was %s)", receipt.Start, receipt.Services)
				require.Equal(t, digest, receipt.Digest,
					"T-105 (d): start %d's recorded hashes equal the manifest's registry", receipt.Start)
				require.NotEmpty(t, receipt.CreateErr, "T-105 (d): start %d's create attempt fails", receipt.Start)
				require.NotEmpty(t, receipt.ChangeErr, "T-105 (d): start %d's change attempt fails", receipt.Start)
				require.NotEmpty(t, receipt.RemoveErr, "T-105 (d): start %d's remove attempt fails", receipt.Start)
			}
			after, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable after the call")
			require.Equal(t, before, after, "T-105 (d): the registry folder is byte-identical after the second start")
		})

		// (e) on the accepted seam (§3.4 D8d ruling): the policy of a real
		// start of THIS component, captured by testHookConverterStart — never
		// a policy the test builds — grants the registry folder read and only
		// read, and no rule covers any other part of <home>/system.
		t.Run("e_policy_grants_registry_read_only", func(t *testing.T) {
			var starts []converterStart
			testHookConverterStart = func(cs converterStart) { starts = append(starts, cs) }
			t.Cleanup(func() { testHookConverterStart = nil })
			conv, _ := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			require.NotEmpty(t, starts, "T-105 (e): the hook captured zero converter starts — a test expecting a confined start fails, never skips (seam ruling, §3.4 D8d)")
			cs := starts[len(starts)-1]
			require.False(t, cs.policy.ReadsOpen, "T-105 (e): ReadsOpen is false")
			require.False(t, cs.policy.ExecOpen, "T-105 (e): ExecOpen is false")
			require.Empty(t, cs.policy.DeniedPaths, "T-105 (e) §3.4 D8c: the converter policy carries no deny list")
			require.Empty(t, cs.policy.DeniedNodes, "T-105 (e) §3.4 D8c: the converter policy carries no deny list")
			require.Empty(t, cs.policy.DeniedPathPrefixes, "T-105 (e) §3.4 D8c: the converter policy carries no deny list")
			require.Empty(t, cs.policy.WriteDeniedPaths, "T-105 (e) §3.4 D8c: the converter policy carries no deny list")
			systemReal, linkErr := filepath.EvalSymlinks(filepath.Join(home, "system"))
			require.NoError(t, linkErr, "instrument: <home>/system resolves")
			foundRegistry := false
			for _, rule := range cs.policy.FilesystemRules {
				resolved, ruleLinkErr := filepath.EvalSymlinks(rule.Path)
				require.NoError(t, ruleLinkErr, "instrument: a captured rule path resolves: %s", rule.Path)
				if resolved == comp.Registry {
					foundRegistry = true
					require.Equal(t, sandbox.AccessRead, rule.Access,
						"T-105 (e) §3.4 D8c R1: the captured policy grants the registry folder READ ONLY — no write, no execute")
					continue
				}
				require.False(t, previewWithinDir(systemReal, resolved),
					"T-105 (e) §3.4 D8c R1: no rule covers any part of <home>/system other than the registry folder — found rule on %s", resolved)
			}
			require.True(t, foundRegistry,
				"T-105 (e) §3.4 D8c R1: the captured policy grants the registry folder read")
		})
	})

	// The packaged fixture: a packaged manifest at a packaged candidate
	// folder, registry inside the component root — the variant that never
	// touches <home>/system (kept per the row; on the pre-D8c code it passes
	// because the component root is already granted read+execute).
	t.Run("packaged", func(t *testing.T) {
		home := t.TempDir()
		fixture := t.TempDir()
		candidate := filepath.Join(fixture, "office")
		regDir := filepath.Join(candidate, "services")
		digest := regReadRegistryFixture(t, regDir)
		exe := regReadLinkSelf(t, candidate)
		sum, _, hashErr := component.HashFile(exe)
		require.NoError(t, hashErr, "instrument: the fixture executable is hashable")
		files := map[string]string{"soffice.bin": sum}
		manifest := component.Manifest{
			Schema: component.ManifestSchema, Component: component.KindOffice,
			Version: "t105-fixture", Platform: component.Platform(),
			Executable: "soffice.bin", Files: files, FileListSHA256: component.FileListDigest(files),
			Registry: "services", RegistrySHA256: digest,
			Trust: component.TrustPackaged,
		}
		regReadWriteManifestAt(t, candidate, manifest)
		comp, problem := (&component.Resolver{Home: home, Executable: filepath.Join(fixture, "bin", "omnipus")}).Resolve(component.KindOffice)
		require.Nil(t, problem, "instrument: the packaged component resolves through the real load and verifies")
		realRegDir, linkErr := filepath.EvalSymlinks(regDir)
		require.NoError(t, linkErr, "instrument: the fixture registry folder resolves")
		require.Equal(t, realRegDir, comp.Registry,
			"instrument: load resolved the packaged registry inside the component root")

		t.Run("a_folder_named_is_component_registry", func(t *testing.T) {
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.Equal(t, comp.Registry, receipts[0].Services,
				"T-105 (a) §3.4 D8c R1: URE_MORE_SERVICES names Component.Registry as load resolved it (was %s: %s)", receipts[0].Services, receipts[0].Note)
		})

		t.Run("b_hashes_equal_the_manifest_registry", func(t *testing.T) {
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.True(t, receipts[0].ReadOK,
				"T-105 (b) §3.4 D8c R1: the fake must read the folder URE_MORE_SERVICES named and record the hashes (%s)", receipts[0].Note)
			require.Equal(t, digest, receipts[0].Digest,
				"T-105 (b): the recorded hashes equal the manifest's registry")
		})

		t.Run("c_write_attempts_fail_folder_identical", func(t *testing.T) {
			before, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable before the call")
			conv, c := regReadCaseCall(t, comp, "regread_read")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 1, "instrument: exactly one start")
			require.True(t, receipts[0].ReadOK,
				"instrument: read-only means the read succeeded and the writes failed (%s)", receipts[0].Note)
			require.NotEmpty(t, receipts[0].CreateErr, "T-105 (c) §3.4 D8c R1: the create attempt in the registry folder must fail")
			require.NotEmpty(t, receipts[0].ChangeErr, "T-105 (c) §3.4 D8c R1: the change attempt in the registry folder must fail")
			require.NotEmpty(t, receipts[0].RemoveErr, "T-105 (c) §3.4 D8c R1: the remove attempt in the registry folder must fail")
			after, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable after the call")
			require.Equal(t, before, after,
				"T-105 (c) §3.4 D8c R1: the registry folder is byte-identical afterwards")
		})

		t.Run("d_second_start_holds", func(t *testing.T) {
			before, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable before the call")
			conv, c := regReadCaseCall(t, comp, "regread_81")
			regReadRun(t, conv)
			receipts := regReadReceipts(t, c)
			require.Len(t, receipts, 2, "instrument: the exit-81 first start and the second start")
			for _, receipt := range receipts {
				require.True(t, receipt.ReadOK,
					"T-105 (d) §3.4 D8c R1: start %d reads the folder URE_MORE_SERVICES named (%s)", receipt.Start, receipt.Note)
				require.Equal(t, comp.Registry, receipt.Services,
					"T-105 (d): start %d's URE_MORE_SERVICES names Component.Registry (was %s)", receipt.Start, receipt.Services)
				require.Equal(t, digest, receipt.Digest,
					"T-105 (d): start %d's recorded hashes equal the manifest's registry", receipt.Start)
				require.NotEmpty(t, receipt.CreateErr, "T-105 (d): start %d's create attempt fails", receipt.Start)
				require.NotEmpty(t, receipt.ChangeErr, "T-105 (d): start %d's change attempt fails", receipt.Start)
				require.NotEmpty(t, receipt.RemoveErr, "T-105 (d): start %d's remove attempt fails", receipt.Start)
			}
			after, hashErr := component.HashDir(regDir)
			require.NoError(t, hashErr, "instrument: the fixture registry is readable after the call")
			require.Equal(t, before, after, "T-105 (d): the registry folder is byte-identical after the second start")
		})
	})

	// (e) runs inside the operator_registered variant above: the captured
	// policy (accepted seam, §3.4 D8d ruling) of a real start of the
	// operator-registered component grants the registry read only and covers
	// no other part of <home>/system.
}

// regReadRegistryFixture writes the fixture registry (two regular files) at
// dir and returns its digest — the value the fixture's manifest carries as
// RegistrySHA256.
func regReadRegistryFixture(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "services.rdb"), []byte("filtered services fixture"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "officeloader.rdb"), []byte("loader fixture"), 0o600))
	files, err := component.HashDir(dir)
	require.NoError(t, err, "instrument: the fixture registry is readable")
	return component.FileListDigest(files)
}

// regReadLinkSelf hard-links the test binary into dir as soffice.bin (the
// fake soffice dispatch is in this binary), copying if the link crosses a
// filesystem, and returns the path.
func regReadLinkSelf(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	self, err := os.Executable()
	require.NoError(t, err)
	exe := filepath.Join(dir, "soffice.bin")
	if os.Link(self, exe) != nil {
		data, readErr := os.ReadFile(self)
		require.NoError(t, readErr, "instrument: the test binary is readable for the fixture copy")
		require.NoError(t, os.WriteFile(exe, data, 0o700), "instrument: the fixture copy of the test binary is written")
	}
	return exe
}

// regReadWriteManifest writes an operator-registered manifest at
// RegisteredDir(home, office)/manifest.json, where the Resolver reads it.
func regReadWriteManifest(t *testing.T, home string, manifest component.Manifest) {
	t.Helper()
	regReadWriteManifestAt(t, component.RegisteredDir(home, component.KindOffice), manifest)
}

func regReadWriteManifestAt(t *testing.T, dir string, manifest component.Manifest) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), data, 0o600))
}

// regReadCaseCall builds the conversion the way production would: the
// component's own resolved executable under the real launcher, with the
// component's resolved registry folder, and the behavior file the fake reads.
func regReadCaseCall(t *testing.T, comp *component.Component, behavior string) (officeConversion, *call) {
	t.Helper()
	conv, c := fakeOfficeCall(t, t.TempDir(), behavior)
	conv.registry = comp.Registry
	conv.executable = comp.Executable
	conv.root = comp.Root
	t.Cleanup(c.finish)
	return conv, c
}

// regReadRun runs the conversion and requires the confined, converting
// baseline every oracle stands on.
func regReadRun(t *testing.T, conv officeConversion) []byte {
	t.Helper()
	pdf, unconfined, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	require.Equal(t, "", unconfined, "T-105: the fake soffice runs confined — an unconfined run is not a pass and proves nothing about reads or writes")
	require.NoError(t, err, "instrument: the conversion completed")
	require.NotEmpty(t, pdf, "instrument: the fake converted")
	return pdf
}

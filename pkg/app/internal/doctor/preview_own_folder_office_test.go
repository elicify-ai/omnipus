//go:build linux

package doctor

// T-131 (c) — office-preview spec §3.9 (2026-11-21, founder decision OF;
// FR-068): doctor reports a pre-§3.9 registration whose recorded root lies
// outside Omnipus's own folder with exactly the OF-4 doctor texts, on
// WARN-PREVIEW-002 (LibreOffice) and WARN-PREVIEW-001 (PDF renderer). The
// oracles are the spec's OF-4 doctor texts, never the code. RED on the code
// before §3.9: doctor reports the generic damaged finding instead, so the
// exact-text assertions fail.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

func t131DoctorPlantOperatorManifest(t *testing.T, home string, kind component.Kind, root, exeName string, files []string) {
	t.Helper()
	// D13 (team-lead 2026-11-21): the executable must be among the
	// fingerprinted files, or the manifest fails to parse before the
	// own-folder check and the doctor reports the wrong finding.
	require.Contains(t, files, exeName,
		"t131 doctor precondition: the manifest's executable must be among the fingerprinted files")
	hashes := make(map[string]string, len(files))
	for _, name := range files {
		sum, _, err := component.HashFile(filepath.Join(root, name))
		require.NoError(t, err)
		hashes[name] = sum
	}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: kind,
		Version: "26.8.0.3", BuildID: "t131-doctor-pre9", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: exeName,
		Files: hashes, FileListSHA256: component.FileListDigest(hashes),
		Trust: component.TrustOperatorRegistered, Root: root, RegisteredBy: "t131-doctor-pre9",
	}
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), raw, 0o600))
}

// The doctor's resolver program path is the default (the running program),
// so its OWN is derived from the test binary exactly as the production
// doctor's would be from the omnipus binary. The planted roots sit OUTSIDE
// that OWN, with mode 000 so the doctor cannot have opened anything inside
// them; as root the lock cannot hold and the row fails loudly (T-131 runs
// as a non-root user).
func TestT131_DoctorWarnsRegisteredOutsideOwnFolder(t *testing.T) {
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: T-131(c) runs as a non-root user — required by office-preview spec §3.9 T-131; as root the mode-000 roots cannot prove the doctor opened nothing inside them")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	own := filepath.Clean(filepath.Join(filepath.Dir(resolved), "..", "share", "omnipus"))
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	for _, tc := range []struct {
		kind    component.Kind
		code    string
		exeName string
		want    string
	}{
		{
			kind: component.KindOffice, code: "WARN-PREVIEW-002", exeName: "soffice.bin",
			want: "LibreOffice is registered outside Omnipus's own folder (%root%; on Linux only folders inside %own% are used): Office previews are refused; PDF previews still work. Remove the registration with `omnipus office unregister`, or register LibreOffice 26.8 from a folder inside %own%.",
		},
		{
			kind: component.KindPDFRenderer, code: "WARN-PREVIEW-001", exeName: "omnipus-pdfium",
			want: "PDF renderer is registered outside Omnipus's own folder (%root%; on Linux only folders inside %own% are used): PDF previews send the PDF or its text, without page images. Remove the registration with `omnipus pdf-renderer unregister`, or register an unpacked renderer release from a folder inside %own%.",
		},
	} {
		t.Run(tc.code, func(t *testing.T) {
			root := filepath.Join(home, "elsewhere", string(tc.kind))
			require.NoError(t, os.MkdirAll(root, 0o700))
			// D13: the executable is among the fingerprinted files, so the
			// planted manifest parses and the own-folder check is reached.
			files := []string{tc.exeName, "manifest-fixture.bin", "dependency.dat"}
			for _, name := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("t131 doctor fixture: "+name), 0o600))
			}
			t131DoctorPlantOperatorManifest(t, home, tc.kind, root, tc.exeName, files)
			require.NoError(t, os.Chmod(root, 0o000))
			t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
			for _, name := range files {
				_, openErr := os.Open(filepath.Join(root, name))
				require.ErrorIs(t, openErr, os.ErrPermission, "T-131(c) precondition: the root must be unreadable")
			}

			want := strings.ReplaceAll(strings.ReplaceAll(tc.want, "%root%", root), "%own%", own)

			warnings := checkDocumentPreview(&config.Config{})
			matched := officeDoctorWarnings(warnings, tc.code)
			require.Len(t, matched, 1,
				"T-131(c): exactly one %s finding for the outside-own registration; got %+v", tc.code, warnings)
			require.Equal(t, want, matched[0],
				"T-131(c): the doctor finding must be the OF-4 doctor text word for word; got %q", matched[0])
		})
	}
}

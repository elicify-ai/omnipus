package doctor

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

// A native process-edge candidate: any attempt by doctor to start it writes
// a marker immediately. It never enters the test suite or an Office runtime.
func init() {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != "office-doctor-candidate" {
		return
	}
	if os.WriteFile(filepath.Join(filepath.Dir(os.Args[0]), "candidate-started"), []byte("started"), 0600) != nil {
		os.Exit(70)
	}
	os.Exit(0)
}

func officeDoctorComponent(t *testing.T, home string, kind component.Kind) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	self, err := os.Executable()
	require.NoError(t, err)
	// Copy rather than link: the product candidate is wholly test-owned.
	raw, err := os.ReadFile(self)
	require.NoError(t, err)
	candidate := "office-doctor-candidate"
	if strings.HasSuffix(self, ".exe") {
		candidate += ".exe"
	}
	exe := filepath.Join(root, candidate)
	require.NoError(t, os.WriteFile(exe, raw, 0700))
	control := exec.Command(exe)
	require.NoError(t, control.Run(), "positive control: candidate must really execute")
	marker := filepath.Join(root, "candidate-started")
	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "started", string(got))
	require.NoError(t, os.Remove(marker))
	t.Cleanup(func() { require.NoFileExists(t, marker, "doctor must never execute a component") })
	dependency := filepath.Join(root, "dependency.dat")
	require.NoError(t, os.WriteFile(dependency, []byte("registered dependency"), 0600))
	files := make(map[string]string)
	for _, name := range []string{candidate, "dependency.dat"} {
		sum, _, hashErr := component.HashFile(filepath.Join(root, name))
		require.NoError(t, hashErr)
		files[name] = sum
	}
	m := component.Manifest{Schema: component.ManifestSchema, Component: kind, Version: "26.8.0.3", BuildID: "doctor-test", ProtocolVersion: 1, Platform: component.Platform(), Executable: candidate, Files: files, FileListSHA256: component.FileListDigest(files), Trust: component.TrustOperatorRegistered, Root: root, RegisteredBy: "doctor-test"}
	manifest, err := json.Marshal(m)
	require.NoError(t, err)
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), manifest, 0600))
	resolver, err := component.NewResolver(home)
	require.NoError(t, err)
	ready, problem := resolver.Resolve(kind)
	require.Nil(t, problem, "fixture must resolve through the real component verifier")
	require.Equal(t, root, ready.Root)
	require.Equal(t, exe, ready.Executable)
	return root, dependency
}

func officeDoctorWarnings(warnings []warning, code string) []string {
	var matched []string
	for _, w := range warnings {
		if w.code == code {
			matched = append(matched, w.message)
		}
	}
	return matched
}

func officeDoctorRequireProblem(t *testing.T, warnings []warning, code, name, reason string) {
	t.Helper()
	matched := officeDoctorWarnings(warnings, code)
	if len(matched) == 0 {
		t.Fatalf("BLOCKED: doctor %s diagnostic for %s not implemented — required by office-preview AD-4 / architect Retry ruling 2026-11-01 Part 3", reason, name)
	}
	require.Len(t, matched, 1, "one finding for this component")
	require.Contains(t, matched[0], name)
	require.Contains(t, matched[0], reason)
}

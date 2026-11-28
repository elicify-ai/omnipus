// Package presetbuild supplies the added-data-only compiler input required by
// S37/S93. It builds one narrow fixture executable, never a nested Go test.
package presetbuild

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildProbe(t testing.TB, target string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("preset fixture cannot locate its source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overlay.json")
	// The only replaced compiler input is an added JSON file. The actual
	// production //go:embed and Presets loader are neither doubled nor changed.
	data, err := json.Marshal(map[string]any{"Replace": map[string]string{
		filepath.Join(root, "pkg/programs/presets/example.json"): filepath.Join(root, "tests/fixtures/cli-credentials-s1/presets/example.json"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "presetprobe")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-tags", "goolm,stdjson", "-overlay", overlay, "-o", bin, target}
	var build *exec.Cmd
	if _, lookErr := exec.LookPath("heavy"); lookErr == nil {
		build = exec.Command("heavy", append([]string{"--local", "go"}, args...)...)
	} else if os.Getenv("CI") == "true" {
		build = exec.Command("go", args...)
	} else {
		t.Fatal("local preset fixture build requires the shared heavy runner")
	}
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	t.Logf("data-only fixture build: %q", build.Args)
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture build failed (not behavior RED): %v\n%s", err, output)
	}
	return bin
}

func Probe(t testing.TB) []byte {
	t.Helper()
	run := exec.Command(buildProbe(t, "./tests/fixtures/cli-credentials-s1/presetprobe"))
	run.Env = probeEnvironment(t)
	t.Logf("run actual embedded preset probe: %q", run.Args)
	output, err := run.Output()
	if err != nil {
		t.Fatalf("real embedded preset probe failed: %v", err)
	}
	return output
}

// Never inherit real provider keys or the one-shot master-key source: gateway
// boot consumes the latter. Each probe owns a new home and synthetic credentials.
func probeEnvironment(t testing.TB) []string {
	t.Helper()
	var env []string
	for _, name := range []string{"PATH", "SYSTEMROOT", "WINDIR", "PATHEXT", "COMSPEC", "TMP", "TEMP", "TMPDIR", "GOMAXPROCS", "GOMEMLIMIT"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	home := t.TempDir()
	return append(env, "HOME="+home, "USERPROFILE="+home, "OMNIPUS_HOME="+home)
}

// ProbeHTTP drives the actual gateway's login and generated Programs routes.
// The file is an output edge of the test build, not a mocked HTTP response.
func ProbeHTTP(t testing.TB) []byte {
	t.Helper()
	result := filepath.Join(t.TempDir(), "actual-http-result.json")
	run := exec.Command(buildProbe(t, "./tests/fixtures/cli-credentials-s1/presetgateway"), result)
	run.Env = probeEnvironment(t)
	t.Logf("run actual added-preset gateway: %q", run.Args)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("actual added-preset gateway failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("actual gateway probe did not write its generated responses: %v", err)
	}
	return data
}

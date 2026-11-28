package programs

// Test process-edge fake for revision-8 S07/S08/S90/S114/S115/S116/S131.
// The real Service, encrypted store, child runner and masking stay in the unit.
import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

type checkChildCase struct {
	Label  string `json:"Label"`
	Stderr string `json:"Stderr"`
	Exit   int    `json:"Exit"`
	Hold   bool   `json:"Hold"`
}

type checkChildObservation struct {
	Args    []string `json:"Args"`
	Token   string   `json:"Token"`
	Home    string   `json:"Home"`
	Ambient string   `json:"Ambient"`
}

// A copy of the test executable is the fake gh; no shell or paid provider is
// involved, and the real runner's clean environment need not pass a test flag.
func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if name == "gh" || name == "gh.exe" || name == "exampletool" || name == "exampletool.exe" {
		os.Exit(runCheckFixtureChild())
	}
	os.Exit(m.Run())
}

func runCheckFixtureChild() int {
	exe, err := os.Executable()
	if err != nil {
		return 91
	}
	dir := filepath.Dir(exe)
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("EXAMPLE_TOKEN")
	}
	var cases map[string]checkChildCase
	data, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err != nil || json.Unmarshal(data, &cases) != nil {
		return 92
	}
	c, exists := cases[token]
	if !exists {
		return 93
	}
	observation, err := json.Marshal(checkChildObservation{Args: os.Args[1:], Token: token, Home: os.Getenv("HOME"), Ambient: os.Getenv("S1_AMBIENT_SECRET")})
	if err != nil {
		return 94
	}
	if err := os.WriteFile(filepath.Join(dir, token+".observation"), observation, 0o600); err != nil {
		return 95
	}
	_, _ = os.Stdout.WriteString(c.Label + "\n")
	_, _ = os.Stderr.WriteString(c.Stderr)
	if err := os.WriteFile(filepath.Join(dir, token+".started"), []byte("started"), 0o600); err != nil {
		return 96
	}
	for c.Hold {
		if _, err := os.Stat(filepath.Join(dir, token+".release")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c.Exit
}

type checkFixture struct {
	cfg   *config.Config
	store *credentials.Store
	svc   *Service
	bin   string
	state string
}

func newCheckFixture(t *testing.T, cases map[string]checkChildCase) *checkFixture {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)
	body, err := os.ReadFile(exe)
	require.NoError(t, err)
	for _, name := range []string{"gh", "exampletool"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), body, 0o700))
	}
	data, err := json.Marshal(cases)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cases.json"), data, 0o600))
	t.Setenv("PATH", bin)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x36}, 32)))
	t.Cleanup(store.Close)
	cfg := &config.Config{}
	f := &checkFixture{cfg: cfg, store: store, bin: bin, state: filepath.Join(home, "connections.json")}
	f.svc = NewService(func() *config.Config { return f.cfg }, func() (*credentials.Store, error) { return store, nil }, f.state)
	t.Cleanup(func() {
		f.svc.mu.Lock()
		for id := range f.svc.checks {
			f.svc.supersedeLocked(id)
		}
		f.svc.mu.Unlock()
		done := make(chan struct{})
		go func() { f.svc.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("check fixture cleanup did not join its children")
		}
	})
	return f
}

func (f *checkFixture) waitStarted(t *testing.T, token string) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(f.bin, token+".started")); return err == nil }, 5*time.Second, 5*time.Millisecond, "fake check must actually launch")
}

func (f *checkFixture) release(t *testing.T, token string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.bin, token+".release"), []byte("release"), 0o600))
}

func (f *checkFixture) await(t *testing.T, id, state string) Connection {
	t.Helper()
	require.Eventually(t, func() bool { return f.svc.Connection(id).State == state }, 5*time.Second, 5*time.Millisecond, "the real check must reach %s", state)
	f.svc.Wait()
	return f.svc.Connection(id)
}

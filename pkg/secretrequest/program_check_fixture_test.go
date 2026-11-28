package secretrequest_test

// FR-051/S54: fake only the account-check executable and continuation transport.
// Registry, Programs publication/check runner, receipts and recovery are real.
import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if name == "gh" || name == "gh.exe" {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		dir := filepath.Dir(exe)
		token := os.Getenv("GH_TOKEN")
		if token != "tok_valid_12345" {
			os.Exit(92)
		}
		if err := os.WriteFile(filepath.Join(dir, "started"), []byte("started"), 0600); err != nil {
			os.Exit(93)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		_, _ = os.Stdout.WriteString("octocat\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type s1CardCheck struct {
	f   *matrixEnv
	svc *programs.Service
	bin string
}

func s1HeldCardCheck(t *testing.T) *s1CardCheck {
	t.Helper()
	f := s1ProgramsCfg(t)
	bin := t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)
	body, err := os.ReadFile(exe)
	require.NoError(t, err)
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), body, 0700))
	t.Setenv("PATH", bin)
	svc := programs.NewService(func() *config.Config { return f.cfg }, func() (*credentials.Store, error) { return f.store, nil }, filepath.Join(f.home, "connections.json"))
	f.reg.SetProgramPublisher(func(id, value string, receipt *credentials.CardReceipt) (int64, error) {
		conn, err := svc.Publish(id, value, nil, receipt)
		return conn.Generation, err
	})
	c := &s1CardCheck{f: f, svc: svc, bin: bin}
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(bin, "release"), []byte("release"), 0600); c.join(t) })
	return c
}

func (c *s1CardCheck) join(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { c.svc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("real account check did not join")
	}
}

func (c *s1CardCheck) started(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(c.bin, "started")); return err == nil }, 5*time.Second, 5*time.Millisecond, "the actual held check must start")
	require.Equal(t, "checking", c.svc.Connection("github").State)
}

func (c *s1CardCheck) release(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(c.bin, "release"), []byte("release"), 0600))
	c.join(t)
}

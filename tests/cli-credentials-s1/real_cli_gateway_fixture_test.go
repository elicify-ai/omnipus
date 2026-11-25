package clicredentialss1_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func buildActualCLI(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	bin := filepath.Join(t.TempDir(), "omnipus-s1-cli")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-tags", "goolm,stdjson", "-o", bin, "./tests/fixtures/cli-credentials-s1/cligateway"}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("heavy"); err == nil {
		cmd = exec.Command("heavy", append([]string{"--local", "go"}, args...)...)
	} else if os.Getenv("CI") == "true" {
		cmd = exec.Command("go", args...)
	} else {
		t.Fatal("local actual-CLI fixture build requires the shared heavy runner")
	}
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	t.Logf("actual app.Main test build (no production overlay): %q", cmd.Args)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "fixture compiler failure is not behavior RED: %s", output)
	return bin
}

// cliRun owns a real process and caches its wait result. Cleanup may join again
// without consuming a one-shot result channel or racing an output buffer.
type cliRun struct {
	cmd    *exec.Cmd
	output bytes.Buffer
	done   chan struct{}
	err    error
}

func startActualCLI(t *testing.T, bin string, env []string, args ...string) *cliRun {
	t.Helper()
	r := &cliRun{cmd: exec.Command(bin, args...), done: make(chan struct{})}
	r.cmd.Env = env
	r.cmd.Stdout, r.cmd.Stderr = &r.output, &r.output
	require.NoError(t, r.cmd.Start())
	go func() { r.err = r.cmd.Wait(); close(r.done) }()
	t.Cleanup(func() {
		select {
		case <-r.done:
			return
		default:
		}
		_ = r.cmd.Process.Signal(os.Interrupt)
		select {
		case <-r.done:
		case <-time.After(15 * time.Second):
			_ = r.cmd.Process.Kill()
			select {
			case <-r.done:
			case <-time.After(5 * time.Second):
				t.Error("actual CLI/gateway child did not join after kill")
			}
		}
	})
	return r
}

func (r *cliRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-r.done:
		t.Logf("actual CLI exit=%d", r.cmd.ProcessState.ExitCode())
		return r.err
	case <-time.After(12 * time.Second):
		t.Fatal("actual CLI did not return within the fixture join budget")
		return nil
	}
}

type realGatewayFixture struct {
	home, bin, base, starts string
	observe                 string
	env                     []string
	client                  *http.Client
	store                   *credentials.Store
	canary                  string
}

func isolatedCLIEnv(home, bin, toolDir, scratch string) []string {
	var env []string
	for _, name := range []string{"PATH", "SYSTEMROOT", "WINDIR", "PATHEXT", "COMSPEC", "GOMAXPROCS", "GOMEMLIMIT"} {
		if value, exists := os.LookupEnv(name); exists && name != "PATH" {
			env = append(env, name+"="+value)
		}
	}
	// No real provider key, one-shot master-key source, or real user home is
	// inherited by the actual CLI/gateway process. Its temporary folder is the
	// scratch folder inside the data folder, never the host's (see the fake gh
	// note in newRealGatewayFixture).
	return append(env, "HOME="+home, "USERPROFILE="+home, config.EnvHome+"="+home,
		"TMPDIR="+scratch, "TMP="+scratch, "TEMP="+scratch,
		"PATH="+toolDir+string(os.PathListSeparator)+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		credentials.EnvMasterKey+"="+hex.EncodeToString(bytes.Repeat([]byte{0x52}, 32)), "OMNIPUS_BEARER_TOKEN=")
}

func newRealGatewayFixture(t *testing.T, bin string) *realGatewayFixture {
	t.Helper()
	home := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, isTCP := listener.Addr().(*net.TCPAddr)
	require.True(t, isTCP, "the loopback listener reports a TCP address")
	port := tcpAddr.Port
	require.NoError(t, listener.Close())
	hash, err := bcrypt.GenerateFromPassword([]byte("s1-real-person-password"), bcrypt.DefaultCost)
	require.NoError(t, err)
	cfg := &config.Config{Version: 1, Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: port,
		Users: []config.UserConfig{{Username: "s1-real-person", PasswordHash: string(hash)}}}}
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Sandbox.Mode = "off"
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	body, err := os.ReadFile(bin)
	require.NoError(t, err)
	localBinDir := filepath.Join(home, "bin")
	require.NoError(t, os.MkdirAll(localBinDir, 0700))
	bin = filepath.Join(localBinDir, filepath.Base(bin))
	require.NoError(t, os.WriteFile(bin, body, 0700))
	// S-08 / FR-005: the account check does not run a program that lies in a
	// folder agents can write to. That covers the Omnipus data folder (home) and
	// the per-user temporary folder, which the product treats as agent-writable
	// on every host where it is not the shared /tmp
	// (pkg/thirdpartycli/executor_settings.go::runtimeTempRoots). The fake gh
	// therefore lives in its own folder outside the data folder, made here, and
	// the gateway's temporary folder is moved inside the data folder
	// (isolatedCLIEnv): the fake is outside both on any host. The fake writes its
	// gh-starts record next to itself (cligateway/main.go), so that record lives
	// in the same folder.
	toolDir := t.TempDir()
	scratch := filepath.Join(home, "tmp")
	require.NoError(t, os.MkdirAll(scratch, 0700))
	fake := filepath.Join(toolDir, "gh")
	if runtime.GOOS == "windows" {
		fake += ".exe"
	}
	require.NoError(t, os.WriteFile(fake, body, 0700))
	f := &realGatewayFixture{home: home, bin: bin, base: fmt.Sprintf("http://127.0.0.1:%d", port), starts: filepath.Join(toolDir, "gh-starts")}
	f.env = isolatedCLIEnv(home, bin, toolDir, scratch)
	f.observe = t.TempDir()
	gatewayEnv := append(append([]string(nil), f.env...), "S1_CLI_OBSERVE_DIR="+f.observe)
	gateway := startActualCLI(t, bin, gatewayEnv, "gateway", "--allow-empty", "--sandbox", "off")
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	f.client = &http.Client{Jar: jar, Timeout: 10 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, getErr := f.client.Get(f.base + "/health")
		if getErr == nil {
			_ = response.Body.Close()
			break
		}
		select {
		case <-gateway.done:
			t.Fatalf("actual gateway boot ended (setup, not behavior RED): %v\n%s", gateway.err, gateway.output.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("actual gateway did not accept HTTP within the fixture startup budget")
		}
		time.Sleep(20 * time.Millisecond)
	}
	w := f.call(t, http.MethodPost, "/api/v1/auth/login", gen.LoginRequest{Username: "s1-real-person", Password: "s1-real-person-password"})
	require.Equal(t, http.StatusOK, w.status, "%s", w.body)
	w = f.call(t, http.MethodPut, "/api/v1/programs/github/credential", gen.ProgramCredentialRequest{Value: "tok_valid_12345"})
	require.Equal(t, http.StatusOK, w.status, "%s", w.body)
	f.awaitConnected(t, 1, "octocat")
	f.store = credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, f.store.UnlockWithKey(bytes.Repeat([]byte{0x52}, 32)))
	t.Cleanup(f.store.Close)
	f.canary = programstore.SeedCanary(t, f.store)
	// Preserve a real historical program-card receipt through both writers,
	// not just an empty receipt map. A second program entry supplies that
	// receipt so the spec's GitHub Given remains connected at generation 1.
	// Commit through the program boundary before contention, never via Set.
	receipt := &credentials.CardReceipt{RequestID: "s136-program-receipt", SessionID: "s136-program-session", References: []string{programs.TokenReference("fly")}, SubmittedAt: time.Now().UTC()}
	_, err = programs.CommitCardToken(f.store, "fly", "fly_fixture_token_123", receipt)
	require.NoError(t, err)
	require.Positive(t, f.checkStarts(t), "actual gh-start instrument must first observe a real connected check")
	return f
}

// Test transport result, not a parallel gateway wire carrier.
type actualHTTPResult struct {
	status int
	body   []byte
}

func (f *realGatewayFixture) call(t *testing.T, method, path string, payload any) actualHTTPResult {
	t.Helper()
	result, err := f.do(method, path, payload)
	require.NoError(t, err)
	return result
}

func (f *realGatewayFixture) do(method, path string, payload any) (actualHTTPResult, error) {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return actualHTTPResult{}, err
		}
	}
	request, err := http.NewRequest(method, f.base+path, bytes.NewReader(body))
	if err != nil {
		return actualHTTPResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.base)
	base, err := url.Parse(f.base)
	if err != nil {
		return actualHTTPResult{}, err
	}
	for _, cookie := range f.client.Jar.Cookies(base) {
		if cookie.Name == middleware.CSRFCookieName || cookie.Name == middleware.CSRFCookieNameHTTP {
			request.Header.Set(middleware.CSRFHeaderName, cookie.Value)
		}
	}
	response, err := f.client.Do(request)
	if err != nil {
		return actualHTTPResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return actualHTTPResult{status: response.StatusCode, body: raw}, err
}

func (f *realGatewayFixture) entry(t *testing.T) gen.ProgramEntry {
	t.Helper()
	w := f.call(t, http.MethodGet, "/api/v1/programs", nil)
	require.Equal(t, http.StatusOK, w.status, "%s", w.body)
	var list gen.ProgramList
	require.NoError(t, json.Unmarshal(w.body, &list))
	for _, entry := range list.Entries {
		if entry.Id == "github" {
			// The generated collection member is anonymous. Decode it into
			// the generated entry carrier, never a handwritten wire type.
			raw, err := json.Marshal(entry)
			require.NoError(t, err)
			var out gen.ProgramEntry
			require.NoError(t, json.Unmarshal(raw, &out))
			return out
		}
	}
	t.Fatal("actual Programs collection omitted the shipped GitHub preset")
	return gen.ProgramEntry{}
}

func (f *realGatewayFixture) awaitConnected(t *testing.T, generation int64, label string) {
	t.Helper()
	require.Eventually(t, func() bool { return f.entry(t).Connection.State != "checking" }, 25*time.Second, 20*time.Millisecond)
	entry := f.entry(t)
	require.Equal(t, "connected", string(entry.Connection.State))
	require.EqualValues(t, generation, entry.Connection.Generation)
	require.NotNil(t, entry.Connection.AccountLabel)
	require.Equal(t, label, *entry.Connection.AccountLabel)
}

func (f *realGatewayFixture) checkStarts(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(f.starts)
	require.NoError(t, err, "a missing check-start file is not zero observed starts")
	return len(strings.Fields(string(raw)))
}

func (f *realGatewayFixture) clearAdmission(t *testing.T) {
	t.Helper()
	for _, stage := range []string{"admission", "after-read"} {
		err := os.Remove(filepath.Join(f.observe, stage))
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	}
}

func (f *realGatewayFixture) awaitAdmission(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(f.observe, "admission")); return err == nil }, 5*time.Second, 5*time.Millisecond,
		"the actual gateway request must reach store admission before timing a held request; a merely scheduled goroutine is not evidence")
}

func startHeldCLI(t *testing.T, f *realGatewayFixture) (*cliRun, func()) {
	t.Helper()
	dir := t.TempDir()
	env := append(append([]string(nil), f.env...), "S1_CLI_OBSERVE_DIR="+dir, "S1_CLI_HOLD=true")
	cli := startActualCLI(t, f.bin, env, "credentials", "set", "OTHER", "y")
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(dir, "after-read")); return err == nil }, 5*time.Second, 5*time.Millisecond,
		"the ACTUAL CLI must reach the inside-read transaction barrier")
	return cli, func() { require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), []byte("release"), 0600)) }
}

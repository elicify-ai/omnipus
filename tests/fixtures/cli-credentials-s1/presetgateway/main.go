// Test-build executable for S37/S93. The only overlaid product input is one
// preset JSON file; the HTTP server, authentication, routes and checks are real.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if filepath.Base(os.Args[0]) == "exampletool" || filepath.Base(os.Args[0]) == "exampletool.exe" {
		if len(os.Args) != 2 || os.Args[1] != "whoami" || os.Getenv("EXAMPLE_TOKEN") != "example_tok_123456" {
			os.Exit(9)
		}
		fmt.Println("ex-user")
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "fixture requires its result path")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(resultPath string) (err error) {
	// S-08 / FR-005: the account check does not run a program that lies in a
	// folder agents can write to. That covers the Omnipus data folder (home) and
	// the per-user temporary folder, which the product treats as agent-writable
	// on every host where it is not the shared /tmp
	// (pkg/thirdpartycli/executor_settings.go::runtimeTempRoots). S-93 needs the
	// fake exampletool to run, so it lives in its own folder, made first, and the
	// temporary folder of this process is then moved inside the data folder
	// (TMPDIR, TMP, TEMP below): the tool is outside both on any host.
	bin, err := os.MkdirTemp("", "s1-preset-bin-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(bin)
	home, err := os.MkdirTemp("", "s1-preset-http-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	scratch := filepath.Join(home, "tmp")
	if err = os.Mkdir(scratch, 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	name := "exampletool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(bin, name), body, 0700); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), config.EnvHome: home,
		credentials.EnvMasterKey: hex.EncodeToString(bytes.Repeat([]byte{0x52}, 32)), "OMNIPUS_BEARER_TOKEN": "",
		"TMPDIR": scratch, "TMP": scratch, "TEMP": scratch,
	} {
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return fmt.Errorf("fixture listener address is %T, not a TCP address", listener.Addr())
	}
	port := tcpAddr.Port
	if err = listener.Close(); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("s1-preset-person-password"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	cfg := &config.Config{Version: 1, Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: port, Users: []config.UserConfig{{Username: "s1-preset-person", PasswordHash: string(hash)}}}}
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Sandbox.Mode = "off"
	configPath := filepath.Join(home, "config.json")
	if err = config.SaveConfig(configPath, cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- gateway.RunContextWithOptions(ctx, gateway.RunOptions{HomePath: home, ConfigPath: configPath, AllowEmptyStartup: true, SandboxMode: "off"})
	}()
	defer func() {
		cancel()
		select {
		case stopErr := <-done:
			if err == nil && stopErr != nil {
				err = stopErr
			}
		case <-time.After(15 * time.Second):
			err = fmt.Errorf("fixture gateway did not stop: %w", err)
		}
	}()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	baseURL, _ := url.Parse(base)
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, e := client.Get(base + "/health")
		if e == nil {
			_ = response.Body.Close()
			break
		}
		select {
		case bootErr := <-done:
			done <- bootErr
			return fmt.Errorf("fixture gateway boot failed: %w", bootErr)
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("fixture gateway did not accept HTTP")
		}
		time.Sleep(20 * time.Millisecond)
	}
	call := func(method, path, body string, want int, out any) error {
		req, e := http.NewRequest(method, base+path, strings.NewReader(body))
		if e != nil {
			return e
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", base)
		for _, cookie := range jar.Cookies(baseURL) {
			if cookie.Name == middleware.CSRFCookieName || cookie.Name == middleware.CSRFCookieNameHTTP {
				req.Header.Set(middleware.CSRFHeaderName, cookie.Value)
			}
		}
		response, e := client.Do(req)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil {
			return e
		}
		if response.StatusCode != want {
			return fmt.Errorf("actual %s %s: status %d, wanted %d: %s", method, path, response.StatusCode, want, raw)
		}
		if out != nil {
			return json.Unmarshal(raw, out)
		}
		return nil
	}
	if err = call(http.MethodPost, "/api/v1/auth/login", `{"username":"s1-preset-person","password":"s1-preset-person-password"}`, http.StatusOK, nil); err != nil {
		return err
	}
	var list gen.ProgramList
	if err = call(http.MethodGet, "/api/v1/programs", "", http.StatusOK, &list); err != nil {
		return err
	}
	var saved gen.ProgramEntry
	if err = call(http.MethodPut, "/api/v1/programs/example/credential", `{"value":"example_tok_123456"}`, http.StatusOK, &saved); err != nil {
		return err
	}
	deadline = time.Now().Add(25 * time.Second)
	var entry gen.ProgramEntry
	for {
		// The contract exposes a collection GET, not GET /programs/{id}.
		var current gen.ProgramList
		if err = call(http.MethodGet, "/api/v1/programs", "", http.StatusOK, &current); err != nil {
			return err
		}
		found := false
		for _, candidate := range current.Entries {
			if candidate.Id == "example" {
				// The generator emits an inline anonymous list member, not
				// a ProgramEntry alias. Cross via JSON into the generated
				// carrier rather than invent a parallel wire-format type.
				raw, e := json.Marshal(candidate)
				if e != nil {
					return e
				}
				if e = json.Unmarshal(raw, &entry); e != nil {
					return e
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("actual GET /api/v1/programs omitted the added Example preset")
		}
		if string(entry.Connection.State) != "checking" {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Example preset check did not settle in the spec's 25s bound")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Test-build result, not a parallel gateway wire format: members are the
	// actual generated response carriers read above.
	// The json tags keep the keys exactly as the untagged struct encoded them
	// (List, Entry); the reading test decodes by these names.
	raw, err := json.Marshal(struct {
		List  gen.ProgramList  `json:"List"`
		Entry gen.ProgramEntry `json:"Entry"`
	}{list, entry})
	if err != nil {
		return err
	}
	return os.WriteFile(resultPath, raw, 0600)
}

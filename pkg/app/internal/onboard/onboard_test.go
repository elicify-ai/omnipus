// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package onboard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/app/internal/clitoken"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// newBufioReader is a convenience wrapper used by menu tests.
func newBufioReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}

// TestRun_FreshInstall_WritesUsableConfig is the regression guard for
// issue #159.
//
// Before the fix `omnipus onboard` was a print-only stub: it told the user to
// "visit /onboarding in your browser" and exited 0. The Docker entrypoint
// gated container boot on this command, so containers shut down before the
// gateway ever ran and operators with no browser had no way through.
//
// This test drives the new interactive wizard against a scripted stdin (the
// container/headless case) and asserts that the resulting on-disk state is
// what `omnipus gateway` needs to boot without dev_mode_bypass:
//
//   - credentials.json exists and contains the encrypted API key
//   - master.key exists at $HOME/master.key with 0600 (the auto-generate path)
//   - config.json has a provider entry and a default model
//   - config.json has NO account at all (ADR-0008 ruling 2)
//   - state.json has onboarding_complete=true
//   - cli.token exists (mode 0600) and gateway.cli_token is in config
func TestRun_FreshInstall_WritesUsableConfig(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	// Provider menu: "3" selects OpenAI (3rd entry in providerMenu).
	scripted := strings.Join([]string{
		"3", // provider: OpenAI (numbered menu)
		"",  // model (accept default for openai → gpt-4o)
	}, "\n") + "\n"

	passwords := []string{
		"sk-test-key-123456789", // API key — the only hidden input left
	}
	idx := 0

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(scripted),
		stdout: &stdout,
		stderr: &stderr,
		readPassword: func() (string, error) {
			if idx >= len(passwords) {
				return "", io.EOF
			}
			out := passwords[idx]
			idx++
			return out, nil
		},
		// Skip the live provider-key probe: this test exercises the full
		// write path (credentials, config, token, state), not key validation.
		skipVerify: true,
	}

	if err := Run(home, wio); err != nil {
		t.Fatalf("Run failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s", err, stdout.String(), stderr.String())
	}

	// 1. master.key was auto-generated.
	masterKeyPath := filepath.Join(home, "master.key")
	keyInfo, err := os.Stat(masterKeyPath)
	if err != nil {
		t.Fatalf("master.key missing: %v", err)
	}
	if mode := keyInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("master.key perms = %o, want 0600", mode)
	}

	// 2. credentials.json exists and decrypts to the API key under the
	//    expected ref name.
	credPath := filepath.Join(home, "credentials.json")
	if _, statErr := os.Stat(credPath); statErr != nil {
		t.Fatalf("credentials.json missing: %v", statErr)
	}
	store := credentials.NewStore(credPath)
	if unlockErr := credentials.Unlock(store); unlockErr != nil {
		t.Fatalf("re-unlock store: %v", unlockErr)
	}
	gotKey, err := store.Get("openai_api_key")
	if err != nil {
		t.Fatalf("get openai_api_key: %v", err)
	}
	if gotKey != "sk-test-key-123456789" {
		t.Errorf("encrypted api key roundtrip: got %q, want %q", gotKey, "sk-test-key-123456789")
	}

	// 3. config.json has provider + default model, and no account.
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}

	providers, _ := cfg["providers"].([]any)
	if len(providers) == 0 {
		t.Fatalf("providers empty in config.json")
	}
	entry, _ := providers[0].(map[string]any)
	if entry["provider"] != "openai" {
		t.Errorf("provider = %v, want openai", entry["provider"])
	}
	// ADR-067 A-21/FR-022: the wizard's default model comes from the
	// EMBEDDED CATALOG SNAPSHOT — the provider's first active, tool-calling,
	// text model — not from a hand-typed per-provider table. Asserting the
	// PROPERTY rather than a literal slug means a snapshot refresh that
	// retires the old default cannot leave this test asserting a model the
	// runtime would 404 on.
	assertCatalogProbeModel(t, "openai", entry["model"])
	if entry["api_key_ref"] != "openai_api_key" {
		t.Errorf("api_key_ref = %v, want openai_api_key", entry["api_key_ref"])
	}
	if _, hasPlain := entry["api_key"]; hasPlain {
		t.Errorf("config.json contains plaintext api_key field — must use api_key_ref only")
	}

	defaults, _ := cfg["agents"].(map[string]any)["defaults"].(map[string]any)
	if _, has := defaults["model_name"]; has {
		t.Errorf("agents.defaults.model_name must not be written (ADR-068 CRIT-001): %v", defaults["model_name"])
	}
	dm, _ := defaults["default_model"].(map[string]any)
	if dm["provider"] != "openai" {
		t.Errorf("agents.defaults.default_model provider = %v, want openai", dm["provider"])
	}
	if dm["model"] != entry["model"] {
		t.Errorf("agents.defaults.default_model model = %v, want the provider row's %v",
			dm["model"], entry["model"])
	}

	// ADR-0008 ruling 2: the CLI creates no account. gateway.users belongs to
	// the platform sign-in path alone.
	assertNoAccountWritten(t, home)

	// 4. state.json marks onboarding complete.
	mgr := onboarding.NewManager(home)
	if !mgr.IsComplete() {
		t.Errorf("state.json onboarding_complete is false; wizard did not commit")
	}

	// 5. cli.token exists with 0600 and gateway.cli_token is in config.json.
	// EnsureCLIToken writes after mutateConfigFile, so re-read the config
	// rather than reusing the `gateway`/`users` map read earlier.
	tokenPath := clitoken.CLITokenPath(home)
	info, statErr := os.Stat(tokenPath)
	if statErr != nil {
		t.Fatalf("cli.token missing after onboard: %v", statErr)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cli.token perm = %o, want 0600", info.Mode().Perm())
	}
	tok, loadErr := clitoken.LoadCLIToken(home)
	if loadErr != nil {
		t.Fatalf("LoadCLIToken: %v", loadErr)
	}
	if tok == "" {
		t.Error("cli.token is empty")
	}

	raw2, readErr := os.ReadFile(filepath.Join(home, "config.json"))
	if readErr != nil {
		t.Fatalf("re-read config.json: %v", readErr)
	}
	var cfg2 map[string]any
	if jsonErr := json.Unmarshal(raw2, &cfg2); jsonErr != nil {
		t.Fatalf("re-parse config.json: %v", jsonErr)
	}
	gw2, _ := cfg2["gateway"].(map[string]any)
	cliToken, _ := gw2["cli_token"].(map[string]any)
	if cliToken == nil {
		t.Errorf("gateway.cli_token not found in config.json after onboard")
	}
}

// TestRun_AlreadyComplete_NoOp confirms that re-running the wizard after a
// successful first run does not clobber the existing config and exits cleanly.
func TestRun_AlreadyComplete_NoOp(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	// Seed a "completed" state directly.
	if err := os.MkdirAll(filepath.Join(home, "system"), 0o700); err != nil {
		t.Fatal(err)
	}
	mgr := onboarding.NewManager(home)
	if err := mgr.CompleteOnboarding(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
		readPassword: func() (string, error) {
			t.Fatal("readPassword called but wizard should have short-circuited")
			return "", nil
		},
	}

	if err := Run(home, wio); err != nil {
		t.Fatalf("Run on completed install must be a no-op, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "already complete") {
		t.Errorf("expected stdout to mention 'already complete', got: %s", stdout.String())
	}
}

// TestPrompt_UnknownProvider_Rejected confirms that choosing "Other" and
// entering an unknown protocol id is rejected.
func TestPrompt_UnknownProvider_Rejected(t *testing.T) {
	withEdition(t, config.EditionHosted)
	// "7" selects "Other", then we type an unknown provider id.
	scripted := "7\nnonexistent-provider\n"
	wio := wizardIO{
		stdin:        strings.NewReader(scripted),
		stdout:       io.Discard,
		stderr:       io.Discard,
		readPassword: func() (string, error) { t.Fatal("should not reach password prompt"); return "", nil },
	}
	_, err := prompt(wio)
	if err == nil {
		t.Fatal("expected unknown-provider error, got nil")
	}
	if !strings.Contains(err.Error(), `unknown provider "nonexistent-provider"`) {
		t.Errorf("expected an unknown-provider error naming the typed id, got: %v", err)
	}
}

// TestProviderMenu_NumberSelectionReprompt verifies FR-010/US-8:
//   - a valid number selects the correct provider
//   - an out-of-range number re-prompts (does not crash or return an error)
//
// The reprompt path is exercised by feeding "99" (out-of-range), then "0"
// (out-of-range), then "1" (valid — OpenRouter).
func TestProviderMenu_NumberSelectionReprompt(t *testing.T) {
	withEdition(t, config.EditionHosted)
	t.Run("out-of-range re-prompts then valid selects", func(t *testing.T) {
		// "99" → reprompt; "0" → reprompt; "1" → OpenRouter.
		scripted := "99\n0\n1\n"
		var out bytes.Buffer
		reader := strings.NewReader(scripted)

		import_bufio_reader := newBufioReader(reader)
		got, err := promptProviderMenu(&out, import_bufio_reader)
		if err != nil {
			t.Fatalf("promptProviderMenu: %v", err)
		}
		if got != "openrouter" {
			t.Errorf("expected openrouter, got %q", got)
		}
		outStr := out.String()
		// The menu should have been printed 3 times (once per invalid + final).
		if count := strings.Count(outStr, "Select your LLM provider:"); count != 3 {
			t.Errorf("expected menu printed 3 times, got %d; output: %s", count, outStr)
		}
	})

	t.Run("each menu entry selects correct provider", func(t *testing.T) {
		cases := []struct {
			input    string
			wantID   string
			wantName string
		}{
			{"1\n", "openrouter", "OpenRouter"},
			{"2\n", "anthropic", "Anthropic"},
			{"3\n", "openai", "OpenAI"},
			{"4\n", "google", "Google Gemini"},
			{"5\n", "groq", "Groq"},
			{"6\n", "deepseek", "DeepSeek"},
		}
		for _, tc := range cases {
			t.Run(tc.wantName, func(t *testing.T) {
				var out bytes.Buffer
				reader := strings.NewReader(tc.input)
				got, err := promptProviderMenu(&out, newBufioReader(reader))
				if err != nil {
					t.Fatalf("promptProviderMenu for %q: %v", tc.wantName, err)
				}
				if got != tc.wantID {
					t.Errorf("selection %q: got %q, want %q", tc.input, got, tc.wantID)
				}
			})
		}
	})

	t.Run("empty input defaults to openrouter (item 1)", func(t *testing.T) {
		var out bytes.Buffer
		reader := strings.NewReader("\n") // empty line
		got, err := promptProviderMenu(&out, newBufioReader(reader))
		if err != nil {
			t.Fatalf("promptProviderMenu: %v", err)
		}
		if got != "openrouter" {
			t.Errorf("empty input should default to openrouter, got %q", got)
		}
	})
}

// TestOnboard_MintsCLIToken verifies FR-018/US-8 AC-1:
// after onboarding completes, cli.token exists (0600) and a cli user
// is present in config.json with a non-empty token_hash.
func TestOnboard_MintsCLIToken(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	// "1" selects OpenRouter (menu item 1).
	scripted := strings.Join([]string{
		"1", // provider: OpenRouter
		"",  // model default
	}, "\n") + "\n"

	passwords := []string{
		"sk-or-v1-test-key", // API key
	}
	idx := 0

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(scripted),
		stdout: &stdout,
		stderr: &stderr,
		readPassword: func() (string, error) {
			if idx >= len(passwords) {
				return "", io.EOF
			}
			out := passwords[idx]
			idx++
			return out, nil
		},
		// Skip the live provider-key probe: this test exercises token minting,
		// not key validation.
		skipVerify: true,
	}

	if err := Run(home, wio); err != nil {
		t.Fatalf("Run failed: %v\n--- stdout ---\n%s", err, stdout.String())
	}

	// cli.token must exist with mode 0600.
	tokenPath := clitoken.CLITokenPath(home)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("cli.token missing after onboard: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cli.token perm = %o, want 0600", info.Mode().Perm())
	}

	// LoadCLIToken must return a non-empty token.
	tok, err := clitoken.LoadCLIToken(home)
	if err != nil {
		t.Fatalf("LoadCLIToken: %v", err)
	}
	if tok == "" {
		t.Error("cli.token is empty")
	}

	// The CLI token must be in config.json's dedicated gateway.cli_token slot
	// (not a "cli"-named Gateway.Users entry — that model was retired in favor
	// of a standalone Gateway.CLIToken field, decoupled from the human account).
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	gw, _ := cfg["gateway"].(map[string]any)
	cliToken, _ := gw["cli_token"].(map[string]any)
	if cliToken == nil {
		t.Fatalf("gateway.cli_token not found in config.json")
	}
	th, _ := cliToken["hash"].(string)
	if th == "" {
		t.Errorf("gateway.cli_token has empty hash")
	}
	// Verify the hash matches the plaintext token.
	if err := bcrypt.CompareHashAndPassword([]byte(th), []byte(tok)); err != nil {
		t.Errorf("cli_token hash does not match plaintext token: %v", err)
	}

	// Token must not appear in stdout.
	if strings.Contains(stdout.String(), tok) {
		t.Errorf("cli token leaked to stdout — must never be printed")
	}
}

// ── Headless (--non-interactive) ─────────────────────────────────────────────

// TestInputFromFlags_HappyPath checks that a fully-specified flag set
// produces a usable Input without touching stdin.
func TestInputFromFlags_HappyPath(t *testing.T) {
	withEdition(t, config.EditionHosted)
	in, err := inputFromFlags(strings.NewReader(""), inputFlags{
		providerID: "openrouter",
		apiKey:     "sk-or-v1-test",
		model:      "z-ai/glm-5v-turbo",
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.ProviderID != "openrouter" || in.APIKey != "sk-or-v1-test" ||
		in.Model != "z-ai/glm-5v-turbo" {
		t.Errorf("unexpected Input: %+v", in)
	}
}

// TestInputFromFlags_APIKeyStdin confirms that --api-key-stdin reads exactly
// the first line and trims CR/LF. It is the only secret the CLI reads from
// stdin — --admin-password-stdin went with the account (ADR-0008 ruling 2).
func TestInputFromFlags_APIKeyStdin(t *testing.T) {
	withEdition(t, config.EditionHosted)
	stdin := strings.NewReader("sk-secret\r\nsecond-line-must-be-ignored\n")
	in, err := inputFromFlags(stdin, inputFlags{
		providerID:  "openrouter",
		apiKeyStdin: true,
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.APIKey != "sk-secret" {
		t.Errorf("API key not trimmed correctly, got %q", in.APIKey)
	}
}

// TestInputFromFlags_ModelDefault confirms that omitting --model picks the
// per-provider default rather than failing.
func TestInputFromFlags_ModelDefault(t *testing.T) {
	withEdition(t, config.EditionHosted)
	in, err := inputFromFlags(strings.NewReader(""), inputFlags{
		providerID: "anthropic",
		apiKey:     "sk-ant-test",
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	assertCatalogProbeModel(t, "anthropic", in.Model)
}

func TestInputFromFlags_Validation(t *testing.T) {
	withEdition(t, config.EditionHosted)
	cases := []struct {
		name string
		f    inputFlags
		want string // substring of error message
	}{
		{
			name: "missing provider",
			f:    inputFlags{apiKey: "k"},
			want: "--provider is required",
		},
		{
			name: "unknown provider",
			f:    inputFlags{providerID: "not-real", apiKey: "k"},
			want: `unknown provider "not-real"`,
		},
		{
			// ADR-067 FR-019 / T067-12: the wizard applies the SAME admission
			// gate as the gateway, against the EMBEDDED snapshot (A-21) — a
			// cloud-IAM row is refused with the catalog's own reason, rather
			// than written into a config whose first turn cannot construct a
			// provider at all.
			name: "unsupported provider carries the catalog's reason",
			f: inputFlags{
				providerID: "amazon-bedrock",
				apiKey:     "k",
			},
			want: "cloud-iam",
		},
		{
			name: "api-key + api-key-stdin both set",
			f: inputFlags{
				providerID:  "openai",
				apiKey:      "k",
				apiKeyStdin: true,
			},
			want: "exactly one of --api-key or --api-key-stdin",
		},
		{
			name: "missing api key",
			f:    inputFlags{providerID: "openai"},
			want: "--api-key (or --api-key-stdin) is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inputFromFlags(strings.NewReader(""), tc.f)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestRunHeadless_EndToEnd drives the non-interactive Run path against a
// fresh OMNIPUS_HOME and asserts the same on-disk shape as the interactive
// regression test in TestRun_FreshInstall_WritesUsableConfig.
func TestRunHeadless_EndToEnd(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
		readPassword: func() (string, error) {
			t.Fatal("readPassword called but headless path must not prompt")
			return "", nil
		},
	}

	in := Input{
		ProviderID: "openrouter",
		APIKey:     "sk-or-v1-headless-test",
		Model:      "z-ai/glm-5v-turbo",
		// Skip the live provider-key probe: this test exercises the full
		// write path (credentials, config, token, state), not key validation.
		SkipVerify:     true,
		NonInteractive: true,
	}
	if err := RunHeadless(home, wio, in); err != nil {
		t.Fatalf("RunHeadless: %v", err)
	}

	// State.json is committed.
	mgr := onboarding.NewManager(home)
	if !mgr.IsComplete() {
		t.Errorf("state.json onboarding_complete is false after headless run")
	}

	// Credentials store has the API key.
	credPath := filepath.Join(home, "credentials.json")
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("credentials.json missing: %v", err)
	}
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("unlock store: %v", err)
	}
	got, err := store.Get("openrouter_api_key")
	if err != nil {
		t.Fatalf("get api key: %v", err)
	}
	if got != "sk-or-v1-headless-test" {
		t.Errorf("api key roundtrip mismatch, got %q", got)
	}

	// Config.json has the provider row — and no account.
	rawCfg, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		t.Fatal(err)
	}
	provs, _ := cfg["providers"].([]any)
	if len(provs) == 0 {
		t.Fatalf("config.providers empty: %s", rawCfg)
	}
	assertNoAccountWritten(t, home)

	// cli.token must exist (EnsureCLIToken called during applyInput).
	if _, err := os.Stat(clitoken.CLITokenPath(home)); err != nil {
		t.Errorf("cli.token missing after RunHeadless: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Onboarding complete.") {
		t.Errorf("stdout missing completion line, got: %s", out)
	}
	_ = stderr // silence unused
}

// TestRunHeadless_AlreadyComplete confirms the headless path mirrors the
// interactive no-op behavior — never overwriting an existing install.
func TestRunHeadless_AlreadyComplete(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "system"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := onboarding.NewManager(home).CompleteOnboarding(); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			t.Fatal("readPassword called but RunHeadless must short-circuit")
			return "", nil
		},
	}
	in := Input{
		ProviderID: "openrouter",
		APIKey:     "irrelevant",
		Model:      "z-ai/glm-5v-turbo",
	}
	if err := RunHeadless(home, wio, in); err != nil {
		t.Fatalf("RunHeadless on completed install must be a no-op, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "already complete") {
		t.Errorf("expected 'already complete' in stdout, got: %s", stdout.String())
	}
}

// assertCatalogProbeModel checks that got is the FIRST active, tool-calling,
// text model the embedded catalog snapshot lists for providerID — the ADR-067
// FR-022 / A-21 rule, recomputed here from the document rather than read back
// off the implementation, so the assertion has its own oracle.
func assertCatalogProbeModel(t *testing.T, providerID string, got any) {
	t.Helper()

	doc, err := catalog.ParseDocument(catalog.EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("parse embedded snapshot: %v", err)
	}
	want := ""
	for i := range doc.Providers {
		if doc.Providers[i].ID != providerID {
			continue
		}
		for _, m := range doc.Providers[i].Models {
			if m.Status != catalog.StatusActive || !m.ToolCall {
				continue
			}
			hasText := false
			for _, mod := range m.InputModalities {
				if mod == catalog.ModalityText {
					hasText = true
					break
				}
			}
			if hasText {
				want = m.ID
				break
			}
		}
		break
	}
	if want == "" {
		t.Fatalf("the embedded snapshot lists no probe-eligible model for %q", providerID)
	}
	if got != want {
		t.Errorf("model = %v, want %q — the first active tool-calling text model "+
			"the catalog lists for %q", got, want, providerID)
	}
}

// ── LOCAL MODE (WP1.5b, ADR-0010): the admin-account CLI path, restored ────

// TestOnboardCommand_LocalMode_ExposesAdminFlags is the positive mirror of
// no_account_test.go's TestOnboardCommand_ExposesNoAccountFlags: in local
// mode the admin flags ARE registered and the help text documents them.
func TestOnboardCommand_LocalMode_ExposesAdminFlags(t *testing.T) {
	withEdition(t, config.EditionCore)
	cmd := NewOnboardCommand()
	for _, name := range []string{"admin-username", "admin-password", "admin-password-stdin"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("--%s not registered in local mode — this edition's CLI creates the admin account", name)
		}
	}
	if !strings.Contains(cmd.Long, "--admin-") {
		t.Errorf("help text does not document an --admin-* flag in local mode:\n%s", cmd.Long)
	}
	if !strings.Contains(cmd.Short, "admin user") {
		t.Errorf("Short does not advertise the admin user in local mode: %q", cmd.Short)
	}
}

// TestOnboard_LocalMode_CreatesAdminAccount drives the full interactive
// wizard in local mode and asserts the on-disk result carries a bcrypt
// password hash and a bearer-token hash under gateway.users — the exact
// upstream shape, restored (merge base 184d724773789513a4a7fd404596115ea4ec55cf).
func TestOnboard_LocalMode_CreatesAdminAccount(t *testing.T) {
	withEdition(t, config.EditionCore)
	home := t.TempDir()

	// "1" = OpenRouter, "" = default model, then admin username + password.
	scripted := strings.Join([]string{
		"1",     // provider: OpenRouter
		"",      // model default
		"admin", // admin username
	}, "\n") + "\n"

	passwords := []string{
		"sk-or-v1-local-mode-key", // API key
		"s3cr3tpassword",          // admin password
	}
	idx := 0

	var stdout bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(scripted),
		stdout: &stdout,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			if idx >= len(passwords) {
				return "", io.EOF
			}
			out := passwords[idx]
			idx++
			return out, nil
		},
		skipVerify: true,
	}

	if err := Run(home, wio); err != nil {
		t.Fatalf("Run: %v\n--- stdout ---\n%s", err, stdout.String())
	}

	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	gw, _ := cfg["gateway"].(map[string]any)
	users, _ := gw["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("gateway.users has %d row(s), want exactly 1: %v", len(users), users)
	}
	u, ok := users[0].(map[string]any)
	if !ok {
		t.Fatalf("gateway.users[0] is not an object: %v", users[0])
	}
	if u["username"] != "admin" {
		t.Errorf("username = %v, want %q", u["username"], "admin")
	}
	ph, _ := u["password_hash"].(string)
	if ph == "" {
		t.Fatal("password_hash is empty — local mode must mint the admin's password hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(ph), []byte("s3cr3tpassword")); err != nil {
		t.Errorf("password_hash does not match the entered password: %v", err)
	}
	th, _ := u["token_hash"].(string)
	if th == "" {
		t.Error("token_hash is empty — local mode must mint a bearer token alongside the account")
	}

	if out := stdout.String(); !strings.Contains(out, `Log in as "admin"`) {
		t.Errorf("completion text does not confirm the login it just created:\n%s", out)
	}
	if out := stdout.String(); !strings.Contains(out, "Bearer token for") {
		t.Errorf("completion text does not print the one-shot bearer token:\n%s", out)
	}
}

// TestInputFromFlags_LocalMode_RequiresAdminFields pins the headless
// (--non-interactive) validation local mode adds: --admin-username and
// --admin-password (or --admin-password-stdin) are required, and the
// password floor is 8 characters — upstream's rules, restored.
func TestInputFromFlags_LocalMode_RequiresAdminFields(t *testing.T) {
	withEdition(t, config.EditionCore)
	cases := []struct {
		name string
		f    inputFlags
		want string
	}{
		{
			name: "missing admin username",
			f:    inputFlags{providerID: "openai", apiKey: "k"},
			want: "--admin-username is required",
		},
		{
			name: "missing admin password",
			f:    inputFlags{providerID: "openai", apiKey: "k", username: "admin"},
			want: "--admin-password (or --admin-password-stdin) is required",
		},
		{
			name: "weak admin password",
			f:    inputFlags{providerID: "openai", apiKey: "k", username: "admin", password: "short"},
			want: "at least 8 characters",
		},
		{
			name: "admin password + admin password stdin both set",
			f: inputFlags{
				providerID: "openai", apiKey: "k", username: "admin",
				password: "s3cr3tpassword", adminPasswordStdin: true,
			},
			want: "exactly one of --admin-password or --admin-password-stdin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inputFromFlags(strings.NewReader(""), tc.f)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestInputFromFlags_LocalMode_HappyPath pins the valid headless shape,
// including the admin fields platform mode never carries.
func TestInputFromFlags_LocalMode_HappyPath(t *testing.T) {
	withEdition(t, config.EditionCore)
	in, err := inputFromFlags(strings.NewReader(""), inputFlags{
		providerID: "openai",
		apiKey:     "sk-test",
		username:   "admin",
		password:   "s3cr3tpassword",
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.Username != "admin" {
		t.Errorf("Username = %q, want %q", in.Username, "admin")
	}
	if in.Password != "s3cr3tpassword" {
		t.Errorf("Password = %q, want %q", in.Password, "s3cr3tpassword")
	}
}

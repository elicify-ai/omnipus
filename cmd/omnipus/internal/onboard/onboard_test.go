// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package onboard

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
)

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
//   - config.json has a provider entry, default model, and admin user with
//     a non-empty password_hash + token_hash
//   - state.json has onboarding_complete=true
//   - the admin password bcrypt-validates
func TestRun_FreshInstall_WritesUsableConfig(t *testing.T) {
	home := t.TempDir()

	scripted := strings.Join([]string{
		"openai", // provider
		"",       // model (accept default for openai → gpt-4o)
		"alice",  // admin username
	}, "\n") + "\n"

	passwords := []string{
		"sk-test-key-123456789",     // API key
		"correcthorsebatterystaple", // admin password
	}
	idx := 0

	var stdout, stderr bytes.Buffer
	io := wizardIO{
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
	}

	if err := Run(home, io); err != nil {
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

	// 3. config.json has provider + default model + admin user.
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
	if entry["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o (default for openai)", entry["model"])
	}
	if entry["api_key_ref"] != "openai_api_key" {
		t.Errorf("api_key_ref = %v, want openai_api_key", entry["api_key_ref"])
	}
	if _, hasPlain := entry["api_key"]; hasPlain {
		t.Errorf("config.json contains plaintext api_key field — must use api_key_ref only")
	}

	defaults, _ := cfg["agents"].(map[string]any)["defaults"].(map[string]any)
	if defaults["model_name"] != "gpt-4o" {
		t.Errorf("agents.defaults.model_name = %v, want gpt-4o", defaults["model_name"])
	}

	gateway, _ := cfg["gateway"].(map[string]any)
	users, _ := gateway["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	user, _ := users[0].(map[string]any)
	if user["username"] != "alice" {
		t.Errorf("username = %v, want alice", user["username"])
	}
	if user["role"] != "admin" {
		t.Errorf("role = %v, want admin", user["role"])
	}
	pwHash, _ := user["password_hash"].(string)
	if pwHash == "" {
		t.Fatalf("password_hash missing or empty")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(pwHash), []byte("correcthorsebatterystaple")); err != nil {
		t.Errorf("password bcrypt mismatch: %v", err)
	}
	tokenHash, _ := user["token_hash"].(string)
	if tokenHash == "" {
		t.Fatalf("token_hash missing or empty")
	}

	// 4. state.json marks onboarding complete.
	mgr := onboarding.NewManager(home)
	if !mgr.IsComplete() {
		t.Errorf("state.json onboarding_complete is false; wizard did not commit")
	}
}

// TestRun_AlreadyComplete_NoOp confirms that re-running the wizard after a
// successful first run does not clobber the existing config and exits cleanly.
func TestRun_AlreadyComplete_NoOp(t *testing.T) {
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
	io := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
		readPassword: func() (string, error) {
			t.Fatal("readPassword called but wizard should have short-circuited")
			return "", nil
		},
	}

	if err := Run(home, io); err != nil {
		t.Fatalf("Run on completed install must be a no-op, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "already complete") {
		t.Errorf("expected stdout to mention 'already complete', got: %s", stdout.String())
	}
}

// TestPrompt_ShortPassword_Rejected confirms the 8-character minimum is
// enforced at the CLI boundary the same way as at the REST boundary.
func TestPrompt_ShortPassword_Rejected(t *testing.T) {
	scripted := strings.Join([]string{
		"openai", // provider
		"",       // model default
		"alice",  // username
	}, "\n") + "\n"

	passwords := []string{
		"sk-test", // api key (any non-empty is fine for the prompt phase)
		"short",   // password — too short, should be rejected
	}
	idx := 0
	io := wizardIO{
		stdin:  strings.NewReader(scripted),
		stdout: io.Discard,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			if idx >= len(passwords) {
				return "", io.EOF
			}
			out := passwords[idx]
			idx++
			return out, nil
		},
	}

	_, err := prompt(io)
	if err == nil {
		t.Fatal("expected error for password shorter than 8 chars, got nil")
	}
	if !strings.Contains(err.Error(), "8 characters") {
		t.Errorf("expected 8-char minimum error, got: %v", err)
	}
}

// TestPrompt_UnknownProvider_Rejected confirms that the same provider
// validation the REST handler applies is also enforced at the CLI.
func TestPrompt_UnknownProvider_Rejected(t *testing.T) {
	scripted := "nonexistent-provider\n"
	io := wizardIO{
		stdin:        strings.NewReader(scripted),
		stdout:       io.Discard,
		stderr:       io.Discard,
		readPassword: func() (string, error) { t.Fatal("should not reach password prompt"); return "", nil },
	}
	_, err := prompt(io)
	if err == nil {
		t.Fatal("expected unknown-provider error, got nil")
	}
	if !strings.Contains(err.Error(), "known protocol") {
		t.Errorf("expected 'known protocol' error, got: %v", err)
	}
}

// ── Headless (--non-interactive) ─────────────────────────────────────────────

// TestInputFromFlags_HappyPath checks that a fully-specified flag set
// produces a usable Input without touching stdin.
func TestInputFromFlags_HappyPath(t *testing.T) {
	in, err := inputFromFlags(strings.NewReader(""), inputFlags{
		providerID: "openrouter",
		apiKey:     "sk-or-v1-test",
		model:      "z-ai/glm-5v-turbo",
		username:   "admin",
		password:   "correcthorsebattery",
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.ProviderID != "openrouter" || in.APIKey != "sk-or-v1-test" ||
		in.Model != "z-ai/glm-5v-turbo" || in.Username != "admin" ||
		in.Password != "correcthorsebattery" {
		t.Errorf("unexpected Input: %+v", in)
	}
}

// TestInputFromFlags_StdinSecrets confirms that --api-key-stdin and
// --admin-password-stdin read one line each, in that order, and trim CR/LF.
func TestInputFromFlags_StdinSecrets(t *testing.T) {
	stdin := strings.NewReader("sk-secret\r\np@ssw0rd-from-stdin\n")
	in, err := inputFromFlags(stdin, inputFlags{
		providerID:         "openrouter",
		apiKeyStdin:        true,
		username:           "admin",
		adminPasswordStdin: true,
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.APIKey != "sk-secret" {
		t.Errorf("API key not trimmed correctly, got %q", in.APIKey)
	}
	if in.Password != "p@ssw0rd-from-stdin" {
		t.Errorf("password not trimmed correctly, got %q", in.Password)
	}
}

// TestInputFromFlags_ModelDefault confirms that omitting --model picks the
// per-provider default rather than failing.
func TestInputFromFlags_ModelDefault(t *testing.T) {
	in, err := inputFromFlags(strings.NewReader(""), inputFlags{
		providerID: "anthropic",
		apiKey:     "sk-ant-test",
		username:   "admin",
		password:   "longenoughpw",
	})
	if err != nil {
		t.Fatalf("inputFromFlags: %v", err)
	}
	if in.Model != "claude-sonnet-4-6" {
		t.Errorf("expected per-provider default model, got %q", in.Model)
	}
}

func TestInputFromFlags_Validation(t *testing.T) {
	cases := []struct {
		name string
		f    inputFlags
		want string // substring of error message
	}{
		{
			name: "missing provider",
			f:    inputFlags{apiKey: "k", username: "u", password: "longenoughpw"},
			want: "--provider is required",
		},
		{
			name: "unknown provider",
			f:    inputFlags{providerID: "not-real", apiKey: "k", username: "u", password: "longenoughpw"},
			want: "known protocol",
		},
		{
			name: "api-key + api-key-stdin both set",
			f: inputFlags{
				providerID:  "openai",
				apiKey:      "k",
				apiKeyStdin: true,
				username:    "u",
				password:    "longenoughpw",
			},
			want: "exactly one of --api-key or --api-key-stdin",
		},
		{
			name: "admin-password + admin-password-stdin both set",
			f: inputFlags{
				providerID:         "openai",
				apiKey:             "k",
				username:           "u",
				password:           "longenoughpw",
				adminPasswordStdin: true,
			},
			want: "exactly one of --admin-password or --admin-password-stdin",
		},
		{
			name: "missing api key",
			f:    inputFlags{providerID: "openai", username: "u", password: "longenoughpw"},
			want: "--api-key (or --api-key-stdin) is required",
		},
		{
			name: "missing username",
			f:    inputFlags{providerID: "openai", apiKey: "k", password: "longenoughpw"},
			want: "--admin-username is required",
		},
		{
			name: "missing password",
			f:    inputFlags{providerID: "openai", apiKey: "k", username: "u"},
			want: "--admin-password (or --admin-password-stdin) is required",
		},
		{
			name: "short password",
			f:    inputFlags{providerID: "openai", apiKey: "k", username: "u", password: "short"},
			want: "at least 8 characters",
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
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	io := wizardIO{
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
		Username:   "alice",
		Password:   "correcthorsebatterystaple",
	}
	if err := RunHeadless(home, io, in); err != nil {
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

	// Config.json has provider + admin entries; password bcrypt-validates.
	rawCfg, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		t.Fatal(err)
	}
	gw, _ := cfg["gateway"].(map[string]any)
	users, _ := gw["users"].([]any)
	if len(users) == 0 {
		t.Fatalf("config.gateway.users empty: %s", rawCfg)
	}
	user, _ := users[0].(map[string]any)
	hash, _ := user["password_hash"].(string)
	if hash == "" {
		t.Fatalf("password_hash empty")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)); err != nil {
		t.Errorf("password_hash does not bcrypt-validate against the headless password: %v", err)
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
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "system"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := onboarding.NewManager(home).CompleteOnboarding(); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	io := wizardIO{
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
		Username:   "alice",
		Password:   "longenoughpw",
	}
	if err := RunHeadless(home, io, in); err != nil {
		t.Fatalf("RunHeadless on completed install must be a no-op, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "already complete") {
		t.Errorf("expected 'already complete' in stdout, got: %s", stdout.String())
	}
}

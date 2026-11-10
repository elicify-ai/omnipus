package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// WU-24 CHECK F1: the worker-owned reader survives the model-transport
// removal. These seven cases restore the original reader protections only;
// the deleted model-provider token-source mechanism stays deleted.
func codexReaderTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", filepath.Join(home, "omnipus"))
	dir := filepath.Join(home, ".codex")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CodexHomeEnvVar, dir)
	return filepath.Join(dir, "auth.json")
}

func writeCodexReaderFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadCodexCliCredentials_IgnoresRefreshTokenAndLeavesFileUntouched(t *testing.T) {
	path := codexReaderTestHome(t)
	original := `{"tokens":{"access_token":"at","refresh_token":"rt-must-be-ignored","account_id":"acct"}}`
	writeCodexReaderFixture(t, path, original)
	// A fixed, old timestamp makes even a same-bytes rewrite detectable;
	// compare the timestamp actually stored by this platform's filesystem.
	fixed := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	tokensType := reflect.TypeOf(CodexCliAuth{}.Tokens)
	if _, exists := tokensType.FieldByName("RefreshToken"); exists {
		t.Fatal("worker reader must not model the vendor refresh token")
	}
	for i := 0; i < tokensType.NumField(); i++ {
		if tokensType.Field(i).Tag.Get("json") == "refresh_token" {
			t.Fatal("worker reader must not bind the vendor refresh-token JSON field")
		}
	}

	token, account, expiry, err := ReadCodexCliCredentials()
	if err != nil {
		t.Fatalf("read temporary vendor login: %v", err)
	}
	if token != "at" || account != "acct" {
		t.Errorf("read = (%q, %q), want (at, acct)", token, account)
	}
	if want := before.ModTime().Add(time.Hour); !expiry.Equal(want) {
		t.Errorf("expiry = %v, want file mtime plus one hour %v", expiry, want)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("auth.json mtime changed: %v -> %v", before.ModTime(), after.ModTime())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(original)) {
		t.Errorf("auth.json bytes changed: got %q, want %q", got, original)
	}
}

func TestReadCodexCliCredentials_Valid(t *testing.T) {
	path := codexReaderTestHome(t)
	writeCodexReaderFixture(t, path, `{"tokens":{"access_token":"test-access-token","refresh_token":"ignored","account_id":"org-test123"}}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	token, account, expiry, err := ReadCodexCliCredentials()
	if err != nil {
		t.Fatalf("valid reader fixture: %v", err)
	}
	if token != "test-access-token" || account != "org-test123" {
		t.Errorf("read = (%q, %q), want (test-access-token, org-test123)", token, account)
	}
	if want := before.ModTime().Add(time.Hour); !expiry.Equal(want) {
		t.Errorf("expiry = %v, want exact file mtime plus one hour %v", expiry, want)
	}
}

func TestReadCodexCliCredentials_MissingFile(t *testing.T) {
	path := codexReaderTestHome(t)
	token, account, expiry, err := ReadCodexCliCredentials()
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v, want os.ErrNotExist", err)
	}
	if !strings.HasPrefix(err.Error(), "reading "+path+": ") {
		t.Errorf("missing-file error does not identify the reader path: %v", err)
	}
	if token != "" || account != "" || !expiry.IsZero() {
		t.Errorf("missing file returned credential values: (%q, %q, %v)", token, account, expiry)
	}
}

func TestReadCodexCliCredentials_EmptyToken(t *testing.T) {
	path := codexReaderTestHome(t)
	writeCodexReaderFixture(t, path, `{"tokens":{"access_token":"","refresh_token":"r","account_id":"a"}}`)
	token, account, expiry, err := ReadCodexCliCredentials()
	if err == nil || err.Error() != "no access_token in "+path {
		t.Fatalf("empty-token error = %v, want no access_token in the fixture path", err)
	}
	if token != "" || account != "" || !expiry.IsZero() {
		t.Errorf("empty token returned credential values: (%q, %q, %v)", token, account, expiry)
	}
}

func TestReadCodexCliCredentials_InvalidJSON(t *testing.T) {
	path := codexReaderTestHome(t)
	writeCodexReaderFixture(t, path, "{")
	token, account, expiry, err := ReadCodexCliCredentials()
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) {
		t.Fatalf("malformed login error = %T (%v), want JSON syntax error", err, err)
	}
	if !strings.HasPrefix(err.Error(), "parsing "+path+": ") {
		t.Errorf("parse error does not identify the fixture path: %v", err)
	}
	if token != "" || account != "" || !expiry.IsZero() {
		t.Errorf("malformed JSON returned credential values: (%q, %q, %v)", token, account, expiry)
	}
}

func TestReadCodexCliCredentials_NoAccountID(t *testing.T) {
	path := codexReaderTestHome(t)
	writeCodexReaderFixture(t, path, `{"tokens":{"access_token":"tok123","refresh_token":"ignored"}}`)
	token, account, _, err := ReadCodexCliCredentials()
	if err != nil {
		t.Fatalf("account id is optional: %v", err)
	}
	if token != "tok123" || account != "" {
		t.Errorf("read = (%q, %q), want (tok123, empty account)", token, account)
	}
}

func TestReadCodexCliCredentials_CodexHomeEnv(t *testing.T) {
	defaultPath := codexReaderTestHome(t)
	writeCodexReaderFixture(t, defaultPath, `{"tokens":{"access_token":"wrong-default-token"}}`)
	customDir := t.TempDir()
	writeCodexReaderFixture(t, filepath.Join(customDir, "auth.json"), `{"tokens":{"access_token":"custom-token","account_id":"custom-account"}}`)
	t.Setenv(CodexHomeEnvVar, customDir)
	token, account, _, err := ReadCodexCliCredentials()
	if err != nil {
		t.Fatalf("custom worker login directory: %v", err)
	}
	if token != "custom-token" || account != "custom-account" {
		t.Errorf("CODEX_HOME read = (%q, %q), want (custom-token, custom-account)", token, account)
	}
}

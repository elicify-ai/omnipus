package audit_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestGitSecSecretScan_BlocksRegisteredValue proves MIN-5's core requirement:
// a secret present in the credential-store registry is caught on the commit
// path, so the auto-commit is refused (Clean == false).
func TestGitSecSecretScan_BlocksRegisteredValue(t *testing.T) {
	const secret = "sup3r-secret-registered-credential-value"
	// This is exactly what config.Config.SensitiveDataReplacer() returns.
	replacer := strings.NewReplacer(secret, "[FILTERED]")

	sc, err := audit.NewSecretScanner(replacer, nil)
	if err != nil {
		t.Fatalf("NewSecretScanner: %v", err)
	}

	content := []byte("export API_TOKEN=" + secret + "\nother=fine\n")
	findings := sc.ScanBytes("config.env", content)
	if len(findings) == 0 {
		t.Fatal("registered secret not detected")
	}
	var sawRegistry bool
	for _, f := range findings {
		if f.Kind == "registered_credential" {
			sawRegistry = true
		}
		// Findings must never echo the secret value.
		if strings.Contains(f.Detail, secret) || strings.Contains(f.Path, secret) {
			t.Errorf("finding leaked the secret value: %+v", f)
		}
	}
	if !sawRegistry {
		t.Errorf("expected a registered_credential finding, got %+v", findings)
	}

	res := sc.ScanCommit(map[string][]byte{"config.env": content})
	if res.Clean {
		t.Fatal("ScanCommit must NOT be clean when a registered secret is present (MIN-5 fail-closed)")
	}
	rep := res.Report()
	if strings.Contains(rep, secret) {
		t.Fatalf("Report leaked the secret value: %s", rep)
	}
	if !strings.Contains(rep, "purge") {
		t.Errorf("Report should surface the purge/gc procedure, got: %s", rep)
	}
	t.Logf("SCAN RESULT: clean=%v findings=%d report=%q", res.Clean, len(res.Findings), rep)
}

// TestGitSecSecretScan_DetectsFormatPatterns proves the second layer: secrets
// the agent obtained at runtime and wrote to a file (never registered) are
// still caught by the reused format patterns.
func TestGitSecSecretScan_DetectsFormatPatterns(t *testing.T) {
	sc, err := audit.NewSecretScanner(nil, nil) // no registry — patterns only
	if err != nil {
		t.Fatalf("NewSecretScanner: %v", err)
	}
	cases := []struct {
		name    string
		content string
	}{
		{"openai", "key: sk-abcdefghij0123456789ABCDEFGHIJ"},
		{"github", "GITHUB_TOKEN=ghp_0123456789abcdefghij0123456789ABCDwx"},
		{"aws", "aws_access_key_id = AKIA0123456789ABCDEF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := sc.ScanBytes("f.txt", []byte(c.content))
			if len(findings) == 0 {
				t.Fatalf("expected a credential_pattern finding for %s", c.name)
			}
			for _, f := range findings {
				if f.Kind != "credential_pattern" {
					t.Errorf("unexpected finding kind %q", f.Kind)
				}
				if f.Line != 1 {
					t.Errorf("Line = %d, want 1", f.Line)
				}
			}
		})
	}
}

func TestGitSecSecretScan_CleanContent(t *testing.T) {
	replacer := strings.NewReplacer("somesecret", "[FILTERED]")
	sc, err := audit.NewSecretScanner(replacer, nil)
	if err != nil {
		t.Fatalf("NewSecretScanner: %v", err)
	}
	res := sc.ScanCommit(map[string][]byte{
		"main.go":   []byte("package main\nfunc main() {}\n"),
		"README.md": []byte("# Hello\nno secrets here\n"),
	})
	if !res.Clean {
		t.Fatalf("benign content must be clean, got findings: %+v", res.Findings)
	}
	if res.Report() != "" {
		t.Errorf("clean result Report() must be empty, got %q", res.Report())
	}
}

// TestGitSecSecretScan_WrittenThenDeleted models the MIN-5 BDD scenario: the
// scanner runs on the commit that INTRODUCES the secret (before it can enter
// tamper-evident history), so a later delete-commit cannot bury it.
func TestGitSecSecretScan_WrittenThenDeleted(t *testing.T) {
	const secret = "leaked-token-abcdef0123456789"
	replacer := strings.NewReplacer(secret, "[FILTERED]")
	sc, err := audit.NewSecretScanner(replacer, nil)
	if err != nil {
		t.Fatalf("NewSecretScanner: %v", err)
	}

	// Boundary commit #1 adds the secret file — must be caught here.
	c1 := sc.ScanCommit(map[string][]byte{"creds.txt": []byte("TOKEN=" + secret)})
	if c1.Clean {
		t.Fatal("commit introducing the secret must be flagged (blocked before history)")
	}

	// A later commit that only records the file's deletion carries no content;
	// nothing to scan, and history never received the secret because #1 was
	// refused.
	c2 := sc.ScanCommit(map[string][]byte{})
	if !c2.Clean {
		t.Errorf("empty write-set should be clean, got %+v", c2.Findings)
	}
}

func TestGitSecSecretScan_CustomPatternError(t *testing.T) {
	if _, err := audit.NewSecretScanner(nil, []string{"("}); err == nil {
		t.Fatal("invalid custom pattern must return an error")
	}
}

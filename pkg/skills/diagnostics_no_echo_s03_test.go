package skills

// Validation diagnostics must not echo skill content. Frontmatter and a
// SKILL.md link target are skill content the secret check (§7.4) has not
// scanned yet when validation refuses, so a refusal that quotes them could
// carry a stored secret value into the tool result, the REST body, the audit
// line and the logs.
//
// Failability: before commit 486ebc9fd the refusal text quoted the YAML
// parser's message (which quotes scalar text) and the symlink target.

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const s03Secret = "s03-stored-secret-value-7c1e"

// s03AuditDir wires a real audit logger to a temp dir and returns a function
// that returns everything written there so far.
func s03AuditDir(t *testing.T) func() string {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	prev := auditLogger.Load()
	SetAuditLogger(logger)
	t.Cleanup(func() {
		SetAuditLogger(prev)
		_ = logger.Close()
	})
	return func() string {
		var sb strings.Builder
		entries, readErr := os.ReadDir(dir)
		require.NoError(t, readErr)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, fileErr := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, fileErr)
			sb.Write(b)
		}
		return sb.String()
	}
}

func TestFrontmatterRefusal_NeverEchoesContent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		frontmatter string
		class       string
		noLine      bool
	}{
		{"type error quoting the scalar", "name: foo\ndescription: !!int " + s03Secret, "syntax error", true},
		{"duplicate key quoting the key", "name: foo\ndescription: d\n" + s03Secret + ": 1\n" + s03Secret + ": 2", "", false},
		{"syntax error with the value on the line", "name: foo\ndescription: [" + s03Secret + "\nmetadata: {", "syntax error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			appDir := writeSkillFixture(t, root, "foo")
			withSecrets(t, namedSecrets([2]string{"S03_TOKEN", s03Secret}))
			auditText := s03AuditDir(t)

			_, err := ApplyChange(root, ChangeRequest{
				Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
				Content: []byte("---\n" + tc.frontmatter + "\n---\n\nbody\n"),
				Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
				Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
			})
			require.Error(t, err, "malformed frontmatter is refused")
			msg := err.Error()
			wantForm := `not valid YAML \(line \d+: `
			if tc.noLine {
				// yaml.v3 reports a scalar decode failure with no line number;
				// the reason is then the bare error class (observed behaviour).
				wantForm = `not valid YAML \(`
			}
			assert.Regexp(t, regexp.MustCompile(wantForm), msg,
				"the reason names the line and the error class only")
			if tc.class != "" {
				assert.Contains(t, msg, tc.class)
			}
			assert.NotContains(t, msg, s03Secret, "the refusal text never carries the value")
			assert.NotContains(t, auditText(), s03Secret, "the audit line never carries the value")
			assert.Regexp(t, regexp.MustCompile(wantForm), auditText(), "the audit reason has the structural form")
		})
	}
}

func TestSymlinkedSkillMD_RefusalNeverEchoesTheTarget(t *testing.T) {
	if _, probeErr := os.Readlink(os.TempDir()); probeErr == nil {
		t.Skip("unexpected: temp dir is a link")
	}
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))
	target := "/nonexistent/" + s03Secret + "/SKILL.md"
	if err := os.Symlink(target, filepath.Join(appDir, "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rev := readLiveRevision(t, appDir)
	withSecrets(t, namedSecrets([2]string{"S03_TOKEN", s03Secret}))
	auditText := s03AuditDir(t)

	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "notes.md", Content: []byte("notes\n"),
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.Error(t, err, "a SKILL.md link that does not resolve to a regular file is refused")
	assert.False(t, errors.Is(err, ErrSecretInSkill), "refused by validation, not by the secret scan")
	assert.Contains(t, err.Error(), "symlink", "the refusal still explains the problem")
	assert.NotContains(t, err.Error(), s03Secret, "the link target text is never echoed")
	assert.NotContains(t, auditText(), s03Secret, "the audit line never carries the target")
}

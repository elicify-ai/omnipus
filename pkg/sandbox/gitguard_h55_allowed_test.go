package sandbox_test

// H55 (#1960 S1), the rows of the architect's ruling
// (/workspace/logs/1960/spec-s1/ruling-H55.md) that must stay green: R7 (the
// allowed push idioms) and the ruling's "only while a work folder is registered".
// They hold on the guard as it is today and must still hold when the ruling's
// Rule A (receive-side override) and Rule B (push target) land, which is what
// catches a fix that is too broad. The refusal rows are in
// gitguard_h55_refusals_test.go.
//
// Oracles: ruling-H55 section (1), "H54 idioms that must stay allowed" and the
// network-URL / plain-name clause of Rule B; section (3) rows R7 and R8.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// h55Program is the receiving-side program the ruling's rows name.
const h55Program = "git -c receive.denyCurrentBranch=ignore receive-pack"

// h55Layout is the layout of the ruling's rows: a protected evidence work folder
// and an approved mount beside it, the way the H54 unit rows lay them out.
type h55Layout struct {
	work, mount string
	guard       *sandbox.GitGuard
}

// h55Setup registers the work folder and shows, in this very layout, that the
// guard is armed: the literal-path form F1 pins is refused. Without that an
// "allowed" decision could come from a guard that protects nothing.
func h55Setup(t *testing.T) h55Layout {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	work := filepath.Join(ws, "work")
	mount := filepath.Join(ws, "m")
	for _, dir := range []string{filepath.Join(work, ".git"), filepath.Join(mount, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := sandbox.NewGitGuard()
	if err := g.Protect(work); err != nil {
		t.Fatal(err)
	}
	if d := g.InspectExec([]string{"sh", "-c", "git -C " + work + " commit --allow-empty -m x"}, mount); d.Allowed {
		t.Fatal("instrument: the guard must refuse the literal-path commit in this layout, or an allowed decision proves nothing")
	}
	return h55Layout{work: work, mount: mount, guard: g}
}

// R7: ordinary pushes from the mount are not an evidence mutation. Every form
// names its remote as a plain name or a network address, never as a path.
func TestGitGuard_H55_PushIdiomsStayAllowed(t *testing.T) {
	l := h55Setup(t)
	scripts := []struct{ name, script string }{
		{"R7_push_branch_variable", `git push origin "$BRANCH"`},
		{"R7_push_bare_remote_variable", `REMOTE=origin; git push "$REMOTE" main`},
		{"R7_push_bare_remote_variable_unquoted", `git push $REMOTE main`},
		{"R7_commit_message_question_mark", `git commit -m "why?"`},
		{"R7_push_with_C_to_the_mount", "git -C " + l.mount + " push origin HEAD"},
		{"R7_s20_push", "git push origin HEAD:refs/heads/s20"},
		{"R7_push_options_before_the_remote_name", `git push -u origin "$BRANCH"`},
		{"R7_push_lease_option_before_the_remote_name", `git push --force-with-lease origin "$BRANCH"`},
		{"R7_push_https_url", "git push https://github.com/org/repo.git HEAD:refs/heads/s20"},
		{"R7_push_ssh_url", "git push ssh://git@github.com/org/repo.git HEAD:refs/heads/s20"},
		{"R7_push_scp_style_url", "git push git@github.com:org/repo.git HEAD:refs/heads/s20"},
	}
	for _, tc := range scripts {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55 R7: %q from the mount must stay allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}

	// The same idioms as a direct git argv (R8's parity, for the allowed side).
	direct := []struct {
		name string
		argv []string
	}{
		{"R8_push_named_remote", []string{"git", "push", "origin", "HEAD:refs/heads/s20"}},
		{"R8_push_https_url", []string{"git", "push", "https://github.com/org/repo.git", "HEAD:refs/heads/s20"}},
		{"R8_push_with_C_to_the_mount", []string{"git", "-C", l.mount, "push", "origin", "HEAD"}},
	}
	for _, tc := range direct {
		t.Run("argv/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec(tc.argv, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55 R8: %q from the mount must stay allowed, got refused: %s (%s)", tc.argv, d.Reason, d.PolicyRule)
			}
		})
	}
}

// The ruling applies its rules "only while a work folder is registered": with
// nothing protected, the guard has nothing to refuse for.
func TestGitGuard_H55_RulesAreOffWithoutAProtectedWorkFolder(t *testing.T) {
	free := sandbox.NewGitGuard()
	mount := t.TempDir()
	scripts := []string{
		"git push --receive-pack='" + h55Program + "' ev master",
		"git push " + filepath.Join(mount, "work") + " HEAD:refs/heads/x",
	}
	for _, script := range scripts {
		if d := free.InspectExec([]string{"sh", "-c", script}, mount); !d.Allowed {
			t.Fatalf("ruling-H55: no protected work folder is registered, so %q must be allowed, got refused: %s", script, d.Reason)
		}
	}
}

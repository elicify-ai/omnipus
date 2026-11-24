package sandbox

// Developer unit rows for the H55 push rules (gitguard_push.go): edges of
// Rule A and Rule B that the author's rows (gitguard_h55_*_test.go) do not name.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type pushRuleLayout struct {
	ws, work, mount string
	g               *GitGuard
}

func newPushRuleLayout(t *testing.T) pushRuleLayout {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	work := filepath.Join(ws, "work")
	mount := filepath.Join(ws, "m")
	for _, d := range []string{filepath.Join(work, ".git"), filepath.Join(mount, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := NewGitGuard()
	if err := g.Protect(work); err != nil {
		t.Fatal(err)
	}
	return pushRuleLayout{ws: ws, work: work, mount: mount, g: g}
}

func TestGitGuardPushRules_ShellRefusals(t *testing.T) {
	l := newPushRuleLayout(t)
	rows := []struct{ name, script, rule string }{
		{"repo_option_glued_names_the_work_path", "git push --repo=" + l.work + " HEAD:refs/heads/x", "push target"},
		{"repo_option_spaced_relative", "git push --repo ../work HEAD:refs/heads/x", "push target"},
		{"exec_after_the_refspec", `git push origin "$BRANCH" --exec=x`, "receive-side override (H55)"},
		{"second_command_after_a_separator", "git status; git push ../work HEAD:refs/heads/x", "push target"},
		{"after_and_and", `true && git push "$(dirname $PWD)/work" HEAD:refs/heads/x`, "push target"},
		{"global_C_before_push_then_computed_path", "git -C " + l.mount + ` push "$D/work" HEAD:x`, "push target"},
		{"config_env_receive_setting", "git --config-env=receive.denyCurrentBranch=V push ev master", "receive-side override (H55)"},
		{"glued_c_receive_setting", "git -creceive.denyCurrentBranch=ignore push ev master", "receive-side override (H55)"},
		{"upper_case_override_option", "git push --Receive-Pack=x ev master", "receive-side override (H55)"},
		{"file_url_with_localhost", "git push file://localhost" + l.work + " HEAD:x", "push target"},
		{"relative_dot_path_variable", `git push "./$X" HEAD:x`, "push target"},
		{"send_pack_relative_work_path", "git send-pack ../work HEAD:x", "push target"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			d := l.g.InspectExec([]string{"bash", "-c", tc.script}, l.mount)
			if d.Allowed {
				t.Fatalf("%q must be refused", tc.script)
			}
			if !strings.Contains(d.PolicyRule, tc.rule) {
				t.Fatalf("%q refused by %q, want a rule naming %q", tc.script, d.PolicyRule, tc.rule)
			}
		})
	}
}

func TestGitGuardPushRules_ShellAllowed(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, script := range []string{
		`git push "https://$HOST/org/repo.git" main`,
		`git push git@github.com:$ORG/repo.git main`,
		`git push "$REMOTE" "$BRANCH"`,
		`git push -o ci.skip origin main`,
		`git push --push-option ci.skip origin main`,
		`git push origin main 2>&1`,
		`git push`,
		`git push w* main`,
		`git ls-remote "$(dirname $PWD)/work"`,
		`git fetch origin && git push origin HEAD:refs/heads/feature`,
	} {
		if d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount); !d.Allowed {
			t.Errorf("%q must stay allowed, got %s (%s)", script, d.Reason, d.PolicyRule)
		}
	}
}

func TestGitGuardPushRules_DirectArgv(t *testing.T) {
	l := newPushRuleLayout(t)
	refused := [][]string{
		{"git", "-C", "..", "push", "work", "HEAD:x"}, // bare name resolved against the -C directory
		{"git", "push", "--repo=" + l.work, "HEAD:x"}, // --repo names the work folder
		{"git", "push", "origin", "main", "--exec=x"}, // --exec after the operands
		{"git", "--config-env", "receive.x=V", "push", "ev"},
		{"git", "-c=receive.denyCurrentBranch=ignore", "push", "ev", "master"},
		{"git", "send-pack", "file://" + l.work, "HEAD:x"},
	}
	for _, argv := range refused {
		if d := l.g.InspectExec(argv, l.mount); d.Allowed {
			t.Errorf("%q must be refused", argv)
		}
	}
	allowed := [][]string{
		{"git", "push", "-o", "ci.skip", "origin", "main"},
		{"git", "push", "git@github.com:org/repo.git", "main"},
		{"git", "-C", l.mount, "log", "--oneline"},
		{"git", "-c", "user.name=x", "push", "origin", "main"},
	}
	for _, argv := range allowed {
		if d := l.g.InspectExec(argv, l.mount); !d.Allowed {
			t.Errorf("%q must stay allowed, got %s (%s)", argv, d.Reason, d.PolicyRule)
		}
	}
}

func TestGitGuardPushRules_OffWithoutProtectedWorkFolder(t *testing.T) {
	g := NewGitGuard()
	dir := t.TempDir()
	for _, argv := range [][]string{
		{"git", "push", "--exec=x", "ev", "master"},
		{"git", "-c", "receive.x=1", "push", "ev"},
		{"bash", "-c", "GIT_CONFIG_COUNT=1 git push ../work HEAD:x"},
	} {
		if d := g.InspectExec(argv, dir); !d.Allowed {
			t.Errorf("%q must be allowed with nothing protected, got %s", argv, d.PolicyRule)
		}
	}
}

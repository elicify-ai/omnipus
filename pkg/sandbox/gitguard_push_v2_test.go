package sandbox

// Developer unit rows for H55 v2 (ruling-H55-v2.md, review h55-review.md):
// F2 abbreviations and quote splitting, F3 operand parsing, F4 scope, plus the
// controls that must stay allowed.

import (
	"strings"
	"testing"
)

const h55v2Override = "receive-side override (H55)"

func TestGitGuardPushRulesV2_ShellRuleAAbbreviationsAndQuoteSplitting(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, script := range []string{
		`git push --rece='x' ev master`,
		`git push --recei=x ev master`,
		`git push --ex=x ev master`,
		`git push --exe x ev master`,
		`git send-pack --ex=x ev master`,
		`git push --rece='git -c rec""eive.denyCurrentBranch=ignore receive-pa""ck' ev HEAD:master`,
		`git push --rec\eive-pack=x ev master`,
		`git -c alias.p='push --rece=x' p`,
		`git -c alias.p='push --ex=x' p ev master`,
		`git -c "rece"ive.denyCurrentBranch=ignore push ev master`,
		`if true; then git push --rece=x ev master; fi`,
		`sudo git push --exe x ev master`,
		`/usr/bin/git push --rece=x ev master`,
		`git log --grep=receive-pack`, // accepted refusal: a git command naming the marker
	} {
		d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount)
		if d.Allowed || !strings.Contains(d.PolicyRule, h55v2Override) {
			t.Errorf("%q must be refused by Rule A, got allowed=%t rule=%q", script, d.Allowed, d.PolicyRule)
		}
	}
}

func TestGitGuardPushRulesV2_RefusalNamesTheMarker(t *testing.T) {
	l := newPushRuleLayout(t)
	d := l.g.InspectExec([]string{"bash", "-c", `git log --grep=receive-pack`}, l.mount)
	if d.Allowed || !strings.Contains(d.Reason, "'receive-pack'") {
		t.Fatalf("the refusal must name the marker, got allowed=%t reason=%q", d.Allowed, d.Reason)
	}
	d = l.g.InspectExec([]string{"bash", "-c", `git push --exe x ev`}, l.mount)
	if d.Allowed || !strings.Contains(d.Reason, "'--exe'") {
		t.Fatalf("the refusal must name the --ex abbreviation, got allowed=%t reason=%q", d.Allowed, d.Reason)
	}
}

func TestGitGuardPushRulesV2_DirectArgvRuleA(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, argv := range [][]string{
		{"git", "push", "--rece=x", "ev", "m"},
		{"git", "push", "--exe", "x", "ev"},
		{"git", "push", "--ex=x", "ev"},
		{"git", "-c", "alias.p=push --rece=x", "p"},
		{"git", "push", "--rece=git -c rec\"\"eive.denyCurrentBranch=ignore receive-pa\"\"ck", "ev", "m"},
	} {
		d := l.g.InspectExec(argv, l.mount)
		if d.Allowed || !strings.Contains(d.PolicyRule, h55v2Override) {
			t.Errorf("%q must be refused by Rule A, got allowed=%t rule=%q", argv, d.Allowed, d.PolicyRule)
		}
	}
}

func TestGitGuardPushRulesV2_RuleBOperandParsing(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, script := range []string{
		`git push $(dirname $(pwd))/work HEAD:refs/heads/yy`,
		`git push $(dirname "$(pwd)")/work HEAD:refs/heads/yy`,
		"git push `dirname \\`pwd\\``/work HEAD:refs/heads/yy",
		`git push --rep="$(dirname $PWD)/work" HEAD:refs/heads/x`,
		`git push --rep "$(dirname $PWD)/work" HEAD:refs/heads/x`,
		`git push --repo=$(dirname $(pwd))/work HEAD:refs/heads/x`,
		`git send-pack $(dirname $(pwd))/work HEAD:x`,
	} {
		d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount)
		if d.Allowed || !strings.Contains(d.PolicyRule, "push target") {
			t.Errorf("%q must be refused by Rule B, got allowed=%t rule=%q", script, d.Allowed, d.PolicyRule)
		}
	}
	if d := l.g.InspectExec([]string{"git", "push", "--rep=" + l.work, "HEAD:x"}, l.mount); d.Allowed {
		t.Errorf("direct argv --rep=<work> must be refused")
	}
}

func TestGitGuardPushRulesV2_AllowedControls(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, script := range []string{
		// F4: non-git commands that mention a marker.
		`grep -rn receive-pack docs`,
		`echo receivepack >> notes.txt`,
		`wc -c receive.txt`,
		`cat GIT_CONFIG_KEY_notes.md`,
		`grep -c receive.go f`,
		`ls | grep -c receive.x`,
		// H54 idioms and S-20.
		`git push origin "$BRANCH"`,
		`git push origin 'feat/*'`,
		`REMOTE=origin; git push "$REMOTE" main`,
		`git push "${REMOTE:-origin}" main`,
		`git push "$(git remote get-url origin)" main`,
		`git commit -m "why?"`,
		`git push -u origin HEAD`,
		`git push origin :refs/heads/old`,
		`git push --follow-tags origin main`,
		`git push origin HEAD:refs/heads/s20`,
		`git -C ` + l.mount + ` push origin HEAD`,
		`git push https://github.com/org/repo.git HEAD:refs/heads/s20`,
		`git log --oneline -n 3 && git diff --stat`,
		// --ex with no push or send-pack anywhere is not an override.
		`git log --exit-code; git status`,
	} {
		if d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount); !d.Allowed {
			t.Errorf("%q must stay allowed, got %s (%s)", script, d.Reason, d.PolicyRule)
		}
	}
	for _, argv := range [][]string{
		{"git", "push", "origin", "HEAD:refs/heads/s20"},
		{"git", "push", "-u", "origin", "HEAD"},
		{"grep", "-rn", "receive-pack", "docs"},
	} {
		if d := l.g.InspectExec(argv, l.mount); !d.Allowed {
			t.Errorf("%q must stay allowed, got %s (%s)", argv, d.Reason, d.PolicyRule)
		}
	}
}

// Re-review F5: the pins in <work>/.git/config cannot be edited away through a
// direct "git config --file" or an in-place editor, on either path.
func TestGitGuardPushRulesV2_PinFileWritersRefused(t *testing.T) {
	l := newPushRuleLayout(t)
	cfg := l.work + "/.git/config"
	for _, argv := range [][]string{
		{"git", "config", "--file", cfg, "receive.denyCurrentBranch", "ignore"},
		{"git", "config", "--file=" + cfg, "core.bare", "true"},
		{"git", "config", "-f", "../work/.git/config", "receive.denyCurrentBranch", "ignore"},
		{"git", "config", "-f../work/.git/config", "core.hooksPath", "/tmp/hk"},
		{"git", "-C", "..", "config", "--file", "work/.git/config", "receive.denyDeletes", "false"},
		{"sed", "-i", "s/refuse/ignore/", cfg},
		{"perl", "-pi", "-e", "s/refuse/ignore/", "../work/.git/config"},
		// Shell path, same commands.
		{"bash", "-c", "git config --file " + cfg + " receive.denyCurrentBranch ignore"},
		{"bash", "-c", "sed -i 's/refuse/ignore/;s/bare = false/bare = true/' ../work/.git/config"},
		{"bash", "-c", "sed -i.bak s/refuse/ignore/ " + cfg},
		{"bash", "-c", "perl -pi -e 's/refuse/ignore/' ../work/.git/config"},
		{"bash", "-c", "perl -i -pe 's/refuse/ignore/' " + cfg},
	} {
		if d := l.g.InspectExec(argv, l.mount); d.Allowed {
			t.Errorf("%q must be refused (it rewrites the evidence repo's pinned config)", argv)
		}
	}
	for _, argv := range [][]string{
		{"git", "config", "user.name", "Jim"},
		{"git", "config", "--file", ".gitmodules", "submodule.x.url", "https://x/y"},
		{"git", "config", "--get", "remote.origin.url"},
		{"bash", "-c", "sed -i s/a/b/ notes.txt && git config --global user.name Jim"},
		{"bash", "-c", "perl -pi -e 's/a/b/' notes.txt"},
		{"bash", "-c", "sed -n 1p ../work/.git/config"},
	} {
		if d := l.g.InspectExec(argv, l.mount); !d.Allowed {
			t.Errorf("%q must stay allowed, got %s (%s)", argv, d.Reason, d.PolicyRule)
		}
	}
}

// CHECK delta "git as an argument": Rule A's scope counts git as a word
// anywhere, not only in command position (the wider scope the chief accepted
// after the re-review). This row pins today's answer: a non-git command whose
// arguments hold the word git and a marker IS refused by Rule A, and the same
// command without the word git is not.
func TestGitGuardPushRulesV2_GitAsAnArgumentIsInRuleAScope(t *testing.T) {
	l := newPushRuleLayout(t)
	d := l.g.InspectExec([]string{"bash", "-c", "echo git receive-pack"}, l.mount)
	if d.Allowed || !strings.Contains(d.PolicyRule, h55v2Override) {
		t.Fatalf("echo git receive-pack: want refused by Rule A (wide git-word scope), got allowed=%t rule=%q", d.Allowed, d.PolicyRule)
	}
	if d := l.g.InspectExec([]string{"bash", "-c", "echo receive-pack"}, l.mount); !d.Allowed {
		t.Fatalf("echo receive-pack (no git word): want allowed, got %s", d.PolicyRule)
	}
}

// ruling-check-delta.md item 1: --ex is an override whenever push or
// send-pack appears anywhere in the script, with no segment condition. A
// quoted ; would otherwise split one real push after quote deletion.
func TestGitGuardPushRulesV2_ExNeedsPushAnywhere(t *testing.T) {
	l := newPushRuleLayout(t)
	for _, script := range []string{
		`git push -o 'a;b' --ex=x ev HEAD:master`,   // quoted-; bypass of the old segment rule
		`git push -o "a&&b" --exe x ev HEAD:master`, // same with a quoted &&
		`git push -o 'a|b' --ex=x ev`,               // same with a quoted |
		`git push origin main; grep --exclude=x .`,  // accepted over-refusal (pinned)
		`git log --exit-code && git send-pack ev master`,
		`xargs git push --rece=x`,
	} {
		d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount)
		if d.Allowed || !strings.Contains(d.PolicyRule, h55v2Override) {
			t.Errorf("%q must be refused by Rule A, got allowed=%t rule=%q", script, d.Allowed, d.PolicyRule)
		}
	}
	d := l.g.InspectExec([]string{"bash", "-c", `git push origin main; grep --exclude=x .`}, l.mount)
	if !strings.Contains(d.Reason, "'--exclude'") {
		t.Errorf("the over-refusal must name the marker so it is visible, got %q", d.Reason)
	}
	for _, script := range []string{
		`git log --exit-code; git status`,
		`grep --exclude=x -rn pushd .`,
		`git diff --exit-code HEAD`,
	} {
		if d := l.g.InspectExec([]string{"bash", "-c", script}, l.mount); !d.Allowed {
			t.Errorf("%q has no push or send-pack word and must stay allowed, got %s (%s)", script, d.Reason, d.PolicyRule)
		}
	}
}

func TestScanShellWord_BalancesSubstitutions(t *testing.T) {
	rows := []struct{ in, want string }{
		{`$(dirname $(pwd))/work HEAD:x`, `$(dirname $(pwd))/work`},
		{`"$(dirname "$PWD")/work" HEAD`, `"$(dirname "$PWD")/work"`},
		{"`a \\`b\\``/w rest", "`a \\`b\\``/w"},
		{`'a b'c d`, `'a b'c`},
		{`origin;git`, `origin`},
		{`x)`, `x`},
		{`$(unclosed /w`, `$(unclosed /w`},
	}
	for _, r := range rows {
		if got := r.in[:scanShellWord(r.in)]; got != r.want {
			t.Errorf("scanShellWord(%q) = %q, want %q", r.in, got, r.want)
		}
	}
}

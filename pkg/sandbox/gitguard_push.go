package sandbox

// H55 (#1960 S1, architect ruling 2026-11-24): a push from an approved write
// mount must not write into a protected evidence repository. Two rules, both
// active only while a work folder is registered (InspectExec returns early
// otherwise), on the shell path and on the direct git path:
//
//   - Rule A, the receive-side override. A push or send-pack can name the
//     program that runs on the RECEIVING side (--receive-pack, --exec,
//     remote.<n>.receivepack) and pass it receive.* settings, which switches
//     off git's own refusal to move a checked-out branch. The ordinary product
//     never needs any of these, so they are refused outright.
//   - Rule B, the push target. A push or send-pack whose remote operand is a
//     path inside a protected work folder (absolute, relative to the starting
//     working directory, or file://), or a path built at run time (an
//     expansion, glob, "~" or backslash together with a "/" or a leading "."),
//     is refused. Plain remote names and network URLs pass.
//
// Like every check in this package this is not a shell parser (LIM-02): a
// disguised command (a script file, eval, base64) still reaches git unseen.

import (
	"regexp"
	"strings"
)

// h55OverridePolicy is the machine-stable rule id of a Rule A refusal; its text
// is pinned by the H55 tests.
const h55OverridePolicy = "git.sandbox.deny: receive-side override (H55)"

// h55PushTargetPolicy is the rule id of a Rule B refusal.
const h55PushTargetPolicy = "git.sandbox.deny: push target is the protected evidence repo or a run-time path (H55)"

func h55OverrideDeny(what string) ExecDecision {
	return ExecDecision{
		Allowed:    false,
		Verb:       "push",
		Reason:     "a receive-side override (" + what + ") is denied while a protected evidence repository is registered",
		PolicyRule: h55OverridePolicy,
	}
}

func h55PushTargetDeny(verb, remote string) ExecDecision {
	return ExecDecision{
		Allowed:    false,
		Verb:       verb,
		Reason:     "git '" + verb + "' to '" + remote + "' could write into the protected evidence repo (the remote is inside it or is a path built at run time)",
		PolicyRule: h55PushTargetPolicy,
	}
}

// h55ReceiveSettingFlag matches a receive.* setting passed on a command line:
// "-c receive.x", "-creceive.x", "-c=receive.x", "--config-env receive.x=V"
// and "--config-env=receive.x=V", with an optional opening quote. Input is
// lower-cased with blank runs collapsed.
var h55ReceiveSettingFlag = regexp.MustCompile(`(?:^|[\s;&|(])(?:-c|--config-env)[ =]?['"]?receive\.`)

// h55ScriptOverride applies Rule A to a shell script. lower is the script
// lower-cased with blank runs collapsed and BEFORE stripGitGlobalOptions, which
// would remove the -c values this rule reads. It reports the trigger found.
func h55ScriptOverride(lower string) (string, bool) {
	for _, marker := range []string{
		"receive-pack", "receivepack",
		"git_config_parameters", "git_config_count", "git_config_key_",
	} {
		if strings.Contains(lower, marker) {
			return marker, true
		}
	}
	if h55ReceiveSettingFlag.MatchString(lower) {
		return "-c receive.*", true
	}
	return "", false
}

// gitGlobalOptionRunCased is gitGlobalOptionRun for text whose case is kept
// (paths are case-sensitive), so the value-taking "-C <dir>" is read as one
// option with its value rather than as a flag followed by a verb.
var gitGlobalOptionRunCased = regexp.MustCompile(
	`\bgit\s+(?:` +
		`(?:-[cC]|--git-dir|--work-tree|--namespace|--exec-path|--config-env)\s+` + gitShellWord + `\s+` +
		`|` +
		`-` + gitShellWord + `\s+` +
		`)+`)

// gitPushVerbAt finds every "git push" and "git send-pack" (group 1 = verb)
// in a script whose global options are already stripped.
var gitPushVerbAt = regexp.MustCompile(`\bgit[ \t]+(push|send-pack)(?:[ \t]|$)`)

// leadingPathWord matches one shell word at the start of the input.
var leadingPathWord = regexp.MustCompile(`^` + pathWord)

// h55PushValueOptions are the push/send-pack options whose value may sit in the
// next word; that word is not the remote operand.
var h55PushValueOptions = map[string]struct{}{
	"--receive-pack": {}, "--exec": {}, "--repo": {}, "-o": {}, "--push-option": {},
}

// pushOperands splits the words after a push or send-pack verb into the remote
// candidates (the first non-option word, plus any --repo value) and reports
// whether an --exec option is present. words keep their shell quoting.
func pushOperands(words []string) (remotes []string, hasExec bool) {
	unq := func(w string) string { return strings.NewReplacer(`"`, "", `'`, "").Replace(w) }
	for i := 0; i < len(words); i++ {
		w := unq(words[i])
		if w == "--exec" || strings.HasPrefix(w, "--exec=") {
			hasExec = true
		}
		if strings.HasPrefix(w, "--repo=") {
			remotes = append(remotes, w[len("--repo="):])
			continue
		}
		if strings.HasPrefix(w, "-") {
			if _, takesValue := h55PushValueOptions[w]; takesValue && i+1 < len(words) {
				if w == "--repo" {
					remotes = append(remotes, words[i+1])
				}
				i++
			}
			continue
		}
		// The first non-option word is the remote; refspecs follow, and git
		// still reads options among them.
		remotes = append(remotes, words[i])
		return remotes, hasExec || h55RestHasExec(words[i+1:])
	}
	return remotes, hasExec
}

// h55RestHasExec reports an --exec option among the words after the remote
// (git accepts options after the operands).
func h55RestHasExec(words []string) bool {
	for _, w := range words {
		w = strings.NewReplacer(`"`, "", `'`, "").Replace(w)
		if w == "--exec" || strings.HasPrefix(w, "--exec=") {
			return true
		}
	}
	return false
}

// scriptPushWords returns, for each push or send-pack in script (case kept,
// blank runs collapsed), its verb and the shell words that follow it up to the
// end of that command.
func scriptPushWords(script string) (verbs []string, args [][]string) {
	stripped := gitGlobalOptionRunCased.ReplaceAllString(script, "git ")
	for _, m := range gitPushVerbAt.FindAllStringSubmatchIndex(stripped, -1) {
		rest := stripped[m[3]:]
		var words []string
		for {
			rest = strings.TrimLeft(rest, " \t")
			if rest == "" || strings.ContainsRune(";&|\n\r)", rune(rest[0])) {
				break
			}
			loc := leadingPathWord.FindStringIndex(rest)
			if loc == nil {
				// A character no word can start with (a redirection or similar):
				// the command's operands are over.
				break
			}
			words = append(words, rest[:loc[1]])
			rest = rest[loc[1]:]
		}
		verbs = append(verbs, stripped[m[2]:m[3]])
		args = append(args, words)
	}
	return verbs, args
}

// networkRemote matches a remote whose literal start is a network URL or an
// scp-style "user@host:" address; such a remote never names a local path.
var networkRemote = regexp.MustCompile(`^(?:(?:https?|ssh|git|git\+ssh|ssh\+git)://|[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:|[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+:)`)

// remoteIsProtectedTarget applies Rule B to one remote operand (shell quoting
// kept). base is the directory a relative path is judged against.
func (g *GitGuard) remoteIsProtectedTarget(remote, base string) bool {
	u := strings.NewReplacer(`"`, "", `'`, "").Replace(remote)
	if u == "" || networkRemote.MatchString(u) {
		return false
	}
	// (ii) a path built at run time.
	if unresolvablePathWord(remote) && (strings.Contains(u, "/") || strings.HasPrefix(u, ".")) {
		return true
	}
	if unresolvablePathWord(remote) {
		return false // a bare variable or glob with no "/": a remote name (ruling's accepted gap)
	}
	// (i) a static path inside a protected work folder.
	p := u
	if strings.HasPrefix(strings.ToLower(p), "file://") {
		p = p[len("file://"):]
		if strings.HasPrefix(p, "localhost/") {
			p = p[len("localhost"):]
		}
	}
	if p == "" {
		return false
	}
	_, inside := g.enclosingProtectedWorkdir(resolvePathBestEffort(p, base))
	return inside
}

// inspectShellPush applies Rules A and B to a shell script. lower is the
// lower-cased, blank-collapsed script before global options are stripped.
func (g *GitGuard) inspectShellPush(script, lower, cwd string) (ExecDecision, bool) {
	if what, ok := h55ScriptOverride(lower); ok {
		return h55OverrideDeny(what), true
	}
	verbs, args := scriptPushWords(collapseBlanks(script))
	for i, words := range args {
		remotes, hasExec := pushOperands(words)
		if hasExec {
			return h55OverrideDeny("--exec"), true
		}
		for _, r := range remotes {
			if g.remoteIsProtectedTarget(r, cwd) {
				return h55PushTargetDeny(verbs[i], r), true
			}
		}
	}
	return ExecDecision{}, false
}

// inspectGitArgsPush applies Rules A and B to a direct git argv. globals are
// the global options before the verb, verbArgs the verb's own arguments and
// effCwd the directory after any -C.
func (g *GitGuard) inspectGitArgsPush(globals []string, verb string, verbArgs []string, effCwd string) (ExecDecision, bool) {
	all := append(append(append([]string{}, globals...), verb), verbArgs...)
	for _, a := range all {
		la := strings.ToLower(a)
		if strings.Contains(la, "receive-pack") || strings.Contains(la, "receivepack") {
			return h55OverrideDeny("receive-pack"), true
		}
	}
	for i, a := range globals {
		la := strings.ToLower(a)
		val := ""
		switch {
		case (a == "-c" || la == "--config-env") && i+1 < len(globals):
			val = strings.ToLower(globals[i+1])
		case strings.HasPrefix(la, "--config-env="):
			val = la[len("--config-env="):]
		case strings.HasPrefix(a, "-c"):
			val = strings.TrimPrefix(la[2:], "=")
		}
		if strings.HasPrefix(val, "receive.") {
			return h55OverrideDeny("-c receive.*"), true
		}
	}
	v := strings.ToLower(verb)
	if v != "push" && v != "send-pack" {
		return ExecDecision{}, false
	}
	remotes, hasExec := pushOperands(verbArgs)
	if hasExec {
		return h55OverrideDeny("--exec"), true
	}
	for _, r := range remotes {
		if g.remoteIsProtectedTarget(r, effCwd) {
			return h55PushTargetDeny(v, r), true
		}
	}
	return ExecDecision{}, false
}

package sandbox

// H55 (#1960 S1, architect rulings ruling-H55.md and ruling-H55-v2.md): a push
// from an approved write mount must not write into a protected evidence
// repository. The main control is structural: the evidence repository pins
// receive.denyCurrentBranch, denyNonFastForwards, denyDeletes and core.bare in
// its own local config (pkg/gitevidence/receivepins.go), which outranks global
// and system config. These two text rules are the second line, for what local
// config cannot outrank (command-line and environment config). Both are active
// only while a work folder is registered (InspectExec returns early otherwise),
// on the shell path and on the direct git path:
//
//   - Rule A, the receive-side override. Only for a command line that holds a
//     git word (F4). The text is lower-cased, blank runs collapsed and every
//     ' " and \ deleted (quote splitting, F2), then refused when it holds
//     receive-pack or receivepack, a word starting with --rece (git's
//     abbreviations of --receive-pack), a word starting with --ex when the
//     text anywhere holds the word push or send-pack (abbreviations of --exec;
//     no segment condition, ruling-check-delta.md item 1), a receive.* setting via
//     -c or --config-env, or the GIT_CONFIG_PARAMETERS/COUNT/KEY_ markers.
//   - Rule B, the push target. A push or send-pack whose remote operand (or a
//     --repo value, any abbreviation from --rep) is a path inside a protected
//     work folder (absolute, relative to the starting working directory, or
//     file://), or a path built at run time (an expansion, glob, "~" or
//     backslash together with a "/" or a leading "."), is refused. The operand
//     is read with $( ) and backticks balanced, nested included (F3). Plain
//     remote names and network URLs pass.
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

func h55OverrideDeny(marker string) ExecDecision {
	return ExecDecision{
		Allowed:    false,
		Verb:       "push",
		Reason:     "a git command holding the receive-side override marker '" + marker + "' is denied while a protected evidence repository is registered",
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

// ---- Rule A ---------------------------------------------------------------

// h55Normalise lower-cases text, collapses blank runs and deletes every ', "
// and \ (F2: quote splitting such as rec""eive-pa""ck).
func h55Normalise(text string) string {
	return collapseBlanks(strings.NewReplacer(`'`, "", `"`, "", `\`, "").Replace(strings.ToLower(text)))
}

// h55SegmentSeparators split a normalised script into command segments.
const h55SegmentSeparators = ";&|()`\n\r"

// h55Words splits normalised text into words on blanks, "=" and the ratified
// separators (; & | ( ) backtick LF CR), so "alias.p=push" yields "push" and
// "--rece=x" yields "--rece". More splitting only makes more words; it never
// hides a marker.
func h55Words(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '=' || strings.ContainsRune(h55SegmentSeparators, r)
	})
}

// h55HasGitWord reports whether the normalised text holds git as a word ("git"
// or a path ending in "/git"). It is deliberately wider than "git in command
// position": shell keywords (then, do, if), prefixes (sudo, xargs, env) and
// assignments can all stand before a command word, and a wider scope only
// refuses scripts that mention git together with an override marker.
func h55HasGitWord(norm string) bool {
	for _, w := range strings.FieldsFunc(norm, func(r rune) bool {
		return r == ' ' || r == '\t' || strings.ContainsRune(h55SegmentSeparators, r)
	}) {
		if w == "git" || strings.HasSuffix(w, "/git") {
			return true
		}
	}
	return false
}

// h55ReceiveSettingFlag matches a receive.* setting passed on a command line:
// "-c receive.x", "-creceive.x", "-c=receive.x", "--config-env receive.x=V"
// and "--config-env=receive.x=V". Input is normalised.
var h55ReceiveSettingFlag = regexp.MustCompile(`(?:^|[\s;&|(])(?:-c|--config-env)[ =]?receive\.`)

// h55OverrideMarker applies Rule A's markers to normalised text that holds a
// git word, and reports the first marker found.
func h55OverrideMarker(norm string) (string, bool) {
	for _, marker := range []string{
		"receive-pack", "receivepack",
		"git_config_parameters", "git_config_count", "git_config_key_",
	} {
		if strings.Contains(norm, marker) {
			return marker, true
		}
	}
	if h55ReceiveSettingFlag.MatchString(norm) {
		return "-c receive.*", true
	}
	// The --ex marker needs push or send-pack anywhere in the text, not in the
	// same segment: segments are cut after quotes are deleted, so a quoted ;
	// would split one real command (ruling-check-delta.md item 1).
	words := h55Words(norm)
	pushing := false
	for _, w := range words {
		if w == "push" || w == "send-pack" {
			pushing = true
		}
	}
	for _, w := range words {
		if strings.HasPrefix(w, "--rece") {
			return w, true
		}
		if pushing && strings.HasPrefix(w, "--ex") {
			return w, true
		}
	}
	return "", false
}

// h55RuleA applies Rule A to a command text (a script, or a direct argv
// joined with spaces).
func h55RuleA(text string) (ExecDecision, bool) {
	norm := h55Normalise(text)
	if !h55HasGitWord(norm) {
		return ExecDecision{}, false
	}
	if marker, ok := h55OverrideMarker(norm); ok {
		return h55OverrideDeny(marker), true
	}
	return ExecDecision{}, false
}

// ---- Rule B ---------------------------------------------------------------

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

// scanShellWord returns the length of the shell word at the start of s: it
// ends at an unquoted blank or one of ; & | ) at the top level, and it keeps
// single and double quotes, backslash escapes, $( ) and backticks balanced,
// nested included (F3: $(dirname $(pwd))/work is one word). An unbalanced
// construct runs to the end of s.
func scanShellWord(s string) int {
	var stack []byte // '(' for $( or a nested (, '`' for a backtick, '"' for a double quote
	i := 0
	for i < len(s) {
		c := s[i]
		top := byte(0)
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		switch {
		case c == '\\':
			i += 2
			continue
		case top == '"':
			switch {
			case c == '"':
				stack = stack[:len(stack)-1]
			case c == '`':
				stack = append(stack, '`')
			case c == '$' && i+1 < len(s) && s[i+1] == '(':
				stack = append(stack, '(')
				i++
			}
		case top == '`' && c == '`':
			stack = stack[:len(stack)-1]
		case top == '(' && c == ')':
			stack = stack[:len(stack)-1]
		case top == 0 && strings.ContainsRune(" \t\n\r;&|)", rune(c)):
			return i
		case c == '\'':
			if end := strings.IndexByte(s[i+1:], '\''); end >= 0 {
				i += end + 2
				continue
			}
			return len(s)
		case c == '"':
			stack = append(stack, '"')
		case c == '`':
			stack = append(stack, '`')
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			stack = append(stack, '(')
			i++
		case c == '(' && top == '(':
			stack = append(stack, '(')
		}
		i++
	}
	if i > len(s) {
		return len(s)
	}
	return i
}

// h55PushValueOptions are the push/send-pack options whose value may sit in
// the next word; that word is not the remote operand.
var h55PushValueOptions = map[string]struct{}{
	"--receive-pack": {}, "--exec": {}, "-o": {}, "--push-option": {},
}

// pushOperands returns the remote candidates among the words after a push or
// send-pack verb: the first non-option word, plus any --repo value (git
// accepts any abbreviation from --rep). words keep their shell quoting.
func pushOperands(words []string) (remotes []string) {
	unq := func(w string) string { return strings.NewReplacer(`"`, "", `'`, "").Replace(w) }
	for i := 0; i < len(words); i++ {
		w := unq(words[i])
		if strings.HasPrefix(w, "--rep") {
			if eq := strings.IndexByte(words[i], '='); eq >= 0 {
				remotes = append(remotes, words[i][eq+1:])
			} else if i+1 < len(words) {
				remotes = append(remotes, words[i+1])
				i++
			}
			continue
		}
		if strings.HasPrefix(w, "-") {
			if _, takesValue := h55PushValueOptions[w]; takesValue && i+1 < len(words) {
				i++
			}
			continue
		}
		// The first non-option word is the remote; refspecs follow.
		return append(remotes, words[i])
	}
	return remotes
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
			if rest == "" || strings.ContainsRune(";&|\n\r)<>", rune(rest[0])) {
				break
			}
			n := scanShellWord(rest)
			if n == 0 {
				break
			}
			words = append(words, rest[:n])
			rest = rest[n:]
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
	if unresolvablePathWord(remote) {
		// (ii) a path built at run time; a bare variable, substitution or glob
		// with no "/" is a remote name (the ruling's accepted gap).
		return strings.Contains(u, "/") || strings.HasPrefix(u, ".")
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

// configNamesProtectedGit reports the first argument of a direct "git config"
// that names a path at or under a protected .git: a --file/-f/--blob value
// (spaced, "=" or glued -f<path>) or any other word, resolved against effCwd.
// The shell path refuses the same through its "git config" pattern; this
// keeps the direct-argv path consistent (re-review F5).
func (g *GitGuard) configNamesProtectedGit(args []string, effCwd string) (string, bool) {
	for _, a := range args {
		candidates := []string{a}
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			candidates = append(candidates, a[eq+1:])
		}
		if strings.HasPrefix(a, "-f") && len(a) > 2 && a[2] != '=' {
			candidates = append(candidates, a[2:])
		}
		for _, c := range candidates {
			if c == "" || strings.HasPrefix(c, "-") {
				continue
			}
			if p, _ := g.matchesProtectedGit(resolvePathBestEffort(c, effCwd)); p {
				return a, true
			}
		}
	}
	return "", false
}

// ---- entry points ---------------------------------------------------------

// inspectShellPush applies Rules A and B to a shell script (the original
// text; Rule A normalises its own copy, before any option stripping).
func (g *GitGuard) inspectShellPush(script, cwd string) (ExecDecision, bool) {
	if d, denied := h55RuleA(script); denied {
		return d, true
	}
	verbs, args := scriptPushWords(collapseBlanks(script))
	for i, words := range args {
		for _, r := range pushOperands(words) {
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
	all := append(append(append([]string{"git"}, globals...), verb), verbArgs...)
	if d, denied := h55RuleA(strings.Join(all, " ")); denied {
		return d, true
	}
	v := strings.ToLower(verb)
	if v == "config" {
		if word, ok := g.configNamesProtectedGit(verbArgs, effCwd); ok {
			return ExecDecision{
				Allowed:    false,
				Verb:       "config",
				Reason:     "git 'config' naming '" + word + "' would write the protected evidence repo's own config",
				PolicyRule: "git.sandbox.deny: config file inside protected .git (H55 F5)",
			}, true
		}
		return ExecDecision{}, false
	}
	if v != "push" && v != "send-pack" {
		return ExecDecision{}, false
	}
	for _, r := range pushOperands(verbArgs) {
		if g.remoteIsProtectedTarget(r, effCwd) {
			return h55PushTargetDeny(v, r), true
		}
	}
	return ExecDecision{}, false
}

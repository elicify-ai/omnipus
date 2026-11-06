// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills spec r15 §1 item 2, §6.1 item 5 (UAT-1 F-8): bash may READ and RUN
// the files of a skill the acting agent can load, and may not change any
// skill path. The loadable folders are the turn policy's
// HomeGuard.SkillFolders (resolveTurnSkillAuthority — the same set the
// kernel grants read+exec, turnAllowRoots). Everything else under the skills
// root (an ungranted skill, .staging, .swap) and system/skill-history keeps
// the child-only deny, and without a turn policy nothing is loadable.
//
// An operand inside a loadable folder is allowed only in one of these
// shapes, judged from the command TEXT, failing closed to "write-shaped":
//
//   - the path is the command itself (command position: run it);
//   - the path is an argument of a literal read-only allowlisted command
//     (readOnlyShellCommands) and the segment's own read proof holds
//     (pathUseClassifier.classify reports readOnly);
//   - the path is the FIRST argument word of a literal script runner
//     (skillScriptRunners) — the script it runs;
//   - the path is the first argument word of a literal `cd`, and every later
//     segment is a `./relative` script run or a literal read-only command
//     with no redirect, and no later operand has a `..` path segment
//     (afterSkillCd, SEC2-03 and SEC3-01).
//
// A glob-bearing operand (operandHasGlob, SEC2-02) never earns the
// exception, and `rg` with a preprocessor flag is not a read (rgRunsCommands,
// SEC2-01).
//
// Every other shape (an output-redirect target, a writer such as rm/cp/mv/
// tee/touch/sed, a runner's later argument, an unparseable command, a brace
// expansion, a command word spelled with a prefix or an expansion) is
// refused with skillWriteRefusal.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// skillScriptRunners are the interpreters whose first argument is the script
// they run. Only that one argument position is accepted for a skill operand.
var skillScriptRunners = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true,
	"python": true, "python3": true, "node": true,
}

// loadableSkillFolder returns the loadable skill folder (from skillFolders)
// that contains p, on both its lexical and its symlink-resolved form, or ""
// when p is not inside one. Both forms must agree: a symlink inside a skill
// folder that points elsewhere is judged by the ordinary rules.
func loadableSkillFolder(p string, skillFolders []string) string {
	if len(skillFolders) == 0 {
		return ""
	}
	resolved := p
	if canonical, err := resolvePathAgainstExistingAncestor(p); err == nil {
		resolved = canonical
	}
	for _, folder := range skillFolders {
		if folder == "" {
			continue
		}
		clean := filepath.Clean(folder)
		if coversForGrantBoth(clean, p, resolved) {
			return clean
		}
	}
	return ""
}

func coversForGrantBoth(folder, lexical, resolved string) bool {
	return fspolicy.CoversForGrant(folder, lexical) && fspolicy.CoversForGrant(folder, resolved)
}

// operandHasGlob reports whether the shell word that begins at byte offset
// start of s carries a glob or brace character (security review 2, SEC2-02).
// absolutePathPattern stops a candidate at such a character, so
// `<skills>/granted*/x` reaches the guard as `<skills>/granted` — a loadable
// folder — while the shell expands it onto ungranted siblings too. A
// glob-bearing operand never earns the skill exception; it meets the
// ordinary deny instead. Quoting is ignored on purpose (a quoted `*` is
// literal, and refusing it only costs a needless refusal).
func operandHasGlob(s string, start int) bool {
	if start < 0 || start > len(s) {
		return true
	}
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[', ']', '{', '}':
			return true
		case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '<', '>', '`':
			return false
		}
	}
	return false
}

// rgRunsCommands reports whether a segment whose head is `rg` names one of
// ripgrep's flags that spawn a program (--pre, --pre-glob, --hostname-bin):
// such an rg is not a read (security review 2, SEC2-01). A segment the
// tokenizer cannot split counts as running commands (fail closed).
func rgRunsCommands(seg string) bool {
	words, ok := tokenizeShellWords(seg)
	if !ok {
		return true
	}
	for _, w := range words {
		if strings.HasPrefix(w, "--pre") || strings.HasPrefix(w, "--hostname-bin") {
			return true
		}
	}
	return false
}

// skillOperandUse reports whether the absolute-path candidate starting at
// byte offset start, which lies inside a loadable skill folder, is used in
// one of the allowed read/run shapes (file comment). reason explains a
// refusal.
func (c pathUseClassifier) skillOperandUse(start int) (allowed bool, reason string) {
	if !c.classifiable {
		return false, "the command contains quoting or a substitution the guard cannot parse"
	}
	if start < 0 || start >= len(c.cmd) {
		return false, "the reference could not be located in the command text"
	}
	segStart, segEnd := c.segmentBounds(start)
	word := c.wordStart(start, segStart)
	if c.precededByOutputRedirect(word, segStart) {
		return false, "the path is the target of an output redirect"
	}
	use := c.classify(start)
	if use.readOnly {
		return true, ""
	}
	for i := segStart; i < segEnd; i++ {
		if !c.mask[i] && (c.cmd[i] == '{' || c.cmd[i] == '}') {
			return false, "the command uses brace expansion ({ }), which rewrites its words before they run"
		}
	}
	if !c.redirectTargetsAreLiteral(segStart, segEnd) {
		return false, "the same command segment redirects output to a target the guard cannot see"
	}
	if use.exec {
		if word != start {
			return false, "the command word is not the skill path itself"
		}
		return true, ""
	}
	head, headIsExpansion, headNormalised := shellCommandHeadDetailed(c.cmd[segStart:segEnd])
	if headIsExpansion || headNormalised || (!skillScriptRunners[head] && head != "cd") {
		return false, fmt.Sprintf("%q can change files named on its command line", head)
	}
	fw := c.firstWordStart(segStart, segEnd)
	j := fw
	for j < segEnd && (c.mask[j] || !isShellWordBreak(c.cmd[j])) {
		j++
	}
	if c.cmd[fw:j] != head {
		return false, "the command word is preceded by an assignment or quoting"
	}
	for j < segEnd && !c.mask[j] && (c.cmd[j] == ' ' || c.cmd[j] == '\t') {
		j++
	}
	if word != j || word != start {
		return false, fmt.Sprintf("only the first argument of %q (the script or folder itself) may name a skill path", head)
	}
	if head == "cd" {
		return c.afterSkillCd(segEnd)
	}
	return true, ""
}

// afterSkillCd judges every segment after a literal `cd <skill folder>`
// (security review 2, SEC2-03 ruling): their relative operands land inside
// the skill, so each later segment must be either a `./relative` script run
// (command word starting with `./`, no `..`) or a literal read-only
// allowlisted command with no output redirect. Anything else — chmod, rm,
// cp, tee, a redirect, another cd — is refused.
func (c pathUseClassifier) afterSkillCd(from int) (bool, string) {
	for i := from; i < len(c.cmd); {
		segStart, segEnd := c.segmentBounds(i)
		if segEnd <= i {
			i++ // a separator byte (`&&`, `;`, `|`)
			continue
		}
		i = segEnd
		seg := c.cmd[segStart:segEnd]
		if strings.TrimSpace(seg) == "" {
			continue
		}
		for k := segStart; k < segEnd; k++ {
			if !c.mask[k] && strings.ContainsRune("><{}", rune(c.cmd[k])) {
				return false, "after cd into a skill folder, a later command redirects output or uses brace expansion"
			}
		}
		if wordLeavesFolder(seg) {
			return false, "after cd into a skill folder, a later operand leaves the folder through a '..' path segment"
		}
		fw := c.firstWordStart(segStart, segEnd)
		if strings.HasPrefix(c.cmd[fw:segEnd], "./") {
			wordEnd := fw
			for wordEnd < segEnd && (c.mask[wordEnd] || !isShellWordBreak(c.cmd[wordEnd])) {
				wordEnd++
			}
			if !strings.Contains(c.cmd[fw:wordEnd], "..") {
				continue // a script of the skill, run in place
			}
		}
		head, headIsExpansion, headNormalised := shellCommandHeadDetailed(seg)
		if headIsExpansion || headNormalised || !readOnlyShellCommands[head] || (head == "rg" && rgRunsCommands(seg)) {
			return false, fmt.Sprintf("after cd into a skill folder, %q could change the skill's files", head)
		}
	}
	return true, ""
}

// loadableSkillReadOrRun is the workspace scan's half of the rule
// (guardCommandClassified, restrictToWorkspace): a read or run of a loadable
// skill folder's file is allowed outside the working directory, as the kernel
// grants those folders read+exec (turnAllowRoots). A write-shaped operand
// there was already refused by guardProtectedOperands; anything not proven a
// read/run shape returns false and meets the ordinary checks.
func loadableSkillReadOrRun(raw string, start int, c pathUseClassifier, policy fspolicy.FSPolicy) bool {
	p, err := filepath.Abs(raw)
	if err != nil || operandHasGlob(c.cmd, start) || loadableSkillFolder(p, policy.HomeGuard.SkillFolders) == "" {
		return false
	}
	ok, _ := c.skillOperandUse(start)
	return ok
}

// wordLeavesFolder reports whether any word of seg is, or contains, a `..`
// path segment (bare `..`, `../x`, `./../x`, `a/../b`, `--flag=../x`): after
// a cd into a skill folder such an operand reaches ungranted siblings
// (security review 3, SEC3-01). Segment boundaries are the word's ends, `/`,
// `=` and `:`. A segment the tokenizer cannot split counts as leaving.
func wordLeavesFolder(seg string) bool {
	words, ok := tokenizeShellWords(seg)
	if !ok {
		return true
	}
	for _, w := range words {
		parts := strings.FieldsFunc(w, func(r rune) bool { return r == '/' || r == '=' || r == ':' })
		for _, part := range parts {
			if part == ".." {
				return true
			}
		}
	}
	return false
}

// SkillWriteTarget implements SkillWriteTargeter for bash (UAT run 2 R2-1):
// a command that names a path in a skill folder in a write shape (any shape
// skillOperandUse does not accept, including changes after a skill cd) is a
// skill write, so the loop composes edit_skill into its verdict and a denied
// edit_skill answers skill_edit_denied with its audit entry. The path guard
// still refuses every such command on its own whatever the verdict.
func (t *ExecTool) SkillWriteTarget(_ context.Context, args map[string]any) (string, bool) {
	command, _ := args["command"].(string)
	return commandSkillWriteTarget(command)
}

// commandSkillWriteTarget returns the skill key of the first write-shaped
// skill operand in command.
func commandSkillWriteTarget(command string) (string, bool) {
	cmd := strings.TrimSpace(command)
	c := newPathUseClassifier(cmd)
	for _, loc := range absolutePathPattern.FindAllStringSubmatchIndex(cmd, -1) {
		start, end := loc[2], loc[3]
		if start < 0 || end < 0 || operandHasGlob(cmd, start) {
			continue
		}
		raw := cmd[start:end]
		if loc[0] >= 0 && loc[0] < start {
			if prefix, resolved, isExpansion := expandCandidatePrefix(cmd[loc[0]:start]); isExpansion {
				if !resolved {
					continue
				}
				raw = filepath.Join(prefix, raw)
			}
		}
		key, ok := refusedSkillWriteTarget(raw)
		if !ok {
			continue
		}
		if allowed, _ := c.skillOperandUse(start); !allowed {
			return key, true
		}
	}
	return "", false
}

// skillWriteRefusal is the refusal for a write-shaped operand inside a
// loadable skill folder.
func skillWriteRefusal(p, folder, reason string) string {
	return fmt.Sprintf(
		"Command blocked: %q is inside the skill folder %q. bash may read and run a skill's files but not change them "+
			"(%s). Change a skill with write_file or edit_file, or with edit_skill to copy, delete or restore it.",
		p, folder, reason)
}

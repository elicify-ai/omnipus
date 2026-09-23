// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file closes review finding #12: under Auto mode, a command starting
// with an ordinary POSIX shell builtin (`cd`, `export`, `set`, `unset`,
// `alias`, ...) has no on-PATH executable for ResolveBinary to find, so
// evaluateSegment's PATH lookup always failed and the segment was judged a
// blind spot — Action=ActionAsk — even with ZERO command_rules configured.
// A fresh install with no operator rules at all would therefore prompt for
// every single `cd /tmp`, which is both a real usability regression and,
// worse, teaches an operator to distrust or disable Auto rather than to
// trust its (correct) escalations.
package shellrule

// posixBuiltins are POSIX/bash builtins with no on-PATH executable to find.
// Resolving them via a PATH search (ResolveBinary's only job) fails by
// construction, not because anything is wrong with the command.
//
// `source`/`.` are DELIBERATELY absent: both execute an arbitrary file's
// contents in the current shell — the same risk class as running a script,
// not "harmless housekeeping" — so they keep going through the ordinary
// PATH-resolution failure path, which correctly stays a blind-spot Ask
// under Auto (FR-020's fail-closed posture, unchanged for these two).
// `eval`/`exec` are likewise absent for the same reason (arbitrary-code
// primitives, not benign builtins) — and both are already refused
// structurally wherever they appear inside a command substitution by
// substitutionDenyAnySegment (pkg/tools/shell_subst_guard.go).
var posixBuiltins = map[string]bool{
	"cd": true, "pwd": true, "export": true, "set": true, "unset": true,
	"alias": true, "unalias": true, "read": true, "shift": true,
	"exit": true, "return": true, "break": true, "continue": true,
	"local": true, "declare": true, "typeset": true, "readonly": true,
	"trap": true, "umask": true, "wait": true, "test": true, "[": true,
	"true": true, "false": true, "let": true, "printf": true, "type": true,
	"hash": true, "times": true, "ulimit": true, "getopts": true,
	"echo": true,
}

// resolveHeadOrBuiltin resolves name to an absolute path via resolve/
// pathList, unless name is a recognised harmless POSIX builtin (above), in
// which case name itself stands in for the resolved form — there is no
// on-PATH executable to find, and none is needed to judge the segment
// safely. Used for BOTH the segment's own head (evaluateSegment) and an
// operator rule's Binary field (matchRules), so a rule authored with
// `binary: "cd"` resolves consistently with what the segment scanner
// itself would report.
func resolveHeadOrBuiltin(name, pathList string, resolve BinaryResolver) (string, error) {
	if posixBuiltins[name] {
		return name, nil
	}
	return resolve(name, pathList)
}

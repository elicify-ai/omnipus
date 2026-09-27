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
//
// Deliberately EXCLUDED even though POSIX also defines them as builtins:
// `true`, `false`, `echo`, `printf`, `pwd`, `test`, `[` — every shell ships
// these, but so does /bin (coreutils ships standalone /bin/true, /bin/pwd,
// /usr/bin/printf, /usr/bin/test, …), and resolveHeadOrBuiltin's own
// resolve-first order below means the shortcut never fires for them anyway
// on any system that has the standalone binary — listing them here would
// only matter on a hypothetical system with NEITHER, and keeping the map to
// genuinely PATH-absent builtins keeps this file honest about what it is
// actually compensating for.
var posixBuiltins = map[string]bool{
	"cd": true, "export": true, "set": true, "unset": true,
	"alias": true, "unalias": true, "read": true, "shift": true,
	"exit": true, "return": true, "break": true, "continue": true,
	"local": true, "declare": true, "typeset": true, "readonly": true,
	"trap": true, "umask": true, "wait": true, "let": true, "type": true,
	"hash": true, "times": true, "ulimit": true, "getopts": true,
}

// resolveHeadOrBuiltin resolves name against pathList via resolve FIRST —
// exactly ResolveBinary's own ordinary behaviour, unchanged whenever a real
// on-PATH executable exists — and falls back to treating name as a
// recognised harmless POSIX builtin (above) ONLY when that real resolution
// fails. This order is deliberate, not cosmetic: it is what keeps `true`,
// `pwd`, and every other command that happens to ALSO be a shell builtin
// resolving to their real, symlink-resolved binary path on any system that
// has one (matching every existing rule/test that resolves against the
// genuine ResolveBinary("true", PATH) result) — only `cd`/`export`/etc.,
// which have no on-PATH executable to find at all, ever take the fallback.
//
// Used for BOTH the segment's own head (evaluateSegment) and an operator
// rule's Binary field (matchRules), so a rule authored with `binary: "cd"`
// resolves consistently with what the segment scanner itself would report.
func resolveHeadOrBuiltin(name, pathList string, resolve BinaryResolver) (string, error) {
	if resolved, err := resolve(name, pathList); err == nil {
		return resolved, nil
	}
	if posixBuiltins[name] {
		return name, nil
	}
	return resolve(name, pathList)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package shellrule implements the ADR-091 D3 "one rule format, one decision
// order" command-rule engine: operator `command_rules` and user grants (D4)
// share this shape and this precedence.
//
// # Decision order
//
// deny beats ask beats allow, specificity-blind (ADR-091 D3): if a broad
// `allow` rule and a narrower `deny` rule both match the same segment, the
// call is denied — a rule is never preferred merely for being more specific.
// See Decide.
//
// # Resolve-and-verify, not resolve-and-rewrite
//
// pkg/tools/shell.go::buildShellArgv execs `["sh", "-c", command]` on POSIX
// and `["powershell", ..., "-Command", command]` on Windows: argv[0] is
// always `sh`/`powershell`, never the user's binary, so there is no argv
// slot to rewrite. Instead this package resolves a segment's leading token
// to its actual executable and matches operator rules against the RESOLVED
// ABSOLUTE PATH, never the command text. See ResolveBinary and Verify.
//
// The look-alike defence (GitHub issue #83's live "Additional" claim, and
// ADR-091 scenario S24) depends on resolving a rule's own Binary field
// against a caller-supplied TRUSTED path list, separate from the CHILD path
// list used to resolve the segment actually about to run: an attacker who
// prepends a malicious `git` onto the untrusted child PATH changes what the
// segment resolves to, but not what the trusted rule resolves to, so the two
// absolute paths diverge and the rule does not match.
//
// # Chained commands
//
// A command is split into segments at the existing chain operators
// (`&&`/`||`/`;`/`|`/newline) — this package does not implement its own
// splitter; it is handed one via the Segmenter function type so the caller
// (pkg/tools, which owns pkg/tools/shell_subst_guard.go::splitShellSegments
// and ::shellCommandHeadDetailed) can pass its own unexported functions in
// as values without either package importing the other. This keeps
// pkg/shellrule a leaf: stdlib only, no internal imports, safe for pkg/tools
// to import in the other direction without a cycle.
//
// # Windows
//
// splitShellSegments is a POSIX operator set that does not model PowerShell
// grammar, and there is no argv[0] slot on Windows either (ADR-091 D3,
// founder decision). On Windows, EvaluateCommand treats the whole command
// string as one segment (Segmenter is never called) and an ArgPrefix, if
// set, must match the full remaining argument text exactly — there is no
// prefix option. SuggestedPrefix returns ("", false) unconditionally on
// Windows. See FR-041.
package shellrule

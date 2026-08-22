// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This file contains the pure, platform-independent Seatbelt profile
// generation logic. It has NO build constraint so it compiles (and is unit-
// tested) on Linux, while the actual SeatbeltBackend that consumes it lives in
// backend_darwin_seatbelt.go behind a `//go:build darwin` tag.
//
// Seatbelt is the macOS mandatory access-control subsystem (the XNU sandbox).
// The no-CGo path to apply it is to emit a Seatbelt profile (the `.sb`
// Scheme-like dialect) and launch the child under `/usr/bin/sandbox-exec`.
// This renderer maps an Omnipus SandboxPolicy onto that dialect.
//
// ADR-052 Phase-3 AC-6: the renderer is no longer a draft. The system preamble
// below was derived empirically on macOS (see seatbeltSystemPreamble) and the
// backend is wired into darwin backend selection by sandbox_darwin.go.

// seatbeltSystemPreamble is the macOS bootstrap block prepended to every
// rendered profile. Without it a policy-only profile does not merely restrict
// a child — it makes the child impossible to start at all.
//
// # Provenance — every line here was MEASURED, not read from documentation
//
// Measured on macOS 26.5.2 (Darwin 25.5.0), x86_64, by starting from the
// policy-only profile and adding the minimum needed to make a real child run.
// To re-derive after an OS change: start from a profile containing only the
// policy rules, run a real child under `sandbox-exec -p`, and add the minimum
// needed to make it start — the method that produced the findings below.
//
// The non-obvious findings, each of which cost a debugging cycle:
//
//  1. (allow file-read* (literal "/")) is MANDATORY. A child needs to stat the
//     root directory itself, and no (subpath "/usr/lib")-style allow covers
//     "/" . Without it EVERY child dies on SIGABRT (exit 134) with completely
//     EMPTY stderr — dyld is killed before it can report anything. This single
//     missing line is indistinguishable from "Seatbelt is broken".
//
//  2. The dyld shared cache needs no rule BEYOND the /System read allow already
//     present below. Do NOT add a rule for
//     /System/Library/dyld: that path DOES NOT EXIST on macOS 13+. The cache
//     lives under /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld/.
//     The original draft comment named the stale path; it was never needed.
//
//  3. /private/var/select is required by /bin/sh (the shell selector symlink).
//     Without it sh fails with "Error opening /private/var/select/sh".
//
//  4. DNS. macOS resolves names via mDNSResponder over a UNIX-domain socket,
//     NOT via UDP/53 to a nameserver. A profile that allow-lists ports but not
//     that socket resolves NOTHING, so every port-scoped network allow appears
//     broken while an unscoped (allow network*) works — which reads exactly
//     like "the port filter syntax is wrong" and sends you chasing the wrong
//     bug. Both the mach-lookup and the network-outbound literal are required.
//
// Everything here is read-only or a well-known sink. The preamble grants no
// write access outside /dev/null and no execute outside the system binary
// directories, so it cannot widen a policy's reach over user data.
const seatbeltSystemPreamble = `;; --- macOS system preamble (empirically derived; see seatbeltSystemPreamble) ---
;; Root directory stat. MANDATORY: without this every child aborts with no diagnostic.
(allow file-read* (literal "/"))
;; Firmlink/symlink traversal for the /tmp, /etc, /var compatibility symlinks.
(allow file-read* (literal "/tmp") (literal "/etc") (literal "/var"))
;; System binaries and libraries: read + execute only, never write.
(allow process-exec (subpath "/bin") (subpath "/sbin") (subpath "/usr/bin") (subpath "/usr/sbin") (subpath "/usr/lib") (subpath "/System"))
(allow file-read* (subpath "/bin") (subpath "/sbin") (subpath "/usr/bin") (subpath "/usr/sbin") (subpath "/usr/lib") (subpath "/System"))
;; /bin/sh resolves through the shell selector.
(allow file-read* (subpath "/private/var/select"))
;; TLS trust store and resolver configuration (macOS equivalent of /etc/ssl + /etc/resolv.conf).
(allow file-read* (subpath "/private/etc"))
;; Well-known device sinks/sources. /dev/null is the only writable path here.
(allow file-read* file-write-data (literal "/dev/null"))
(allow file-read* (literal "/dev/urandom") (literal "/dev/random") (literal "/dev/zero"))
;; Process lifecycle: fork children, signal self. No signalling of other processes.
(allow process-fork)
(allow signal (target self))
;; Hardware/OS introspection used by libc and language runtimes at startup.
(allow sysctl-read)
;; DNS via mDNSResponder. Required for ANY name resolution; see finding 4 above.
(allow mach-lookup (global-name "com.apple.mDNSResponder"))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
`

// renderSeatbeltProfile translates a SandboxPolicy into a Seatbelt `.sb`
// profile string. The posture mirrors the LinuxBackend (Landlock): default-
// deny with explicit allows derived from the policy, on top of the system
// preamble that makes a child startable at all.
//
// Field → directive mapping:
//
//   - FilesystemRules with AccessRead    → (allow file-read*  (subpath "..."))
//   - FilesystemRules with AccessWrite   → (allow file-write* (subpath "..."))
//   - FilesystemRules with AccessExecute → (allow process-exec (subpath "..."))
//     plus an implicit file-read* allow (Seatbelt requires read access to mmap
//     a binary; note this is NOT universal — Landlock treats execute and read
//     as independent rights).
//   - BindPortRules    → (allow network-bind    (local  tcp "*:<port>"))
//   - ConnectPortRules → (allow network-outbound (remote tcp "*:<port>"))
//
// # Symlink resolution (macOS correctness requirement, not a nicety)
//
// Seatbelt matches on the RESOLVED path. On macOS /tmp, /etc and /var are
// symlinks into /private, so an (subpath "/tmp") filter matches NOTHING — a
// child denied every write under /tmp while the profile plainly appears to
// grant it. DefaultPolicy grants RWX on /tmp, so rendering policy paths
// verbatim would produce a profile that looks correct and enforces a policy
// nobody wrote. Each rule path is therefore resolved with filepath.EvalSymlinks
// before it is emitted, and every symlinked ANCESTOR of the original path also
// gets a read allow so the child can traverse it.
//
// Network posture is strictly more conservative than Landlock's back-compat
// behavior: under (deny default) ALL network is denied, and only the ports
// enumerated in ConnectPortRules / BindPortRules are re-allowed. Landlock
// degrades to unrestricted networking when ConnectPortRules is empty (for
// pre-ABI-v4 kernel back-compat); Seatbelt is new code with no back-compat
// obligation, so empty port lists mean "no network". Callers that need
// outbound HTTPS must populate ConnectPortRules (DefaultPolicy seeds
// DefaultConnectPorts: 53/80/443 + the dev-server range).
func renderSeatbeltProfile(policy SandboxPolicy) (string, error) {
	if len(policy.FilesystemRules) == 0 && len(policy.BindPortRules) == 0 && len(policy.ConnectPortRules) == 0 {
		return "", fmt.Errorf(
			"seatbelt: refusing to render a default-deny profile with zero allows (would brick any child); policy must enumerate at least the workspace path",
		)
	}

	var b strings.Builder
	b.WriteString(";; Omnipus Seatbelt profile — generated by renderSeatbeltProfile.\n")
	b.WriteString(";; Posture: default-deny with explicit allows (mirrors Landlock).\n")
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n\n")
	b.WriteString(seatbeltSystemPreamble)

	// --- ADR-062 open model: blanket read and execute. ---
	//
	// These are emitted HERE, before the policy rules and before the deny block.
	// See the deny block below for the measured precedence rule that makes the
	// ordering matter; it is asserted by test rather than left to reviewer
	// vigilance, because the profile reads as correct either way.
	//
	// Both are emitted together. Opening execute while leaving read enumerated
	// stops every child starting, since Seatbelt requires read access to mmap a
	// binary — the failure looks like a broken sandbox rather than a policy
	// choice, with no diagnostic pointing at the cause.
	if policy.ReadsOpen || policy.ExecOpen {
		b.WriteString("\n;; --- Filesystem model: open (ADR-062) ---\n")
		if policy.ReadsOpen {
			b.WriteString("(allow file-read*)\n")
		}
		if policy.ExecOpen {
			b.WriteString("(allow process-exec)\n")
		}
	}

	// --- Filesystem allows (policy order preserved; duplicates are the
	// caller's responsibility, same contract as Landlock). ---
	b.WriteString("\n;; --- Filesystem allows (from SandboxPolicy.FilesystemRules) ---\n")

	// seenTraversal dedupes the symlink-ancestor read allows across rules so a
	// policy with many paths under one symlinked root emits each ancestor once.
	// Emission stays in policy order, so the output remains deterministic.
	seenTraversal := make(map[string]struct{})

	for _, rule := range policy.FilesystemRules {
		if err := validateSeatbeltPath(rule.Path); err != nil {
			return "", fmt.Errorf("seatbelt: filesystem rule path %q: %w", rule.Path, err)
		}
		if rule.Access == 0 {
			return "", fmt.Errorf("seatbelt: filesystem rule for %q has no access flags", rule.Path)
		}

		target, traversal := resolveSeatbeltPath(rule.Path)

		// A resolved path must survive the same injection validation as the
		// declared one — EvalSymlinks reads the filesystem, so its result is
		// not automatically trusted just because the input was clean.
		if err := validateSeatbeltPath(target); err != nil {
			return "", fmt.Errorf("seatbelt: filesystem rule path %q resolved to %q: %w", rule.Path, target, err)
		}

		// Read allows for any symlinked ancestor, so the child can traverse
		// the declared path even though the allow is on the resolved one.
		for _, anc := range traversal {
			if _, dup := seenTraversal[anc]; dup {
				continue
			}
			if err := validateSeatbeltPath(anc); err != nil {
				return "", fmt.Errorf("seatbelt: symlink ancestor %q of %q: %w", anc, rule.Path, err)
			}
			seenTraversal[anc] = struct{}{}
			fmt.Fprintf(&b, ";; traversal for symlinked ancestor of %s\n", rule.Path)
			fmt.Fprintf(&b, "(allow file-read* (literal %q))\n", anc)
		}

		sub := fmt.Sprintf("(subpath %q)", target)
		// Three independent emissions rather than a switch: the write line is
		// identical in both arms, and duplicating it invites the two copies to
		// drift. Output is byte-identical for all seven non-zero access
		// combinations (Access == 0 is rejected above).
		if rule.Access&AccessExecute != 0 {
			b.WriteString("(allow process-exec " + sub + ")\n")
		}
		if rule.Access&(AccessRead|AccessExecute) != 0 {
			// Execute implies read: a binary cannot be mmap'd unreadable.
			b.WriteString("(allow file-read* " + sub + ")\n")
		}
		if rule.Access&AccessWrite != 0 {
			b.WriteString("(allow file-write* " + sub + ")\n")
		}
	}

	// --- Network. Under (deny default) all network is already denied; the
	// explicit (deny network*) is emitted for auditor clarity and to make the
	// posture assertable in tests. Port allow-lists re-enable only what the
	// policy enumerates.
	//
	// Ordering was verified empirically rather than assumed: with this
	// (deny network*) sitting BETWEEN the preamble's resolver allow and the
	// port allows below, name resolution and an allowed-port HTTPS request
	// both still succeed, and a non-allow-listed port is still refused. The
	// deny does not revoke the earlier resolver allow, so it is deliberately
	// NOT re-emitted here.
	b.WriteString("\n;; --- Network (default-deny; explicit port allow-list) ---\n")
	b.WriteString("(deny network*)\n")
	for _, r := range policy.BindPortRules {
		fmt.Fprintf(&b, "(allow network-bind (local tcp \"*:%d\"))\n", r.Port)
	}
	for _, r := range policy.ConnectPortRules {
		fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"*:%d\"))\n", r.Port)
		// UDP on the SAME port the operator already allow-listed.
		//
		// Emitting TCP alone denied UDP entirely, which is stricter than the
		// policy says and stricter than Linux: Landlock ABI v4 restricts TCP
		// bind/connect only, so UDP is completely unrestricted there. The
		// operator chose a PORT; TCP-vs-UDP is a transport detail they never
		// expressed an opinion about.
		//
		// What TCP-only actually broke, measured on this platform: raw DNS
		// resolvers (dig, nslookup, Go's pure-Go resolver) on port 53, and
		// QUIC/HTTP3 on 443. The mDNSResponder allow in the preamble covers
		// the system resolver, which is why ordinary name lookups worked and
		// hid this.
		//
		// This remains far stricter than Linux — UDP is confined to the
		// allow-list rather than unrestricted.
		fmt.Fprintf(&b, "(allow network-outbound (remote udp \"*:%d\"))\n", r.Port)
	}

	// --- Secret set: denied LAST (ADR-062 §4.1, spec FR-3.2) ---
	//
	// # The precedence rule, as MEASURED rather than assumed
	//
	// Executed against real children on this platform (see
	// TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed, which reproduces the
	// whole table):
	//
	//	allow (subpath dir)   then deny (subpath file)  -> denied
	//	deny  (subpath file)  then allow (subpath dir)  -> READ SUCCEEDS
	//	allow file-read*      then deny (subpath file)  -> denied
	//	deny  (subpath file)  then allow file-read*     -> denied
	//
	// So the rule is narrower than the "last match wins" shorthand: a later
	// FILTERED allow overrides an earlier deny, while an unfiltered blanket
	// allow does not, whichever side of the deny it sits on. It is the second
	// row that makes this block's position load-bearing — $OMNIPUS_HOME is
	// granted RWX above, and that grant is exactly a filtered allow over the
	// directory these files live in. Emitted before it, every deny here would
	// be silently overridden and the profile would read as protecting a key it
	// does not protect.
	//
	// Nothing may be appended after this block. The test enforces that against
	// blanket allows too, which the measurements show are harmless — being
	// stricter than the platform requires costs nothing here, and it removes
	// the need for a future reader to re-derive which allows are safe.
	if len(policy.DeniedPaths) > 0 || len(policy.DeniedNodes) > 0 || len(policy.DeniedPathPrefixes) > 0 {
		b.WriteString("\n;; --- Secret set: denied last so no earlier allow can re-open it ---\n")
	}
	if len(policy.DeniedPaths) > 0 {
		emitted := make(map[string]struct{}, len(policy.DeniedPaths)*2)
		for _, raw := range policy.DeniedPaths {
			clean, expanded := expandUserPath(raw)
			if !expanded {
				return "", fmt.Errorf("seatbelt: denied path %q is not absolute and could not be expanded", raw)
			}
			if err := validateSeatbeltPath(clean); err != nil {
				return "", fmt.Errorf("seatbelt: denied path %q: %w", raw, err)
			}

			// Deny the declared path AND its symlink-resolved form. Seatbelt
			// matches resolved paths, so the resolved form is the one that
			// actually bites; the declared form is emitted too because these
			// paths may not exist yet, and the resolution of a path that is
			// created later can differ from the resolution computed now.
			target, _ := resolveSeatbeltPath(clean)
			for _, path := range []string{clean, target} {
				if err := validateSeatbeltPath(path); err != nil {
					return "", fmt.Errorf("seatbelt: denied path %q resolved to %q: %w", raw, path, err)
				}
				if _, dup := emitted[path]; dup {
					continue
				}
				emitted[path] = struct{}{}

				// Read AND write. A read-only deny is defeated in two syscalls:
				// rename(2) moves the file to a name the deny does not cover and
				// it reads normally afterwards, and truncate destroys the vault
				// irreversibly without reading anything at all. Both were
				// executed against a real child before this line was written.
				fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", path)
				fmt.Fprintf(&b, "(deny file-write* (subpath %q))\n", path)
			}
		}
	}

	// --- Denied NODES: the directory entries on the chain down to the work
	// dir (SandboxPolicy.DeniedNodes, fspolicy.KernelDeniedNodesFor). ---
	//
	// LITERAL, not subpath, and file-write* only. Both halves are load-bearing
	// and both were measured against real children on Darwin 25.5.0 rather
	// than reasoned about:
	//
	//	literal vs subpath  A subpath deny on $OMNIPUS_HOME/agents would deny
	//	                    the agent its OWN home, which is the whole reason
	//	                    the root is re-admitted. A literal covers the
	//	                    directory entry and nothing beneath it, so
	//	                    read/write/create inside the caller's own tree are
	//	                    all unaffected.
	//	write vs read       Denying reads here would deny TRAVERSAL of a
	//	                    directory the child must walk through to reach its
	//	                    own work dir. The residual — a child can `ls` the
	//	                    node and learn other agents'/workspaces' NAMES — is
	//	                    the one accepted divergence already pinned by
	//	                    TestKernelDeniedPaths_MatchIsCarveOutPathForPath.
	//
	// What it closes: `mv $OMNIPUS_HOME/agents $OMNIPUS_HOME/agents-old`, which
	// succeeded against a real child with the whole per-sibling deny list above
	// already in place, relocating every agent's home to a path no deny covers.
	//
	// Emitted inside this final block, after the subpath denies, so the
	// "nothing is appended after the deny block" invariant still holds: a
	// filtered allow after a deny re-opens it (see the measured precedence
	// table above), and these denies must be reachable by nothing.
	if len(policy.DeniedNodes) > 0 {
		emittedNodes := make(map[string]struct{}, len(policy.DeniedNodes)*2)
		for _, raw := range policy.DeniedNodes {
			clean, expanded := expandUserPath(raw)
			if !expanded {
				return "", fmt.Errorf("seatbelt: denied node %q is not absolute and could not be expanded", raw)
			}
			if err := validateSeatbeltPath(clean); err != nil {
				return "", fmt.Errorf("seatbelt: denied node %q: %w", raw, err)
			}
			// Declared AND symlink-resolved form, for the same reason the
			// subpath block emits both: Seatbelt matches resolved paths, and
			// on macOS a $OMNIPUS_HOME under /tmp or /var resolves elsewhere.
			target, _ := resolveSeatbeltPath(clean)
			for _, path := range []string{clean, target} {
				if err := validateSeatbeltPath(path); err != nil {
					return "", fmt.Errorf("seatbelt: denied node %q resolved to %q: %w", raw, path, err)
				}
				if _, dup := emittedNodes[path]; dup {
					continue
				}
				emittedNodes[path] = struct{}{}
				fmt.Fprintf(&b, "(deny file-write* (literal %q))\n", path)
			}
		}
	}

	// --- Denied path PREFIXES (SandboxPolicy.DeniedPathPrefixes). ---
	//
	// An anchored regex is the only Seatbelt filter that covers a path which
	// does not exist yet. `subpath` and `literal` both name a specific path,
	// and $OMNIPUS_HOME/config.json.bak-1 is NOT under
	// $OMNIPUS_HOME/config.json in the subpath sense — it is a sibling with a
	// longer name. Measured: with the enumerated deny list in place, a real
	// child read a config backup the gateway wrote after the child started.
	//
	// The `^` anchor plus a fully escaped prefix is what keeps this tight: it
	// matches exactly "this absolute prefix, then anything", never a path that
	// merely contains the prefix somewhere.
	if len(policy.DeniedPathPrefixes) > 0 {
		emittedPrefixes := make(map[string]struct{}, len(policy.DeniedPathPrefixes)*2)
		for _, raw := range policy.DeniedPathPrefixes {
			clean, expanded := expandUserPath(raw)
			if !expanded {
				return "", fmt.Errorf("seatbelt: denied path prefix %q is not absolute and could not be expanded", raw)
			}
			if err := validateSeatbeltPath(clean); err != nil {
				return "", fmt.Errorf("seatbelt: denied path prefix %q: %w", raw, err)
			}
			// A prefix names no existing file, so it goes through the SAME
			// resolver the subpath denies use rather than a private
			// EvalSymlinks: resolveSeatbeltPath falls back to the deepest
			// EXISTING ancestor and re-attaches the remainder, which is the
			// case that matters here and the case a hand-rolled version got
			// wrong (a bare EvalSymlinks on the parent fails too when the
			// parent does not exist either, emitting an unresolved /tmp/… form
			// that matches nothing on macOS while reading as protection —
			// caught by TestSeatbeltProfile_PrefixDenyIsSingleEscapedAndAnchored).
			//
			// The traversal allows resolveSeatbeltPath also returns are
			// deliberately dropped: nothing may be emitted after the deny
			// block, and the subpath deny loop above drops them for the same
			// reason.
			target, _ := resolveSeatbeltPath(clean)
			for _, form := range []string{clean, target} {
				if err := validateSeatbeltPath(form); err != nil {
					return "", fmt.Errorf("seatbelt: denied path prefix %q resolved to %q: %w", raw, form, err)
				}
				if _, dup := emittedPrefixes[form]; dup {
					continue
				}
				emittedPrefixes[form] = struct{}{}
				// NOT %q. Go quoting would escape the backslashes this
				// escaper just inserted, emitting `\\.` where SBPL needs `\.`
				// — measured: the doubled form matches nothing and the backup
				// stays readable, with a profile that reads as protecting it.
				// Safe because validateSeatbeltPath has already rejected any
				// double quote or backslash in the input.
				pattern := "^" + escapeSeatbeltRegex(form)
				fmt.Fprintf(&b, "(deny file-read* (regex #\"%s\"))\n", pattern)
				fmt.Fprintf(&b, "(deny file-write* (regex #\"%s\"))\n", pattern)
			}
		}
	}

	return b.String(), nil
}

// escapeSeatbeltRegex renders a literal string as a Seatbelt regex fragment.
//
// Allow-list, not deny-list: every byte outside [A-Za-z0-9/_-] is backslash
// escaped. A deny-list of "the metacharacters I could think of" is how an
// unescaped `.` or `+` in a user's home path turns an anchored deny into a
// looser pattern, and the failure is silent — the profile still renders, still
// looks like protection, and matches the wrong set.
//
// Callers must have passed the input through validateSeatbeltPath first, which
// already rejects the two bytes that could break OUT of the quoted string
// (a double quote and a backslash) as well as control and non-printable runes.
// This function is about regex semantics, not about string quoting.
func escapeSeatbeltRegex(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Bytes >= 0x80 are the continuation/lead bytes of a multi-byte UTF-8
		// rune. A backslash in front of one is not an escape, it is a new and
		// undefined two-byte sequence, so they are passed through verbatim —
		// none of them is a regex metacharacter, which are all ASCII.
		isSafe := c >= 0x80 ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '/' || c == '_' || c == '-'
		if !isSafe {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// resolveSeatbeltPath returns the path a Seatbelt filter must name in order to
// actually match p, plus the symlinked ancestors of p that need a read allow
// so the child can traverse the declared path.
//
// Seatbelt matches resolved paths. On macOS /tmp -> private/tmp, /etc ->
// private/etc and /var -> private/var, so emitting the declared path verbatim
// yields a filter that matches nothing (see renderSeatbeltProfile).
//
// If the path does not exist yet, EvalSymlinks fails and the cleaned declared
// path is returned unchanged. That mirrors Landlock's contract, where Apply()
// skips missing paths with a warning rather than failing the whole policy: a
// workspace directory that has not been created yet must not brick the render.
func resolveSeatbeltPath(p string) (target string, traversal []string) {
	clean := filepath.Clean(p)

	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// The path does not exist yet (a workspace created lazily, an operator
		// entry for a directory not made yet). Emitting the declared path
		// verbatim would be the ORIGINAL BUG in miniature: under a symlinked
		// ancestor such as macOS's /tmp, /etc or /var, `(subpath "/tmp/x")`
		// matches nothing, so the profile reads as granting the path while the
		// child is denied it, silently.
		//
		// Resolve the deepest ancestor that DOES exist and re-attach the
		// remainder, which is correct for the not-yet-created case.
		if base, rest, ok := deepestExistingAncestor(clean); ok {
			resolved = filepath.Join(base, rest)
			if resolved != clean {
				return resolved, symlinkedAncestors(clean)
			}
		}
		return clean, symlinkedAncestors(clean)
	}
	if resolved == clean {
		// Nothing to resolve; no ancestor needs a traversal allow.
		return clean, nil
	}

	return resolved, symlinkedAncestors(clean)
}

// symlinkedAncestors returns the ancestors of the DECLARED path that are
// themselves symlinks and therefore need a read allow for the child to traverse
// them. Walking the declared path (not the resolved one) is the point: those
// are the components the child will actually walk through.
func symlinkedAncestors(clean string) []string {
	var traversal []string
	for _, anc := range ancestorPaths(clean) {
		fi, lerr := os.Lstat(anc)
		if lerr != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			traversal = append(traversal, anc)
		}
	}
	return traversal
}

// deepestExistingAncestor splits p into the deepest existing prefix (with
// symlinks resolved) and the not-yet-created remainder.
func deepestExistingAncestor(p string) (base, rest string, ok bool) {
	ancestors := ancestorPaths(p)
	for i := len(ancestors) - 1; i >= 0; i-- {
		resolved, err := filepath.EvalSymlinks(ancestors[i])
		if err != nil {
			continue
		}
		rel, relErr := filepath.Rel(ancestors[i], p)
		if relErr != nil {
			return "", "", false
		}
		if rel == "." {
			return resolved, "", true
		}
		return resolved, rel, true
	}
	return "", "", false
}

// ancestorPaths returns every path prefix of p, shallowest first, INCLUDING p
// itself: "/tmp/a/b" → ["/tmp", "/tmp/a", "/tmp/a/b"]. Including p is
// load-bearing — it is how a symlinked leaf such as /tmp gets its own traversal
// allow. The root "/" is excluded because the preamble grants it already.
func ancestorPaths(p string) []string {
	p = filepath.Clean(p)
	if p == string(filepath.Separator) || p == "." {
		return nil
	}

	parts := strings.Split(strings.TrimPrefix(p, string(filepath.Separator)), string(filepath.Separator))
	out := make([]string, 0, len(parts))
	cur := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = cur + string(filepath.Separator) + part
		out = append(out, cur)
	}
	return out
}

// validateSeatbeltPath enforces that a path is safe to embed in a Seatbelt
// (subpath ...) filter, AND that emitting it via fmt.Sprintf("%q", ...) will
// reproduce the path byte-for-byte inside the quotes.
//
// # Go's %q is not SBPL's escape vocabulary — and the mismatch fails OPEN
//
// The render site emits every path with %q (strconv.Quote). That is a correct
// INJECTION defense: strconv.Quote never emits a bare `"`, so no path can
// terminate the filter and append its own directive. It is NOT a correct
// ENCODING, and an earlier version of this comment claiming it "escapes every
// special byte" was the reason the gap below survived review.
//
// strconv.Quote escapes any rune that is not unicode.IsPrint, using Go's OWN
// backslash vocabulary: a U+00A0 becomes the six characters backslash-u-0-0-a-0,
// and an invalid byte becomes backslash-x-f-f. SBPL's reader does not know those
// escapes. Rather than erroring, it DROPS the unrecognised backslash and keeps
// the following characters literally, so that escape decays to the five ASCII
// characters u00a0. The rendered filter then names a path that does not exist,
// and matches NOTHING.
//
// MEASURED against a real child on macOS 26.5.2, with a directory whose name
// contains U+00A0 (written <NBSP> below -- the real byte is deliberately NOT
// present in this source file, since an invisible rune in a comment about
// invisible runes is exactly the trap this whole check exists to close):
//
//	emitted:  (deny file-read* (subpath ".../home\u00a0x/master.key"))
//	  sandbox-exec -f p.sb /bin/cat ".../home<NBSP>x/master.key"
//	  -> printed the file contents, exit 0            (DENY VOIDED)
//	control, the same deny carrying the raw UTF-8 bytes c2 a0:
//	  -> "Operation not permitted", exit 1            (deny enforced)
//
// One such rune anywhere in $OMNIPUS_HOME voids EVERY deny that names a path
// under it — the whole secret set at once, read AND write, so a child could
// also rewrite config.json. Under the ADR-062 open model the blanket
// `(allow file-read*)` is then completely unopposed. Nothing reports this:
// sandbox-exec accepts the profile, boot succeeds, and the profile reads as
// correct. Triggers are ordinary, not exotic: U+00A0 (a non-breaking space,
// which is what a path pasted from a web page carries), U+200D (present in
// every multi-person emoji), U+3000 (ordinary in CJK paths), and any invalid
// UTF-8 byte.
//
// # The rule, and why it is a refusal rather than a re-encoding
//
// A path is accepted only when strconv.Quote(p) == `"` + p + `"` — i.e. %q
// changed nothing but the surrounding quotes. Anything else is refused.
//
// The alternative, emitting raw UTF-8 bytes, was measured to work (the control
// above) but is rejected on principle: it would make profile safety depend on a
// hand-rolled escaper agreeing with an undocumented reader for every byte
// sequence, and getting that wrong fails open again with no diagnostic. A
// refusal fails CLOSED, which is the same contract as the rest of ADR-062 —
// renderSeatbeltProfile's error propagates to SeatbeltBackend.Apply (aborting
// boot) and to ApplyToCmd (aborting the spawn), never to an unconfined child.
//
// The byte scan below runs FIRST so the ASCII injection characters keep their
// precise "forbidden character" diagnostic; the round-trip check is what
// catches everything above 0x7f that the scan cannot see.
//
// Relative paths are rejected because (subpath "...") is interpreted relative
// to the sandbox-exec CWD, which would make the allow non-deterministic.
func validateSeatbeltPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute (relative paths resolve against sandbox-exec CWD)")
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '"' || c == '\\' || c < 0x20 || c == 0x7f {
			return fmt.Errorf(
				"path contains a forbidden character (byte 0x%02x at offset %d) that could inject profile directives",
				c, i,
			)
		}
	}
	if quoted := strconv.Quote(p); quoted != `"`+p+`"` {
		return fmt.Errorf(
			"path is not representable in a Seatbelt filter: Go quoting rewrites it to %s, and SBPL drops the backslash escapes rather than decoding them, "+
				"so the emitted filter would match nothing and silently enforce no policy at all "+
				"(non-printable or non-UTF-8 runes such as U+00A0, U+200D, U+3000 or an invalid byte); "+
				"rename the path to printable ASCII-or-UTF-8 characters",
			quoted,
		)
	}
	return nil
}

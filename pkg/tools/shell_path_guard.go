// shell_path_guard.go: Command-text path guard for the bash tool - workspace containment for every path a command mentions, with the ADR-068 option A read/write classification that proves a read.

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

var (
	// absolutePathPattern matches absolute file paths in commands (Unix and Windows).
	// absolutePathPattern extracts absolute-path candidates from raw command
	// TEXT so guardCommand can reject references outside the workspace.
	//
	// Three properties are load-bearing:
	//
	//  1. The path BODY stops at shell metacharacters (; | & ( ) < > , { } [ ]
	//     ! * ` $ ~ \), not just at whitespace and quotes. The original class
	//     was `[^\s"']+`, so the ubiquitous idiom `2>/dev/null;` extracted the
	//     candidate `/dev/null;` — WITH the semicolon — which does not match
	//     the safePaths key "/dev/null". The exemption silently missed and the
	//     whole command was rejected, including every innocent fragment
	//     chained beside it. The set was later widened (this revision) to
	//     also stop at brace/bracket/glob punctuation so multi-path shell
	//     shapes like `{/etc/shadow,/etc/passwd}` or `cat[/etc/shadow]` split
	//     into the individual candidates they textually contain instead of
	//     gluing trailing punctuation onto (or past) the real path.
	//
	//  2. The match must begin at a TOKEN BOUNDARY, captured in group 1.
	//     Plain single-character boundaries (whitespace, quote, = : ; , { }
	//     [ ] ! * ` $ ~ \ and the shell metacharacters above) cover most
	//     cases. Without this restriction the pattern matched the first `/`
	//     found ANYWHERE, including one in the middle of a relative path:
	//     `-o build/app.min.js` yielded the fabricated candidate
	//     `/app.min.js`, which resolves to the filesystem root and was
	//     rejected as outside the workspace — even though the real argument
	//     was a relative path inside it.
	//
	//     Two shapes need MORE than a single boundary character because no
	//     single character sits directly before the leading `/`:
	//
	//       - Attached short flags: `-o/etc/passwd`, `-I/etc`, `-C/etc`. The
	//         candidate has the exact same textual shape as the legitimate
	//         relative path in `-o build/app.min.js` — a `/` following a
	//         `-flag` token — so the two must be told apart by what comes
	//         right after the flag, not by the flag itself. This pattern
	//         additionally matches `(?:^|\s)-[A-Za-z]` immediately followed
	//         by the candidate body, i.e. the flag itself must sit at a
	//         token start (start of command or preceded by whitespace) AND
	//         the very next character after the flag letter must be `/`. A
	//         relative arg (`-o build/…`) never satisfies the second part
	//         (there is a space, not a `/`, right after `-o`), and a
	//         hyphenated relative path segment (`build-x/output`) never
	//         satisfies the first part (the `-x` is not at a token start —
	//         `d` precedes it, not whitespace) — so neither is affected.
	//       - Variable-expansion prefixes: `$HOME/.ssh/id_rsa`,
	//         `${HOME}/.ssh/id_rsa`. This pattern additionally matches
	//         `(?:^|\s)\$\{?[A-Za-z_][A-Za-z0-9_]*\}?` (a token-start shell
	//         variable reference) immediately followed by the candidate
	//         body, so a resolved-at-runtime path prefix does not hide an
	//         absolute suffix from this compile-time text scan.
	//
	//  3. The boundary set and the excluded body set are almost — but
	//     deliberately NOT — the same metacharacters: `:` and `=` are
	//     boundary-only, never excluded from the body. `=` bodies are
	//     unrestricted by design (unchanged from the original version).
	//     `:` is unrestricted so that a colon-joined path LIST (`PATH=`
	//     assignments, `-I a:b`-style compiler/linker flags) is captured as
	//     ONE candidate rather than split into several fragments that each
	//     independently look like a bare absolute path outside the
	//     workspace. guardCommand recognizes that specific shape
	//     (colonPathListPattern) and evaluates EACH `:`-separated segment
	//     against the same workspace-boundary check applied to a bare
	//     candidate (safePaths / allowedPathPatterns exemption, then
	//     containment) — see guardCommand's colon-list check. A list is
	//     allowed only when every segment is inside the workspace or
	//     exempt; a list containing any out-of-workspace segment is
	//     blocked, same as a single bare candidate would be.
	//
	// Worked examples:
	//   - `which node 2>/dev/null; echo done` -> candidate "/dev/null", exempt
	//     via safePaths (rule 1).
	//   - `curl -sL -o build/app.min.js https://…` -> no candidate at all
	//     for `build/app.min.js` (rule 2, general case).
	//   - `curl -o/etc/passwd https://x` -> candidate "/etc/passwd", blocked
	//     (rule 2, attached-flag case) — contrast with the previous example,
	//     where the space after `-o` prevents the flag alternative from
	//     firing.
	//   - `PATH=/usr/bin:/usr/local/bin make` -> candidate
	//     "/usr/bin:/usr/local/bin" (ONE match, per rule 3); guardCommand's
	//     colon-list check splits it into "/usr/bin" and "/usr/local/bin"
	//     and evaluates each — both are outside the workspace, so the
	//     command is BLOCKED (consistent with what a bare `/usr/bin`
	//     candidate would do).
	//
	// Callers MUST read group 1, not the whole match, since the match also
	// consumes the leading boundary text (which may be more than one
	// character — see rule 2's attached-flag and variable-prefix cases).
	//
	// The attached-flag alternative is `-[A-Za-z]+`, not `-[A-Za-z]`. With
	// exactly one letter, `-o/etc/passwd` was caught but a COMBINED short flag
	// with an attached path was not: in `tar -cf/etc/passwd` the character
	// before `/` is `f`, which is neither a single flag letter after `-` nor a
	// boundary character, so no candidate was extracted at all and the
	// workspace-boundary check never ran on that path. Same for `cc -Wl/etc/x`.
	// Defence-in-depth rather than the primary control — the kernel sandbox is
	// that — but a guard that misses the combined form misses the form people
	// actually type.
	absolutePathPattern = regexp.MustCompile(
		`(?:^|[\s"'=:;,{}\[\]!*` + "`" + `$~\\|&()<>]|(?:^|\s)-[A-Za-z]+|(?:^|\s)\$\{?[A-Za-z_][A-Za-z0-9_]*\}?)` +
			`([A-Za-z]:\\[^\\\s"';,{}\[\]!*` + "`" + `$~|&()<>]+` +
			`|/[^\s"';,{}\[\]!*` + "`" + `$~\\|&()<>]+)`,
	)

	// colonPathListPattern recognizes a colon-joined list of two or more
	// absolute Unix paths — e.g. "/usr/bin:/usr/local/bin" — the shape
	// produced by `PATH=` assignments and by multi-path compiler/linker
	// flags (`-I a:b`). absolutePathPattern's Unix body does not exclude
	// `:` (see that pattern's rule 3), so a colon-joined list is captured
	// as a single raw candidate rather than fragmented at each colon.
	// guardCommand matches that raw candidate against this pattern and, on
	// a match, SPLITS it on `:` and evaluates each segment independently
	// against the same workspace-boundary check applied to a bare
	// candidate (safePaths exemption, allowedPathPatterns exemption, then
	// containment relative to cwd). A list is allowed only when every
	// segment clears that check; a list containing any out-of-workspace,
	// non-exempt segment is blocked. This is deliberately narrow — every
	// segment must independently look like an absolute path (start with
	// `/`, contain none of the same excluded punctuation) — so it does not
	// exempt a single path with a stray colon suffix appended to it
	// (`/etc/passwd:evil` has a second segment that does not start with
	// `/`, so it does not match and is still evaluated as a single,
	// literal candidate).
	colonPathListPattern = regexp.MustCompile(
		`^/[^\s"';,{}\[\]!*` + "`" + `$~\\:]+` +
			`(?::/[^\s"';,{}\[\]!*` + "`" + `$~\\:]+)+$`,
	)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

// --- deny-pattern guard ------------------------------------------------------

// guardCommand applies, in order: (1) the hardcoded baseline (FR-B4,
// unconditional) — both its regex half (defaultDenyPatterns) and its structural
// half (substitutionGuard) — (2) the opt-in operator-extensible layer,
// and (3) a legacy defense-in-depth scan for absolute paths referenced in the
// command TEXT (independent of the cwd parameter guard above), gated on
// restrictToWorkspace exactly as the pre-consolidation exec tool did.
//
// ADR-068 (2026-08-23) named step 3 the system's THIRD file-access rule layer
// and corrected the record about what it does: it had been enforcing the
// pre-ADR-062 CONFINED model for reads as well as writes, which is why
// `bash cat ~/notes.txt` was refused while ADR-063 documented it as allowed.
// Under the founder's ruling (ADR-068 §2.1 option A) this layer now
// distinguishes the two: a path reference outside the working directory that is
// PROVABLY a read is allowed; anything else — every write, and every reference
// this scanner cannot prove is a read — still requires a workspace mount,
// exactly as before. The proof lives in pathUseClassifier below, and it is
// deliberately allowlist-shaped: see its doc comment for why "not provably a
// read" must mean "treat as a write", never the other way round.
func (t *ExecTool) guardCommand(ctx context.Context, command, cwd string) string {
	if msg := applyDenyPatterns(command, t.denyPatterns, nil); msg != "" {
		return msg
	}
	// FR-B4, structural half: command substitutions are judged by what they
	// run and where they sit, not by their mere presence. Unconditional and
	// not disableable, exactly like the regex baseline above.
	if msg := substitutionGuard(command); msg != "" {
		return msg
	}
	if t.enableOperatorDenyPatterns {
		if msg := applyDenyPatterns(command, t.operatorDenyPatterns, nil); msg != "" {
			return msg
		}
	}

	if !t.restrictToWorkspace {
		return ""
	}

	cmd := strings.TrimSpace(command)
	if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
		return "Command blocked by safety guard (path traversal detected)"
	}

	cwdPath, err := filepath.Abs(cwd)
	if err != nil {
		return "cannot resolve working directory"
	}

	// ADR-063 alignment: the command-TEXT scan below must honor the SAME
	// workspace mounts that the kernel policy (turnKernelPolicy ->
	// ResolveTurnFSPolicy) and the app-layer path resolver (matchedAllowedRoot)
	// already grant write to. Without this, an absolute path under a mounted
	// folder is rejected here with "no mount covers it" before the kernel —
	// which WOULD allow it — ever runs, so the guard's own message described a
	// mount check the function never performed. Resolve mount roots from that
	// one authoritative source. On resolve error, fall back to NO mount
	// exemption: the guard then stays exactly as strict as before, and (unlike
	// the kernel path, which fails closed by ABORTING the spawn) it never aborts
	// a command over a transient mount-resolution hiccup.
	//
	// The KERNEL sandbox remains the authoritative boundary, not this text scan.
	// This guard is deliberately COARSER than the kernel for the mount exemption:
	// it grants read+write+exec under a mount root where the kernel grants only
	// read+write; it matches lexically (filepath.Abs) where the kernel enforces
	// on realpaths (a symlink inside a mount pointing out is passed here, denied
	// by the kernel); and it does NOT subtract the per-turn secret set, so a
	// mount that overlaps $OMNIPUS_HOME could name a secret path here that the
	// kernel still denies. On Linux 5.13+ the kernel blocks every one of those
	// deltas. On a fallback platform (no Landlock) bash is governed by the boot
	// profile, which already grants $OMNIPUS_HOME broadly — this scan was never a
	// secret boundary there. (Subtracting KernelDeniedPathsFor here to close the
	// gap needs a home/work-dir path-form match this call site can't cheaply
	// guarantee; it is tracked as follow-up, not attempted inline.)
	var mountRoots []string

	// ADR-068 read exemption vs the turn's secret set (code review, 2026-08-23,
	// second pass). Opening reads outside the working directory must NOT open
	// the secret set with them. secretGuardPatterns (the literal text backstop)
	// covers only fspolicy.SecretEntriesAlways — config.json, master.key,
	// cli.token, entities — and deliberately NOT the PER-TURN roots `agents/`
	// and `workspaces/`. Those were out of reach here only as a side effect of
	// blocking every outside-WorkDir path, so granting reads removed the sole
	// app-layer barrier: another agent's SOUL.md and another workspace's work
	// tree became readable from bash wherever the kernel layer is absent or off
	// (a non-Landlock host, or sandbox.mode=off) — the exact regression
	// pkg/sandbox/derive_from_fspolicy.go:160 records as previously fixed.
	//
	// IsCarveOut is the right primitive here, and the two obvious alternatives
	// are both wrong:
	//   - DeniedPathsFor re-admits a whole per-turn ROOT, so during a workspace
	//     turn every OTHER workspace is re-admitted with the caller's own. The
	//     comment at derive_from_fspolicy.go:160 warns against exactly this.
	//   - KernelDeniedPathsFor is correct but enumerates the sibling set from
	//     disk on every call — thousands of entries, per bash command, matched
	//     lexically. Wrong shape for a per-command text guard.
	// IsCarveOut answers per PATH with no listing, applies the own-tree
	// exception per-root, and resolves by filesystem identity rather than by
	// string prefix — which is also what makes this immune to the
	// lexical-vs-realpath mismatch that made the first attempt at this fix
	// inert on macOS (/var vs /private/var).
	//
	// It is ALSO the same primitive the app-layer file tools call, so `bash cat
	// X` and `read_file X` now agree about the secret set — the file-access
	// spec's R-4 ("a rule must not depend on which tool asks"), which ADR-068
	// §1.1 found was never true on the bash path.
	//
	// FAIL CLOSED: when the policy cannot be resolved there is no way to tell
	// the caller's own tree from anyone else's, so the read exemption is
	// withheld entirely (readPolicyOK=false) and the guard behaves exactly as
	// it did before ADR-068. Never widen on an error path.
	var (
		turnPolicy   fspolicy.FSPolicy
		readPolicyOK bool
	)
	if authored, ferr := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace); ferr == nil {
		mountRoots = authored.AllowedRoots
		turnPolicy = authored
		readPolicyOK = true
	} else {
		// The fail-closed direction is right, but silence is not: with no
		// policy the agent loses BOTH its mounts and the ADR-068 read
		// exemption, so an operator sees writes to a folder they demonstrably
		// mounted refused with "request a mount for that folder" — advice for
		// a thing they already did. Log it so the degraded mode is
		// diagnosable instead of looking like the mount never existed
		// (code review round 3).
		logger.WarnCF("tools", "bash guard: turn filesystem policy unresolved; mounts and the ADR-068 read exemption are both withheld for this command",
			map[string]any{"working_dir": t.workingDir, "error": ferr.Error()})
	}

	// Web URL schemes whose path components (starting with //) should be
	// exempt from workspace sandbox checks. file: is intentionally excluded
	// so file:// URIs are still validated against the workspace boundary.
	webSchemes := []string{"http:", "https:", "ftp:", "ftps:", "sftp:", "ssh:", "git:"}

	// ADR-068 §1: read/write classification for this command. Built ONCE — it
	// scans the command for quoting once and answers per-candidate questions
	// from that single pass. A command it cannot parse confidently classifies
	// every candidate as a write, i.e. behaves exactly like the pre-ADR-068
	// guard.
	classifier := newPathUseClassifier(cmd)

	// Group 1 is the path itself; the full match also consumes the leading
	// boundary character, so indices 2:4 (not 0:2) are what we want. Reading
	// the whole match here would re-introduce the leading space/operator into
	// every candidate and break both filepath.Abs and the safePaths lookup.
	matchIndices := absolutePathPattern.FindAllStringSubmatchIndex(cmd, -1)
	for _, loc := range matchIndices {
		start, end := loc[2], loc[3]
		if start < 0 || end < 0 {
			continue
		}
		raw := cmd[start:end]

		// EXPANSION-DERIVED CANDIDATE (code review round 3, 2026-08-24).
		//
		// absolutePathPattern treats `~` and a token-start `$VAR` as BOUNDARY
		// characters and captures only the suffix after them, so
		// `$HOME/.omnipus/agents/other/SOUL.md` reaches this loop as the
		// candidate `/.omnipus/agents/other/SOUL.md` — a path that names no
		// real file. Judging that phantom is meaningless: it is outside the
		// work dir, no mount covers it, and IsCarveOut cannot match it because
		// the carve-out roots are anchored at the real $OMNIPUS_HOME. The shell
		// then expands the variable and touches the REAL file.
		//
		// Measured before this guard: `cat $HOME/.omnipus/agents/victim/SOUL.md`
		// and the `~` spelling were both ALLOWED while the literal absolute
		// path was correctly blocked — the ADR-068 read exemption reachable
		// through a different spelling of the same file.
		//
		// The boundary the regex consumed sits between loc[0] and loc[2], so
		// the fact is available here and nowhere downstream. A candidate built
		// this way is UNRESOLVABLE by a text scan: fail closed. It earns
		// neither the read exemption nor the inside-the-work-dir pass, because
		// the suffix can be made to land inside the work dir while the real
		// target is anywhere on the host (`printf x > $HOME<abs-cwd>/pwned`).
		expansionDerived := false
		if loc[0] >= 0 && loc[0] < start {
			if prefix, resolved, isExpansion := expandCandidatePrefix(cmd[loc[0]:start]); isExpansion {
				if resolved {
					// Judge the REAL path. `cat ~/notes.txt` stays an ordinary
					// outside-the-work-dir read (allowed), while
					// `cat ~/.omnipus/agents/other/SOUL.md` now resolves onto
					// the carve-out and is refused — the same verdict its
					// literal absolute spelling already got.
					raw = filepath.Join(prefix, raw)
				} else {
					expansionDerived = true
				}
			}
		}

		// The classification is a property of WHERE the candidate sits in the
		// command text, so it must be computed here (byte offsets exist only at
		// this call site) and threaded down into checkPathSegment, which sees
		// the path string alone.
		use := classifier.classify(start)
		if expansionDerived {
			use.readOnly = false
		}

		// Colon-joined path list (PATH= assignments, -I a:b-style flags):
		// each `:`-separated segment is checked independently against the
		// workspace boundary (the same check applied to a bare candidate
		// below), rather than the whole list being skipped wholesale. See
		// colonPathListPattern's doc comment for why this shape is
		// recognized narrowly, and why an unconditional skip here would
		// have let a colon-joined list smuggle an out-of-workspace segment
		// past the guard entirely.
		//
		// Every segment of the list inherits the SAME read/write
		// classification: the list is one token in one argument position, so
		// whatever the command does with it, it does with all of it.
		if strings.Contains(raw, ":") && colonPathListPattern.MatchString(raw) {
			for _, seg := range strings.Split(raw, ":") {
				if msg := t.checkPathSegment(seg, cwdPath, mountRoots, turnPolicy, readPolicyOK, use, expansionDerived); msg != "" {
					return msg
				}
			}
			continue
		}

		if strings.HasPrefix(raw, "//") && start > 0 {
			before := cmd[:start]
			isWebURL := false
			for _, scheme := range webSchemes {
				if strings.HasSuffix(before, scheme) {
					isWebURL = true
					break
				}
			}
			if isWebURL {
				continue
			}
		}

		if msg := t.checkPathSegment(raw, cwdPath, mountRoots, turnPolicy, readPolicyOK, use, expansionDerived); msg != "" {
			return msg
		}
	}

	return ""
}

// pathUseVerdict is what the classifier can say about one absolute-path
// candidate from the command TEXT alone. readOnly is the ADR-068 proof;
// exec and reason exist so a refusal can name WHY the proof failed (UAT
// 2026-09-13 D-44/D-66) instead of describing every unproven reference as
// "a WRITE". (A former `head` field carried the segment's command word too,
// but no reader ever consumed it — the reasons embed the word via %q where
// it matters — so it was deleted as dead code, Claude review 2026-09-14
// cut-list; do not re-add it without a reader.)
type pathUseVerdict struct {
	// readOnly is true only when rules 1-6 all hold.
	readOnly bool
	// exec is true when the candidate sits in the segment's command position
	// — the command would RUN it, which is neither a read nor a write.
	exec bool
	// reason explains, for a human or an agent, why the read proof failed.
	// Empty when readOnly is true.
	reason string
}

// checkPathSegment evaluates a single absolute-path candidate — either a
// bare candidate from the main scan loop, or one `:`-separated segment of a
// colon-joined path list — against the safePaths exemption, the
// operator-configured allowlist, the workspace-containment boundary, and the
// turn's workspace mounts (mountRoots, from ResolveTurnFSPolicy.AllowedRoots).
// Returns "" when the segment is allowed, or a rejection message otherwise.
//
// use is the caller's ADR-068 classification of this candidate: use.readOnly
// is true only when guardCommand's pathUseClassifier could PROVE, from the
// command text, that the reference is a read. It changes exactly one thing —
// the final out-of-working-directory rejection (and, via use.exec/use.reason,
// what that rejection SAYS). Everything above that point (safePaths, the
// operator allowlist, containment, mounts) is identical for reads and writes,
// so no existing exemption widens or narrows because of this parameter.
func (t *ExecTool) checkPathSegment(raw, cwdPath string, mountRoots []string, turnPolicy fspolicy.FSPolicy, readPolicyOK bool, use pathUseVerdict, expansionDerived bool) string {
	readOnly := use.readOnly
	p, err := filepath.Abs(raw)
	if err != nil {
		return "Command blocked by safety guard (cannot resolve path)"
	}

	// An UNRESOLVABLE expansion (see guardCommand's scan loop): the candidate is
	// only the suffix after a `$VAR` whose value this process cannot know, so
	// every check below would reason about a path that names no real file.
	// `~`, `$HOME` and `$OMNIPUS_HOME` are resolved at the call site instead and
	// never arrive here; anything else is refused. Fail closed.
	if expansionDerived {
		return fmt.Sprintf(
			"Command blocked by safety guard (unresolvable path): %q follows a shell variable whose value "+
				"the guard cannot know, so it cannot tell which file the command will actually open. "+
				"Rule: bash workspace path guard (RestrictToWorkspace). "+
				"Fix: write the path literally, or use ~ / $HOME / $OMNIPUS_HOME, which the guard does resolve.", raw)
	}

	if safePaths[p] {
		return ""
	}
	if isAllowedPath(p, t.allowedPathPatterns) {
		return ""
	}

	rel, err := filepath.Rel(cwdPath, p)
	if err != nil {
		return "Command blocked by safety guard (cannot resolve relative path)"
	}
	if strings.HasPrefix(rel, "..") {
		// Outside the working dir — but a workspace mount may cover it. The
		// kernel policy and the app-layer path resolver both grant paths under
		// an AllowedRoots mount (matchedAllowedRoot); the guard must not reject a
		// write the kernel would permit. This is the "and no mount covers it"
		// branch finally consulting the mounts its message has always named.
		if _, ok := matchedAllowedRoot(p, mountRoots); ok {
			return ""
		}

		// ADR-068 §2.3: match the mount roots on the RESOLVED path too.
		// Mount.HostPath is realpath-resolved when the mount is created
		// (pkg/workspace/mount.go:37), while p above is only lexically absolute
		// (filepath.Abs follows no symlinks). On macOS the two forms disagree
		// constantly — /tmp is a symlink to /private/tmp, /var to /private/var —
		// so an operator could approve a mount and watch every write to it still
		// be refused with "no mount covers it", the mount plainly existing. That
		// is the single worst failure mode this guard has: a denial that
		// contradicts the operator's own explicit grant.
		//
		// resolvePathAgainstExistingAncestor (filesystem.go) is the same
		// deepest-existing-ancestor resolver resolveAbsPath uses, so a
		// not-yet-created file under a mounted directory still resolves through
		// its existing parent. NON-FATAL by design, matching the mount-root
		// resolution above: a resolve error (nothing on the path exists, an
		// unreadable ancestor, a symlink loop) leaves the candidate judged on
		// its lexical form alone — i.e. exactly the pre-ADR-068 behaviour — and
		// never aborts the command. This can only ADD a mount match, never
		// remove one, because the lexical match above has already been tried.
		if resolved, rerr := resolvePathAgainstExistingAncestor(p); rerr == nil && resolved != p {
			if _, ok := matchedAllowedRoot(resolved, mountRoots); ok {
				return ""
			}
		}

		// ADR-068 §1/§2.1 option A: reads outside the working directory are
		// allowed; writes outside it still require a mount. readOnly is true
		// only when the command text PROVES the reference is a read (see
		// pathUseClassifier) — an unproven reference arrives here as a write and
		// falls through to the rejection below, which is the pre-ADR-068
		// behaviour unchanged.
		if readOnly && readPolicyOK && !t.inTurnSecretSet(p, turnPolicy) {
			return ""
		}

		// ADR-068 §6: an unexplained denial is what drives operators to switch
		// off the entire boundary rather than the one rule in their way, so the
		// message names the rule, the field behind it, and both ways to change
		// it. `sandbox.workspace_path_guard` is the operator-facing key that
		// resolves into AgentDefaults.RestrictToWorkspace (pkg/config/sandbox.go);
		// the env var is the recovery path that outranks it. Neither is the
		// kernel sandbox (`sandbox.mode`) — the confusion between the two is
		// precisely what UAT defect 002 reported.
		return outsideWorkDirRefusal(p, cwdPath, use, readOnly && !readPolicyOK)
	}
	return ""
}

// outsideWorkDirRefusal builds the message for an absolute path outside the
// working directory that no mount covers. It says exactly which of three
// things was refused — running a program, a reference the guard could not
// prove is a read, or a read withheld because the turn policy was
// unresolvable — instead of calling every one of them "a WRITE" (UAT
// 2026-09-13 D-66, D-44). It also states plainly what this guard is (D-14):
// a scan of the command text, which a path assembled at runtime never
// reaches, and names the layer that actually enforces the boundary on this
// host.
func outsideWorkDirRefusal(p, cwdPath string, use pathUseVerdict, readWithheld bool) string {
	var what string
	switch {
	case use.exec:
		what = "RUNNING a program from outside the working directory by its absolute path is refused: ADR-068 opens reads only, and executing is neither a read nor a write. " +
			"Fix: copy or install the program inside the workspace, or run it through an approved mount."
	case readWithheld:
		what = "This looks like a read, but the turn's filesystem policy could not be resolved, so the read exemption is withheld for this command (fail closed). Retry; if it persists, the gateway log names the cause."
	default:
		reason := use.reason
		if reason == "" {
			reason = "the guard could not prove from the command text that the reference is a read"
		}
		what = fmt.Sprintf("The guard treats this reference as a WRITE because %s. Reads outside the working directory are allowed (ADR-068) only when the guard can PROVE the reference is a read from the command text; every other reference needs an approved workspace mount. "+
			"read_file and list_directory answer the same read without this limitation.", reason)
	}
	return fmt.Sprintf(
		"Command blocked by safety guard (path outside working dir): %q is outside the effective working directory %q and no mount covers it. %s "+
			"Rule: bash workspace path guard (RestrictToWorkspace). "+
			"Fixes: request a mount for that folder (request_mount), or have an operator set sandbox.workspace_path_guard=false (env OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false). "+
			"This is NOT the kernel sandbox setting (sandbox.mode). %s",
		p, cwdPath, what, guardNatureStatement())
}

// guardNatureStatement is the honest one-liner every path-guard refusal
// carries (UAT 2026-09-13 D-14): this guard reads the command TEXT, so a path
// the command assembles at runtime (an interpreter concatenating strings) is
// invisible to it. It is a courtesy check for a cooperative agent, not the
// boundary. The boundary is the kernel sandbox where the platform has one;
// where it does not, the statement says so rather than implying a protection
// that is not there. The post-command symlink sweep (sweepEscapingSymlinks)
// is named because it is the one check that does look at what the command
// actually did rather than what it said — and it reports, never removes.
func guardNatureStatement() string {
	if sandbox.TurnPolicyBaseInstalled() {
		return "Note: this guard scans the command text and is advisory — a path assembled at runtime is not seen by it. " +
			"The enforced boundary is the kernel sandbox, which confines the command by the real path it touches; " +
			"symlinks inside the workspace that point outside it are reported after the command runs (never removed) and recorded for the operator."
	}
	return "Note: this guard scans the command text and is advisory — a path assembled at runtime is not seen by it. " +
		"On this host NO kernel sandbox is active, so this scan and the post-command symlink sweep are the only checks on where a command writes."
}

// pathUseClassifier answers one question about one command: is the
// absolute-path candidate starting at byte offset N provably a READ?
//
// It fails CLOSED at every step. "false" is returned for anything it cannot
// prove, which reproduces the pre-ADR-068 guard exactly, so the worst outcome of
// a gap in this scanner is a command that stays blocked — never a write that
// slips out of the working directory unnoticed.
//
// A candidate is read-only only when ALL of these hold:
//
//  1. The whole command is parseable by this scanner: quotes balance, and it
//     contains no command substitution (`$( )`, backticks) or process
//     substitution (`<( )`, `>( )`). Those shapes are already judged by
//     substitutionGuard and the deny patterns, but they are re-tested here so
//     this classifier stays honest on its own terms if either layer is ever
//     relaxed — an unresolvable expansion means we do not know what runs.
//  2. The command segment containing the candidate (split at unquoted
//     `| ; & newline`) has a LITERAL head that is in readOnlyShellCommands.
//     Quote-aware splitting is load-bearing: without it `tee "a;cat" /etc/x`
//     splits into `tee "a` and `cat" /etc/x`, the second segment's head reads
//     as `cat`, and a genuine write to /etc/x would be classified as a read.
//  3. The segment contains no brace character. This scanner does not model
//     brace expansion, and bash rewrites the words before parsing
//     (`{cat,/etc/passwd}` runs `cat /etc/passwd`), so any `{` or `}` means the
//     command we are judging is not the command that will run.
//  4. The candidate is not in the segment's FIRST word. A path in command
//     position is an EXEC, not a read — `echo hi;/etc/shadow` and
//     `cat[/etc/shadow]` both land here. ADR-068 rules on reads only; exec
//     stays where it was.
//  5. The candidate's word is not the target of an output redirect (`>`, `>>`,
//     `2>`, `&>`, `>&file`, `<>`). The check scans back from the start of the
//     WORD, not from the path, so a redirect onto an expansion-prefixed target
//     (`> ~/evil`, `> $HOME/evil`, whose candidate text starts mid-word) is
//     still seen as a redirect.
//  6. Every output redirect in the segment names a LITERAL target. A target
//     containing an expansion or a glob (`> $OUT`, `> out*.txt`) is a write to
//     a path this scan cannot see, so the whole segment becomes unclassifiable
//     rather than let a read be granted beside an invisible write.
type pathUseClassifier struct {
	cmd string
	// mask[i] is true when byte i is quoting syntax, escaped, or inside quotes
	// — i.e. NOT an operator the shell would act on.
	mask []bool
	// classifiable is false when rule 1 fails, which makes every candidate in
	// the command a write.
	classifiable bool
}

func newPathUseClassifier(cmd string) pathUseClassifier {
	mask, balanced := shellQuoteMask(cmd)
	return pathUseClassifier{
		cmd:  cmd,
		mask: mask,
		classifiable: balanced &&
			!strings.Contains(cmd, "$(") &&
			!strings.Contains(cmd, "`") &&
			!strings.Contains(cmd, "<(") &&
			!strings.Contains(cmd, ">("),
	}
}

// --- read/write classification (ADR-068 §1, option A) ------------------------

// readOnlyShellCommands is the allowlist of command names whose path arguments
// are provably only READ.
//
// MEMBERSHIP CRITERION, and it is deliberately severe: a name belongs here only
// if the binary has NO flag, in ANY common implementation, that writes to a
// filesystem path named on its own command line. If a reviewer has to think
// about it, the answer is no.
//
// Why an allowlist and not a blocklist of write markers. Deciding read-vs-write
// from shell TEXT is, in general, undecidable — the head can be an interpreter
// whose file access is invisible (`python3 -c`, `sh stage2.sh`), the same binary
// reads or writes depending on a flag (`sed` vs `sed -i`, `sort` vs `sort -o`,
// `xxd` vs `xxd -r`), and expansions this scanner does not model change which
// token is even a path. ADR-068 §5 criticises exactly that kind of pattern
// matching, so this classifier must never grow a "looks like a read" heuristic.
// The only safe shape is: prove a read, or call it a write.
//
// DELIBERATE EXCLUSIONS, recorded so nobody re-adds them as an "obvious" omission:
//   - sed, awk, gawk, perl, ruby — in-place flags (`-i`, `-i inplace`).
//   - sort — `-o FILE`. tee, dd, cp, mv, ln, install, rsync, truncate, touch,
//     mkdir, tar, unzip — writers, or writers under a destination flag.
//   - find — `-exec` / `-delete`. less, more, vi, view — shell escapes.
//   - xxd, od-style dumpers with a reverse mode — `xxd -r in out` writes.
//   - file — `file -C -m FILE` compiles a magic file and writes FILE.mgc, so
//     it fails this list's own membership criterion (code review, 2026-08-23).
//   - every interpreter (python*, node, sh, bash, ruby, php…), plus git, make,
//     gcc, curl, wget — arbitrary file access by construction.
//   - echo, printf — they never OPEN the path at all, so there is no read to
//     grant; leaving them out also keeps `echo x,/etc/shadow` blocked.
//
// The known, accepted cost: `gcc -I/usr/include` and `tar -C/etc` are genuine
// reads that stay blocked. Inferring "this flag names a read-only directory" is
// the fragile inference this design refuses to make.
var readOnlyShellCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "nl": true, "wc": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"ls": true, "stat": true, "du": true,
	"diff": true, "cmp": true, "strings": true,
	"readlink": true, "realpath": true, "basename": true, "dirname": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"shasum": true, "cksum": true,
}

// classify reports whether the absolute-path candidate beginning at byte
// offset start is provably a read, with its reasoning attached. See the
// type's doc comment for the rules; each early return below names the rule
// it applies.
func (c pathUseClassifier) classify(start int) pathUseVerdict {
	if !c.classifiable {
		return pathUseVerdict{reason: "the command contains quoting or a command/process substitution this guard cannot parse, so it cannot tell which file each reference opens"}
	}
	if start < 0 || start >= len(c.cmd) {
		return pathUseVerdict{reason: "the reference could not be located in the command text"}
	}

	segStart, segEnd := c.segmentBounds(start)
	seg := c.cmd[segStart:segEnd]

	// Rule 3: unmodelled brace expansion.
	if strings.ContainsAny(seg, "{}") {
		return pathUseVerdict{reason: "the command uses brace expansion ({ }), which rewrites its words before they run"}
	}

	// Rule 2: literal, allowlisted head — named EXACTLY, with no directory
	// prefix and no case folding.
	//
	// shellCommandHead normalises for the DENY path: it lowercases and strips a
	// directory prefix so `/bin/rm` is judged as `rm`. That is right for a deny
	// list and a bypass for an allow list — `./cat`, `bin/cat` and `CAT` all
	// normalise onto the allowlisted head `cat`, and an agent may freely write
	// an executable named `cat` into its own working directory. It would then
	// be classified as a proven read while being an arbitrary program with the
	// account's full write access. shellCommandHeadDetailed reports whether
	// normalisation changed the token; a changed token is refused here, which
	// costs only the absolute spellings (`/bin/cat f`) and keeps the doctrine
	// intact: prove a read, or call it a write.
	head, headIsExpansion, headNormalised := shellCommandHeadDetailed(seg)
	word := c.wordStart(start, segStart)

	// Rule 4: command position is exec, not read. Checked before the head
	// allowlist so an absolute path in command position is reported as an
	// EXEC rather than as "head not on the allowlist" (D-66).
	if word == c.firstWordStart(segStart, segEnd) {
		return pathUseVerdict{exec: true, reason: "the path is in command position — the shell would RUN it, which is not a read"}
	}
	switch {
	case headIsExpansion:
		return pathUseVerdict{reason: "the command word is a shell expansion, so the guard cannot tell which program runs"}
	case headNormalised:
		return pathUseVerdict{reason: fmt.Sprintf("the command word is spelled with a directory prefix or in upper case (%q), which the read-only allowlist matches only literally", head)}
	case !readOnlyShellCommands[head]:
		return pathUseVerdict{reason: fmt.Sprintf("%q is not on the guard's read-only allowlist (%s) — it has a flag or mode that can write to a path named on its command line, so the guard cannot prove this use is a read", head, readOnlyShellCommandsSummary())}
	}
	// Rule 5: this word is a redirect target.
	if c.precededByOutputRedirect(word, segStart) {
		return pathUseVerdict{reason: "the path is the target of an output redirect"}
	}
	// Rule 6: some other redirect in this segment writes somewhere we cannot see.
	if !c.redirectTargetsAreLiteral(segStart, segEnd) {
		return pathUseVerdict{reason: "the same command segment redirects output to a target the guard cannot see (an expansion or a glob)"}
	}
	return pathUseVerdict{readOnly: true}
}

// readOnlyShellCommandsSummary renders the allowlist for a refusal message,
// sorted so the text is stable across runs.
func readOnlyShellCommandsSummary() string {
	names := make([]string, 0, len(readOnlyShellCommands))
	for n := range readOnlyShellCommands {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// segmentBounds returns the half-open byte range of the command segment
// containing idx, splitting at unquoted `| ; & newline` exactly as
// splitShellSegments does — but preserving offsets, which that helper discards.
func (c pathUseClassifier) segmentBounds(idx int) (int, int) {
	start := idx
	for start > 0 && !c.isSegmentSeparator(start-1) {
		start--
	}
	end := idx
	for end < len(c.cmd) && !c.isSegmentSeparator(end) {
		end++
	}
	return start, end
}

func (c pathUseClassifier) isSegmentSeparator(i int) bool {
	if c.mask[i] {
		return false
	}
	switch c.cmd[i] {
	case '|', ';', '\n', '\r':
		return true
	case '&':
		// `2>&1`, `<&-`: an `&` bound to a redirection operator on its left is
		// a file-descriptor duplication, not a command separator. Splitting
		// there would strand the redirect with an empty target and make the
		// ubiquitous `cmd 2>&1` shape unclassifiable for no reason.
		if i > 0 && !c.mask[i-1] && (c.cmd[i-1] == '>' || c.cmd[i-1] == '<') {
			return false
		}
		return true
	}
	return false
}

// wordStart returns the offset at which the shell word containing idx begins,
// never running past segStart. Quoted word-break characters do not break a
// word, which is what keeps `cat "/etc/my file"` one argument.
func (c pathUseClassifier) wordStart(idx, segStart int) int {
	i := idx
	for i > segStart && (c.mask[i-1] || !isShellWordBreak(c.cmd[i-1])) {
		i--
	}
	return i
}

// firstWordStart returns the offset of the segment's first word — the command
// position.
func (c pathUseClassifier) firstWordStart(segStart, segEnd int) int {
	i := segStart
	for i < segEnd && !c.mask[i] && (c.cmd[i] == ' ' || c.cmd[i] == '\t') {
		i++
	}
	return i
}

// precededByOutputRedirect reports whether the word beginning at word is the
// target of an output redirect. It covers `>`, `>>`, `N>`, `<>` (open for
// read-WRITE) and bash's `>&target` combined form; `<` alone is a read and is
// deliberately not matched.
func (c pathUseClassifier) precededByOutputRedirect(word, segStart int) bool {
	i := word - 1
	for i >= segStart && !c.mask[i] && (c.cmd[i] == ' ' || c.cmd[i] == '\t') {
		i--
	}
	if i < segStart || c.mask[i] {
		return false
	}
	if c.cmd[i] == '>' {
		return true
	}
	if c.cmd[i] == '&' && i-1 >= segStart && !c.mask[i-1] && c.cmd[i-1] == '>' {
		return true
	}
	return false
}

// redirectTargetsAreLiteral reports whether every output redirect in the
// segment names a target this text scan can actually see. A target built from
// an expansion or a glob (`> $OUT`, `> "$d"/x`, `> out*.txt`) is a write to an
// unknown path: the write itself is no more and no less guarded than it was
// before ADR-068 (this scan never resolved `$OUT`), but we refuse to hand out a
// READ exemption in the same segment as a write we cannot locate.
func (c pathUseClassifier) redirectTargetsAreLiteral(segStart, segEnd int) bool {
	for i := segStart; i < segEnd; i++ {
		if c.mask[i] || c.cmd[i] != '>' {
			continue
		}
		j := i + 1
		for j < segEnd && !c.mask[j] && c.cmd[j] == '>' { // `>>`
			j++
		}
		for j < segEnd && !c.mask[j] && (c.cmd[j] == ' ' || c.cmd[j] == '\t') {
			j++
		}
		tokStart := j
		// A leading `&` belongs to the redirection (`>&1`, `>&-`, `>&file`)
		// even though `&` is otherwise a word break, so step over it before the
		// word scan starts.
		if j < segEnd && !c.mask[j] && c.cmd[j] == '&' {
			j++
		}
		for j < segEnd && (c.mask[j] || !isShellWordBreak(c.cmd[j])) {
			j++
		}
		tok := c.cmd[tokStart:j]
		if tok == "" {
			// A redirect with no visible target (truncated command, or a
			// separator we split on). Unclassifiable.
			return false
		}
		if strings.HasPrefix(tok, "&") {
			// `>&1`, `>&-`: file-descriptor duplication, no file involved.
			// `>&file` (bash's combined redirect) IS a file, so only a pure
			// fd/close suffix short-circuits.
			if rest := tok[1:]; rest == "-" || isAllDigits(rest) {
				continue
			}
			tok = tok[1:]
		}
		if strings.ContainsAny(tok, "$`*?[") {
			return false
		}
		i = j - 1
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// shellQuoteMask marks every byte of s that the shell would NOT treat as a bare
// operator: the quote characters themselves, everything inside them, a
// backslash and the byte it escapes. The second return value is false when the
// string ends inside a quote — an unbalanced command is one this scanner
// refuses to reason about at all.
//
// This exists because segment splitting and redirect detection must agree with
// bash about which `; | & >` characters are syntax and which are data. Getting
// that wrong in the permissive direction is a real bypass: `tee "a;cat" /etc/x`
// would otherwise present a segment whose head looks like `cat`.
func shellQuoteMask(s string) ([]bool, bool) {
	mask := make([]bool, len(s))
	var quote byte // 0 = unquoted, '\'' or '"' = inside that quote
	for i := 0; i < len(s); i++ {
		ch := s[i]
		// Backslash escapes everywhere except inside single quotes, where it is
		// a literal character.
		if ch == '\\' && quote != '\'' {
			mask[i] = true
			if i+1 < len(s) {
				mask[i+1] = true
				i++
			}
			continue
		}
		switch {
		case quote == 0 && (ch == '\'' || ch == '"'):
			quote = ch
			mask[i] = true
		case quote != 0 && ch == quote:
			quote = 0
			mask[i] = true
		default:
			mask[i] = quote != 0
		}
	}
	return mask, quote == 0
}

// inTurnSecretSet reports whether p is in this turn's secret set — another
// agent's home, another workspace's tree, or one of the always-secret entries.
//
// DENY-side, so it errs toward matching. IsCarveOut wants a RESOLVED path
// (its own parameter is named resolvedAbsPath) and resolves by filesystem
// identity, so both spellings of the same file are asked about: the lexical
// form the command actually wrote, and its symlink-resolved form. Checking only
// one is what made the first version of this fix inert on macOS, where
// /var/folders/... and /private/var/folders/... are the same directory and the
// guard compared them as strings.
//
// A resolve failure leaves the lexical verdict standing rather than failing
// open, and never aborts the command.
func (t *ExecTool) inTurnSecretSet(p string, policy fspolicy.FSPolicy) bool {
	if fspolicy.IsCarveOut(p, policy) {
		return true
	}
	// The child-only half of the secret set, which IsCarveOut deliberately no
	// longer covers. ADR-072 D10.3 removed $OMNIPUS_HOME/skills from the app
	// layer's carve-out roots so the in-process file tools could gate a
	// skill's INSTRUCTION FILE rather than its whole directory — a distinction
	// this guard must not inherit. `bash` is a spawned CHILD: on POSIX the
	// kernel ruleset still denies it the whole skills directory, so following
	// the narrowing here would only produce a guard that passes a command the
	// kernel then refuses; on Windows there is no ruleset at all
	// (selectBackendPlatform returns FallbackBackend), so this guard is the
	// only thing there is. Both reasons point the same way: keep the
	// directory-shaped deny for children.
	home := config.OmnipusHomeDir()
	if fspolicy.CoversChildOnlySecretPath(p, home) {
		return true
	}
	if resolved, err := resolvePathAgainstExistingAncestor(p); err == nil && resolved != p {
		if fspolicy.IsCarveOut(resolved, policy) || fspolicy.CoversChildOnlySecretPath(resolved, home) {
			return true
		}
	}
	return false
}

// expandCandidatePrefix inspects the boundary text absolutePathPattern consumed
// immediately before a path candidate and, when that boundary is a `~` or a
// variable this process can resolve, returns the prefix the shell will prepend.
//
// # Why this exists
//
// absolutePathPattern treats `~` and a token-start `$VAR` as boundary
// characters and captures only the suffix, so `$HOME/.omnipus/agents/x/SOUL.md`
// arrives as the candidate `/.omnipus/agents/x/SOUL.md` — a path naming no real
// file. Judging that phantom is meaningless, and it was measurably exploitable
// (code review round 3): the ADR-068 read exemption was reachable through the
// `~`/`$HOME` spelling of a file whose literal absolute spelling was correctly
// refused, because IsCarveOut cannot match a carve-out root against a path that
// never contained $OMNIPUS_HOME.
//
// # Why resolve rather than refuse
//
// Refusing every expansion-derived candidate outright also blocks `cat
// ~/notes.txt`, an ordinary read ADR-068 exists to permit — and an existing
// test asserts that it must stay permitted. Resolving the prefix keeps that
// read working while giving the secret-set and mount checks the real path.
//
// # Fail-closed remainder
//
// Only `~`, `$HOME`/`${HOME}` and `$OMNIPUS_HOME`/`${OMNIPUS_HOME}` are
// resolved, because only those have a value this process can be sure of. Any
// other `$VAR` returns resolved=false, and the caller refuses the candidate
// rather than guessing. os.UserHomeDir failing likewise yields resolved=false.
//
// Returns (prefix, resolved, isExpansion). isExpansion=false means the boundary
// was ordinary punctuation and the candidate is already a literal path.
func expandCandidatePrefix(boundary string) (string, bool, bool) {
	if boundary == "" {
		return "", false, false
	}
	// The boundary may carry leading punctuation (`="`, `,`, a flag cluster);
	// only its TAIL decides, because that is what abuts the captured path.
	switch {
	case strings.HasSuffix(boundary, "~"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false, true
		}
		return home, true, true
	case strings.HasSuffix(boundary, "$HOME"), strings.HasSuffix(boundary, "${HOME}"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false, true
		}
		return home, true, true
	case strings.HasSuffix(boundary, "$OMNIPUS_HOME"), strings.HasSuffix(boundary, "${OMNIPUS_HOME}"):
		if h := config.OmnipusHomeDir(); h != "" {
			return h, true, true
		}
		return "", false, true
	case strings.ContainsRune(boundary, '$'):
		// Some other variable — value unknown to this process.
		return "", false, true
	}
	return "", false, false
}

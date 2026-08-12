// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// This file holds the containment primitive every secret-set decision is built
// on. It exists because the obvious primitive — comparing two path strings —
// is wrong on the filesystem Omnipus most often runs on.
//
// # The defect this closes
//
// APFS is case-INSENSITIVE by default, and filepath.EvalSymlinks does NOT
// canonicalise case. So $OMNIPUS_HOME/CLI.TOKEN and $OMNIPUS_HOME/cli.token are
// ONE file to the kernel and two different strings to a byte-comparing guard.
// Every carve-out was byte-comparing. Measured on a real APFS volume, through
// the real EffectiveFSPolicy + ResolvePath + PathHandle.ReadFile with the
// default restrict=true, the lowercase spelling was denied while the uppercase
// spelling returned the live gateway bearer token, the master key, and the
// decrypted-credential store; with a workspace mount in play (ADR-061 D4) the
// same trick WROTE config.json and destroyed master.key.
//
// The kernel is not a backstop here. Seatbelt does match case-insensitively,
// but it confines CHILD processes only — read_file/write_file/edit/send_file
// run inside the gateway process, which is unconfined on macOS. IsCarveOut is
// the sole gate.
//
// # Why identity, not case folding
//
// Case folding was the first fix considered and it is not sufficient. Measured
// on this machine's APFS volume (probeVolume in carveout_case_identity_test.go
// measures what the volume actually does rather than assuming):
//
//	case-insensitive              LOWER.TXT opens lower.txt
//	normalization-insensitive     an NFD name opens an NFC file
//	sharp-s folds to SS           STRASSE.txt opens straße.txt
//
// strings.ToLower matches the first of those and MISSES the other two —
// ToLower("STRASSE") is "strasse", never "straße", and ToLower leaves NFD and
// NFC forms distinct. A fold would therefore have closed the ASCII payload that
// was demonstrated and left a narrower version of the identical hole open: any
// install whose $OMNIPUS_HOME path contains a non-ASCII component (a macOS
// account named "José" gives ~/.omnipus under an NFC or NFD spelling, and the
// attacker picks the other one) stays exploitable. That is not theoretical —
// the NFD/NFC bypass was reproduced end-to-end before this fix.
//
// So the primitive is filesystem IDENTITY: os.SameFile, which compares device
// and inode. It is immune to case, to Unicode normalization, to locale-specific
// case rules, and to symlink spelling, because it asks the kernel which file a
// name denotes instead of guessing from the bytes. It also closes a bypass no
// string comparison of any kind could: a HARD LINK to credentials.json planted
// inside the agent's own working directory has a completely unrelated path and
// the same inode, so it is now denied.
//
// # The residual, and why the fallback direction is asymmetric
//
// Identity cannot answer for a path that does not exist yet, and the secret set
// deliberately includes not-yet-created files (SecretPaths returns the exact
// names whether or not they exist — a credential file created a moment after
// the check must already be covered). When identity cannot decide, the answer
// falls back to a string comparison, and WHICH string comparison depends on
// which way the caller's decision fails safe:
//
//	CoversForDeny   fallback = CASE-FOLDED. A false match denies something,
//	                which is visible, bounded and recoverable. A false miss
//	                leaks the master key. Folding is the safe direction, so
//	                deny-side comparisons fold.
//	CoversForGrant  fallback = STRICT (byte-exact). These are the own-tree
//	                exception legs: a true answer RE-ADMITS a path that a
//	                carve-out just matched. Folding here would widen the
//	                exception — on a genuinely case-sensitive volume,
//	                agents/MIA and agents/mia are two different agents' homes,
//	                and a folded exception would hand one the other's tree.
//	                Fewer exemptions is the safe direction, so grant-side
//	                comparisons stay byte-exact.
//
// This asymmetry is the whole reason a single blanket "lowercase everything"
// helper would have been wrong even ignoring Unicode: the two directions do not
// fail safe the same way. Identity resolves both correctly whenever it can
// answer; the asymmetric fallback keeps both safe when it cannot.
//
// Residual, stated plainly and covered by TestCarveOut_ResidualIsDocumented:
// when BOTH the secret and the candidate are absent from disk AND the payload
// is a non-ASCII case/normalization variant, the fold fallback misses it. That
// window requires the secret itself not to exist, at which point there is
// nothing to leak or overwrite in place; the file becomes covered by identity
// the instant it is created.

// pathRelation is the outcome of an identity-based containment question.
// relUnknown means the filesystem could not answer — the caller must fall back
// to a string comparison whose direction fails safe for that caller.
type pathRelation int

const (
	relUnknown pathRelation = iota
	relWithin
	relOutside
)

// ancestorChain stats candidate and every one of its ancestors up to the
// filesystem root, returning the FileInfo of each component that exists.
//
// The chain is what makes identity work for paths that do not exist yet, which
// is the common write case: for $OMNIPUS_HOME/ENTITIES/agents/new.json, the leaf
// and possibly its parent do not exist, but $OMNIPUS_HOME/ENTITIES does — and it
// stats to the SAME inode as $OMNIPUS_HOME/entities, so the candidate is
// correctly judged to be inside the entities carve-out.
//
// os.Stat (following symlinks), not os.Lstat: the access this guard is about to
// permit or refuse will itself follow symlinks, so the identity that matters is
// the one the eventual open() reaches.
//
// Returns ok=false on any hard error (a permission failure, an invalid path),
// never a partial chain — a partial chain could be missing exactly the ancestor
// that matches a carve-out root, which would read as "outside" and fail OPEN.
// A missing component is not a hard error: the walk simply continues upward.
func ancestorChain(candidate string) ([]os.FileInfo, bool) {
	p := filepath.Clean(candidate)
	if p == "" || !filepath.IsAbs(p) {
		return nil, false
	}
	out := make([]os.FileInfo, 0, 8)
	for {
		info, err := os.Stat(p)
		switch {
		case err == nil:
			out = append(out, info)
		case isMissingComponent(err):
			// This component does not exist; keep walking up.
		default:
			return nil, false
		}
		parent := filepath.Dir(p)
		if parent == p {
			return out, true
		}
		p = parent
	}
}

// isMissingComponent reports whether err means "this path component is simply
// not there", as opposed to a hard failure that must abandon identity.
//
// ENOTDIR is included alongside ErrNotExist because a path whose interior
// component is a regular file (".../somefile/x") reports ENOTDIR rather than
// ENOENT; treating it as a hard error would discard identity for the whole
// chain and drop the decision onto the string fallback unnecessarily.
func isMissingComponent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// identityRelation answers "is candidate (represented by its pre-computed
// ancestor chain) equal to, or contained by, container?" using device+inode
// identity alone.
//
// relOutside is only returned when the answer is genuinely knowable: container
// exists, the chain is complete, and no component of the chain is the same file
// as container. That is sound because containment implies ancestry — if
// candidate were inside container, container would BE one of the chain entries
// (the chain walks every ancestor by name, and a name that denotes container on
// a case-insensitive volume stats to container's own inode).
func identityRelation(chain []os.FileInfo, chainOK bool, container string) pathRelation {
	if !chainOK {
		return relUnknown
	}
	containerInfo, err := os.Stat(container)
	if err != nil {
		return relUnknown
	}
	for _, info := range chain {
		if os.SameFile(info, containerInfo) {
			return relWithin
		}
	}
	return relOutside
}

// CoversForDeny reports whether candidate is container itself or lives under
// it, for a caller whose TRUE answer DENIES access.
//
// Identity first; when the filesystem cannot answer, a case-folded string
// comparison — the fail-safe direction for a deny (see this file's header).
//
// Exported so the other packages that re-implement containment against the same
// secret set can adopt one rule instead of three:
// pkg/tools/filesystem.go's isWithinWorkspace and pkg/workspace/mount.go's
// isWithinOrEqualPath both still compare bytes.
func CoversForDeny(container, candidate string) bool {
	chain, ok := ancestorChain(candidate)
	return coversForDenyChain(container, candidate, chain, ok)
}

// coversForDenyChain is CoversForDeny with the candidate's ancestor chain
// supplied by the caller, so a loop over many containers (IsCarveOut's walk
// over every carve-out root) stats the candidate once rather than once per
// root.
func coversForDenyChain(container, candidate string, chain []os.FileInfo, chainOK bool) bool {
	switch identityRelation(chain, chainOK, container) {
	case relWithin:
		return true
	case relOutside:
		return false
	}
	return PathCoversFold(container, candidate)
}

// CoversForGrant reports whether candidate is container itself or lives under
// it, for a caller whose TRUE answer GRANTS access (or re-admits a path a
// carve-out already matched).
//
// Identity first; when the filesystem cannot answer, a STRICT byte-exact
// comparison — the fail-safe direction for a grant. Never folds: folding a
// grant would merge two genuinely distinct directories on a case-sensitive
// volume and hand one agent another's tree.
func CoversForGrant(container, candidate string) bool {
	chain, ok := ancestorChain(candidate)
	return coversForGrantChain(container, candidate, chain, ok)
}

// coversForGrantChain is CoversForGrant with a caller-supplied ancestor chain.
func coversForGrantChain(container, candidate string, chain []os.FileInfo, chainOK bool) bool {
	switch identityRelation(chain, chainOK, container) {
	case relWithin:
		return true
	case relOutside:
		return false
	}
	return isWithinOrEqual(candidate, container)
}

// SameLocationForDeny reports whether two paths denote the SAME file or
// directory, for a caller whose TRUE answer denies (or withholds an
// exemption).
//
// Identity first; case-folded equality as the fail-safe fallback. Used for
// IsCarveOut's "WorkDir is not the carve-out root itself" guard, where
// answering "they are the same" is what withholds the own-tree exemption.
func SameLocationForDeny(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(aInfo, bInfo)
	}
	return foldPath(a) == foldPath(b)
}

// PathCoversFold reports whether container equals candidate or is one of its
// ancestors, comparing WITHOUT regard to letter case.
//
// This is the same rule as pkg/sandbox's own pathCoversFold (exec_paths.go),
// which the codebase already applies to the lower-value exec-path overlap
// check; the secret set — the higher-value target — never got it. Deliberately
// unconditional rather than gated on a per-volume case-sensitivity probe: case
// sensitivity is a property of the MOUNT, not the OS (macOS can be formatted
// case-sensitive; Linux ext4 supports per-directory casefold and vfat/exfat/SMB
// mounts are case-insensitive; NTFS can opt directories into case sensitivity),
// so any per-platform guess is wrong somewhere — and the wrong guess in the
// "assume case-sensitive" direction is precisely the bug being fixed. This is
// only ever reached as a DENY-side fallback, where over-matching is the safe
// direction.
//
// Both sides are folded in full before the prefix test rather than slicing one
// by the other's byte length: case folding can change a string's byte length,
// and slicing on the unfolded length could split a rune and miss.
func PathCoversFold(container, candidate string) bool {
	sep := string(filepath.Separator)
	p := foldPath(container)
	c := foldPath(candidate)
	if p == "" {
		// Clean+Trim collapses the filesystem root to "": it covers everything.
		return true
	}
	return c == p || strings.HasPrefix(c, p+sep)
}

// foldPath normalises a path for case-insensitive comparison: cleaned, with any
// trailing separator removed, lowercased.
//
// strings.ToLower and NOT a Unicode normalization pass, deliberately. Adding
// golang.org/x/text/unicode/norm would buy the NFC/NFD half of the residual and
// still miss locale case rules, while making a stdlib-only leaf package (see
// this package's doc comment — it must stay importable from both pkg/tools and
// pkg/sandbox without a cycle) depend on x/text. Identity already covers every
// one of those cases whenever the path exists, which is the case that matters;
// this fallback exists only for paths that do not.
func foldPath(p string) string {
	sep := string(filepath.Separator)
	return strings.ToLower(strings.TrimSuffix(filepath.Clean(p), sep))
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package pathsafe is the single, shared filename-safety validator for
// every part of Omnipus that names a file or directory on disk. It exists
// because an audit (2026-07) found FOUR independent filename sanitizers —
// pkg/library/root.go's CleanRelPath, pkg/agent/upload_workpath.go's
// SanitizeUploadFilename, pkg/utils/media.go's SanitizeFilename, and
// pkg/notifications/store.go's sanitize — each with a different rule set,
// and none of them safe on every platform Omnipus ships on (Linux, macOS,
// Windows). This package is the ONE implementation all four now share, so
// a name Omnipus rejects (or rewrites) is rejected (or rewritten) the same
// way everywhere, regardless of which of those four call sites is asking.
//
// # Two kinds of rule, and only one of them is conditional (ADR-067 Stage 0)
//
// Until 2026-08 every rule below applied unconditionally on every OS. That
// was right for some of them and a category error for the rest, so the
// package now splits its rules BY PURPOSE. rules.go holds the mechanism
// and the full argument; the short version is:
//
//   - ADDRESSING SAFETY — an empty name, a bare "." or "..", and C0
//     control characters (0x00-0x1F, NUL/CR/LF among them). These are
//     properties of the STRING, not of a filesystem: a NUL byte truncates
//     a path in any syscall that takes a C string, and a "." or ".."
//     component addresses a directory rather than a name inside it, on
//     every operating system there is. They are enforced in every build,
//     for reads and writes alike, and no RuleSet can switch them off —
//     which is why they live outside RuleSet entirely, in
//     ValidateAddressingSafety. See FR-0002, FR-0002a, FR-0002b.
//
//   - NAME SHAPE — the NTFS-illegal character set (< > : " | ? *), Windows
//     reserved device names (CON, NUL, COM1 …), a trailing dot or space
//     Win32 silently strips, and the length budget. These describe one
//     operating system's filesystem and exist to stop Omnipus CREATING a
//     name that will not survive there. They are carried in a RuleSet
//     value selected by GOOS, and they apply only where they mean
//     something.
//
// # Why the old "unconditional, everywhere" rule was wrong
//
// The original argument was portability: a workspace created on Linux may
// later be opened on Windows, so a name Linux tolerates should still be
// refused. That argument holds for names Omnipus creates in its own
// workspace storage — and it is exactly why the Windows rule set still
// exists and still applies there on Windows builds.
//
// It does not hold, at all, for a MOUNTED folder. A mount stores an
// absolute host path that is meaningful only on the machine it was created
// on; copying $OMNIPUS_HOME to another OS breaks the mount regardless of
// what the files are called. Those documents were named by the operator,
// often years before Omnipus existed, and Omnipus is a reader of them.
// Refusing to open "Meeting: notes.md" on macOS protects a Windows
// deployment that can never see the file anyway; all it achieves is making
// the operator's own documents invisible inside a feature whose entire
// purpose is reading their existing documents. Measured on the reference
// vault, 3 of 748 notes were unreachable for this reason and Omnipus had
// named none of them.
//
// What must NOT vary by platform is containment — traversal, root
// confinement, symlink escape. Those are unrelated to whether a name is
// Windows-legal, and they stay unconditional.
//
// # Reject vs. sanitize
//
// Most call sites can reject: an HTTP caller can be told "400 invalid
// filename" and try again — use ValidateComponent for these. One call
// site genuinely cannot reject: an inbound chat-channel attachment
// (Discord, Telegram, Feishu, QQ) already exists on a remote server with
// whatever name its SENDER gave it, and Omnipus must still store the bytes
// somehow. For that case, SanitizeComponent deterministically REWRITES the
// name into something safe and reports whether it did so, so the caller
// can log/surface the change — it never silently swaps the name without
// telling anyone.
//
// SanitizeComponent takes no RuleSet from the build and never reads
// ActiveRules: that path handles attacker-chosen input, so it composes the
// unconditional control-character pass with SanitizeRules — the strict set
// — and its output is byte-identical on every platform and in every build
// (FR-0001d). Stage 0's relaxation is scoped to the validating read path
// and nothing else. Fusing the two — a build-dependent character set
// shared by the validator and the sanitizer — is the tempting
// implementation and the wrong one: it would relax the remote-attachment
// path as a side effect of relaxing the operator's read path, which is the
// opposite of the intent.
//
// (SanitizeComponent also never does iterative substring surgery, which is
// the bug class this package replaces: the previous pkg/utils/media.go
// SanitizeFilename stripped ".." by repeated substring removal, which can
// reconstitute a dangerous sequence — e.g. the four dots in "....//"
// reduce, after removing two non-overlapping ".." matches, to a bare "//"
// that a later, separate replacement pass then has to catch. It extracts
// the last path element once, replaces disallowed characters in a single
// pass, and never re-scans its own output for patterns it just removed.)
package pathsafe

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

// Sentinel errors. Callers use errors.Is against these — mirroring the
// convention pkg/library's own sentinel errors already establish — rather
// than string-matching Error().
var (
	// ErrEmptyName marks a name that is empty, or whose only content was a
	// bare "." or "..". Both reduce to nothing addressable: "." names the
	// directory itself and ".." names its parent, so neither is a name a
	// caller can create, open or list inside it.
	//
	// The dot cases are refused INDEPENDENTLY of any RuleSet, by
	// ValidateAddressingSafety (FR-0002b). Before Stage 0 the promise in
	// this comment was not kept: ValidateComponent("..") failed ONLY as a
	// side effect of the Windows trailing-dot rule, so turning that rule
	// off — which the POSIX set does — made both "." and ".." validate
	// clean. TestPathsafe_DotAndDotDotRejectedWithoutTrailingDotRule
	// guards exactly that.
	ErrEmptyName = errors.New("pathsafe: name is empty")
	// ErrReservedName marks a Windows reserved device name (CON, PRN, AUX,
	// NUL, COM1-9, LPT1-9), matched case-insensitively and regardless of
	// extension — Windows reserves the name for the DEVICE, not the file
	// type, so "nul.txt" is exactly as unusable as bare "nul". Name-shape
	// rule: raised only by a RuleSet with ReservedDeviceNames set.
	ErrReservedName = errors.New("pathsafe: reserved device name")
	// ErrIllegalChar marks a name containing a C0 control character
	// (0x00-0x1F, which includes NUL, CR and LF) — addressing safety,
	// raised in every build under every rule set — or a character NTFS
	// refuses in ANY filename (< > : " | ? *) — a name-shape rule, raised
	// only by a RuleSet whose IllegalRunes carries it. The two used to be
	// one fused predicate; they are deliberately separate now, because
	// relaxing the NTFS set on POSIX must not quietly relax NUL/CR/LF
	// along with it (FR-0002a).
	ErrIllegalChar = errors.New("pathsafe: illegal character")
	// ErrTrailingDotOrSpace marks a name ending in '.' or ' '. The Win32
	// API silently STRIPS a trailing dot or space before a name ever
	// reaches NTFS, so "report." and "report" name the same file there —
	// two names Omnipus's own de-duplication logic would otherwise
	// consider distinct, and Windows would otherwise refuse to create at
	// all in some call paths. Name-shape rule: raised only by a RuleSet
	// with TrailingDotOrSpace set.
	ErrTrailingDotOrSpace = errors.New("pathsafe: name ends in a dot or space")
	// ErrNameTooLong marks a single path component (from ValidateComponent)
	// or a whole assembled relative path (from ValidateRelPathLength)
	// longer than the active rule set allows. Name-shape rule, and the one
	// whose UNIT varies: Windows counts runes against a MAX_PATH-derived
	// budget, POSIX counts bytes against NAME_MAX. See
	// MaxComponentNameLength and MaxComponentNameBytes.
	ErrNameTooLong = errors.New("pathsafe: name is too long")
)

// MaxComponentNameLength bounds a SINGLE path component — one filename or
// directory name, never a whole path — measured in runes (Unicode code
// points, so a single emoji or CJK character counts once, matching how a
// user perceives "how long is this name").
//
// Windows' legacy MAX_PATH is 260 UTF-16 code units for the FULL path
// without long-path support enabled, and Omnipus's own path prefix before
// a workspace-relative path even starts already consumes a meaningful
// slice of that budget in a typical install:
//
//	C:\Users\<user>\.omnipus\workspaces\<36-char-uuid>\work\
//
// which alone runs 80-115+ characters depending on where $OMNIPUS_HOME
// lives and how long the username is. 100 runes leaves comfortable room
// for several levels of nested Library directories underneath that prefix
// while still being far more generous than any realistic user-chosen
// filename needs — the longest name in this package's own test corpus,
// "My Report (final) — résumé 测试 🎉.txt", is 38 runes. It replaces the
// far larger 256-rune cap pkg/agent's SanitizeUploadFilename used to
// enforce: a 210-rune filename already passed under that cap — comfortably
// under 256, but only 50 runes from Windows' hard ceiling the moment it is
// nested even one Library folder deep.
//
// Because the budget it protects is a Windows one, this is a name-shape
// rule and lives in WindowsRules only; POSIX builds count bytes against
// MaxComponentNameBytes instead (FR-0004).
const MaxComponentNameLength = 100

// MaxRelPathLength bounds a WHOLE assembled relative path (every segment
// plus its separators), measured in runes. A per-component cap alone
// cannot catch this: many short, individually-valid segments can still sum
// to something that will not fit under Windows' MAX_PATH once combined
// with Omnipus's own path prefix (see MaxComponentNameLength's doc for
// that budget). 200 runes leaves roughly the same headroom. Like the
// component rune cap, this belongs to WindowsRules alone.
const MaxRelPathLength = 200

// reservedDeviceNames are the Windows device names that cannot be used as
// a file or directory name — with or without an extension — on any
// Windows filesystem. Matched against the part of a component BEFORE its
// first '.', case-insensitively (see RuleSet.IsReservedDeviceName).
var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// illegalRunes are the characters NTFS refuses in any path component,
// regardless of position — the character half of the Windows NAME-SHAPE
// rules, carried by WindowsRules.IllegalRunes.
//
// It deliberately does NOT include '/' or '\' — those are path
// SEPARATORS, and rejecting/stripping them is each call site's own
// existing, already-correct responsibility (pkg/library's CleanRelPath,
// pkg/agent's SanitizeUploadFilename); this package only adds the checks
// those sites were missing, and must not weaken what they already enforce
// by re-litigating it here with different rules.
//
// It also deliberately does NOT include the C0 control characters, which
// used to be folded into the same predicate. Control characters are
// addressing safety: they are rejected by ValidateAddressingSafety and
// replaced by ReplaceControlRunes, under every rule set, and no relaxation
// of THIS constant can reach them (FR-0002a).
const illegalRunes = `<>:"|?*`

// ValidateComponent checks a single path component — one filename or
// directory name, never a full path — under the rule set selected for this
// build (rules_windows.go / rules_other.go). Its signature is deliberately
// unchanged: all seventeen dependent symbols keep calling it exactly as
// before, and the conditional part of the behaviour moved into the RuleSet
// value it delegates to rather than into any call site.
//
// It enforces addressing safety unconditionally, then this build's
// name-shape rules — everything this package owns EXCEPT case-insensitive
// collision (that check needs a directory listing to compare against,
// which this package deliberately does not own; a caller with access to
// sibling names should use SameName/FindFold directly — see pkg/library's
// caseInsensitiveMatch for a worked example). Callers remain responsible
// for rejecting separators and absolute paths themselves.
//
// Callers were also historically responsible for rejecting "." and ".."
// — they may keep doing so, but they no longer have to: this function
// refuses both itself, in every build, with ErrEmptyName (FR-0002b).
//
// Returns the first violation found, wrapping one of this package's
// sentinel errors so callers can errors.Is against it.
func ValidateComponent(name string) error {
	return activeRules.ValidateComponent(name)
}

// ValidateRelPathLength checks a whole assembled, already-cleaned relative
// path against this build's total budget. Callers apply ValidateComponent
// to each individual segment separately; this catches the case where many
// individually-short segments still sum past a safe total.
//
// The total it guards descends entirely from Windows' MAX_PATH, so it is a
// name-shape rule and is inert under a rule set that does not carry one.
func ValidateRelPathLength(relPath string) error {
	return activeRules.ValidateRelPathLength(relPath)
}

// SameName reports whether a and b name the SAME slot on a
// case-insensitive filesystem (NTFS, and macOS's default APFS/HFS+) —
// Unicode simple case-folding, via strings.EqualFold. This is the ONE
// comparison every collision check in Omnipus must use instead of a plain
// "==" or an OS Stat call: relying on the host's own case sensitivity
// would make the identical Omnipus binary behave differently depending on
// which OS happens to be running it — allowing a rename or upload on a
// Linux dev machine that would silently overwrite a different file the
// moment the same workspace is opened on Windows or a default macOS
// install. Applying the check unconditionally, on every OS, is the only
// way a workspace behaves identically wherever it is opened.
//
// It is therefore NOT part of the name-shape rule set and took no part in
// Stage 0's split: this is a collision question, not a Windows-legality
// one.
func SameName(a, b string) bool {
	return strings.EqualFold(a, b)
}

// FindFold scans existing for an entry SameName considers equal to
// candidate, returning the first match's ACTUAL stored name (which may
// differ from candidate only in case) so a caller can decide what to do
// about it — allow a same-entry case-only rename, reject a genuine
// collision with a different file, etc.
func FindFold(existing []string, candidate string) (match string, found bool) {
	for _, name := range existing {
		if SameName(name, candidate) {
			return name, true
		}
	}
	return "", false
}

// SanitizeComponent deterministically rewrites raw into a name safe under
// the STRICTEST rule set this package knows — SanitizeRules, every rule,
// Windows shapes included — for the one class of caller that cannot
// reject: media arriving from a remote channel already exists on someone
// else's server with whatever name its sender gave it, and Omnipus must
// still store it somehow. It NEVER returns an error; instead it reports
// whether it changed anything, so the caller can log/surface the fact
// rather than silently swapping the name.
//
// It never consults ActiveRules, by design (FR-0001d). Its input is
// attacker-chosen — the filename on a Discord, Telegram, Feishu or QQ
// attachment — and its output is byte-identical on every platform and in
// every build. Stage 0's relaxation applies to the validating read path
// alone; wiring this function to the active rule set would relax the
// remote-ingest path as a side effect, which is precisely the mistake
// TestSanitizeComponent_UnchangedOnEveryBuild exists to catch.
//
// Unlike the sanitizer this replaces, this never does iterative substring
// removal (see this package's doc for why that is unsafe) — every step
// below is a single, non-recursive pass.
func SanitizeComponent(raw string) (name string, changed bool) {
	name = lastPathElement(raw)
	name = replaceIllegalRunes(name)
	name = trimTrailingDotsAndSpaces(name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	name = defuseReservedName(name)
	name = truncateComponent(name, MaxComponentNameLength)
	// Truncation can re-expose a trailing dot/space that was previously
	// mid-string (e.g. truncating "report. txt" right after the dot); trim
	// once more rather than leaving a name Windows would silently mutate.
	name = trimTrailingDotsAndSpaces(name)
	if name == "" {
		name = "file"
	}
	return name, name != raw
}

// lastPathElement returns the final path segment of raw, treating BOTH
// '/' and '\' as separators regardless of the host's runtime.GOOS. A
// filename arriving from a remote chat channel is not a local OS path, so
// path/filepath's OS-specific separator handling (only '/' on Linux/macOS,
// both '/' and '\' on Windows) is the wrong tool: the exact same crafted
// name must be neutralized identically no matter which OS the Omnipus
// binary happens to be running on. Falls back to "file" for an input that
// is empty, all separators, or resolves to "." or "..".
func lastPathElement(raw string) string {
	unified := strings.ReplaceAll(raw, `\`, "/")
	parts := strings.Split(unified, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if p := parts[i]; p != "" && p != "." && p != ".." {
			return p
		}
	}
	return "file"
}

// replaceIllegalRunes is the sanitizing counterpart of the split
// FR-0002a requires: the unconditional control-character pass composed
// with SanitizeRules' name-shape character pass. Together they reproduce
// the pre-Stage-0 fused pass exactly — both halves only ever MAP a rune to
// '_', never delete, and '_' is in neither rejected set, so the
// composition is order-independent and cannot re-expose anything a single
// pass would have caught.
//
// The two halves are named separately, and this one reads SanitizeRules
// rather than ActiveRules, precisely so that relaxing the character set
// for a POSIX build cannot reach either the control-character half or the
// remote-attachment path. Swapping SanitizeRules for ActiveRules here is
// the single most likely way to break FR-0001d, and is exactly what
// TestSanitizeComponent_UnchangedOnEveryBuild detects.
func replaceIllegalRunes(name string) string {
	return SanitizeRules.ReplaceIllegalRunes(ReplaceControlRunes(name))
}

// trimTrailingDotsAndSpaces removes every trailing '.' or ' ' — the two
// characters Win32 silently strips from the end of a name before it
// reaches NTFS. Unconditional here, like every other step of the
// sanitizing path: see SanitizeComponent's doc.
func trimTrailingDotsAndSpaces(name string) string {
	return strings.TrimRight(name, ". ")
}

// defuseReservedName appends an underscore to name's stem (the part
// before its first '.') if that stem is a Windows reserved device name,
// preserving whatever extension follows: "con" -> "con_",
// "CON.tar.gz" -> "CON_.tar.gz". Asks SanitizeRules, never ActiveRules,
// for the same reason replaceIllegalRunes does.
func defuseReservedName(name string) string {
	if !SanitizeRules.IsReservedDeviceName(name) {
		return name
	}
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i] + "_" + name[i:]
	}
	return name + "_"
}

// truncateComponent shortens name to at most maxRunes runes, preserving
// its extension where the extension itself fits within the budget (so
// truncating "a-very-long-report-name.txt" shortens the descriptive part,
// not the ".txt" that makes it recognizable). Falls back to a flat,
// extension-blind truncation only in the pathological case where the
// extension alone already meets or exceeds maxRunes.
func truncateComponent(name string, maxRunes int) string {
	if utf8.RuneCountInString(name) <= maxRunes {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	extRunes := []rune(ext)
	if len(extRunes) >= maxRunes {
		nameRunes := []rune(name)
		return string(nameRunes[:maxRunes])
	}
	stemBudget := maxRunes - len(extRunes)
	stemRunes := []rune(stem)
	if len(stemRunes) > stemBudget {
		stemRunes = stemRunes[:stemBudget]
	}
	return string(stemRunes) + ext
}

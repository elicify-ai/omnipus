// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package pathsafe is the single, shared cross-platform filename-safety
// validator for every part of Omnipus that names a file or directory on
// disk. It exists because an audit (2026-07) found FOUR independent
// filename sanitizers — pkg/library/root.go's CleanRelPath,
// pkg/agent/upload_workpath.go's SanitizeUploadFilename,
// pkg/utils/media.go's SanitizeFilename, and pkg/notifications/store.go's
// sanitize — each with a different rule set, and none of them safe on
// every platform Omnipus ships on (Linux, macOS, Windows). This package is
// the ONE implementation all four now share, so a name Omnipus rejects (or
// rewrites) is rejected (or rewritten) the same way everywhere, regardless
// of which of those four call sites is asking, and regardless of which OS
// the check happens to run on.
//
// # Why platform-independent, not runtime.GOOS-gated
//
// Filenames are portable data: a workspace can be created on a Linux
// server and later browsed from a macOS or Windows client, synced to
// another machine, or restored from a backup taken on a different host.
// If this package accepted "CON.txt" on Linux because Linux itself has no
// problem with that name, the exact same workspace would become partially
// unusable the moment it reached a Windows deployment — and a workspace
// that behaves differently depending on which machine opened it is a
// portability bug even when every individual host is technically doing
// what its own filesystem allows. Every rule below therefore applies
// unconditionally, on every OS Omnipus runs on. See ValidateComponent's
// doc for the specific rules, and SameName's doc for why collision
// detection gets the same treatment.
//
// # Reject vs. sanitize
//
// Most call sites can reject: an HTTP caller can be told "400 invalid
// filename" and try again — use ValidateComponent for these. One call
// site genuinely cannot reject: an inbound chat-channel attachment
// (Discord, Feishu, …) already exists on a remote server with whatever
// name its sender gave it, and Omnipus must still store the bytes
// somehow. For that case, SanitizeComponent deterministically REWRITES the
// name into something safe and reports whether it did so, so the caller
// can log/surface the change — it never silently swaps the name without
// telling anyone. (Silently rewriting via naive substring removal is
// exactly the bug class this package replaces: the previous
// pkg/utils/media.go SanitizeFilename stripped ".." by repeated substring
// removal, which can reconstitute a dangerous sequence — e.g. the four
// dots in "....//" reduce, after removing two non-overlapping ".."
// matches, to a bare "//" that a later, separate replacement pass then has
// to catch. SanitizeComponent never does iterative substring surgery: it
// extracts the last path element once, replaces disallowed characters in
// a single pass, and never re-scans its own output for patterns it just
// removed.)
package pathsafe

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Sentinel errors. Callers use errors.Is against these — mirroring the
// convention pkg/library's own sentinel errors already establish — rather
// than string-matching Error().
var (
	// ErrEmptyName marks a name that is empty, or reduces to nothing once
	// its only content was a bare "." or "..".
	ErrEmptyName = errors.New("pathsafe: name is empty")
	// ErrReservedName marks a Windows reserved device name (CON, PRN, AUX,
	// NUL, COM1-9, LPT1-9), matched case-insensitively and regardless of
	// extension — Windows reserves the name for the DEVICE, not the file
	// type, so "nul.txt" is exactly as unusable as bare "nul".
	ErrReservedName = errors.New("pathsafe: reserved device name")
	// ErrIllegalChar marks a name containing a character NTFS refuses in
	// ANY filename (< > : " | ? *) or a C0 control character (0x00-0x1F,
	// which includes NUL).
	ErrIllegalChar = errors.New("pathsafe: illegal character")
	// ErrTrailingDotOrSpace marks a name ending in '.' or ' '. The Win32
	// API silently STRIPS a trailing dot or space before a name ever
	// reaches NTFS, so "report." and "report" name the same file there —
	// two names Omnipus's own de-duplication logic would otherwise
	// consider distinct, and Windows would otherwise refuse to create at
	// all in some call paths.
	ErrTrailingDotOrSpace = errors.New("pathsafe: name ends in a dot or space")
	// ErrNameTooLong marks a single path component (from ValidateComponent)
	// or a whole assembled relative path (from ValidateRelPathLength)
	// longer than this package allows. See MaxComponentNameLength's doc for
	// the reasoning.
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
const MaxComponentNameLength = 100

// MaxRelPathLength bounds a WHOLE assembled relative path (every segment
// plus its separators), measured in runes. A per-component cap alone
// cannot catch this: many short, individually-valid segments can still sum
// to something that will not fit under Windows' MAX_PATH once combined
// with Omnipus's own path prefix (see MaxComponentNameLength's doc for
// that budget). 200 runes leaves roughly the same headroom.
const MaxRelPathLength = 200

// reservedDeviceNames are the Windows device names that cannot be used as
// a file or directory name — with or without an extension — on any
// Windows filesystem. Matched against the part of a component BEFORE its
// first '.', case-insensitively (see isReservedDeviceName).
var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// illegalRunes are the characters NTFS refuses in any path component,
// regardless of position. This deliberately does NOT include '/' or '\' —
// those are path SEPARATORS, and rejecting/stripping them is each call
// site's own existing, already-correct responsibility (pkg/library's
// CleanRelPath, pkg/agent's SanitizeUploadFilename); this package only
// adds the checks those sites were missing, and must not weaken what they
// already enforce by re-litigating it here with different rules.
const illegalRunes = `<>:"|?*`

// ValidateComponent checks a single path component — one filename or
// directory name, never a full path — against every cross-platform-safety
// rule this package enforces EXCEPT case-insensitive collision (that check
// needs a directory listing to compare against, which this package
// deliberately does not own; a caller with access to sibling names should
// use SameName/FindFold directly — see pkg/library's caseInsensitiveMatch
// for a worked example). Callers remain responsible for rejecting
// separators, ".", "..", NUL, and absolute paths themselves — this
// function assumes those are already handled and focuses on the checks
// none of Omnipus's four sanitizers had before: reserved device names,
// NTFS-illegal characters, a trailing dot/space, and length.
//
// Returns the first violation found, wrapping one of this package's
// sentinel errors so callers can errors.Is against it.
func ValidateComponent(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if r, ok := firstIllegalRune(name); ok {
		return fmt.Errorf("%w: %q contains %q", ErrIllegalChar, name, string(r))
	}
	if hasTrailingDotOrSpace(name) {
		return fmt.Errorf("%w: %q", ErrTrailingDotOrSpace, name)
	}
	if isReservedDeviceName(name) {
		return fmt.Errorf("%w: %q", ErrReservedName, name)
	}
	if utf8.RuneCountInString(name) > MaxComponentNameLength {
		return fmt.Errorf("%w: %q exceeds %d characters", ErrNameTooLong, name, MaxComponentNameLength)
	}
	return nil
}

// ValidateRelPathLength checks a whole assembled, already-cleaned relative
// path against MaxRelPathLength. Callers apply ValidateComponent to each
// individual segment separately; this catches the case where many
// individually-short segments still sum past a safe total.
func ValidateRelPathLength(relPath string) error {
	if utf8.RuneCountInString(relPath) > MaxRelPathLength {
		return fmt.Errorf("%w: relative path %q exceeds %d characters", ErrNameTooLong, relPath, MaxRelPathLength)
	}
	return nil
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
// every rule ValidateComponent enforces, for the one class of caller that
// cannot reject: media arriving from a remote channel already exists on
// someone else's server with whatever name its sender gave it, and
// Omnipus must still store it somehow. It NEVER returns an error; instead
// it reports whether it changed anything, so the caller can log/surface
// the fact rather than silently swapping the name.
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

// replaceIllegalRunes replaces every NTFS-illegal character and C0 control
// character (0x00-0x1F, including NUL) with '_', in one single left-to-
// right pass — never a repeated/iterative substring removal, which is the
// bug class (see package doc) that let a crafted input reconstitute a
// disallowed sequence after an earlier pass "removed" it.
func replaceIllegalRunes(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r <= 0x1F || strings.ContainsRune(illegalRunes, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trimTrailingDotsAndSpaces removes every trailing '.' or ' ' — the two
// characters Win32 silently strips from the end of a name before it
// reaches NTFS.
func trimTrailingDotsAndSpaces(name string) string {
	return strings.TrimRight(name, ". ")
}

// defuseReservedName appends an underscore to name's stem (the part
// before its first '.') if that stem is a Windows reserved device name,
// preserving whatever extension follows: "con" -> "con_",
// "CON.tar.gz" -> "CON_.tar.gz".
func defuseReservedName(name string) string {
	if !isReservedDeviceName(name) {
		return name
	}
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i] + "_" + name[i:]
	}
	return name + "_"
}

// isReservedDeviceName reports whether name's stem — everything before its
// first '.', or the whole name if it has none — is a Windows reserved
// device name, matched case-insensitively. Windows reserves the DEVICE
// name regardless of what extension follows, so "nul.txt" is exactly as
// unusable as bare "nul".
func isReservedDeviceName(name string) bool {
	stem := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		stem = name[:i]
	}
	return reservedDeviceNames[strings.ToUpper(stem)]
}

// hasTrailingDotOrSpace reports whether name ends in '.' or ' '. A
// byte-level check on the last byte is safe here even for multi-byte UTF-8
// content: '.' (0x2E) and ' ' (0x20) are both ASCII values no UTF-8
// continuation byte (which is always >= 0x80) can ever equal, so this
// never misfires on a name ending in a multi-byte rune.
func hasTrailingDotOrSpace(name string) bool {
	last := name[len(name)-1]
	return last == '.' || last == ' '
}

// firstIllegalRune returns the first NTFS-illegal character or C0 control
// character (0x00-0x1F) found in name, if any.
func firstIllegalRune(name string) (rune, bool) {
	for _, r := range name {
		if r <= 0x1F {
			return r, true
		}
		if strings.ContainsRune(illegalRunes, r) {
			return r, true
		}
	}
	return 0, false
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package pathsafe

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// This file implements ADR-067 Stage 0's mechanism: filename validation
// split BY PURPOSE rather than by platform.
//
// # The two purposes, and why they must not share a switch
//
// Until Stage 0 this package applied one fused rule set to every name it
// ever saw. That rule set is Windows' — NTFS-illegal characters, reserved
// device names, no trailing dot or space, a 100-rune budget — and it was
// applied on every platform, to every name, whether Omnipus was CREATING
// the name or merely READING one that was already sitting on the
// operator's disk.
//
// The second half of that is a category error, not a trade-off. A mounted
// folder is the operator's own disk: they named those files, often years
// before Omnipus existed, and a mount stores an immutable absolute host
// path that is meaningless on any other machine — so there is no scenario
// in which a mounted file's name has to survive a trip to Windows.
// Refusing to open "Meeting: notes.md" protects an operating system the
// operator is not using, while making their own documents invisible inside
// a feature whose entire purpose is reading their existing documents.
// Measured on the reference vault: 3 of 748 notes were unreachable, none
// of them named by Omnipus.
//
// So the rules divide in two:
//
//	Addressing safety  — empty, "." / "..", C0 control runes (and, at the
//	                     caller, traversal and root confinement). These say
//	                     "this string cannot safely be turned into a path".
//	                     They are TRUE OF THE STRING, not of a filesystem,
//	                     so they are unconditional: every platform, every
//	                     build, read path and create path alike (FR-0002,
//	                     FR-0002a, FR-0002b). They live OUTSIDE RuleSet —
//	                     see ValidateAddressingSafety — precisely so that
//	                     no future edit can turn one of them into a
//	                     platform-conditional field.
//
//	Name shape         — the Windows rules above. These exist to stop
//	                     Omnipus CREATING a name that a Windows deployment
//	                     could not open. They are a property of the
//	                     destination filesystem, so they belong to a
//	                     RuleSet, they are selected by the build target,
//	                     and they are evaluated only on create/rename and
//	                     only after root resolution, where the caller knows
//	                     whether the destination is workspace storage
//	                     (ours to name) or a mount (the operator's).
//
// # Why a value and not a build tag around the behaviour
//
// The rule set is a VALUE passed in, not a compile-time fork of the rule
// bodies, for two independent reasons:
//
//  1. No CI job runs Go tests on Windows — all nineteen workflows run on
//     Ubuntu or macOS, and the only Windows exposure is cross-compilation,
//     which proves the code compiles and asserts nothing about what it
//     does. Forking the behaviour on a tag would ship half of Stage 0 with
//     zero executed assertions while CI reported green. Because the set is
//     a value, both verdicts are exercised on one Linux runner by a table
//     that passes each set explicitly; only ONE fact then needs a Windows
//     machine — that the right set is selected there (TestRuleSet_
//     ActiveSetMatchesGOOS).
//
//  2. The selection is by GOOS, in a pair of one-line files
//     (rules_windows.go / rules_other.go), and deliberately NOT by a
//     custom build tag. A custom tag is a runtime footgun in platform
//     clothing: an operator running the Linux binary with the tag set
//     would get Windows rules on a filesystem that never needed them,
//     which is the exact behaviour Stage 0 removes.
//
// # Blast radius
//
// ValidateComponent has 17 dependent symbols across Gateway and Agent and
// is rated CRITICAL. Nothing here changes an exported signature: the
// existing package-level functions keep theirs and delegate to the active
// set, so the number of call sites that have to change is zero.

// MaxComponentNameBytes bounds a single path component in BYTES, and is
// the POSIX half of FR-0004's "measure a limit in the unit the thing it
// protects uses".
//
// Three units are in play and the pre-Stage-0 code conflated them: the
// component cap was 100 RUNES, its own rationale cites Windows MAX_PATH
// which counts UTF-16 CODE UNITS, and every POSIX filesystem Omnipus
// targets caps a component at 255 BYTES (ext4/xfs/btrfs NAME_MAX, and
// APFS's 255-byte limit). The consequence of conflating them is not
// theoretical in either direction: a 90-rune CJK name is 270 bytes — well
// inside a 100-rune cap and impossible to create on Linux — while a
// 106-rune Latin name is 106 bytes and creates fine.
//
// This applies on CREATION only. On the read path no length limit applies
// at all (FR-0001), because a name already on disk is by construction
// inside its own filesystem's limit.
const MaxComponentNameBytes = 255

// RuleSet is the set of NAME-SHAPE rules in force for a destination
// filesystem. It is a plain comparable value: pass it to a rule function
// rather than branching on the platform inside one, so both verdicts are
// reachable from a single test binary on a single runner.
//
// The two instances are POSIXRules and WindowsRules. They differ ONLY in
// whether the Windows-shape rules are active, plus the length rule's unit,
// which is that same distinction expressed in the unit each platform
// actually enforces (FR-0004). Everything else about validating a name —
// emptiness, "." / "..", control runes, traversal, root confinement — is
// deliberately NOT represented here, because none of it varies. See this
// file's header.
//
// A zero RuleSet applies no shape rules at all. That is a meaningful,
// safe value (it is exactly POSIXRules minus the byte budget) and not a
// mistake to guard against: shape rules only ever ADD refusals, so an
// empty set can never let through something addressing safety would have
// caught.
type RuleSet struct {
	// Name identifies the set in errors and test failures. "posix" or
	// "windows".
	Name string

	// IllegalRunes are the characters this filesystem refuses anywhere in
	// a component. Empty disables the rule. Note that C0 control runes are
	// NOT in here on any set — they are addressing safety and are rejected
	// unconditionally by ValidateAddressingSafety. Fusing the two is the
	// specific mistake FR-0002a exists to prevent: they were one predicate
	// before Stage 0, so relaxing the character set would have relaxed
	// control-character rejection with it, silently and on every path.
	IllegalRunes string

	// ReservedDeviceNames enables rejection of the Windows device names
	// (CON, PRN, AUX, NUL, COM1-9, LPT1-9), matched case-insensitively
	// against the stem and regardless of extension.
	ReservedDeviceNames bool

	// TrailingDotOrSpace enables rejection of a name ending in '.' or ' ',
	// which the Win32 API silently strips before the name reaches NTFS.
	TrailingDotOrSpace bool

	// MaxComponentRunes caps one component in runes. 0 disables the rule.
	// Windows only: the budget descends from MAX_PATH, which POSIX has no
	// equivalent of.
	MaxComponentRunes int

	// MaxComponentBytes caps one component in bytes. 0 disables the rule.
	// POSIX only: this is NAME_MAX, which Windows has no equivalent of.
	MaxComponentBytes int

	// MaxRelPathRunes caps a whole assembled relative path in runes. 0
	// disables the rule. Windows only, for the same MAX_PATH reason: many
	// short, individually-legal segments can still sum past the ceiling
	// once combined with Omnipus's own install prefix.
	MaxRelPathRunes int
}

// POSIXRules is the set in force on Linux and macOS. No Windows-shape
// rule is active; the only length rule is the one POSIX filesystems
// themselves enforce, in bytes.
var POSIXRules = RuleSet{
	Name:                "posix",
	IllegalRunes:        "",
	ReservedDeviceNames: false,
	TrailingDotOrSpace:  false,
	MaxComponentRunes:   0,
	MaxComponentBytes:   MaxComponentNameBytes,
	MaxRelPathRunes:     0,
}

// WindowsRules is the set in force on Windows, and is byte-for-byte the
// pre-Stage-0 behaviour of this package minus the control-character check
// that moved out of it (which still runs, unconditionally, for every set).
// The 29 assertions in pathsafe_test.go hold under this set unchanged.
var WindowsRules = RuleSet{
	Name:                "windows",
	IllegalRunes:        illegalRunes,
	ReservedDeviceNames: true,
	TrailingDotOrSpace:  true,
	MaxComponentRunes:   MaxComponentNameLength,
	MaxComponentBytes:   0,
	MaxRelPathRunes:     MaxRelPathLength,
}

// SanitizeRules is the set the SANITIZING path must use, on every
// platform, forever: the strict one.
//
// This is not a copy-paste of ActiveRules and must never become one
// (FR-0001d). The sanitizer serves exactly one caller class — an inbound
// attachment from Discord, Telegram, Feishu or QQ, whose filename is
// remote and attacker-chosen, and which cannot be rejected because there
// is nobody to return an error to. Stage 0's argument ("these are the
// operator's own files") is false for that caller, so nothing about it may
// relax on any platform.
//
// Naming it here rather than leaving SanitizeComponent to reach for
// whatever is in scope is the point: the most natural implementation of
// Stage 0 — making the illegal-character set depend on the build — relaxes
// the sanitizer too, as a side effect, because the validating and
// sanitizing paths shared one predicate. That is the opposite of the
// intent, and TestSanitizeComponent_UnchangedOnEveryBuild exists to catch
// it.
var SanitizeRules = WindowsRules

// ActiveRules returns the rule set for the platform this binary was built
// for, selected by GOOS in rules_windows.go / rules_other.go. The
// package-level ValidateComponent and ValidateRelPathLength delegate here,
// which is what keeps their signatures — and all 17 dependent symbols —
// untouched.
//
// Callers on the READ path should not use this at all: FR-0001 says name
// shape has no bearing on listing, opening, indexing or linking a file
// that already exists. It is the create/rename path's tool, applied after
// root resolution (FR-0001a) and skipped inside mounts (FR-0001b).
func ActiveRules() RuleSet { return activeRules }

// ValidateAddressingSafety enforces the rules that never vary, for a
// single path component: not empty, not "." or "..", and free of C0
// control runes (0x00-0x1F, which includes NUL, CR and LF).
//
// It takes no RuleSet ON PURPOSE. These are properties of the string
// rather than of a filesystem — a component that is "." does not address
// a child on ANY operating system, and a NUL byte truncates a path in
// every C library there has ever been. Making any of them a RuleSet field
// would be enough for a later edit to switch one off for a platform,
// which is the failure Stage 0 is most exposed to: it relaxes a validator
// that 17 symbols depend on, so an over-broad relaxation would be a
// containment regression rather than a cosmetic one.
//
// The "." / ".." check is explicitly independent of the trailing-dot rule
// (FR-0002b). Before Stage 0, ValidateComponent("..") failed ONLY via
// hasTrailingDotOrSpace — a Windows-shape rule. Turning that rule off for
// POSIX, with no replacement, would have let ".." through this function
// on Linux and macOS. library.CleanRelPath has its own check and would
// still have caught it, but the guarantee must not depend on every caller
// remembering to repeat it.
func ValidateAddressingSafety(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: %q addresses no child", ErrEmptyName, name)
	}
	if r, ok := FirstControlRune(name); ok {
		return fmt.Errorf("%w: %q contains control character %#U", ErrIllegalChar, name, r)
	}
	return nil
}

// FirstControlRune returns the first C0 control rune (0x00-0x1F) in name,
// if any. Unconditional by design — see ValidateAddressingSafety.
func FirstControlRune(name string) (rune, bool) {
	for _, r := range name {
		if r <= 0x1F {
			return r, true
		}
	}
	return 0, false
}

// ReplaceControlRunes replaces every C0 control rune with '_', in one
// single left-to-right pass — never a repeated or iterative substring
// removal, which is the bug class (see the package doc) that let a crafted
// input reconstitute a disallowed sequence after an earlier pass "removed"
// it.
//
// This is the unconditional half of the sanitizing split FR-0002a
// requires. Composed with RuleSet.ReplaceIllegalRunes it reproduces the
// pre-Stage-0 fused pass exactly: both halves only ever MAP a rune to '_',
// never delete, and '_' is in neither rejected set, so the composition is
// order-independent and cannot re-expose anything.
func ReplaceControlRunes(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r <= 0x1F {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ValidateComponent checks one path component against BOTH halves:
// addressing safety first (unconditional), then this set's name-shape
// rules. It is the drop-in the package-level ValidateComponent delegates
// to.
//
// Composing the unconditional half in here, rather than leaving it to
// each caller, is deliberate: it makes "control characters are rejected
// under every rule set" true by construction instead of by convention. A
// caller that wants the shape rules in isolation — a test asserting which
// half refused a name, say — should call ValidateNameShape.
func (s RuleSet) ValidateComponent(name string) error {
	if err := ValidateAddressingSafety(name); err != nil {
		return err
	}
	return s.ValidateNameShape(name)
}

// ValidateNameShape checks only this set's name-shape rules, in the same
// order the pre-Stage-0 ValidateComponent used, so the sentinel a given
// name fails with under WindowsRules is the sentinel it failed with
// before. It assumes ValidateAddressingSafety has already run (or is about
// to); on its own it will happily accept "" and "..", which is why it is
// the narrower of the two entry points and not the default one.
func (s RuleSet) ValidateNameShape(name string) error {
	if r, ok := s.FirstIllegalRune(name); ok {
		return fmt.Errorf("%w: %q contains %q", ErrIllegalChar, name, string(r))
	}
	if s.HasTrailingDotOrSpace(name) {
		return fmt.Errorf("%w: %q", ErrTrailingDotOrSpace, name)
	}
	if s.IsReservedDeviceName(name) {
		return fmt.Errorf("%w: %q", ErrReservedName, name)
	}
	if s.MaxComponentRunes > 0 {
		if n := utf8.RuneCountInString(name); n > s.MaxComponentRunes {
			return fmt.Errorf("%w: %q is %d characters, over the %d-character limit",
				ErrNameTooLong, name, n, s.MaxComponentRunes)
		}
	}
	if s.MaxComponentBytes > 0 {
		if n := len(name); n > s.MaxComponentBytes {
			return fmt.Errorf("%w: %q is %d bytes, over the %d-byte limit",
				ErrNameTooLong, name, n, s.MaxComponentBytes)
		}
	}
	return nil
}

// ValidateRelPathLength checks a whole assembled, already-cleaned relative
// path against this set's total budget. Disabled sets (MaxRelPathRunes ==
// 0) accept everything: the cap descends entirely from Windows' MAX_PATH,
// and no POSIX filesystem Omnipus targets has a comparable ceiling worth
// enforcing.
func (s RuleSet) ValidateRelPathLength(relPath string) error {
	if s.MaxRelPathRunes <= 0 {
		return nil
	}
	if n := utf8.RuneCountInString(relPath); n > s.MaxRelPathRunes {
		return fmt.Errorf("%w: relative path %q is %d characters, over the %d-character limit",
			ErrNameTooLong, relPath, n, s.MaxRelPathRunes)
	}
	return nil
}

// FirstIllegalRune returns the first character this set refuses anywhere
// in a component, if any. Control runes are not its business — see
// FirstControlRune.
func (s RuleSet) FirstIllegalRune(name string) (rune, bool) {
	if s.IllegalRunes == "" {
		return 0, false
	}
	for _, r := range name {
		if strings.ContainsRune(s.IllegalRunes, r) {
			return r, true
		}
	}
	return 0, false
}

// HasTrailingDotOrSpace reports whether name ends in '.' or ' ' AND this
// set cares. A byte-level check on the last byte is safe even for
// multi-byte UTF-8 content: '.' (0x2E) and ' ' (0x20) are both ASCII
// values no UTF-8 continuation byte (always >= 0x80) can equal, so this
// never misfires on a name ending in a multi-byte rune.
func (s RuleSet) HasTrailingDotOrSpace(name string) bool {
	if !s.TrailingDotOrSpace || name == "" {
		return false
	}
	last := name[len(name)-1]
	return last == '.' || last == ' '
}

// IsReservedDeviceName reports whether name's stem — everything before its
// first '.', or the whole name if it has none — is a Windows reserved
// device name, matched case-insensitively, AND this set cares. Windows
// reserves the DEVICE name regardless of what extension follows, so
// "nul.txt" is exactly as unusable as bare "nul".
func (s RuleSet) IsReservedDeviceName(name string) bool {
	if !s.ReservedDeviceNames {
		return false
	}
	stem := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		stem = name[:i]
	}
	return reservedDeviceNames[strings.ToUpper(stem)]
}

// ReplaceIllegalRunes replaces every character this set refuses with '_',
// in one single left-to-right pass. It is the shape half of the sanitizing
// split (FR-0002a); ReplaceControlRunes is the other, unconditional half,
// and SanitizeComponent must compose them off SanitizeRules — never off
// ActiveRules, or a Linux build would start storing remote attachment
// names that a Windows build refuses.
func (s RuleSet) ReplaceIllegalRunes(name string) string {
	if s.IllegalRunes == "" {
		return name
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if strings.ContainsRune(s.IllegalRunes, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

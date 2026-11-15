package config

import "sync"

// Edition is the product this binary was built as (ADR-0010). It is stamped at
// build time, exactly like Version:
//
//	-X github.com/elicify-ai/omnipus/pkg/config.Edition=core|desktop|hosted
//
// The source default is core — the open-source engine built without any
// stamp behaves as upstream does. The commercial builds stamp desktop or
// hosted, and two guards make a missing stamp fail closed rather than fall
// back to a password login: the hosted image's provenance check and the
// desktop build target assert the stamp, and EditionMisbuild refuses to boot
// a core-stamped binary that has a platform trust anchor configured (a
// hosted instance always has one, so that combination is only ever a
// misbuilt image).
var Edition = EditionCore

// editionMu orders reads and writes of Edition across goroutines. Production
// never writes Edition after start (the linker stamps it), so the lock costs
// nothing there; it exists so a test that swaps the edition cannot race a
// goroutine an earlier test left running (#1819).
var editionMu sync.RWMutex

// CurrentEdition returns the stamped edition. Every reader goes through it,
// never through the Edition variable directly.
func CurrentEdition() string {
	editionMu.RLock()
	defer editionMu.RUnlock()
	return Edition
}

// SetEdition replaces the edition and returns a function that restores the
// previous value. It is for tests; a real build stamps Edition at link time.
func SetEdition(e string) (restore func()) {
	editionMu.Lock()
	prev := Edition
	Edition = e
	editionMu.Unlock()
	return func() {
		editionMu.Lock()
		Edition = prev
		editionMu.Unlock()
	}
}

const (
	EditionCore    = "core"
	EditionDesktop = "desktop"
	EditionHosted  = "hosted"
)

// AuthMode is how a build authenticates its users. It is derived from the
// edition and never read from config, so nothing at runtime can move a
// hosted or desktop binary back to the password login ADR-0008 removed.
type AuthMode string

const (
	// AuthModeLocal: the engine's own password login and step-up password
	// gate; the open-source edition.
	AuthModeLocal AuthMode = "local"
	// AuthModePlatform: sign-in is delegated to a registered platform
	// provider (omnipus.ai); no local credential exists.
	AuthModePlatform AuthMode = "platform"
)

// KnownEdition reports whether the stamped edition is one of the three the
// engine knows. Anything else is a misbuild and must not be served.
func KnownEdition() bool {
	switch CurrentEdition() {
	case EditionCore, EditionDesktop, EditionHosted:
		return true
	}
	return false
}

// EditionAuthMode derives the auth mode from the stamped edition. For an
// unknown edition it returns "" so that a caller composing routes selects
// none of them — sign-in answers 503 — instead of guessing.
func EditionAuthMode() AuthMode {
	switch CurrentEdition() {
	case EditionCore:
		return AuthModeLocal
	case EditionDesktop, EditionHosted:
		return AuthModePlatform
	}
	return ""
}

// EditionMisbuild names the reason a binary must refuse to boot, or "" when
// it may. A core-stamped binary with a platform trust anchor configured is a
// hosted or desktop image whose build lost its edition stamp; serving it
// would expose the password login on an instance that is supposed to have
// none.
func EditionMisbuild(platformIssuer string) string {
	edition := CurrentEdition()
	if !KnownEdition() {
		return "unknown edition stamp " + edition + " — the build is not one of core, desktop or hosted"
	}
	if edition == EditionCore && platformIssuer != "" {
		return "a core-stamped binary has a platform trust anchor configured — the build lost its edition stamp; refusing to serve a password login on a platform instance"
	}
	return ""
}

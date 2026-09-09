// Omnipus — profile cache trimming (ADR-075 D1, FR-072/FR-073/FR-074)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// trim.go bounds what a workspace's browser profile costs on disk, WITHOUT a
// quota and WITHOUT deleting the profile.
//
// The operator ruling (ADR D1.9b ruling 4, 2026-09-01) is verbatim: "Profile
// disk is bounded by periodic cache trimming, not by a quota and not by
// deletion alone. Logins are preserved; the disposable cache is trimmed on a
// schedule."
//
// A per-workspace SIZE CAP was rejected by name, and the reasoning is worth
// keeping where the code is: when a quota binds, something has to be discarded
// mid-session, and the only large things in a profile are the cache AND THE
// LOGINS. Discarding the logins is the single outcome this whole design exists
// to prevent. A trim that only ever removes regenerable data cannot cause it.
//
// THE TRIM IS ALLOW-LIST-DRIVEN AND MUST NEVER BECOME DENY-LIST-DRIVEN.
//
// That is a requirement, not a style preference. A deny-list widens itself
// with every Chromium upgrade — a new directory appears and is deleted because
// nobody listed it — and the first place it would widen into is wherever
// Chromium next moves credentials. An allow-list gets narrower instead: a new
// directory is KEPT until a human classifies it, and the cost of being wrong
// is disk rather than somebody's session.
//
// The criterion the list below is derived from, and the thing to apply when
// Chromium ships a directory nobody here has seen: a path is trimmable if and
// only if the BROWSER wrote it as a performance cache of data it can re-fetch
// or re-derive, AND no site wrote it through a web storage API. Everything
// else is kept. "Who wrote it" is the question, not "does it have the word
// cache in the name" — which is why Service Worker/CacheStorage is PROTECTED
// (a site put it there through the Cache API) while Service Worker/ScriptCache
// is trimmed (Chromium put it there, and re-fetches it).

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// defaultCacheTrimInterval is tools.browser.cache_trim_interval's default: the
// SCHEDULED pass's cadence.
//
// It is deliberately far slower than the one-minute reaper sweep and must not
// be folded into it: the reaper does a map scan, this walks directories. And
// the scheduled pass is not the primary trigger anyway — pool.Close(k)
// returning is, which needs no interval and fires within milliseconds of a
// browser going away.
//
// ⚠ This interval does NOT bound the cache. See FR-074 and
// logUnboundedContinuousDriveOnce: a workspace driven without a long enough
// gap never becomes eligible, so its cache grows for as long as it is driven,
// whatever this value is.
const defaultCacheTrimInterval = time.Hour

// trimAllowList is THE closed set (spec §0.8). Every entry is a path relative
// to a workspace's profile directory, and every one of them is recreated by
// Chromium on the next launch.
//
// Entries ending in a separator are DIRECTORIES whose contents are removed;
// entries with a trailing "*" are prefix matches within their parent (Chromium
// versions the optimisation-guide directories). Nothing else matches anything.
//
// Do not add to this list without answering the criterion above out loud, and
// do not add a pattern broad enough to reach a sibling — trimAllowListPaths
// resolves every entry inside the profile directory and refuses anything that
// escapes it.
var trimAllowList = []string{
	// The HTTP disk cache: the largest and fastest-growing thing in a profile.
	"Default/Cache",
	// Compiled JS and WASM bytecode, re-derived from source on next execution.
	"Default/Code Cache",
	// Compiled shader and GPU program caches.
	"Default/GPUCache",
	"GrShaderCache",
	"ShaderCache",
	// WebGPU pipeline caches.
	"Default/DawnCache",
	"Default/DawnGraphiteCache",
	"Default/DawnWebGPUCache",
	// Cached service-worker SCRIPT bodies. The registration itself lives in
	// Service Worker/Database and is protected; CacheStorage next to it is a
	// site's own quota storage and is protected too.
	"Default/Service Worker/ScriptCache",
	// Downloaded optimisation-hint models, re-fetched on demand. Versioned, so
	// this one is a prefix.
	"Default/optimization_guide_*",
	// Downloaded component payloads.
	"component_crx_cache",
}

// trimProtectedPaths is NOT a mechanism — the mechanism is the allow-list, and
// everything absent from it is already protected. This list exists so a test
// can assert the two never overlap, which is the check that would have caught
// a well-meaning addition of "Default/Service Worker" (the parent) to the
// allow-list.
//
// Read it as documentation of what is at stake in each row of the allow-list.
var trimProtectedPaths = []string{
	"Default/Cookies",                     // the session cookies: why the profile survives eviction at all
	"Default/Network/Cookies",             // ditto, newer layout
	"Default/Login Data",                  // saved credentials
	"Default/Login Data For Account",      // saved credentials, account-scoped
	"Default/Local Storage",               // where many sites keep the token a cookie does not carry
	"Default/Session Storage",             // ditto
	"Default/IndexedDB",                   // origin-owned quota storage: a SITE wrote this
	"Default/Service Worker/CacheStorage", // the Cache API: a SITE wrote this, despite the name
	"Default/Service Worker/Database",     // the service-worker registration itself
	"Default/Preferences",                 // profile identity and settings
	"Default/Web Data",                    // autofill and more
	"Default/Trust Tokens",                // privacy-token state
	"Local State",                         // profile identity
}

// TrimResult reports one pass over one profile.
type TrimResult struct {
	Key            BrowsingKey
	BytesReclaimed int64
	PathsRemoved   int
	// Skipped is true when the profile was NOT eligible — a live instance, or
	// a launch lock somebody else holds. A skip is a normal outcome, not a
	// failure: it means the browser is in use.
	Skipped bool
}

// TrimProfile removes the allow-listed cache paths from key's profile
// directory, if and only if that key is eligible (FR-072).
//
// ELIGIBILITY IS THE FR-042a DISCRIMINATOR, REUSED VERBATIM: no live instance
// for the key in this pool, no launch IN PROGRESS for it, AND the per-key
// launch lock acquirable. There is deliberately no second "is it running?"
// notion. A separate liveness check would be a second answer to a question the
// pool already answers, and the two would first disagree in exactly the case
// that matters — a second gateway against the same $OMNIPUS_HOME, where this
// pool's instances map knows nothing and only the lock does.
//
// THE LAUNCHING CHECK IS NOT REDUNDANT WITH EITHER OF THE OTHER TWO, and this
// is the R5 finding-2 defect. A key is registered in p.launching BEFORE
// p.launch runs, and p.launch does the profile MkdirAll and only then hands
// off to the coordinator, which takes the launch lock inside takeLaunchLock.
// For that whole window — a Chrome-for-Testing download makes it seconds, not
// microseconds — there is no instance in the map and nobody holds the lock, so
// the old two-part check called the profile eligible and started deleting
// directories out from under a browser that was starting.
//
// Winning the lock first does not save us either, which is why the fix is a
// refusal rather than a lock-ordering argument. When the trim holds the lock
// and the launcher arrives, takeLaunchLock finds it held, finds no ownership
// marker (the trim writes none), concludes "stale lockfile from a crashed
// process", os.Remove()s it and re-acquires. On Unix that leaves the trim
// holding a flock on an unlinked inode while Chrome launches into the
// directory it is mid-walk on. The lock is a guard against another GATEWAY,
// not against this pool's own launch path; only this pool knows about that.
//
// The profile DIRECTORY is never removed here. That has one trigger and it is
// workspace deletion (FR-043a, DeleteProfile).
func (p *BrowserPool) TrimProfile(key BrowsingKey) TrimResult {
	res := TrimResult{Key: key}

	p.mu.Lock()
	_, live := p.instances[key.String()]
	_, launching := p.launching[key.String()]
	p.mu.Unlock()
	if live || launching {
		res.Skipped = true
		return res
	}

	dir, err := p.ProfileDirFor(key)
	if err != nil {
		res.Skipped = true
		return res
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		res.Skipped = true
		return res
	}

	// The launch lock. Holding it for the duration of the walk is what stops a
	// concurrent launch starting Chrome into a directory we are deleting out
	// from under it.
	lock, acquired, lockErr := acquireLaunchLock(filepath.Join(dir, launchLockFileName))
	if lockErr != nil || !acquired {
		res.Skipped = true
		return res
	}
	defer releaseLaunchLock(lock)

	for _, target := range trimAllowListPaths(dir) {
		size := dirSizeBytes(target)
		if err := os.RemoveAll(target); err != nil {
			logger.WarnCF("browser", "could not trim a browser cache directory (leaving it)", map[string]any{
				"workspace": key.WorkspaceID(),
				"path":      target,
				"error":     err.Error(),
			})
			continue
		}
		res.BytesReclaimed += size
		res.PathsRemoved++
	}

	if res.PathsRemoved > 0 {
		logger.InfoCF("browser", "trimmed a closed workspace browser's disposable cache (its logins are untouched)",
			map[string]any{
				"workspace":       key.WorkspaceID(),
				"paths_removed":   res.PathsRemoved,
				"bytes_reclaimed": res.BytesReclaimed,
			})
	}
	return res
}

// trimAllowListPaths resolves the allow-list against one profile directory and
// returns only the entries that actually exist.
//
// Every resolved path is checked to be INSIDE dir. The allow-list is a
// hardcoded constant so this cannot fire today, but the check is what keeps it
// true if somebody later makes an entry configurable — and the thing on the
// other side of that boundary is the profile root, holding every other
// workspace's cookies.
func trimAllowListPaths(dir string) []string {
	var out []string
	clean := filepath.Clean(dir)
	for _, entry := range trimAllowList {
		if strings.HasSuffix(entry, "*") {
			parent := filepath.Join(clean, filepath.FromSlash(filepath.Dir(entry)))
			prefix := strings.TrimSuffix(filepath.Base(entry), "*")
			children, err := os.ReadDir(parent)
			if err != nil {
				continue
			}
			for _, c := range children {
				if !strings.HasPrefix(c.Name(), prefix) {
					continue
				}
				if candidate, ok := insideDir(clean, filepath.Join(parent, c.Name())); ok {
					out = append(out, candidate)
				}
			}
			continue
		}
		candidate, ok := insideDir(clean, filepath.Join(clean, filepath.FromSlash(entry)))
		if !ok {
			continue
		}
		if _, err := os.Lstat(candidate); err != nil {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// insideDir reports whether candidate resolves within root, and returns the
// cleaned path. A symlink pointing out of the profile is refused: a trim that
// followed one would delete whatever it pointed at.
func insideDir(root, candidate string) (string, bool) {
	clean := filepath.Clean(candidate)
	if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return "", false
	}
	if info, err := os.Lstat(clean); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return clean, true
}

// dirSizeBytes sums the apparent size of a file or directory tree. Best
// effort: an unreadable entry contributes nothing rather than failing the
// trim, because the number is for an operator's log line, not for a decision.
func dirSizeBytes(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// TrimAllEligible runs a pass over every profile directory under the profile
// root that has no live Chrome. This is trigger 2 (boot) and trigger 3 (the
// tools.browser.cache_trim_interval schedule); trigger 1 is pool.Close(k),
// which calls TrimProfile directly for the one key it just closed.
//
// Triggers 1 and 3 cannot race destructively on one profile: both take the
// per-key launch lock, and the loser skips.
func (p *BrowserPool) TrimAllEligible() []TrimResult {
	root := p.profileRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []TrimResult
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), browsingProfileSegmentPrefix) {
			continue
		}
		key, keyErr := newBrowsingKey(strings.TrimPrefix(e.Name(), browsingProfileSegmentPrefix))
		if keyErr != nil {
			continue
		}
		if res := p.TrimProfile(key); !res.Skipped {
			out = append(out, res)
		}
	}
	return out
}

// logUnboundedContinuousDriveOnce names FR-074's residual out loud, once per
// process, the first time the pool trims anything.
//
// The gap it names is real and the ruling leaves it deliberately: a workspace
// driven without a long enough idle gap never becomes eligible for a trim — its
// per-tab TTL and its idle-close TTL never both elapse — so its cache grows for
// as long as it is driven, whatever cache_trim_interval is set to. Neither
// candidate fix could be taken here: bounding Chromium's own cache with
// --disk-cache-size needs a value nobody has measured (and shipping a launch
// flag on a guess is how --renderer-process-limit once arrived), and trimming
// mid-session means closing a browser somebody is using, which is the outcome
// the ruling exists to prevent.
//
// So it is DECLARED rather than defaulted through. An operator who fills a
// disk should find this line, not silence.
//
// p.cacheTrimInterval is read INSIDE the critical section, with the latch, and
// must stay there. ApplyRuntimeConfig writes that field under p.mu on every
// config reload, and every other reader — CacheTrimInterval() — takes the lock
// too; this one function used to read it afterwards, unsynchronised. It is a
// data race in the plain sense (`go test -race` will say so), and it is a race
// on the value that decides how often the sweep which DELETES directories
// runs, which is why it is worth a sentence rather than a silent one-line
// diff. It happens to be a word-sized read, and that is not a defence: the Go
// memory model gives an unsynchronised read no guarantee at all, and "it is
// only a log line" stops being true the moment somebody reuses the pattern for
// a decision.
func (p *BrowserPool) logUnboundedContinuousDriveOnce() {
	p.mu.Lock()
	already := p.trimResidualLogged
	p.trimResidualLogged = true
	trimEvery := p.cacheTrimInterval
	p.mu.Unlock()
	if already {
		return
	}
	logger.InfoCF(
		"browser",
		"a workspace's browser cache is trimmed only while its browser is CLOSED — a workspace driven "+
			"continuously, with no idle gap, keeps growing its cache for as long as it is driven. "+
			"tools.browser.cache_trim_interval sets how often closed profiles are swept; it does not bound "+
			"an open one",
		map[string]any{"cache_trim_interval": trimEvery.String()},
	)
}

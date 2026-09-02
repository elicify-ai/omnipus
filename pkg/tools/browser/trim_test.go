package browser

// trim_test.go — profile cache trimming, over a fixture profile tree. No
// Chrome runs here; the real-Chrome proof that a login survives a trim is
// TestTrim_LoginSurvivesRelaunch, which does not exist yet (see the pool
// commit's "NOT DONE" list).
//
// The assertion that carries the most weight is
// TestTrim_AllowListContainsNoProtectedPath. It is a STRUCTURAL check on the
// two lists, and it is the one that catches the change nobody would flag in
// review: adding "Default/Service Worker" to the allow-list to sweep up
// ScriptCache, which would take CacheStorage and the registration Database
// with it and log the workspace out of every site using a service worker.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProfileTree writes a realistic profile: one file inside every
// allow-listed path, one inside every protected path, and one directory this
// implementation has never seen.
func seedProfileTree(t *testing.T, dir string) {
	t.Helper()
	write := func(rel, body string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	for _, p := range trimAllowList {
		rel := strings.TrimSuffix(p, "*")
		if strings.HasSuffix(p, "*") {
			rel += "1.2.3"
		}
		write(rel+"/data_0", "regenerable-"+p)
	}
	for _, p := range trimProtectedPaths {
		// Protected entries are a mix of files (Cookies, Local State) and
		// directories (Local Storage, IndexedDB). Writing a child of each
		// covers both: for the file cases this makes a directory of the same
		// name, which is still an artefact the trim must not touch.
		write(p+"/payload", "PROTECTED-"+p)
	}
	// The unseen directory. Chromium ships new ones; the default is KEEP.
	write("Default/SomeFutureChromiumThing/blob", "unclassified")
}

func TestTrim_RemovesAllowListOnly(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")
	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	seedProfileTree(t, dir)

	res := f.pool.TrimProfile(key)

	require.False(t, res.Skipped, "a profile with no live Chrome is eligible")
	assert.Positive(t, res.PathsRemoved)
	assert.Positive(t, res.BytesReclaimed)

	for _, p := range trimAllowList {
		rel := strings.TrimSuffix(p, "*")
		if strings.HasSuffix(p, "*") {
			rel += "1.2.3"
		}
		_, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		assert.True(t, os.IsNotExist(statErr), "%s is regenerable cache and must be gone", p)
	}

	_, statErr := os.Stat(dir)
	assert.NoError(t, statErr,
		"the trim removes named cache subdirectories — never the profile directory itself, "+
			"which has exactly one deletion trigger and it is workspace deletion")
}

// TestTrim_ProtectedSetIsByteIdentical is the assertion the ruling is about.
// Not "still present" — byte-identical, because a trim that truncated or
// rewrote a cookie jar would pass a presence check and still log everyone out.
func TestTrim_ProtectedSetIsByteIdentical(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")
	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	seedProfileTree(t, dir)

	before := map[string][]byte{}
	for _, p := range trimProtectedPaths {
		full := filepath.Join(dir, filepath.FromSlash(p), "payload")
		body, readErr := os.ReadFile(full)
		require.NoError(t, readErr)
		before[full] = body
	}
	unseen := filepath.Join(dir, "Default", "SomeFutureChromiumThing", "blob")
	unseenBody, err := os.ReadFile(unseen)
	require.NoError(t, err)

	f.pool.TrimProfile(key)

	for full, want := range before {
		got, readErr := os.ReadFile(full)
		require.NoError(t, readErr, "%s was removed by the trim — this is a login or a site's own storage", full)
		assert.Equal(t, want, got, "%s was modified by the trim", full)
	}

	got, readErr := os.ReadFile(unseen)
	require.NoError(t, readErr,
		"a directory this implementation has never seen must be KEPT. The default is keep, and it has "+
			"to be: a deny-list trim widens itself with every Chromium upgrade, and the first place it "+
			"widens into is wherever credentials moved to")
	assert.Equal(t, unseenBody, got)
}

// TestTrim_AllowListContainsNoProtectedPath is a structural check on the two
// lists, independent of any filesystem.
//
// A path is unsafe not only when it IS a protected path but when it is a
// PARENT of one — "Default/Service Worker" would remove ScriptCache (fine),
// CacheStorage (a site's own data) and Database (the registration) in one
// RemoveAll. That is the plausible mistake, so it is the one asserted against.
func TestTrim_AllowListContainsNoProtectedPath(t *testing.T) {
	for _, allowed := range trimAllowList {
		a := strings.TrimSuffix(strings.TrimSuffix(allowed, "*"), "/")
		for _, protected := range trimProtectedPaths {
			assert.NotEqual(t, protected, a,
				"%q is in BOTH lists", allowed)
			assert.False(t, strings.HasPrefix(protected+"/", a+"/"),
				"allow-listed %q is a PARENT of protected %q — a RemoveAll there takes the protected "+
					"path with it, and nothing else in this system would notice until somebody was "+
					"logged out", allowed, protected)
		}
	}
}

// TestTrim_SkipsLiveProfile: nothing is ever trimmed while a Chrome is live.
// Eligibility is the FR-042a discriminator and there is deliberately no second
// liveness notion — both halves are asserted here.
func TestTrim_SkipsLiveProfile(t *testing.T) {
	t.Run("a live pool instance blocks it", func(t *testing.T) {
		f := newPoolFixture(t)
		inst := f.mustAcquire(t, "alpha")
		seedProfileTree(t, inst.profileDir)

		res := f.pool.TrimProfile(inst.key)
		assert.True(t, res.Skipped, "a workspace whose browser is running must not have its cache pulled out")
		_, err := os.Stat(filepath.Join(inst.profileDir, "Default", "Cache", "data_0"))
		assert.NoError(t, err, "nothing may be removed from a live profile")
	})

	t.Run("a launch lock held elsewhere blocks it", func(t *testing.T) {
		f := newPoolFixture(t)
		key := browserTestKey("beta")
		dir, err := f.pool.ProfileDirFor(key)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		seedProfileTree(t, dir)

		// This pool holds no instance for the key, but somebody does hold its
		// lock — the second-gateway case, which is exactly where a separate
		// "is it running?" check would have disagreed with the lock and
		// trimmed a live profile.
		held, acquired, lockErr := acquireLaunchLock(filepath.Join(dir, launchLockFileName))
		require.NoError(t, lockErr)
		require.True(t, acquired)
		t.Cleanup(func() { releaseLaunchLock(held) })

		res := f.pool.TrimProfile(key)
		assert.True(t, res.Skipped,
			"the pool's own instances map knows nothing about another gateway's Chrome — only the "+
				"lock does, which is why eligibility is the lock and not a second liveness check")
		_, statErr := os.Stat(filepath.Join(dir, "Default", "Cache", "data_0"))
		assert.NoError(t, statErr)
	})
}

// TestTrim_FiresOnCloseAndOnSweep covers triggers 1 and 3. Trigger 1 —
// pool.Close(k) returning — is the primary one and needs no interval.
func TestTrim_FiresOnCloseAndOnSweep(t *testing.T) {
	t.Run("trigger 1: closing a browser trims its profile", func(t *testing.T) {
		f := newPoolFixture(t)
		inst := f.mustAcquire(t, "alpha")
		seedProfileTree(t, inst.profileDir)

		f.pool.Close(inst.key)

		_, err := os.Stat(filepath.Join(inst.profileDir, "Default", "Cache", "data_0"))
		assert.True(t, os.IsNotExist(err),
			"the cache must be gone within milliseconds of the browser closing — no interval to wait for")
		_, err = os.Stat(filepath.Join(inst.profileDir, "Default", "Cookies", "payload"))
		assert.NoError(t, err, "the login must survive the close and the trim that follows it")
	})

	t.Run("trigger 3: the sweep reaches profiles this process never opened", func(t *testing.T) {
		f := newPoolFixture(t)
		// Two profiles on disk with no instance in this pool — a previous
		// run's, which is precisely what the scheduled pass is for.
		for _, ws := range []string{"alpha", "beta"} {
			dir, err := f.pool.ProfileDirFor(browserTestKey(ws))
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			seedProfileTree(t, dir)
		}

		results := f.pool.TrimAllEligible()

		assert.Len(t, results, 2, "both closed profiles must be swept")
		for _, ws := range []string{"alpha", "beta"} {
			dir, _ := f.pool.ProfileDirFor(browserTestKey(ws))
			_, err := os.Stat(filepath.Join(dir, "Default", "Cache", "data_0"))
			assert.True(t, os.IsNotExist(err), "%s's cache must be trimmed", ws)
			_, err = os.Stat(filepath.Join(dir, "Default", "Local Storage", "payload"))
			assert.NoError(t, err, "%s's web storage must survive", ws)
		}
	})

	t.Run("the sweep skips a live profile", func(t *testing.T) {
		f := newPoolFixture(t)
		live := f.mustAcquire(t, "live")
		seedProfileTree(t, live.profileDir)

		closedDir, err := f.pool.ProfileDirFor(browserTestKey("closed"))
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(closedDir, 0o700))
		seedProfileTree(t, closedDir)

		results := f.pool.TrimAllEligible()

		require.Len(t, results, 1)
		assert.Equal(t, "ws:closed", results[0].Key.String())
		_, statErr := os.Stat(filepath.Join(live.profileDir, "Default", "Cache", "data_0"))
		assert.NoError(t, statErr, "the running workspace's cache is untouched")
	})
}

// TestTrim_RefusesToFollowASymlinkOutOfTheProfile: the allow-list is a
// constant today, so this cannot fire — which is exactly why it is worth
// pinning. The thing on the other side of that boundary is the profile root,
// holding every other workspace's cookies.
func TestTrim_RefusesToFollowASymlinkOutOfTheProfile(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")
	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Default"), 0o700))

	victim := filepath.Join(f.home, "not-a-cache")
	require.NoError(t, os.MkdirAll(victim, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(victim, "important"), []byte("keep me"), 0o600))

	if symErr := os.Symlink(victim, filepath.Join(dir, "Default", "Cache")); symErr != nil {
		t.Skipf("symlinks unavailable on this platform: %v", symErr)
	}

	f.pool.TrimProfile(key)

	_, statErr := os.Stat(filepath.Join(victim, "important"))
	assert.NoError(t, statErr,
		"the trim must not follow a symlink out of the profile — the directory next door holds "+
			"another workspace's cookies")
}

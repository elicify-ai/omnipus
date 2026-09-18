// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Target is one installation destination for a single environment_setup
// operation. The tool layer obtains it with BeginInstall before running the
// agent's command, points the command at Prefix()/Cache()/Tmp() (via the
// OMNIPUS_ENV_* environment variables), and then calls Commit (shared scope,
// success) or Abort (failure/cancellation) exactly once.
//
// For workspace scope the target holds the workspace prefix's lifetime lock
// until Abort — the tool calls Abort on every terminal outcome (success,
// failure, kill, timeout) — so two installers can never run against the same
// workspace prefix at once (ES-FR-03/BDD-05).
type Target struct {
	scope         Scope
	storeDir      string       // shared scope: resolved shared-store directory
	workspaceRoot string       // workspace scope: resolved workspace root
	prefix        string       //
	cache         string       //
	tmp           string       //
	genID         string       // shared scope only
	lock          *installLock // workspace scope: held until Abort
	state         targetState
	now           func() time.Time // test seam
}

type targetState int

const (
	targetActive targetState = iota
	targetCommitted
	targetAborted
)

// errTargetBusy reports that another installation is already running against
// the same workspace prefix (ES-FR-03/BDD-05 same-target serialization).
var errTargetBusy = errors.New("environmentsetup: installation target is busy")

// Paths beneath the workspace root, in os.Root (slash-separated) form.
const (
	// lockRelPath is the workspace prefix lifetime lock file. It lives in
	// the reserved .omnipus metadata dir but OUTSIDE the installer-writable
	// prefix (.omnipus/env): the installer legitimately owns its prefix, so
	// a generic script that clears and recreates it must not be able to
	// unlink the lock mid-run. Creation stays root-confined through the
	// same os.Root as everything else, and Grant() keeps writable access at
	// exactly the prefix — the lock is in no writable grant.
	lockRelPath  = workspaceMetaRelative + "/" + installLockName
	cacheRelPath = workspaceEnvRelative + "/cache"
	tmpRelPath   = workspaceEnvRelative + "/tmp"
)

// cleanupUnpublishedGeneration removes this begin's own unpublished
// generation directory after a post-creation failure. The removal outcome is
// never silently discarded: a failed removal is joined onto the cause so the
// caller sees both.
func cleanupUnpublishedGeneration(dir string, cause error) error {
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		return errors.Join(cause, fmt.Errorf("environmentsetup: remove unpublished generation %s: %w", dir, rmErr))
	}
	return cause
}

// testFailGenerationStep is a test-only seam, nil in production: when set, it
// is called with the rel path of each confined creation step in a shared
// begin, and a non-nil error fails that step after the previous steps
// succeeded — letting tests exercise post-creation failure handling
// deterministically (the generation directory name is not predictable from
// outside, so a failure after generation creation cannot be planted
// beforehand).
var testFailGenerationStep func(rel string) error

// BeginInstall validates inputs and creates the installation destination.
//
//   - ScopeWorkspace: workspaceRoot is the tool-resolved, authorized workspace
//     (must exist); the reserved .omnipus/env subtree is created inside it.
//   - ScopeShared: appDataRoot is the Omnipus data root; a fresh generation
//     directory is allocated under its FINAL name before any command runs, so
//     scripts can embed absolute paths that stay valid after publication.
//
// BeginInstall performs no permission checks — the existing tool-policy
// approval is the approval. It performs no installation either: the agent's
// command does that.
func BeginInstall(appDataRoot string, workspaceRoot string, scope Scope) (*Target, error) {
	switch scope {
	case ScopeWorkspace:
		return beginWorkspaceInstall(workspaceRoot)
	case ScopeShared:
		return beginSharedInstall(appDataRoot)
	case "":
		return nil, errors.New("environmentsetup: scope is required (workspace or shared)")
	default:
		return nil, fmt.Errorf("environmentsetup: unknown scope %q (want %q or %q)", scope, ScopeWorkspace, ScopeShared)
	}
}

func beginWorkspaceInstall(workspaceRoot string) (*Target, error) {
	resolved, err := resolveExistingDir("workspace root", workspaceRoot)
	if err != nil {
		return nil, err
	}
	root, err := openConfinedRoot("workspace root", resolved)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// Create the reserved metadata dir through the confined root: a
	// pre-existing symlink below the authorized workspace root is refused,
	// not followed (ES-FR-03) — the child sandbox cannot retroactively
	// protect these parent-side operations.
	if err = mkdirConfined(root, "workspace metadata dir", workspaceMetaRelative); err != nil {
		return nil, err
	}
	// The prefix lifetime lock lives in the reserved metadata dir, OUTSIDE
	// the installer-writable prefix, so a generic installer that clears and
	// recreates its prefix cannot unlink it mid-run. Acquire it before
	// anything beyond the metadata dir is touched, so a concurrent begin is
	// refused before it mutates anything else.
	lock, err := acquireInstallLock(root, lockRelPath)
	if err != nil {
		if errors.Is(err, errTargetBusy) {
			return nil, fmt.Errorf("%w: another setup is already installing into this workspace", err)
		}
		return nil, fmt.Errorf("acquire workspace install lock: %w", err)
	}
	// Startup failure after the lock was taken: release it before failing.
	if err := mkdirConfined(root, "workspace env subtree", workspaceEnvRelative); err != nil {
		lock.release()
		return nil, err
	}
	if err := mkdirConfined(root, "workspace cache", cacheRelPath); err != nil {
		lock.release()
		return nil, err
	}
	if err := mkdirConfined(root, "workspace tmp", tmpRelPath); err != nil {
		lock.release()
		return nil, err
	}
	lexical := filepath.Join(resolved, filepath.FromSlash(workspaceEnvRelative))
	return &Target{
		scope:         ScopeWorkspace,
		workspaceRoot: lexical, // resolved; kept for reference only
		prefix:        lexical,
		cache:         WorkspaceCacheDir(lexical),
		tmp:           WorkspaceTmpDir(lexical),
		lock:          lock,
		now:           time.Now,
	}, nil
}

func beginSharedInstall(appDataRoot string) (*Target, error) {
	// Anchor the confined root at the AUTHORIZED data root (not at the store,
	// and not lexically): the store and all its ancestors below the data root
	// are descendants of an authorized root, so a planted <dataRoot>/toolchains
	// symlink must be refused here, before any creation follows it (CRIT2).
	resolved, err := resolveExistingDir("data root", appDataRoot)
	if err != nil {
		return nil, err
	}
	root, err := openConfinedRoot("data root", resolved)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = mkdirConfined(root, "shared store", storeRelative); err != nil {
		return nil, err
	}
	// The store was created (or already lives) INSIDE the confined data root,
	// so the lexical join is its real location; no symlink components were
	// followed to reach it.
	realStore := filepath.Join(resolved, filepath.FromSlash(storeRelative))
	t := &Target{
		scope:    ScopeShared,
		storeDir: realStore,
		now:      time.Now,
	}
	t.genID, err = newGenerationID(t.now)
	if err != nil {
		return nil, err
	}
	// The generation dir is allocated under its FINAL name through the
	// same confined data-root handle — store descendants cannot be
	// redirected by a planted symlink either. The rel paths are
	// DATA-ROOT-relative: the store component must be part of the path,
	// or the gen tree would be created beside the store instead of in it.
	genRel := storeRelative + "/" + t.genID
	t.prefix = filepath.Join(realStore, t.genID)
	t.cache = filepath.Join(t.prefix, cacheSubdirName)
	t.tmp = filepath.Join(t.prefix, tmpSubdirName)
	for _, step := range []struct{ what, rel string }{
		{"shared generation", genRel},
		{"shared cache", genRel + "/" + cacheSubdirName},
		{"shared tmp", genRel + "/" + tmpSubdirName},
	} {
		if testFailGenerationStep != nil {
			if err := testFailGenerationStep(step.rel); err != nil {
				return nil, cleanupUnpublishedGeneration(t.prefix, err)
			}
		}
		if err := mkdirConfined(root, step.what, step.rel); err != nil {
			return nil, cleanupUnpublishedGeneration(t.prefix, err)
		}
	}
	return t, nil
}

// Scope reports the destination scope of this target.
func (t *Target) Scope() Scope { return t.scope }

// Prefix is the script-visible installation prefix: the workspace env subtree
// (workspace scope) or the pre-allocated final generation directory (shared
// scope). Set as EnvVarPrefix for the setup child.
func (t *Target) Prefix() string { return t.prefix }

// Cache is the operation's writable cache directory. Set as EnvVarCache.
func (t *Target) Cache() string { return t.cache }

// Tmp is the operation's writable temp directory. Set as EnvVarTmp.
func (t *Target) Tmp() string { return t.tmp }

// Grant returns the sandbox-relevant shape of this destination for the setup
// child: write access on exactly the prefix — cache and tmp live inside it,
// so one rule confines the whole operation (ES-FR-03).
func (t *Target) Grant() Paths {
	return Paths{Root: t.prefix, Writable: []string{t.prefix}}
}

// GenerationID reports the shared generation allocated for this target ("" for
// workspace scope).
func (t *Target) GenerationID() string { return t.genID }

// Commit publishes a completed shared-scope installation. It must be called
// only after the installation command exited 0; a successful Commit means the
// tree was publishable — it does NOT mean any installed application works.
//
// Under the store flock, Commit: digests the tree, writes the install marker
// with that digest, locks the tree read-only (symlink-safe), and atomically
// prepends the generation to published.json. The tree is not moved, so
// absolute paths the command baked in stay valid. On any error the previous
// published set is untouched and the generation remains unpublished.
func (t *Target) Commit() (SharedResult, error) {
	if t.scope != ScopeShared {
		return SharedResult{}, errors.New("environmentsetup: only shared-scope installs are committed")
	}
	if t.state != targetActive {
		return SharedResult{}, errors.New("environmentsetup: target already spent (Commit or Abort was called)")
	}
	err := fileutil.WithFlock(filepath.Join(t.storeDir, installLockName), func() error {
		digest, count, err := digestTree(t.prefix, markerFileName)
		if err != nil {
			return fmt.Errorf("digest installed tree: %w", err)
		}
		if count == 0 {
			return errors.New("environmentsetup: the installation command produced no files; refusing to publish an empty generation")
		}
		if err := t.writeMarker(digest); err != nil {
			return err
		}
		if err := lockDownReadOnly(t.prefix); err != nil {
			return fmt.Errorf("restrict published tree permissions: %w", err)
		}
		return publishGeneration(t.storeDir, t.genID)
	})
	if err != nil {
		return SharedResult{}, err
	}
	t.state = targetCommitted
	return SharedResult{Generation: t.genID, Dir: t.prefix}, nil
}

// Abort releases an installation target. For every scope it releases the
// prefix lifetime lock — the tool calls Abort on every terminal outcome
// (success, failure, kill, timeout), so this is where the workspace target
// frees the prefix for the next installer. In shared scope it additionally
// removes the not-yet-published generation directory: cleanup restores
// owner-writable permissions inside its OWN unpublished destination (never
// following symlinks), because a Commit that failed after lockDownReadOnly
// leaves a read-only tree that plain RemoveAll cannot delete. A failed or
// cancelled command leaves previously published generations untouched.
func (t *Target) Abort() error {
	switch t.state {
	case targetCommitted:
		return errors.New("environmentsetup: refusing to abort a committed (published) installation")
	case targetAborted:
		return nil
	}
	t.state = targetAborted
	t.lock.release()
	if t.scope != ScopeShared {
		return nil
	}
	// Cleanup truthfulness: removal outcome is the contract; the permission
	// restore is best-effort because a successful removal makes it moot.
	_ = restoreOwnerWrites(t.prefix)
	if err := os.RemoveAll(t.prefix); err != nil {
		return fmt.Errorf("remove unpublished installation: %w", err)
	}
	return nil
}

// Close is an alias for Abort, so callers can defer target cleanup.
func (t *Target) Close() error { return t.Abort() }

// writeMarker records completeness and integrity data inside the generation
// directory, atomically.
func (t *Target) writeMarker(digest string) error {
	marker := installMarker{
		StorageVersion: storageVersion,
		Generation:     t.genID,
		Digest:         digest,
		PublishedAt:    t.now().UTC().Format(time.RFC3339),
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(t.prefix, markerFileName), data, 0o444)
}

// installMarker is the completeness marker inside a generation directory.
type installMarker struct {
	StorageVersion int    `json:"storage_version"`
	Generation     string `json:"generation"`
	Digest         string `json:"digest"`
	PublishedAt    string `json:"published_at"`
	Platform       string `json:"platform"`
}

// publishGeneration atomically prepends the generation ID to published.json
// (newest first = PATH precedence order). Caller holds the store flock.
func publishGeneration(storeDir, generationID string) error {
	set, _, err := readPublishedSet(storeDir)
	if err != nil {
		return err
	}
	// Additive: keep every earlier entry, prepend the new one. A duplicate
	// (same ID re-published — cannot happen with unique IDs, but stay safe)
	// moves to the front rather than appearing twice.
	entries := []string{generationID}
	for _, id := range set.Generations {
		if id != generationID {
			entries = append(entries, id)
		}
	}
	data, err := json.Marshal(publishedSet{Version: storageVersion, Generations: entries})
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(storeDir, publishedFileName), data, 0o644)
}

// newGenerationID mints a unique generation ID: a UTC timestamp (sortable,
// human-readable) plus random bytes (uniqueness under concurrent creation).
func newGenerationID(now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("environmentsetup: mint generation id: %w", err)
	}
	return fmt.Sprintf("%s%s-%s", generationPrefix, now().UTC().Format("20060102T150405"), hex.EncodeToString(buf[:])), nil
}

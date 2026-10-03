package gateway

// mail_cleanup.go — the removal cascade and pending-cleanup intents
// (w5-integration wave; w5 spec US-5/MC-8/MC-9/MC-23; founder Q-B=A: the
// watcher state is EXCLUDED and PURGED on mailbox removal; §7.1 removal
// cascade row).
//
// Ordering is the correctness argument (US-5.7/B-22: tombstone/advance
// FIRST, then revoke, then purge):
//
//  1. the config row is deleted and the credential removed (the callers do
//     this before invoking the cascade — the pair can no longer authorize
//     or re-resolve);
//  2. the persisted config epoch ADVANCES, so the generation changes and
//     any in-flight work captured under the old generation can no longer
//     publish as current (W1's budget gate; MC-10);
//  3. preview grants and presence bindings for the pair are revoked;
//  4. the private cache subtree and the pair's watcher state file are
//     purged — a failed unlink is NEVER reported as a successful purge:
//     the caller receives the truthful removed_cleanup_pending outcome with
//     a safe closed code and an opaque retry intent.
//
// A LATE completion (a watcher cycle or cache write still in flight when
// the cascade runs) cannot resurrect anything that publishes as current:
// the epoch advanced before the purge, so old-generation work is rejected,
// and the config row is gone so no new cycle starts. The one residual race
// — an in-flight watcher cycle whose save lands between the reload barrier
// and the purge — needs W1's per-pair quiesce seam (the cascade CONSUMES
// "W1 lease/flight/watcher lifecycle"; none is published yet) and is filed
// as the wave report's hand-off, not papered over with sleeps or polling.
//
// Retry: intents survive the config row's deletion because they carry the
// pair's address themselves; POST /api/v1/mailboxes/cleanup re-runs the
// purge from the intent and answers with the same truthful discrimination.
// Boot reconciliation (reconcileOrphanMailState) refuses to serve orphaned
// cache/watcher state for absent pairs and re-attempts their deletion,
// keeping a retry intent when a deletion fails.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Safe closed cleanup codes (MC-8: never raw upstream text, never paths).
const (
	mailCleanupFailedCache   = "cache_cleanup_failed"
	mailCleanupFailedWatcher = "watcher_cleanup_failed"
)

// mailCacheDirName / mailWatchDirName are the two private Mail namespaces
// under the data root (the cache layout the W2 write gate enforces; the
// watcher state namespace pkg/email/watcher.go writes).
const (
	mailCacheDirName = "mail-cache"
	mailWatchDirName = "email-watch"
)

// mailWatcherStateFileName derives the watcher state file name for one pair
// from the ON-DISK FORMAT pkg/email/watcher.go::keyFor defines (lowercase
// alphanumeric runs of each ID joined by "-", plus ".json"). This is a
// format dependency on a stable on-disk name, not a behavior copy; the
// R-3 hand-off asks W1 for an exported path/deletion helper so the
// derivation has exactly one Go definition.
func mailWatcherStateFileName(agentID, workspaceID string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return clean(agentID) + "-" + clean(workspaceID) + ".json"
}

// purgePairMailState removes one pair's disposable Mail state: the private
// cache subtree and the watcher state file. Every target is best-effort and
// independently reported; a failed unlink is returned as a pending code,
// never logged into success. A missing target is already-clean (idempotent
// retry). Symlink components are refused, never followed out of the private
// namespaces. deleteIdentity is REMOVAL-only (US-5/MC-24: identity state is
// deleted only by the removal cascade); a disable purge keeps it so the
// pair's subtree name stays stable across disable→enable.
func purgePairMailState(homePath, agentID, workspaceID string, deleteIdentity bool) (pending []string) {
	// Identity first (read): the cache subtree is named by the pair ID.
	ident, identErr := config.LoadOrMintMailPairIdentity(homePath, agentID, workspaceID)
	if identErr != nil {
		// Without the identity the cache subtree's name is unknown; the
		// boot reconciler will still find it via the identity listing (or
		// it does not exist). Surface honestly.
		slog.Warn("mail cleanup: pair identity unreadable — cache subtree not addressed by this pass")
		pending = append(pending, mailCleanupFailedCache)
	}

	if identErr == nil {
		if err := removeMailCacheSubtree(homePath, ident.PairID); err != nil {
			slog.Warn("mail cleanup: cache subtree purge failed", "code", mailCleanupFailedCache)
			pending = append(pending, mailCleanupFailedCache)
		}
	}

	watchPath := filepath.Join(homePath, mailWatchDirName, mailWatcherStateFileName(agentID, workspaceID))
	if err := removePrivateFile(watchPath); err != nil {
		slog.Warn("mail cleanup: watcher state purge failed", "code", mailCleanupFailedWatcher)
		pending = append(pending, mailCleanupFailedWatcher)
	}

	// Identity state is deleted ONLY by the removal cascade (MC-24) — and
	// only once the purge itself has nothing pending. While a target is
	// still pending, the record STAYS: it is what names the pair's cache
	// subtree, so a retry can find and purge the REAL subtree; deleting it
	// early would make the retry mint a FRESH identity and purge a subtree
	// that never existed while the removed pair's state lived on under a
	// false success (B-18/MC-8 truthfulness). The boot reconciler still
	// backstops a record left by an interrupted cascade (a pending intent
	// dies with the process by design). The delete's own failure leaves an
	// orphan the boot reconciler removes — it is opaque, non-sensitive and
	// serves nothing, so it does not pend the outcome.
	if deleteIdentity && identErr == nil && len(pending) == 0 {
		if err := config.DeleteMailPairIdentity(homePath, agentID, workspaceID); err != nil {
			slog.Warn("mail cleanup: identity record delete deferred to boot reconciliation")
		}
	}
	return pending
}

// removeMailCacheSubtree removes mail-cache/<pairID> refusing symlinks: the
// cache root and the pair directory must be real directories inside the
// private namespace, never links out of it (US-3.4/B-12).
func removeMailCacheSubtree(homePath, pairID string) error {
	if strings.TrimSpace(pairID) == "" || strings.ContainsAny(pairID, "/\\") {
		return errors.New("mail cleanup: malformed pair subtree name")
	}
	cacheRoot := filepath.Join(homePath, mailCacheDirName)
	pairDir := filepath.Join(cacheRoot, pairID)
	if err := refuseSymlinkComponent(cacheRoot); err != nil {
		return err
	}
	if err := refuseSymlinkComponent(pairDir); err != nil {
		return err
	}
	if _, err := os.Lstat(pairDir); errors.Is(err, os.ErrNotExist) {
		return nil // already clean
	}
	return os.RemoveAll(pairDir)
}

// removePrivateFile removes one file, refusing a symlink at the path and
// treating absence as success (idempotent retry, MC-8's retry rule).
func removePrivateFile(path string) error {
	if fi, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("mail cleanup: refusing symlinked private path")
	}
	return os.Remove(path)
}

// refuseSymlinkComponent fails when path itself (or, cheaply, its final
// existing ancestor via EvalSymlinks' contract) is a symlink out of the
// private tree. Lstat on the exact path is the strong check; callers invoke
// it per private component they are about to remove.
func refuseSymlinkComponent(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("mail cleanup: refusing symlinked private path component")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pending-cleanup intents (US-5.2: separately authorized Retry cleanup that
// works after the config row is gone). In-memory, process lifetime: a
// gateway restart drops the intents BY DESIGN — the boot reconciler then
// owns the same state through the orphan sweep, so no pending cleanup is
// ever silently lost across a restart.
// ---------------------------------------------------------------------------

// mailCleanupIntentTTL bounds an intent's life; an expired intent is 404
// (the generated cleanup contract's refused cases).
const mailCleanupIntentTTL = 24 * time.Hour

type mailCleanupIntent struct {
	AgentID     string    `json:"-"`
	WorkspaceID string    `json:"-"`
	PairID      string    `json:"-"` // set only for reconciliation intents (pair already unmapped)
	WatchFile   string    `json:"-"` // set only for reconciliation intents
	Pending     []string  `json:"-"`
	CreatedAt   time.Time `json:"-"`
}

type mailCleanupIntentStore struct {
	mu       sync.Mutex
	byToken  map[string]*mailCleanupIntent
	randRead func([]byte) (int, error)
	now      func() time.Time
}

func newMailCleanupIntentStore() *mailCleanupIntentStore {
	return &mailCleanupIntentStore{
		byToken:  map[string]*mailCleanupIntent{},
		randRead: rand.Read,
		now:      time.Now,
	}
}

// create mints one opaque intent token ("cln_" + 16 hex chars). Entropy
// failure refuses: an unaddressable pending cleanup must never masquerade
// as a retryable one.
func (s *mailCleanupIntentStore) create(in *mailCleanupIntent) (string, error) {
	buf := make([]byte, 8)
	n, err := s.randRead(buf)
	if err != nil || n != len(buf) {
		return "", fmt.Errorf("mail cleanup: intent entropy failed: %w", err)
	}
	token := "cln_" + hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	in.CreatedAt = s.now()
	s.byToken[token] = in
	return token, nil
}

// take returns (and keeps) the intent for one token; unknown/expired is
// false. The intent STAYS until its cleanup fully completes — a retry that
// fails again must keep the same intent (the generated contract: the retry
// "returns removed_cleanup_pending again (with the same intent)").
func (s *mailCleanupIntentStore) take(token string) (*mailCleanupIntent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	in, ok := s.byToken[token]
	return in, ok
}

// complete drops the intent once every pending step has succeeded.
func (s *mailCleanupIntentStore) complete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byToken, token)
}

// refresh updates an intent's pending list after a retry that still failed,
// keeping the SAME token addressable (the generated contract: the retry
// "returns removed_cleanup_pending again (with the same intent)").
func (s *mailCleanupIntentStore) refresh(token string, pending []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.byToken[token]; ok {
		in.Pending = pending
	}
}

func (s *mailCleanupIntentStore) expireLocked() {
	now := s.now()
	for tok, in := range s.byToken {
		if now.Sub(in.CreatedAt) > mailCleanupIntentTTL {
			delete(s.byToken, tok)
		}
	}
}

// mailCleanupIntentsOf is the restAPI's nil-safe store accessor.
func (a *restAPI) mailCleanupIntentsOf() *mailCleanupIntentStore {
	a.mailCleanupOnce.Do(func() {
		if a.mailCleanupIntents == nil {
			a.mailCleanupIntents = newMailCleanupIntentStore()
		}
	})
	return a.mailCleanupIntents
}

// mailEpochAdvanceFailed is the safe code for a failed persisted-epoch
// advance (the tombstone/advance step itself).
const mailEpochAdvanceFailed = "epoch_advance_failed"

// mailRemovalCascadeSteps is the shared invalidation sequence of every
// pair-level removal and of the authorized retry: advance the epoch
// (tombstone/advance FIRST — old work can no longer publish), revoke the
// pair's preview grants, then purge both private targets. The caller has
// already removed the config row and the stored credential. Every failure
// lands in the returned safe-code list; nothing is swallowed.
func (a *restAPI) mailRemovalCascadeSteps(agentID, workspaceID string) []string {
	pending := []string{}
	// US-5.7/MC-10: the persisted epoch advances BEFORE older work can
	// publish. A failed advance is visible and pends the outcome — but the
	// purge still runs: with the config row already gone the pair cannot
	// re-authorize, and deleting the state is itself the refuse-to-serve
	// guarantee.
	if _, err := config.AdvanceMailPairEpoch(a.homePath, agentID, workspaceID); err != nil {
		slog.Warn("mail removal: config epoch advance failed — outcome pends", "agent_id", agentID, "workspace_id", workspaceID)
		pending = append(pending, mailEpochAdvanceFailed)
	}
	// Revoke the pair's preview grants (US-5.1: grants/presence revoked
	// before the purge reports). Presence bindings ride each connection's
	// lifecycle; a live observer of a removed workspace simply stops being
	// able to authorize any pair (US-2.5) and dies with its socket.
	if store := a.mailPreviewTokenStoreOf(); store != nil {
		store.invalidatePair(agentID, workspaceID)
	}
	if grants := a.mailAttachmentTokens.Load(); grants != nil {
		grants.invalidatePair(agentID, workspaceID)
	}
	pending = append(pending, purgePairMailState(a.homePath, agentID, workspaceID, true)...)
	return pending
}

// runMailboxRemovalCascade is the shared tail of every pair-level removal:
// the cascade steps, then the truthful outcome.
func (a *restAPI) runMailboxRemovalCascade(agentID, workspaceID string) (outcome, cleanupIntent, cleanupCode string) {
	pending := a.mailRemovalCascadeSteps(agentID, workspaceID)
	if len(pending) == 0 {
		return "removed", "", ""
	}
	return a.recordMailCleanupPending(agentID, workspaceID, pending)
}

// runMailboxDisableCascade is the enabled→disabled transition's honest
// cascade (US-5.1/B-42): same invalidation ordering as removal — epoch
// advance first, grants revoked — then both private targets purged. The
// identity record and the stored credential STAY (a disable is reversible;
// the pair's subtree name must remain stable), and the caller's response
// shape is the Mailbox wire, which cannot carry a cleanup outcome — a
// failed purge is logged with its safe code and left retryable through the
// intent it keeps.
func (a *restAPI) runMailboxDisableCascade(agentID, workspaceID string) {
	if _, err := config.AdvanceMailPairEpoch(a.homePath, agentID, workspaceID); err != nil {
		slog.Warn("mail disable: config epoch advance failed", "agent_id", agentID, "workspace_id", workspaceID)
	}
	if store := a.mailPreviewTokenStoreOf(); store != nil {
		store.invalidatePair(agentID, workspaceID)
	}
	if grants := a.mailAttachmentTokens.Load(); grants != nil {
		grants.invalidatePair(agentID, workspaceID)
	}
	if pending := purgePairMailState(a.homePath, agentID, workspaceID, false); len(pending) > 0 {
		if _, err := a.mailCleanupIntentsOf().create(&mailCleanupIntent{
			AgentID:     agentID,
			WorkspaceID: workspaceID,
			Pending:     pending,
		}); err != nil {
			slog.Warn("mail disable: cleanup pending but intent could not be created", "code", pending[0])
		}
	}
}

// recordMailCleanupPending stores the retry intent and renders the truthful
// pending outcome (MC-8: a failed cleanup is NEVER a successful purge; the
// logged line carries only the safe code).
func (a *restAPI) recordMailCleanupPending(agentID, workspaceID string, pending []string) (outcome, cleanupIntent, cleanupCode string) {
	token, err := a.mailCleanupIntentsOf().create(&mailCleanupIntent{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Pending:     pending,
	})
	if err != nil {
		// No intent addressable: still NEVER a success claim.
		slog.Warn("mail removal: cleanup pending but intent could not be created", "code", pending[0])
		return "removed_cleanup_pending", "", pending[0]
	}
	return "removed_cleanup_pending", token, pending[0]
}

// ---------------------------------------------------------------------------
// The Retry-cleanup endpoint (POST /api/v1/mailboxes/cleanup) and boot
// reconciliation.
// ---------------------------------------------------------------------------

// handleMailboxCleanup implements POST /api/v1/mailboxes/cleanup (the
// generated Retry-cleanup operation; US-5.2/MC-8). The opaque intent is the
// only address — it keeps working after the mailbox config row is gone.
// Unknown, expired or already-completed intents are 404; a retry that fails
// again keeps the SAME intent and answers removed_cleanup_pending again.
func (a *restAPI) handleMailboxCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailboxCleanupRequest
	if !decodeAndValidate(w, r, "MailboxCleanupRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	store := a.mailCleanupIntentsOf()
	intent, ok := store.take(req.CleanupIntent)
	if !ok {
		jsonErr(w, http.StatusNotFound, "unknown, expired or already-completed cleanup intent")
		return
	}
	var pending []string
	switch {
	case intent.AgentID != "" && intent.WorkspaceID != "":
		// Pair-addressed intent: re-run the truthful cascade steps. The
		// epoch advance is idempotent (monotonic), grant revocation a
		// no-op, the purge idempotent — a completed step reports
		// already-clean.
		pending = a.mailRemovalCascadeSteps(intent.AgentID, intent.WorkspaceID)
	default:
		// Reconciliation intent: re-remove the recorded orphan paths.
		pending = retryReconciliationIntent(a.homePath, intent)
	}
	if len(pending) == 0 {
		store.complete(req.CleanupIntent)
		writeJSON(w, http.StatusOK, gen.MailboxRemovalResult{
			Outcome: gen.MailboxRemovalResultOutcomeRemoved,
		})
		return
	}
	// Still pending: same intent, truthful outcome (the generated contract).
	store.refresh(req.CleanupIntent, pending)
	code := pending[0]
	writeJSON(w, http.StatusOK, gen.MailboxRemovalResult{
		Outcome:       gen.MailboxRemovalResultOutcomeRemovedCleanupPending,
		CleanupIntent: &req.CleanupIntent,
		CleanupCode:   &code,
	})
}

// retryReconciliationIntent re-removes one reconciliation intent's recorded
// orphan paths; every already-gone path is success.
func retryReconciliationIntent(homePath string, in *mailCleanupIntent) (pending []string) {
	if in.PairID != "" {
		if err := removeMailCacheSubtree(homePath, in.PairID); err != nil {
			pending = append(pending, mailCleanupFailedCache)
		}
	}
	if in.WatchFile != "" {
		if err := removePrivateFile(in.WatchFile); err != nil {
			pending = append(pending, mailCleanupFailedWatcher)
		}
	}
	return pending
}

// reconcileOrphanMailState is the boot reconciliation (US-5.9/B-33): orphan
// cache subtrees, watcher state files and identity records — state whose
// pair is absent from the live config — are never servable (nothing
// addresses them: every Mail route resolves the pair through the live
// config first), deletion is attempted once here, and a failed deletion
// keeps a safe, visible, retryable intent instead of a log-only success.
// State belonging to LIVE pairs is never touched.
func (a *restAPI) reconcileOrphanMailState() {
	homePath := a.homePath
	cfg := a.agentLoop.GetConfig()
	live := func(agentID, workspaceID string) bool {
		mb, ok := cfg.Mailboxes[agentID][workspaceID]
		return ok && mb.Enabled
	}

	// 1. Identity records: one whose pair is gone names an orphan whose
	// cache subtree and watcher file must go with it.
	idents, listErrs := config.ListMailPairIdentities(homePath)
	for _, e := range listErrs {
		slog.Warn("mail reconciliation: identity record unreadable", "error", e)
	}
	livePairIDs := map[string]bool{}
	liveWatchNames := map[string]bool{}
	for agentID, byWorkspace := range cfg.Mailboxes {
		for workspaceID := range byWorkspace {
			if !live(agentID, workspaceID) {
				continue
			}
			liveWatchNames[mailWatcherStateFileName(agentID, workspaceID)] = true
			if ident, err := config.LoadOrMintMailPairIdentity(homePath, agentID, workspaceID); err == nil {
				livePairIDs[ident.PairID] = true
			}
		}
	}
	for _, ident := range idents {
		if live(ident.AgentID, ident.WorkspaceID) {
			livePairIDs[ident.PairID] = true // a live pair's identity (already counted, keep the map authoritative)
			continue
		}
		in := &mailCleanupIntent{PairID: ident.PairID}
		if werr := removePrivateFile(filepath.Join(homePath, mailWatchDirName, mailWatcherStateFileName(ident.AgentID, ident.WorkspaceID))); werr != nil {
			in.WatchFile = filepath.Join(homePath, mailWatchDirName, mailWatcherStateFileName(ident.AgentID, ident.WorkspaceID))
		}
		if cerr := removeMailCacheSubtree(homePath, ident.PairID); cerr != nil {
			in.PairID = ident.PairID
		} else {
			in.PairID = ""
		}
		if derr := config.DeleteMailPairIdentity(homePath, ident.AgentID, ident.WorkspaceID); derr != nil && in.PairID == "" && in.WatchFile == "" {
			in.PairID = ident.PairID // identity itself stuck: keep an intent alive
		}
		if in.PairID != "" || in.WatchFile != "" {
			if _, err := a.mailCleanupIntentsOf().create(in); err != nil {
				slog.Warn("mail reconciliation: orphan cleanup pending but intent could not be created", "code", mailCleanupFailedCache)
			}
		}
	}

	// 2. Watcher state files for pairs with no live config row.
	watchDir := filepath.Join(homePath, mailWatchDirName)
	if entries, err := os.ReadDir(watchDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || liveWatchNames[e.Name()] {
				continue
			}
			p := filepath.Join(watchDir, e.Name())
			if err := removePrivateFile(p); err != nil {
				if _, cerr := a.mailCleanupIntentsOf().create(&mailCleanupIntent{WatchFile: p}); cerr != nil {
					slog.Warn("mail reconciliation: orphan watcher state pending, intent could not be created", "code", mailCleanupFailedWatcher)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("mail reconciliation: watcher state directory unreadable")
	}

	// 3. Cache subtrees no live pair identity names.
	cacheDir := filepath.Join(homePath, mailCacheDirName)
	if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || livePairIDs[e.Name()] {
				continue
			}
			if err := removeMailCacheSubtree(homePath, e.Name()); err != nil {
				if _, cerr := a.mailCleanupIntentsOf().create(&mailCleanupIntent{PairID: e.Name()}); cerr != nil {
					slog.Warn("mail reconciliation: orphan cache subtree pending, intent could not be created", "code", mailCleanupFailedCache)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("mail reconciliation: cache directory unreadable")
	}
}

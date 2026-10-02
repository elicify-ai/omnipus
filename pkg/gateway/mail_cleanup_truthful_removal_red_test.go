package gateway

// RED — w5-integration claim 3: removal cascades are truthful — an
// incomplete cleanup reports removed_cleanup_pending and never a false
// success; the authorized retry works after the config row is gone and
// survives a FAILED retry; a late completion cannot recreate a removed
// pair's cache or watcher file.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-5.2/B-18/MC-8: "the cascade distinguishes full `removed`
//     from `removed_cleanup_pending` with a safe cleanup code"; a faulted
//     cache-subtree OR watcher-state unlink is pending, "never a
//     successful purge"; the separately authorized Retry works after the
//     config row is gone.
//   - w5 spec US-5.3: on an unlink failure "the pair remains
//     disabled/tombstoned, pending cleanup is visible/retryable" — a
//     failed retry must leave the intent addressable for the next retry.
//   - w5 spec US-5.4/B-19/MC-9: "Given cache or watcher-state work in
//     flight at removal, When its late completion is released, Then no
//     removed-pair file is recreated"; §4.2: "rely on sleeps to prevent
//     resurrection" is forbidden — the identity/publication guard is the
//     mechanism, so the persisted epoch (and with it the generation) must
//     advance before the purge (US-5.7: "the persisted config epoch
//     advances before older work can publish").
//
// Mutations this pack must kill (check-integration-report.md §2):
//   - M6: runMailboxRemovalCascade reports `removed` even when cleanup is
//     pending (len(pending) >= 0).
//   - M7: unlink errors swallowed (removePrivateFile returns nil always).
//   - M8: epoch advance skipped — old work can publish after removal.
//   - M14: retry intent consumed on take — a failed retry loses the intent.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// removalFixture pair IDs are unique per test on purpose: the identity memo
// is keyed by (agent, workspace) alone, so shared fixture names could pick
// up another test's memoized identity.
type removalFixture struct {
	home     string
	agentID  string
	wsID     string
	pairID   string
	cacheDir string
	watch    string
}

func newRemovalFixture(t *testing.T, agentID, wsID string) *removalFixture {
	t.Helper()
	home := t.TempDir()
	ident, err := config.LoadOrMintMailPairIdentity(home, agentID, wsID)
	require.NoError(t, err)
	f := &removalFixture{
		home:     home,
		agentID:  agentID,
		wsID:     wsID,
		pairID:   ident.PairID,
		cacheDir: filepath.Join(home, "mail-cache", ident.PairID),
		watch:    filepath.Join(home, "email-watch", mailWatcherStateFileName(agentID, wsID)),
	}
	require.NoError(t, os.MkdirAll(f.cacheDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.cacheDir, "folders.enc"), []byte("ciphertext"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(f.watch), 0o700))
	require.NoError(t, os.WriteFile(f.watch, []byte("{}"), 0o600))
	return f
}

func (f *removalFixture) faultCacheSubtree(t *testing.T) {
	t.Helper()
	// A symlink at the pair directory is refused by the purge's private-path
	// rule (US-3.4) — a deterministic, portable unlink failure for the
	// cache target.
	require.NoError(t, os.RemoveAll(f.cacheDir))
	require.NoError(t, os.Symlink(t.TempDir(), f.cacheDir))
}

func (f *removalFixture) faultWatchFile(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Remove(f.watch))
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "elsewhere.json"), f.watch))
}

func TestMailboxRemoval_FailedCacheUnlinkIsCleanupPending(t *testing.T) {
	f := newRemovalFixture(t, "rem-cache-agent", "ws-rem1")
	f.faultCacheSubtree(t)

	api := &restAPI{homePath: f.home}
	outcome, intent, code := api.runMailboxRemovalCascade(f.agentID, f.wsID)

	if outcome != "removed_cleanup_pending" {
		t.Fatalf("MC-8/US-5.2: a faulted cache unlink must report removed_cleanup_pending, got %q (a failed cleanup is NEVER a successful purge)", outcome)
	}
	if intent == "" {
		t.Fatal("MC-8: a pending cleanup must carry an opaque retry intent")
	}
	if code != "cache_cleanup_failed" {
		t.Fatalf("MC-8: the safe cleanup code must name the failed target (cache_cleanup_failed), got %q", code)
	}
}

func TestMailboxRemoval_FailedWatcherUnlinkIsCleanupPending(t *testing.T) {
	f := newRemovalFixture(t, "rem-watch-agent", "ws-rem2")
	f.faultWatchFile(t)

	api := &restAPI{homePath: f.home}
	outcome, intent, code := api.runMailboxRemovalCascade(f.agentID, f.wsID)

	if outcome != "removed_cleanup_pending" {
		t.Fatalf("MC-8/US-5.2: a faulted watcher-state unlink must report removed_cleanup_pending, got %q (founder Q-B=A: the watcher state is purged on removal — a failure is pending, never success)", outcome)
	}
	if intent == "" {
		t.Fatal("MC-8: a pending cleanup must carry an opaque retry intent")
	}
	if code != "watcher_cleanup_failed" {
		t.Fatalf("MC-8: the safe cleanup code must name the failed target (watcher_cleanup_failed), got %q", code)
	}
}

func TestMailboxRemoval_FullPurgeReportsRemoved(t *testing.T) {
	// The B-17 control: with both targets removable, the outcome is full
	// `removed`, both private targets are gone, and the identity record is
	// deleted with the pair (MC-24: identity state is deleted only by the
	// removal cascade).
	f := newRemovalFixture(t, "rem-ok-agent", "ws-rem3")

	api := &restAPI{homePath: f.home}
	outcome, intent, code := api.runMailboxRemovalCascade(f.agentID, f.wsID)

	if outcome != "removed" {
		t.Fatalf("B-17 control: a clean removal must report removed, got %q (intent=%q code=%q)", outcome, intent, code)
	}
	if _, err := os.Stat(f.cacheDir); !os.IsNotExist(err) {
		t.Fatalf("B-17: the cache subtree must be gone after a removed outcome (stat err=%v)", err)
	}
	if _, err := os.Stat(f.watch); !os.IsNotExist(err) {
		t.Fatalf("B-17/MC-23: the watcher state file must be gone after a removed outcome (stat err=%v)", err)
	}
	// MC-24: identity state is deleted ONLY by the removal cascade. Read the
	// listing (never LoadOrMint — a re-mint would recreate what the cascade
	// must have removed) and require this pair's record to be gone.
	idents, _ := config.ListMailPairIdentities(f.home)
	for _, id := range idents {
		if id.AgentID == f.agentID && id.WorkspaceID == f.wsID {
			t.Fatalf("MC-24: the pair's identity record must be deleted by the removal cascade (still present: pair_id=%s)", id.PairID)
		}
	}
}

func TestMailboxDisable_EpochAdvancesBeforePurge(t *testing.T) {
	// US-5.1/B-42/US-5.7: the disable cascade uses the SAME guarded
	// invalidation ordering — the persisted config epoch advances BEFORE
	// older work can publish. The disable path (unlike removal) KEEPS the
	// identity record, so the advance is observable: the generation is the
	// fingerprint of canonical identity + persisted epoch, so a skipped
	// advance leaves the generation unchanged and an old in-flight write
	// would still publish as current.
	f := newRemovalFixture(t, "rem-epoch-agent", "ws-rem4")
	mb := config.MailboxConfig{
		Enabled: true, WorkspaceID: f.wsID,
		IMAPHost: "imap.old.test", IMAPPort: 993, Username: "old@test.local",
	}
	identBefore, err := config.LoadOrMintMailPairIdentity(f.home, f.agentID, f.wsID)
	require.NoError(t, err)
	genBefore := config.MailPairGeneration(mb, f.agentID, f.wsID, identBefore)

	api := &restAPI{homePath: f.home}
	api.runMailboxDisableCascade(f.agentID, f.wsID)

	identAfter, err := config.LoadOrMintMailPairIdentity(f.home, f.agentID, f.wsID)
	require.NoError(t, err, "a disable keeps the identity record (the subtree name stays stable across disable→enable)")
	genAfter := config.MailPairGeneration(mb, f.agentID, f.wsID, identAfter)

	if identAfter.Epoch <= identBefore.Epoch {
		t.Fatalf("US-5.7/MC-9: the persisted config epoch must advance on the disable cascade (before=%d after=%d) — without the advance, in-flight work captured under the old generation can still publish as current", identBefore.Epoch, identAfter.Epoch)
	}
	if genAfter == genBefore {
		t.Fatal("US-5.7/MC-10: the pair's generation must change so old-generation work is rejected, not silently accepted")
	}
	if _, err := os.Stat(f.cacheDir); !os.IsNotExist(err) {
		t.Fatalf("US-5.1: the disable cascade purges the disposable cache (stat err=%v)", err)
	}
	if _, err := os.Stat(f.watch); !os.IsNotExist(err) {
		t.Fatalf("US-5.1/MC-23: the disable cascade purges the watcher state (stat err=%v)", err)
	}
}

func TestMailCleanupIntent_FailedRetryKeepsIntentAddressable(t *testing.T) {
	// US-5.3: pending cleanup stays "visible/retryable" — a retry that fails
	// again must keep the SAME intent addressable, or the second failure
	// silently strands the pending cleanup. Kills M14.
	store := newMailCleanupIntentStore()
	token, err := store.create(&mailCleanupIntent{
		AgentID: "rem-intent-agent", WorkspaceID: "ws-rem5",
		Pending: []string{mailCleanupFailedCache},
	})
	require.NoError(t, err)

	in, ok := store.take(token)
	require.True(t, ok, "the intent must be addressable after create")
	require.Equal(t, "rem-intent-agent", in.AgentID)

	// The retry ran and failed again: the handler refreshes the pending list
	// and the intent must still be takeable for the NEXT retry.
	store.refresh(token, []string{mailCleanupFailedCache})
	_, ok = store.take(token)
	if !ok {
		t.Fatal("US-5.3/MC-8: a FAILED retry consumed the cleanup intent — pending cleanup must stay visible/retryable until it fully completes")
	}

	// Only a completing retry drops it (the generated contract's refused
	// cases: unknown/expired/completed are then not found).
	store.complete(token)
	_, ok = store.take(token)
	require.False(t, ok, "a completed intent must be dropped")
}

func TestMailboxRemoval_RetryAfterConfigRowGoneCompletesTruthfully(t *testing.T) {
	// US-5.2/B-18: "separately authorized Retry cleanup works after config
	// deletion through opaque cleanup intent". The config row is ALREADY
	// gone in this fixture (the cascade is invoked pair-addressed, exactly
	// as the cleanup endpoint's pair branch re-runs it).
	f := newRemovalFixture(t, "rem-retry-agent", "ws-rem6")
	f.faultCacheSubtree(t)

	api := &restAPI{homePath: f.home}
	outcome, intent, code := api.runMailboxRemovalCascade(f.agentID, f.wsID)
	require.Equal(t, "removed_cleanup_pending", outcome)
	require.NotEmpty(t, intent)
	require.Equal(t, "cache_cleanup_failed", code)

	// The fault clears (operator or reboot fixed the symlink); the retry
	// re-runs the same cascade steps pair-addressed and must complete BOTH
	// purges truthfully — completing the intent, not merely claiming so.
	require.NoError(t, os.Remove(f.cacheDir))
	require.NoError(t, os.MkdirAll(f.cacheDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.cacheDir, "folders.enc"), []byte("ciphertext"), 0o600))

	pending := api.mailRemovalCascadeSteps(f.agentID, f.wsID)
	if len(pending) != 0 {
		t.Fatalf("B-18: the retry after the config row is gone must complete both purges, got pending=%v", pending)
	}
	api.mailCleanupIntentsOf().complete(intent)
	if _, ok := api.mailCleanupIntentsOf().take(intent); ok {
		t.Fatal("B-18: a completed retry must drop the intent (it is no longer retryable)")
	}
	// Truthfulness of the completed retry: "retry now completes both purges
	// truthfully" (B-18). A cleared pending with the protected state still
	// on disk is a successful purge claimed for state that remains (MC-8).
	if _, err := os.Stat(f.cacheDir); err == nil {
		t.Fatalf("B-18/MC-8: the retry reports the cleanup complete while the removed pair's cache subtree still exists at %s — the purge did not complete", f.cacheDir)
	}
	if _, err := os.Stat(f.watch); !os.IsNotExist(err) {
		t.Fatalf("B-18/MC-23: the watcher state file must be gone after the retry (stat err=%v)", err)
	}
}

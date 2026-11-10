package config

// RED — w5-integration claim 8 (the audit's 14th surviving mutant, US-8):
// the pair ID is a RANDOMLY MINTED 128-bit value persisted beside the pair
// config — never a derivation of the pair's own names.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-3.2/MC-17: "each has a randomly minted 128-bit ID stored
//     beside its configuration, stable across normal restart and credential
//     rotation; no derived credential value, lossy name, email address or
//     folder name identifies the subtree."
//   - w5 spec X-13: "Separate consumers derive different/credential-based
//     generations, or password rotation changes the path ID" is a mandatory
//     kill.
//
// Mutation this pack must kill (check-integration-report.md §2, M15):
// LoadOrMintMailPairIdentity derives the pair ID (SHA-256 of
// agent/workspace) instead of minting random 128 bits. A derived ID is
// identical on every fresh install of the same pair — the discriminating
// property is randomness across independent mints, which no stability
// property can substitute for.

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// resetIdentityMemo clears the process-wide memo so a test observes the
// DISK truth (a fresh mint), not another test's loaded entry. The memo is
// keyed by (agent, workspace) alone, so pair names cannot isolate tests.
func resetIdentityMemo() {
	mailIdentityMu.Lock()
	mailIdentityMemo = map[string]MailPairIdentity{}
	mailIdentityMu.Unlock()
}

func TestMailPairIdentity_RandomMintNotDerivedFromPairNames(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()

	resetIdentityMemo()
	idA, err := LoadOrMintMailPairIdentity(rootA, "same-agent", "same-ws")
	require.NoError(t, err)

	resetIdentityMemo() // a second, independent process would hold no memo either
	idB, err := LoadOrMintMailPairIdentity(rootB, "same-agent", "same-ws")
	require.NoError(t, err)

	if idA.PairID == idB.PairID {
		t.Fatalf("US-3.2/MC-17: two independent mints of the SAME pair produced the SAME pair ID %q — the ID is derived from the pair's names (lossy/deterministic), not randomly minted; cache isolation between installs is broken", idA.PairID)
	}
}

func TestMailPairIdentity_Is128Bits(t *testing.T) {
	root := t.TempDir()
	resetIdentityMemo()
	id, err := LoadOrMintMailPairIdentity(root, "bits-agent", "ws-bits")
	require.NoError(t, err)

	raw, err := hex.DecodeString(id.PairID)
	require.NoError(t, err, "the pair ID is 32 lowercase hex chars (the rendered form of 128 bits)")
	if len(raw) != 16 {
		t.Fatalf("US-3.2/MC-17: the pair ID must be 128 bits of material, got %d bytes (%q)", len(raw), id.PairID)
	}
}

func TestMailPairIdentity_StableAcrossRestart(t *testing.T) {
	// The "Or" half of LoadOrMint: once minted and persisted, a later
	// process (memo empty) must load the SAME identity — stability across
	// normal restart (US-3.2). Control for the randomness test: this is the
	// property a persistence regression would break.
	root := t.TempDir()
	resetIdentityMemo()
	minted, err := LoadOrMintMailPairIdentity(root, "stable-agent", "ws-stable")
	require.NoError(t, err)

	resetIdentityMemo()
	reloaded, err := LoadOrMintMailPairIdentity(root, "stable-agent", "ws-stable")
	require.NoError(t, err)

	if reloaded.PairID != minted.PairID {
		t.Fatalf("US-3.2: the pair ID must be stable across a restart (memo lost, disk authoritative); minted %q, reloaded %q", minted.PairID, reloaded.PairID)
	}
	if reloaded.Epoch != minted.Epoch || reloaded.Revision != minted.Revision {
		t.Fatalf("US-8.2: an unchanged restart must not advance epoch/revision (epoch %d→%d, revision %d→%d)", minted.Epoch, reloaded.Epoch, minted.Revision, reloaded.Revision)
	}
}

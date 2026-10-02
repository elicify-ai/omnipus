package config

// mail_identity.go — durable per-pair Mail identity state (w5-integration
// wave; ADR-20261001 "Mail live access"; landing-order register rows 10/12;
// w5 spec US-8/MC-17/MC-24).
//
// Three facts about one (agent, workspace) mailbox pair live here, persisted
// in the product's own state BESIDE the pair config (config.json holds the
// mailbox row; this sidecar holds its identity), one JSON file per pair:
//
//   - PairID: a randomly minted 128-bit value, stable across normal restart
//     AND password rotation. It names the pair's private cache subtree
//     (<dataRoot>/mail-cache/<pairID>/) and is never derived from
//     credentials, lossy names or the email address (register row 12's
//     settlement; US-3.2).
//   - Epoch: the persisted config epoch. Every relevant mailbox save —
//     including a same-value re-save — advances it BEFORE older work can
//     publish, so the generation changes and old snapshots/work are
//     rejected (US-5.7, US-8.3, MC-10).
//   - Revision: the per-pair publication-revision counter seed (MC-24,
//     R2-IMP-1). A single per-pair counter seeds every folder revision of
//     the pair; it is persisted on every advance and therefore NEVER
//     restarts low after a gateway restart — the first post-restart value
//     orders newer than every pre-restart value consumers still hold.
//
// The generation itself is the non-secret fingerprint of the pair's
// canonical identity PLUS the persisted epoch (MailPairGeneration). It
// contains no credential material: PasswordRef is a reference key, not a
// secret, and the resolved password never enters any computation here.
//
// Concurrency: one process-wide memo (guarded by mailIdentityMu) prevents
// concurrent first-loads from minting two competing identities for the same
// pair; the file on disk stays the source of truth across processes, and
// every write is atomic (fileutil.WriteFileAtomic, 0600 under a 0700
// directory).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// MailPairIdentity is one pair's durable identity state (see the file
// comment). Serialized as JSON beside the pair config.
type MailPairIdentity struct {
	// PairID is the randomly minted 128-bit subtree identity: 32 lowercase
	// hex chars. Opaque to every consumer; never carries meaning.
	PairID string `json:"pair_id"`
	// Epoch is the persisted config epoch (>=1). Advanced on every relevant
	// mailbox save, including a same-value re-save.
	Epoch uint64 `json:"config_epoch"`
	// Revision is the per-pair publication-revision counter seed (>=1 for a
	// freshly initialized pair).
	Revision uint64 `json:"revision_counter"`
}

// mailIdentityDirName is the sidecar namespace under the data root. Not a
// cache namespace (nothing sensitive is stored here — a random ID and two
// counters), so it stays eligible for ordinary state backup on purpose:
// a restored data root keeps its pairs' identities stable.
const mailIdentityDirName = "mail-identity"

var (
	mailIdentityMu sync.Mutex
	// mailIdentityMemo caches loaded-or-minted identities per pair so
	// concurrent first reads cannot race two mints into two files. The disk
	// file remains authoritative: every advance rewrites it before the memo
	// is updated.
	mailIdentityMemo = map[string]MailPairIdentity{}
)

// mailPairIdentityKey is the memo key (not a wire or disk format).
func mailPairIdentityKey(agentID, workspaceID string) string {
	return agentID + "\x00" + workspaceID
}

// MailPairIdentityPath returns the sidecar file path for one pair. The file
// name is derived from the pair's IDs with a collision-proof hash suffix:
// the readable prefix is an operator-aid only, the suffix is what makes the
// name unambiguous after sanitization, and neither carries meaning a cache
// subtree could leak.
func MailPairIdentityPath(dataRoot, agentID, workspaceID string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return "", errors.New("mail identity: data root is empty")
	}
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(workspaceID) == "" {
		return "", errors.New("mail identity: agent and workspace IDs are required")
	}
	sum := sha256.Sum256([]byte(agentID + "\x00" + workspaceID))
	name := mailIdentityFileStem(agentID) + "-" + mailIdentityFileStem(workspaceID) + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(dataRoot, mailIdentityDirName, name+".json"), nil
}

// mailIdentityFileStem reduces an ID to filename-safe characters, bounded so
// pathological IDs cannot produce unbounded names; the hash suffix added by
// MailPairIdentityPath keeps distinct pairs distinct.
func mailIdentityFileStem(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "pair"
	}
	return b.String()
}

// LoadOrMintMailPairIdentity returns the pair's identity, minting and
// persisting it on first use (random 128-bit PairID, epoch 1, revision 1).
// A later call with unchanged state returns the SAME identity — the
// stability US-3.2/US-8.2 require across restart and password rotation.
func LoadOrMintMailPairIdentity(dataRoot, agentID, workspaceID string) (MailPairIdentity, error) {
	path, err := MailPairIdentityPath(dataRoot, agentID, workspaceID)
	if err != nil {
		return MailPairIdentity{}, err
	}
	key := mailPairIdentityKey(agentID, workspaceID)
	mailIdentityMu.Lock()
	defer mailIdentityMu.Unlock()
	if id, ok := mailIdentityMemo[key]; ok {
		return id, nil
	}
	if id, err := readMailIdentityFile(path); err == nil {
		mailIdentityMemo[key] = id
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// A present-but-unreadable identity file must never be silently
		// replaced by a fresh mint: a re-minted PairID would orphan the
		// pair's cache subtree and a reset epoch could let old work look
		// current. Surface the failure (US-8.4's honest store-state rule).
		return MailPairIdentity{}, fmt.Errorf("mail identity: read %s: %w", mailIdentityDirName, err)
	}
	id := MailPairIdentity{}
	buf := make([]byte, 16) // 128 bits
	if n, rerr := rand.Read(buf); rerr != nil || n != len(buf) {
		return MailPairIdentity{}, fmt.Errorf("mail identity: entropy source failed: %w", rerr)
	}
	id.PairID = hex.EncodeToString(buf)
	id.Epoch = 1
	id.Revision = 1
	if err := writeMailIdentityFile(path, id); err != nil {
		return MailPairIdentity{}, err
	}
	mailIdentityMemo[key] = id
	return id, nil
}

// AdvanceMailPairEpoch advances the persisted config epoch and returns the
// updated identity. Callers run it BEFORE older work can publish (the
// save-path ordering US-5.7/MC-10 require), so an in-flight read captured
// under the old generation can no longer publish as current.
func AdvanceMailPairEpoch(dataRoot, agentID, workspaceID string) (MailPairIdentity, error) {
	id, err := LoadOrMintMailPairIdentity(dataRoot, agentID, workspaceID)
	if err != nil {
		return MailPairIdentity{}, err
	}
	id.Epoch++
	return persistMailIdentity(dataRoot, agentID, workspaceID, id)
}

// AdvanceMailPairRevision advances the per-pair publication-revision
// counter and returns the NEW value. The advance is persisted before the
// value is returned, so a gateway restart can never re-issue a value at or
// below one already handed out (MC-24's never-restarts-low rule).
func AdvanceMailPairRevision(dataRoot, agentID, workspaceID string) (uint64, error) {
	id, err := LoadOrMintMailPairIdentity(dataRoot, agentID, workspaceID)
	if err != nil {
		return 0, err
	}
	id.Revision++
	persisted, err := persistMailIdentity(dataRoot, agentID, workspaceID, id)
	if err != nil {
		return 0, err
	}
	return persisted.Revision, nil
}

// CurrentMailPairRevision returns the pair's current publication revision
// without advancing it — the value a read captures before server work.
func CurrentMailPairRevision(dataRoot, agentID, workspaceID string) (uint64, error) {
	id, err := LoadOrMintMailPairIdentity(dataRoot, agentID, workspaceID)
	if err != nil {
		return 0, err
	}
	return id.Revision, nil
}

// DeleteMailPairIdentity removes the pair's identity state. Only the removal
// cascade calls this (MC-24: identity state is deleted only by removal); a
// missing file is already-clean, not an error.
func DeleteMailPairIdentity(dataRoot, agentID, workspaceID string) error {
	path, err := MailPairIdentityPath(dataRoot, agentID, workspaceID)
	if err != nil {
		return err
	}
	mailIdentityMu.Lock()
	delete(mailIdentityMemo, mailPairIdentityKey(agentID, workspaceID))
	mailIdentityMu.Unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mail identity: delete: %w", err)
	}
	return nil
}

// persistMailIdentity writes the updated identity atomically and refreshes
// the memo only after the write succeeded.
func persistMailIdentity(dataRoot, agentID, workspaceID string, id MailPairIdentity) (MailPairIdentity, error) {
	path, err := MailPairIdentityPath(dataRoot, agentID, workspaceID)
	if err != nil {
		return MailPairIdentity{}, err
	}
	if err := writeMailIdentityFile(path, id); err != nil {
		return MailPairIdentity{}, err
	}
	mailIdentityMu.Lock()
	mailIdentityMemo[mailPairIdentityKey(agentID, workspaceID)] = id
	mailIdentityMu.Unlock()
	return id, nil
}

func readMailIdentityFile(path string) (MailPairIdentity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return MailPairIdentity{}, err
	}
	var id MailPairIdentity
	if err := json.Unmarshal(b, &id); err != nil {
		return MailPairIdentity{}, fmt.Errorf("mail identity: malformed identity file: %w", err)
	}
	if id.PairID == "" || id.Epoch == 0 || id.Revision == 0 {
		return MailPairIdentity{}, errors.New("mail identity: incomplete identity file")
	}
	return id, nil
}

func writeMailIdentityFile(path string, id MailPairIdentity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mail identity: create directory: %w", err)
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("mail identity: encode: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, b, 0o600); err != nil {
		return fmt.Errorf("mail identity: persist: %w", err)
	}
	return nil
}

// MailPairGeneration derives the pair's non-secret configuration generation:
// a fingerprint of the canonical identity (the authorized pair, the
// configured endpoint, the username, the credential-REFERENCE binding and
// the folder overrides) plus the persisted config epoch. Every consumer —
// pool identity, budget flight identity, cache scope — receives THIS one
// value and treats it as opaque (US-8.1, MC-17). No resolved credential
// material ever enters the input.
func MailPairGeneration(mb MailboxConfig, agentID, workspaceID string, id MailPairIdentity) string {
	canonical := strings.Join([]string{
		"v1",
		agentID,
		workspaceID,
		mb.IMAPHost,
		fmt.Sprint(mb.IMAPPort),
		mb.SMTPHost,
		fmt.Sprint(mb.SMTPPort),
		mb.Username,
		mb.PasswordRef,
		mb.SentFolderName,
		mb.DraftsFolderName,
		fmt.Sprint(id.Epoch),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:16])
}

// RenderMailRevision renders a publication revision for the wire and for
// byte-wise comparison: fixed-width zero-padded decimal (u64, 20 digits),
// so lexicographic order IS numeric order (R2-IMP-1). Consumers compare
// byte-wise or for equality; none parses the value.
func RenderMailRevision(n uint64) string {
	return fmt.Sprintf("%020d", n)
}

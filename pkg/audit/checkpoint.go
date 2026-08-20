// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

// checkpoint.go — persisted HMAC chain checkpoint (retention/cleanup fix).
//
// Bug this file fixes: retention cleanup (Logger.cleanupExpired, audit.go)
// deletes expired rotated `audit-*.jsonl` files with no record of what chain
// state they carried. VerifyDir (verify.go) always seeds its walk from
// GenesisSeed(), which is only correct while the ORIGINAL first file is still
// on disk. Once cleanup has deleted that file, the new-oldest surviving
// file's first entry was never meant to chain from genesis — its `prev_hmac`
// was computed against the last entry of a file that no longer exists. The
// result: a false-positive "hmac mismatch" on the very first entry VerifyDir
// looks at, and because VerifyFile/VerifyDir stop at the first break, every
// newer (and perfectly intact) file after that point is silently never
// checked at all.
//
// Fix: cleanupExpired persists a small signed sidecar — chainCheckpoint — the
// moment a deletion would sever the chain from genesis. It records which
// file is now the oldest survivor and the HMAC that survivor's first entry
// should be checked against (the last good link of whatever was superseded).
// VerifyDir consults this checkpoint instead of GenesisSeed() when the
// checkpoint's target matches what's actually on disk.
//
// Threat model for the checkpoint itself: an attacker who can write to the
// audit directory but does NOT have the chain key could otherwise drop a
// forged checkpoint file naming an arbitrary FinalHMAC — engineered so that
// the first entry of a file THEY tampered with recomputes to match it,
// laundering the tamper as "seeded from a legitimate checkpoint". To close
// that gap the checkpoint carries its own `sum` field: HMAC-SHA256 over the
// checkpoint's own fields, keyed with the SAME chain key used for audit
// entries, under a distinct domain-separation tag (checkpointHMACInfo) so a
// checkpoint sum can never be confused with (or replayed as) an entry-chain
// HMAC. Without the key, a forged checkpoint's sum will not verify and
// VerifyDir refuses to trust it.
//
// File naming: `audit-chain-checkpoint.json` lives alongside `audit.jsonl`
// and the rotated `audit-YYYY-MM-DD.jsonl` files, following the existing
// `<name>.meta.json` sidecar convention used by pkg/session's retention
// sweep (metaPath := TrimSuffix(path, ".jsonl") + ".meta.json"). The `.json`
// (not `.jsonl`) extension keeps it out of the `audit-*.jsonl` glob used by
// both cleanupExpired and VerifyDir's file walk, so it is never mistaken for
// a rotated log or deleted/verified as one.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// errNoCheckpoint is the sentinel error readChainCheckpoint returns when no
// checkpoint sidecar exists on disk yet. It is distinct from any other
// error readChainCheckpoint can return (parse failure, integrity-check
// failure): callers use errors.Is against this sentinel to tell "nothing
// written yet" (benign, silent genesis-seed fallback) apart from "a
// checkpoint exists but cannot be trusted" (loggable, fail-closed
// genesis-seed fallback). See readChainCheckpoint's doc comment.
var errNoCheckpoint = errors.New("audit: no chain checkpoint")

// checkpointFileName is the sidecar file that persists the HMAC chain
// checkpoint. See this file's doc comment above for the naming rationale.
const checkpointFileName = "audit-chain-checkpoint.json"

// checkpointHMACInfo domain-separates the checkpoint's integrity HMAC from
// the per-entry chain HMAC (computeEntryHMAC) and from GenesisSeed, even
// though all three may be derived from/keyed by material that traces back to
// the same master key. Mirrors the naming style of AuditChainKeyInfo /
// "omnipus-audit-genesis-v1".
const checkpointHMACInfo = "omnipus-audit-checkpoint-v1"

// chainCheckpoint is the on-disk (JSON) shape of the persisted chain-seed
// checkpoint written by Logger.cleanupExpired whenever retention cleanup
// deletes the file that was, until then, needed to seed genesis-based
// verification.
type chainCheckpoint struct {
	// AppliesToFile is the base name (not full path) of the file that is now
	// the oldest surviving audit file as of this checkpoint — i.e. the file
	// whose FIRST entry should be verified against FinalHMAC instead of
	// GenesisSeed(). VerifyDir only trusts this checkpoint when AppliesToFile
	// matches the actual oldest file currently on disk; a mismatch means the
	// checkpoint is stale (e.g. files were deleted manually outside the
	// normal cleanupExpired path, or this checkpoint is left over from a
	// different retention run) and is NOT used as a seed (see verify.go).
	AppliesToFile string `json:"applies_to_file"`
	// FinalHMAC is the hex-encoded (64 chars / 32 bytes) HMAC of the last
	// entry of the file that was superseded — the exact same value that
	// would have been threaded as the cross-file chain seed had that file
	// still been on disk (see rotate() in audit.go for the equivalent
	// same-process case).
	FinalHMAC string `json:"final_hmac"`
	// CreatedAt is informational only (operator debugging / audit-of-the-
	// audit-system); it is NOT part of the signed payload — see
	// computeCheckpointSum — so clock skew or JSON time round-tripping can
	// never invalidate an otherwise-legitimate checkpoint.
	CreatedAt time.Time `json:"created_at"`
	// Sum is the hex-encoded (64 chars / 32 bytes) integrity HMAC over
	// (AppliesToFile, FinalHMAC), keyed with the same audit chain key used
	// for entry HMACs. Without the chain key, a forged checkpoint cannot
	// produce a Sum that verifies — see this file's doc comment for the
	// threat model.
	Sum string `json:"sum"`
}

// checkpointPath returns the full path to the checkpoint sidecar for the
// given audit directory.
func checkpointPath(dir string) string {
	return filepath.Join(dir, checkpointFileName)
}

// computeCheckpointSum computes the integrity HMAC for a checkpoint's
// (appliesToFile, finalHMAC) pair. A 0x00 separator between fields prevents
// the classic HMAC field-concatenation ambiguity (e.g. "ab"+"c" colliding
// with "a"+"bc") even though appliesToFile (a filename) and finalHMAC (fixed
// 32 bytes) are not realistically confusable in practice — cheap to do
// right.
func computeCheckpointSum(appliesToFile string, finalHMAC []byte, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(checkpointHMACInfo))
	mac.Write([]byte{0})
	mac.Write([]byte(appliesToFile))
	mac.Write([]byte{0})
	mac.Write(finalHMAC)
	return mac.Sum(nil)
}

// writeChainCheckpoint persists (atomically, via temp-file + rename) a
// chainCheckpoint recording that appliesToFile is now the oldest surviving
// audit file and finalHMAC is the seed its first entry should be verified
// against. Called from Logger.cleanupExpired immediately after a deletion
// that supersedes the previous chain-verification starting point.
func writeChainCheckpoint(dir, appliesToFile string, finalHMAC, key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("audit: writeChainCheckpoint requires a non-empty chain key")
	}
	if len(finalHMAC) != 32 {
		return fmt.Errorf("audit: writeChainCheckpoint requires a 32-byte finalHMAC, got %d bytes", len(finalHMAC))
	}
	if appliesToFile == "" {
		return fmt.Errorf("audit: writeChainCheckpoint requires a non-empty appliesToFile")
	}

	sum := computeCheckpointSum(appliesToFile, finalHMAC, key)
	cp := chainCheckpoint{
		AppliesToFile: appliesToFile,
		FinalHMAC:     hex.EncodeToString(finalHMAC),
		CreatedAt:     time.Now().UTC(),
		Sum:           hex.EncodeToString(sum),
	}

	data, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return fmt.Errorf("audit: marshal chain checkpoint: %w", err)
	}

	path := checkpointPath(dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("audit: write chain checkpoint temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("audit: rename chain checkpoint into place: %w", err)
	}
	return nil
}

// readChainCheckpoint reads and integrity-verifies the checkpoint sidecar
// for dir.
//
// Return contract:
//   - (nil, errNoCheckpoint): no checkpoint file exists. This is the common
//     case — either no cleanup has ever deleted a file, or retention hasn't
//     kicked in yet. Callers must fall back to genesis-seeding (today's
//     behavior), and should do so silently (errors.Is(err, errNoCheckpoint))
//     since this is not an anomaly.
//   - (nil, err) with err not errNoCheckpoint: the file exists but failed to
//     parse OR failed its integrity check (Sum mismatch). Per the threat
//     model in this file's doc comment, a Sum mismatch means either disk
//     corruption or a forged checkpoint attempting to redirect the chain
//     seed — the checkpoint MUST NOT be trusted. Callers fall back to
//     genesis-seeding (fail closed: worst case is a false "chain broken"
//     alarm investigable by an operator, never a silent bypass of real
//     tampering).
//   - (cp, nil): the checkpoint parsed and its Sum verified against key.
//     Callers still must confirm cp.AppliesToFile matches the actual oldest
//     surviving file before trusting cp.FinalHMAC as a seed — see VerifyDir.
func readChainCheckpoint(dir string, key []byte) (*chainCheckpoint, error) {
	path := checkpointPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errNoCheckpoint
		}
		return nil, fmt.Errorf("audit: read chain checkpoint: %w", err)
	}

	var cp chainCheckpoint
	if err = json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("audit: parse chain checkpoint: %w", err)
	}

	finalMAC, err := hex.DecodeString(cp.FinalHMAC)
	if err != nil || len(finalMAC) != 32 {
		return nil, fmt.Errorf("audit: chain checkpoint final_hmac is not 32-byte hex")
	}
	wantSum, err := hex.DecodeString(cp.Sum)
	if err != nil || len(wantSum) != 32 {
		return nil, fmt.Errorf("audit: chain checkpoint sum is not 32-byte hex")
	}

	gotSum := computeCheckpointSum(cp.AppliesToFile, finalMAC, key)
	if !hmac.Equal(gotSum, wantSum) {
		return nil, fmt.Errorf(
			"audit: chain checkpoint integrity check failed (sum mismatch: corrupt file, wrong key, or forged checkpoint)",
		)
	}

	return &cp, nil
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package audit — HMAC-chain primitives for tamper-evident audit logs.
//
// Threat model (closes C2-AUDIT from the v0.2 / #155 pentest backlog):
//
//   Each audit entry carries a `hmac` field computed over
//      HMAC-SHA256(chainKey, prev_hmac || canonical_json_without_hmac)
//
//   The chain key is derived from the unlocked master key via HKDF-SHA256
//   (info = "omnipus-audit-chain-v1") and held only in process memory. The
//   key MUST NOT touch disk: an attacker with read access to ~/.omnipus
//   (but not the master key) can read audit entries and read the embedded
//   hmac strings, but cannot forge a new chain link without the key.
//
//   Threats this defends against:
//     1. Truncation: removing the trailing entry leaves the previous entry's
//        hmac intact, but Verify() walks every entry top-to-bottom and the
//        chain check still succeeds for the kept entries — truncation is
//        detectable only if the verifier records and compares the EXPECTED
//        terminator (e.g. a reference final-hmac stored elsewhere). We close
//        the partial-truncation gap by requiring that rotated files chain
//        into the next file's first entry: dropping the final entry of the
//        active file is detectable post-rotation, and dropping rotated files
//        breaks the chain seed at the next rotation.
//     2. Surgical rewrite: flipping decision="deny" → "allow" on entry N
//        changes entry N's content hash. Entry N+1's hmac was computed over
//        the ORIGINAL content of entry N, so the recomputed-from-rewrite
//        hash for entry N no longer matches what entry N+1 chains to. Verify
//        flags entry N+1 as the broken link.
//     3. Re-ordering: same as rewrite — entry N+1's hmac depends on entry
//        N's hmac, so any swap breaks the chain.
//
//   Threats this does NOT defend against (out of scope, documented for the
//   security review):
//     - Attacker with the master key: they can decrypt credentials.json and
//       forge any entry. The HMAC chain raises the bar from "byte-edit a
//       JSONL file" to "compromise the master key".
//     - Wholesale file deletion: Verify cannot tell the difference between
//       "log file does not exist" and "log file was deleted". Operators must
//       rely on filesystem-level monitoring (auditd, file integrity tools)
//       to detect the file disappearing entirely.
//     - Replay across rotation files: an attacker who renames/swaps a rotated
//       file can confuse the cross-file chain verifier. The mitigation is to
//       include the file's date suffix in the chain seed; however, the
//       initial v0.2 implementation chains rotation by walking files in
//       lexicographic order and checking each file's first entry's prev_hmac
//       against the previous file's last entry's hmac.

package audit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"

	"golang.org/x/crypto/hkdf"
)

// AuditChainKeyInfo is the HKDF info tag used to derive the audit-chain HMAC
// key from the master key. Bumping the suffix (v1 -> v2) forces a key
// rotation and breaks chain verification for all existing files — only do
// that as part of a documented key-rotation migration.
const AuditChainKeyInfo = "omnipus-audit-chain-v1"

// genesisSeed is the constant prepended to the first entry's HMAC computation
// in a fresh log file. Using a fixed sentinel rather than a zero buffer makes
// the genesis distinguishable from "I forgot to seed prev_hmac" bugs and
// gives tests a stable check point. Anchored to the package version string so
// it survives copy-paste into other Omnipus products.
//
// Computed once at init from sha256("omnipus-audit-genesis-v1").
var genesisSeed []byte

func init() {
	h := sha256.Sum256([]byte("omnipus-audit-genesis-v1"))
	genesisSeed = make([]byte, len(h))
	copy(genesisSeed, h[:])
}

// GenesisSeed returns a copy of the chain seed used for the first entry of
// a fresh audit log. Exposed for tests and the CLI verifier so they can
// independently start a chain walk from byte zero.
func GenesisSeed() []byte {
	out := make([]byte, len(genesisSeed))
	copy(out, genesisSeed)
	return out
}

// processChainKey is a package-level fallback chain key. The gateway sets it
// once at boot via SetProcessChainKey after the credential store is unlocked.
// audit.NewLogger consults this when LoggerConfig.HMACKey is nil — this lets
// the agent loop construct its audit logger without threading a key through
// the agent.NewAgentLoop signature.
//
// In test contexts where neither LoggerConfig.HMACKey nor processChainKey is
// set, NewLogger falls back to a deterministic dev-only key with a
// sticky-once slog.Warn so the gap is loud but the test still runs.
var (
	processChainKeyMu   sync.RWMutex
	processChainKey     []byte
	devChainKeyWarnOnce sync.Once
)

// SetProcessChainKey installs a process-wide audit-chain key. Idempotent;
// safe to call multiple times across hot reloads. Pass nil to clear (used by
// shutdown / tests).
//
// The key MUST be derived from the master key (or be of equivalent strength
// — 32 random bytes from a CSPRNG). Storing the master key directly here is
// a bug: every additional copy of the master key in process memory expands
// the surface for a memory-disclosure attack.
func SetProcessChainKey(key []byte) {
	processChainKeyMu.Lock()
	defer processChainKeyMu.Unlock()
	if key == nil {
		processChainKey = nil
		return
	}
	processChainKey = make([]byte, len(key))
	copy(processChainKey, key)
}

// getProcessChainKey returns a copy of the process-wide chain key, or nil.
func getProcessChainKey() []byte {
	processChainKeyMu.RLock()
	defer processChainKeyMu.RUnlock()
	if processChainKey == nil {
		return nil
	}
	out := make([]byte, len(processChainKey))
	copy(out, processChainKey)
	return out
}

// DeriveAuditKey derives the audit-chain HMAC key from a master key using
// HKDF-SHA256 with info = AuditChainKeyInfo. Returns 32 bytes.
//
// This helper exists for tests and direct callers. In production, the
// gateway uses credentials.Store.DeriveSubkey so the master key never
// crosses the credentials package boundary.
func DeriveAuditKey(masterKey []byte) ([]byte, error) {
	if len(masterKey) == 0 {
		return nil, fmt.Errorf("audit: DeriveAuditKey requires non-empty master key")
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte(AuditChainKeyInfo))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("audit: hkdf expand: %w", err)
	}
	return out, nil
}

// resolveChainKey picks the chain key for a Logger in the documented
// precedence order: LoggerConfig.HMACKey → processChainKey → dev fallback.
// The dev fallback is deterministic (sha256("omnipus-audit-dev-only-key"))
// so tests across the suite see consistent behavior, but emits a sticky
// slog.Warn the first time it fires so a misconfigured production deploy
// is loud.
func resolveChainKey(cfgKey []byte) []byte {
	if len(cfgKey) > 0 {
		out := make([]byte, len(cfgKey))
		copy(out, cfgKey)
		return out
	}
	if k := getProcessChainKey(); k != nil {
		return k
	}
	devChainKeyWarnOnce.Do(func() {
		slog.Warn("audit: HMAC chain key not configured, using insecure dev-only fallback "+
			"(set LoggerConfig.HMACKey or call audit.SetProcessChainKey at boot)",
			"info_tag", AuditChainKeyInfo)
	})
	h := sha256.Sum256([]byte("omnipus-audit-dev-only-key"))
	return h[:]
}

// computeEntryHMAC computes the chain HMAC for a single entry given the
// previous link's HMAC (or genesisSeed for the first entry) and the entry's
// canonicalized JSON bytes (with the `hmac` field already removed).
//
// Returns the 32-byte raw HMAC (not hex-encoded). Callers hex-encode for
// embedding in the JSONL line.
func computeEntryHMAC(prev []byte, canonical []byte, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	// prev || canonical: the order matters; swapping breaks chain verification.
	// We deliberately do NOT include a length prefix because canonical_json
	// always ends in '}' — the chain-link layer is unambiguous.
	mac.Write(prev)
	mac.Write(canonical)
	return mac.Sum(nil)
}

// canonicalJSONWithoutHMAC returns a canonical JSON encoding of the entry
// suitable for HMAC computation. It:
//
//  1. Unmarshals the input bytes into a generic map[string]any.
//  2. Removes the `hmac` key (must NOT be in the input to the HMAC).
//  3. Re-marshals with sorted keys at every level.
//
// We rely on a custom encoder because Go's encoding/json randomizes map
// iteration but DOES sort top-level map keys when marshaling map[string]any
// — however, the contract is documented only for sub-maps, not nested
// types. To be safe we walk the structure ourselves.
//
// Note: this is the canonicalisation used both at write time (Logger.writeLine
// computes the HMAC over canonical bytes BEFORE serializing the final line)
// and at verify time. They must agree byte-for-byte.
func canonicalJSONWithoutHMAC(raw []byte) ([]byte, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("audit: canonicalise: not a JSON object: %w", err)
	}
	// Remove the hmac field — we are computing what it WILL be.
	delete(m, "hmac")
	return canonicalMarshal(m)
}

// canonicalMarshal recursively marshals v with sorted map keys. Numbers,
// strings, bools, nulls are passed through Go's encoding/json which produces
// stable output for those primitive types. Only map ordering is non-stable
// in stdlib, so that's the only layer we override.
func canonicalMarshal(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		var buf bytes.Buffer
		buf.WriteByte('{')
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := canonicalMarshal(x[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			ib, err := canonicalMarshal(item)
			if err != nil {
				return nil, err
			}
			buf.Write(ib)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		return json.Marshal(v)
	}
}

// embedHMAC takes an already-marshaled entry (without `hmac` field), computes
// its HMAC against prev, and returns a NEW byte slice containing the entry
// with `hmac` field appended. The output is the line that gets written to
// audit.jsonl (sans trailing newline).
//
// Returns the embedded line, the raw HMAC bytes (so the Logger can update
// prevHMAC), and any error.
func embedHMAC(rawJSON []byte, prev []byte, key []byte) (line []byte, mac []byte, err error) {
	canonical, err := canonicalJSONWithoutHMAC(rawJSON)
	if err != nil {
		return nil, nil, err
	}
	mac = computeEntryHMAC(prev, canonical, key)
	hexMac := hex.EncodeToString(mac)

	// Inject the hmac field at the end of the original (non-canonical) bytes.
	// We re-parse to a map and re-marshal with the hmac field appended; this
	// preserves the existing field order for human-readable lines but adds
	// the new field. Verification will canonicalize both sides anyway.
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(rawJSON))
	dec.UseNumber()
	if decErr := dec.Decode(&m); decErr != nil {
		return nil, nil, fmt.Errorf("audit: embed hmac: %w", decErr)
	}
	m["hmac"] = hexMac
	out, err := json.Marshal(m)
	if err != nil {
		return nil, nil, fmt.Errorf("audit: marshal entry with hmac: %w", err)
	}
	return out, mac, nil
}

// Package minhash provides near-duplicate memory detection using MinHash
// shingling (FR-7.5 / M-5).
//
// Design:
//   - 128-permutation MinHash over 3-shingles (3 consecutive words).
//   - Jaccard similarity threshold: 0.85 (configurable via DefaultThreshold).
//   - Non-destructive: near-duplicates are LINKED in minhash.jsonl, never
//     deleted. The near-duplicate link is an append-only record so v0.2
//     can prune later if desired, without losing provenance.
//   - The minhash.jsonl lives at <room_root>/.index/minhash.jsonl (DERIVED).
//   - All writes are via fileutil.AppendJSONL (POSIX-safe sub-PIPE_BUF appends).
//   - Pure-Go: no CGo, no external C libs.
//
// Usage:
//
//	sig := minhash.Signature(text, 128)
//	// On write: compute sig of new memory, compare against known sigs in ring,
//	// if Jaccard(sig, existing) >= threshold → append NearDupRecord and return
//	// true (caller skips the actual write or logs a dedup notice).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package minhash

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

const (
	// DefaultNumPerm is the number of MinHash permutations (hash functions).
	// 128 gives ~1% Jaccard estimation error.
	DefaultNumPerm = 128

	// DefaultThreshold is the Jaccard similarity above which two memories are
	// considered near-duplicates.
	DefaultThreshold = 0.85

	// MinHashJSONLFile is the filename (relative to <room_root>/.index/) for
	// the near-duplicate link log. DERIVED — rebuilt if deleted.
	MinHashJSONLFile = "minhash.jsonl"
)

// Signature is a MinHash signature: a fixed-length slice of minimum hash values.
type Signature []uint64

// IsZero reports whether the signature is degenerate: empty, or every element
// is zero. Compute returns such a signature for text with fewer than 3 words
// (no shingles). Two distinct short texts therefore both produce an all-zero
// signature and would Jaccard-compare as identical (1.0) — a false positive.
// Callers performing near-dup dedup MUST skip comparison against, or
// registration of, a zero signature so unrelated short memories are never
// linked as duplicates. (See pkg/agent/memory.go::checkAndRegisterSigLocked.)
func (s Signature) IsZero() bool {
	if len(s) == 0 {
		return true
	}
	for _, v := range s {
		if v != 0 {
			return false
		}
	}
	return true
}

// NearDupRecord is the frozen v0.1.0 schema for a minhash.jsonl near-dup link.
// Append-only. Never delete records — v0.2 may prune, not v0.1.
type NearDupRecord struct {
	// TS is the UTC RFC3339 timestamp of the dedup event.
	TS time.Time `json:"ts"`
	// NewID is the memory that triggered the dedup check.
	NewID string `json:"new_id"`
	// ExistingID is the memory the new one was found similar to.
	ExistingID string `json:"existing_id"`
	// Jaccard is the estimated Jaccard similarity (0–1).
	Jaccard float64 `json:"jaccard"`
	// RoomRoot is the room root path (for provenance; not a key).
	RoomRoot string `json:"room_root"`
}

// Compute returns a MinHash signature for text using numPerm permutations.
// The shingling is over 3-word windows of whitespace-tokenised text.
// Returns a zero signature when text has fewer than 3 words.
func Compute(text string, numPerm int) Signature {
	shingles := shingle3(text)
	if len(shingles) == 0 {
		return make(Signature, numPerm)
	}

	sig := make(Signature, numPerm)
	for i := range sig {
		sig[i] = math.MaxUint64
	}

	// Use FNV-64a as the base hash; derive numPerm independent hashes via
	// the double-hashing technique: h_i(s) = h1(s) + i*h2(s)  mod 2^64.
	// h2 uses a different FNV seed (xor with a prime) to break correlation.
	for _, sh := range shingles {
		h1 := fnvHash64(sh)
		h2 := fnvHash64Alt(sh)
		for i := uint64(0); i < uint64(numPerm); i++ {
			v := h1 + i*h2
			if v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

// Jaccard estimates the Jaccard similarity between two Signatures.
//
// When the two signatures have different lengths (a programming error — all
// signatures in a given store use the same DefaultNumPerm), Jaccard returns 0
// rather than panicking. A panic here would crash the memory-write hot path on
// a benign mismatch; treating incomparable signatures as "not similar" fails
// safe (no false dedup) and is logged by the caller, not the library.
func Jaccard(a, b Signature) float64 {
	if len(a) != len(b) {
		return 0
	}
	if len(a) == 0 {
		return 0
	}
	matches := 0
	for i := range a {
		if a[i] == b[i] {
			matches++
		}
	}
	return float64(matches) / float64(len(a))
}

// IsNearDup returns true when Jaccard(a, b) >= threshold.
func IsNearDup(a, b Signature, threshold float64) bool {
	return Jaccard(a, b) >= threshold
}

// AppendNearDupRecord appends a NearDupRecord to minhashJSONLPath.
// Non-destructive: the new memory is NOT deleted — this record is a link only.
func AppendNearDupRecord(minhashJSONLPath string, rec NearDupRecord) error {
	return fileutil.AppendJSONL(minhashJSONLPath, rec)
}

// --- internal helpers -------------------------------------------------------

// shingle3 tokenises text into 3-word shingles.
// Returns nil when fewer than 3 tokens exist.
func shingle3(text string) []string {
	words := strings.Fields(strings.ToLower(text))
	if len(words) < 3 {
		return nil
	}
	out := make([]string, 0, len(words)-2)
	for i := 0; i <= len(words)-3; i++ {
		sh := words[i] + " " + words[i+1] + " " + words[i+2]
		out = append(out, sh)
	}
	return out
}

// fnvHash64 returns the FNV-64a hash of s.
func fnvHash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// fnvHash64Alt returns an alternative hash of s by xor-ing a large prime into
// the FNV-64a state before writing. Used as h2 in the double-hashing scheme.
func fnvHash64Alt(s string) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], 0x517CC1B727220A95) // large prime seed
	h := fnv.New64a()
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(s))
	return h.Sum64() | 1 // ensure odd (non-zero step for double-hashing)
}

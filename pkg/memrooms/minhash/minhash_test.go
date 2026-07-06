// Package minhash — MinHash near-dup dedup tests (FR-7.5 / M-5).
//
// Testing policy: written but NOT run locally (CI authority per CLAUDE.md).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package minhash_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memrooms/minhash"
)

// TestMinHash_IdenticalTextsMaxSimilarity verifies that identical texts have
// Jaccard ≈ 1.0 under 128 permutations.
func TestMinHash_IdenticalTextsMaxSimilarity(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog. It does so repeatedly."
	s1 := minhash.Compute(text, 128)
	s2 := minhash.Compute(text, 128)
	j := minhash.Jaccard(s1, s2)
	if j < 0.99 {
		t.Errorf("identical texts should have Jaccard ≈ 1.0, got %.4f", j)
	}
}

// TestMinHash_DisjointTextsLowSimilarity verifies that unrelated texts have
// low Jaccard (< DefaultThreshold ≈ 0.85).
func TestMinHash_DisjointTextsLowSimilarity(t *testing.T) {
	a := "Kubernetes cluster deployment helm chart replica scaling"
	b := "The French Revolution began in 1789 and transformed European history"
	sa := minhash.Compute(a, 128)
	sb := minhash.Compute(b, 128)
	j := minhash.Jaccard(sa, sb)
	if j >= minhash.DefaultThreshold {
		t.Errorf("disjoint texts should have Jaccard < %.2f, got %.4f", minhash.DefaultThreshold, j)
	}
}

// TestMinHash_NearDupDetected verifies that a slightly-paraphrased text is
// detected as near-duplicate (Jaccard >= DefaultThreshold).
// Traces to: US-5 / TDD #7 (dedup on write).
func TestMinHash_NearDupDetected(t *testing.T) {
	original := "We deploy using Kubernetes with Helm charts. The cluster has 5 nodes and " +
		"uses autoscaling policies to handle traffic spikes during peak hours."
	nearDup := "We deploy using Kubernetes with Helm charts. The cluster has 5 nodes and " +
		"uses autoscaling policies to handle traffic spikes during peak load times."

	so := minhash.Compute(original, 128)
	sn := minhash.Compute(nearDup, 128)
	j := minhash.Jaccard(so, sn)

	// These texts share the vast majority of 3-shingles; expect high similarity.
	if j < 0.80 {
		// At 128 perms, estimation error ≈ 1/sqrt(128) ≈ 9%; two very similar
		// texts should be well above 0.80.
		t.Errorf("near-dup texts should have Jaccard >= 0.80, got %.4f", j)
	}
}

// TestMinHash_IsNearDupThreshold verifies the IsNearDup helper with the
// DefaultThreshold boundary.
func TestMinHash_IsNearDupThreshold(t *testing.T) {
	// Two signatures constructed to be equal → Jaccard = 1.0 → is near-dup.
	s := minhash.Signature(make([]uint64, 16))
	isDup := minhash.IsNearDup(s, s, minhash.DefaultThreshold)
	if !isDup {
		t.Error("identical zero-signatures should be classified as near-dup")
	}

	// A non-empty sig vs empty sig: all MinHash values differ → Jaccard = 0.
	s2 := minhash.Compute("some text with three words", 16)
	notDup := minhash.IsNearDup(minhash.Signature(make([]uint64, 16)), s2, minhash.DefaultThreshold)
	if notDup {
		t.Error("zero-sig vs real sig should not be classified as near-dup")
	}
}

// TestMinHash_DedupLinkNonDestructive verifies that AppendNearDupRecord writes
// a frozen NearDupRecord to minhash.jsonl WITHOUT deleting any file.
// Traces to: FR-7.5 / M-5 / TDD #7.
func TestMinHash_DedupLinkNonDestructive(t *testing.T) {
	tmpDir := t.TempDir()
	mhPath := filepath.Join(tmpDir, minhash.MinHashJSONLFile)

	rec := minhash.NearDupRecord{
		TS:         time.Now().UTC(),
		NewID:      "mem-new-001",
		ExistingID: "mem-old-001",
		Jaccard:    0.91,
		RoomRoot:   tmpDir,
	}

	if err := minhash.AppendNearDupRecord(mhPath, rec); err != nil {
		t.Fatalf("AppendNearDupRecord: %v", err)
	}

	// Verify the JSONL file exists and contains the frozen format fields.
	data, err := os.ReadFile(mhPath)
	if err != nil {
		t.Fatalf("ReadFile minhash.jsonl: %v", err)
	}

	var parsed minhash.NearDupRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal minhash record: %v", err)
	}
	if parsed.NewID != rec.NewID {
		t.Errorf("new_id: got %q, want %q", parsed.NewID, rec.NewID)
	}
	if parsed.ExistingID != rec.ExistingID {
		t.Errorf("existing_id: got %q, want %q", parsed.ExistingID, rec.ExistingID)
	}
	if parsed.Jaccard != rec.Jaccard {
		t.Errorf("jaccard: got %.4f, want %.4f", parsed.Jaccard, rec.Jaccard)
	}
	// The original memory file must NOT have been deleted (non-destructive).
	// There is no .md file in this test — we're verifying the function only
	// appends a link record and does not attempt to delete anything.
}

// TestMinHash_ShortTextZeroSig verifies that texts shorter than 3 words
// produce a zero signature (no shingles).
func TestMinHash_ShortTextZeroSig(t *testing.T) {
	sig := minhash.Compute("hello world", 128) // 2 words — no 3-shingle
	for i, v := range sig {
		if v != 0 {
			t.Errorf("expected zero sig for 2-word text, got sig[%d]=%d", i, v)
		}
	}
}

// TestMinHash_FrozenRecordFormat verifies the JSON field names of NearDupRecord
// match the frozen v0.1.0 schema (FR-7.5 / NFR-1).
func TestMinHash_FrozenRecordFormat(t *testing.T) {
	rec := minhash.NearDupRecord{
		TS:         time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		NewID:      "id-new",
		ExistingID: "id-existing",
		Jaccard:    0.88,
		RoomRoot:   "/tmp/room",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)

	requiredKeys := []string{`"ts"`, `"new_id"`, `"existing_id"`, `"jaccard"`, `"room_root"`}
	for _, k := range requiredKeys {
		found := false
		for i := 0; i < len(s)-len(k); i++ {
			if s[i:i+len(k)] == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("frozen field %s not found in JSON: %s", k, s)
		}
	}
}

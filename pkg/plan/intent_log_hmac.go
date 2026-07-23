// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// intent_log_hmac.go implements sec-MINOR-3/#539 tamper-evidence for the
// write-ahead intent-log: a per-record HMAC chain over each plan's JSONL
// file, reusing the v0.2 #155 audit-log mechanism (pkg/audit/hmac.go,
// pkg/audit/verify.go) rather than a parallel implementation (DoD-11/BOM
// discipline) —
//
//   - Write side: embedChainHMAC computes hex(HMAC-SHA256(chainKey,
//     prev_hmac || canonical_json_without_hmac)) via the audit package's
//     exported ComputeChainHMAC/CanonicalJSONForChain, the exact same
//     algorithm audit.Logger uses for audit.jsonl.
//   - Verify side: VerifyChain calls audit.VerifyFile directly — it is
//     already fully generic over any JSONL file with an `hmac` field, so no
//     new verifier is written here at all.
//
// Key material: the chain key is a subkey of the master key, derived via
// credentials.Store.DeriveSubkey(IntentLogChainKeyInfo) — a domain-separated
// info tag distinct from audit.AuditChainKeyInfo, so a compromise of one
// subsystem's derived key does not affect the other (see DeriveSubkey's doc
// comment in pkg/credentials/store.go). Unlike the audit package's
// process-wide SetProcessChainKey global, the intent log's key is threaded
// through NewIntentLog's constructor and stored on the IntentLog value —
// there is exactly one IntentLog instance per process (unlike the audit
// Logger, which some tests construct many of), so a constructor parameter is
// simpler than a second package-level global.
//
// Chain scope: each plan_id's file is its own independent chain, seeded from
// audit.GenesisSeed() — there is no cross-plan or rotation chaining (unlike
// audit.jsonl, which rotates and threads the chain across rotation files).
// The chain covers the whole append-only file (every physical line, whether
// it is a fresh AppendIntent or a markLocked status-update line for an
// existing intent_id) — prev_hmac is always the file's last physical line,
// not the last line for the same intent_id.
package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// IntentLogChainKeyInfo is the HKDF info tag used to derive the intent-log's
// HMAC-chain key from the master key via credentials.Store.DeriveSubkey.
// Domain-separated from audit.AuditChainKeyInfo (#539) — bumping the suffix
// (v1 -> v2) forces a key rotation and breaks chain verification for all
// existing intent-log files, same migration contract as the audit package's
// own AuditChainKeyInfo.
const IntentLogChainKeyInfo = "omnipus-intent-log-chain-v1"

var intentLogDevKeyWarnOnce sync.Once

// resolveChainKey returns key if non-empty (copied defensively), else a
// deterministic dev-only fallback key with a sticky slog.Warn the first time
// it's used — mirrors pkg/audit's resolveChainKey precedent (hmac.go) so a
// misconfigured production boot (credStore.DeriveSubkey failing) is loud
// rather than silently running with a weaker key, while tests and early-dev
// boots without a credential store still work.
func resolveChainKey(key []byte) []byte {
	if len(key) > 0 {
		out := make([]byte, len(key))
		copy(out, key)
		return out
	}
	intentLogDevKeyWarnOnce.Do(func() {
		slog.Warn("intent_log: HMAC chain key not configured, using insecure dev-only fallback "+
			"(pass a key derived via credentials.Store.DeriveSubkey(plan.IntentLogChainKeyInfo) to NewIntentLog)",
			"info_tag", IntentLogChainKeyInfo)
	})
	h := sha256.Sum256([]byte("omnipus-intent-log-dev-only-key"))
	return h[:]
}

// embedChainHMAC computes and sets rec.Hmac by chaining off the last record
// in `records` (the plan file's current on-disk content, read under the
// per-plan striped lock by the caller) — audit.GenesisSeed() when records is
// empty or its last entry predates the chain (a pre-#539 legacy row, or one
// whose hmac field failed to decode). rec.Hmac is cleared before computing so
// re-embedding is idempotent regardless of what the caller passed in.
func embedChainHMAC(records []IntentRecord, rec *IntentRecord, key []byte) error {
	prev := audit.GenesisSeed()
	if n := len(records); n > 0 {
		if last := records[n-1].Hmac; last != "" {
			if mac, decErr := hex.DecodeString(last); decErr == nil && len(mac) == 32 {
				prev = mac
			}
		}
	}
	rec.Hmac = ""
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("intent_log: marshal for hmac: %w", err)
	}
	canonical, err := audit.CanonicalJSONForChain(raw)
	if err != nil {
		return fmt.Errorf("intent_log: canonicalise for hmac: %w", err)
	}
	mac := audit.ComputeChainHMAC(prev, canonical, key)
	rec.Hmac = hex.EncodeToString(mac)
	return nil
}

// VerifyChain walks planID's intent-log file and verifies the HMAC chain,
// reusing audit.VerifyFile directly (#539) rather than a parallel verifier —
// VerifyFile is already generic over any JSONL file keyed by an `hmac`
// field, so the intent log needs no bespoke verification loop. A plan with
// no log file yet is reported as a trivially valid (empty) chain, matching
// readAllLocked's own "file does not exist -> no records" tolerance.
func (l *IntentLog) VerifyChain(ctx context.Context, planID string) (*audit.ChainResult, error) {
	path := l.path(planID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &audit.ChainResult{Valid: true, BrokenAt: -1}, nil
		}
		return nil, fmt.Errorf("intent_log: stat %q: %w", path, err)
	}
	return audit.VerifyFile(ctx, path, l.chainKey, audit.GenesisSeed())
}

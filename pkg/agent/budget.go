// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// budget.go implements the ADR-053 Phase-2 app-level OVERALL token budget
// (D12, R§8.3, FR-171..FR-178, US-13, G-14): ONE shared pool debited by ALL
// workloads — owner loops, plan members, verifiers, the Judge — from
// provider-reported token usage, deliberately NOT honoring IsPrivilegedAgent
// (FR-172 — a deliberate SEC-26 posture shift: core-agent turns debit).
//
// Mechanics (R§8.3 resolution of the 5 D12 sub-gaps):
//
//	(a) default cap 0 == unbounded sentinel (FR-175); an unset budget runs
//	    unbounded and the Usage screen surfaces a persistent advisory.
//	(b) operator set-time warning that a token cap != dollar cap across
//	    providers is surfaced by SetTimeWarning (consumed by the gateway/Usage
//	    screen — this package only produces the text).
//	(c) the brake honors ADR-049 NFR-3 graceful wind-down: the current turn
//	    finishes; the transition to failed(budget_exhausted) happens at the
//	    next turn/adjudication boundary with a handover summary. Exhausted()
//	    is the boundary gate — it is NEVER consulted mid-turn (FR-174).
//	(d) ONE shared pool, single Debit(n) read-modify-write under ONE lock — the
//	    decrement and exhaustion check are one critical section so concurrent
//	    debits cannot corrupt the counter or lose a debit (FR-173/M5). Because
//	    usage is debited POST-turn from provider-reported counts, Consumed MAY
//	    exceed Cap, but the overshoot is bounded by the sum of in-flight turn
//	    costs (the turns already running when the pool crossed the cap).
//	(e) the ceiling is restart-gated (FR-177): SetCap is called ONCE at boot
//	    from config; a live ceiling change would straddle two budgets (the
//	    N-15 hazard) and is explicitly NOT supported. The live lever for
//	    runaway spend is the existing Stop/cancel cascade (per-goal-id or
//	    global) — no new live token cut is added here.
//
// This type deliberately takes no agent-type parameter anywhere: D12 removes
// the IsPrivilegedAgent exemption, so the debit path is agent-agnostic. The
// persisted counter is the Usage accounting and is reconciled at boot.
package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// FailedReasonBudgetExhausted is the failed_reason value a scope transitions to
// when the overall token budget brakes at a boundary (FR-174/R§8.3c, graceful
// wind-down). Consumed by the goal loop / plan engine / session sweep as the
// honest terminal for a budget-exhausted scope.
const FailedReasonBudgetExhausted = "budget_exhausted"

// TokenBudget is the single app-level OVERALL token pool (D12/R§8.3). It is
// safe for concurrent use: every debit goes through Debit, which performs the
// decrement and exhaustion check under one critical section (FR-173). The cap
// is restart-gated (FR-177); the live spend lever is Stop/cancel, not a live
// ceiling change.
type TokenBudget struct {
	mu        sync.Mutex
	capTok    int64 // 0 == unbounded sentinel (FR-175); restart-gated
	consumed  int64 // reconciled at boot from the persisted counter
	persister *TokenBudgetPersister
}

// NewTokenBudget constructs the pool with a restart-gated ceiling (capTok 0 =
// unbounded). persister may be nil (in-memory only, e.g. tests); when non-nil
// the consumed counter is reconciled from disk and each debit persists.
func NewTokenBudget(capTok int64, persister *TokenBudgetPersister) *TokenBudget {
	b := &TokenBudget{capTok: capTok, persister: persister}
	if persister != nil {
		if st, err := persister.Load(); err != nil {
			// BRAKE BASELINE UNRELIABLE (silent-MAJOR-1): a load failure means
			// the prior consumed counter cannot be trusted, so consumed stays
			// at 0. This is NOT a fresh-install-info Warn — when an operator
			// has set a non-zero cap, that cap no longer protects the prior
			// spend it was meant to bound (the counter that would have tripped
			// Exhausted() has been reset). Future debits accumulate against
			// the cap from zero. Surface this at ERROR grade so the operator
			// knows the spend brake is effectively defeated for prior spend
			// until the next successful save lands a fresh baseline.
			if capTok > 0 {
				slog.Error("token_budget: brake baseline unreliable — consumed counter reset to 0; "+
					"the configured cap no longer protects prior spend (future debits accumulate from zero)",
					"cap", capTok, "error", err.Error())
			} else {
				slog.Warn("token_budget: could not load persisted counter; starting at 0 (cap unset/unbounded)",
					"error", err.Error())
			}
		} else if st.Consumed > 0 {
			b.consumed = st.Consumed
			slog.Info("token_budget: reconciled consumed counter at boot",
				"consumed", st.Consumed, "cap", capTok)
		}
	}
	return b
}

// SetCap sets the restart-gated ceiling. Per FR-177 this is intended to be
// called ONCE at boot from config; a later call is a reconfiguration that
// deliberately does NOT carry live (the documented N-15 hazard). The consumed
// counter is preserved across a cap change. Negative is clamped to 0
// (unbounded).
func (b *TokenBudget) SetCap(capTok int64) {
	b.mu.Lock()
	if capTok < 0 {
		capTok = 0
	}
	b.capTok = capTok
	b.mu.Unlock()
}

// Cap returns the configured ceiling (0 = unbounded).
func (b *TokenBudget) Cap() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capTok
}

// Consumed returns the total tokens debited across all workloads since boot
// (or since the persisted counter was last reconciled).
func (b *TokenBudget) Consumed() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consumed
}

// IsUnbounded reports whether the ceiling is the 0 sentinel (FR-175): an
// unbounded pool debits for accounting but never brakes.
func (b *TokenBudget) IsUnbounded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capTok == 0
}

// Debit atomically subtracts n tokens from the shared pool, returning the
// post-debit consumed total and whether the pool is now exhausted (the
// graceful-wind-down gate). The decrement and exhaustion check are ONE
// critical section (R§8.3d/FR-173): concurrent debits cannot corrupt the
// counter or lose a debit. n <= 0 is a no-op accounting touch that returns the
// current state without changing it.
//
// Agent-agnostic by design (FR-172): there is no agentType parameter — the
// IsPrivilegedAgent exemption is removed, so core-agent turns debit the same
// pool. Because usage is debited post-turn from provider-reported counts,
// consumed MAY exceed cap, but the overshoot is bounded by the sum of costs of
// turns already in flight when the pool crossed (M5).
func (b *TokenBudget) Debit(n int64) (consumed int64, exhausted bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > 0 {
		b.consumed += n
	}
	consumed = b.consumed
	exhausted = b.capTok > 0 && b.consumed >= b.capTok
	if b.persister != nil {
		if err := b.persister.Save(tokenBudgetState{Consumed: b.consumed}); err != nil {
			// A persist failure does NOT roll back the in-memory debit (the
			// tokens were already spent): the counter stays correct in-memory
			// and is reconciled at the next successful save / boot.
			slog.Warn("token_budget: persist after debit failed (in-memory counter still correct)",
				"error", err.Error(), "consumed", b.consumed)
		}
	}
	return consumed, exhausted
}

// Exhausted reports whether the pool has crossed the cap — the graceful
// wind-down gate, consulted at a turn/adjudication BOUNDARY (never mid-turn,
// FR-174/R§8.3c). Always false when unbounded (cap 0).
func (b *TokenBudget) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capTok > 0 && b.consumed >= b.capTok
}

// Remaining returns the tokens left before the brake (max(0, cap-consumed)),
// or -1 when unbounded (cap 0). Useful for the Usage screen's spend display.
func (b *TokenBudget) Remaining() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capTok == 0 {
		return -1
	}
	r := b.capTok - b.consumed
	if r < 0 {
		return 0
	}
	return r
}

// SetTimeWarning is the operator-facing R§8.3b warning surfaced when an
// operator sets a token budget: a token cap does not bound dollar spend
// uniformly across providers (models differ in price-per-token). The gateway /
// Usage screen renders this text at set-time; this function is the single
// source so the wording cannot drift.
func SetTimeWarning() string {
	return "Note: a token budget caps token usage, not dollar spend — " +
		"per-token prices differ across models/providers, so equal token " +
		"usage can cost different amounts. Use Stop/cancel for live spend control."
}

// UnboundedAdvisory is the persistent Usage-screen advisory for an unset
// budget (R§8.3a/FR-175): the install runs unbounded until the operator sets
// a ceiling.
func UnboundedAdvisory() string {
	return "Overall token budget is unset — running unbounded. Set a token budget in Usage to cap spend."
}

// --- persistence (mirrors security.CostTracker: atomic temp+rename) --------

// tokenBudgetState is the persisted shape of the consumed counter.
type tokenBudgetState struct {
	Consumed int64 `json:"consumed"`
}

// TokenBudgetPersister reconciles the consumed counter across restarts
// (R§8.3d "persisted counter = Usage accounting, reconciled at boot"). Same
// atomic write pattern as security.CostTracker (temp file + rename).
type TokenBudgetPersister struct {
	filePath string
}

// NewTokenBudgetPersister creates a persister at filePath (typically
// $OMNIPUS_HOME/token_budget.json).
func NewTokenBudgetPersister(filePath string) *TokenBudgetPersister {
	return &TokenBudgetPersister{filePath: filePath}
}

// Load reads the persisted consumed counter. Returns zero (and a nil error)
// when the file does not yet exist (fresh install). A CORRUPT state file
// (present but unparseable) is surfaced as a non-nil error — silently
// returning {0}, nil here would defeat the spend brake for prior spend:
// NewTokenBudget would skip reconcile, leave consumed at 0, and Exhausted()
// would read false even though the operator set a cap to bound exactly that
// prior spend (silent-MAJOR-1). The error lets NewTokenBudget emit a distinct
// ERROR-grade "brake baseline unreliable" advisory so the operator knows the
// cap they set no longer protects prior spend.
func (p *TokenBudgetPersister) Load() (tokenBudgetState, error) {
	data, err := os.ReadFile(p.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return tokenBudgetState{}, nil
		}
		return tokenBudgetState{}, fmt.Errorf("token_budget: read: %w", err)
	}
	var st tokenBudgetState
	if err := json.Unmarshal(data, &st); err != nil {
		// Do NOT swallow this as {0}, nil — surface it (see the doc comment
		// above). NewTokenBudget decides what to do with the error.
		return tokenBudgetState{}, fmt.Errorf("token_budget: corrupt state file %q: %w", p.filePath, err)
	}
	return st, nil
}

// Save persists the consumed counter atomically (temp + rename, 0600).
func (p *TokenBudgetPersister) Save(st tokenBudgetState) error {
	if err := os.MkdirAll(filepath.Dir(p.filePath), 0o700); err != nil {
		return fmt.Errorf("token_budget: mkdir: %w", err)
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("token_budget: marshal: %w", err)
	}
	tmp := p.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("token_budget: write temp: %w", err)
	}
	if err := os.Rename(tmp, p.filePath); err != nil {
		return fmt.Errorf("token_budget: rename: %w", err)
	}
	return nil
}

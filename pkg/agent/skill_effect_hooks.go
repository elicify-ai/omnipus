// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-24 order of effects: answering a skill version card
// writes the trust approval, then records the timed allowance, then
// releases the waiting call. These are the loop's inputs at those three
// boundaries: each runs just before its effect and may stop it (a non-nil
// error is that effect's failure). Production sets none.

import "sync"

// SkillEffectHooks are the inputs before the three C-24 effects. A nil
// field does nothing.
type SkillEffectHooks struct {
	BeforeTrustWrite      func() error
	BeforeAllowanceRecord func() error
	BeforeWaitingRelease  func()
}

var (
	skillEffectHooksMu sync.Mutex
	skillEffectHooks   = map[*AgentLoop]SkillEffectHooks{}
)

// SetSkillEffectHooks sets the loop's C-24 effect inputs and returns the
// function that restores the previous ones.
func (al *AgentLoop) SetSkillEffectHooks(beforeTrustWrite, beforeAllowanceRecord func() error, beforeWaitingRelease func()) (restore func()) {
	skillEffectHooksMu.Lock()
	defer skillEffectHooksMu.Unlock()
	prev, had := skillEffectHooks[al]
	skillEffectHooks[al] = SkillEffectHooks{
		BeforeTrustWrite:      beforeTrustWrite,
		BeforeAllowanceRecord: beforeAllowanceRecord,
		BeforeWaitingRelease:  beforeWaitingRelease,
	}
	return func() {
		skillEffectHooksMu.Lock()
		defer skillEffectHooksMu.Unlock()
		if had {
			skillEffectHooks[al] = prev
			return
		}
		delete(skillEffectHooks, al)
	}
}

// SkillEffectHooks returns the loop's C-24 effect inputs (all nil in
// production).
func (al *AgentLoop) SkillEffectHooks() SkillEffectHooks {
	skillEffectHooksMu.Lock()
	defer skillEffectHooksMu.Unlock()
	return skillEffectHooks[al]
}

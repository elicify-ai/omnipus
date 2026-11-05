package discord

// Test-only observation points of the T-9 wrapper and the FR-046 DM-open
// (channels-spec-r8 T-24b, S-89). Nil is the production default and changes
// nothing; a hook is a passive acknowledgement. Tests install a hook before
// the work and clear it (nil) after joining every participant. The hooks are
// process-wide like the wrapper's slot pool, so tests using them must not run
// in parallel with each other.

import "sync/atomic"

// The wrapper call names a slot hook receives.
const (
	wrappedCallChannel = "channel" // T-9 step 2, REST channel read
	wrappedCallUser    = "user"    // T-9 step 3, REST user read
	wrappedCallDMOpen  = "dm_open" // FR-046 UserChannelCreate
)

var (
	wrappedCallSlotHook atomic.Pointer[func(call string)]
	beforeDMOpenHook    atomic.Pointer[func(userID string)]
)

// SetWrappedCallSlotHookForTest installs fn, called each time a wrapper
// call has obtained one of the two shared in-flight slots and before it
// enters discordgo; call is "channel", "user" or "dm_open". It runs while
// the slot is held, so a test can count parked slots exactly. It must not
// block indefinitely: the slot is released only after the call returns.
func SetWrappedCallSlotHookForTest(fn func(call string)) {
	if fn == nil {
		wrappedCallSlotHook.Store(nil)
		return
	}
	wrappedCallSlotHook.Store(&fn)
}

// SetBeforeDMOpenHookForTest installs fn, called by a start to a person
// after the target resolved to a user (T-9 step 3) and immediately before
// the DM-open enters the wrapper's slot wait (the before-DM admission
// point). No lock and no slot is held while it runs, so it may block.
func SetBeforeDMOpenHookForTest(fn func(userID string)) {
	if fn == nil {
		beforeDMOpenHook.Store(nil)
		return
	}
	beforeDMOpenHook.Store(&fn)
}

func observeWrappedCallSlot(call string) {
	if fn := wrappedCallSlotHook.Load(); fn != nil {
		(*fn)(call)
	}
}

func observeBeforeDMOpen(userID string) {
	if fn := beforeDMOpenHook.Load(); fn != nil {
		(*fn)(userID)
	}
}

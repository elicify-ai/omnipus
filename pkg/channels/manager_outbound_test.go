package channels

// T-01 — Availability states (channels-spec-r8, Test implementation order
// T-01; T-7 table; S-04; S-52 inputs; FR-051).
//
// Oracles: the T-7 table (normative) —
//   running     : registered (GetChannel) and IsRunning, reason empty
//   not_running : registered, not running; reason = LastStartError(id) text if
//                 set, else "not started"
//   not_loaded  : not registered; reason = the FailedChannels() entry for id
//                 if any, else "not loaded"
// S-04 pins the two degraded states' reasons surfacing: construction failure
// ("token not resolved" from the FailedChannels entry) and Start failure
// ("401" from LastStartError).
//
// RED expectation on the WP-0 shell: Manager.Availability returns the zero
// Availability for every state, so every state/reason assertion below fails.

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// availabilityChannel returns a fresh mock adapter with the given running
// flag, named id, registered on its own manager.
func newAvailabilityFixture(running bool) (*Manager, *mockChannel) {
	m := newTestManager()
	ch := &mockChannel{}
	ch.SetRunning(running)
	m.RegisterChannel("discord", ch)
	return m, ch
}

func TestAvailability_RunningRegisteredChannelReportsRunning(t *testing.T) {
	// T-7 row 1: registered + IsRunning → running, empty reason.
	m, _ := newAvailabilityFixture(true)

	got := m.Availability("discord")

	require.Equal(t, AvailabilityRunning, got.State,
		"T-7: a registered running channel must report running")
	require.Empty(t, got.Reason,
		"T-7: the running row carries no reason")
}

func TestAvailability_RegisteredStoppedChannelReportsNotStarted(t *testing.T) {
	// T-7 row 2 (no start error): registered, not running → not_running with
	// the fixed reason "not started".
	m, _ := newAvailabilityFixture(false)

	got := m.Availability("discord")

	require.Equal(t, AvailabilityNotRunning, got.State,
		"T-7: a registered channel that is not running must report not_running")
	require.Equal(t, "not started", got.Reason,
		"T-7: without a start error the reason is exactly \"not started\"")
}

func TestAvailability_StartFailureReasonSurfacesFromLastStartError(t *testing.T) {
	// T-7 row 2 (with start error) + S-04 case 2: the reason is
	// LastStartError(id)'s text — the 401 from the S-04 fixture.
	m, _ := newAvailabilityFixture(false)
	m.mu.Lock()
	if m.startErrors == nil {
		m.startErrors = make(map[string]error)
	}
	m.startErrors["discord"] = errors.New("401 unauthorized: bad bot token")
	m.mu.Unlock()

	got := m.Availability("discord")

	require.Equal(t, AvailabilityNotRunning, got.State)
	require.Contains(t, got.Reason, "401",
		"T-7/S-04: the reason must carry the LastStartError text")
}

func TestAvailability_UnregisteredChannelReportsNotLoaded(t *testing.T) {
	// T-7 row 3 (no failed entry): not registered → not_loaded, "not loaded".
	m := newTestManager()

	got := m.Availability("discord")

	require.Equal(t, AvailabilityNotLoaded, got.State,
		"T-7: an unregistered channel must report not_loaded")
	require.Equal(t, "not loaded", got.Reason,
		"T-7: without a failed-channel entry the reason is exactly \"not loaded\"")
}

func TestAvailability_ConstructionFailureReasonSurfacesFromFailedChannels(t *testing.T) {
	// T-7 row 3 (with failed entry) + S-04 case 1: the reason is the
	// FailedChannels entry's text — the "token not resolved" fixture — and
	// the state is not_loaded (the instance never registered).
	m := newTestManager()
	m.mu.Lock()
	m.failedChannels = append(m.failedChannels, ChannelInitError{
		Name:             "discord",
		Channel:          "Discord",
		Err:              errors.New("token not resolved"),
		CredentialScoped: true,
	})
	m.mu.Unlock()

	got := m.Availability("discord")

	require.Equal(t, AvailabilityNotLoaded, got.State,
		"T-7: a construction failure leaves the instance unregistered → not_loaded")
	require.Contains(t, got.Reason, "token not resolved",
		"T-7/S-04: the reason must carry the FailedChannels entry's text")
}

func TestAvailability_StatesUseContractEnumSpellings(t *testing.T) {
	// The states are the contract enum ChannelAvailability.state values
	// (contract-first, T-7); the generated TS/Go clients switch on them.
	require.Equal(t, AvailabilityState("running"), AvailabilityRunning)
	require.Equal(t, AvailabilityState("not_running"), AvailabilityNotRunning)
	require.Equal(t, AvailabilityState("not_loaded"), AvailabilityNotLoaded)
	// The running reason is empty, never a placeholder string.
	m, _ := newAvailabilityFixture(true)
	got := m.Availability("discord")
	require.False(t, strings.TrimSpace(got.Reason) != "",
		"T-7: running must not carry a reason string")
}

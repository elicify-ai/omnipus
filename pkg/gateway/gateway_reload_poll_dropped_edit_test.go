// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

const pollTestBase = `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`

func pollTestConfigWith(binary string) []byte {
	return []byte(fmt.Sprintf(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],`+
		`"sandbox":{"command_rules":[{"action":"allow","binary":%q}]}}`, binary))
}

// watchRefusedHandoffs makes the poller report every refused hand-off on the
// returned channel, so a test waits for the real event instead of sleeping.
// Call it BEFORE setupConfigWatcherPolling: the cleanup that clears the hook
// then runs after the watcher has been stopped.
func watchRefusedHandoffs(t *testing.T) <-chan struct{} {
	t.Helper()
	refused := make(chan struct{}, 64)
	configWatcherHandoffRefusedHook = func() {
		select {
		case refused <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { configWatcherHandoffRefusedHook = nil })
	return refused
}

func awaitRefusedHandoff(t *testing.T, refused <-chan struct{}) {
	t.Helper()
	select {
	case <-refused:
	case <-time.After(10 * time.Second):
		t.Fatal("the poller never refused a hand-off while the buffer was full")
	}
}

// fillHandoffBuffer plays a busy reload loop: edit one fills the one-slot buffer
// and nothing reads it. It returns once the buffer holds edit one.
func fillHandoffBuffer(t *testing.T, configPath string, configChan chan watchedConfigChange) {
	t.Helper()
	// Sizes differ per edit so a coarse-mtime filesystem still sees each change.
	require.NoError(t, os.WriteFile(configPath, pollTestConfigWith("edit-one"), 0o600))
	require.Eventually(t, func() bool { return len(configChan) == 1 }, 10*time.Second, 20*time.Millisecond,
		"edit 1 must reach the hand-off buffer")
}

func requireEditOffered(t *testing.T, configChan chan watchedConfigChange, binary string) {
	t.Helper()
	select {
	case got := <-configChan:
		require.NotNil(t, got.cfg)
		require.Equal(t, binary, got.cfg.Sandbox.CommandRules[0].Binary,
			"the dropped last edit must be offered again with the file's current content")
	case <-time.After(10 * time.Second):
		t.Fatal("the edit dropped while a reload was running was never offered again")
	}
}

// #1743: the poller recorded the new file timestamp BEFORE the non-blocking
// hand-off to the reload loop. While a reload runs, the loop does not read the
// channel: edit 1 waits in the one-slot buffer, edit 2 is refused and, with its
// timestamp already recorded, was never offered again. The operator's last edit
// stayed unapplied until the next edit.
//
// The test waits for the poller's real refused hand-off (a test seam), then
// frees the buffer. It uses the real poller clock (2 s tick + 500 ms debounce).
func TestConfigWatcherPoll_EditDroppedWhileReloadRunningIsOfferedAgain(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(pollTestBase), 0o600))
	refused := watchRefusedHandoffs(t)

	configChan, stop := setupConfigWatcherPolling(configPath, home, false,
		credentials.NewStore(filepath.Join(home, "credentials.json")), nil, nil, nil)
	t.Cleanup(stop)

	fillHandoffBuffer(t, configPath, configChan)
	require.NoError(t, os.WriteFile(configPath, pollTestConfigWith("edit-number-two"), 0o600))
	awaitRefusedHandoff(t, refused) // edit 2 hit the full buffer

	first := <-configChan // the running reload finishes: the loop takes edit 1
	require.NotNil(t, first.cfg)
	require.Equal(t, "edit-one", first.cfg.Sandbox.CommandRules[0].Binary)
	requireEditOffered(t, configChan, "edit-number-two")
}

// #1743 (CHECK round 1, finding 1): config loading can rewrite the file itself
// (the CLI-token migration) and registers that write's hash as a self-write. If
// that happens while the hand-off is refused, the next tick must still offer the
// outstanding external edit instead of treating the file as the app's own write.
// The migration is reproduced exactly as it reaches the poller: after the
// refusal the file holds the migrated bytes and their hash is registered.
func TestConfigWatcherPoll_OutstandingEditSurvivesOwnMigrationWrite(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(pollTestBase), 0o600))
	refused := watchRefusedHandoffs(t)
	reg := &configSelfWriteRegistry{hashes: map[[32]byte]struct{}{}}

	configChan, stop := setupConfigWatcherPolling(configPath, home, false,
		credentials.NewStore(filepath.Join(home, "credentials.json")), reg, nil, nil)
	t.Cleanup(stop)

	fillHandoffBuffer(t, configPath, configChan)
	require.NoError(t, os.WriteFile(configPath, pollTestConfigWith("edit-number-two"), 0o600))
	awaitRefusedHandoff(t, refused)

	// The load of edit 2 rewrote the file (same settings, migrated layout) and
	// registered the written bytes.
	migrated := append(pollTestConfigWith("edit-number-two"), '\n', '\n')
	require.NoError(t, os.WriteFile(configPath, migrated, 0o600))
	reg.register(sha256.Sum256(migrated))

	<-configChan // the running reload finishes
	requireEditOffered(t, configChan, "edit-number-two")
}

// Guard: with no refused hand-off, a registered self-write is still suppressed
// (ordinary app writes must not reload).
func TestConfigWatcherPoll_SelfWriteStillSuppressedWhenNothingOutstanding(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(pollTestBase), 0o600))
	reg := &configSelfWriteRegistry{hashes: map[[32]byte]struct{}{}}

	configChan, stop := setupConfigWatcherPolling(configPath, home, false,
		credentials.NewStore(filepath.Join(home, "credentials.json")), reg, nil, nil)
	t.Cleanup(stop)

	own := pollTestConfigWith("app-wrote-this")
	reg.register(sha256.Sum256(own))
	require.NoError(t, os.WriteFile(configPath, own, 0o600))

	select {
	case got := <-configChan:
		t.Fatalf("an app-initiated write was offered as an external edit: %+v", got.cfg.Sandbox.CommandRules)
	case <-time.After(6 * time.Second): // at least two ticks plus the debounce
	}
}

// #1743 guard: a file that fails to load stays marked as seen. It is reported
// once, not re-read and re-reported on every tick (no reload loop).
func TestConfigWatcherPoll_BrokenFileIsReportedOnceNotEveryTick(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(pollTestBase), 0o600))

	var mu sync.Mutex
	reports := 0
	_, stop := setupConfigWatcherPolling(configPath, home, false,
		credentials.NewStore(filepath.Join(home, "credentials.json")), nil,
		func(error) { mu.Lock(); reports++; mu.Unlock() }, nil)
	t.Cleanup(stop)

	require.NoError(t, os.WriteFile(configPath, []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],`+
		`"sandbox":{"command_rules":[{"action":"Deny","binary":"rm"}]}}`), 0o600))

	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return reports >= 1 },
		10*time.Second, 50*time.Millisecond, "the broken file must be reported")
	time.Sleep(6 * time.Second) // at least two more poll ticks: the negative claim needs elapsed ticks
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, reports, "an unchanged broken file must not be re-read and re-reported every tick")
}

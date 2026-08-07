package logger

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestLogMessage_ConcurrentWithLoggingToggles drives the real, exported
// production entry points concurrently: a stream of goroutines emitting log
// messages via WarnCF (which funnels through logMessage) while other
// goroutines repeatedly call EnableFileLogging, DisableFileLogging, and
// DisableConsole -- the same mutators that raced against logMessage in
// production (see captured trace: DisableFileLogging at logger.go:171 vs.
// WarnCF -> logMessage read at logger.go:255, hit via
// AgentLoop.HydrateAgentHistoryFromTranscript -> WSHandler.handleAttachSession).
//
// Run with -race. Before the fix (unsynchronized reads of the package-level
// `logger`/`fileLogger` globals in logMessage), this reliably reports
// "WARNING: DATA RACE". After the fix, it must be race-clean.
func TestLogMessage_ConcurrentWithLoggingToggles(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "race.log")

	// Leave package state clean for any tests that run later in this binary.
	defer DisableFileLogging()

	deadline := time.Now().Add(300 * time.Millisecond)

	var wg sync.WaitGroup

	// Writers: hammer the three mutators that touch the shared globals.
	wg.Add(3)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if err := EnableFileLogging(logPath); err != nil {
				t.Errorf("EnableFileLogging: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			DisableFileLogging()
		}
	}()
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			DisableConsole()
		}
	}()

	// Readers: hammer logMessage via the real public logging API.
	const readerGoroutines = 8
	wg.Add(readerGoroutines)
	for i := 0; i < readerGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			j := 0
			for time.Now().Before(deadline) {
				WarnCF("race-test", fmt.Sprintf("msg-%d-%d", id, j), map[string]any{"n": j})
				j++
			}
		}(i)
	}

	wg.Wait()
}

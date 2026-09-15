package logger

import "testing"

// TestDisableConsole_RestoreCycle: the return value puts the previous console
// logger back, and ConsoleDisabled reports the transition both ways. Guards
// the mechanism the test suites rely on (a t.Cleanup'd restore), so a test
// that silences the console can never leave every later test in the same
// binary unable to log.
func TestDisableConsole_RestoreCycle(t *testing.T) {
	if ConsoleDisabled() {
		t.Fatalf("console is disabled before the test ran — an earlier test left it silenced")
	}
	restore := DisableConsole()
	if !ConsoleDisabled() {
		t.Fatalf("DisableConsole ran but ConsoleDisabled reports false")
	}
	restore()
	if ConsoleDisabled() {
		t.Fatalf("restore ran but ConsoleDisabled still reports true")
	}
}

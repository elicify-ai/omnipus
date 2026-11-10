//go:build linux

// max_tool_iterations_env_import_linux_test.go — #904: an env import whose
// write to config.json FAILS is applied in memory, logged at WARN, and
// retried on the next load (spec "API and Data", D6/D7: never an outage).
//
// Linux-only: the unwritable-yet-readable config path is /proc/self/fd/<n>
// — reading it opens the real file, but no file can be created in
// /proc/self/fd, so the atomic write's temp file cannot be made, even as
// root (where a chmod-based fault would not fire). CI's Go test gate runs on
// Linux.

package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEnvImport_WriteFailure_AppliedInMemoryAndRetried(t *testing.T) {
	logs := captureMTIConfigLogs(t)
	p := writeMTIConfig(t, `"max_tool_iterations":200`)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	unwritable := fmt.Sprintf("/proc/self/fd/%d", f.Fd())
	t.Setenv(mtiEnvVar, "80")

	cfg, err := LoadConfig(unwritable)
	if err != nil {
		t.Fatalf("a failed import write must never refuse the load (D7): %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 80 {
		t.Errorf("in-memory global = %d, want 80 (applied for this run despite the write failure)", got)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatalf("instrument: the fault did not fire — config.json was rewritten:\n%s", after)
	}
	warned := false
	for _, l := range logs.lines() {
		if mtiIsWarn(l) && strings.Contains(l, mtiEnvVar) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("a WARN naming %s must report the failed save; lines: %v", mtiEnvVar, logs.lines())
	}

	// Next boot, file writable: the import is retried and lands with its marker.
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("retry LoadConfig: %v", err)
	}
	d := mtiDiskDefaults(t, p)
	if d["max_tool_iterations"] != float64(80) || d["max_tool_iterations_env_imported"] != true {
		t.Errorf("retried import: global = %v, marker = %v; want 80 and true", d["max_tool_iterations"], d["max_tool_iterations_env_imported"])
	}
}

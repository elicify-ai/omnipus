// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// intent_log_boot_tamper_test.go closes the gap between "VerifyChain CAN
// detect a forged/edited intent line" (already proven directly by
// TestIntentLog_Hardening in intent_log_test.go) and "boot ACTUALLY invokes
// that check and surfaces the detection" (sec-MINOR-3/#539 item 2's
// "boot detects a forged/edited line" acceptance wording). ReplayAtBoot is
// the boot entrypoint (see its doc comment and the chain-check block at the
// top of its body) — this test drives THAT function, not VerifyChain
// directly, against a tampered log file and asserts the loud slog.Error
// alarm fires with the plan_id and reason, mirroring the audit-log
// precedent (chain verification is an on-demand/boot-time alarm, never a
// silent no-op and never a boot-abort — see ReplayAtBoot's own comment for
// why a broken chain still degrades to the existing fail-safe
// discard/replay-forward classification rather than bricking recovery).
package plan

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureErrorLogs redirects slog's default logger to a buffer for the
// duration of the test (restored via t.Cleanup), mirroring the existing
// pkg/audit captureWarnLogs precedent (checkpoint_adversarial_test.go).
func captureErrorLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestIntentLog_ReplayAtBoot_DetectsForgedLine is the boot-path proof for
// sec-MINOR-3/#539 item 2: forge a byte in an on-disk intent record, then
// call ReplayAtBoot (the actual boot entrypoint, not the lower-level
// VerifyChain primitive) and assert it logs a chain-broken alarm naming the
// plan. This is the non-vacuous check that the HMAC chain is actually WIRED
// INTO the boot path, not merely capable of detecting tampering in
// isolation.
func TestIntentLog_ReplayAtBoot_DetectsForgedLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plan_intents")
	key := []byte("boot-tamper-test-key-0123456789")
	il, err := NewIntentLog(dir, key)
	if err != nil {
		t.Fatalf("NewIntentLog: %v", err)
	}
	rec := IntentRecord{IntentID: "i-boot-tamper", PlanID: "p-boot-tamper"}
	if appendErr := il.AppendIntent(rec); appendErr != nil {
		t.Fatalf("AppendIntent: %v", appendErr)
	}
	if commitErr := il.MarkCommitted(rec.PlanID, rec.IntentID); commitErr != nil {
		t.Fatalf("MarkCommitted: %v", commitErr)
	}

	// Sanity: an untampered log must NOT trip the alarm.
	cleanBuf := captureErrorLogs(t)
	if _, replayErr := il.ReplayAtBoot(rec.PlanID, func(IntentRecord) error { return nil }); replayErr != nil {
		t.Fatalf("ReplayAtBoot (clean): %v", replayErr)
	}
	if strings.Contains(cleanBuf.String(), "HMAC chain broken") {
		t.Fatalf("ReplayAtBoot alarmed on an untampered log: %s", cleanBuf.String())
	}

	// Forge a byte in the on-disk record (flip the recorded status, exactly
	// as TestIntentLog_Hardening does), then drive ReplayAtBoot again against
	// the SAME plan_id and assert it raises the alarm this time.
	path := il.path(rec.PlanID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read intent log: %v", err)
	}
	forged := bytes.Replace(data, []byte(`"status":"committed"`), []byte(`"status":"done"`), 1)
	if bytes.Equal(forged, data) {
		t.Fatal("test fixture did not modify an intent line")
	}
	if writeErr := os.WriteFile(path, forged, 0o600); writeErr != nil {
		t.Fatalf("forge intent log: %v", writeErr)
	}

	tamperBuf := captureErrorLogs(t)
	if _, replayErr := il.ReplayAtBoot(rec.PlanID, func(IntentRecord) error { return nil }); replayErr != nil {
		t.Fatalf("ReplayAtBoot (tampered): %v", replayErr)
	}
	got := tamperBuf.String()
	if !strings.Contains(got, "HMAC chain broken") {
		t.Fatalf("ReplayAtBoot did not log the chain-broken alarm for a forged line; log output: %s", got)
	}
	if !strings.Contains(got, rec.PlanID) {
		t.Fatalf("chain-broken alarm did not name the affected plan_id %q; log output: %s", rec.PlanID, got)
	}
}

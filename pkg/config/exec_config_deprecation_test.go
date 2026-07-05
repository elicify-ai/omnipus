// Contract test: tools.exec.* legacy fields (ExecConfig, config.go) are dead
// under ADR-036 — the merged `bash` tool never reads config.Tools.Exec at
// all (NewExecToolWithConfig discards its *config.Config parameter).
// warnDeprecatedExecConfigFields (exec_config_deprecation.go) surfaces
// operator-customized values via a boot-time WARN instead of silently
// ignoring them, mirroring migrateDeprecatedToolEnableFlags' Info+Warn-once
// convention (migration.go).

package config

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureExecConfigWarnLogs resets the Once guard, captures all Info+ level
// slog output produced by a single warnDeprecatedExecConfigFields call, and
// restores the previous default logger on test cleanup.
func captureExecConfigWarnLogs(t *testing.T, raw []byte) string {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })

	deprecatedExecConfigWarnOnce = sync.Once{}
	warnDeprecatedExecConfigFields(raw)
	return buf.String()
}

func TestWarnDeprecatedExecConfigFields_CustomDenyPatternsWarns(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{"enable_deny_patterns":true,"custom_deny_patterns":["curl.*\\|.*sh"]}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Contains(t, out, "custom_deny_patterns")
	assert.Contains(t, out, "deprecated")
	// Both the per-load Info and the Once-guarded Warn must fire on first call.
	assert.Equal(t, 2, len(splitLines(out)),
		"expected exactly 2 log lines (Info + Warn-once); got: %s", out)
}

func TestWarnDeprecatedExecConfigFields_CustomAllowPatternsWarns(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{"custom_allow_patterns":["^git .*"]}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Contains(t, out, "custom_allow_patterns")
	assert.Contains(t, out, "no successor")
}

func TestWarnDeprecatedExecConfigFields_CustomTimeoutWarns(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{"timeout_seconds":120}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Contains(t, out, "timeout_seconds")
}

func TestWarnDeprecatedExecConfigFields_CustomMaxBackgroundSecondsWarns(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{"max_background_seconds":900}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Contains(t, out, "max_background_seconds")
}

func TestWarnDeprecatedExecConfigFields_ExplicitDisableWarns(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{"enable_deny_patterns":false}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Contains(t, out, "enable_deny_patterns")
}

// TestWarnDeprecatedExecConfigFields_SeededDefaultsNoWarn is the critical
// false-positive guard: config.json persists the FULL defaulted ExecConfig
// (these fields lack `omitempty`), so every fresh/onboarded install already
// carries enable_deny_patterns:true, timeout_seconds:60,
// max_background_seconds:300 whether or not the operator ever touched them
// (see the real sample captured in defaults.go's ExecConfig comment). A
// naive "key present" check would fire on effectively every config.json in
// existence; this test guards against that regression.
func TestWarnDeprecatedExecConfigFields_SeededDefaultsNoWarn(t *testing.T) {
	raw := []byte(`{"tools":{"exec":{
		"enabled":true,
		"enable_deny_patterns":true,
		"custom_deny_patterns":null,
		"custom_allow_patterns":null,
		"timeout_seconds":60,
		"max_background_seconds":300
	}}}`)
	out := captureExecConfigWarnLogs(t, raw)
	assert.Empty(t, out, "seeded defaults alone must not trigger the deprecation warning; got: %s", out)
}

func TestWarnDeprecatedExecConfigFields_NoExecKey_NoOp(t *testing.T) {
	require.NotPanics(t, func() {
		out := captureExecConfigWarnLogs(t, []byte(`{"tools":{"web":{"enabled":true}}}`))
		assert.Empty(t, out)
	})
}

func TestWarnDeprecatedExecConfigFields_EmptyOrNilRaw_NoOp(t *testing.T) {
	require.NotPanics(t, func() {
		warnDeprecatedExecConfigFields(nil)
		warnDeprecatedExecConfigFields([]byte(""))
	})
}

func TestWarnDeprecatedExecConfigFields_MalformedJSON_NoPanic(t *testing.T) {
	require.NotPanics(t, func() {
		warnDeprecatedExecConfigFields([]byte(`{not valid json`))
	})
}

// TestWarnDeprecatedExecConfigFields_WarnOnce verifies the sync.Once guard:
// the Warn-level log fires at most once per process lifetime even across
// repeated loads/hot-reloads that each carry the same operator-customized
// value, mirroring TestMigrateDeprecatedToolEnableFlags_MigrateOnce.
func TestWarnDeprecatedExecConfigFields_WarnOnce(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })

	deprecatedExecConfigWarnOnce = sync.Once{}

	raw := []byte(`{"tools":{"exec":{"custom_deny_patterns":["curl.*\\|.*sh"]}}}`)
	warnDeprecatedExecConfigFields(raw)
	warnDeprecatedExecConfigFields(raw)

	lineCount := len(splitLines(buf.String()))
	assert.Equal(t, 1, lineCount,
		"the Warn-once log must fire exactly once across two calls; got %d lines: %q", lineCount, buf.String())
}

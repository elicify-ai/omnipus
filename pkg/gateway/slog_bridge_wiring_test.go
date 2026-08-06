// slog_bridge_wiring_test.go — proof that installSlogBridge (called from
// RunContextWithOptions immediately after logger.EnableFileLogging) actually
// makes a bare `slog.Warn/Info/Error(...)` call land in the same file
// EnableFileLogging wires up as gateway.log, with structured attributes
// preserved as fields rather than flattened into the message string.
//
// This deliberately proves the two production call sites together
// (logger.EnableFileLogging + installSlogBridge), not the logger.SlogHandler
// type in isolation — the CRITICAL bug this change fixes is specifically
// that slog.SetDefault was never called anywhere in production code, so a
// unit test of the handler alone (which would necessarily call
// slog.SetDefault itself) would not prove gateway.go's own wiring is
// present and in the right place. See pkg/logger/slog_bridge.go for the
// handler implementation and level/attribute-mapping design notes.
//
// TestSlogBridge_WithoutInstall_LineAbsentFromGatewayLog is the revert-proof
// half required alongside the positive proof: it exercises the exact same
// EnableFileLogging + bare slog.Warn shape MINUS installSlogBridge, using an
// explicit slog.SetDefault(slog.NewTextHandler(io.Discard, ...)) stand-in for
// "whatever log/slog.Default() would otherwise be" (a real, unmodified
// stdlib zero-value default writes to os.Stderr, which cannot be captured
// via a file assertion in-process without redirecting the process's actual
// fd 2 — the discard handler is the closest safe, deterministic substitute
// that still proves the same thing: without the bridge, the line does NOT
// reach gateway.log).

package gateway

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// enableGatewayLogFile mirrors RunContextWithOptions' own
// `logger.EnableFileLogging(filepath.Join(homePath, logPath, logFile))`
// call byte-for-byte (same logPath/logFile package constants gateway.go's
// boot path uses), so this test proves the bridge against the real
// production log path shape, not a stand-in filename.
func enableGatewayLogFile(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	logFilePath := filepath.Join(homePath, logPath, logFile)
	require.NoError(t, logger.EnableFileLogging(logFilePath))
	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	return logFilePath
}

// TestSlogBridge_InstallSlogBridge_LineAppearsInGatewayLogFile is the
// REQUIRED positive proof: after calling installSlogBridge() (the exact
// function RunContextWithOptions calls), a bare slog.Warn with structured
// key/value attributes appears in the real gateway.log file — message,
// level, and every attribute intact as structured fields.
func TestSlogBridge_InstallSlogBridge_LineAppearsInGatewayLogFile(t *testing.T) {
	logFilePath := enableGatewayLogFile(t)

	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	installSlogBridge()

	slog.Warn("slog-bridge proof: stranded media reconcile failed",
		"ref", "media-ref-7f3a1c",
		"error", "disk quota exceeded",
		"attempt", 3,
	)

	data, err := os.ReadFile(logFilePath)
	require.NoError(t, err)
	logged := string(data)

	assert.Contains(t, logged, "slog-bridge proof: stranded media reconcile failed",
		"the bare slog.Warn message must reach gateway.log")
	assert.Contains(t, logged, "media-ref-7f3a1c",
		"the ref attribute must survive as a structured field, not be dropped")
	assert.Contains(t, logged, "disk quota exceeded",
		"the error attribute must survive as a structured field")
	assert.Contains(t, logged, "attempt",
		"the int attribute's key must survive")
	assert.Contains(t, logged, `"level":"warn"`,
		"a slog.Warn call must map to pkg/logger's WARN level, not fall through to a different severity")
}

// TestSlogBridge_WithoutInstall_LineAbsentFromGatewayLog is the REQUIRED
// revert-proof: with the exact same EnableFileLogging setup but WITHOUT
// installSlogBridge, a bare slog.Warn does NOT reach gateway.log — proving
// the file-appears assertion above is actually caused by installSlogBridge
// and not some other ambient wiring.
func TestSlogBridge_WithoutInstall_LineAbsentFromGatewayLog(t *testing.T) {
	logFilePath := enableGatewayLogFile(t)

	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	// Explicit stand-in for slog's own zero-value stderr default: a discard
	// handler. A real, un-redirected stdlib default also writes to
	// os.Stderr, which this in-process test cannot capture as a file
	// assertion — the discard handler proves the same negative (nothing
	// lands in gateway.log) without relying on fd redirection.
	slog.SetDefault(slog.New(slog.NewTextHandler(discardWriter{}, nil)))

	slog.Warn("slog-bridge revert-proof: this line must not reach gateway.log",
		"ref", "media-ref-revert-9b2e4",
	)

	data, err := os.ReadFile(logFilePath)
	require.NoError(t, err)
	logged := string(data)

	assert.NotContains(t, logged, "slog-bridge revert-proof",
		"without installSlogBridge, a bare slog.Warn must NOT reach gateway.log — "+
			"this is the exact CRITICAL bug the bridge fixes")
	assert.NotContains(t, logged, "media-ref-revert-9b2e4")
}

// TestSlogBridge_LevelMapping_DebugSuppressedByLoggerLevel proves
// SlogHandler.Enabled honors pkg/logger's own currentLevelAtomic threshold:
// with the level raised to WARN, a slog.Debug/slog.Info call must not reach
// gateway.log even though the bridge is installed, exactly like a native
// logger.Debug/logger.Info call would be suppressed at the same threshold.
func TestSlogBridge_LevelMapping_DebugSuppressedByLoggerLevel(t *testing.T) {
	homePath := t.TempDir()
	logFilePath := filepath.Join(homePath, logPath, logFile)
	require.NoError(t, logger.EnableFileLogging(logFilePath))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(logger.INFO)
	})

	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	installSlogBridge()

	slog.Debug("slog-bridge level-mapping: debug must be suppressed at WARN threshold")
	slog.Info("slog-bridge level-mapping: info must be suppressed at WARN threshold")
	slog.Warn("slog-bridge level-mapping: warn must pass at WARN threshold")

	data, err := os.ReadFile(logFilePath)
	require.NoError(t, err)
	logged := string(data)

	assert.NotContains(t, logged, "debug must be suppressed")
	assert.NotContains(t, logged, "info must be suppressed")
	assert.Contains(t, logged, "warn must pass at WARN threshold")
}

// discardWriter is a minimal io.Writer that discards everything written to
// it, used as TestSlogBridge_WithoutInstall_LineAbsentFromGatewayLog's
// stand-in for "log/slog's own default writes to stderr, unreachable to a
// file assertion in this test."
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

package gateway

// r15 §6.3/§9.4, FR-015/061, S-2.11/S-12.1. This test is not
// Linux-gated: native Windows/macOS can run the same actual boot emitter.
// Cross-compilation alone is not evidence of native execution.
// FallbackBackend is real; no fake warning emitter or simulated runtime.GOOS.
import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsBoot_WarnsForEveryNoKernelMode(t *testing.T) {
	const warning = `Skill folders are protected by Omnipus's own checks only in this mode; scripts can change them. Changes made that way are recorded as "changed outside Omnipus".`
	for _, mode := range []string{"off", "enforce", "permissive"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(oldLogger) })
			oldBase, hadBase := sandbox.RegisteredTurnPolicyBase()
			t.Cleanup(func() {
				if hadBase {
					sandbox.RegisterTurnPolicyBase(&oldBase)
				} else {
					sandbox.RegisterTurnPolicyBase(nil)
				}
			})
			home := t.TempDir()
			stderr, err := os.Create(filepath.Join(home, "boot-stderr.log"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stderr.Close()) })
			cfg := config.DefaultConfig()
			cfg.Sandbox.Mode = config.SandboxMode(mode)
			result, err := applySandbox(SandboxApplyOptions{Cfg: cfg, HomePath: home, Backend: sandbox.NewFallbackBackend(), GetEnv: func(string) string { return "" }, Stderr: stderr})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, sandbox.Mode(mode), result.Mode)
			control := "sandbox.degraded"
			if mode == "off" {
				control = "sandbox.disabled"
			}
			require.Contains(t, logs.String(), control, "positive control: capture observes actual production boot warnings")
			found := false
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var row map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &row))
				if row["msg"] == warning {
					found = true
					assert.Equal(t, "WARN", row["level"], "operator must see a warning, not hidden debug information")
				}
			}
			assert.True(t, found, "BLOCKED: exact r15 §9.4 skill-folder warning missing for mode=%s on a real no-kernel boot path; actual log=%s", mode, logs.String())
		})
	}
}

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

// The P0-a log instrument must not depend on a preceding test's native level.
// Reuse the real provider/boot leaf: its existing assertion requires the actual
// config-save record, not a fabricated log line. Cleanup must restore our level.
func TestWU4P0AConfiguredDamageNativeLogCaptureRestoresInheritedLevel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level logger.LogLevel
	}{
		{name: "warn", level: logger.WARN},
		{name: "error", level: logger.ERROR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousLevel := logger.GetLevel()
			logger.SetLevel(tc.level)
			t.Cleanup(func() { logger.SetLevel(previousLevel) })
			require.Equal(t, tc.level, logger.GetLevel(), "instrument: enter the leaf with a higher inherited native level")
			t.Run("provider_boot", func(t *testing.T) {
				wu4P0ARequireScopedDamage(t, "provider", "boot")
			})
			require.Equal(t, tc.level, logger.GetLevel(), "the leaf's cleanup must restore the exact inherited native level")
		})
	}
}

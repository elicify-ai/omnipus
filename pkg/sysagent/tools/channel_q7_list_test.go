package systools_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQ7T23_ConfiguredAbsentOutboundDefaultsToOnlyAnswer_PositiveControl(t *testing.T) {
	// FR-034: configured-without-property differs from not-configured. The
	// existing T-23/S-37 roots stay untouched and run alongside this control.
	deps, cfg := newTestDeps(t)
	seedListedOutbound(cfg)
	inst := cfg.Channels["discord"]
	inst.Outbound = nil
	cfg.Channels["discord"] = inst
	row := t23Row(t, deps, "discord")
	require.Equal(t, map[string]any{"policy": "reply_only", "targets": []any{}}, row["outbound"], "FR-034: exact absent-property default on a configured connection")
}

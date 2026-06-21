package config

import "testing"

// TestHeartbeatIntervalMinutes_Clamp covers the per-agent heartbeat interval
// resolution (O6): a sub-5 explicit interval floors at 5, a <=0 interval falls
// back to the global default (itself floored at 5), and an in-range value passes
// through unchanged.
func TestHeartbeatIntervalMinutes_Clamp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		agentInterval int
		globalDefault int
		want          int
	}{
		{"explicit below floor → 5", 3, 30, 5},
		{"explicit at floor → 5", 5, 30, 5},
		{"explicit above floor passes through", 20, 30, 20},
		{"zero → global default", 0, 30, 30},
		{"negative → global default", -1, 30, 30},
		{"zero with sub-floor global → floored to 5", 0, 2, 5},
		{"zero with zero global → floored to 5", 0, 0, 5},
		{"large explicit passes through", 1440, 30, 1440},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := AgentConfig{HeartbeatInterval: c.agentInterval}
			if got := a.HeartbeatIntervalMinutes(c.globalDefault); got != c.want {
				t.Errorf("HeartbeatIntervalMinutes(%d) with agent=%d = %d, want %d",
					c.globalDefault, c.agentInterval, got, c.want)
			}
		})
	}
}

// TestHeartbeatIsEnabled covers the opt-in tri-state: nil pointer (unset) →
// disabled; explicit false → disabled; explicit true → enabled.
func TestHeartbeatIsEnabled(t *testing.T) {
	t.Parallel()
	tru, fls := true, false
	cases := []struct {
		name string
		ptr  *bool
		want bool
	}{
		{"nil → disabled", nil, false},
		{"false → disabled", &fls, false},
		{"true → enabled", &tru, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := AgentConfig{HeartbeatEnabled: c.ptr}
			if got := a.HeartbeatIsEnabled(); got != c.want {
				t.Errorf("HeartbeatIsEnabled() = %v, want %v", got, c.want)
			}
		})
	}
}

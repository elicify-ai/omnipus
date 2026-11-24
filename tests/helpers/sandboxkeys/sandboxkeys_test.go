package sandboxkeys

// Instrument tests, not product tests: they show that the helper can see the
// failures it exists to expose. A passing run proves nothing about the product.
// The fake loop copies the contract of a reload (a candidate copy in, live only
// when the publisher makes it live) so each failure path of the helper can be
// driven: an uncarried key, a decoder rejection, a refused reload and a
// publisher that returns without publishing.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

type fakeLoop struct {
	cfg       *config.Config
	calls     int   // publish calls, successful or not
	published int   // publish calls that made the candidate live
	publishes bool  // false: publish returns nil without making the candidate live
	refuse    error // non-nil: publish refuses the reload with this error
}

func newFakeLoop(t *testing.T, publishes bool) *fakeLoop {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sandbox.ToolPolicies = map[string]string{"read_file": "allow", "write_file": "allow"}
	cfg.Sandbox.AutoApprove = false
	return &fakeLoop{cfg: cfg, publishes: publishes}
}

func (f *fakeLoop) live() *config.Config { return f.cfg }

func (f *fakeLoop) publish(next *config.Config) error {
	f.calls++
	if f.refuse != nil {
		return f.refuse
	}
	if f.publishes {
		f.cfg = next
		f.published++
	}
	return nil
}

func TestSandboxKeys_CarriedMapKeyReachesTheLiveConfig(t *testing.T) {
	loop := newFakeLoop(t, true)
	before := loop.cfg
	require.NoError(t, Apply(loop.live, loop.publish, map[string]any{"tool_policies": map[string]string{"read_file": "deny"}}))
	require.Equal(t, 1, loop.published)
	require.NotSame(t, before, loop.cfg, "the candidate is a copy, not the previous live object")
	require.Equal(t, "allow", before.Sandbox.ToolPolicies["read_file"], "the previous live object is never mutated in place")
	require.Equal(t, "deny", loop.cfg.Sandbox.ToolPolicies["read_file"], "the written entry is live")
	require.Equal(t, "allow", loop.cfg.Sandbox.ToolPolicies["write_file"], "an unwritten entry is kept")
}

func TestSandboxKeys_CarriedScalarKeyReachesTheLiveConfig(t *testing.T) {
	loop := newFakeLoop(t, true)
	require.False(t, loop.cfg.Sandbox.AutoApprove, "instrument precondition: the scalar starts off")
	require.NoError(t, Apply(loop.live, loop.publish, map[string]any{"auto_approve": true}))
	require.True(t, loop.cfg.Sandbox.AutoApprove)
}

func TestSandboxKeys_ADecoderRejectionIsNotCarriedAndNothingIsPublished(t *testing.T) {
	loop := newFakeLoop(t, true)
	// No release accepts this trust word: the real SkillTrustLevel decoder
	// rejects it, which is the same "config cannot carry it" obligation.
	err := Apply(loop.live, loop.publish, map[string]any{"skill_trust": "no_such_trust_vocabulary"})
	require.ErrorIs(t, err, ErrNotCarried)
	require.Contains(t, err.Error(), "sandbox.skill_trust: the config decoder rejects it")
	require.Zero(t, loop.calls, "a refused write never reaches the reload")
}

func TestSandboxKeys_UncarriedKeyIsNotCarriedAndNothingIsPublished(t *testing.T) {
	loop := newFakeLoop(t, true)
	err := Apply(loop.live, loop.publish, map[string]any{"no_such_sandbox_key": map[string]string{"x": "y"}})
	require.ErrorIs(t, err, ErrNotCarried)
	require.Contains(t, err.Error(), "sandbox.no_such_sandbox_key")
	require.Zero(t, loop.calls, "a refused write never reaches the reload")
	require.Equal(t, "allow", loop.cfg.Sandbox.ToolPolicies["read_file"])
}

func TestSandboxKeys_PartiallyCarriedPatchPublishesNothing(t *testing.T) {
	loop := newFakeLoop(t, true)
	err := Apply(loop.live, loop.publish, map[string]any{
		"tool_policies":       map[string]string{"read_file": "deny"},
		"no_such_sandbox_key": "x",
	})
	require.ErrorIs(t, err, ErrNotCarried)
	require.Zero(t, loop.calls)
	require.Equal(t, "allow", loop.cfg.Sandbox.ToolPolicies["read_file"], "the carried half is not published alone")
}

func TestSandboxKeys_TheFirstKeyTheProductCannotCarryIsReportedInSortedOrder(t *testing.T) {
	loop := newFakeLoop(t, true)
	// skill_trust is rejected by the decoder and no_such_a is silently dropped;
	// sorted order puts no_such_a first, and that is the key reported.
	err := Apply(loop.live, loop.publish, map[string]any{"skill_trust": "no_such_trust_vocabulary", "no_such_a": "x"})
	require.ErrorIs(t, err, ErrNotCarried)
	require.Contains(t, err.Error(), "sandbox.no_such_a")
	require.NotContains(t, err.Error(), "skill_trust")
	require.Zero(t, loop.calls)
}

func TestSandboxKeys_ANoOpPublisherIsNotPublished(t *testing.T) {
	loop := newFakeLoop(t, false)
	err := Apply(loop.live, loop.publish, map[string]any{"tool_policies": map[string]string{"read_file": "deny"}})
	require.ErrorIs(t, err, ErrNotPublished)
	require.False(t, errors.Is(err, ErrNotCarried), "a publisher that drops the write is not a missing config key")
	require.Equal(t, 1, loop.calls, "the candidate did reach the reload")
	require.Equal(t, "allow", loop.cfg.Sandbox.ToolPolicies["read_file"], "the live object holds the old entry")
}

func TestSandboxKeys_ARefusedReloadIsAnInstrumentFailureNotAMissingKey(t *testing.T) {
	loop := newFakeLoop(t, true)
	loop.refuse = errors.New("reload failed in the harness")
	err := Apply(loop.live, loop.publish, map[string]any{"tool_policies": map[string]string{"read_file": "deny"}})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrNotCarried), "a refused reload must not read as a missing config key")
	require.False(t, errors.Is(err, ErrNotPublished))
	require.Contains(t, err.Error(), "the loop refused the reload: reload failed in the harness")
	require.Zero(t, loop.published)
}

func TestSandboxKeys_ContainsDistinguishesEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want any
		held      bool
	}{
		{"equal scalar", "deny", "deny", true},
		{"different scalar", "allow", "deny", false},
		{"extra stored entries are allowed", map[string]any{"a": "1", "b": "2"}, map[string]any{"a": "1"}, true},
		{"missing written entry", map[string]any{"b": "2"}, map[string]any{"a": "1"}, false},
		{"different written entry", map[string]any{"a": "2"}, map[string]any{"a": "1"}, false},
		{"stored scalar where an object was written", "x", map[string]any{"a": "1"}, false},
	} {
		require.Equal(t, tc.held, contains(tc.got, tc.want), tc.name)
	}
}

// fatalCapture records a Fatalf instead of ending the test, so the BLOCKED text
// can be read.
type fatalCapture struct {
	testing.TB
	message string
}

type stopped struct{}

func (f *fatalCapture) Fatalf(format string, args ...any) {
	f.message = fmt.Sprintf(format, args...)
	panic(stopped{})
}

func capture(t *testing.T, run func(testing.TB)) (message string) {
	t.Helper()
	tb := &fatalCapture{TB: t}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(stopped); !ok {
				panic(r)
			}
			message = tb.message
		}
	}()
	run(tb)
	return ""
}

func TestSandboxKeys_StoreSaysBLOCKEDOnlyForAnUncarriedKey(t *testing.T) {
	loop := newFakeLoop(t, true)
	blocked := capture(t, func(tb testing.TB) {
		Store(tb, loop.live, loop.publish, map[string]any{"skill_visibility": map[string]string{"notes-helper": "full"}}, "D9 row 5 (S-89)")
	})
	require.True(t, strings.HasPrefix(blocked, "BLOCKED: config does not carry the key: sandbox.skill_visibility"), blocked)
	require.Contains(t, blocked, "required by D9 row 5 (S-89)")

	instrument := capture(t, func(tb testing.TB) {
		quiet := newFakeLoop(t, false)
		Store(tb, quiet.live, quiet.publish, map[string]any{"tool_policies": map[string]string{"read_file": "deny"}}, "row")
	})
	require.True(t, strings.HasPrefix(instrument, "sandboxkeys instrument failure"), instrument)
	require.NotContains(t, instrument, "BLOCKED")

	require.Empty(t, capture(t, func(tb testing.TB) {
		Store(tb, loop.live, loop.publish, map[string]any{"auto_approve": true}, "row")
	}), "a carried key stores without a fatal")
	require.True(t, loop.cfg.Sandbox.AutoApprove)
}

package sandboxkeys

// Instrument tests, not product tests: they show that the helper can see the
// failures it exists to expose. A passing run proves nothing about the product.
// The fake loop copies the contract of a reload (a candidate copy in, live only
// when the publisher makes it live) so each failure path of the helper can be
// driven: an uncarried key, a decoder rejection, a refused reload and a
// publisher that returns without publishing.

import (
	"encoding/json"
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

// impossibleSandboxKey is a key no release will ever carry. The instrument that
// shows Store says BLOCKED for an uncarried key must use a key like this one and
// never a key the spec requires the product to carry: when the product carries
// sandbox.skill_visibility (C-6) a required-key instrument would fail the correct
// implementation (CHECK round 4, N17). The real spec keys are covered by the
// stand-in controls and the either-state test below.
const impossibleSandboxKey = "instrument_key_that_no_release_carries"

func TestSandboxKeys_StoreSaysBLOCKEDOnlyForAnUncarriedKey(t *testing.T) {
	loop := newFakeLoop(t, true)
	blocked := capture(t, func(tb testing.TB) {
		Store(tb, loop.live, loop.publish, map[string]any{impossibleSandboxKey: map[string]string{"notes-helper": "full"}}, "D9 row 5 (S-89)")
	})
	require.True(t, strings.HasPrefix(blocked, "BLOCKED: config does not carry the key: sandbox."+impossibleSandboxKey), blocked)
	require.Contains(t, blocked, "required by D9 row 5 (S-89)")
	require.Zero(t, loop.calls, "a BLOCKED write never reaches the reload")
	require.Zero(t, loop.published)

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

// standInSandbox is the sandbox section of a product that DOES carry the three
// stores the spec requires: C-2 skill_policies and C-6 tool_visibility and
// skill_visibility. The controls below hand it to the instrument so they hold
// whether or not the product under test carries those keys yet.
type standInSandbox struct {
	SkillPolicies   map[string]string `json:"skill_policies,omitempty"`
	ToolVisibility  map[string]string `json:"tool_visibility,omitempty"`
	SkillVisibility map[string]string `json:"skill_visibility,omitempty"`
}

// specRequiredKeys lists the stores the spec requires the product to carry, each
// with literal spec values: C-2 gives a skill's global permission as allow, ask
// or deny; C-6 gives what the model sees as hidden, name_only,
// name_and_description or full. written is what a test stores; other is a
// different stored value of the same store; differentMessage is the exact text the
// instrument gives when the product holds other instead of written.
var specRequiredKeys = []struct {
	key              string
	written, other   map[string]string
	holding          func(map[string]string) standInSandbox
	differentMessage string
}{
	{
		key: "skill_policies", written: map[string]string{"notes-helper": "deny"}, other: map[string]string{"notes-helper": "ask"},
		holding:          func(m map[string]string) standInSandbox { return standInSandbox{SkillPolicies: m} },
		differentMessage: "config does not carry the key: sandbox.skill_policies holds map[notes-helper:ask], written map[notes-helper:deny]",
	},
	{
		key: "tool_visibility", written: map[string]string{"read_file": "name_only"}, other: map[string]string{"read_file": "full"},
		holding:          func(m map[string]string) standInSandbox { return standInSandbox{ToolVisibility: m} },
		differentMessage: "config does not carry the key: sandbox.tool_visibility holds map[read_file:full], written map[read_file:name_only]",
	},
	{
		key: "skill_visibility", written: map[string]string{"notes-helper": "full"}, other: map[string]string{"notes-helper": "name_only"},
		holding:          func(m map[string]string) standInSandbox { return standInSandbox{SkillVisibility: m} },
		differentMessage: "config does not carry the key: sandbox.skill_visibility holds map[notes-helper:name_only], written map[notes-helper:full]",
	},
}

// asNormalised turns the table's literal entries into the shape Apply compares
// (a JSON-decoded object), written out by hand so the control does not borrow the
// instrument's own encoding.
func asNormalised(entries map[string]string) map[string]any {
	out := make(map[string]any, len(entries))
	for name, level := range entries {
		out[name] = level
	}
	return out
}

func TestSandboxKeys_CarriedAcceptsEverySpecRequiredKeyOnceAProductCarriesIt(t *testing.T) {
	for _, tc := range specRequiredKeys {
		t.Run(tc.key, func(t *testing.T) {
			want := map[string]any{tc.key: asNormalised(tc.written)}
			require.NoError(t, carried(tc.holding(tc.written), want, ErrNotCarried),
				"a product that carries sandbox.%s and holds the written entry is accepted", tc.key)
		})
	}
}

func TestSandboxKeys_CarriedRejectsASpecRequiredKeyThatIsMissingOrDifferent(t *testing.T) {
	for _, tc := range specRequiredKeys {
		t.Run(tc.key, func(t *testing.T) {
			want := map[string]any{tc.key: asNormalised(tc.written)}

			missing := carried(standInSandbox{}, want, ErrNotCarried)
			require.ErrorIs(t, missing, ErrNotCarried)
			require.EqualError(t, missing, "config does not carry the key: sandbox."+tc.key)

			different := carried(tc.holding(tc.other), want, ErrNotCarried)
			require.ErrorIs(t, different, ErrNotCarried)
			require.EqualError(t, different, tc.differentMessage)

			notPublished := carried(tc.holding(tc.other), want, ErrNotPublished)
			require.ErrorIs(t, notPublished, ErrNotPublished)
			require.False(t, errors.Is(notPublished, ErrNotCarried), "the kind the caller names is the kind reported")
		})
	}
}

// The real product configuration may or may not carry a required key yet. Whichever
// it is, the instrument must say so truthfully: the write is carried and published,
// or it is exactly ErrNotCarried with nothing published. It must never answer with
// another error class, and a product that has since gained the key must not make it
// fail (CHECK round 4, N17). Neither outcome is a product verdict.
func TestSandboxKeys_ARequiredKeyIsEitherCarriedAndPublishedOrBlockedNeverAnInstrumentFailure(t *testing.T) {
	for _, tc := range specRequiredKeys {
		t.Run(tc.key, func(t *testing.T) {
			loop := newFakeLoop(t, true)
			err := Apply(loop.live, loop.publish, map[string]any{tc.key: tc.written})
			if err != nil {
				require.EqualError(t, err, "config does not carry the key: sandbox."+tc.key,
					"a product that does not carry the key yet is reported as exactly that")
				require.Zero(t, loop.calls, "an uncarried write never reaches the reload")
				require.Zero(t, loop.published)
				return
			}
			require.Equal(t, 1, loop.published, "a carried write is made live exactly once")
			stored, marshalErr := json.Marshal(loop.cfg.Sandbox)
			require.NoError(t, marshalErr)
			var section map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(stored, &section))
			written, marshalErr := json.Marshal(tc.written)
			require.NoError(t, marshalErr)
			require.JSONEq(t, string(written), string(section[tc.key]), "the live sandbox section holds the written entries")
		})
	}
}

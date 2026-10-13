// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U3 (ordinary intake), pkg/config slice.
//
// Spec: docs/internal/specs/session-core-spec.md FR-010, FR-011; C-LIMIT (#1216).
// ADR: docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md.
// Every expected value comes from the spec's C-LIMIT rows, never the current code.

package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// C-LIMIT: "Existing steer_body/steer_rate types and zero/effective meanings
// unchanged; defaults 65,536 UTF-8 bytes and 60/minute/sender+target."
// FR-010: the ordinary intake applies that body cap and rate cap.
//
// Oracle: the spec's stated defaults. The current constants are 16 KiB / 6 per
// minute; these must become 65,536 bytes / 60 per minute.
func TestSessionCoreU3_OrdinaryBodyCapDefaultIs65536Bytes(t *testing.T) {
	var c SessionMessagingConfig
	assert.Equal(t, 65536, c.EffectiveSteerBodyBytes(),
		"C-LIMIT: the ordinary steer/body default must be 65,536 UTF-8 bytes (FR-010)")
}

func TestSessionCoreU3_OrdinaryRateDefaultIsSixtyPerMinute(t *testing.T) {
	var c SessionMessagingConfig
	assert.Equal(t, 60, c.EffectiveSteerRatePerMinute(),
		"C-LIMIT: the ordinary rate default must be 60/minute per trusted sender+target (FR-010)")
}

// C-LIMIT: "Sole new setting session_messaging.steer_aggregate_body: 1,048,576
// bytes; ordinary item count 200."
//
// Oracle: the spec names the wire key `steer_aggregate_body` and its default
// 1,048,576 bytes. Two facts are load-bearing: (1) the key must decode into a
// real field — pkg/config silently ignores unknown keys, so a missing field
// would swallow an operator's value with no error; (2) when unset it must
// resolve to the spec default, not zero.
func TestSessionCoreU3_AggregateBodySettingExistsAndDefaultsTo1048576(t *testing.T) {
	rt := reflect.TypeOf(SessionMessagingConfig{})

	// (1) The key must be honoured: decoding it must populate a field.
	var c SessionMessagingConfig
	require.NoError(t, json.Unmarshal([]byte(`{"steer_aggregate_body":1048576}`), &c))

	val, ok := intFieldByJSONTag(reflect.ValueOf(c), "steer_aggregate_body")
	require.True(t, ok,
		"FR-010/C-LIMIT: session_messaging must expose the sole new setting steer_aggregate_body; present json keys: %v",
		jsonTagNames(rt))
	assert.Equal(t, 1048576, val, "C-LIMIT: an explicit steer_aggregate_body value must be honoured")

	// (2) Effective default when unset must be the spec value.
	var zero SessionMessagingConfig
	eff, ok := effectiveAggregateBody(reflect.ValueOf(zero))
	require.True(t, ok,
		"C-LIMIT: SessionMessagingConfig must expose an effective aggregate-body resolver (default 1,048,576 bytes)")
	assert.Equal(t, 1048576, eff,
		"C-LIMIT: an unset steer_aggregate_body must resolve to the spec default 1,048,576 bytes")
}

// TestSessionCoreU3_AggregateBodyResolverHonoursExplicitOverride closes the
// oracle gap CHECK found in TestSessionCoreU3_AggregateBodySettingExistsAnd-
// DefaultsTo1048576: that test only ever resolved the ZERO config, so a resolver
// mutated to ignore the field and always return the default still passed. This
// drives a DISTINCT explicit value end-to-end through the wire key and the
// resolver, and pins the min/zero/negative fallbacks.
func TestSessionCoreU3_AggregateBodyResolverHonoursExplicitOverride(t *testing.T) {
	// (1) An explicit operator value must be honoured by the resolver — the
	// value chosen (2 MiB) is deliberately distinct from the 1,048,576 default,
	// so "resolver returned the default" cannot pass this assertion.
	var explicit SessionMessagingConfig
	require.NoError(t, json.Unmarshal([]byte(`{"steer_aggregate_body":2097152}`), &explicit),
		"C-LIMIT: an explicit steer_aggregate_body must decode into the config")
	assert.Equal(t, 2097152, explicit.EffectiveSteerAggregateBody(),
		"C-LIMIT: the resolver must return an explicit steer_aggregate_body, never the default")

	// (2) Boundary: the smallest positive value (min) is honoured verbatim.
	assert.Equal(t, 1, (SessionMessagingConfig{SteerAggregateBody: 1}).EffectiveSteerAggregateBody(),
		"C-LIMIT: steer_aggregate_body=1 (the min positive) must be honoured, not defaulted")

	// (3) Fallback: zero (unset) and negative both resolve to the spec default.
	assert.Equal(t, 1048576, (SessionMessagingConfig{}).EffectiveSteerAggregateBody(),
		"C-LIMIT: an unset (zero) steer_aggregate_body must resolve to the 1,048,576 default")
	assert.Equal(t, 1048576, (SessionMessagingConfig{SteerAggregateBody: -1}).EffectiveSteerAggregateBody(),
		"C-LIMIT: a negative steer_aggregate_body must resolve to the 1,048,576 default")
}

// intFieldByJSONTag returns the int value of the field whose json tag name is
// (tag). A non-int or absent field reports ok=false.
func intFieldByJSONTag(v reflect.Value, tag string) (int, bool) {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	rt := v.Type()
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name != tag {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int(f.Int()), true
		}
		return 0, false
	}
	return 0, false
}

// effectiveAggregateBody finds a no-arg method on SessionMessagingConfig whose
// name mentions Aggregate and Body and returns an int — the effective resolver
// C-LIMIT requires. Returns ok=false when no such method exists.
func effectiveAggregateBody(v reflect.Value) (int, bool) {
	for _, recv := range []reflect.Value{v, reflect.New(v.Type())} {
		rt := recv.Type()
		for i := 0; i < rt.NumMethod(); i++ {
			m := rt.Method(i)
			low := strings.ToLower(m.Name)
			if !strings.Contains(low, "aggregate") || !strings.Contains(low, "body") {
				continue
			}
			if m.Type.NumIn() != 1 || m.Type.NumOut() != 1 {
				continue
			}
			out := recv.Method(i).Call(nil)
			if len(out) == 1 && out[0].Kind() == reflect.Int {
				return int(out[0].Int()), true
			}
		}
	}
	return 0, false
}

func jsonTagNames(rt reflect.Type) []string {
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	return out
}

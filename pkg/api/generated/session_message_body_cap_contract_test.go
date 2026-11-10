// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The runtime steer/respond body ceiling is a configurable byte limit, default
// 65,536 UTF-8 bytes (config.DefaultSMSteerBodyBytes, session.DefaultSteerBodyBytes).
// The wire schemas that carry that body must accept every length the runtime
// accepts, or a delivered message is rejected by the SPA's generated validators.
// Architect ruling S2-21 (combined review, 2026-10-12): the schemas carry NO
// numeric maxLength at all - it counted characters, not bytes, and silently
// overrode an operator-raised limit - so the server's byte cap is the single
// bound. The former "one over the ceiling is refused" assertion is replaced by
// its inverse for that reason.
func TestContract_SteerRespondBodyCapMatchesRuntime(t *testing.T) {
	const runtimeCap = 65536

	envelope := func(kind string, text string, extra map[string]any) []byte {
		m := map[string]any{
			"message_id":       "msg_01J3ZQK8N2H8VXNRP5T7C9M4WV",
			"session_id":       "sid-child",
			"direction":        "parent_to_child",
			"kind":             kind,
			"depth":            1,
			"created_at":       "2026-07-22T10:00:00Z",
			"sender_identity":  "jim",
			"untrusted_origin": false,
			"text":             text,
		}
		for k, v := range extra {
			m[k] = v
		}
		b, err := json.Marshal(m)
		require.NoError(t, err)
		return b
	}

	cases := []struct {
		schema string
		body   []byte
		mk     func(text string) []byte
	}{
		{"SessionMessageSteer", nil, func(s string) []byte { return envelope("steer", s, nil) }},
		{"SessionMessageRespond", nil, func(s string) []byte {
			return envelope("respond", s, map[string]any{"correlation_id": "corr_1"})
		}},
		{"DelegateSteerAction", nil, func(s string) []byte {
			b, _ := json.Marshal(map[string]any{"action": "steer", "session_id": "sid-child", "text": s})
			return b
		}},
		{"DelegateRespondAction", nil, func(s string) []byte {
			b, _ := json.Marshal(map[string]any{"action": "respond", "session_id": "sid-child", "text": s, "correlation_id": "corr_1"})
			return b
		}},
	}
	for _, c := range cases {
		t.Run(c.schema, func(t *testing.T) {
			require.NoError(t, validateAgainstComponentSchemaRawJSON(t, c.schema, c.mk("ok")),
				"instrument: the baseline envelope must validate, or the cap assertions below prove nothing")
			require.NoError(t, validateAgainstComponentSchemaRawJSON(t, c.schema, c.mk(strings.Repeat("a", 16385))),
				"16,385 characters is within the runtime ceiling and must validate")
			require.NoError(t, validateAgainstComponentSchemaRawJSON(t, c.schema, c.mk(strings.Repeat("a", runtimeCap))),
				"exactly the runtime ceiling must validate")
			require.NoError(t, validateAgainstComponentSchemaRawJSON(t, c.schema, c.mk(strings.Repeat("a", runtimeCap+1))),
				"S2-21: the schema imposes no length cap; the server's steer_body byte limit is the only bound")
		})
	}
}

// DelegateRespondResponse is an acknowledgement only: no corrective session is
// ever created, so the wire type must not declare a field for one.
func TestContract_DelegateRespondResponseDeclaresNoCorrectiveSession(t *testing.T) {
	ack := []byte(`{"acknowledged": true}`)
	require.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DelegateRespondResponse", ack),
		"instrument: the bare acknowledgement must validate")

	withSession := []byte(`{"acknowledged": true, "corrective_session":
		{"session_id":"sid-1","generation":1,"is_3p":false,"state":"running"}}`)
	require.Error(t, validateAgainstComponentSchemaRawJSON(t, "DelegateRespondResponse", withSession),
		"corrective_session is never set and must not be a declared property")
}

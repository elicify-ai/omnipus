package gateway

import (
	"reflect"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The exact public setter signatures were supplied by chief and read on the
// pinned developer ref. Reflection is only a baseline-compatibility bridge:
// no private fields, unsafe access, invented implementation symbols or skips.
// The pre-room base still compiles its existing gateway tests and fails loudly.
type wu15RoomCommitWriter interface {
	AppendTranscriptOnce(string, session.TranscriptEntry) (bool, error)
}

func (h *wu15RoomHarness) roomWriter(writer wu15RoomCommitWriter) {
	h.t.Helper()
	setter := reflect.ValueOf(h.al).MethodByName("SetRoomReceiverWriter")
	if !setter.IsValid() {
		h.t.Fatal("BLOCKED: agent.AgentLoop.SetRoomReceiverWriter not implemented — required by FR-216 / chief Q-D")
	}
	require.Equal(h.t, 1, setter.Type().NumIn(), "named public writer setter signature")
	value := reflect.ValueOf(writer)
	if writer == nil {
		value = reflect.Zero(setter.Type().In(0))
	} else {
		require.True(h.t, value.Type().AssignableTo(setter.Type().In(0)), "writer implements the supplied public RoomReceiverWriter interface")
	}
	setter.Call([]reflect.Value{value})
}

func (h *wu15RoomHarness) callPricer(pricer func(string, providers.UsageInfo) float64) {
	h.t.Helper()
	setter := reflect.ValueOf(h.al).MethodByName("SetRoomCallPricer")
	if !setter.IsValid() {
		h.t.Fatal("BLOCKED: agent.AgentLoop.SetRoomCallPricer not implemented — required by §6.14 / BDD-17.30")
	}
	require.Equal(h.t, 1, setter.Type().NumIn())
	value := reflect.ValueOf(pricer)
	if pricer == nil {
		value = reflect.Zero(setter.Type().In(0))
	} else {
		require.True(h.t, value.Type().ConvertibleTo(setter.Type().In(0)), "public RoomCallPricer has the read model/usage signature")
		value = value.Convert(setter.Type().In(0))
	}
	setter.Call([]reflect.Value{value})
}

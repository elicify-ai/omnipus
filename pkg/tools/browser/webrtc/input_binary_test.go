package webrtc

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func TestInputBinaryGoldenAndStrictFraming(t *testing.T) {
	golden, err := hex.DecodeString("4f4249010700840b00010061000000000000f03f0000000000000000000000000000f03f0000000000000000")
	require.NoError(t, err)
	text, one, zero := "a", 1, 0
	want := generated.BrowserInputFrame{Type: "browser_input", Kind: "text", Text: &text, InputEpoch: &one, ControlEpoch: &zero, ReliableSeq: &one, GestureBarrier: &zero}
	actual, err := DecodeInputPacket(golden)
	require.NoError(t, err)
	require.Equal(t, want, actual)
	encoded, err := EncodeInputPacket(want)
	require.NoError(t, err)
	require.Equal(t, golden, encoded)
	for size := 0; size < len(golden); size++ {
		_, err := DecodeInputPacket(golden[:size])
		require.ErrorIs(t, err, errInputBinary, "prefix %d", size)
	}
	_, err = DecodeInputPacket(append(append([]byte(nil), golden...), 0))
	require.ErrorIs(t, err, errInputBinary)
	for _, fault := range [][2]int{{0, 0}, {3, 2}, {4, 0}, {4, 8}, {8, 128}, {9, 255}, {11, 255}} {
		bad := append([]byte(nil), golden...)
		bad[fault[0]] = byte(fault[1])
		_, err = DecodeInputPacket(bad)
		require.ErrorIs(t, err, errInputBinary)
	}
	_, err = DecodeInputPacket(make([]byte, 65537))
	require.ErrorIs(t, err, errInputBinary)
}

func TestInputBinaryPreservesUnicodeAndAllFields(t *testing.T) {
	for _, text := range []string{"", "Zażółć 世界 👋", strings.Repeat("🙂", 8192)} {
		one, zero, max, code, mods := 1, 0, 9007199254740991, 76, 1
		x, y, width, height, dx, dy := -0.5, 0.0, 640.5, 480.0, -1.25, 0.0
		key, physical, button, url, capture := "@", "KeyL", "left", "", "capture"
		want := generated.BrowserInputFrame{Type: "browser_input", Kind: "key_down", Text: &text, InputEpoch: &one, ControlEpoch: &zero, ReliableSeq: &one, HoverSeq: &one, GestureBarrier: &zero, X: &x, Y: &y, CaptureWidth: &width, CaptureHeight: &height, DeltaX: &dx, DeltaY: &dy, Key: &key, Code: &physical, KeyCode: &code, Modifiers: &mods, Button: &button, Url: &url, CaptureId: &capture, CaptureGeneration: &max}
		encoded, err := EncodeInputPacket(want)
		require.NoError(t, err)
		actual, err := DecodeInputPacket(encoded)
		require.NoError(t, err)
		require.Equal(t, want, actual)
	}
}

func TestInputBinaryRejectsUnsafeIntegerBeforeConversion(t *testing.T) {
	for _, value := range []float64{1.5, 9007199254740992, -9007199254740992, math.NaN(), math.Inf(1)} {
		// Independent header: text kind, input_epoch bit 15, one float64 value.
		packet := []byte{'O', 'B', 'I', 1, 7, 0, 128, 0, 0}
		packet = binary.LittleEndian.AppendUint64(packet, math.Float64bits(value))
		_, err := DecodeInputPacket(packet)
		require.ErrorIs(t, err, errInputBinary, "value %v", value)
	}
}

func TestInputBinaryIndependentFieldBits(t *testing.T) {
	cases := []struct {
		field string
		bit   uint
		text  bool
	}{
		{"x", 0, false}, {"y", 1, false}, {"capture_width", 2, false}, {"capture_height", 3, false},
		{"button", 4, true}, {"delta_x", 5, false}, {"delta_y", 6, false}, {"key", 7, true},
		{"code", 8, true}, {"key_code", 9, false}, {"text", 10, true}, {"modifiers", 11, false},
		{"url", 12, true}, {"capture_generation", 13, false}, {"capture_id", 14, true},
		{"input_epoch", 15, false}, {"control_epoch", 16, false}, {"reliable_seq", 17, false},
		{"hover_seq", 18, false}, {"gesture_barrier", 19, false},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			mask := uint32(1) << tc.bit
			packet := []byte{'O', 'B', 'I', 1, 7, byte(mask), byte(mask >> 8), byte(mask >> 16), 0}
			var value any = float64(1)
			if tc.text {
				value = "a"
				packet = append(packet, 1, 0, 97)
			} else {
				packet = append(packet, 0, 0, 0, 0, 0, 0, 240, 63)
			}
			expectedJSON, err := json.Marshal(map[string]any{"type": "browser_input", "kind": "text", tc.field: value})
			require.NoError(t, err)
			var expected generated.BrowserInputFrame
			require.NoError(t, json.Unmarshal(expectedJSON, &expected))
			decoded, err := DecodeInputPacket(packet)
			require.NoError(t, err)
			require.Equal(t, expected, decoded)
			encoded, err := EncodeInputPacket(expected)
			require.NoError(t, err)
			require.Equal(t, packet, encoded)
		})
	}
}

func TestInputBinaryPreservesBOMAndEmptyText(t *testing.T) {
	for _, tc := range []struct {
		text    string
		payload []byte
	}{{"\uFEFFa", []byte{4, 0, 239, 187, 191, 97}}, {"", []byte{0, 0}}} {
		packet := append([]byte{'O', 'B', 'I', 1, 7, 0, 4, 0, 0}, tc.payload...)
		want := generated.BrowserInputFrame{Type: "browser_input", Kind: "text", Text: &tc.text}
		got, err := DecodeInputPacket(packet)
		require.NoError(t, err)
		require.Equal(t, want, got)
		encoded, err := EncodeInputPacket(want)
		require.NoError(t, err)
		require.Equal(t, packet, encoded)
	}
}

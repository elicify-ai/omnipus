package webrtc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

const InputBinaryProtocol = "omnipus.input.v1"
const inputBinaryMaxBytes = 64 * 1024

var inputBinaryKinds = [...]string{"mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text"}
var inputBinaryFields = [...]string{"x", "y", "capture_width", "capture_height", "button", "delta_x", "delta_y", "key", "code", "key_code", "text", "modifiers", "url", "capture_generation", "capture_id", "input_epoch", "control_epoch", "reliable_seq", "hover_seq", "gesture_barrier"}
var errInputBinary = errors.New("invalid browser input binary packet")

func inputBinaryString(field string) bool {
	switch field {
	case "button", "key", "code", "text", "url", "capture_id":
		return true
	}
	return false
}

// EncodeInputPacket is the Go fixture encoder; production sending is TypeScript.
// It preserves field presence for v1. Semantic validation remains
// the receiver's existing generated schema and dedicated queue admission.
func EncodeInputPacket(frame generated.BrowserInputFrame) ([]byte, error) {
	if frame.Type != "browser_input" {
		return nil, errInputBinary
	}
	kind := byte(0)
	for i, name := range inputBinaryKinds {
		if frame.Kind == name {
			kind = byte(i + 1)
		}
	}
	if kind == 0 {
		return nil, errInputBinary
	}
	for _, value := range []*string{frame.Button, frame.Key, frame.Code, frame.Text, frame.Url, frame.CaptureId} {
		if value != nil && !utf8.ValidString(*value) {
			return nil, errInputBinary
		}
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return nil, errInputBinary
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, errInputBinary
	}
	packet := []byte{'O', 'B', 'I', 1, kind, 0, 0, 0, 0}
	var mask uint32
	for bit, field := range inputBinaryFields {
		value, present := values[field]
		if !present {
			continue
		}
		mask |= 1 << bit
		if inputBinaryString(field) {
			value, ok := value.(string)
			if !ok || !utf8.ValidString(value) || len(value) > math.MaxUint16 {
				return nil, errInputBinary
			}
			packet = binary.LittleEndian.AppendUint16(packet, uint16(len(value)))
			packet = append(packet, value...)
		} else {
			value, ok := value.(float64)
			if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errInputBinary
			}
			packet = binary.LittleEndian.AppendUint64(packet, math.Float64bits(value))
		}
		if len(packet) > inputBinaryMaxBytes {
			return nil, errInputBinary
		}
	}
	binary.LittleEndian.PutUint32(packet[5:9], mask)
	return packet, nil
}

// DecodeInputPacket rejects malformed framing without interpreting it as JSON.
// The caller must still apply the existing schema and ownership checks.
func DecodeInputPacket(packet []byte) (generated.BrowserInputFrame, error) {
	var frame generated.BrowserInputFrame
	if len(packet) < 9 || len(packet) > inputBinaryMaxBytes || string(packet[:3]) != "OBI" || packet[3] != 1 || packet[4] < 1 || int(packet[4]) > len(inputBinaryKinds) {
		return frame, errInputBinary
	}
	mask := binary.LittleEndian.Uint32(packet[5:9])
	if mask>>len(inputBinaryFields) != 0 {
		return frame, errInputBinary
	}
	frame.Type, frame.Kind = "browser_input", inputBinaryKinds[packet[4]-1]
	offset := 9
	for bit, field := range inputBinaryFields {
		if mask&(1<<bit) == 0 {
			continue
		}
		if inputBinaryString(field) {
			if offset+2 > len(packet) {
				return frame, errInputBinary
			}
			size := int(binary.LittleEndian.Uint16(packet[offset : offset+2]))
			offset += 2
			if offset+size > len(packet) || !utf8.Valid(packet[offset:offset+size]) {
				return frame, errInputBinary
			}
			value := string(packet[offset : offset+size])
			switch field {
			case "button":
				frame.Button = &value
			case "key":
				frame.Key = &value
			case "code":
				frame.Code = &value
			case "text":
				frame.Text = &value
			case "url":
				frame.Url = &value
			case "capture_id":
				frame.CaptureId = &value
			}

			offset += size
		} else {
			if offset+8 > len(packet) {
				return frame, errInputBinary
			}
			value := math.Float64frombits(binary.LittleEndian.Uint64(packet[offset : offset+8]))
			offset += 8
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return frame, errInputBinary
			}
			switch field {
			case "x":
				frame.X = &value
			case "y":
				frame.Y = &value
			case "capture_width":
				frame.CaptureWidth = &value
			case "capture_height":
				frame.CaptureHeight = &value
			case "delta_x":
				frame.DeltaX = &value
			case "delta_y":
				frame.DeltaY = &value
			default:
				if math.Trunc(value) != value || math.Abs(value) > 9007199254740991 || (strconv.IntSize == 32 && (value > math.MaxInt32 || value < math.MinInt32)) {
					return frame, errInputBinary
				}
				integer := int(value)
				switch field {
				case "key_code":
					frame.KeyCode = &integer
				case "modifiers":
					frame.Modifiers = &integer
				case "capture_generation":
					frame.CaptureGeneration = &integer
				case "input_epoch":
					frame.InputEpoch = &integer
				case "control_epoch":
					frame.ControlEpoch = &integer
				case "reliable_seq":
					frame.ReliableSeq = &integer
				case "hover_seq":
					frame.HoverSeq = &integer
				case "gesture_barrier":
					frame.GestureBarrier = &integer
				}
			}
		}
	}
	if offset != len(packet) {
		return frame, errInputBinary
	}
	return frame, nil
}

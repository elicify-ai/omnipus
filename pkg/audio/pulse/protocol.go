package pulse

import (
	"encoding/binary"
	"fmt"
	"io"
)

// command is a PulseAudio native-protocol command/reply-type number. Values
// verified against noisetorch/pulseaudio's command.go (an iota-numbered
// const block matching PulseAudio's own pulsecore/protocol-native.h /
// pulse/def.h PA_COMMAND_* enum) — only the ones this client uses are named
// here.
type command uint32

const (
	commandError         command = 0
	commandTimeout       command = 1
	commandReply         command = 2
	commandAuth          command = 8
	commandSetClientName command = 9
	commandLoadModule    command = 51
)

func (c command) String() string {
	switch c {
	case commandError:
		return "ERROR"
	case commandTimeout:
		return "TIMEOUT"
	case commandReply:
		return "REPLY"
	case commandAuth:
		return "AUTH"
	case commandSetClientName:
		return "SET_CLIENT_NAME"
	case commandLoadModule:
		return "LOAD_MODULE"
	default:
		return fmt.Sprintf("command(%d)", uint32(c))
	}
}

// controlChannel is the PulseAudio pstream "channel" value reserved for
// control commands (as opposed to a playback/record stream's data channel).
const controlChannel = 0xffffffff

// maxPacketPayload bounds a single packet's payload so a corrupt or hostile
// peer (this client only ever talks to a PulseAudio server WE spawned on a
// private socket, but defend anyway) can't force an unbounded allocation.
const maxPacketPayload = 16 * 1024 * 1024

// writePacket frames one PulseAudio pstream packet: a 20-byte descriptor
// (length, channel, offset_hi, offset_lo, flags — all big-endian uint32)
// followed by the payload. Control commands use channel=controlChannel and
// zero offset/flags. Verified against noisetorch/pulseaudio's Client.request
// (client.go), which builds the identical 20-byte prefix before the
// tagstruct-encoded command+tag body.
func writePacket(w io.Writer, payload []byte) error {
	var hdr [20]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[4:8], controlChannel)
	// hdr[8:20] (offset_hi, offset_lo, flags) stay zero for control commands.
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("pulse: write packet header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("pulse: write packet payload: %w", err)
	}
	return nil
}

// readPacket reads one PulseAudio pstream packet and returns its payload
// (the descriptor's length/channel/offset/flags fields are consumed and
// discarded — this client has no use for streamed data, only control
// replies on the control channel).
func readPacket(r io.Reader) ([]byte, error) {
	var hdr [20]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("pulse: read packet header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	if n > maxPacketPayload {
		return nil, fmt.Errorf("pulse: packet payload %d exceeds max %d", n, maxPacketPayload)
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("pulse: read packet payload: %w", err)
		}
	}
	return payload, nil
}

// buildRequest encodes one command+tag+args tagstruct payload (NOT including
// the 20-byte packet descriptor — see writePacket). tag is the request
// sequence number the reply must echo back.
func buildRequest(cmd command, tag uint32, args *tagWriter) []byte {
	var w tagWriter
	w.putUint32(uint32(cmd))
	w.putUint32(tag)
	if args != nil {
		w.buf.Write(args.bytes())
	}
	return w.bytes()
}

// ProtocolError represents a PA_COMMAND_ERROR reply — the server rejected a
// request with a numeric PulseAudio error code. Only the code is surfaced
// (not a decoded string) to avoid vendoring PulseAudio's whole pa_strerror
// table for a client this narrow.
type ProtocolError struct {
	Code uint32
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("pulseaudio: server returned error code %d", e.Code)
}

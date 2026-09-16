package webrtc

import (
	"bytes"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"
)

const testPlayoutURI = "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"

func TestViewerPlayoutDelayNegotiatedVideo(t *testing.T) {
	for _, id := range []int{1, 7, 14, 20, 255} {
		t.Run(string(rune('A'+id)), func(t *testing.T) {
			i, err := (viewerPlayoutDelayFactory{}).NewInterceptor("viewer")
			if err != nil {
				t.Fatal(err)
			}
			defer i.Close()
			h := rtp.Header{Version: 2, SequenceNumber: 91, Timestamp: 123456, SSRC: 42}
			if extensionErr := h.SetExtension(3, []byte{9, 8}); extensionErr != nil {
				t.Fatal(extensionErr)
			}
			original := h.Clone()
			payload := []byte{4, 5, 6}
			attrs := interceptor.Attributes{"probe": true}
			calls := 0
			sentinel := errors.New("writer failed")
			w := i.BindLocalStream(&interceptor.StreamInfo{MimeType: "video/VP8", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: id}}}, interceptor.RTPWriterFunc(func(got *rtp.Header, p []byte, a interceptor.Attributes) (int, error) {
				calls++
				// Two 12-bit values, in 10ms units: min=0, max=20 => 00 00 14.
				if !bytes.Equal(got.GetExtension(uint8(id)), []byte{0, 0, 20}) {
					t.Errorf("delay=%v", got.GetExtension(uint8(id)))
				}
				if !bytes.Equal(got.GetExtension(3), []byte{9, 8}) {
					t.Error("unrelated extension changed")
				}
				if got.Timestamp != 123456 || got.SequenceNumber != 91 || got.SSRC != 42 || !bytes.Equal(p, payload) || !reflect.DeepEqual(a, attrs) {
					t.Error("media identity/payload/attributes changed")
				}
				if _, marshalErr := got.Marshal(); marshalErr != nil {
					t.Errorf("invalid wire header: %v", marshalErr)
				}
				return 17, sentinel
			}))
			n, err := w.Write(&h, payload, attrs)
			if n != 17 || !errors.Is(err, sentinel) || calls != 1 {
				t.Fatalf("writer result=%d,%v calls%d", n, err, calls)
			}
			if !reflect.DeepEqual(h, original) {
				t.Fatal("shared source header mutated")
			}
		})
	}
}

func TestViewerPlayoutDelayUnnegotiatedAndAudioUnchanged(t *testing.T) {
	for _, info := range []interceptor.StreamInfo{
		{MimeType: "audio/opus", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: 7}}},
		{MimeType: "video/H264"},
		{MimeType: "video/VP8", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: 0}}},
		{MimeType: "video/VP8", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: 256}}},
		{MimeType: "video/VP8", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: "unrelated", ID: 7}}},
	} {
		i, _ := (viewerPlayoutDelayFactory{}).NewInterceptor("viewer")
		h := rtp.Header{Version: 2}
		_ = h.SetExtension(7, []byte{8})
		original := h.Clone()
		w := i.BindLocalStream(&info, interceptor.RTPWriterFunc(func(got *rtp.Header, _ []byte, _ interceptor.Attributes) (int, error) {
			if !reflect.DeepEqual(*got, original) {
				t.Error("unnegotiated/audio changed")
			}
			return 0, nil
		}))
		_, _ = w.Write(&h, nil, nil)
		_ = i.Close()
	}
}

func TestViewerPlayoutDelayRegistryUsesSeparateViewerIDs(t *testing.T) {
	m := &pion.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	r := &interceptor.Registry{}
	if err := registerViewerInterceptors(m, r); err != nil {
		t.Fatal(err)
	}
	i, err := r.Build("viewer")
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close()
	// The same source packet crosses two bindings; each must receive only its
	// negotiated ID, and existing interceptor writes must not mutate the source.
	h := rtp.Header{Version: 2, SequenceNumber: 123, Timestamp: 999}
	original := h.Clone()
	for _, id := range []int{5, 9} {
		info := &interceptor.StreamInfo{SSRC: uint32(id), MimeType: "video/VP8", ClockRate: 90000, RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: id}, {URI: "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01", ID: 3}}}
		w := i.BindLocalStream(info, interceptor.RTPWriterFunc(func(got *rtp.Header, _ []byte, _ interceptor.Attributes) (int, error) {
			if !bytes.Equal(got.GetExtension(uint8(id)), []byte{0, 0, 20}) {
				t.Errorf("viewer%d extension missing", id)
			}
			other := uint8(5)
			if id == 5 {
				other = 9
			}
			if got.GetExtension(other) != nil {
				t.Error("other viewer extension leaked")
			}
			return 0, nil
		}))
		_, err = w.Write(&h, []byte{1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h, original) {
			t.Fatal("registry order allowed shared header mutation")
		}
		i.UnbindLocalStream(info)
	}
}

func TestViewerPlayoutDelayOnlyViewerNegotiatesExtension(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	offers := make([]string, 2)
	for idx, api := range []*pion.API{s.api, s.apiViewer} {
		pc, err := api.NewPeerConnection(pion.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeAudio, pion.RTPCodecTypeVideo} {
			if _, transceiverErr := pc.AddTransceiverFromKind(kind, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionSendonly}); transceiverErr != nil {
				t.Fatal(transceiverErr)
			}
		}
		offer, err := pc.CreateOffer(nil)
		if err != nil {
			t.Fatal(err)
		}
		offers[idx] = offer.SDP
	}
	if strings.Contains(offers[0], testPlayoutURI) {
		t.Fatal("ingest unexpectedly advertises playout extension")
	}
	section := ""
	videoFound := false
	for _, line := range strings.Split(offers[1], "\n") {
		if strings.HasPrefix(line, "m=") {
			section = strings.Fields(line)[0]
		}
		if strings.Contains(line, testPlayoutURI) {
			if section != "m=video" {
				t.Fatalf("extension advertised in %s", section)
			}
			videoFound = true
		}
	}
	if !videoFound {
		t.Fatal("viewer video lacks negotiated extension capability")
	}
	capabilities := func(sdp string) []string {
		var out []string
		for _, line := range strings.Split(sdp, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "a=rtpmap:") || strings.HasPrefix(line, "a=fmtp:") || strings.HasPrefix(line, "a=rtcp-fb:") {
				out = append(out, line)
			}
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(capabilities(offers[0]), capabilities(offers[1])) {
		t.Fatal("viewer codec/feedback capabilities diverged from ingest")
	}
}

func TestViewerPlayoutDelayReplacesExistingWithoutMutatingSource(t *testing.T) {
	i, _ := (viewerPlayoutDelayFactory{}).NewInterceptor("viewer")
	defer i.Close()
	h := rtp.Header{Version: 2}
	if err := h.SetExtension(7, []byte{0, 1, 244}); err != nil {
		t.Fatal(err)
	}
	original := h.Clone()
	w := i.BindLocalStream(&interceptor.StreamInfo{MimeType: "video/VP8", RTPHeaderExtensions: []interceptor.RTPHeaderExtension{{URI: testPlayoutURI, ID: 7}}}, interceptor.RTPWriterFunc(func(got *rtp.Header, _ []byte, _ interceptor.Attributes) (int, error) {
		if !bytes.Equal(got.GetExtension(7), []byte{0, 0, 20}) {
			t.Fatalf("old cap not replaced: %v", got.GetExtension(7))
		}
		return 0, nil
	}))
	if _, err := w.Write(&h, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h, original) {
		t.Fatal("replacing extension mutated shared source")
	}
}

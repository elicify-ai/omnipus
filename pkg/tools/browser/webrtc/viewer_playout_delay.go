package webrtc

import (
	"fmt"
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

const viewerPlayoutDelayURI = "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"

type viewerPlayoutDelayFactory struct{}

func (viewerPlayoutDelayFactory) NewInterceptor(string) (interceptor.Interceptor, error) {
	return &viewerPlayoutDelayInterceptor{}, nil
}

type viewerPlayoutDelayInterceptor struct{ interceptor.NoOp }

// BindLocalStream uses the viewer's negotiated ID, never the ingest ID. The
// best-effort 0–200ms request bounds video smoothing, including Chromium's
// audio-sync minimum. Audio remains present but may temporarily lag video
// when its own buffering exceeds this range; this does not repair late audio.
func (*viewerPlayoutDelayInterceptor) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	if !strings.HasPrefix(strings.ToLower(info.MimeType), "video/") {
		return writer
	}
	var id uint8
	for _, extension := range info.RTPHeaderExtensions {
		if extension.URI == viewerPlayoutDelayURI && extension.ID > 0 && extension.ID <= 255 {
			id = uint8(extension.ID)
			break
		}
	}
	if id == 0 {
		return writer
	}
	return interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
		// Registered last, this runs before other outbound interceptors and keeps
		// their mutations, as well as ours, private to this viewer binding.
		out := header.Clone()
		if id > 14 {
			if !out.Extension {
				out.Extension = true
				out.ExtensionProfile = rtp.ExtensionProfileTwoByte
			} else if out.ExtensionProfile == rtp.ExtensionProfileOneByte {
				out.ExtensionProfile = rtp.ExtensionProfileTwoByte
			}
		}
		// Two 12-bit fields in 10ms units: minimum 0, maximum 20.
		if err := out.SetExtension(id, []byte{0, 0, 20}); err != nil {
			return 0, fmt.Errorf("set playout delay extension: %w", err)
		}
		return writer.Write(&out, payload, attributes)
	})
}

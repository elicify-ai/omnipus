package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/report"
	"github.com/pion/webrtc/v4"
)

// registerViewerInterceptors follows Pion v4.2.16's documented custom registry
// recipe (RegisterDefaultInterceptorsWithOptions), omitting only its sender-report
// generator. Viewers receive the encoder's clock through forwardSenderReport;
// generating another clock from relay packet-arrival times breaks that mapping.
// On Pion upgrades, compare the upstream default stages with this list. Real-wire
// sender-clock, ingest receiver-report and viewer feedback tests guard its purpose.
func registerViewerInterceptors(m *webrtc.MediaEngine, registry *interceptor.Registry) error {
	if err := webrtc.ConfigureNack(m, registry); err != nil {
		return err
	}
	receiver, err := report.NewReceiverInterceptor()
	if err != nil {
		return err
	}
	registry.Add(receiver)
	if err := webrtc.ConfigureSimulcastExtensionHeaders(m); err != nil {
		return err
	}
	if err := webrtc.ConfigureStatsInterceptor(registry); err != nil {
		return err
	}
	return webrtc.ConfigureTWCCSender(m, registry)
}

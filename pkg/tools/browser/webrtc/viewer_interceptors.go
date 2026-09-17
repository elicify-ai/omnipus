package webrtc

import (
	"fmt"
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
	if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: viewerPlayoutDelayURI}, webrtc.RTPCodecTypeVideo); err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	if err := webrtc.ConfigureNack(m, registry); err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	receiver, err := report.NewReceiverInterceptor()
	if err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	registry.Add(receiver)
	if err := webrtc.ConfigureSimulcastExtensionHeaders(m); err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	if err := webrtc.ConfigureStatsInterceptor(registry); err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	if err := webrtc.ConfigureTWCCSender(m, registry); err != nil {
		return fmt.Errorf("registerViewerInterceptors: %w", err)
	}
	// Pion wraps in registration order: last runs first on outbound RTP.
	registry.Add(viewerPlayoutDelayFactory{})
	return nil
}

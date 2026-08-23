// ADR-062 tier 1: what address viewers are told to send media to.
//
// The design promise is "no additional configuration for the user" — a hosted
// operator who already set gateway.public_url (for CSP/CORS/WS origin checks)
// must get working media without discovering a WebRTC-specific knob. These
// tests pin that derivation, and pin the honest limits of it: a HOSTNAME is
// not resolved (SetNAT1To1IPs takes literal addresses; resolving at boot would
// bake in one DNS answer and rot silently), and an unset public_url yields
// nothing rather than a guess.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func cfgWith(publicURL, explicitIP string) *config.Config {
	c := &config.Config{}
	c.Gateway.PublicURL = publicURL
	c.Tools.Browser.WebRTCPublicIP = explicitIP
	return c
}

func TestResolveWebRTCPublicIPs_DerivesFromPublicURL_NoNewConfigNeeded(t *testing.T) {
	require.Equal(t, []string{"109.105.222.208"},
		resolveWebRTCPublicIPs(cfgWith("https://109.105.222.208", "")),
		"an operator who set public_url must get media candidates for free")
	require.Equal(t, []string{"109.105.222.208"},
		resolveWebRTCPublicIPs(cfgWith("https://109.105.222.208:8443/", "")),
		"a port or path in public_url must not defeat the derivation")
}

func TestResolveWebRTCPublicIPs_ExplicitOverrideWins(t *testing.T) {
	require.Equal(t, []string{"203.0.113.7"},
		resolveWebRTCPublicIPs(cfgWith("https://109.105.222.208", "203.0.113.7")),
		"an operator who set the explicit key knows something we do not (split DNS, separate media IP)")
}

func TestResolveWebRTCPublicIPs_HostnameIsNotResolved(t *testing.T) {
	// Deliberate: SetNAT1To1IPs takes literal IPs. Resolving a name at boot
	// would freeze one DNS answer into every candidate and rot on a DNS
	// change, with no signal. Returning nil keeps the interface addresses,
	// and the operator sets webrtc_public_ip when fronted by a name.
	require.Nil(t, resolveWebRTCPublicIPs(cfgWith("https://omnipus.example.com", "")))
}

func TestResolveWebRTCPublicIPs_UnsetIsNilNotAGuess(t *testing.T) {
	require.Nil(t, resolveWebRTCPublicIPs(cfgWith("", "")),
		"a laptop install advertises its real interfaces; inventing an address would be worse than none")
	require.Nil(t, resolveWebRTCPublicIPs(cfgWith("://not a url", "")))
}

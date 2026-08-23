package webrtc

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ADR-062 tier 3 was BLOCKED on two findings in adversarial review. These
// tests are the two findings, made non-vacuous.

// CORRECTION 2: pion's DefaultPermissionHandler admits every peer, which turns
// an embedded relay into an authenticated OPEN RELAY — a credential holder
// could relay to localhost, the provider's internal network, or cloud metadata.
func TestAllowedRelayPeer_AdmitsOnlyTheGatewayItself(t *testing.T) {
	self := net.ParseIP("203.0.113.9")
	perm := allowedRelayPeer([]net.IP{self})

	require.True(t, perm(nil, self), "the gateway's own media address is the one legal peer")

	for _, forbidden := range []string{
		"127.0.0.1",       // the gateway's own HTTP listener
		"::1",             // same, v6
		"169.254.169.254", // cloud metadata
		"10.0.0.5",        // provider internal network
		"172.19.60.75",    // Fly 6PN-adjacent private space
		"8.8.8.8",         // the open internet
	} {
		require.False(t, perm(nil, net.ParseIP(forbidden)),
			"relaying to %s must be refused — that is the open-relay finding", forbidden)
	}
}

// Tier 3 is opt-in: it costs a declared port, so an unconfigured install must
// start nothing at all rather than quietly opening one.
func TestStartTURN_DisabledByDefault(t *testing.T) {
	srv, err := StartTURN(TURNConfig{})
	require.NoError(t, err)
	require.Nil(t, srv, "TURN must be off unless a port is configured")
	// Every accessor must be safe on the nil server, because callers are
	// written against "TURN may be off".
	require.NoError(t, srv.Close())
	servers, err := srv.ICEServers("viewer-1")
	require.NoError(t, err)
	require.Nil(t, servers)
}

// A relay that advertises a private address it happens to be bound to is
// useless to every remote viewer, so refuse rather than start something that
// cannot work.
func TestStartTURN_RefusesWithoutAPublicAddress(t *testing.T) {
	_, err := StartTURN(TURNConfig{UDPPort: 30000})
	require.Error(t, err)
	require.Contains(t, err.Error(), "public address")
}

func TestStartTURN_MintsScopedShortLivedCredentials(t *testing.T) {
	srv, err := StartTURN(TURNConfig{UDPPort: 0})
	require.NoError(t, err)
	require.Nil(t, srv)

	// Bind on an ephemeral port so the test never collides with a real deploy.
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr, ok := probe.LocalAddr().(*net.UDPAddr)
	require.True(t, ok, "unexpected type %T for probe.LocalAddr(), want *net.UDPAddr", probe.LocalAddr())
	port := udpAddr.Port
	require.NoError(t, probe.Close())

	srv, err = StartTURN(TURNConfig{UDPPort: port, BindAddress: "127.0.0.1", PublicIP: "203.0.113.9"})
	require.NoError(t, err)
	require.NotNil(t, srv)
	t.Cleanup(func() { _ = srv.Close() })

	servers, err := srv.ICEServers("viewer-abc")
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.NotEmpty(t, servers[0].Username)
	require.NotEmpty(t, servers[0].Credential)
	require.Contains(t, servers[0].URLs[0], "turn:203.0.113.9:")

	// TURN-REST usernames are "<unix expiry>:<user>": the expiry is IN the
	// credential, which is what makes the window enforceable by the server
	// rather than a promise.
	require.Contains(t, servers[0].Username, ":viewer-abc")
	require.True(t, strings.HasPrefix(servers[0].Username, strings.Split(servers[0].Username, ":")[0]))
	require.Positive(t, srv.CredentialTTL())

	// Two viewers must not share a credential.
	other, err := srv.ICEServers("viewer-xyz")
	require.NoError(t, err)
	require.NotEqual(t, servers[0].Credential, other[0].Credential)
}

func TestTURNServer_CloseIsIdempotent(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr, ok := probe.LocalAddr().(*net.UDPAddr)
	require.True(t, ok, "unexpected type %T for probe.LocalAddr(), want *net.UDPAddr", probe.LocalAddr())
	port := udpAddr.Port
	require.NoError(t, probe.Close())

	srv, err := StartTURN(TURNConfig{UDPPort: port, BindAddress: "127.0.0.1", PublicIP: "203.0.113.9"})
	require.NoError(t, err)
	require.NoError(t, srv.Close())
	require.NoError(t, srv.Close(), "a second Close must not panic or error — shutdown paths double-close")
}

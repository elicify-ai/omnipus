package webrtc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

// ADR-062 tier 3: embedded TURN, no external service.
//
// Tiers 1 and 2 both require the VIEWER to send packets directly to a port on
// this gateway. Tier 3 is for the client that cannot: a VPN system extension
// that eats Chrome's ICE traffic, a corporate firewall that permits only
// established outbound connections. The client opens ONE connection to the
// relay and the relay carries the media.
//
// The adversarial review of this ADR blocked tier 3 on two findings, and both
// are structural here rather than left to configuration:
//
//   CORRECTION 1 — it needs its OWN port. There is no TLS listener anywhere in
//   this product and on a hosted provider 443 belongs to the edge proxy, so
//   "TURN over TLS on the same 443 as the web UI" was never possible. The port
//   is explicit (TURNConfig.UDPPort / TCPPort) and the operator declares it.
//
//   CORRECTION 2 — pion's DefaultPermissionHandler admits EVERY peer, which
//   would make this an authenticated open relay: a credential holder could
//   relay UDP to localhost, to the provider's internal network, or to cloud
//   metadata endpoints. The permission handler here admits only the gateway's
//   OWN media addresses, so an allocation can reach this gateway's ICE agent
//   and nothing else. That is the entire legitimate use.
//
// Credentials are short-lived TURN-REST (username "<expiry>:<viewer>", password
// HMAC-SHA1 over it). pion/turn exposes no per-allocation teardown, so a
// credential CANNOT be revoked on detach — the honest guarantee is a bounded
// residual window, which is why the TTL is minutes and is stated on the wire.

const (
	// turnCredentialTTL bounds how long a minted credential stays valid.
	// pion/turn cannot revoke an allocation, so this window IS the guarantee:
	// a leaked credential is useless after it, and it is long enough to cover
	// a slow panel open plus one reconnect.
	turnCredentialTTL = 10 * time.Minute

	// turnRealm is arbitrary but must be stable: clients echo it back.
	turnRealm = "omnipus"
)

// TURNConfig configures the embedded relay. A zero UDPPort disables it
// entirely — tier 3 is opt-in, because it costs a declared port.
type TURNConfig struct {
	UDPPort int
	// TCPPort is optional. It matters for clients that block UDP outright,
	// which is the case tier 3 exists for. NOTE for hosted deploys: a provider
	// whose TCP proxy is not a transparent byte pipe will reset these
	// connections — measured on Fly 2026-08-17 for ICE-TCP, and the same proxy
	// carries this.
	TCPPort int
	// BindAddress is the local address the relay listens on. Some platforms
	// route inbound UDP only to a specific address (Fly: fly-global-services).
	BindAddress string
	// PublicIP is the address handed to clients as the relay address. Without
	// it the relay would advertise a private address no viewer can route to.
	PublicIP string
}

// ICEServer is one entry of the client's RTCConfiguration.iceServers.
type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

// TURNServer is the running relay.
type TURNServer struct {
	srv      *turn.Server
	secret   string
	publicIP string
	udpPort  int
	tcpPort  int

	mu     sync.Mutex
	closed bool
	conns  []interface{ Close() error }
}

// allowedRelayPeer returns the permission handler: only the gateway's own
// media address is a legal peer for an allocation. See CORRECTION 2 above —
// the default handler would admit localhost and cloud metadata.
func allowedRelayPeer(selfIPs []net.IP) turn.PermissionHandler {
	return func(_ net.Addr, peerIP net.IP) bool {
		for _, ip := range selfIPs {
			if ip.Equal(peerIP) {
				return true
			}
		}
		return false
	}
}

// StartTURN starts the embedded relay. Returns (nil, nil) when TURN is not
// configured, so a caller can always call it unconditionally.
func StartTURN(cfg TURNConfig) (*TURNServer, error) {
	if cfg.UDPPort <= 0 {
		return nil, nil
	}
	if cfg.PublicIP == "" {
		return nil, fmt.Errorf("webrtc: TURN needs a public address to advertise as its relay address")
	}
	relayIP := net.ParseIP(cfg.PublicIP)
	if relayIP == nil {
		return nil, fmt.Errorf("webrtc: TURN public address %q is not an IP", cfg.PublicIP)
	}

	secretRaw := make([]byte, 32)
	if _, err := rand.Read(secretRaw); err != nil {
		return nil, fmt.Errorf("webrtc: TURN shared secret: %w", err)
	}
	secret := hex.EncodeToString(secretRaw)

	logf := logging.NewDefaultLoggerFactory()
	perm := allowedRelayPeer([]net.IP{relayIP})
	gen := &turn.RelayAddressGeneratorStatic{
		RelayAddress: relayIP,
		Address:      relayBindAddress(cfg.BindAddress),
	}

	ts := &TURNServer{secret: secret, publicIP: cfg.PublicIP, udpPort: cfg.UDPPort, tcpPort: cfg.TCPPort}

	scfg := turn.ServerConfig{
		Realm:         turnRealm,
		AuthHandler:   turn.NewLongTermAuthHandler(secret, logf.NewLogger("turn")),
		LoggerFactory: logf,
	}

	udp, err := net.ListenPacket("udp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.UDPPort)))
	if err != nil {
		return nil, fmt.Errorf("webrtc: TURN udp listen: %w", err)
	}
	ts.conns = append(ts.conns, udp)
	scfg.PacketConnConfigs = append(scfg.PacketConnConfigs, turn.PacketConnConfig{
		PacketConn:            udp,
		RelayAddressGenerator: gen,
		PermissionHandler:     perm,
	})

	if cfg.TCPPort > 0 {
		ln, lerr := net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.TCPPort)))
		if lerr != nil {
			_ = udp.Close()
			return nil, fmt.Errorf("webrtc: TURN tcp listen: %w", lerr)
		}
		ts.conns = append(ts.conns, ln)
		scfg.ListenerConfigs = append(scfg.ListenerConfigs, turn.ListenerConfig{
			Listener:              ln,
			RelayAddressGenerator: gen,
			PermissionHandler:     perm,
		})
	}

	srv, err := turn.NewServer(scfg)
	if err != nil {
		ts.closeConns()
		return nil, fmt.Errorf("webrtc: TURN server: %w", err)
	}
	ts.srv = srv
	return ts, nil
}

// relayBindAddress is what the relay passes to Listen when allocating. Empty
// means all interfaces, which is right nearly everywhere.
func relayBindAddress(bind string) string {
	if bind == "" {
		return "0.0.0.0"
	}
	return bind
}

// Credentials mints a short-lived TURN-REST credential for one viewer. The
// residual window is turnCredentialTTL — see the file comment on why this is a
// bound rather than a revocation.
func (t *TURNServer) Credentials(viewerID string) (username, password string, err error) {
	if t == nil {
		return "", "", nil
	}
	return turn.GenerateLongTermTURNRESTCredentials(t.secret, viewerID, turnCredentialTTL)
}

// ICEServers returns the entry to hand this viewer, or nil when TURN is off.
func (t *TURNServer) ICEServers(viewerID string) ([]ICEServer, error) {
	if t == nil {
		return nil, nil
	}
	user, pass, err := t.Credentials(viewerID)
	if err != nil {
		return nil, err
	}
	urls := []string{fmt.Sprintf("turn:%s:%d?transport=udp", t.publicIP, t.udpPort)}
	if t.tcpPort > 0 {
		urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=tcp", t.publicIP, t.tcpPort))
	}
	return []ICEServer{{URLs: urls, Username: user, Credential: pass}}, nil
}

// CredentialTTL is the stated residual window for a minted credential.
func (t *TURNServer) CredentialTTL() time.Duration { return turnCredentialTTL }

func (t *TURNServer) closeConns() {
	for _, c := range t.conns {
		_ = c.Close()
	}
	t.conns = nil
}

// Close stops the relay and releases its ports.
func (t *TURNServer) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	var err error
	if t.srv != nil {
		err = t.srv.Close()
	}
	t.closeConns()
	return err
}

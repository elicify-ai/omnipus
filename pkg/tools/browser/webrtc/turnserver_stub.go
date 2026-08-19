//go:build lite

package webrtc

import "time"

// TURNConfig is the lite-build stand-in for the real TURNConfig
// (turnserver.go, //go:build !lite). Lite builds deliberately exclude
// pion/turn (same Pion-exclusion policy as stub.go's Session), so this
// mirrors the real struct's field set field-for-field purely so gateway call
// sites (webrtc.StartTURN(webrtc.TURNConfig{...})) compile unchanged.
type TURNConfig struct {
	UDPPort     int
	TCPPort     int
	BindAddress string
	PublicIP    string
}

// ICEServer mirrors the real ICEServer (turnserver.go) field-for-field so a
// caller ranging over a lite build's (always-empty) []ICEServer result reads
// the same field names it would on a real build.
type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

// TURNServer is the lite-build stand-in for the real embedded-TURN relay.
// StartTURN below never actually produces a working instance in a lite
// build, so every method here exists purely so callers written against the
// real API compile unchanged and get a typed ErrUnavailable.
type TURNServer struct{}

// StartTURN mirrors the real StartTURN's "callable unconditionally" contract
// (turnserver.go): a zero/negative UDPPort means TURN was never configured,
// so it returns (nil, nil) exactly like the real build's "not configured"
// path. When the operator DID configure a port (UDPPort > 0), a lite build
// still cannot start the relay — pion/turn is excluded — so this returns
// (nil, ErrUnavailable) rather than a silent fake success. The gateway
// (browser_webrtc.go's sharedTURN) already treats any StartTURN error as
// "relay failed to start": it logs the error and surfaces it to the operator
// via turnUnavailableNotice, so the operator who asked for tier-3 TURN in a
// lite build gets a real, visible signal instead of video silently having no
// relay path.
func StartTURN(cfg TURNConfig) (*TURNServer, error) {
	if cfg.UDPPort <= 0 {
		return nil, nil
	}
	return nil, ErrUnavailable
}

// Credentials always fails: there is never a running relay in a lite build
// to mint TURN-REST credentials against.
func (t *TURNServer) Credentials(_ string) (username, password string, err error) {
	return "", "", ErrUnavailable
}

// ICEServers always fails for the same reason. StartTURN above never
// produces a non-nil *TURNServer, so this is unreachable from the gateway in
// practice, but it mirrors the real method's nil-receiver safety (no panic)
// rather than relying on that being true.
func (t *TURNServer) ICEServers(_ string) ([]ICEServer, error) {
	return nil, ErrUnavailable
}

// CredentialTTL mirrors the real TURNServer's exported surface so a caller
// reading it for display purposes compiles unchanged. It is never
// meaningful here since Credentials/ICEServers never succeed.
func (t *TURNServer) CredentialTTL() time.Duration { return 0 }

// Close is a nil-receiver-safe no-op, matching the real TURNServer's Close
// contract used by shutdown paths.
func (t *TURNServer) Close() error { return nil }

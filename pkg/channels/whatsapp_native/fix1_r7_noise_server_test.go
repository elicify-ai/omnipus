package whatsapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCert"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

// External platform fixture: real WebSocket + authenticated Noise XX exchange.
// Test-only trust root replaces the remote service's root; certificate checking,
// shared-secret mixing, native Connect and native Disconnect remain real.
type fix1NoiseServer struct {
	server     *httptest.Server
	static     *keys.KeyPair
	cert       []byte
	connected  chan *websocket.Conn
	errors     chan error
	handshakes atomic.Int32
}

func fix1NewNoiseServer(t *testing.T) *fix1NoiseServer {
	t.Helper()
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	previous := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	t.Cleanup(func() { whatsmeow.WACertPubKey = previous })
	certify := func(key *[32]byte, serial, issuer uint32, signer *[32]byte) *waCert.CertChain_NoiseCertificate {
		details, err := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Key: key[:], Serial: proto.Uint32(serial), IssuerSerial: proto.Uint32(issuer), NotBefore: proto.Uint64(uint64(time.Now().Add(-time.Minute).Unix())), NotAfter: proto.Uint64(uint64(time.Now().Add(time.Hour).Unix()))})
		require.NoError(t, err)
		sig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*signer), details)
		return &waCert.CertChain_NoiseCertificate{Details: details, Signature: sig[:]}
	}
	cert, err := proto.Marshal(&waCert.CertChain{Intermediate: certify(intermediate.Pub, 1, 0, root.Priv), Leaf: certify(static.Pub, 2, 1, intermediate.Priv)})
	require.NoError(t, err)
	s := &fix1NoiseServer{static: static, cert: cert, connected: make(chan *websocket.Conn, 8), errors: make(chan error, 8)}
	s.server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.server.Close)
	return s
}

func fix1NoiseFrame(raw []byte) (*waWa6.HandshakeMessage, error) {
	if len(raw) < 3 {
		return nil, fmt.Errorf("short platform handshake frame")
	}
	n := int(raw[0])<<16 | int(raw[1])<<8 | int(raw[2])
	if n != len(raw)-3 {
		return nil, fmt.Errorf("frame length %d != payload %d", n, len(raw)-3)
	}
	var msg waWa6.HandshakeMessage
	err := proto.Unmarshal(raw[3:], &msg)
	return &msg, err
}

func (s *fix1NoiseServer) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.errors <- err
		return
	}
	defer ws.CloseNow()
	if err = s.handshake(r.Context(), ws); err != nil {
		s.errors <- err
		return
	}
	s.handshakes.Add(1)
	s.connected <- ws
	// Remain a real connected remote endpoint until the captured client or the
	// test's explicit remote-disconnect stimulus closes this exact socket.
	for {
		if _, _, err = ws.Read(r.Context()); err != nil {
			return
		}
	}
}

func (s *fix1NoiseServer) handshake(ctx context.Context, ws *websocket.Conn) error {
	_, raw, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	headerLen := len(socket.WAConnHeader)
	if len(raw) < headerLen+3 {
		return fmt.Errorf("no client handshake header")
	}
	header := append([]byte(nil), raw[:headerLen]...)
	hello, err := fix1NoiseFrame(raw[headerLen:])
	if err != nil {
		return err
	}
	clientEphemeral := hello.GetClientHello().GetEphemeral()
	if len(clientEphemeral) != 32 {
		return fmt.Errorf("client ephemeral length %d", len(clientEphemeral))
	}
	ephemeral := keys.NewKeyPair()
	nh := socket.NewNoiseHandshake()
	nh.Start(socket.NoiseStartPattern, header)
	nh.Authenticate(clientEphemeral)
	nh.Authenticate(ephemeral.Pub[:])
	if err = nh.MixSharedSecretIntoKey(*ephemeral.Priv, *(*[32]byte)(clientEphemeral)); err != nil {
		return err
	}
	serverStatic := nh.Encrypt(s.static.Pub[:])
	if err = nh.MixSharedSecretIntoKey(*s.static.Priv, *(*[32]byte)(clientEphemeral)); err != nil {
		return err
	}
	certificate := nh.Encrypt(s.cert)
	response, err := proto.Marshal(&waWa6.HandshakeMessage{ServerHello: &waWa6.HandshakeMessage_ServerHello{Ephemeral: ephemeral.Pub[:], Static: serverStatic, Payload: certificate}})
	if err != nil {
		return err
	}
	framed := append([]byte{byte(len(response) >> 16), byte(len(response) >> 8), byte(len(response))}, response...)
	if err = ws.Write(ctx, websocket.MessageBinary, framed); err != nil {
		return err
	}
	_, raw, err = ws.Read(ctx)
	if err != nil {
		return err
	}
	finish, err := fix1NoiseFrame(raw)
	if err != nil {
		return err
	}
	clientStatic, err := nh.Decrypt(finish.GetClientFinish().GetStatic())
	if err != nil {
		return err
	}
	if len(clientStatic) != 32 {
		return fmt.Errorf("client static length %d", len(clientStatic))
	}
	if err = nh.MixSharedSecretIntoKey(*ephemeral.Priv, *(*[32]byte)(clientStatic)); err != nil {
		return err
	}
	_, err = nh.Decrypt(finish.GetClientFinish().GetPayload())
	return err
}

package googlechat

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// rerouteTransport sends every request to the fake token server.
type rerouteTransport struct{ target *url.URL }

func (r rerouteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func serviceAccountJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	raw, err := json.Marshal(map[string]string{"client_email": "bot@gate.iam.gserviceaccount.com", "private_key": string(pemKey), "token_uri": "https://oauth2.googleapis.com/token"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func newBotModeChannel(t *testing.T, saJSON string, tokenStatus int) *GoogleChatChannel {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		if tokenStatus == http.StatusOK {
			_, _ = w.Write([]byte(`{"access_token":"ya29.gate","expires_in":3600,"token_type":"Bearer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := NewGoogleChatChannel(config.GoogleChatConfig{Mode: "bot", ServiceAccountJSONRef: "GC_SA"},
		credentials.SecretBundle{"GC_SA": saJSON}, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewGoogleChatChannel: %v", err)
	}
	ch.client = &http.Client{Transport: rerouteTransport{target: target}}
	return ch
}

// TestGoogleChatStart_RejectedServiceAccountFailsTheStart is Train 2 gate r1
// (BDD-8.3 channel survey): in bot mode an unusable service account was
// logged ("webhook-only features available") while the channel ran with no
// send path, and a key the token endpoint rejects was never checked before
// first send. Both must fail Start; an accepted key starts (control).
func TestGoogleChatStart_RejectedServiceAccountFailsTheStart(t *testing.T) {
	for name, ch := range map[string]*GoogleChatChannel{
		"unusable service account":         newBotModeChannel(t, `{"client_email":"x","private_key":"not a pem key"}`, http.StatusOK),
		"token endpoint 400 invalid_grant": newBotModeChannel(t, serviceAccountJSON(t), http.StatusBadRequest),
		"token endpoint 401":               newBotModeChannel(t, serviceAccountJSON(t), http.StatusUnauthorized),
		"token endpoint 403":               newBotModeChannel(t, serviceAccountJSON(t), http.StatusForbidden),
	} {
		if err := ch.Start(context.Background()); err == nil {
			_ = ch.Stop(context.Background())
			t.Fatalf("%s: Start must fail", name)
		}
		if ch.IsRunning() {
			t.Fatalf("%s: the channel must not be running", name)
		}
	}
	ok := newBotModeChannel(t, serviceAccountJSON(t), http.StatusOK)
	if err := ok.Start(context.Background()); err != nil {
		t.Fatalf("an accepted service account must start, got %v", err)
	}
	if !ok.IsRunning() {
		t.Fatal("an accepted service account must leave the channel running")
	}
	_ = ok.Stop(context.Background())

	// A network failure is not a rejection: Start succeeds and sends retry.
	unreachable := newBotModeChannel(t, serviceAccountJSON(t), http.StatusOK)
	unreachable.client = &http.Client{Transport: failingTransport{}}
	if err := unreachable.Start(context.Background()); err != nil {
		t.Fatalf("an unreachable token endpoint must not fail Start, got %v", err)
	}
	_ = unreachable.Stop(context.Background())
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// TestGoogleChatTokenExchange_SendsRawSignedJWT is Train 2 gate r1 (Q2 = A):
// the jwt-bearer assertion must be the signed JWT itself
// (header.claims.signature), not a base64 encoding of it — Google's
// service-account flow rejects anything else, so every bot-mode send failed.
func TestGoogleChatTokenExchange_SendsRawSignedJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	assertions := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if parseErr := r.ParseForm(); parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		assertions <- r.PostForm.Get("assertion")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.gate","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ch := &GoogleChatChannel{
		client:  &http.Client{Transport: rerouteTransport{target: target}},
		saKey:   key,
		saEmail: "bot@gate.iam.gserviceaccount.com",
	}
	if _, tokenErr := ch.getAccessToken(context.Background()); tokenErr != nil {
		t.Fatalf("getAccessToken: %v", tokenErr)
	}
	assertion := <-assertions
	if parts := strings.Split(assertion, "."); len(parts) != 3 {
		t.Fatalf("assertion must be a 3-part signed JWT, got %d part(s)", len(parts))
	}
	parsed, err := jwt.Parse(assertion, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(googleChatTokenURL))
	if err != nil || !parsed.Valid {
		t.Fatalf("assertion must verify against the service-account key: %v", err)
	}
}

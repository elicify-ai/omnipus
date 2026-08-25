package googlechat

// Webhook signature verification tests.
//
// The Google Chat webhook (POST /webhook/google-chat) is a public,
// unauthenticated HTTP endpoint. Its ONLY protection against a forged inbound
// event is verifySignature. Before this file, none of the package's tests
// exercised it: making verifySignature `return true` unconditionally left the
// suite green, so a total collapse of the control was invisible.
//
// Expected values here are derived from the contract, not from observed output:
//
//   - Header shape and algorithm: the verifySignature doc comment in
//     googlechat.go -- `kid:base64signature`, an RSA signature over the SHA-256
//     hash of the request body.
//   - Accept/reject semantics: the universal signature-verification contract --
//     only a signature produced by the holder of the private key, over the
//     exact bytes received, under the promised scheme, from a key resolvable
//     for the presented kid, may be accepted.
//   - HTTP statuses: RFC 7231 6.5.3 (403 for a credential that is present but
//     invalid -- 401 would require a WWW-Authenticate challenge) and the
//     sibling LINE channel precedent in pkg/channels/line/line_behavior_test.go,
//     which asserts 403 for both a bad and a missing signature.

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- test doubles: the JWKS fetch is the only mocked boundary ---------------

// gchatFakeClient stands in for the outbound HTTP edge (the GoogleChatClient
// seam that production fills with *http.Client). Everything the tests claim to
// verify -- header parsing, key lookup, RSA verification, handler status codes
// -- stays real.
type gchatFakeClient struct {
	mu   sync.Mutex
	reqs []*http.Request
	resp func(*http.Request) (*http.Response, error)
}

func (f *gchatFakeClient) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if f.resp == nil {
		// Fail closed: an unexpected JWKS fetch must not silently succeed.
		return nil, errors.New("gchatFakeClient: no response configured")
	}
	return f.resp(r)
}

func (f *gchatFakeClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *gchatFakeClient) lastRequest() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return nil
	}
	return f.reqs[len(f.reqs)-1]
}

// --- test keys and signing --------------------------------------------------

var (
	gchatKeyOnce   sync.Once
	gchatKeyVal    *rsa.PrivateKey
	errGchatKeyGen error

	gchatForeignKeyOnce   sync.Once
	gchatForeignKeyVal    *rsa.PrivateKey
	errGchatForeignKeyGen error
)

// gchatTestKey is the "Google" signing key: 2048-bit RSA, the modulus size
// Google publishes for service-account JWKS keys. Generated once per run.
func gchatTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	gchatKeyOnce.Do(func() { gchatKeyVal, errGchatKeyGen = rsa.GenerateKey(rand.Reader, 2048) })
	if errGchatKeyGen != nil {
		t.Fatalf("generate test RSA key: %v", errGchatKeyGen)
	}
	return gchatKeyVal
}

// gchatForeignKey is an attacker-controlled key that is never published in the
// channel's JWKS cache.
func gchatForeignKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	gchatForeignKeyOnce.Do(func() { gchatForeignKeyVal, errGchatForeignKeyGen = rsa.GenerateKey(rand.Reader, 2048) })
	if errGchatForeignKeyGen != nil {
		t.Fatalf("generate foreign RSA key: %v", errGchatForeignKeyGen)
	}
	return gchatForeignKeyVal
}

// signGChatBody builds a header in the documented `kid:base64signature` form:
// PKCS#1 v1.5 RSA over the SHA-256 digest of the exact body bytes.
func signGChatBody(t *testing.T, kid string, priv *rsa.PrivateKey, body []byte) string {
	t.Helper()
	digest := sha256.Sum256(body)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign body: %v", err)
	}
	return kid + ":" + base64.StdEncoding.EncodeToString(sig)
}

// gchatJWKSBody renders a JWKS document in the RFC 7517 form Google serves:
// base64url (unpadded) big-endian modulus and exponent.
func gchatJWKSBody(kid string, pub *rsa.PublicKey) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return fmt.Sprintf(`{"keys":[{"kid":%q,"kty":"RSA","alg":"RS256","use":"sig","n":%q,"e":%q}]}`, kid, n, e)
}

func gchatHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

const gchatTestKid = "3d8a1f2b9c4e5677889900aabbccddeeff001122"

// newSignatureTestChannel returns a channel whose JWKS cache already holds the
// public half of gchatTestKey under gchatTestKid -- the same field refreshJWKS
// populates in production -- and whose HTTP edge fails closed. jwksLastFetch is
// deliberately left at its zero value (i.e. stale), so a cache hit must
// short-circuit on its own rather than because the clock says the cache is
// fresh.
func newSignatureTestChannel(t *testing.T) (*GoogleChatChannel, *gchatFakeClient) {
	t.Helper()
	fake := &gchatFakeClient{}
	ch := &GoogleChatChannel{
		saEmail:   "omnipus-bot@omnipus-test.iam.gserviceaccount.com",
		jwksCache: map[string]*rsa.PublicKey{gchatTestKid: &gchatTestKey(t).PublicKey},
		client:    fake,
	}
	return ch, fake
}

// =========================== verifySignature ===============================

// --- positive controls ---
//
// Without these, a verifySignature that rejected everything would look correct
// to every negative test in this file.

func TestVerifySignature_AcceptsValidSignatureFromCachedKey(t *testing.T) {
	ch, fake := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE","space":{"name":"spaces/AAAA"}}`)

	if !ch.verifySignature(body, signGChatBody(t, gchatTestKid, gchatTestKey(t), body)) {
		t.Fatal("verifySignature() = false for a valid kid:RSA-SHA256 signature, want true")
	}
	// Property: a kid already in the cache must be served from cache, with no
	// outbound fetch -- even though jwksLastFetch is stale.
	if n := fake.callCount(); n != 0 {
		t.Errorf("JWKS fetches for a cached kid = %d, want 0 (last URL %v)", n, fake.lastRequest().URL)
	}
}

func TestVerifySignature_AcceptsValidSignatureAfterResolvingKidFromJWKS(t *testing.T) {
	const uncachedKid = "aa11bb22cc33dd44ee55ff6600778899aabbccdd"
	ch, fake := newSignatureTestChannel(t)
	delete(ch.jwksCache, gchatTestKid) // force the resolution path
	fake.resp = func(*http.Request) (*http.Response, error) {
		return gchatHTTPResponse(http.StatusOK, gchatJWKSBody(uncachedKid, &gchatTestKey(t).PublicKey)), nil
	}
	body := []byte(`{"type":"ADDED_TO_SPACE"}`)

	if !ch.verifySignature(body, signGChatBody(t, uncachedKid, gchatTestKey(t), body)) {
		t.Fatal("verifySignature() = false for a valid signature whose kid resolves via JWKS, want true")
	}

	// Assert what was requested, not merely that something was.
	if n := fake.callCount(); n != 1 {
		t.Fatalf("JWKS fetches = %d, want 1", n)
	}
	req := fake.lastRequest()
	if req.Method != http.MethodGet {
		t.Errorf("JWKS request method = %q, want %q", req.Method, http.MethodGet)
	}
	// The key material must come from Google over TLS, scoped to our own
	// service account -- otherwise the kid could be resolved by an attacker.
	if req.URL.Scheme != "https" {
		t.Errorf("JWKS request scheme = %q, want %q (key material must not travel in clear)", req.URL.Scheme, "https")
	}
	if req.URL.Host != "www.googleapis.com" {
		t.Errorf("JWKS request host = %q, want %q", req.URL.Host, "www.googleapis.com")
	}
	if !strings.Contains(req.URL.Path, ch.saEmail) {
		t.Errorf("JWKS request path = %q, want it scoped to service account %q", req.URL.Path, ch.saEmail)
	}

	// The resolved key must be cached under its kid for reuse.
	if got := ch.jwksCache[uncachedKid]; got == nil || got.N.Cmp(gchatTestKey(t).N) != 0 {
		t.Errorf("jwksCache[%q] = %v, want the fetched RSA public key", uncachedKid, got)
	}
}

// --- negative cases ---

func TestVerifySignature_RejectsTamperedBody(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	signed := []byte(`{"type":"MESSAGE","message":{"text":"transfer 1 unit"}}`)
	tampered := []byte(`{"type":"MESSAGE","message":{"text":"transfer 9 unit"}}`)
	header := signGChatBody(t, gchatTestKid, gchatTestKey(t), signed)

	if ch.verifySignature(tampered, header) {
		t.Fatal("verifySignature() = true for a body that differs from the signed bytes, want false")
	}
	// A single trailing byte appended after signing must also break it.
	if ch.verifySignature(append(append([]byte{}, signed...), ' '), header) {
		t.Fatal("verifySignature() = true for signed body + one appended byte, want false")
	}
}

func TestVerifySignature_RejectsMissingSignature(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)

	if ch.verifySignature(body, "") {
		t.Fatal("verifySignature() = true for an empty signature header, want false")
	}
}

func TestVerifySignature_RejectsMalformedHeader(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)
	valid := signGChatBody(t, gchatTestKid, gchatTestKey(t), body)
	_, rawSig, _ := strings.Cut(valid, ":")

	cases := []struct {
		name   string
		header string
	}{
		// The documented shape is "kid:base64signature". Anything else is
		// unparseable and must be rejected, never treated as a bare signature.
		{"no colon separator, bare base64 signature", rawSig},
		{"no colon separator, kid only", gchatTestKid},
		{"kid present, signature absent", gchatTestKid + ":"},
		{"colon only", ":"},
		{"undecodable base64 signature", gchatTestKid + ":!!!not-base64!!!"},
		{"base64 with invalid padding", gchatTestKid + ":" + strings.TrimRight(rawSig, "=") + "==="},
		{"whitespace-padded signature", gchatTestKid + ": " + rawSig},
		{"empty kid with valid signature bytes", ":" + rawSig},
		{"kid and signature swapped", rawSig + ":" + gchatTestKid},
		{"header is only whitespace", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ch.verifySignature(body, tc.header) {
				t.Fatalf("verifySignature(body, %q) = true, want false", tc.header)
			}
		})
	}
}

func TestVerifySignature_RejectsSignatureFromForeignKey(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)

	// Attacker signs with their own key but presents the legitimate kid, which
	// resolves to the legitimate public key.
	if ch.verifySignature(body, signGChatBody(t, gchatTestKid, gchatForeignKey(t), body)) {
		t.Fatal("verifySignature() = true for a signature made with a key other than the one the kid resolves to, want false")
	}
}

func TestVerifySignature_RejectsUnknownKid(t *testing.T) {
	ch, fake := newSignatureTestChannel(t)
	// JWKS resolves, but does not contain the presented kid.
	fake.resp = func(*http.Request) (*http.Response, error) {
		return gchatHTTPResponse(http.StatusOK, gchatJWKSBody("some-other-kid", &gchatTestKey(t).PublicKey)), nil
	}
	body := []byte(`{"type":"MESSAGE"}`)

	if ch.verifySignature(body, signGChatBody(t, "kid-that-google-never-published", gchatTestKey(t), body)) {
		t.Fatal("verifySignature() = true for a kid absent from the JWKS, want false")
	}
}

func TestVerifySignature_RejectsWhenJWKSFetchFails(t *testing.T) {
	ch, fake := newSignatureTestChannel(t)
	delete(ch.jwksCache, gchatTestKid)
	fake.resp = func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	}
	body := []byte(`{"type":"MESSAGE"}`)

	// Fail closed: an unresolvable key must reject, not admit.
	if ch.verifySignature(body, signGChatBody(t, gchatTestKid, gchatTestKey(t), body)) {
		t.Fatal("verifySignature() = true when the key could not be resolved, want false (must fail closed)")
	}
}

func TestVerifySignature_RejectsTruncatedAndCorruptedSignature(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)
	digest := sha256.Sum256(body)
	sig, err := rsa.SignPKCS1v15(rand.Reader, gchatTestKey(t), crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign body: %v", err)
	}

	// One byte short of the modulus length.
	truncated := gchatTestKid + ":" + base64.StdEncoding.EncodeToString(sig[:len(sig)-1])
	if ch.verifySignature(body, truncated) {
		t.Error("verifySignature() = true for a signature one byte short, want false")
	}

	// Correct length, one flipped bit in the last byte.
	flipped := append([]byte{}, sig...)
	flipped[len(flipped)-1] ^= 0x01
	if ch.verifySignature(body, gchatTestKid+":"+base64.StdEncoding.EncodeToString(flipped)) {
		t.Error("verifySignature() = true for a signature with one flipped bit, want false")
	}

	// All-zero signature of the right length.
	if ch.verifySignature(body, gchatTestKid+":"+base64.StdEncoding.EncodeToString(make([]byte, len(sig)))) {
		t.Error("verifySignature() = true for an all-zero signature, want false")
	}
}

func TestVerifySignature_RejectsWrongDigestAlgorithm(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)

	// The contract is SHA-256. A well-formed PKCS#1 v1.5 signature over a
	// SHA-512 digest, by the correct key, must still be rejected.
	digest := sha512.Sum512(body)
	sig, err := rsa.SignPKCS1v15(rand.Reader, gchatTestKey(t), crypto.SHA512, digest[:])
	if err != nil {
		t.Fatalf("sign body with SHA-512: %v", err)
	}
	if ch.verifySignature(body, gchatTestKid+":"+base64.StdEncoding.EncodeToString(sig)) {
		t.Fatal("verifySignature() = true for a SHA-512 signature, want false (contract is SHA-256)")
	}
}

func TestVerifySignature_RejectsAlternativeSignatureScheme(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":"MESSAGE"}`)

	// RSASSA-PSS over the right digest with the right key. The verifier only
	// ever promised PKCS#1 v1.5, so accepting PSS would mean the scheme is
	// attacker-selectable.
	digest := sha256.Sum256(body)
	sig, err := rsa.SignPSS(rand.Reader, gchatTestKey(t), crypto.SHA256, digest[:], nil)
	if err != nil {
		t.Fatalf("sign body with PSS: %v", err)
	}
	if ch.verifySignature(body, gchatTestKid+":"+base64.StdEncoding.EncodeToString(sig)) {
		t.Fatal("verifySignature() = true for an RSASSA-PSS signature, want false")
	}
}

func TestVerifySignature_RejectsSignatureOverEmptyBodyForNonEmptyBody(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)

	// Boundary: the empty body is a legitimate input to sign, but its signature
	// must not validate a non-empty body (and vice versa).
	emptySig := signGChatBody(t, gchatTestKid, gchatTestKey(t), []byte{})
	if !ch.verifySignature([]byte{}, emptySig) {
		t.Fatal("verifySignature() = false for a valid signature over an empty body, want true")
	}
	if ch.verifySignature([]byte(`{"type":"MESSAGE"}`), emptySig) {
		t.Fatal("verifySignature() = true using the empty-body signature for a non-empty body, want false")
	}
}

// ============================ webhookHandler ================================

// nonMessageEvent is a syntactically valid event that processEvent ignores, so
// an accepted request exercises the handler's status path without dispatching
// into the bus.
const nonMessageEvent = `{"type":"ADDED_TO_SPACE","space":{"name":"spaces/AAAA","type":"ROOM"}}`

func gchatWebhookRequest(body []byte, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhook/google-chat", bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("Google-Signature", signature)
	}
	return req
}

func TestWebhookHandler_AcceptsValidlySignedRequestWith200(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(nonMessageEvent)
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, gchatWebhookRequest(body, signGChatBody(t, gchatTestKid, gchatTestKey(t), body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a validly signed request, want %d. body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestWebhookHandler_RejectsForgedSignatureWith403(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(nonMessageEvent)
	rec := httptest.NewRecorder()

	// Forged: correct kid, attacker's key.
	ch.webhookHandler(rec, gchatWebhookRequest(body, signGChatBody(t, gchatTestKid, gchatForeignKey(t), body)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d for a forged signature, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_RejectsTamperedBodyWith403(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	signedBody := []byte(nonMessageEvent)
	header := signGChatBody(t, gchatTestKid, gchatTestKey(t), signedBody)
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, gchatWebhookRequest([]byte(`{"type":"MESSAGE","space":{"name":"spaces/EVIL"}}`), header))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d for a body that does not match the signature, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_RejectsMissingSignatureHeaderWith403(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, gchatWebhookRequest([]byte(nonMessageEvent), ""))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d with no Google-Signature header, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_RejectsMalformedSignatureHeaderWith403(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(nonMessageEvent)

	for _, header := range []string{
		"no-colon-here",
		gchatTestKid + ":!!!not-base64!!!",
		gchatTestKid + ":",
		":",
	} {
		t.Run(header, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ch.webhookHandler(rec, gchatWebhookRequest(body, header))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d for Google-Signature %q, want %d", rec.Code, header, http.StatusForbidden)
			}
		})
	}
}

func TestWebhookHandler_RejectsNonPostWith405(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ch.webhookHandler(rec, httptest.NewRequest(method, "/webhook/google-chat", nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d for %s, want %d", rec.Code, method, http.StatusMethodNotAllowed)
			}
		})
	}
}

// TestWebhookHandler_BodySizeBoundary pins the documented 1 MiB limit at
// max and max+1. The limit must be enforced before any signature work, so an
// oversized body cannot be used to force expensive verification.
func TestWebhookHandler_BodySizeBoundary(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)

	t.Run("exactly max is not rejected for size", func(t *testing.T) {
		body := bytes.Repeat([]byte("A"), googleChatMaxBodySize)
		rec := httptest.NewRecorder()
		ch.webhookHandler(rec, gchatWebhookRequest(body, "invalid:c2ln"))
		// Reaches the signature check and fails there -- not a 413.
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d at exactly googleChatMaxBodySize, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("max plus one is rejected with 413 before signature check", func(t *testing.T) {
		body := bytes.Repeat([]byte("A"), googleChatMaxBodySize+1)
		rec := httptest.NewRecorder()
		// A syntactically valid header, so a 413 can only come from the size
		// check running first.
		ch.webhookHandler(rec, gchatWebhookRequest(body, signGChatBody(t, gchatTestKid, gchatTestKey(t), body)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d at googleChatMaxBodySize+1, want %d", rec.Code, http.StatusRequestEntityTooLarge)
		}
	})
}

// TestWebhookHandler_RejectsUnparseableBodyWith400 pins the ordering: the
// signature is checked before the payload is parsed, so a validly signed but
// malformed payload is a 400 (client error), never a 403 and never a 200.
func TestWebhookHandler_RejectsUnparseableBodyWith400(t *testing.T) {
	ch, _ := newSignatureTestChannel(t)
	body := []byte(`{"type":`)
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, gchatWebhookRequest(body, signGChatBody(t, gchatTestKid, gchatTestKey(t), body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d for a validly signed but unparseable body, want %d", rec.Code, http.StatusBadRequest)
	}
}

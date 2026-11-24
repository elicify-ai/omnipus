package agent

// S20/FR013: only the external TLS Git server is replaced. CGI executes the
// installed git-http-backend; successful receive-pack must update a real ref.
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type programsGitReceiverState struct {
	mu              sync.Mutex
	users           []string
	receivePacks    int
	unauthenticated int
}

func (s *programsGitReceiverState) snapshot() ([]string, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.users...), s.receivePacks, s.unauthenticated
}

type programsGitReceiver struct {
	server *httptest.Server
	state  *programsGitReceiverState
	cert   string
	repo   string
}

func programsGitTLSCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	// A per-test self-signed CA/server certificate; the private key stays in
	// memory. SANs bind the only requested host and the loopback controls.
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "S20 local fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"github.com"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newProgramsGitReceiver(t *testing.T, git, home string) *programsGitReceiver {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "s20.git")
	programsGitSetup(t, git, home, "", "init", "--bare", repo)
	programsGitSetup(t, git, home, "", "--git-dir", repo, "config", "http.receivepack", "true")
	backend := filepath.Join(programsGitSetup(t, git, home, "", "--exec-path"), "git-http-backend")
	info, err := os.Stat(backend)
	require.NoError(t, err, "installed smart-Git CGI is a fixture prerequisite")
	require.False(t, info.IsDir())
	state := &programsGitReceiverState{}
	cgiHandler := &cgi.Handler{Path: backend, Env: []string{
		"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=x-access-token",
	}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "x-access-token" || password != "tok_valid_12345" {
			state.mu.Lock()
			state.unauthenticated++
			state.mu.Unlock()
			w.Header().Set("WWW-Authenticate", `Basic realm="S20"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		state.mu.Lock()
		state.users = append(state.users, user) // never retain the password/header
		if r.Method == http.MethodPost && r.URL.Path == "/s20.git/git-receive-pack" {
			state.receivePacks++
		}
		state.mu.Unlock()
		cgiHandler.ServeHTTP(w, r)
	}))
	certificate, publicPEM := programsGitTLSCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	t.Cleanup(server.Close)
	certPath := filepath.Join(root, "s20-ca.pem")
	require.NoError(t, os.WriteFile(certPath, publicPEM, 0600))
	return &programsGitReceiver{server: server, state: state, cert: certPath, repo: repo}
}

// A normal HTTP CONNECT proxy, constrained to a single requested host and a
// single preselected loopback destination. It never resolves/dials github.com.
func programsGitLocalProxy(t *testing.T, destination string) string {
	t.Helper()
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "github.com:443" {
			http.Error(w, "S20 proxy accepts only the fixture Git host", http.StatusForbidden)
			return
		}
		peer, err := net.DialTimeout("tcp", destination, 5*time.Second)
		if err != nil {
			http.Error(w, "S20 local receiver unavailable", http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = peer.Close()
			http.Error(w, "S20 CONNECT transport unavailable", http.StatusInternalServerError)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			_ = peer.Close()
			return
		}
		mu.Lock()
		connections[peer], connections[client] = true, true
		mu.Unlock()
		closeBoth := sync.OnceFunc(func() { _ = peer.Close(); _ = client.Close() })
		defer closeBoth()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer closeBoth()
			_, _ = io.Copy(peer, buffered)
		}()
		_, _ = io.Copy(client, peer)
	}))
	t.Cleanup(func() {
		proxy.Close()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return proxy.URL
}

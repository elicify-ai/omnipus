package health

import (
	"net"
	"testing"
)

// #1823: NewServer("::", port) must produce an address net.Listen accepts.
func TestNewServer_IPv6WildcardAddrIsListenable_1823(t *testing.T) {
	for _, host := range []string{"::", "::1", "[::1]", "[::]", "127.0.0.1"} {
		s := NewServer(host, 0)
		ln, err := net.Listen("tcp", s.server.Addr)
		if err != nil {
			t.Errorf("host %q: addr %q is not listenable: %v", host, s.server.Addr, err)
			continue
		}
		_ = ln.Close()
	}
}

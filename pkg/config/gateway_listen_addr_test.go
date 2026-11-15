package config

import "testing"

// #1823: gateway.host "::" built ":::5000" with "%s:%d", which net.Listen
// rejects ("too many colons"). The listen address must be a valid host:port
// for IPv4, IPv6 and wildcard hosts.
func TestGatewayListenAddr_1823(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 5000, "127.0.0.1:5000"},
		{"0.0.0.0", 18800, "0.0.0.0:18800"},
		{"", 5000, ":5000"},
		{"::", 5000, "[::]:5000"},
		{"::1", 5000, "[::1]:5000"},
		{"localhost", 80, "localhost:80"},
	}
	for _, c := range cases {
		g := GatewayConfig{Host: c.host, Port: c.port}
		if got := g.ListenAddr(); got != c.want {
			t.Errorf("host %q port %d: ListenAddr() = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

// A host already written with brackets must not be bracketed twice.
func TestGatewayListenAddr_BracketedHost_1823(t *testing.T) {
	for host, want := range map[string]string{"[::1]": "[::1]:5000", "[::]": "[::]:5000"} {
		if got := (GatewayConfig{Host: host, Port: 5000}).ListenAddr(); got != want {
			t.Errorf("host %q: %q, want %q", host, got, want)
		}
	}
}

func TestURLHost_1823(t *testing.T) {
	for host, want := range map[string]string{
		"127.0.0.1": "127.0.0.1", "localhost": "localhost", "::1": "[::1]",
		"[::1]": "[::1]", "2001:db8::1": "[2001:db8::1]",
	} {
		if got := URLHost(host); got != want {
			t.Errorf("URLHost(%q) = %q, want %q", host, got, want)
		}
	}
}

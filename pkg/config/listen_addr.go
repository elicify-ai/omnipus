package config

import (
	"net"
	"strconv"
	"strings"
)

// ListenAddr returns the "host:port" the gateway binds to. It uses
// net.JoinHostPort so an IPv6 host such as "::" becomes "[::]:port"; the
// "%s:%d" form produced ":::port", which net.Listen rejects (#1823).
func (g GatewayConfig) ListenAddr() string {
	return JoinHostPort(g.Host, g.Port)
}

// JoinHostPort is net.JoinHostPort for a configured host that may already carry
// one enclosing bracket pair ("[::1]" worked before #1823 because the old
// "%s:%d" form kept it as written; JoinHostPort alone would double it).
func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(trimBrackets(host), strconv.Itoa(port))
}

// URLHost returns host as it must appear in a URL authority: a bare IPv6
// literal is wrapped in brackets, an already bracketed host and any other host
// are returned unchanged.
func URLHost(host string) string {
	h := trimBrackets(host)
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return host
}

func trimBrackets(host string) string {
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}
	return host
}

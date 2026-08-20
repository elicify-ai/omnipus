// Package netinfo provides a bind-aware access-URL builder for the gateway.
//
// BuildAccessURLs is pure and takes its interface-IP list as an explicit
// parameter so callers in tests can inject a controlled list without
// depending on the host network. The real caller passes LocalIPv4s().
//
// Resolution rules (FR-011, US-9, A4):
//
//  1. publicURL != "" → Primary = publicURL; no extras; no hint.
//  2. host is "0.0.0.0" or "" → Primary = http://localhost:port;
//     Extras = http://<ip>:port for each non-loopback IPv4.
//  3. host is "127.0.0.1" or "localhost" → Primary = http://localhost:port;
//     Extras empty; Hint set to guide the operator.
//  4. any other explicit host → Primary = http://<host>:port.
package netinfo

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// AccessURLs holds the set of access URLs computed for a given gateway binding.
type AccessURLs struct {
	// Primary is the canonical URL the operator should open.
	Primary string
	// Extras are additional reachable URLs (LAN IPs when bound to 0.0.0.0).
	// May be empty.
	Extras []string
	// Hint is an optional operator-facing guidance message (non-empty only when
	// the gateway is bound to loopback and LAN access is therefore unavailable).
	Hint string
}

// BuildAccessURLs computes access URLs according to FR-011 rules.
//
// Parameters:
//   - host: the value of gateway.host ("0.0.0.0", "127.0.0.1", "localhost",
//     a specific IP/hostname, or "").
//   - port: the value of gateway.port.
//   - publicURL: the value of gateway.public_url (empty string if not set).
//   - ipv4s: non-loopback IPv4 strings to use as LAN extras when host is
//     "0.0.0.0" or "". In production this is LocalIPv4s(); in tests it is a
//     static slice.
func BuildAccessURLs(host string, port int, publicURL string, ipv4s []string) AccessURLs {
	// Rule 1: public URL takes full precedence.
	if publicURL != "" {
		return AccessURLs{Primary: publicURL}
	}

	localhost := fmt.Sprintf("http://localhost:%d", port)

	switch host {
	case "0.0.0.0", "":
		// Rule 2: listening on all interfaces — expose localhost + LAN IPs.
		extras := make([]string, 0, len(ipv4s))
		portStr := strconv.Itoa(port)
		for _, ip := range ipv4s {
			extras = append(extras, "http://"+net.JoinHostPort(ip, portStr))
		}
		return AccessURLs{
			Primary: localhost,
			Extras:  extras,
		}
	case "127.0.0.1", "localhost":
		// Rule 3: loopback-only — localhost + operator hint.
		return AccessURLs{
			Primary: localhost,
			Hint:    "bound to localhost — set gateway.host=0.0.0.0 to allow other devices",
		}
	default:
		// Rule 4: explicit host (IP or hostname).
		return AccessURLs{
			Primary: "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		}
	}
}

// LocalIPv4s enumerates non-loopback IPv4 addresses from all active network
// interfaces on this machine. It is used by the real callers (start, onboard)
// — unit tests inject a static slice via BuildAccessURLs instead.
//
// Note OBS-1: a Docker/VPN NIC may sort first among the extras because
// net.Interfaces returns interfaces in system order (typically kernel
// registration order). Listing all extras lets the operator pick the
// reachable one.
func LocalIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				result = append(result, v4.String())
			}
		}
	}
	return result
}

// AccessURLsFromConfig reads host, port (default 5000), and public_url from
// the config.json at configPath and calls BuildAccessURLs with LocalIPv4s().
// It is the single implementation of the config-read + URL-build sequence
// previously duplicated between onboard.printAccessURLBlock and
// gateway/command.go's printStartURLBlock. Errors reading or parsing the
// config are silently swallowed and defaults are used — the URL block is
// informational and must never cause callers to fail.
func AccessURLsFromConfig(configPath string) AccessURLs {
	host := ""
	port := 5000
	publicURL := ""

	raw, err := os.ReadFile(configPath)
	if err == nil {
		var m map[string]any
		if jsonErr := json.Unmarshal(raw, &m); jsonErr == nil {
			gw, _ := m["gateway"].(map[string]any)
			if gw != nil {
				if h, ok := gw["host"].(string); ok {
					host = h
				}
				if p, ok := gw["port"].(float64); ok && p > 0 {
					port = int(p)
				}
				if pu, ok := gw["public_url"].(string); ok {
					publicURL = pu
				}
			}
		}
	}

	return BuildAccessURLs(host, port, publicURL, LocalIPv4s())
}

// Render returns a human-readable multi-line block for printing at gateway
// start and onboard completion. The format is designed to be scannable at a
// glance and actionable.
func Render(u AccessURLs) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "  This machine:   %s\n", u.Primary)
	for _, e := range u.Extras {
		fmt.Fprintf(&sb, "  On your network: %s\n", e)
	}
	if u.Hint != "" {
		fmt.Fprintf(&sb, "  Hint: %s\n", u.Hint)
	}
	return sb.String()
}

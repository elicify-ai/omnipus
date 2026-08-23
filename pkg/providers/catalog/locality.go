package catalog

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// DeriveLocality is the single locality predicate (FR-039, X-16/X-17):
//
//	local ⇔ protocol = ollama ∨ id ∈ {ollama, vllm, lmstudio}
//	        ∨ (custom ∧ api host loopback/private)
//
// else cloud. FR-039's text writes "protocol ∈ {ollama, vllm}"; `vllm` is
// not a protocol (FR-002's set) but a local-providers-file id whose protocol
// is openai-compatible, so it is matched on id here. A hosted (non-custom)
// row with a private host is still cloud — FR-033 rejects that document
// instead of reclassifying it.
func DeriveLocality(id string, protocol Protocol, custom bool, api string) Locality {
	if protocol == ProtocolOllama {
		return LocalityLocal
	}
	switch id {
	case "ollama", "vllm", "lmstudio":
		return LocalityLocal
	}
	if custom {
		if u, err := url.Parse(api); err == nil && isLocalHost(u.Hostname()) {
			return LocalityLocal
		}
	}
	return LocalityCloud
}

// isLocalHost reports whether host (no port) is a loopback name or an IP
// literal in a loopback, link-local, RFC 1918, ULA, unspecified, or
// cloud-metadata range. Hostnames other than localhost are not local — a
// DNS name resolving privately is a deployment choice, not a catalog fact.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(h)
	if err != nil {
		// Not an IP literal (net.ParseIP accepts zone-less forms netip may
		// reject, e.g. with leading zeros — treat those as literals too).
		ip := net.ParseIP(h)
		if ip == nil {
			return false
		}
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			return false
		}
		addr = a
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsPrivate() || addr.IsUnspecified() || addr.IsMulticast() {
		return true
	}
	// Cloud metadata endpoints (IPv4 169.254.169.254 is already link-local;
	// GCP/AWS IPv6 metadata is fd00:ec2::254, already ULA → IsPrivate).
	return false
}

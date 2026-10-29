// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// LinkScheme is the prefix of every chat link to a Library file
// (chat-links spec, "Link grammar"): omnipus://file/<workspace>/<path>.
const LinkScheme = "omnipus://file/"

// maxLinkSegment is the longest workspace id or path segment a link may carry,
// counted in characters. The SPA rejects a longer segment, so the encoder must
// never produce one.
const maxLinkSegment = 255

// linkWorkspacePattern is the SPA's workspace-segment grammar. It is stricter
// than the workspace package's own id rule, which the encoder also applies.
var linkWorkspacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// EncodeLibraryLink builds the one chat link for a file in a workspace
// Library: omnipus://file/<workspaceID>/<rel>, with each path segment
// percent-encoded as UTF-8 (everything except A-Z, a-z, 0-9 and "-._~" is
// escaped, which covers "(", ")", "[", "]", space, "<", ">", "%", "#" and
// "?"). rel is a Library path: workspace-relative, mount name first for a
// mounted folder.
//
// ok is false — and no link may be shown — when the workspace id or the path
// falls outside what the SPA's strict link parser accepts (chat-links spec,
// "Machine-Verifiable Constraints"): an empty path (the Library root is not a
// valid target), a path CleanRelPath rejects or would rewrite, a segment over
// 255 characters, invalid UTF-8, or a DEL, C1, bidirectional-control or
// zero-width character. Emitting a link the SPA then refuses would show the
// agent a link that renders as plain text, so the encoder refuses first.
func EncodeLibraryLink(workspaceID, rel string) (string, bool) {
	if !validLinkWorkspace(workspaceID) {
		return "", false
	}
	if rel == "" {
		return "", false
	}
	cleaned, err := CleanRelPath(rel)
	if err != nil || cleaned != rel {
		return "", false
	}
	segs := strings.Split(rel, "/")
	var b strings.Builder
	b.WriteString(LinkScheme)
	b.WriteString(workspaceID)
	for _, seg := range segs {
		if !validLinkSegment(seg) {
			return "", false
		}
		b.WriteByte('/')
		writeEncodedSegment(&b, seg)
	}
	return b.String(), true
}

// validLinkWorkspace applies the SPA's workspace-segment grammar plus the
// workspace package's id rule (non-empty, no separators, no "..").
func validLinkWorkspace(id string) bool {
	if id == "" || len(id) > maxLinkSegment || !linkWorkspacePattern.MatchString(id) {
		return false
	}
	if strings.Trim(id, ".") == "" || strings.Contains(id, "..") {
		return false
	}
	return true
}

// validLinkSegment reports whether one path segment may appear in a link.
func validLinkSegment(seg string) bool {
	if seg == "" || !utf8.ValidString(seg) || utf8.RuneCountInString(seg) > maxLinkSegment {
		return false
	}
	for _, r := range seg {
		if linkRejectedRune(r) {
			return false
		}
	}
	return true
}

// linkRejectedRune is the explicit SPA-only rejection list for characters:
// C0 controls (CleanRelPath already refuses them), DEL, C1 controls,
// bidirectional controls and zero-width characters. A name carrying any of
// them can be made to read as something it is not, so it gets no link.
func linkRejectedRune(r rune) bool {
	switch {
	case r <= 0x1F, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F:
		return true
	case r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF:
		return true
	}
	return false
}

// writeEncodedSegment percent-encodes seg's UTF-8 bytes, keeping only the
// RFC 3986 unreserved characters literal. Hex digits are lowercase, matching
// the shared golden vectors.
func writeEncodedSegment(b *strings.Builder, seg string) {
	const hex = "0123456789abcdef"
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if isUnreservedByte(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
}

func isUnreservedByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package media

import (
	"fmt"
	"strings"
)

const refPrefix = "media://"

// Ref is a validated media:// reference. A zero-value Ref is invalid.
// The distinct type prevents unresolved media refs from being confused with
// resolved data-URLs or local paths in the LLM content pipeline.
// Construct via ParseMediaRef; the string form is obtained via String().
type Ref struct {
	raw string // always has the "media://" prefix
}

// ParseMediaRef validates and wraps a media:// reference string.
// Returns an error when the string is empty, does not start with "media://",
// or the ID portion is empty.
func ParseMediaRef(s string) (Ref, error) {
	if !strings.HasPrefix(s, refPrefix) {
		return Ref{}, fmt.Errorf("media: ref %q does not have media:// prefix", s)
	}
	id := strings.TrimPrefix(s, refPrefix)
	if id == "" {
		return Ref{}, fmt.Errorf("media: ref %q has empty ID", s)
	}
	return Ref{raw: s}, nil
}

// String returns the canonical "media://..." form.
func (r Ref) String() string { return r.raw }

// IsValid reports whether this Ref was constructed by ParseMediaRef and is
// safe to pass to a MediaStore.
func (r Ref) IsValid() bool { return strings.HasPrefix(r.raw, refPrefix) }

// FilterValidRefs returns only the elements of refs that parse as valid
// media:// references, discarding any that do not. This is the
// centralized ingress guard used by resolveMediaRefs and every channel
// that populates InboundMessage.Media.
func FilterValidRefs(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, s := range refs {
		if _, err := ParseMediaRef(s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

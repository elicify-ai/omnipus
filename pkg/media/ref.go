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
// The distinct type is a validation newtype: it guarantees that the wrapped
// string has the "media://" prefix and a non-empty ID. The InboundMessage.Media
// field is still []string (not []Ref) for backwards compatibility; Ref is used
// at validation call-sites (ParseMediaRef, FilterValidRefs) to enforce the
// invariant before strings enter the agent pipeline.
// Construct via ParseMediaRef; the string form is obtained via String().
type Ref struct {
	raw string // always non-empty and has the "media://" prefix with non-empty ID
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
// safe to pass to a MediaStore. A zero-value Ref returns false.
// Consistent with the constructor: only non-empty raw values are set by
// ParseMediaRef (empty raw = zero-value = invalid).
func (r Ref) IsValid() bool { return r.raw != "" }

// FilterValidRefs returns only the elements of refs that parse as valid
// media:// references (non-empty ID), discarding any that do not.
// Callers that need batch ingress filtering can use this instead of
// calling ParseMediaRef in a loop. The WebSocket ingress chokepoint
// in pkg/gateway/websocket.go calls ParseMediaRef directly per-ref;
// resolveMediaRefs in pkg/agent/loop_media.go is the single LLM-pipeline
// chokepoint that validates refs before any content reaches the model.
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

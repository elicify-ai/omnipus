//go:build mipsle || netbsd || (freebsd && arm)

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package whatsapp

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestStubReturnsError asserts that, in builds where native WhatsApp is not
// compiled in (an architecture where modernc.org/sqlite is unavailable),
// NewWhatsAppNativeChannel fails closed: it returns a nil channel
// and a non-nil error. This guards the build-tag split — a regression that made
// the stub return (nil, nil) would let initChannels register a dead channel that
// silently never pairs.
func TestStubReturnsError(t *testing.T) {
	ch, err := NewWhatsAppNativeChannel(config.WhatsAppConfig{Enabled: true}, nil, "")
	if err == nil {
		t.Fatal("expected an error from the WhatsApp native stub, got nil")
	}
	if ch != nil {
		t.Fatalf("expected a nil channel from the stub, got %T", ch)
	}
}

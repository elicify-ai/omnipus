package gateway

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// #1823: a failed boot is now shown on the terminal. The terminal copy must never
// carry free-form error text (it can hold credentials in any shape); only a
// listener bind failure, which holds just an address and an OS error, is shown.
func TestTerminalBootMessage_BindFailureIsShown_1823(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	_, bindErr := net.Listen("tcp", occupied.Addr().String())
	if bindErr == nil {
		t.Fatal("SETUP: second listen on an occupied port must fail")
	}
	wrapped := fmt.Errorf("error starting channels: channels: bind shared HTTP server at %s: %w", occupied.Addr(), bindErr)

	got := terminalBootMessage(wrapped, "/home/x/.omnipus")
	if !strings.Contains(got, occupied.Addr().String()) || !strings.Contains(got, "address already in use") {
		t.Errorf("bind failure must name the address and the OS error: %q", got)
	}
}

func TestTerminalBootMessage_OtherErrorsCarryNoText_1823(t *testing.T) {
	secrets := []string{
		"http://alice:SECRET@proxy.example:8080",
		"http://alice:SECRET/suffix@proxy.example:8080",
		"https://alice:prefix@SECRET@host",
		"//alice:SECRET@host",
		"https://host/?access_token=SECRET",
		"password=SECRET",
	}
	for _, s := range secrets {
		got := terminalBootMessage(errors.New("provider setup failed: "+s), "/home/x/.omnipus")
		if strings.Contains(got, "SECRET") || strings.Contains(got, "provider setup failed") {
			t.Errorf("terminal message carries error text for %q: %q", s, got)
		}
		if !strings.Contains(got, "/home/x/.omnipus/logs/gateway_panic.log") {
			t.Errorf("terminal message must point at the panic log: %q", got)
		}
	}
}

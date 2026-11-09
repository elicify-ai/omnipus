package security

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingResolver answers every lookup with an error, so a host literal the
// checker fails to recognise surfaces as an untyped resolution failure
// instead of whatever the machine's resolver happens to say.
type failingResolver struct{}

func (failingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.New("lookup refused by test resolver")
}

// TestCheckURL_PortlessIPLiterals — #1700 re-check finding F1: a bracketed
// IPv6 literal with no port (http://[::1]/x) kept its brackets, missed the
// IP-literal check and came back as an untyped resolution failure, so a
// redirect to it was reported as "unreachable" instead of an address-policy
// refusal. Every portless form must be refused exactly like its explicit-port
// control: typed, and with the same message.
func TestCheckURL_PortlessIPLiterals(t *testing.T) {
	cases := []struct {
		name     string
		portless string
		control  string
	}{
		{"ipv6_loopback", "http://[::1]/x", "http://[::1]:80/x"},
		{"ipv4_mapped_loopback", "http://[::ffff:127.0.0.2]/x", "http://[::ffff:127.0.0.2]:80/x"},
		{"ipv6_link_local_with_zone", "http://[fe80::1%25eth0]/x", "http://[fe80::1]:80/x"},
		{"ipv6_unique_local_https", "https://[fd00::5]", "https://[fd00::5]:443"},
		{"ipv4_trailing_dot", "http://127.0.0.1./x", "http://127.0.0.1:80/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := NewSSRFChecker(nil)
			sc.SetResolver(failingResolver{})
			ctx := context.Background()

			controlErr := sc.CheckURL(ctx, tc.control)
			require.Error(t, controlErr, "control %s must be refused", tc.control)
			require.True(t, IsBlockedAddress(controlErr), "control must be a policy refusal: %v", controlErr)

			err := sc.CheckURL(ctx, tc.portless)
			require.Error(t, err, "%s must be refused", tc.portless)
			assert.True(t, IsBlockedAddress(err),
				"%s must be an address-policy refusal, not a resolution failure: %v", tc.portless, err)
			assert.Equal(t, controlErr.Error(), err.Error(),
				"the portless form must be refused with the same message as its explicit-port control")
		})
	}

	t.Run("public_portless_ipv6_passes", func(t *testing.T) {
		sc := NewSSRFChecker(nil)
		sc.SetResolver(failingResolver{})
		assert.NoError(t, sc.CheckURL(context.Background(), "http://[2606:4700::1111]/x"))
	})
	t.Run("allowlisted_portless_ipv6_passes", func(t *testing.T) {
		sc := NewSSRFChecker([]string{"::1"})
		sc.SetResolver(failingResolver{})
		assert.NoError(t, sc.CheckURL(context.Background(), "http://[::1]/x"))
	})
}

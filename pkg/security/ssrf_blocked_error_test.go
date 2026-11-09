package security

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return f.addrs, f.err
}

// TestIsBlockedAddress tells the guard's address-policy refusals apart from
// ordinary resolution failures (#1700 review finding 1): a caller that saw
// the guard refuse a connection must be able to say so instead of reporting
// "unreachable". The message text of each refusal stays what it was.
func TestIsBlockedAddress(t *testing.T) {
	ctx := context.Background()

	t.Run("loopback_literal", func(t *testing.T) {
		err := NewSSRFChecker(nil).CheckURL(ctx, "http://127.0.0.1:8080/v1")
		require.Error(t, err)
		assert.True(t, IsBlockedAddress(err))
		assert.Equal(t, "SSRF: blocked private IP range 127.0.0.1 (127.0.0.0/8)", err.Error())
	})
	t.Run("metadata_literal", func(t *testing.T) {
		err := NewSSRFChecker(nil).CheckURL(ctx, "http://169.254.169.254/")
		require.Error(t, err)
		assert.True(t, IsBlockedAddress(err))
	})
	t.Run("ipv6_loopback", func(t *testing.T) {
		err := NewSSRFChecker(nil).CheckURL(ctx, "http://[::1]:80/")
		require.Error(t, err)
		assert.True(t, IsBlockedAddress(err))
	})
	t.Run("6to4_embedding_loopback", func(t *testing.T) {
		_, err := NewSSRFChecker(nil).CheckHost(ctx, "2002:7f00:1::")
		require.Error(t, err)
		assert.True(t, IsBlockedAddress(err))
	})
	t.Run("hostname_resolving_to_private", func(t *testing.T) {
		sc := NewSSRFChecker(nil)
		sc.SetResolver(fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}})
		err := sc.CheckURL(ctx, "http://internal.example.test/")
		require.Error(t, err)
		assert.True(t, IsBlockedAddress(err))
	})
	t.Run("dns_failure_is_not_a_policy_refusal", func(t *testing.T) {
		sc := NewSSRFChecker(nil)
		sc.SetResolver(fixedResolver{err: errors.New("no such host")})
		err := sc.CheckURL(ctx, "http://nowhere.example.test/")
		require.Error(t, err)
		assert.False(t, IsBlockedAddress(err))
	})
	t.Run("public_address_passes", func(t *testing.T) {
		assert.NoError(t, NewSSRFChecker(nil).CheckURL(ctx, "http://93.184.216.34/"))
	})
	t.Run("nil_and_plain_errors", func(t *testing.T) {
		assert.False(t, IsBlockedAddress(nil))
		assert.False(t, IsBlockedAddress(errors.New("SSRF: blocked private IP range")))
	})
}

package gateway

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// SF-10: an occupied-port retry must not erase an OS diagnostic, and resource
// exhaustion/permission errors are not occupied ports. Inject only the socket
// edge rather than consuming real shared-machine descriptors.
func TestTrain3Fix3_PortDrawRetainsOSCauseAndRetriesOnlyAddressInUse(t *testing.T) {
	for _, network := range []string{"TCP", "UDP"} {
		t.Run(network, func(t *testing.T) {
			for _, cause := range []error{syscall.EMFILE, syscall.EACCES} {
				t.Run(cause.Error(), func(t *testing.T) {
					calls := 0
					osErr := &net.OpError{Op: "listen", Net: strings.ToLower(network), Err: cause}
					port, err := drawFixturePort(network, 5, func() (int, error) {
						calls++
						return 0, osErr
					})
					require.Zero(t, port)
					require.ErrorIs(t, err, cause, "preserve the underlying OS cause")
					require.EqualError(t, err, fmt.Sprintf("%s fixture port draw 1/5 failed: %v", network, osErr))
					require.Equal(t, 1, calls, "resource/permission errors fail immediately, never pretend to be a bind race")
				})
			}
			calls := 0
			osErr := &net.OpError{Op: "listen", Net: strings.ToLower(network), Err: syscall.EADDRINUSE}
			port, err := drawFixturePort(network, 3, func() (int, error) {
				calls++
				return 0, osErr
			})
			require.Zero(t, port)
			require.Equal(t, 3, calls)
			require.ErrorIs(t, err, syscall.EADDRINUSE)
			require.EqualError(t, err, fmt.Sprintf("%s fixture port draw 3/3 failed: %v", network, osErr))

			calls = 0
			port, err = drawFixturePort(network, 3, func() (int, error) {
				calls++
				if calls == 1 {
					return 0, osErr
				}
				return 32123, nil // injected socket draw, not a real shared port
			})
			require.NoError(t, err)
			require.Equal(t, 32123, port)
			require.Equal(t, 2, calls)
		})
	}
}

type train3PortCloser struct {
	closed bool
	err    error
}

func (s *train3PortCloser) Close() error {
	s.closed = true
	return s.err
}

func TestTrain3Fix3_PortDrawNamesUnexpectedAddressAndCloseError(t *testing.T) {
	for _, network := range []string{"TCP", "UDP"} {
		t.Run(network, func(t *testing.T) {
			socket := &train3PortCloser{}
			port, err := closeFixturePort(network, &net.UnixAddr{Name: "wrong", Net: "unix"}, socket)
			require.Zero(t, port)
			require.EqualError(t, err, network+" fixture received unexpected address type *net.UnixAddr")
			require.True(t, socket.closed, "unexpected address types still close their owned socket")

			closeErr := errors.New("injected close failure")
			socket = &train3PortCloser{err: closeErr}
			var addr net.Addr = &net.TCPAddr{Port: 32123}
			if network == "UDP" {
				addr = &net.UDPAddr{Port: 32123}
			}
			port, err = closeFixturePort(network, addr, socket)
			require.Zero(t, port)
			require.ErrorIs(t, err, closeErr)
			require.True(t, socket.closed)
		})
	}
}

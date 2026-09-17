//go:build linux || darwin

package tools

import (
	"golang.org/x/sys/unix"
	"os"
)

// OpenRegularNonBlocking atomically opens through the anchored handle with a
// nonblocking flag, then verifies the opened object. A path swapped to a FIFO
// between authorization and open therefore cannot strand the turn.
func regularReadOpenFlags() int { return os.O_RDONLY | unix.O_NONBLOCK }

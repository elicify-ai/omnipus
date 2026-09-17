//go:build linux || darwin

package tools

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// OpenRegularNonBlocking atomically opens through the anchored handle with a
// nonblocking flag, then verifies the opened object. A path swapped to a FIFO
// between authorization and open therefore cannot strand the turn.
func (h *PathHandle) OpenRegularNonBlocking() (fs.File, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(h.abs, os.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, wrapOpenErr(err)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, ErrImageSourceNotRegular
		}
		return f, nil
	}
	f, err := h.root.OpenFile(h.rel, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, wrapOpenErr(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrImageSourceNotRegular
	}
	return f, nil
}

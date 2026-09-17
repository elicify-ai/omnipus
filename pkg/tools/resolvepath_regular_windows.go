//go:build windows

package tools

import "io/fs"

func (h *PathHandle) OpenRegularNonBlocking() (fs.File, error) {
	f, err := h.Open()
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrImageSourceNotRegular
	}
	return f, nil
}

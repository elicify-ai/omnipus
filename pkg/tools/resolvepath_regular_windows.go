//go:build windows

package tools

import "os"

func regularReadOpenFlags() int { return os.O_RDONLY }

//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"syscall"
)

func nativeCaseInsensitive() bool { return false }
func rootObjectIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return "", fmt.Errorf("native root identity unavailable")
	}
	return fmt.Sprintf("dev:%d/file:%d", stat.Dev, stat.Ino), nil
}

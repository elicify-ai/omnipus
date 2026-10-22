//go:build linux || darwin

package tools

import (
	"fmt"
	"os"
	"syscall"
)

func fileEffectDirectoryIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("file recovery: anchor is not a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("file recovery: anchor identity unavailable")
	}
	return fmt.Sprintf("%v:%v", stat.Dev, stat.Ino), nil
}

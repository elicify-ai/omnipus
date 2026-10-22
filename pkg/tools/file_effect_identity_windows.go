//go:build windows

package tools

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func fileEffectDirectoryIdentity(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("file recovery: anchor is not a directory")
	}
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &identity); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x:%x:%x", identity.VolumeSerialNumber, identity.FileIndexHigh, identity.FileIndexLow), nil
}

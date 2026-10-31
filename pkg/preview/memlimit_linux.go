// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package preview

import (
	"os"

	"golang.org/x/sys/unix"
)

const ignoreRlimitDataParam = "/sys/module/kernel/parameters/ignore_rlimit_data"

// HostMemoryLimitUnenforced runs the §3.1 D1.4 check on this host.
func HostMemoryLimitUnenforced() (reason, note string) {
	var uts unix.Utsname
	var release string
	err := unix.Uname(&uts)
	if err == nil {
		release = unix.ByteSliceToString(uts.Release[:])
	}
	param, paramErr := os.ReadFile(ignoreRlimitDataParam)
	return dataLimitUnenforced(release, err, param, paramErr)
}

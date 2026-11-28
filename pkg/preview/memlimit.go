// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package preview

import (
	"fmt"
	"strconv"
	"strings"
)

// dataLimitUnenforced decides spec §3.1 D1.4: Linux's data limit governs
// mmap only from kernel 4.7, and the boot option ignore_rlimit_data makes it
// warn-only. It returns the disclosed reason ("" when the limit is enforced)
// and a note to log once ("" when there is nothing to note). An unreadable
// parameter file or an unparsable kernel release counts as enforcing (the
// default) with a note.
func dataLimitUnenforced(release string, releaseErr error, param []byte, paramErr error) (reason, note string) {
	switch {
	case releaseErr != nil:
		note = "kernel release unreadable (" + releaseErr.Error() + "); memory limit treated as enforced"
	default:
		major, minor, ok := kernelVersion(release)
		if !ok {
			note = "kernel release " + strconv.Quote(release) + " not understood; memory limit treated as enforced"
		} else if major < 4 || (major == 4 && minor < 7) {
			return fmt.Sprintf("the Linux kernel %s is older than 4.7", release), ""
		}
	}
	if paramErr != nil {
		msg := "ignore_rlimit_data unreadable (" + paramErr.Error() + "); memory limit treated as enforced"
		if note != "" {
			msg = note + "; " + msg
		}
		return "", msg
	}
	if strings.TrimSpace(string(param)) == "Y" {
		return "the kernel boot option ignore_rlimit_data is set", note
	}
	return "", note
}

// kernelVersion parses the "major.minor" start of a kernel release such as
// "6.12.114-fly" or "4.6.0-1-amd64".
func kernelVersion(release string) (major, minor int, ok bool) {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	digits := parts[1]
	for i, r := range digits {
		if r < '0' || r > '9' {
			digits = digits[:i]
			break
		}
	}
	minor, err = strconv.Atoi(digits)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

//go:build linux

package tools

import (
	"syscall"
	"time"
)

func statCtime(st *syscall.Stat_t) time.Time {
	return time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
}

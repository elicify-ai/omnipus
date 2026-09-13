//go:build linux

package tools

import (
	"syscall"
	"time"
)

func statCtime(st *syscall.Stat_t) time.Time {
	return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
}

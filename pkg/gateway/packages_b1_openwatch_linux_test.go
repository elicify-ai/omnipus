//go:build linux

// packages_b1_openwatch_linux_test.go: the B1 planted-open control's drain,
// which needs the real inotify watcher's event count (packages_t5_openwatch_
// linux_test.go owns the watcher; this file only exposes its count to the
// build-tag-neutral B1 test).
package gateway

import "testing"

func b1OpenWatchDrain(t *testing.T, w *openWatch) int { return w.drain(t) }

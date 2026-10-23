//go:build goolm && stdjson

// Package testhooks supplies test-only access to private passive observers.
// No production package imports this bridge. The aliases deliberately live in
// a different package from their owners: same-package aliases redeclare symbols.
package testhooks

import (
	"testing"
	_ "unsafe" // Required by the compiler for private, test-only symbol aliases.
)

// These slots are nil in production. A missing production call remains a
// runnable assertion failure, not a compile failure or an unconditional fatal.
// Install before the observed operation and restore only after its writers join.

//go:linkname taskBeforeAtomicWrite github.com/elicify-ai/omnipus/pkg/task.taskBeforeAtomicWriteTestHook
var taskBeforeAtomicWrite func(path string, data []byte)

//go:linkname fileEffectAfterHashRead github.com/elicify-ai/omnipus/pkg/tools.fileEffectAfterHashReadTestHook
var fileEffectAfterHashRead func(target string)

// ObserveTaskAtomicWrite installs a passive private Store.write observer.
func ObserveTaskAtomicWrite(t *testing.T, hook func(string, []byte)) {
	t.Helper()
	previous := taskBeforeAtomicWrite
	taskBeforeAtomicWrite = hook
	t.Cleanup(func() { taskBeforeAtomicWrite = previous })
}

// ObserveFileEffectHashRead observes completed hash I/O before identity checks.
func ObserveFileEffectHashRead(t *testing.T, hook func(string)) {
	t.Helper()
	previous := fileEffectAfterHashRead
	fileEffectAfterHashRead = hook
	t.Cleanup(func() { fileEffectAfterHashRead = previous })
}

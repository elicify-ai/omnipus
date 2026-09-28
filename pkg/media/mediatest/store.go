// Package mediatest provides lifecycle-safe media stores for tests outside
// package media.
package mediatest

import "github.com/elicify-ai/omnipus/pkg/media"

// CleanupRegistrar is the subset of testing.TB needed by NewFileMediaStore.
type CleanupRegistrar interface {
	Helper()
	Cleanup(fn func())
}

// NewFileMediaStore returns a store whose debounced registry writer is stopped
// and flushed before the calling test or benchmark releases its resources.
func NewFileMediaStore(tb CleanupRegistrar) *media.FileMediaStore {
	tb.Helper()
	store := media.NewFileMediaStore()
	tb.Cleanup(store.Stop)
	return store
}

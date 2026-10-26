//go:build windows

package session

// New generation files and checkpoints use WriteFileAtomicSyncDir's checked
// write-through replacement on Windows. An unprivileged process cannot flush
// a writable directory handle; this is not a power-loss atomicity guarantee.
func historySyncDirectory(string) error { return nil }

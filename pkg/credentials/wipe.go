// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

// wipe overwrites every byte of b with zero.
//
// # What this is, and what it is not
//
// This is a best-effort overwrite of one known backing array. It is NOT a
// guarantee that no readable copy of the key remains in the process, and it
// must not be described or relied on as one. Go gives a program no way to
// reach every copy of a byte slice: the collector may have moved the array,
// append may have copied it, a closure may hold a header to it, and any copy
// handed to another package (a derived subkey, a resolved credential value, an
// environment variable) is outside this function's reach entirely.
//
// What it does buy: the copy this package owns — the array behind Store.key,
// and the transient derivation buffers alongside it — stops being readable the
// moment its owner is done with it, instead of living in the heap until a
// collection that may never come and staying readable in a core dump, a swap
// file, or under a debugger attached later.
//
// # Why a plain loop is not optimised away
//
// The compiler does not eliminate stores through a slice header it cannot prove
// dead, and this loop's target is a slice the caller still holds, so the writes
// survive the current toolchain. There is no stronger primitive available to
// pure Go; a compiler that did prove the array dead and drop the loop would
// still be overwriting memory nothing can read, which is the same outcome.
// Deliberately no unsafe or runtime-internal tricks here — they would be
// claiming a guarantee the language does not make.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

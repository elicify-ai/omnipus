//go:build !mipsle && !netbsd && !(freebsd && arm) && (goolm || cgo)

package gateway

import (
	// Matrix currently pulls in mautrix crypto and modernc sqlite transitively.
	//
	// GATE HISTORY: this file was `cgo`-gated from the pre-goolm (libolm) era.
	// The goolm migration made matrix pure-Go, but this gate was never updated —
	// so matrix silently vanished from every CGO_ENABLED=0 release binary (the
	// canonical goolm,stdjson build) while the SPA kept offering it: enabling it
	// hit "factory not registered". The gate is now `goolm || cgo`: goolm builds
	// (the default) link matrix pure-Go. A cgo-only build (no `goolm` tag) still
	// imports this file, but there is no cgo/libolm crypto implementation to
	// link against anymore — that path was removed in the goolm migration and
	// no longer exists. Such a build degrades to init_stub.go's always-erroring
	// factory (a non-fatal, unavailable-channel stub), not a working libolm path.
	//
	// We exclude it on:
	// - linux/mipsle: mautrix crypto falls back to libolm when the `goolm` build
	//   tag is unavailable, and modernc.org/sqlite/modernc.org/libc also lacks a
	//   working build path for our mipsle + softfloat target.
	// - netbsd/*: modernc.org/sqlite v1.46.1 fails to compile due to broken
	//   generated mutex code on NetBSD (for example sqlite_netbsd_amd64.go calls
	//   mu.enter/mu.leave, but the generated mutex type does not define them).
	// - freebsd/arm: modernc.org/libc v1.67.6 fails to compile due to broken
	//   generated 32-bit FreeBSD code (size_t/uint64 and int32/int64 mismatches
	//   in libc_freebsd.go).
	//
	// This means Matrix is currently unavailable on those targets. The proper
	// long-term fix is to split Matrix basic support from its E2EE/sqlite-backed
	// crypto path, or to upgrade/replace the upstream sqlite dependency once the
	// affected targets are supported.
	_ "github.com/dapicom-ai/omnipus/pkg/channels/matrix"
)

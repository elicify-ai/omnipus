// Omnipus — ADR-068 D16.2a / spec FR-020h: the no-SQLite half of the platform gate.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build records_no_sqlite || mipsle || netbsd || (freebsd && arm)

package propindex

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// modernc.org/sqlite cannot build on linux/mipsle, netbsd/* or freebsd/arm, and
// the repository already documents each (pkg/gateway/channel_matrix.go:20-28).
// The build constraint above is the EXACT complement of the SQLite half's, and
// of pkg/records/propindex_stub_available.go's — including `records_no_sqlite`,
// the forcing tag that makes the unavailable path testable on a developer
// machine without cross-compiling.
//
// The constraint deliberately does NOT contain `lite`. `-tags lite` KEEPS
// records (ADR-068 D16.2a, consequence 1): the lite tag drops whatsmeow, Matrix
// is not lite-gated, and `make build-lite` still links SQLite. A reasonable
// reader assumes the opposite, which is why the omission is stated rather than
// left to be noticed.
//
// The refusal is the whole point. On a build without SQLite the record layer
// REFUSES BY NAME (FR-020h): it never returns an empty index, because an empty
// answer that looks complete is D13's headline failure and is precisely the
// silence this ADR exists to remove. The refusal string and its WARN belong to
// pkg/records.RequirePropertyIndex and are pinned by its own test — this file
// returns that error UNCHANGED and adds nothing to it.
// ---------------------------------------------------------------------------

// Open refuses, naming the platform.
func Open(_ context.Context, _ string, _ Options) (Store, error) {
	return nil, records.RequirePropertyIndex(records.CapabilityOpenIndex)
}

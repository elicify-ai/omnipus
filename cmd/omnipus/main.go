// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// stdjson-only gate (NOT goolm): this file needs nothing from goolm — the tag
// only matters for pkg/channels/matrix, which degrades gracefully when absent.
// Gating on stdjson alone keeps the fail-fast against tag-less builds while
// letting the mipsle targets (which must strip goolm: its transitive deps
// don't build on mips softfloat) link. A goolm gate here broke build-lite's
// mipsle line and build-linux-mipsle with "function main is undeclared".
//go:build stdjson

// This file is a thin caller of pkg/app (WP1.5a public entry-point seam,
// docs/plans/engine-divergence-workplan.md, ADR-0010). The CLI/gateway
// assembly — command tree, flags, help text, auto-start orchestration — moved
// to pkg/app so a second main package (editions/cmd/omnipus) can build the
// same binary plus edition-specific parts without importing anything under
// cmd/omnipus/internal (which Go's internal-package rule keeps private to
// this directory). Behaviour is unchanged: same commands, same flags, same
// help text, same exit codes.
package main

import "github.com/elicify-ai/omnipus/pkg/app"

func main() {
	app.Main()
}

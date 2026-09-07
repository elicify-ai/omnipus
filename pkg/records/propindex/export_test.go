// Omnipus — export shim breaking the propindex/knowledge test import cycle.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !windows && !(freebsd && arm)

package propindex

// pkg/knowledge/author.go imports this package, so a test file that itself
// imports pkg/knowledge (memory_both_test.go, measuring both indexes together
// per ADR-068 D16.4 item 4) cannot live in `package propindex` — the compiler
// rejects it as an import cycle. Moving that test to the external
// `propindex_test` package breaks the cycle, but it still needs the fixtures
// and RSS-measurement helpers the internal (white-box) tests in fixture_test.go
// and memory_test.go define unexported, for their own sibling tests.
//
// These are test-only forwarders, compiled solely into the test binary — no
// production symbol is exported to satisfy this. They exist so
// memory_both_test.go can reach plantSchema/note/mustWriteFile (fixture_test.go)
// and peakRSSBytes/mib/budgetBytes/childWork (memory_test.go) without those
// helpers needing to be duplicated or promoted into production code.
var (
	ExportedPlantSchema   = plantSchema
	ExportedNote          = note
	ExportedMustWriteFile = mustWriteFile
	ExportedPeakRSSBytes  = peakRSSBytes
	ExportedMib           = mib
	ExportedChildWork     = childWork
)

// ExportedBudgetBytes forwards budgetBytes (memory_test.go) for the same reason.
const ExportedBudgetBytes = budgetBytes

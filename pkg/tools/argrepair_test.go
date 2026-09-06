// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRepairLeakedToolArgs' ./pkg/tools/

package tools

import (
	"reflect"
	"testing"
)

func TestRepairLeakedToolArgs_RecoversCapturedGLMLeak(t *testing.T) {
	// Verbatim from the A1 live transcript.
	args := map[string]any{
		"collection": "kb",
		"op":         "create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note",
		"view":       "jim-tooltest--recent",
		"kind":       "table",
	}

	changed := repairLeakedToolArgs(args)
	if !changed {
		t.Fatal("expected repair to fire on leaked template tokens")
	}
	if got := args["op"]; got != "create_view" {
		t.Fatalf("op not recovered: got %q, want %q", got, "create_view")
	}
	if got := args["type"]; got != "note" {
		t.Fatalf("type not recovered from leaked op value: got %q, want %q", got, "note")
	}
	// Untouched fields survive verbatim.
	if args["view"] != "jim-tooltest--recent" || args["kind"] != "table" || args["collection"] != "kb" {
		t.Fatalf("clean fields corrupted: %#v", args)
	}
}

func TestRepairLeakedToolArgs_NoOpOnCleanArgs(t *testing.T) {
	args := map[string]any{
		"op":   "create_view",
		"type": "note",
		"view": "v1",
		"tags": []any{"a", "b"},
	}
	before := map[string]any{"op": "create_view", "type": "note", "view": "v1"}

	if repairLeakedToolArgs(args) {
		t.Fatal("repair must not fire on clean, well-formed arguments")
	}
	for k, v := range before {
		if args[k] != v {
			t.Fatalf("clean arg %q changed: got %v want %v", k, args[k], v)
		}
	}
}

func TestRepairLeakedToolArgs_HexAloneDoesNotTrigger(t *testing.T) {
	// A value that merely contains an 8-hex-looking token but NONE of the
	// structural tags must be left untouched — repair requires BOTH a
	// structural tag and a hex sentinel, so a hex-only value (like a legit
	// tool arg mentioning a commit hash) is never mangled.
	args := map[string]any{
		"op":    "write_view",
		"value": "commit <deadbeef> landed",
	}
	if repairLeakedToolArgs(args) {
		t.Fatal("hex-only value must not trigger repair")
	}
	if args["value"] != "commit <deadbeef> landed" {
		t.Fatalf("hex-only value was altered: %q", args["value"])
	}
}

func TestRepairLeakedToolArgs_DoesNotClobberRealArg(t *testing.T) {
	// If the model ALSO sent a real `type`, the correctly-emitted one wins
	// over anything unpacked from a leaked blob.
	args := map[string]any{
		"op":   "create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note",
		"type": "invoice",
	}
	repairLeakedToolArgs(args)
	if args["op"] != "create_view" {
		t.Fatalf("op not recovered: %q", args["op"])
	}
	if args["type"] != "invoice" {
		t.Fatalf("real type must not be clobbered by recovered value: got %q want %q", args["type"], "invoice")
	}
}

func TestRepairLeakedToolArgs_LegitimateTagMentionNotMangled(t *testing.T) {
	// A1 regression: a legitimate argument that merely MENTIONS the literal
	// structural tag (e.g. a bash command echoing the string, or note/body
	// text discussing it) must NOT be silently truncated. Before the fix the
	// tag alone triggered repair, so this command was rewritten to
	// "echo 'the model leaked" and then executed truncated — a silent
	// corruption of what actually ran.
	cmd := "echo 'the model leaked </arg_value> into the value' > notes.txt"
	args := map[string]any{"command": cmd}

	if repairLeakedToolArgs(args) {
		t.Fatalf("repair must not fire on a legitimate tag mention: value became %q", args["command"])
	}
	if args["command"] != cmd {
		t.Fatalf("legitimate command was mangled: got %q want %q", args["command"], cmd)
	}
}

func TestSplitLeakedValue_LeadingValueAndPairs(t *testing.T) {
	clean, pairs := splitLeakedValue(
		"create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note")
	if clean != "create_view" {
		t.Fatalf("clean value: got %q want %q", clean, "create_view")
	}
	want := map[string]string{"type": "note"}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("pairs: got %#v want %#v", pairs, want)
	}
}

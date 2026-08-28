// Omnipus — tests for the list-valued splice (FR-040a) and the multi-line
// scalar guard (FR-040b).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestAddListValue_BlockStyle_TouchesOnlyOneLine(t *testing.T) {
	src := []byte("---\n" +
		"title: Fern\n" +
		"tags:\n" +
		"  - alpha\n" +
		"  - beta\n" +
		"---\n" +
		"Body.\n")
	out, err := AddListValue("tags", "gamma")(src)
	if err != nil {
		t.Fatalf("AddListValue: %v", err)
	}
	want := "---\n" +
		"title: Fern\n" +
		"tags:\n" +
		"  - alpha\n" +
		"  - beta\n" +
		"  - gamma\n" +
		"---\n" +
		"Body.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	// Every byte of the untouched region (title, existing tags, body) must
	// be a literal prefix-and-suffix of the output, which is exactly what a
	// single-line insertion guarantees and a re-serialisation would not.
	if !bytes.Contains(out, []byte("title: Fern\ntags:\n  - alpha\n  - beta\n")) {
		t.Fatalf("existing lines were not preserved verbatim: %s", out)
	}
}

func TestAddListValue_AlreadyPresent_IsByteIdenticalNoOp(t *testing.T) {
	src := []byte("---\ntags:\n  - alpha\n  - beta\n---\nBody.\n")
	out, err := AddListValue("tags", "beta")(src)
	if err != nil {
		t.Fatalf("AddListValue: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("adding an already-present value must be a byte-identical no-op:\ngot:  %q\nwant: %q", out, src)
	}
}

func TestRemoveListValue_BlockStyle_TouchesOnlyOneLine(t *testing.T) {
	src := []byte("---\n" +
		"title: Fern\n" +
		"tags:\n" +
		"  - alpha\n" +
		"  - beta\n" +
		"  - gamma\n" +
		"---\n" +
		"Body.\n")
	out, err := RemoveListValue("tags", "beta")(src)
	if err != nil {
		t.Fatalf("RemoveListValue: %v", err)
	}
	want := "---\n" +
		"title: Fern\n" +
		"tags:\n" +
		"  - alpha\n" +
		"  - gamma\n" +
		"---\n" +
		"Body.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestRemoveListValue_NotPresent_IsByteIdenticalNoOp(t *testing.T) {
	src := []byte("---\ntags:\n  - alpha\n  - beta\n---\nBody.\n")
	out, err := RemoveListValue("tags", "nonexistent")(src)
	if err != nil {
		t.Fatalf("RemoveListValue: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("removing an absent value must be a byte-identical no-op:\ngot:  %q\nwant: %q", out, src)
	}
}

func TestRemoveListValue_AbsentProperty_IsByteIdenticalNoOp(t *testing.T) {
	src := []byte("---\ntitle: Fern\n---\nBody.\n")
	out, err := RemoveListValue("tags", "alpha")(src)
	if err != nil {
		t.Fatalf("RemoveListValue: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("removing from an absent property must be a byte-identical no-op:\ngot:  %q\nwant: %q", out, src)
	}
}

func TestRemoveListValue_LastElement_LeavesEmptyListNotAbsentKey(t *testing.T) {
	src := []byte("---\ntags:\n  - alpha\n---\nBody.\n")
	out, err := RemoveListValue("tags", "alpha")(src)
	if err != nil {
		t.Fatalf("RemoveListValue: %v", err)
	}
	want := "---\ntags: []\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestAddListValue_AbsentProperty_CreatesBlockList(t *testing.T) {
	src := []byte("---\ntitle: Fern\n---\nBody.\n")
	out, err := AddListValue("tags", "alpha")(src)
	if err != nil {
		t.Fatalf("AddListValue: %v", err)
	}
	want := "---\ntitle: Fern\ntags:\n  - alpha\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestListSplice_FlowStyle_PreservesStyle(t *testing.T) {
	src := []byte("---\ntags: [alpha, beta]\n---\nBody.\n")
	out, err := AddListValue("tags", "gamma")(src)
	if err != nil {
		t.Fatalf("AddListValue: %v", err)
	}
	want := "---\ntags: [alpha, beta, gamma]\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}

	out2, err := RemoveListValue("tags", "alpha")(out)
	if err != nil {
		t.Fatalf("RemoveListValue: %v", err)
	}
	want2 := "---\ntags: [beta, gamma]\n---\nBody.\n"
	if string(out2) != want2 {
		t.Fatalf("got:\n%s\nwant:\n%s", out2, want2)
	}
}

func TestAddListValue_AgainstScalar_Refuses(t *testing.T) {
	src := []byte("---\nstatus: active\n---\nBody.\n")
	out, err := AddListValue("status", "x")(src)
	if err == nil {
		t.Fatalf("expected a refusal, got output: %s", out)
	}
	if !errors.Is(err, ErrListShapeUnsupported) {
		t.Fatalf("expected ErrListShapeUnsupported, got: %v", err)
	}
	if !strings.Contains(err.Error(), "single value") {
		t.Fatalf("refusal should name the reason: %v", err)
	}
}

func TestSetPropertyScalarChecked_RefusesMultiLineClobber(t *testing.T) {
	// "tags:" (the header line) plus two item lines: three physical lines
	// belong to this key's span, matching the spec's own worked example
	// ("segment currently spans 3 lines").
	src := []byte("---\ntags:\n  - alpha\n  - beta\n---\nBody.\n")
	out, err := SetPropertyScalarChecked("tags", "solo")(src)
	if err == nil {
		t.Fatalf("expected a refusal, got output: %s", out)
	}
	if !errors.Is(err, ErrMultiLineValue) {
		t.Fatalf("expected ErrMultiLineValue, got: %v", err)
	}
	if !strings.Contains(err.Error(), "3 lines") {
		t.Fatalf("refusal should name the line count (3): %v", err)
	}
	if !strings.Contains(err.Error(), "list value instead") {
		t.Fatalf("refusal should name the remedy: %v", err)
	}
}

func TestSetPropertyScalarChecked_AllowsSingleLineOverwrite(t *testing.T) {
	src := []byte("---\nstatus: draft\n---\nBody.\n")
	out, err := SetPropertyScalarChecked("status", "active")(src)
	if err != nil {
		t.Fatalf("SetPropertyScalarChecked: %v", err)
	}
	want := "---\nstatus: active\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSetPropertyList_ReplacesWholeListPreservingStyle(t *testing.T) {
	src := []byte("---\ntags:\n  - alpha\n  - beta\n---\nBody.\n")
	out, err := SetPropertyList("tags", []string{"x", "y", "z"})(src)
	if err != nil {
		t.Fatalf("SetPropertyList: %v", err)
	}
	want := "---\ntags:\n  - x\n  - y\n  - z\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSetPropertyList_EmptyValueIsExplicitEmptyList(t *testing.T) {
	src := []byte("---\ntitle: Fern\n---\nBody.\n")
	out, err := SetPropertyList("tags", nil)(src)
	if err != nil {
		t.Fatalf("SetPropertyList: %v", err)
	}
	want := "---\ntitle: Fern\ntags: []\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestListSplice_UnsupportedNestedShape_Refuses(t *testing.T) {
	src := []byte("---\nitems:\n  - key: value\n    other: thing\n---\nBody.\n")
	if _, err := AddListValue("items", "x")(src); !errors.Is(err, ErrListShapeUnsupported) {
		t.Fatalf("expected ErrListShapeUnsupported for a nested mapping, got: %v", err)
	}
}

// TestListSplice_MappingEntry_RefusesEvenAtConsistentIndent isolates the
// mapping-shape guard from the indent-consistency guard: every continuation
// line here shares one indent, so only a check that actually inspects each
// item's own text — not one that merely compares indents across lines — can
// catch it. Mutation testing found that an earlier fixture let the indent
// check catch a mapping-shaped case by coincidence, masking that no check
// for the shape itself existed yet.
func TestListSplice_MappingEntry_RefusesEvenAtConsistentIndent(t *testing.T) {
	src := []byte("---\ncontacts:\n  - name: Alice\n  - name: Bob\n---\nBody.\n")
	if _, err := AddListValue("contacts", "x")(src); !errors.Is(err, ErrListShapeUnsupported) {
		t.Fatalf("expected ErrListShapeUnsupported for unquoted mapping items at one consistent indent, got: %v", err)
	}
	if _, err := RemoveListValue("contacts", "name: Alice")(src); !errors.Is(err, ErrListShapeUnsupported) {
		t.Fatalf("expected ErrListShapeUnsupported for unquoted mapping items at one consistent indent, got: %v", err)
	}
}

// TestListSplice_QuotedColonValue_IsNotMistakenForAMapping proves the guard
// above is scoped to UNQUOTED items: a quoted scalar containing ": " is
// ordinary text, not a mapping, and must still splice normally.
func TestListSplice_QuotedColonValue_IsNotMistakenForAMapping(t *testing.T) {
	src := []byte("---\nnotes:\n  - \"ratio: 2:1\"\n---\nBody.\n")
	out, err := AddListValue("notes", "second")(src)
	if err != nil {
		t.Fatalf("a quoted colon-bearing value must not be refused as a mapping: %v", err)
	}
	want := "---\nnotes:\n  - \"ratio: 2:1\"\n  - second\n---\nBody.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestListSplice_CRLF_PreservesLineEndings(t *testing.T) {
	src := []byte("---\r\ntags:\r\n  - alpha\r\n---\r\nBody.\r\n")
	out, err := AddListValue("tags", "beta")(src)
	if err != nil {
		t.Fatalf("AddListValue: %v", err)
	}
	want := "---\r\ntags:\r\n  - alpha\r\n  - beta\r\n---\r\nBody.\r\n"
	if string(out) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

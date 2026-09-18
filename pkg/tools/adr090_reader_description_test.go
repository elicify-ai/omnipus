package tools

import (
	"strings"
	"testing"
)

// ADR-090 §6.6: library_read wraps the same reader as read_file, including
// PNG/JPEG inspection. These tests pin the model-facing contract, derived
// from that spec and from readOpenFile / inspectionImageResult /
// extractDocument — not from whatever the Description currently happens to say.

func paramDesc(t *testing.T, params map[string]any, name string) string {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters.properties missing")
	}
	field, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("parameter %q missing", name)
	}
	desc, ok := field["description"].(string)
	if !ok || desc == "" {
		t.Fatalf("parameter %q has no description", name)
	}
	return desc
}

func TestLibraryRead_DescriptionMentionsImageInspection(t *testing.T) {
	desc := NewLibraryReadTool("", false, 0).Description()
	for _, want := range []string{
		"library",
		"uploaded",
		"PNG",
		"JPEG",
		"offset",
		"length",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("library_read Description missing %q:\n%s", want, desc)
		}
	}
	// Chat-upload guidance must remain (library-spec D3).
	if !strings.Contains(desc, "library_list") {
		t.Errorf("library_read Description dropped upload-path guidance (library_list):\n%s", desc)
	}
}

func TestReadFileAndLibraryRead_ImageInspectionSameContract(t *testing.T) {
	readFile := (&ReadFileTool{}).Description()
	library := NewLibraryReadTool("", false, 0).Description()
	for _, want := range []string{
		"PNG",
		"JPEG",
		"visual",
		"offset",
		"length",
	} {
		if !strings.Contains(readFile, want) {
			t.Errorf("read_file Description missing %q:\n%s", want, readFile)
		}
		if !strings.Contains(library, want) {
			t.Errorf("library_read Description missing %q:\n%s", want, library)
		}
	}
	// Either "attach"/"user" phrasing is fine; the false claim would be that
	// inspection delivers the file — asserted negatively below.
	if strings.Contains(library, "sends the file to the user") || strings.Contains(library, "attaches the image to the user") {
		t.Errorf("library_read Description must not claim user delivery:\n%s", library)
	}
}

func TestReadFile_OffsetLengthParamsDistinguishUnits(t *testing.T) {
	params := (&ReadFileTool{}).Parameters()
	offset := paramDesc(t, params, "offset")
	length := paramDesc(t, params, "length")
	for _, desc := range []string{offset, length} {
		// The retired one-size-fits-all wording. A model that paginates a PNG
		// by "bytes" then treats the rejection as "cannot inspect images".
		if desc == "Byte offset to start reading from." || desc == "Maximum number of bytes to read." {
			t.Errorf("parameter still claims a single byte unit: %q", desc)
		}
		if !strings.Contains(strings.ToLower(desc), "image") {
			t.Errorf("parameter must say images reject pagination: %q", desc)
		}
		if !strings.Contains(strings.ToLower(desc), "character") {
			t.Errorf("parameter must say extracted documents paginate by character: %q", desc)
		}
		if !strings.Contains(strings.ToLower(desc), "byte") {
			t.Errorf("parameter must say plain text paginates by byte: %q", desc)
		}
	}
}

func TestLibraryRead_OffsetLengthParamsMatchReadFileUnits(t *testing.T) {
	readFile := (&ReadFileTool{}).Parameters()
	library := NewLibraryReadTool("", false, 0).Parameters()
	for _, name := range []string{"offset", "length"} {
		got := paramDesc(t, library, name)
		want := paramDesc(t, readFile, name)
		if !strings.Contains(got, "image") || !strings.Contains(got, "character") || !strings.Contains(got, "byte") {
			t.Errorf("library_read %s description does not carry the same unit contract: %q", name, got)
		}
		// Both wrappers share the inner reader; the unit sentences must agree.
		if !strings.Contains(want, "image") {
			t.Errorf("read_file %s lost the image-reject sentence: %q", name, want)
		}
	}
}

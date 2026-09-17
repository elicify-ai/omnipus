package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise both public readers with the same independently encoded JPEG.
// Limits surround the fixture size; expected acceptance comes from ADR-090 M.
func TestReadImage_ReaderJPEGAndByteBoundaryMatrix(t *testing.T) {
	fixture := image.NewRGBA(image.Rect(0, 0, 19, 13))
	fixture.Set(0, 0, color.RGBA{R: 200, G: 70, B: 20, A: 255})
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, fixture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	for _, library := range []bool{false, true} {
		name := "read_file"
		if library {
			name = "library_read"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := root
			if library {
				dir = filepath.Join(root, ".library")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), encoded.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name  string
				limit int
				valid bool
			}{
				{"size_M_minus_1", encoded.Len() + 1, true},
				{"size_M", encoded.Len(), true},
				{"size_M_plus_1", encoded.Len() - 1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var reader interface {
						Execute(context.Context, map[string]any) *ToolResult
						SetMaxInspectionImageBytes(int)
					}
					if library {
						reader = NewLibraryReadTool(root, true, MaxReadFileSize)
					} else {
						reader = NewReadFileTool(root, true, MaxReadFileSize)
					}
					reader.SetMaxInspectionImageBytes(tc.limit)
					result := reader.Execute(context.Background(), map[string]any{"path": "photo.jpg"})
					if !tc.valid {
						if !result.IsError || !strings.Contains(result.ForLLM, "image exceeds byte limit") || len(result.InspectionImages) != 0 {
							t.Fatalf("oversized JPEG result: %#v", result)
						}
						return
					}
					if result.IsError || len(result.InspectionImages) != 1 {
						t.Fatalf("accepted JPEG result: %#v", result)
					}
					got := result.InspectionImages[0]
					if got.MIMEType != "image/jpeg" || !bytes.Equal(got.Bytes, encoded.Bytes()) {
						t.Fatal("JPEG MIME or authorized bytes changed")
					}
					if len(result.Media) != 0 || result.ForUser != "" || !strings.Contains(result.ForLLM, "original: 19x13") {
						t.Fatalf("JPEG inspection contract: %#v", result)
					}
					for _, field := range []string{"offset", "length"} {
						for _, pagination := range []struct {
							value   any
							message string
						}{
							{nil, "unsupported type <nil> for " + field + " parameter"},
							{"invalid", "invalid integer format for " + field + " parameter"},
							{true, "unsupported type bool for " + field + " parameter"},
							{float64(0), "image pagination is not supported"},
							{float64(-1), "image pagination is not supported"},
						} {
							paginated := reader.Execute(context.Background(), map[string]any{"path": "photo.jpg", field: pagination.value})
							if !paginated.IsError || !strings.Contains(paginated.ForLLM, pagination.message) || len(paginated.InspectionImages) != 0 {
								t.Fatalf("%s=%v wrong refusal: %#v", field, pagination.value, paginated)
							}
						}
					}
				})
			}
		})
	}
}

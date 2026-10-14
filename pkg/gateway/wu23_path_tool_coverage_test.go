package gateway

import (
	"strings"
	"testing"
)

// D8 T-FT: discover path-taking tools from the actual boot catalog, never a
// hand-maintained list of what to run. A new surface without a real protected-
// path adapter is a loud testability blocker, not a silent coverage omission.
func TestWU23_RegisteredPathToolsHaveProtectionProbes(t *testing.T) {
	reg, _ := buildCentralBuiltinRegistry(nil)
	coverage := map[string]string{
		"read_file":             "TestWU23_FileToolsProtectInternalInventory",
		"write_file":            "TestWU23_FileToolsProtectInternalInventory",
		"edit_file":             "TestWU23_FileToolsProtectInternalInventory",
		"append_file":           "TestWU23_FileToolsProtectInternalInventory",
		"list_directory":        "TestWU23_FileToolsProtectInternalInventory",
		"grep":                  "TestWU23_GrepNoInternalContentOrNameLeaks",
		"library_read":          "TestWU23_AdditionalHostPathTools",
		"library_list":          "TestWU23_AdditionalHostPathTools",
		"request_mount":         "TestWU23_AdditionalHostPathTools",
		"send_file":             "TestWU23_AdditionalHostPathTools",
		"serve_web":             "TestWU23_AdditionalHostPathTools",
		"browser_upload_file":   "TestWU23_BrowserUploadProtectsInternalFiles",
		"knowledge_read":        "TestWU23_DedicatedKnowledgeStoreCannotEscape",
		"knowledge_edit":        "TestWU23_DedicatedKnowledgeStoreCannotEscape",
		"knowledge_restructure": "TestWU23_DedicatedKnowledgeStoreCannotEscape",
	}
	count := 0
	for _, tool := range reg.All() {
		paths := wu23PathParameters(tool.Parameters(), "")
		if len(paths) == 0 {
			continue
		}
		count++
		t.Run(tool.Name(), func(t *testing.T) {
			if coverage[tool.Name()] == "" {
				t.Fatalf("BLOCKED: registered path-taking tool %s (%s) has no real protected-path/positive-control adapter — required by amendment §2 D6/T-FT", tool.Name(), strings.Join(paths, ", "))
			}
		})
	}
	if count == 0 {
		t.Fatal("instrument failed: no path-taking tool found in boot registry")
	}
	t.Logf("mechanically discovered %d path-taking tools", count)
}

func wu23PathParameters(schema map[string]any, prefix string) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for name, raw := range props {
		child, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := prefix + name
		if name == "path" || name == "directory" || name == "file_path" || name == "host_path" || name == "output_path" || name == "screenshot_path" {
			out = append(out, key)
		}
		out = append(out, wu23PathParameters(child, key+".")...)
		if items, ok := child["items"].(map[string]any); ok {
			out = append(out, wu23PathParameters(items, key+"[].")...)
		}
	}
	return out
}

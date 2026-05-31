package tools

// Test-only exports for the metadata guard. These let the external
// (package tools_test) regression suite exercise package-private guard
// internals — specifically the fail-closed branches of guardMetadataPath —
// without weakening their visibility in production code.

// GuardMetadataPathForTest exposes guardMetadataPath for tests.
func GuardMetadataPathForTest(workspace, path, op string) *ToolResult {
	return guardMetadataPath(workspace, path, op)
}

// ResolveAbsPathForTest exposes resolveAbsPath for tests.
func ResolveAbsPathForTest(rawPath, workspace string) (string, error) {
	return resolveAbsPath(rawPath, workspace)
}

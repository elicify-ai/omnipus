package tools

// WasInvoked reports entry into the actual Execute/ExecuteAsync implementation,
// not argument validation, registry lookup, a timing guess or stored status.
// A caller must still reject Deferred results when deciding whether work ran.
// This fact is private to the live result and is never serialized.
func (tr *ToolResult) WasInvoked() bool {
	return tr != nil && tr.invoked
}

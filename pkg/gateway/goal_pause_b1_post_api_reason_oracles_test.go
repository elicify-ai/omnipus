package gateway

// REQUIRED POST-API ORACLE DATA ONLY; NOT an active pre-change test root.
// Same author later binds these exact frozen records to the real private formatter.
// Requires the shared b1DraftReasonCase type; no production API is invented here.
func b1PendingReasonCases() []b1DraftReasonCase {
	return []b1DraftReasonCase{
		{"Empty", "", ""},
		{"ShortTrailingWhitespaceUnchanged", "a  ", "a  "},
	}
}

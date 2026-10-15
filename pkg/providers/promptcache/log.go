package promptcache

import "github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"

// CallAccounting is the input to the one info line written for a completed
// model call. The line carries these correlation ids and the numeric counters
// only. It does not carry prompt text, tool arguments, credentials, or any
// string taken from the provider usage payload.
type CallAccounting struct {
	ProviderID   string
	ModelID      string
	AgentID      string
	SessionID    string
	CallID       string
	Usage        protocoltypes.UsageInfo
	Cost         float64
	BillingClass string
	OneHourRule  string
}

// FormatCallAccounting builds the info accounting line. The skeleton returns
// an empty string, which is not a line.
func FormatCallAccounting(in CallAccounting) string {
	_ = in
	return ""
}

package promptcache

import "github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"

// NormalizeUsage reads one provider usage JSON object and returns uncached
// prompt, cache read, and cache write, including the 5-minute and 1-hour
// split when the provider reported it. providerID selects a host whose
// documented fields must not be summed twice (DeepSeek) or must not be
// subtracted (Gemini). The skeleton returns a zero UsageInfo and a nil error.
func NormalizeUsage(providerID string, raw []byte) (protocoltypes.UsageInfo, error) {
	_, _ = providerID, raw
	return protocoltypes.UsageInfo{}, nil
}

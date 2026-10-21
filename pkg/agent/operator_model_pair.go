package agent

import "strings"

// operatorModelPair uses attempt metadata, never provider prose. The same
// credential scrubber used by ordinary provider messages applies to labels.
func operatorModelPair(provider, model string) string {
	provider = strings.TrimSpace(scrubDetailText(provider))
	model = strings.TrimSpace(scrubDetailText(model))
	subject := "the configured model"
	if model != "" {
		subject = "model " + model
	}
	if provider != "" {
		subject += " on provider " + provider
	}
	return subject
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import "testing"

func TestOAuthEntryName(t *testing.T) {
	cases := map[string]string{
		"openai":         "openai_OAUTH",
		"xai":            "xai_OAUTH",
		"OpenAI":         "openai_OAUTH",
		"openai-chatgpt": "openai-chatgpt_OAUTH",
	}
	for in, want := range cases {
		if got := OAuthEntryName(in); got != want {
			t.Errorf("OAuthEntryName(%q) = %q, want %q", in, got, want)
		}
	}
}

package anthropicprovider

import "testing"

// Characterization control for dispatch #4: changing numeric coercion must not
// alter the adapter's pre-existing 4096 default when the option is absent.
// This pins unchanged behavior, not a newly specified provider/model limit.
func TestCachingFixAbsentMaxTokensKeepsDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options map[string]any
	}{
		{"nil_options", nil},
		{"empty_options", map[string]any{}},
		{"unrelated_option", map[string]any{"temperature": 0.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "fixture", tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if params.MaxTokens != 4096 {
				t.Fatalf("absent max_tokens default=%d, want unchanged4096", params.MaxTokens)
			}
		})
	}
}

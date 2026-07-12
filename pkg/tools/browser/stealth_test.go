package browser

import "testing"

// TestDeHeadlessUA verifies the User-Agent de-Headless rewrite used by
// applyStealth removes the biggest automation giveaway (the "HeadlessChrome"
// / "Headless" token) while leaving a normal UA untouched.
func TestDeHeadlessUA(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "chrome-headless-shell UA",
			in:   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/125.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		},
		{
			name: "already-clean Chrome UA is unchanged",
			in:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deHeadlessUA(tc.in); got != tc.want {
				t.Fatalf("deHeadlessUA(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := deHeadlessUA(tc.in); containsHeadless(got) {
				t.Errorf("deHeadlessUA output still contains a Headless token: %q", got)
			}
		})
	}
}

func containsHeadless(s string) bool {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "Headless" {
			return true
		}
	}
	return false
}

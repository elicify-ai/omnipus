package browser

func containsHeadless(s string) bool {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "Headless" {
			return true
		}
	}
	return false
}

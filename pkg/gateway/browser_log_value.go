package gateway

import "strconv"

// browserLogValue preserves readable diagnostics while preventing browser-origin
// values from creating console lines or terminal controls. Escaping quotes and
// backslashes also prevents the console formatter from unquoting them again.
func browserLogValue(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return quoted[1 : len(quoted)-1]
}

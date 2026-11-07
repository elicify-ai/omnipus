package skills

import "strings"

// logSafe escapes line breaks in a value that reaches a log record, so a
// name or error text that carries one cannot forge a new log line.
func logSafe(value string) string {
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, "\r", `\r`)
}

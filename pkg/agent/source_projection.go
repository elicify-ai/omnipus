package agent

import (
	"fmt"
	"unicode/utf8"
)

// projectSource keeps literal source runes around a complete addressed mark.
// The mark is not source and is never shortened or used for a later cut.
func projectSource(archive windowArchive, line, kept int, tool, id string) (string, error) {
	src, ok := archive.message(line)
	if line < 0 || !ok || src.Role != "tool" || src.ToolCallID != id {
		return "", fmt.Errorf("context checkpoint: tool result %q has no archive identity", id)
	}
	full := src.Content
	n := utf8.RuneCountInString(full)
	if kept < 0 || kept > n {
		return "", fmt.Errorf("context checkpoint: invalid source limit %d/%d", kept, n)
	}
	if kept == n {
		return full, nil
	}
	state := "capped"
	if kept == 0 {
		state = "emptied"
	}
	mark, err := buildRecallMark(state, tool, id, line, full, turnNumberForArchiveLine(archive, line))
	if err != nil {
		return "", err
	}
	if kept == 0 {
		return mark, nil
	}
	r := []rune(full)
	return string(r[:(kept+1)/2]) + "\n" + mark + "\n" + string(r[len(r)-kept/2:]), nil
}

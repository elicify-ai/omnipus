package gateway

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func todoFromWire(text, status string, activeForm *string) task.Todo {
	td := task.Todo{Text: text, Status: task.TodoStatus(status)}
	if activeForm != nil {
		td.ActiveForm = strings.TrimSpace(*activeForm)
	}
	return td
}

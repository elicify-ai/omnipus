package gateway

import (
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func todoFromWire(text, status string, activeForm *string) task.Todo {
	td := task.Todo{Text: text, Status: task.TodoStatus(status)}
	if activeForm != nil {
		td.ActiveForm = strings.TrimSpace(*activeForm)
	}
	return td
}

func toWireTodo(td task.Todo) wireTodo {
	out := wireTodo{Text: td.Text, Status: gen.TaskTodosStatus(td.Status)}
	if td.ActiveForm != "" {
		out.ActiveForm = &td.ActiveForm
	}
	return out
}

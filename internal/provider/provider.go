package provider

import "wtp/internal/core"

type TaskFilter struct {
	Status *core.Status
}

type Provider interface {
	ListTasks(filter TaskFilter) ([]core.Task, error)
	GetTask(idOrShortID string) (core.Task, error)
	CreateTask(input core.CreateTaskInput) (core.Task, error)
	UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.Task, error)
	AddComment(idOrShortID, actor, message string) (core.Task, error)
	GetNextTask(agent string) (core.Task, error)
	ExportCanonical(outDir string) error
}

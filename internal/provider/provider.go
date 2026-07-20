package provider

import (
	"errors"

	"github.com/mattrandles/wtproj/internal/core"
)

var ErrNoEligibleTask = errors.New("no eligible task found")

type TaskFilter struct {
	Status *core.Status
	Agent  string
}

type Provider interface {
	ListTasks(filter TaskFilter) ([]core.TaskView, error)
	GetTask(idOrShortID, agent string) (core.TaskView, error)
	CreateTask(input core.CreateTaskInput) (core.TaskView, error)
	UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error)
	UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error)
	AddComment(idOrShortID, actor, message string) (core.TaskView, error)
	PeekNextTask(agent string) (core.TaskView, error)
	PeekNextTasks(agent string, limit int) ([]core.TaskView, error)
	GetNextTask(agent string) (core.TaskView, error)
	ExportCanonical(outDir string) error
}

package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// WorkflowDoneMsg returns terminal ownership to the containing application.
// A workflow never quits its parent Bubble Tea program.
type WorkflowDoneMsg struct {
	Err     error
	Message string
	Changed bool
	Owner   tea.Model
}

type WorkflowRequest struct {
	Action, Host, Source, JobID, Schedule string
}

type WorkflowFactory func(context.Context, WorkflowRequest) (tea.Model, error)

type Embeddable interface{ SetEmbedded(bool) }

func embedded(model tea.Model) tea.Model {
	if m, ok := model.(Embeddable); ok {
		m.SetEmbedded(true)
	}
	return model
}

// RunModel mounts the same workflow used by the dashboard in its own terminal.
func RunModel(ctx context.Context, model tea.Model) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
}

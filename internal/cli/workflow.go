package cli

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

func (o *options) workflow(ctx context.Context, r ui.WorkflowRequest) (tea.Model, error) {
	c, err := config.Load(o.config)
	if err != nil {
		return nil, err
	}
	switch r.Action {
	case "add", "edit":
		if r.Action == "add" {
			r.JobID = ""
		}
		return newJobModel(ctx, service.New(c), r.Host, r.Source, r.Action, r.JobID, r.Schedule)
	case "host-add":
		return ui.NewHostPicker(ctx, c, ui.HostPickerOptions{Source: "ssh"}), nil
	case "source-add":
		return newSourceModel(ctx, c, r.Host)
	case "run":
		return newRunModel(ctx, service.New(c), r.Host, r.Source, r.JobID)
	case "remove", "toggle":
		return newChangeModel(ctx, service.New(c), r)
	case "guide":
		return ui.NewHelpBrowser("", c.Mouse), nil
	}
	return nil, fmt.Errorf("unknown workflow %q", r.Action)
}

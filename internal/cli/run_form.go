package cli

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

func runFormSpec(s *service.Service, snap service.Snapshot, j service.Entry, recipe service.Recipe, forceOverride bool) ui.FormSpec {
	return ui.FormSpec{Title: "Run · " + j.Key(), Theme: s.Config.Theme, Mouse: s.Config.Mouse,
		Fields: []ui.Field{{Key: "runner", Label: "Execute via", Value: recipe.Runner, Options: []string{"direct", "pueue"}}, {Key: "group", Label: "Existing Pueue group (empty=default)", Value: recipe.Group, DisabledWhen: pueueGroupDisabled}},
		Build: func(ctx context.Context, v map[string]string) (ui.Review, error) {
			r := recipe
			r.Runner = v["runner"]
			r.Group = v["group"]
			var override *service.Recipe
			if forceOverride || r.Runner != recipe.Runner || r.Runner == "pueue" && r.Group != recipe.Group {
				if r.Runner != "pueue" {
					r.Group = ""
				}
				override = &r
			}
			p, err := s.RunPlan(ctx, snap, j.ID, override)
			return ui.Review{Text: runPlanText(p), Data: p}, err
		},
		Apply: func(ctx context.Context, _ map[string]string, r ui.Review) (string, error) {
			record, err := s.Run(ctx, r.Data.(service.ExecutionPlan))
			return runRecordText(record), err
		},
		LoadKeys: []string{"runner"}, Load: func(ctx context.Context, v map[string]string) []ui.FieldUpdate {
			if v["runner"] == "pueue" {
				return pueueFields(ctx, s, j.Host)
			}
			return nil
		},
	}
}
func newRunModel(ctx context.Context, s *service.Service, host, source, id string) (tea.Model, error) {
	snap, err := s.Snapshot(ctx, host, source)
	if err != nil {
		return nil, err
	}
	j, err := entry(snap, id)
	if err != nil {
		return nil, err
	}
	recipe, err := service.LoadRecipe(j)
	if err != nil {
		return nil, err
	}
	if recipe.Runner == "" {
		recipe.Runner = "direct"
	}
	return ui.NewForm(ctx, runFormSpec(s, snap, j, recipe, false)), nil
}

func newChangeModel(ctx context.Context, s *service.Service, r ui.WorkflowRequest) (tea.Model, error) {
	snap, err := s.Snapshot(ctx, r.Host, r.Source)
	if err != nil {
		return nil, err
	}
	j, err := snap.Document.Find(r.JobID)
	if err != nil {
		return nil, err
	}
	original := j
	op := r.Action
	var plan service.Plan
	if op == "remove" {
		plan, err = s.Plan(ctx, snap, r.JobID, nil, op)
	} else {
		op = "enable"
		if j.Enabled {
			op = "disable"
		}
		j.Enabled = !j.Enabled
		plan, err = s.Plan(ctx, snap, r.JobID, &j, op)
	}
	if err != nil {
		return nil, err
	}
	return ui.NewReviewForm(ctx, ui.FormSpec{Title: op + " · " + r.Host + "/" + r.Source, Theme: s.Config.Theme, Mouse: s.Config.Mouse,
		Build: func(context.Context, map[string]string) (ui.Review, error) {
			return changeReview(plan, original), nil
		},
		Apply: func(ctx context.Context, _ map[string]string, review ui.Review) (string, error) {
			receipt, err := s.Apply(ctx, review.Data.(service.Plan))
			return receiptText(receipt, plan.Operation, r.JobID), err
		},
	}), nil
}

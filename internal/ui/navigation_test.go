package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

func TestPlaygroundIsPersistentAndTypingOwnsNumbers(t *testing.T) {
	m := fixtureDashboard(t)
	_, cmd := m.Update(key('3'))
	if cmd != nil {
		m.Update(cmd())
	}
	if m.view != "playground" || m.playground == nil {
		t.Fatal("not an embedded view")
	}
	m.Update(key('1'))
	if m.view != "jobs" {
		t.Fatal("3 then 1 trapped in input")
	}
	m.Update(key('3'))
	editor := m.playground
	m.Update(special(tea.KeyEnter))
	m.Update(ctrl('u'))
	m.Update(key('1'))
	m.Update(key('2'))
	if m.view != "playground" || !strings.HasPrefix(editor.Expression(), "12 ") {
		t.Fatal(editor.Expression(), m.view)
	}
	m.Update(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt})
	m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.playground != editor || !strings.HasPrefix(editor.Expression(), "12 ") {
		t.Fatal("switch lost draft")
	}
}

func TestNestedConceptHelpReturnsToSameDraft(t *testing.T) {
	m := fixtureDashboard(t)
	f := NewForm(m.ctx, FormSpec{Fields: []Field{{Key: "command", Value: "keep this draft"}}})
	f.SetEmbedded(true)
	m.child = f
	m.childView = "jobs"
	m.Update(special(tea.KeyF1))
	if f.help == nil {
		t.Fatal("missing contextual help")
	}
	_, cmd := m.Update(special(tea.KeyEscape))
	if cmd == nil {
		t.Fatal("help did not return")
	}
	m.Update(cmd())
	if m.child != f || f.help != nil || f.Values()["command"] != "keep this draft" {
		t.Fatal("nested help closed or reset the job draft")
	}
}
func TestPendingWorkflowCancellationRejectsLateFactory(t *testing.T) {
	m := fixtureDashboard(t)
	m.factory = func(ctx context.Context, r WorkflowRequest) (tea.Model, error) { return NewForm(ctx, FormSpec{}), nil }
	cmd := m.openWorkflow("edit", "")
	m.Update(special(tea.KeyEscape))
	m.Update(cmd())
	if m.childPending || m.child != nil {
		t.Fatal("cancelled factory reopened workflow")
	}
}
func TestAsyncDefaultsContinueAfterScheduleContext(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "schedule", Kind: "schedule", Value: "0 9 * * *"}, {Key: "runtime"}}, Load: func(context.Context, map[string]string) []FieldUpdate { return nil }})
	defer f.cancel()
	f.load()
	f.openSchedule()
	runtime := "/project/.venv/bin/python"
	f.Update(formLoaded{f.loadGeneration, []FieldUpdate{{Key: "schedule", Schedule: &ScheduleEditorOptions{Dialect: schedule.System, Timezone: "UTC", Locale: "en"}}, {Key: "runtime", Value: &runtime}}})
	if f.Values()["runtime"] != runtime {
		t.Fatal("schedule update dropped later defaults")
	}
}
func TestPartialApplyFailureStillRequestsReconciliation(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Apply: func(context.Context, map[string]string, Review) (string, error) {
		return "cron saved", fmt.Errorf("metadata save failed")
	}})
	f.SetEmbedded(true)
	f.Update(f.applyCmd()())
	_, cmd := f.Update(special(tea.KeyEnter))
	done := cmd().(WorkflowDoneMsg)
	if !done.Changed || done.Owner != f || done.Err == nil {
		t.Fatal(done)
	}
	if _, cmd = f.Update(special(tea.KeyEnter)); cmd != nil {
		t.Fatal("duplicate completion")
	}
}
func TestWorkflowCompletionCannotCloseAnotherDraft(t *testing.T) {
	m := fixtureDashboard(t)
	current := NewForm(m.ctx, FormSpec{})
	previous := NewForm(m.ctx, FormSpec{})
	m.child = current
	m.childView = "jobs"
	m.Update(WorkflowDoneMsg{Owner: previous})
	if m.child != current {
		t.Fatal("stale completion closed new draft")
	}
}
func TestCreateDoesNotTargetSelectedExistingJob(t *testing.T) {
	m := fixtureDashboard(t)
	var request WorkflowRequest
	m.factory = func(ctx context.Context, r WorkflowRequest) (tea.Model, error) {
		request = r
		return NewForm(ctx, FormSpec{}), nil
	}
	cmd := m.openWorkflow("add", "*/5 * * * *")
	m.Update(cmd())
	if request.JobID != "" || request.Schedule != "*/5 * * * *" {
		t.Fatal(request)
	}
	if m.child == nil || !m.childShowing() {
		t.Fatal("workflow not mounted")
	}
	m.Update(WorkflowDoneMsg{Err: ErrCancelled})
	cmd = m.openWorkflow("edit", "")
	m.Update(cmd())
	if request.JobID != "a" {
		t.Fatal(request)
	}
}
func TestMouseTabsAndHoveredPaneAndModalContainment(t *testing.T) {
	m := fixtureDashboard(t)
	m.mouse = true
	m.focus = 2
	m.Update(tea.MouseWheelMsg{X: 30, Y: 5, Button: tea.MouseWheelDown})
	if m.selected != 1 || m.detailScroll != 0 {
		t.Fatal("wheel used keyboard focus")
	}
	m.help = true
	before := m.selected
	m.Update(tea.MouseWheelMsg{X: 30, Y: 5, Button: tea.MouseWheelUp})
	m.Update(tea.MouseClickMsg{X: 30, Y: 4})
	if m.selected != before {
		t.Fatal("help leaked events")
	}
	m.help = false
	m.Update(tea.MouseClickMsg{X: 28, Y: 0})
	m.Update(tea.MouseReleaseMsg{X: 28, Y: 0})
	if m.view != "week" {
		t.Fatal("visible Week tab not clickable", m.view)
	}
	m.Update(tea.MouseClickMsg{X: 15, Y: 0})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.MouseReleaseMsg{X: 15, Y: 0})
	if m.view != "week" {
		t.Fatal("resize retained press authority")
	}
}
func TestFormVisibleButtonsUseActualGeometry(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Mouse: true, Fields: []Field{{Key: "mode", Options: []string{"a", "b"}, Value: "a"}}, Build: func(context.Context, map[string]string) (Review, error) { return Review{Text: "review"}, nil }})
	defer f.cancel()
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		f.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(f.View().Content, "\n")
		if len(lines) > size[1] {
			t.Fatal("form height overflow")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("form width overflow")
			}
		}
		for _, h := range f.hits() {
			if h.id == "review" && !strings.Contains(ansi.Strip(lines[h.rect.y]), "Review") {
				t.Fatal("invisible review is clickable")
			}
		}
	}
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f.Update(tea.MouseClickMsg{X: 6, Y: 3})
	f.Update(tea.MouseReleaseMsg{X: 6, Y: 3})
	if f.Values()["mode"] != "b" {
		t.Fatal("selector arrow not clickable")
	}
}

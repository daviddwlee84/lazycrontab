package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

func editorFixture(t *testing.T, expression string) *ScheduleEditor {
	t.Helper()
	e := NewScheduleEditor(context.Background(), ScheduleEditorOptions{Expression: expression, Dialect: schedule.System, Timezone: "UTC", Mouse: true})
	t.Cleanup(func() {
		if e.cancelPreview != nil {
			e.cancelPreview()
		}
	})
	runEditorCmd(e, e.Init())
	return e
}
func runEditorCmd(e *ScheduleEditor, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			runEditorCmd(e, child)
		}
		return
	}
	_, next := e.Update(msg)
	// textinput blink commands intentionally remain pending in model tests.
	if _, ok := msg.(schedulePreviewMsg); ok && next != nil {
		runEditorCmd(e, next)
	}
}
func TestScheduleEditorTypingValidationAndPaste(t *testing.T) {
	e := editorFixture(t, "* * * * *")
	if e.Editing() || !e.Valid() {
		t.Fatalf("initial state editing=%v valid=%v", e.Editing(), e.Valid())
	}
	e.Update(special(tea.KeyEnter))
	_, cmd := e.Update(key('5'))
	runEditorCmd(e, cmd)
	if !strings.Contains(e.diagnostic, "use */5, not *5") || e.Valid() {
		t.Fatal(e.diagnostic)
	}
	e.inputs[0].SetValue("*/")
	e.expression = e.fieldsExpression()
	runEditorCmd(e, e.preview())
	if !strings.Contains(e.diagnostic, "incomplete") {
		t.Fatal(e.diagnostic)
	}
	_, cmd = e.Update(key('5'))
	runEditorCmd(e, cmd)
	if !e.Valid() || e.Expression() != "*/5 * * * *" {
		t.Fatal(e.Expression(), e.diagnostic)
	}
	_, cmd = e.Update(tea.PasteMsg{Content: "15 4 * JAN MON"})
	runEditorCmd(e, cmd)
	if e.Expression() != "15 4 * JAN MON" || !e.Valid() {
		t.Fatal(e.Expression(), e.diagnostic)
	}
	before := e.Expression()
	e.Update(tea.PasteMsg{Content: "1 2 3"})
	if e.Expression() != before || !strings.Contains(e.status, "expects 5") {
		t.Fatal("invalid paste changed draft", e.Expression(), e.status)
	}
	e.Update(special(tea.KeyEscape))
	if e.Editing() {
		t.Fatal("Esc failed to blur")
	}
	e.Update(key('1'))
	if e.Expression() != before {
		t.Fatal("navigation typed into field")
	}
	e.Update(special(tea.KeyEnter))
	e.Update(key('1'))
	if e.Expression() == before {
		t.Fatal("numeric typing was lost")
	}
}
func TestScheduleEditorPreciseFieldAndContextErrors(t *testing.T) {
	e := editorFixture(t, "61 * * * *")
	if e.Valid() || !strings.HasPrefix(e.diagnostic, "Minute:") {
		t.Fatal(e.diagnostic)
	}
	runEditorCmd(e, e.SetExpression("0 9 * * *"))
	oldCmd := e.preview()
	runEditorCmd(e, e.SetContext(schedule.System, "Asia/Taipei", "en"))
	old := oldCmd()
	e.Update(old)
	if !e.Valid() || len(e.next) == 0 || e.next[0].Location().String() != "Asia/Taipei" {
		t.Fatal("late timezone result replaced current preview", e.next)
	}
	runEditorCmd(e, e.SetContext(schedule.System, "", "en"))
	if !e.Valid() || len(e.next) != 0 || !strings.Contains(strings.Join(e.warnings, " "), "unknown") {
		t.Fatal("unknown timezone should permit syntax without inventing a forecast")
	}
	other := editorFixture(t, "*/5 * * * *")
	e.Update(other.preview()())
	if len(e.next) != 0 {
		t.Fatal("accepted another editor's result")
	}
}
func TestScheduleEditorSupercronicLayoutsAndMacros(t *testing.T) {
	e := editorFixture(t, "0 9 * * *")
	runEditorCmd(e, e.SetContext(schedule.Supercronic, "UTC", "en"))
	runEditorCmd(e, e.setFieldCount(6))
	if e.Expression() != "0 9 * * * *" || len(e.fieldNames()) != 6 || e.fieldNames()[5] != "Year" || !e.Valid() {
		t.Fatal(e.Expression(), e.diagnostic)
	}
	runEditorCmd(e, e.setFieldCount(7))
	if e.Expression() != "0 0 9 * * * *" || e.fieldNames()[0] != "Second" || !e.Valid() {
		t.Fatal(e.Expression(), e.diagnostic)
	}
	runEditorCmd(e, e.setFieldCount(5))
	if e.Expression() != "0 9 * * *" {
		t.Fatal("field layout lost minute/hour values", e.Expression())
	}
	runEditorCmd(e, e.changeMode(3))
	runEditorCmd(e, e.chooseMacro(5))
	if e.Valid() {
		t.Fatal("Supercronic @reboot accepted")
	}
	runEditorCmd(e, e.SetContext(schedule.System, "UTC", "en"))
	if !e.Valid() || len(e.next) != 0 {
		t.Fatal("native @reboot must remain event-only", e.diagnostic)
	}
}
func TestScheduleEditorMouseAndVisibleGeometry(t *testing.T) {
	e := editorFixture(t, "0 9 * * *")
	e.SetSize(100, 24)
	var h scheduleHit
	for _, hit := range e.layout().hits {
		if hit.id == "field1" {
			h = hit
		}
	}
	e.Update(tea.MouseClickMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
	e.Update(tea.MouseReleaseMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
	if !e.Editing() || e.focus != 1 {
		t.Fatal("click failed to focus hour")
	}
	e.Blur()
	for _, hit := range e.layout().hits {
		if hit.id == "mode1" {
			h = hit
		}
	}
	e.Update(tea.MouseClickMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
	e.SetSize(80, 24)
	e.Update(tea.MouseReleaseMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
	if e.mode != 0 {
		t.Fatal("resize preserved stale pressed target")
	}
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		e.SetSize(size[0], size[1])
		layout := e.layout()
		view := e.View().Content
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatal("height overflow", size)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("width overflow", size, line)
			}
		}
		for _, hit := range layout.hits {
			if hit.x < 0 || hit.x+hit.w > size[0] || hit.y < 0 || hit.y+hit.h > size[1] {
				t.Fatal("invisible hit rectangle", size, hit)
			}
		}
	}
}
func TestScheduleEditorUseAndStandaloneWorkflowReturn(t *testing.T) {
	e := editorFixture(t, "*/5 * * * *")
	msg := e.use()().(ScheduleUseMsg)
	if msg.Expression != e.Expression() || msg.Dialect != schedule.System || msg.Timezone != "UTC" {
		t.Fatal(msg)
	}
	form := NewForm(context.Background(), FormSpec{Title: "Draft", Fields: []Field{{Key: "command"}}})
	defer form.cancel()
	e.options.OnUse = func(ScheduleUseMsg) (tea.Model, error) { return form, nil }
	m := &standaloneSchedule{editor: e}
	m.Update(msg)
	if m.child == nil {
		t.Fatal("use did not embed workflow")
	}
	m.Update(WorkflowDoneMsg{Err: ErrCancelled})
	if m.child != nil || e.Expression() != "*/5 * * * *" {
		t.Fatal("return discarded playground")
	}
	e.Update(special(tea.KeyEnter))
	_, cmd := m.Update(special(tea.KeyEscape))
	if cmd != nil || e.Editing() {
		t.Fatal("first Esc should blur only")
	}
	_, cmd = m.Update(special(tea.KeyEscape))
	if cmd == nil {
		t.Fatal("second Esc should exit standalone")
	}
}

func TestScheduleEditorCachedView(t *testing.T) {
	e := editorFixture(t, "*/5 * * * *")
	beforeGeneration, beforeNext := e.generation, e.next[0]
	for range 10 {
		_ = e.View()
	}
	if e.generation != beforeGeneration || !e.next[0].Equal(beforeNext) {
		t.Fatal("View recomputed preview")
	}
	// A completed cancelled generation cannot make an invalid draft actionable.
	e.inputs[0].SetValue("*/")
	e.expression = e.fieldsExpression()
	e.preview()
	e.Update(schedulePreviewMsg{owner: e.owner, generation: e.generation - 1, next: []time.Time{time.Now()}})
	if e.Valid() {
		t.Fatal("late result enabled an incomplete draft")
	}
}

func TestScheduleEditorThemeAndFivePreviews(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	e := editorFixture(t, "*/5 * * * *")
	e.SetSize(120, 30)
	if strings.Count(e.View().Content, "Next  ") != 5 {
		t.Fatal("five occurrences should fit a roomy terminal")
	}
	if !strings.Contains(e.View().Content, "╭") {
		t.Fatal("roomy fields are not boxed")
	}
	e.options.Theme = "dark"
	dark := e.paint("fixture", "accent", false)
	e.options.Theme = "light"
	light := e.paint("fixture", "accent", false)
	if dark == light {
		t.Fatal("theme preference ignored")
	}
	t.Setenv("NO_COLOR", "1")
	if strings.Contains(e.paint("fixture", "accent", false), "\x1b[") {
		t.Fatal("NO_COLOR ignored")
	}
}

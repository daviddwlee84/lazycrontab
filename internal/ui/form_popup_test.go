package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func draftPopupFixture(t *testing.T) (*dashboard, *Form) {
	t.Helper()
	m := fixtureDashboard(t)
	m.mouse = true
	fields := []Field{
		{Key: "name", Label: "Name", Value: "draft name"},
		{Key: "command", Label: "Command", Value: "echo hi"},
		{Key: "schedule", Label: "Schedule", Kind: "schedule", Value: "*/5 * * * *"},
		{Key: "path", Label: "Script path", Pick: func(context.Context, map[string]string) ([]PickOption, error) {
			return []PickOption{{Label: "/srv/first.sh", Value: "/srv/first.sh"}, {Label: "/srv/second.sh", Value: "/srv/second.sh"}}, nil
		}},
		{Key: "body", Label: "Script content", Kind: "multiline", Value: "echo first\necho second"},
	}
	for i := range 12 {
		fields = append(fields, Field{Key: fmt.Sprintf("extra%d", i), Label: fmt.Sprintf("Extra field %d", i)})
	}
	f := NewForm(m.ctx, FormSpec{Title: "add job · local/user", Popup: true, Mouse: true, Fields: fields, Build: func(_ context.Context, values map[string]string) (Review, error) {
		return Review{Text: "Review " + values["name"], Diff: "--- current\n+++ proposed\n+* * * * * echo hi\n"}, nil
	}, Apply: func(context.Context, map[string]string, Review) (string, error) { return "Saved · local/user", nil }})
	f.SetEmbedded(true)
	f.SetPopupHost(true)
	f.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height - 2})
	m.child, m.childView = f, "jobs"
	t.Cleanup(f.cancel)
	return m, f
}

func popupMousePoint(f *Form, x, y int) (int, int) {
	inner := f.draftPopupLayout().inner
	return inner.x + x, inner.y + y + 2
}

func TestJobDraftPopupPreservesDashboardTypingAndModalNavigation(t *testing.T) {
	m, f := draftPopupFixture(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 44})
	view := ansi.Strip(m.View().Content)
	for _, expected := range []string{"Hosts / sources", "Jobs [1]", "Job draft", "add job · local/user", "draft name"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("draft popup lost %q\n%s", expected, view)
		}
	}
	selected := m.selectedKey
	for _, r := range "123qjkl/" {
		m.Update(key(r))
	}
	if f.Values()["name"] != "draft name123qjkl/" || m.view != "jobs" {
		t.Fatal("typing escaped draft", f.Values(), m.view)
	}
	m.Update(tea.MouseClickMsg{X: 29, Y: 0})
	m.Update(tea.MouseReleaseMsg{X: 29, Y: 0})
	m.Update(tea.MouseClickMsg{X: 0, Y: 6})
	m.Update(tea.MouseReleaseMsg{X: 0, Y: 6})
	if m.view != "jobs" || m.selectedKey != selected || f.focus != 0 {
		t.Fatal("popup click leaked into dashboard")
	}
	m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.view != "jobs" || m.child != f {
		t.Fatal("removed Alt shortcut escaped modal draft")
	}
	m.Update(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt})
	if m.view != "jobs" || m.child != f || f.Values()["name"] != "draft name123qjkl/" {
		t.Fatal("draft did not resume intact")
	}
	_, cancel := m.Update(special(tea.KeyEscape))
	m.Update(cancel())
	if m.child != nil || m.selectedKey != selected {
		t.Fatal("closing draft changed dashboard selection")
	}
}

func TestJobDraftPopupResizeScrollAndVisibleButtons(t *testing.T) {
	m, f := draftPopupFixture(t)
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, row := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(row) > size[0] {
				t.Fatal("draft popup width overflow", size, row)
			}
		}
		inner := f.draftPopupLayout().inner
		if f.width != inner.w || f.height != inner.h {
			t.Fatal("nested form uses screen instead of popup dimensions")
		}
		if size[0] >= 40 {
			for i := range len(f.spec.Fields) {
				f.focusField(i)
				view := ansi.Strip(m.View().Content)
				if !strings.Contains(view, f.spec.Fields[i].Label) {
					t.Fatal("focused field not reachable after resize", size, i, view)
				}
			}
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			for _, hit := range f.hits() {
				if hit.id == "review" || hit.id == "cancel" || hit.id == "advanced" {
					x, y := popupMousePoint(f, hit.rect.x, hit.rect.y)
					if y >= len(lines) || !strings.Contains(ansi.Cut(lines[y], x, x+hit.rect.w), "[") {
						t.Fatal("hidden or shifted button remains clickable", size, hit, x, y)
					}
				}
			}
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	f.focusField(0)
	var review hitRegion
	for _, hit := range f.hits() {
		if hit.id == "review" {
			review = hit
		}
	}
	x, y := popupMousePoint(f, review.rect.x, review.rect.y)
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y}); cmd != nil || f.stage != "edit" {
		t.Fatal("resize kept draft button press authority")
	}
	for _, hit := range f.hits() {
		if hit.id == "review" {
			x, y = popupMousePoint(f, hit.rect.x, hit.rect.y)
		}
	}
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	_, build := m.Update(tea.MouseReleaseMsg{X: x, Y: y})
	if build == nil || f.stage != "building" {
		t.Fatal("draft review button not clickable")
	}
	m.Update(build())
	box, panel := f.popupPanel()
	if box != f.modalLayout().box || strings.Contains(panel, "Job draft") || !strings.Contains(panel, "Changes") {
		t.Fatal("review was double framed as draft", panel)
	}
	m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.view != "jobs" {
		t.Fatal("review allowed draft-only suspension")
	}
	m.Update(special(tea.KeyEscape))
	if !f.DraftPopupActive() || f.Values()["name"] != "draft name" {
		t.Fatal("review back lost draft")
	}
}

func TestNestedJobDraftEditorsUsePopupSpaceAndReturnToDraft(t *testing.T) {
	m, f := draftPopupFixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	f.focusField(2)
	m.Update(special(tea.KeyEnter))
	if f.schedule == nil || f.schedule.width != f.width || f.schedule.height != f.height || !strings.Contains(m.View().Content, "Job draft") {
		t.Fatal("schedule editor escaped popup")
	}
	keep := f.schedule
	m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	m.Update(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt})
	if f.schedule != keep {
		t.Fatal("suspension replaced nested schedule editor")
	}
	m.Update(special(tea.KeyEscape))
	if f.schedule != nil || !f.DraftPopupActive() || f.Values()["schedule"] != "*/5 * * * *" {
		t.Fatal("schedule return lost draft", f.Values())
	}
	f.focusField(3)
	m.Update(ctrl('p'))
	m.Update(f.fetchPick()())
	if f.picker == nil || len(f.picker.items) != 2 {
		t.Fatal("path picker not loaded")
	}
	x, y := popupMousePoint(f, 2, 5)
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	if f.picker.selected != 1 {
		t.Fatal("picker mouse row ignored popup offset")
	}
	m.Update(special(tea.KeyEnter))
	if f.picker != nil || f.Values()["path"] != "/srv/second.sh" {
		t.Fatal("picker return lost choice", f.Values())
	}
	f.focusField(4)
	m.Update(special(tea.KeyEnter))
	if f.multiline == nil {
		t.Fatal("multiline editor not opened")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if f.multiline.area.Width() > f.width {
		t.Fatal("multiline editor retained screen width")
	}
	m.Update(ctrl('s'))
	if f.multiline != nil || f.Values()["body"] != "echo first\necho second" {
		t.Fatal("multiline return lost content")
	}
	m.Update(special(tea.KeyF1))
	if f.help == nil || f.help.width != f.width || f.help.height != f.height {
		t.Fatal("help escaped popup")
	}
	// Help's native hit rules use local X < 12 for Back. A negative coordinate
	// from outside the parent popup must never reach those rules.
	x, y = popupMousePoint(f, -2, f.height-2)
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	if _, leaked := m.Update(tea.MouseReleaseMsg{X: x, Y: y}); leaked != nil || f.help == nil {
		t.Fatal("outside click activated nested help Back")
	}
	beforeTopic := f.help.selected
	x, y = popupMousePoint(f, -2, 3)
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y})
	if f.help.selected != beforeTopic {
		t.Fatal("outside click selected a nested help topic")
	}
	_, closeHelp := m.Update(special(tea.KeyEscape))
	m.Update(closeHelp())
	if f.help != nil || m.child != f || f.Values()["name"] != "draft name" {
		t.Fatal("nested help closed parent draft")
	}
}

func TestPopupOptionDoesNotChangeStandaloneWizardDimensions(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Popup: true, Fields: []Field{{Key: "name", Label: "Name"}}})
	defer f.cancel()
	f.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	if f.DraftPopupActive() || f.Modal() || f.width != 120 || f.height != 32 {
		t.Fatal("standalone wizard changed to embedded popup")
	}
	f.SetEmbedded(true)
	if f.DraftPopupActive() || f.width != 120 || f.height != 32 {
		t.Fatal("generic embedded lifecycle resized without a popup compositor")
	}
	f.SetPopupHost(true)
	if !f.DraftPopupActive() || f.width >= 120 || f.height >= 32 {
		t.Fatal("embedded popup did not resize to its frame")
	}
	f.SetEmbedded(false)
	if f.width != 120 || f.height != 32 {
		t.Fatal("standalone dimensions not restored")
	}
}

func advancedPopupFixture(t *testing.T) (*dashboard, *Form) {
	t.Helper()
	m := fixtureDashboard(t)
	m.mouse = true
	fields := []Field{}
	for i := range 9 {
		label := fmt.Sprintf("Basic %d", i+1)
		if i == 8 {
			label = "Remark"
		}
		fields = append(fields, Field{Key: fmt.Sprintf("basic%d", i), Label: label})
	}
	fields = append(fields,
		Field{Key: "runner", Label: "Run directly or enqueue", Value: "direct", Options: []string{"direct", "pueue"}, Advanced: true},
		Field{Key: "group", Label: "Existing Pueue group", Advanced: true, Show: func(v map[string]string) bool { return v["runner"] == "pueue" }},
		Field{Key: "environment", Label: "Variables", Advanced: true},
	)
	for _, key := range []string{"output", "stderr", "log"} {
		fields = append(fields, Field{Key: key, Label: "Path " + key, Advanced: true, Show: func(v map[string]string) bool { return v["runner"] != "pueue" || v[key] != "" }})
	}
	f := NewForm(m.ctx, FormSpec{Title: "add job · local/user", Popup: true, Mouse: true, Fields: fields})
	f.SetEmbedded(true)
	f.SetPopupHost(true)
	m.child, m.childView = f, "jobs"
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 62})
	f.focusField(8)
	t.Cleanup(f.cancel)
	return m, f
}

func TestAdvancedPopupGrowsAndRevealsNewFields(t *testing.T) {
	m, f := advancedPopupFixture(t)
	before := f.draftPopupLayout().box.h
	m.Update(ctrl('o'))
	if len(f.visible()) != 14 || f.focus != 9 || f.draftPopupLayout().box.h <= before {
		t.Fatal("Advanced did not grow and select the first new field", len(f.visible()), f.focus, before, f.draftPopupLayout().box)
	}
	view := ansi.Strip(m.View().Content)
	for _, expected := range []string{"Run directly or enqueue", "Path log", "1–14/14", "Basic ^O"} {
		if !strings.Contains(view, expected) {
			t.Fatal("expanded form did not reveal settings", expected, view)
		}
	}
	f.focusField(11)
	m.Update(tea.PasteMsg{Content: "TOKEN=keep"})
	f.mousePress = "apply"
	m.Update(ctrl('o'))
	if f.advanced || f.focus != 8 || f.Values()["environment"] != "TOKEN=keep" || f.draftPopupLayout().box.h != before || f.mousePress != "" {
		t.Fatal("collapse lost answers or nearest basic focus", f.focus, f.Values(), f.draftPopupLayout().box)
	}
	m.Update(ctrl('o'))
	if f.focus != 9 || f.Values()["environment"] != "TOKEN=keep" {
		t.Fatal("reopening Advanced lost input")
	}
}

func TestAdvancedPopupCappedRangeNavigationAndControls(t *testing.T) {
	m, f := advancedPopupFixture(t)
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}} {
		if f.advanced {
			m.Update(ctrl('o'))
		}
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		f.focusField(8)
		m.Update(ctrl('o'))
		if f.focus != 9 || !strings.Contains(ansi.Strip(m.View().Content), "Run directly or enqueue") {
			t.Fatal("capped popup hid first advanced field", size, f.focus, m.View().Content)
		}
		if !strings.Contains(ansi.Strip(m.View().Content), "/14") || !strings.Contains(ansi.Strip(m.View().Content), "Tab/↑↓/wheel") {
			t.Fatal("capped popup lacks range and navigation affordance", size, m.View().Content)
		}
		for range len(f.visible()) {
			m.Update(special(tea.KeyTab))
			if !strings.Contains(ansi.Strip(m.View().Content), f.spec.Fields[f.focus].Label) {
				t.Fatal("Tab cannot reach a field after expansion", size, f.focus)
			}
		}
		f.focusField(11)
		m.Update(special(tea.KeyDown))
		if f.focus != 12 {
			t.Fatal("Down did not navigate single-line fields")
		}
		inner := f.draftPopupLayout().inner
		m.Update(tea.MouseWheelMsg{X: inner.x + 1, Y: inner.y + 2, Button: tea.MouseWheelDown})
		if f.focus != 13 {
			t.Fatal("wheel did not reveal the next field")
		}
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		for _, hit := range f.hits() {
			if hit.id == "review" || hit.id == "advanced" || hit.id == "cancel" {
				x, y := popupMousePoint(f, hit.rect.x, hit.rect.y)
				if y >= len(lines) || x+hit.rect.w > size[0] || !strings.Contains(ansi.Cut(lines[y], x, x+hit.rect.w), "[") {
					t.Fatal("expanded control is outside its painted bounds", size, hit)
				}
			}
		}
	}
}

func TestPopupReflowsDynamicVisibilityAndKeepsNestedEditorStable(t *testing.T) {
	m, f := advancedPopupFixture(t)
	m.Update(ctrl('o'))
	fullHeight := f.draftPopupLayout().box.h
	m.Update(special(tea.KeyRight))
	if f.Values()["runner"] != "pueue" || len(f.visible()) != 12 || f.draftPopupLayout().box.h >= fullHeight || f.height != f.draftPopupLayout().inner.h {
		t.Fatal("runner visibility did not reflow actual model geometry", f.Values(), len(f.visible()), f.height, f.draftPopupLayout())
	}
	f.focusField(10)
	m.Update(special(tea.KeyF1))
	if f.help == nil {
		t.Fatal("nested help did not open")
	}
	nestedHeight := f.draftPopupLayout().box.h
	f.loadGeneration = 42
	f.loadValues = f.Values()
	runner := "direct"
	m.Update(formLoaded{generation: 42, fields: []FieldUpdate{{Key: "runner", Value: &runner, Options: []string{"direct", "pueue"}}}})
	if f.help == nil || f.draftPopupLayout().box.h != nestedHeight || f.help.height != f.height {
		t.Fatal("late defaults moved an open nested editor")
	}
	_, closeHelp := m.Update(special(tea.KeyEscape))
	m.Update(closeHelp())
	if f.help != nil || len(f.visible()) != 14 || f.draftPopupLayout().box.h != fullHeight || f.height != f.draftPopupLayout().inner.h {
		t.Fatal("return from nested editor did not reflow updated fields", f.height, f.draftPopupLayout())
	}
	if f.focus != 9 {
		t.Fatal("hidden focus did not move to nearest visible field", f.focus)
	}
}

func TestAdvancedMouseExpansionResetsPressAtNewGeometry(t *testing.T) {
	m, f := advancedPopupFixture(t)
	var advanced hitRegion
	for _, hit := range f.hits() {
		if hit.id == "advanced" {
			advanced = hit
		}
	}
	x, y := popupMousePoint(f, advanced.rect.x, advanced.rect.y)
	m.Update(tea.MouseClickMsg{X: x, Y: y})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y})
	if !f.advanced || f.focus != 9 || f.mousePress != "" {
		t.Fatal("mouse expansion did not reveal Advanced safely")
	}
	m.Update(tea.MouseReleaseMsg{X: x, Y: y})
	if !f.advanced {
		t.Fatal("stale release toggled the moved button")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if !strings.Contains(ansi.Strip(m.View().Content), "Run directly or enqueue") || f.height != f.draftPopupLayout().inner.h {
		t.Fatal("resize lost the revealed field")
	}
}

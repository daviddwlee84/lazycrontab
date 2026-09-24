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

func TestJobDraftPopupPreservesDashboardTypingAndExplicitViewSwitch(t *testing.T) {
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
	if m.view != "playground" || m.child != f {
		t.Fatal("explicit draft suspension was lost")
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

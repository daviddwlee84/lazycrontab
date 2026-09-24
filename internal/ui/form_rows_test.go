package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func stableRowsFixture(t *testing.T) *Form {
	t.Helper()
	presetIs := func(kinds ...string) func(map[string]string) bool {
		return func(v map[string]string) bool {
			for _, kind := range kinds {
				if v["preset"] == kind {
					return true
				}
			}
			return false
		}
	}
	fields := []Field{
		{Key: "host", Label: "Host", Value: "local", Options: []string{"local"}},
		{Key: "source", Label: "Source", Value: "user", Options: []string{"user"}},
		{Key: "name", Label: "Name", Value: "keep name"},
		{Key: "preset", Label: "What to run", Value: "command", Options: []string{"command", "shell", "uv", "managed"}},
		{Key: "command", Label: "Command", Value: "echo preserved", Slot: "payload", Show: presetIs("command")},
		{Key: "script", Label: "Script", Value: "/srv/preserved.sh", Slot: "payload", Show: presetIs("shell", "uv")},
		{Key: "body", Label: "Script content", Value: "echo first\necho second", Kind: "multiline", Slot: "payload", Show: presetIs("managed")},
		{Key: "runtime", Label: "Interpreter", Value: "/bin/sh", Reserve: true, Show: presetIs("shell", "uv", "managed")},
		{Key: "project", Label: "Project", Value: "/srv/project", Reserve: true, Show: presetIs("uv")},
		{Key: "directory", Label: "Working directory"},
		{Key: "args", Label: "Arguments", Reserve: true, Show: presetIs("shell", "uv", "managed")},
		{Key: "schedule", Label: "Schedule", Kind: "schedule", Value: "* * * * *"},
		{Key: "enabled", Label: "Enabled", Value: "true", Options: []string{"true", "false"}},
		{Key: "remark", Label: "Remark"},
		{Key: "runner", Label: "Run directly or enqueue", Value: "direct", Options: []string{"direct", "pueue"}, Advanced: true},
		{Key: "group", Label: "Pueue group", Value: "default", Options: []string{"default", "work"}, Advanced: true, DisabledWhen: func(v map[string]string) string {
			if v["runner"] != "pueue" {
				return "Choose Pueue to use a group"
			}
			return ""
		}},
		{Key: "environment", Label: "Variables", Advanced: true},
	}
	for _, key := range []string{"output", "stderr", "log"} {
		fields = append(fields, Field{Key: key, Label: key, Advanced: true, KeepEditingOnDisable: true, DisabledWhen: func(v map[string]string) string {
			if v["runner"] == "pueue" && v[key] == "" {
				return "Captured by Pueue"
			}
			return ""
		}, Pick: func(context.Context, map[string]string) ([]PickOption, error) {
			return []PickOption{{Label: "/tmp/picked", Value: "/tmp/picked"}}, nil
		}})
	}
	f := NewForm(context.Background(), FormSpec{Title: "add job", Fields: fields, Mouse: true, Popup: true, Build: func(context.Context, map[string]string) (Review, error) { return Review{Text: "review"}, nil }})
	f.SetEmbedded(true)
	f.SetPopupHost(true)
	f.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
	t.Cleanup(f.cancel)
	return f
}

func formRowFor(f *Form, key string) int {
	for i, row := range f.renderRows() {
		if row.field >= 0 && f.spec.Fields[row.field].Key == key {
			return i
		}
	}
	return -1
}

func TestSharedSlotsReserveGeometryAndAllStoredValues(t *testing.T) {
	f := stableRowsFixture(t)
	if len(f.inputs) != 20 || len(f.renderRows()) != 12 || f.structuralRowCount() != 18 {
		t.Fatal("stored inputs and render structure were conflated", len(f.inputs), len(f.renderRows()), f.structuralRowCount())
	}
	f.focusField(3)
	before, initial := f.draftPopupLayout().box, f.Values()
	positions := map[string]int{}
	for _, key := range []string{"directory", "schedule", "enabled", "remark"} {
		positions[key] = formRowFor(f, key)
	}
	for _, wanted := range []string{"shell", "uv", "managed", "command"} {
		f.Update(special(tea.KeyRight))
		if f.Values()["preset"] != wanted || f.draftPopupLayout().box != before || len(f.renderRows()) != 12 || f.focus != 3 || f.fieldTop != 0 {
			t.Fatal("preset moved the frame or focus", wanted, f.Values()["preset"], f.draftPopupLayout(), f.focus, f.fieldTop)
		}
		for key, row := range positions {
			if formRowFor(f, key) != row {
				t.Fatal("preset shifted a fixed row", wanted, key)
			}
		}
	}
	for _, key := range []string{"command", "script", "body", "runtime", "project"} {
		if f.Values()[key] != initial[key] {
			t.Fatal("inactive draft value was discarded", key)
		}
	}
	view := ansi.Strip(f.View().Content)
	if strings.Contains(view, "Interpreter") || strings.Contains(view, "Project") || strings.Contains(view, "Arguments") {
		t.Fatal("hidden reserved rows exposed labels", view)
	}
	rows := f.renderRows()
	if rows[5].field != -1 || rows[6].field != -1 || rows[8].field != -1 {
		t.Fatal("inapplicable fields did not reserve blank rows", rows)
	}
}

func TestSlotOrderingAndEmptyMembersAreDeterministic(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Fields: []Field{
		{Key: "first", Slot: "same", Show: func(map[string]string) bool { return false }},
		{Key: "between"},
		{Key: "second", Slot: "same", Show: func(map[string]string) bool { return false }},
		{Key: "advanced", Slot: "extra", Advanced: true},
	}})
	defer f.cancel()
	if rows := f.renderRows(); len(rows) != 2 || rows[0].field != -1 || rows[1].field != 1 {
		t.Fatal("empty slot did not stay at first declaration", rows)
	}
	f.spec.Fields[2].Show = nil
	f.reflowDraft()
	if rows := f.renderRows(); rows[0].field != 2 || rows[1].field != 1 {
		t.Fatal("active later member moved the slot", rows)
	}
}

func TestPopupAnchorAndViewportStayStableAcrossModes(t *testing.T) {
	f := stableRowsFixture(t)
	for _, size := range [][2]int{{180, 60}, {120, 32}, {80, 24}, {40, 10}} {
		if f.advanced {
			f.Update(ctrl('o'))
		}
		f.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		basic := f.draftPopupLayout().box
		f.Update(ctrl('o'))
		expanded := f.draftPopupLayout().box
		if expanded.y != basic.y || expanded.x != basic.x || expanded.h < basic.h || f.focus != 14 {
			t.Fatal("Advanced recentered instead of growing down", size, basic, expanded, f.focus)
		}
		viewport, row := f.fieldTop, formRowFor(f, "runner")
		f.Update(special(tea.KeyRight))
		if f.Values()["runner"] != "pueue" || f.draftPopupLayout().box != expanded || f.fieldTop != viewport || formRowFor(f, "runner") != row {
			t.Fatal("runner changed stable geometry", size)
		}
		f.Update(special(tea.KeyLeft))
		f.Update(ctrl('o'))
		if f.draftPopupLayout().box != basic {
			t.Fatal("collapse changed the common anchor", size)
		}
	}
}

func TestSelectorsNavigateWithoutChangingValuesAndTextOwnsJK(t *testing.T) {
	f := stableRowsFixture(t)
	f.focusField(3)
	f.Update(special(tea.KeyDown))
	if f.focus != 4 || f.Values()["preset"] != "command" {
		t.Fatal("Down changed selector instead of focus", f.focus, f.Values()["preset"])
	}
	f.Update(special(tea.KeyUp))
	f.Update(key('j'))
	if f.focus != 4 || f.Values()["preset"] != "command" {
		t.Fatal("selector j did not move focus")
	}
	f.Update(key('j'))
	f.Update(key('k'))
	if f.Values()["command"] != "echo preservedjk" || f.focus != 4 {
		t.Fatal("textfield j/k became navigation", f.Values())
	}
	f.Update(special(tea.KeyUp))
	f.Update(key('l'))
	if f.Values()["preset"] != "shell" || f.focus != 3 {
		t.Fatal("selector l did not change value")
	}
	f.Update(key('k'))
	if f.focus != 2 || f.Values()["preset"] != "shell" {
		t.Fatal("selector k changed value")
	}
}

func TestDisabledRowsStayVisibleSkipFocusAndRejectLateActions(t *testing.T) {
	f := stableRowsFixture(t)
	f.Update(ctrl('o'))
	if !strings.Contains(ansi.Strip(f.View().Content), "Choose Pueue to use a group") {
		t.Fatal("disabled group reason was hidden")
	}
	f.Update(special(tea.KeyDown))
	if f.focus != 16 || f.Values()["runner"] != "direct" {
		t.Fatal("disabled group accepted focus", f.focus)
	}
	f.focusField(17)
	if cmd := f.startPicker(); cmd == nil || f.picker == nil {
		t.Fatal("enabled output picker could not open")
	}
	generation := f.picker.generation
	f.loadGeneration = 7
	f.loadValues = f.Values()
	runner := "pueue"
	f.mousePress = "browse:17"
	f.Update(formLoaded{generation: 7, fields: []FieldUpdate{{Key: "runner", Value: &runner}}})
	if f.picker != nil || f.mousePress != "" || f.fieldFocusable(17) {
		t.Fatal("disabled state retained an open picker or pending press")
	}
	f.Update(formPickLoaded{owner: f, generation: generation, items: []PickOption{{Value: "/tmp/stale"}}})
	if cmd := f.activate("browse:17"); cmd != nil {
		t.Fatal("stale Browse action activated disabled output")
	}
	if f.Values()["output"] != "" {
		t.Fatal("stale picker result modified output")
	}
	for _, hit := range f.hits() {
		if strings.HasSuffix(hit.id, ":17") || strings.HasSuffix(hit.id, ":18") || strings.HasSuffix(hit.id, ":19") {
			t.Fatal("disabled output retained a hit region", hit)
		}
	}
	f.inputs[14].SetValue("direct")
	f.reflowDraft()
	if cmd := f.activate("next:15"); cmd != nil || f.Values()["group"] != "default" {
		t.Fatal("stale selector arrow changed disabled group")
	}
}

func TestClearingEnabledOutputLatchesOnlyUntilBlurOrReview(t *testing.T) {
	f := stableRowsFixture(t)
	f.inputs[17].SetValue("/tmp/current.log")
	f.Update(ctrl('o'))
	f.Update(special(tea.KeyRight))
	f.focusField(17)
	f.inputs[17].CursorEnd()
	f.Update(ctrl('u'))
	if f.focus != 17 || f.Values()["output"] != "" || !f.fieldFocusable(17) || f.keepEditingIndex != 17 {
		t.Fatal("clearing output jumped focus mid-edit", f.focus, f.Values(), f.keepEditingIndex)
	}
	// An unrelated async suggestion must not interrupt this edit session.
	f.loadGeneration = 8
	f.loadValues = f.Values()
	directory := "/srv/new-default"
	f.Update(formLoaded{generation: 8, fields: []FieldUpdate{{Key: "directory", Value: &directory}}})
	if f.focus != 17 || !f.fieldFocusable(17) {
		t.Fatal("unrelated suggestion revoked output edit latch")
	}
	f.Update(key('x'))
	if f.Values()["output"] != "x" {
		t.Fatal("latched empty output could not be retyped")
	}
	f.Update(ctrl('u'))
	f.Update(special(tea.KeyTab))
	if f.focus == 17 || f.fieldFocusable(17) || f.keepEditingIndex != -1 || f.Values()["output"] != "" {
		t.Fatal("blur did not restore disabled Pueue output", f.focus, f.Values())
	}
	f.inputs[17].SetValue("/tmp/external.log")
	f.reflowDraft()
	f.focusField(17)
	f.loadGeneration = 9
	f.loadValues = f.Values()
	empty := ""
	f.Update(formLoaded{generation: 9, fields: []FieldUpdate{{Key: "output", Value: &empty}}})
	if f.focus == 17 || f.keepEditingIndex != -1 || f.fieldFocusable(17) {
		t.Fatal("async clearing incorrectly latched editability")
	}
	f.inputs[17].SetValue("/tmp/review.log")
	f.focusField(17)
	f.inputs[17].CursorEnd()
	f.Update(ctrl('u'))
	f.Update(ctrl('s'))
	if f.keepEditingIndex != -1 {
		t.Fatal("review retained edit-only latch")
	}
}

func TestAllDisabledFormsRemainSafeAndReviewable(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "blocked", Label: "Blocked", Value: "keep", Options: []string{"keep", "change"}, DisabledWhen: func(map[string]string) string { return "Unavailable here" }}}, Build: func(context.Context, map[string]string) (Review, error) {
		return Review{Text: "Review disabled values"}, nil
	}})
	defer f.cancel()
	for _, msg := range []tea.Msg{special(tea.KeyTab), special(tea.KeyUp), special(tea.KeyDown), special(tea.KeyEnter), key('j'), key('k'), special(tea.KeyLeft), ctrl('p')} {
		f.Update(msg)
	}
	if f.focus != -1 || f.Values()["blocked"] != "keep" || !strings.Contains(ansi.Strip(f.View().Content), "Unavailable here") {
		t.Fatal("all-disabled form changed or hid state", f.focus, f.Values())
	}
	_, review := f.Update(ctrl('s'))
	f.Update(review())
	if f.stage != "review" {
		t.Fatal("disabled form lost footer operations")
	}
}

func TestSlotRegistryIndicesSurviveNestedEditorsAndInactiveFocus(t *testing.T) {
	f := stableRowsFixture(t)
	f.focusField(3)
	for range 3 {
		f.Update(special(tea.KeyRight))
	}
	f.focusField(6)
	f.Update(special(tea.KeyEnter))
	if f.multiline == nil || f.multiline.index != 6 {
		t.Fatal("slot row index replaced original input index")
	}
	f.Update(ctrl('s'))
	if f.Values()["body"] != "echo first\necho second" || f.Values()["command"] != "echo preserved" {
		t.Fatal("nested slot editor wrote another field")
	}
	f.inputs[3].SetValue("shell")
	f.reflowDraft()
	if f.focus != 5 {
		t.Fatal("active slot replacement did not retain logical focus", f.focus)
	}
}

func TestInactiveNestedEditorRejectsTypingBeforeReflow(t *testing.T) {
	f := stableRowsFixture(t)
	f.inputs[3].SetValue("managed")
	f.reflowDraft()
	f.focusField(6)
	f.Update(special(tea.KeyEnter))
	before := f.Values()["body"]
	// State can change before a queued key/paste is delivered. Revalidate in
	// the nested handler itself, rather than relying on a later layout pass.
	f.inputs[3].SetValue("command")
	f.Update(tea.PasteMsg{Content: "must not be appended"})
	if f.multiline != nil || f.Values()["body"] != before {
		t.Fatal("inactive nested editor consumed queued paste")
	}
}

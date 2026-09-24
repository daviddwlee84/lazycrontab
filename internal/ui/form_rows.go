package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// A render row always owns two lines. Its field is the original input registry
// index; -1 is an intentionally blank reserved row, never an editable input.
type formRow struct {
	field int
	slot  string
}

func (f *Form) renderRows() []formRow {
	values := f.Values()
	rows := []formRow{}
	seen := map[string]bool{}
	for index, field := range f.spec.Fields {
		if field.Slot != "" {
			if seen[field.Slot] {
				continue
			}
			seen[field.Slot] = true
			selected, included := -1, false
			for candidate, member := range f.spec.Fields {
				if member.Slot != field.Slot || member.Advanced && !f.advanced {
					continue
				}
				included = true
				if selected < 0 && (member.Show == nil || member.Show(values)) {
					selected = candidate
				}
			}
			if included {
				rows = append(rows, formRow{selected, field.Slot})
			}
			continue
		}
		if field.Advanced && !f.advanced {
			continue
		}
		shown := field.Show == nil || field.Show(values)
		if shown {
			rows = append(rows, formRow{field: index})
		} else if field.Reserve {
			rows = append(rows, formRow{field: -1})
		}
	}
	return rows
}

func (f *Form) structuralRowCount() int {
	count := 0
	seen := map[string]bool{}
	for _, field := range f.spec.Fields {
		if field.Slot != "" {
			if seen[field.Slot] {
				continue
			}
			seen[field.Slot] = true
		}
		count++
	}
	return count
}

func (f *Form) disabledReason(index int, values map[string]string) string {
	field := f.spec.Fields[index]
	if field.DisabledWhen == nil {
		return ""
	}
	reason := field.DisabledWhen(values)
	if reason == "" || index != f.focus || index != f.keepEditingIndex {
		return reason
	}
	if reason != f.keepEditingReason {
		return reason
	}
	return ""
}

func (f *Form) focusable() []int {
	values := f.Values()
	var fields []int
	for _, row := range f.renderRows() {
		if row.field >= 0 && f.disabledReason(row.field, values) == "" {
			fields = append(fields, row.field)
		}
	}
	return fields
}

func (f *Form) fieldFocusable(index int) bool {
	if index < 0 || index >= len(f.inputs) {
		return false
	}
	for _, candidate := range f.focusable() {
		if index == candidate {
			return true
		}
	}
	return false
}

func (f *Form) rowSignature() string {
	var out strings.Builder
	values := f.Values()
	for _, row := range f.renderRows() {
		reason := ""
		if row.field >= 0 {
			reason = f.disabledReason(row.field, values)
		}
		fmt.Fprintf(&out, "%d:%q:%q;", row.field, row.slot, reason)
	}
	return out.String()
}

func (f *Form) clearEditLatch() {
	f.keepEditingIndex = -1
	f.keepEditingReason = ""
}

func (f *Form) blurField() {
	if f.focus >= 0 && f.focus < len(f.inputs) {
		f.inputs[f.focus].Blur()
	}
	f.focus = -1
	f.clearEditLatch()
	f.mousePress = ""
}

func (f *Form) latchClearedInput(msg tea.Msg, before string, previous map[string]string) {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
	default:
		return
	}
	field := f.spec.Fields[f.focus]
	if !field.KeepEditingOnDisable || field.DisabledWhen == nil || before == "" || f.fieldValue(f.focus) != "" || field.DisabledWhen(previous) != "" || field.DisabledWhen(f.Values()) == "" {
		return
	}
	f.keepEditingIndex = f.focus
	f.keepEditingReason = field.DisabledWhen(f.Values())
}

func (f *Form) ensureFieldViewport() {
	rows := f.renderRows()
	page := max(1, (f.height-10)/2)
	f.fieldTop = max(0, min(f.fieldTop, max(0, len(rows)-page)))
	if f.focus < 0 {
		return
	}
	for i, row := range rows {
		if row.field != f.focus {
			continue
		}
		if i < f.fieldTop {
			f.fieldTop = i
		} else if i >= f.fieldTop+page {
			f.fieldTop = i - page + 1
		}
		return
	}
}

package ui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type formPicker struct {
	index, selected, generation int
	query                       textinput.Model
	values                      map[string]string
	items                       []PickOption
	pending                     bool
	err                         error
	press                       string
	cancel                      context.CancelFunc
}
type formPickLoaded struct {
	owner      *Form
	generation int
	items      []PickOption
	err        error
}

func (f *Form) startPicker() tea.Cmd {
	if f.stage != "edit" || len(f.inputs) == 0 || f.spec.Fields[f.focus].Pick == nil {
		return nil
	}
	q := textinput.New()
	q.Prompt = "/ "
	q.SetVirtualCursor(true)
	q.SetWidth(max(1, f.width-6))
	q.Focus()
	f.picker = &formPicker{index: f.focus, query: q, values: f.Values()}
	return tea.Batch(textinput.Blink, f.fetchPick())
}
func (f *Form) fetchPick() tea.Cmd {
	p := f.picker
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(f.ctx)
	p.cancel = cancel
	p.pending = true
	p.err = nil
	p.generation = int(formSequence.Add(1))
	g := p.generation
	values := map[string]string{}
	for k, v := range p.values {
		values[k] = v
	}
	load := f.spec.Fields[p.index].Pick
	return func() tea.Msg { items, err := load(ctx, values); return formPickLoaded{f, g, items, err} }
}
func (f *Form) acceptPick(m formPickLoaded) tea.Cmd {
	if m.owner != f || f.picker == nil || f.picker.generation != m.generation {
		return nil
	}
	f.picker.items = m.items
	f.picker.err = m.err
	f.picker.pending = false
	f.picker.selected = 0
	return nil
}
func (f *Form) pickerItems() []PickOption {
	var out []PickOption
	q := strings.ToLower(f.picker.query.Value())
	for _, item := range f.picker.items {
		if strings.Contains(strings.ToLower(item.Label+" "+item.Value), q) {
			out = append(out, item)
		}
	}
	return out
}
func (f *Form) pickCurrent() tea.Cmd {
	p := f.picker
	items := f.pickerItems()
	if p.pending || len(items) == 0 {
		return nil
	}
	item := items[min(p.selected, len(items)-1)]
	if item.Navigate {
		p.values[f.spec.Fields[p.index].Key] = item.Value
		p.query.SetValue("")
		p.selected = 0
		return f.fetchPick()
	}
	f.inputs[p.index].SetValue(item.Value)
	key := f.spec.Fields[p.index].Key
	if p.cancel != nil {
		p.cancel()
	}
	f.picker = nil
	return f.changed(key)
}
func (f *Form) closePicker() {
	if f.picker.cancel != nil {
		f.picker.cancel()
	}
	f.picker = nil
}
func (f *Form) pickerStart() int { return max(0, f.picker.selected-max(1, f.height-9)+1) }
func (f *Form) pickerHits() []hitRegion {
	rows := max(1, f.height-9)
	items := f.pickerItems()
	start := f.pickerStart()
	var hits []hitRegion
	for i := start; i < min(len(items), start+rows); i++ {
		hits = append(hits, hitRegion{fmt.Sprintf("row:%d", i), rect{0, 4 + i - start, f.width, 1}})
	}
	hits = append(hits, hitRegion{"choose", rect{0, max(0, f.height-2), 16, 1}}, hitRegion{"back", rect{17, max(0, f.height-2), 12, 1}})
	return hits
}
func (f *Form) updatePicker(msg tea.Msg) tea.Cmd {
	p := f.picker
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		p.press = ""
		switch m.String() {
		case "esc", "ctrl+c":
			f.closePicker()
			return nil
		case "enter":
			return f.pickCurrent()
		case "up":
			p.selected = max(0, p.selected-1)
			return nil
		case "down":
			p.selected = min(max(0, len(f.pickerItems())-1), p.selected+1)
			return nil
		case "pgdown":
			p.selected = min(max(0, len(f.pickerItems())-1), p.selected+max(1, f.height-9))
			return nil
		case "pgup":
			p.selected = max(0, p.selected-max(1, f.height-9))
			return nil
		}
	case tea.MouseClickMsg:
		if !f.spec.Mouse {
			return nil
		}
		mouse := m.Mouse()
		p.press = hitAt(f.pickerHits(), mouse.X, mouse.Y)
		if strings.HasPrefix(p.press, "row:") {
			fmt.Sscanf(p.press, "row:%d", &p.selected)
			p.press = ""
		}
		return nil
	case tea.MouseReleaseMsg:
		if !f.spec.Mouse {
			return nil
		}
		mouse := m.Mouse()
		id := hitAt(f.pickerHits(), mouse.X, mouse.Y)
		pressed := p.press
		p.press = ""
		if id == pressed {
			if id == "choose" {
				return f.pickCurrent()
			}
			if id == "back" {
				f.closePicker()
			}
		}
		return nil
	case tea.MouseWheelMsg:
		if !f.spec.Mouse {
			return nil
		}
		p.press = ""
		delta := 1
		if strings.Contains(m.String(), "up") {
			delta = -1
		}
		p.selected = max(0, min(max(0, len(f.pickerItems())-1), p.selected+delta))
		return nil
	}
	before := p.query.Value()
	var cmd tea.Cmd
	p.query, cmd = p.query.Update(msg)
	if before != p.query.Value() {
		p.selected = 0
	}
	return cmd
}
func (f *Form) pickerView() tea.View {
	p := f.picker
	t := styles(f.dark)
	field := f.spec.Fields[p.index]
	lines := []string{t.Title.Render("Choose " + safe(field.Label)), t.Muted.Render(clip(p.values[field.Key], f.width)), p.query.View(), ""}
	items := f.pickerItems()
	start := f.pickerStart()
	for i := start; i < min(len(items), start+max(1, f.height-9)); i++ {
		line := "  " + safe(items[i].Label)
		if items[i].Navigate {
			line += " /"
		}
		if i == p.selected {
			line = t.Selected.Render(pad("› "+safe(items[i].Label), f.width))
		}
		lines = append(lines, clip(line, f.width))
	}
	if p.pending {
		lines = append(lines, t.Warning.Render("Looking up choices…"))
	} else if p.err != nil {
		lines = append(lines, t.Error.Render(safe(p.err.Error())))
	} else if len(items) == 0 {
		lines = append(lines, "No matching choices. Esc returns to manual entry.")
	}
	lines = lines[:min(len(lines), max(0, f.height-2))]
	for len(lines) < f.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, t.Accent.Render("[ Choose Enter ] [ Back Esc ]"), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), f.width, f.height))
	v.AltScreen = true
	if f.spec.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

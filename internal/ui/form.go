package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var ErrCancelled = errors.New("cancelled")

type Field struct {
	Key, Label, Value string
	Options           []string
	Advanced          bool
	Unavailable       map[string]string
	Show              func(map[string]string) bool
}
type FieldUpdate struct {
	Key         string
	Options     []string
	Unavailable map[string]string
}
type Review struct {
	Text string
	Data any
}
type FormSpec struct {
	Title  string
	Fields []Field
	Live   func(map[string]string) string
	Build  func(context.Context, map[string]string) (Review, error)
	Apply  func(context.Context, map[string]string, Review) (string, error)
	Mouse  bool
	Load   func(context.Context, map[string]string) []FieldUpdate
}
type FormResult struct {
	Values    map[string]string
	Review    Review
	Message   string
	Submitted bool
}
type formBuilt struct {
	review Review
	err    error
}
type formApplied struct {
	message string
	err     error
}
type formLoaded struct {
	generation int
	fields     []FieldUpdate
}
type Form struct {
	spec                         FormSpec
	ctx                          context.Context
	cancel                       context.CancelFunc
	inputs                       []textinput.Model
	focus, width, height, scroll int
	advanced                     bool
	stage                        string
	review                       Review
	message                      string
	err                          error
	result                       FormResult
	mousePress                   string
	loadGeneration               int
}

func NewForm(ctx context.Context, spec FormSpec) *Form {
	child, cancel := context.WithCancel(ctx)
	f := &Form{spec: spec, ctx: child, cancel: cancel, width: 80, height: 24, stage: "edit"}
	for _, field := range spec.Fields {
		input := textinput.New()
		input.SetValue(field.Value)
		input.Prompt = ""
		input.SetWidth(60)
		input.CharLimit = 65536
		input.SetVirtualCursor(true)
		f.inputs = append(f.inputs, input)
	}
	if len(f.inputs) > 0 {
		f.inputs[0].Focus()
	}
	return f
}
func (f *Form) Init() tea.Cmd { return tea.Batch(textinput.Blink, f.load()) }
func (f *Form) load() tea.Cmd {
	if f.spec.Load == nil {
		return nil
	}
	f.loadGeneration++
	g := f.loadGeneration
	v := f.Values()
	load, ctx := f.spec.Load, f.ctx
	return func() tea.Msg { return formLoaded{g, load(ctx, v)} }
}
func (f *Form) Values() map[string]string {
	v := map[string]string{}
	for i, field := range f.spec.Fields {
		v[field.Key] = f.inputs[i].Value()
	}
	return v
}
func (f *Form) visible() []int {
	out := []int{}
	values := f.Values()
	for i, v := range f.spec.Fields {
		if (!v.Advanced || f.advanced) && (v.Show == nil || v.Show(values)) {
			out = append(out, i)
		}
	}
	return out
}
func (f *Form) move(delta int) {
	list := f.visible()
	if len(list) == 0 {
		return
	}
	pos := 0
	for i, n := range list {
		if n == f.focus {
			pos = i
		}
	}
	f.inputs[f.focus].Blur()
	f.focus = list[(pos+delta+len(list))%len(list)]
	f.inputs[f.focus].Focus()
}
func (f *Form) reviewCmd() tea.Cmd {
	f.mousePress = ""
	f.err = nil
	f.stage = "building"
	values := f.Values()
	return func() tea.Msg { r, e := f.spec.Build(f.ctx, values); return formBuilt{r, e} }
}
func (f *Form) applyCmd() tea.Cmd {
	f.mousePress = ""
	f.stage = "applying"
	values, review := f.Values(), f.review
	return func() tea.Msg { message, err := f.spec.Apply(f.ctx, values, review); return formApplied{message, err} }
}
func (f *Form) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case formLoaded:
		if m.generation != f.loadGeneration {
			return f, nil
		}
		for _, u := range m.fields {
			for i := range f.spec.Fields {
				if f.spec.Fields[i].Key == u.Key {
					f.spec.Fields[i].Options = u.Options
					f.spec.Fields[i].Unavailable = u.Unavailable
				}
			}
		}
		return f, nil
	case tea.WindowSizeMsg:
		f.width = max(20, m.Width)
		f.height = max(6, m.Height)
		f.mousePress = ""
		for i := range f.inputs {
			f.inputs[i].SetWidth(max(8, f.width-8))
		}
		return f, nil
	case formBuilt:
		if f.stage != "building" {
			return f, nil
		}
		if m.err != nil {
			f.err = m.err
			f.stage = "edit"
		} else {
			f.review = m.review
			f.stage = "review"
			f.scroll = 0
		}
		return f, nil
	case formApplied:
		if f.stage != "applying" {
			return f, nil
		}
		f.message = m.message
		f.err = m.err
		f.stage = "result"
		f.result = FormResult{Values: f.Values(), Review: f.review, Message: m.message, Submitted: m.err == nil}
		return f, nil
	case tea.MouseClickMsg:
		if !f.spec.Mouse {
			return f, nil
		}
		mouse := m.Mouse()
		f.mousePress = ""
		if mouse.Y == f.height-2 && mouse.X >= 0 && mouse.X < min(14, f.width) {
			f.mousePress = f.stage
		}
		if f.stage == "edit" {
			list, start, rows := f.fieldWindow()
			row := (mouse.Y - 2) / 2
			if mouse.Y >= 2 && row >= 0 && row < rows && start+row < len(list) {
				f.inputs[f.focus].Blur()
				f.focus = list[start+row]
				f.inputs[f.focus].Focus()
			}
		}
		return f, nil
	case tea.MouseReleaseMsg:
		mouse := m.Mouse()
		hit := f.mousePress != "" && f.mousePress == f.stage && mouse.Y == f.height-2 && mouse.X >= 0 && mouse.X < min(14, f.width)
		f.mousePress = ""
		if hit {
			if f.stage == "edit" {
				return f, f.reviewCmd()
			}
			if f.stage == "review" {
				return f, f.applyCmd()
			}
		}
		return f, nil
	case tea.KeyPressMsg:
		key := m.String()
		if m.IsRepeat && (key == "ctrl+s" || key == "y") {
			return f, nil
		}
		if key == "ctrl+c" {
			f.cancel()
			f.err = ErrCancelled
			return f, tea.Quit
		}
		if f.stage == "result" {
			if key == "enter" || key == "q" || key == "esc" {
				return f, tea.Quit
			}
			if key == "down" || key == "j" {
				f.scroll++
			}
			if key == "up" || key == "k" {
				f.scroll = max(0, f.scroll-1)
			}
			return f, nil
		}
		if f.stage == "building" || f.stage == "applying" {
			if key == "esc" {
				f.cancel()
				f.err = ErrCancelled
				return f, tea.Quit
			}
			return f, nil
		}
		if f.stage == "review" {
			switch key {
			case "ctrl+s", "y":
				return f, f.applyCmd()
			case "esc", "n":
				if len(f.inputs) == 0 {
					f.err = ErrCancelled
					return f, tea.Quit
				}
				f.stage = "edit"
				f.scroll = 0
			case "down", "j":
				f.scroll++
			case "up", "k":
				f.scroll = max(0, f.scroll-1)
			case "pgdown":
				f.scroll += max(1, f.height-7)
			case "pgup":
				f.scroll = max(0, f.scroll-max(1, f.height-7))
			}
			return f, nil
		}
		switch key {
		case "esc":
			f.err = ErrCancelled
			return f, tea.Quit
		case "ctrl+s":
			return f, f.reviewCmd()
		case "ctrl+o":
			f.advanced = !f.advanced
			if !f.advanced && f.spec.Fields[f.focus].Advanced {
				f.inputs[f.focus].Blur()
				f.focus = 0
				f.inputs[0].Focus()
			}
			return f, nil
		case "tab", "enter":
			f.move(1)
			return f, nil
		case "shift+tab":
			f.move(-1)
			return f, nil
		}
		if len(f.spec.Fields) > 0 && len(f.spec.Fields[f.focus].Options) > 0 {
			options := f.spec.Fields[f.focus].Options
			pos := 0
			for i, v := range options {
				if v == f.inputs[f.focus].Value() {
					pos = i
				}
			}
			switch key {
			case "left", "up", "h", "k":
				pos = (pos + len(options) - 1) % len(options)
			case "right", "down", "l", "j", "space":
				pos = (pos + 1) % len(options)
			default:
				return f, nil
			}
			if reason := f.spec.Fields[f.focus].Unavailable[options[pos]]; reason != "" {
				f.err = fmt.Errorf("%s", reason)
				return f, nil
			}
			f.inputs[f.focus].SetValue(options[pos])
			if f.spec.Fields[f.focus].Key == "host" {
				return f, f.load()
			}
			return f, nil
		}
	}
	if f.stage == "edit" && len(f.inputs) > 0 {
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		return f, cmd
	}
	return f, nil
}
func (f *Form) fieldWindow() ([]int, int, int) {
	list := f.visible()
	rows := max(1, (f.height-10)/2)
	pos := 0
	for i, n := range list {
		if n == f.focus {
			pos = i
		}
	}
	start := max(0, pos-rows+1)
	return list, start, rows
}
func safe(s string) string {
	var b strings.Builder
	for _, r := range ansi.Strip(s) {
		if r == '\n' || r == '\t' || r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Plain removes terminal control sequences from human-facing external text.
func Plain(s string) string           { return safe(s) }
func clip(s string, width int) string { return ansi.Truncate(s, max(1, width), "…") }
func (f *Form) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Render(clip(f.spec.Title, f.width-2))
	lines := []string{title, ""}
	footer := ""
	switch f.stage {
	case "edit":
		list, start, rows := f.fieldWindow()
		for _, i := range list[start:min(len(list), start+rows)] {
			field := f.spec.Fields[i]
			prefix := "  "
			if i == f.focus {
				prefix = "› "
			}
			lines = append(lines, clip(prefix+safe(field.Label), f.width))
			value := f.inputs[i].View()
			if len(field.Options) > 0 {
				value = "◀ " + safe(f.inputs[i].Value()) + " ▶"
				if reason := field.Unavailable[f.inputs[i].Value()]; reason != "" {
					value += " (unavailable: " + safe(reason) + ")"
				}
			}
			lines = append(lines, "  "+clip(value, f.width-3))
		}
		if f.spec.Live != nil {
			lines = append(lines, "", clip(safe(f.spec.Live(f.Values())), f.width-2))
		}
		footer = "Ctrl+S review · Tab next · Shift+Tab back · Ctrl+O advanced · Esc cancel"
	case "review":
		body := strings.Split(safe(f.review.Text), "\n")
		start := min(f.scroll, max(0, len(body)-1))
		for _, line := range body[start:min(len(body), start+max(1, f.height-6))] {
			lines = append(lines, clip(line, f.width-2))
		}
		footer = "Ctrl+S / y apply · Enter does nothing · Esc back · ↑↓ scroll"
		if len(f.inputs) == 0 {
			footer = "Ctrl+S / y apply · Enter does nothing · Esc cancel · ↑↓ scroll"
		}
	case "building":
		lines = append(lines, "Preparing review…")
		footer = "Esc cancel"
	case "applying":
		lines = append(lines, "Applying to the reviewed target…")
		footer = "Esc stops waiting; dispatched operations may already have taken effect"
	case "result":
		body := strings.Split(safe(f.message), "\n")
		start := min(f.scroll, max(0, len(body)-1))
		lines = append(lines, body[start:min(len(body), start+max(1, f.height-7))]...)
		footer = "Enter / Esc return · ↑↓ scroll"
	}
	if f.err != nil {
		lines = append(lines, "", clip("Error: "+safe(f.err.Error()), f.width-2))
	}
	lines = lines[:min(len(lines), max(1, f.height-3))]
	for len(lines) < f.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, clip(footer, f.width), "")
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	if f.spec.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}
func RunForm(ctx context.Context, spec FormSpec) (FormResult, error) {
	f := NewForm(ctx, spec)
	defer f.cancel()
	m, err := tea.NewProgram(f, tea.WithContext(ctx)).Run()
	if err != nil {
		return FormResult{}, err
	}
	result := m.(*Form)
	if result.err != nil {
		return result.result, result.err
	}
	if !result.result.Submitted {
		return result.result, ErrCancelled
	}
	return result.result, nil
}
func Confirm(ctx context.Context, title, body string, apply func(context.Context) (string, error)) (FormResult, error) {
	return RunFormReview(ctx, FormSpec{Title: title, Build: func(context.Context, map[string]string) (Review, error) { return Review{Text: body}, nil }, Apply: func(ctx context.Context, _ map[string]string, _ Review) (string, error) { return apply(ctx) }})
}
func RunFormReview(ctx context.Context, spec FormSpec) (FormResult, error) {
	f := NewForm(ctx, spec)
	defer f.cancel()
	r, e := spec.Build(ctx, map[string]string{})
	if e != nil {
		return FormResult{}, e
	}
	f.review = r
	f.stage = "review"
	m, e := tea.NewProgram(f, tea.WithContext(ctx)).Run()
	if e != nil {
		return FormResult{}, e
	}
	end := m.(*Form)
	if end.err != nil {
		return end.result, end.err
	}
	if !end.result.Submitted {
		return end.result, ErrCancelled
	}
	return end.result, nil
}
func ValueInt(v map[string]string, k string) (int, error) {
	var n int
	_, err := fmt.Sscanf(v[k], "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", k)
	}
	return n, nil
}

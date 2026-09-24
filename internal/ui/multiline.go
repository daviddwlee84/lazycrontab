package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// Multiline values never pass through textinput's newline sanitizer.
type multilineDraft struct {
	index             int
	area              textarea.Model
	readonly, pending bool
	notice, press     string
	editGeneration    int
}
type textEditorReady struct {
	owner      *Form
	generation int
	path       string
	err        error
	cleanup    func()
}
type textEditorResult struct {
	owner      *Form
	generation int
	content    string
	err        error
}
type multilinePaste struct {
	owner   *Form
	draft   *multilineDraft
	content string
	err     error
}

func inlineTextSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || r != '\n' && unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (f *Form) fieldValue(index int) string {
	if f.spec.Fields[index].Kind == "multiline" {
		return f.multilineValues[index]
	}
	return f.inputs[index].Value()
}
func (f *Form) setFieldValue(index int, value string) {
	if f.spec.Fields[index].Kind == "multiline" {
		if f.multilineValues == nil {
			f.multilineValues = map[int]string{}
		}
		f.multilineValues[index] = value
	} else {
		f.inputs[index].SetValue(value)
	}
}
func (f *Form) openMultiline() tea.Cmd {
	if !f.fieldFocusable(f.focus) {
		return nil
	}
	area := textarea.New()
	area.Prompt = ""
	area.Placeholder = "echo \"hi\"\n# Add more shell commands here"
	area.CharLimit = 0
	area.MaxHeight = 0
	area.MaxWidth = 0
	area.SetVirtualCursor(true)
	f.multiline = &multilineDraft{index: f.focus, area: area}
	f.loadMultilineValue()
	return f.multiline.area.Focus()
}
func (f *Form) loadMultilineValue() {
	m := f.multiline
	body := f.fieldValue(m.index)
	m.area.SetValue(body)
	m.area.MoveToBegin()
	m.readonly = m.area.Value() != body
	if m.readonly {
		m.notice = "Use F4 editor to preserve this content's tabs/line endings or size."
	} else {
		m.notice = ""
	}
	f.resizeMultiline()
}
func (f *Form) resizeMultiline() {
	m := f.multiline
	m.press = ""
	m.area.SetWidth(max(1, f.width-2))
	m.area.SetHeight(max(1, f.height-6))
	style := textarea.DefaultStyles(f.dark)
	if os.Getenv("NO_COLOR") != "" {
		style = textarea.Styles{}
	}
	m.area.SetStyles(style)
}
func (f *Form) closeMultiline() tea.Cmd {
	if f.multiline.pending {
		return nil
	}
	key := f.spec.Fields[f.multiline.index].Key
	f.multiline = nil
	return f.changed(key)
}
func (f *Form) multilineHits() []hitRegion {
	if f.height < 6 {
		return nil
	}
	var hits []hitRegion
	x := 0
	buttons := [][2]string{{"done", "Done Ctrl+S"}, {"back", "Back Esc"}}
	if f.spec.TextEditor != nil {
		buttons = append(buttons, [2]string{"editor", "Editor F4"})
	}
	for _, b := range buttons {
		w := len(b[1]) + 4
		if x+w <= f.width {
			hits = append(hits, hitRegion{b[0], rect{x, f.height - 2, w, 1}})
		}
		x += w + 1
	}
	return hits
}
func (f *Form) updateMultiline(msg tea.Msg) tea.Cmd {
	m := f.multiline
	if !f.fieldFocusable(m.index) {
		f.multiline = nil
		return nil
	}
	switch v := msg.(type) {
	case multilinePaste:
		if v.owner != f || v.draft != m {
			return nil
		}
		if v.err != nil {
			m.notice = v.err.Error()
			return nil
		}
		return f.updateMultiline(tea.PasteMsg{Content: v.content})
	case tea.KeyPressMsg:
		m.press = ""
		if m.pending {
			return nil
		}
		if !inlineTextSafe(v.Text) {
			m.notice = "Use F4 editor to preserve control characters exactly."
			return nil
		}
		switch v.String() {
		case "esc", "ctrl+s", "ctrl+c":
			return f.closeMultiline()
		case "f4":
			return f.prepareTextEditor()
		case "ctrl+v":
			return func() tea.Msg { value, err := clipboard.ReadAll(); return multilinePaste{f, m, value, err} }
		case "tab":
			if !m.readonly {
				m.area.InsertString("    ")
				f.setFieldValue(m.index, m.area.Value())
			}
			return nil
		}
	case tea.MouseClickMsg:
		if !f.spec.Mouse {
			return nil
		}
		mouse := v.Mouse()
		m.press = hitAt(f.multilineHits(), mouse.X, mouse.Y)
		return nil
	case tea.MouseReleaseMsg:
		if !f.spec.Mouse {
			return nil
		}
		mouse := v.Mouse()
		id := hitAt(f.multilineHits(), mouse.X, mouse.Y)
		press := m.press
		m.press = ""
		if id != "" && id == press && !m.pending {
			if id == "editor" {
				return f.prepareTextEditor()
			}
			return f.closeMultiline()
		}
		return nil
	case tea.PasteMsg:
		if !inlineTextSafe(v.Content) {
			m.notice = "Paste contains tabs or non-LF text. Use F4 editor to preserve it exactly."
			return nil
		}
		if len(f.fieldValue(m.index))+len(v.Content) > 1<<20 || m.area.LineCount()+strings.Count(v.Content, "\n") >= 10000 {
			m.notice = "Paste exceeds the inline editor limit; use F4 editor or --script-content-file."
			return nil
		}
	case tea.MouseWheelMsg:
		if !f.spec.Mouse || m.pending {
			return nil
		}
		m.press = ""
		for i := 0; i < 3; i++ {
			if strings.Contains(v.String(), "up") {
				m.area.CursorUp()
			} else {
				m.area.CursorDown()
			}
		}
		return nil
	}
	if m.pending || m.readonly {
		return nil
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(offsetMouse(msg, -2))
	f.setFieldValue(m.index, m.area.Value())
	return cmd
}
func (f *Form) prepareTextEditor() tea.Cmd {
	m := f.multiline
	if m.pending {
		return nil
	}
	if f.spec.TextEditor == nil {
		m.notice = "No external editor configured for this field."
		return nil
	}
	m.pending = true
	m.press = ""
	m.editGeneration = int(formSequence.Add(1))
	g := m.editGeneration
	body := f.fieldValue(m.index)
	ctx := f.ctx
	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return textEditorReady{owner: f, generation: g, err: err}
		}
		file, err := os.CreateTemp("", "lazycrontab-draft-*.sh")
		if err != nil {
			return textEditorReady{owner: f, generation: g, err: err}
		}
		path := file.Name()
		stop := context.AfterFunc(ctx, func() { os.Remove(path) })
		cleanup := func() { stop(); os.Remove(path) }
		_, err = file.WriteString(body)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			cleanup()
		}
		return textEditorReady{f, g, path, err, cleanup}
	}
}
func (f *Form) launchTextEditor(msg textEditorReady) tea.Cmd {
	if msg.owner != f || f.multiline == nil || msg.generation != f.multiline.editGeneration || f.finished || !f.fieldFocusable(f.multiline.index) {
		if msg.cleanup != nil {
			msg.cleanup()
		}
		return nil
	}
	if msg.err != nil {
		f.multiline.pending = false
		f.multiline.notice = msg.err.Error()
		return nil
	}
	spec := f.spec.TextEditor(msg.path)
	cmd := exec.CommandContext(f.ctx, spec.Path, spec.Args[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer msg.cleanup()
		body := ""
		if err == nil {
			stat, e := os.Stat(msg.path)
			if e != nil {
				err = e
			} else if stat.Size() > 1<<20 {
				err = fmt.Errorf("script content exceeds 1 MiB; previous draft preserved")
			} else {
				data, e := os.ReadFile(msg.path)
				err = e
				body = string(data)
			}
		}
		return textEditorResult{f, msg.generation, body, err}
	})
}
func (f *Form) receiveTextEditor(msg textEditorResult) tea.Cmd {
	if msg.owner != f || f.multiline == nil || msg.generation != f.multiline.editGeneration || !f.fieldFocusable(f.multiline.index) {
		return nil
	}
	f.multiline.pending = false
	if msg.err != nil {
		f.multiline.notice = msg.err.Error()
		return nil
	}
	f.setFieldValue(f.multiline.index, msg.content)
	f.loadMultilineValue()
	return f.multiline.area.Focus()
}
func (f *Form) multilineView() tea.View {
	m := f.multiline
	t := styles(f.dark)
	lines := []string{t.Title.Render(clip(f.spec.Fields[m.index].Label, f.width)), t.Muted.Render("Draft only · saved after job review and Apply")}
	lines = append(lines, strings.Split(fitLines(m.area.View(), max(1, f.width-2), max(1, f.height-6)), "\n")...)
	notice := m.notice
	if m.pending {
		notice = "Opening local editor…"
	}
	if notice == "" {
		notice = fmt.Sprintf("%d lines · Enter newline · F4 editor", strings.Count(f.fieldValue(m.index), "\n")+1)
	}
	lines = append(lines, t.Muted.Render(clip(safe(notice), f.width)), "")
	labels := map[string]string{"done": "[ Done Ctrl+S ]", "back": "[ Back Esc ]", "editor": "[ Editor F4 ]"}
	var buttons []string
	for _, hit := range f.multilineHits() {
		buttons = append(buttons, labels[hit.id])
	}
	lines = append(lines, t.Accent.Render(strings.Join(buttons, " ")), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), f.width, f.height))
	v.AltScreen = true
	if f.spec.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

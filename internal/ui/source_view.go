package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

// RawSourceEditMsg carries the viewed target rather than the dashboard's possibly
// changed selection. The parent checks ownership before handing off its terminal.
type RawSourceEditMsg struct {
	Owner        *SourceView
	Host, Source string
}

type SourceView struct {
	snapshot                                          service.Snapshot
	source                                            config.Source
	raw                                               string
	lines                                             []string
	maxWidth                                          int
	width, height, top, left                          int
	embedded, mouse, dark, finished, editingRequested bool
	theme, editKey, press, status                     string
	filter                                            textinput.Model
	filtering                                         bool
	matches                                           []int
	match                                             int
}

func NewSourceView(snapshot service.Snapshot, source config.Source, theme string, mouse bool, editKey string) *SourceView {
	input := textinput.New()
	input.Prompt = "/ "
	input.SetVirtualCursor(true)
	m := &SourceView{snapshot: snapshot, source: source, theme: theme, mouse: mouse, dark: theme != "light", editKey: editKey, width: 80, height: 24, filter: input, match: -1}
	if snapshot.Document != nil {
		m.raw = snapshot.Document.Raw
	}
	if m.raw != "" {
		for _, line := range strings.Split(strings.TrimSuffix(m.raw, "\n"), "\n") {
			display := strings.ReplaceAll(safe(strings.TrimSuffix(line, "\r")), "\t", "    ")
			m.lines = append(m.lines, display)
			m.maxWidth = max(m.maxWidth, ansi.StringWidth(display))
		}
	}
	return m
}
func (m *SourceView) SetEmbedded(v bool) { m.embedded = v }
func (m *SourceView) SetMouse(v bool)    { m.mouse = v; m.press = "" }
func (m *SourceView) Init() tea.Cmd      { return tea.RequestBackgroundColor }
func (m *SourceView) readonly() bool     { return m.source.ReadOnly || m.source.Kind == "system" }
func (m *SourceView) rows() int          { return max(1, m.height-6) }
func (m *SourceView) gutter() int {
	return min(max(3, len(fmt.Sprint(max(1, len(m.lines))))+2), max(0, m.width-1))
}
func (m *SourceView) clamp() {
	m.top = max(0, min(m.top, max(0, len(m.lines)-m.rows())))
	m.left = max(0, min(m.left, max(0, m.maxWidth-max(1, m.width-m.gutter()))))
}
func (m *SourceView) finish() tea.Cmd {
	if m.finished {
		return nil
	}
	m.finished = true
	if m.embedded {
		return func() tea.Msg { return WorkflowDoneMsg{Owner: m} }
	}
	return tea.Quit
}
func (m *SourceView) edit() tea.Cmd {
	if m.readonly() {
		m.status = "Read-only source · viewing is available; editing is disabled."
		return nil
	}
	if m.editingRequested {
		return nil
	}
	m.editingRequested = true
	return func() tea.Msg { return RawSourceEditMsg{m, m.snapshot.Host, m.snapshot.Source} }
}
func (m *SourceView) search() {
	m.matches = nil
	m.match = -1
	q := strings.ToLower(m.filter.Value())
	if q == "" {
		return
	}
	for i, line := range m.lines {
		if strings.Contains(strings.ToLower(line), q) {
			m.matches = append(m.matches, i)
		}
	}
	if len(m.matches) > 0 {
		m.match = 0
		m.top = m.matches[0]
		m.clamp()
	}
}
func (m *SourceView) nextMatch(delta int) {
	if len(m.matches) > 0 {
		m.match = (m.match + delta + len(m.matches)) % len(m.matches)
		m.top = m.matches[m.match]
		m.clamp()
	}
}
func (m *SourceView) buttons() [][2]string {
	out := [][2]string{{"back", "Back Esc"}}
	if !m.readonly() {
		out = append(out, [2]string{"edit", "Edit " + m.editKey})
	}
	out = append(out, [2]string{"copy", "Copy c"}, [2]string{"search", "Find /"})
	return out
}
func (m *SourceView) hits() []hitRegion {
	if m.height < 6 {
		return nil
	}
	var hits []hitRegion
	x := 0
	for _, button := range m.buttons() {
		w := ansi.StringWidth(button[1]) + 4
		if x+w > m.width {
			break
		}
		hits = append(hits, hitRegion{button[0], rect{x, m.height - 2, w, 1}})
		x += w + 1
	}
	return hits
}
func (m *SourceView) activate(id string) tea.Cmd {
	switch id {
	case "back":
		return m.finish()
	case "edit":
		return m.edit()
	case "copy":
		m.status = "Copied original source to terminal clipboard"
		return tea.SetClipboard(m.raw)
	case "search":
		m.filtering = true
		return m.filter.Focus()
	}
	return nil
}
func (m *SourceView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, v.Width)
		m.height = max(1, v.Height)
		m.filter.SetWidth(max(1, m.width-4))
		m.press = ""
		m.clamp()
		return m, nil
	case tea.BackgroundColorMsg:
		if m.theme == "" || m.theme == "auto" {
			m.dark = v.IsDark()
		}
		return m, nil
	case tea.MouseClickMsg:
		if !m.mouse {
			return m, nil
		}
		p := v.Mouse()
		m.press = hitAt(m.hits(), p.X, p.Y)
		return m, nil
	case tea.MouseReleaseMsg:
		if !m.mouse {
			return m, nil
		}
		p := v.Mouse()
		id := hitAt(m.hits(), p.X, p.Y)
		press := m.press
		m.press = ""
		if id != "" && id == press {
			return m, m.activate(id)
		}
		return m, nil
	case tea.MouseWheelMsg:
		if !m.mouse {
			return m, nil
		}
		m.press = ""
		if strings.Contains(v.String(), "up") {
			m.top -= 3
		} else {
			m.top += 3
		}
		m.clamp()
		return m, nil
	case tea.KeyPressMsg:
		m.press = ""
		key := v.String()
		if key == "ctrl+c" {
			return m, m.finish()
		}
		if m.filtering {
			switch key {
			case "esc":
				m.filtering = false
				m.filter.Blur()
				m.filter.SetValue("")
				m.search()
				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()
				return m, nil
			}
		} else {
			if key == m.editKey {
				return m, m.edit()
			}
			switch key {
			case "esc", "q":
				return m, m.finish()
			case "/":
				return m, m.activate("search")
			case "c", "ctrl+y":
				return m, m.activate("copy")
			case "up", "k":
				m.top--
			case "down", "j":
				m.top++
			case "left", "h":
				m.left -= 8
			case "right", "l":
				m.left += 8
			case "pgup":
				m.top -= m.rows()
			case "pgdown", "space":
				m.top += m.rows()
			case "home", "g":
				m.top = 0
			case "end", "G":
				m.top = len(m.lines)
			case "n":
				m.nextMatch(1)
			case "N":
				m.nextMatch(-1)
			}
			m.clamp()
			return m, nil
		}
	}
	if m.filtering {
		before := m.filter.Value()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		if before != m.filter.Value() {
			m.search()
		}
		return m, cmd
	}
	return m, nil
}
func (m *SourceView) View() tea.View {
	t := styles(m.dark)
	title := "Raw source · " + m.snapshot.Host + "/" + m.snapshot.Source
	if m.readonly() {
		title += " · read-only"
	}
	location := m.source.Path
	if m.source.Kind == "user" {
		location = "User crontab · crontab -l"
	}
	lines := []string{t.Title.Render(clip(title, m.width)), t.Muted.Render(clip(safe(location), m.width))}
	if m.filtering {
		lines = append(lines, m.filter.View())
	} else {
		lines = append(lines, t.Muted.Render(clip("↑↓ scroll · ←→ pan · / find · n/N match", m.width)))
	}
	for row := 0; row < m.rows(); row++ {
		index := m.top + row
		line := ""
		if index < len(m.lines) {
			text := m.lines[index]
			gutter := fmt.Sprintf("%*d │", max(1, m.gutter()-2), index+1)
			gutter = clip(gutter, m.gutter())
			content := ansi.Cut(text, m.left, m.left+max(1, m.width-m.gutter()))
			if m.match >= 0 && m.matches[m.match] == index {
				line = t.Selected.Render(pad(gutter+content, m.width))
			} else {
				line = t.Muted.Render(gutter) + content
			}
		} else if len(m.lines) == 0 && row == 0 {
			line = "Empty source"
			if !m.snapshot.Exists {
				line = "No crontab installed yet"
			}
		}
		lines = append(lines, clip(line, m.width))
	}
	status := fmt.Sprintf("Lines %d–%d/%d · column %d", min(len(m.lines), m.top+1), min(len(m.lines), m.top+m.rows()), len(m.lines), m.left+1)
	if m.filter.Value() != "" {
		status += fmt.Sprintf(" · %d matches", len(m.matches))
	}
	if m.status != "" {
		status = m.status
	}
	lines = append(lines, t.Muted.Render(clip(status, m.width)))
	var buttons []string
	visible := m.hits()
	for i := range visible {
		buttons = append(buttons, "[ "+m.buttons()[i][1]+" ]")
	}
	lines = append(lines, t.Accent.Render(strings.Join(buttons, " ")), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), m.width, m.height))
	v.AltScreen = true
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

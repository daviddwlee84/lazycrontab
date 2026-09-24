package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// sourceRetry retains the CLI's private editor draft while the user repairs an
// invalid document. It never installs content or approves a write.
type sourceRetry struct {
	title, diagnostic, theme, press string
	width, height, scroll           int
	mouse, dark, retry              bool
}

func (m *sourceRetry) Init() tea.Cmd { return tea.RequestBackgroundColor }
func (m *sourceRetry) hits() []hitRegion {
	if m.height < 6 {
		return nil
	}
	var hits []hitRegion
	for _, b := range []hitRegion{{"retry", rect{0, m.height - 2, 20, 1}}, {"discard", rect{21, m.height - 2, 15, 1}}} {
		if b.rect.x+b.rect.w <= m.width {
			hits = append(hits, b)
		}
	}
	return hits
}
func (m *sourceRetry) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, v.Width)
		m.height = max(1, v.Height)
		m.press = ""
	case tea.BackgroundColorMsg:
		if m.theme == "auto" || m.theme == "" {
			m.dark = v.IsDark()
		}
	case tea.KeyPressMsg:
		m.press = ""
		switch v.String() {
		case "enter":
			m.retry = true
			return m, tea.Quit
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.scroll = max(0, m.scroll-1)
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll = max(0, m.scroll-max(1, m.height-6))
		case "pgdown":
			m.scroll += max(1, m.height-6)
		}
	case tea.MouseClickMsg:
		if m.mouse {
			p := v.Mouse()
			m.press = hitAt(m.hits(), p.X, p.Y)
		}
	case tea.MouseReleaseMsg:
		if m.mouse {
			p := v.Mouse()
			id := hitAt(m.hits(), p.X, p.Y)
			press := m.press
			m.press = ""
			if id != "" && id == press {
				m.retry = id == "retry"
				return m, tea.Quit
			}
		}
	case tea.MouseWheelMsg:
		if m.mouse {
			m.press = ""
			if strings.Contains(v.String(), "up") {
				m.scroll = max(0, m.scroll-3)
			} else {
				m.scroll += 3
			}
		}
	}
	return m, nil
}
func (m *sourceRetry) View() tea.View {
	t := styles(m.dark)
	body := strings.Split(ansi.Hardwrap(safe(m.diagnostic), max(1, m.width), true), "\n")
	start := min(m.scroll, max(0, len(body)-1))
	lines := []string{t.Title.Render(clip(m.title, m.width)), t.Warning.Render("Source unchanged · your private draft is retained"), ""}
	lines = append(lines, body[start:min(len(body), start+max(1, m.height-6))]...)
	lines = lines[:min(len(lines), max(0, m.height-2))]
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	labels := map[string]string{"retry": "[ Edit again Enter ]", "discard": "[ Discard Esc ]"}
	var buttons []string
	for _, h := range m.hits() {
		buttons = append(buttons, labels[h.id])
	}
	lines = append(lines, t.Accent.Render(strings.Join(buttons, " ")), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), m.width, m.height))
	v.AltScreen = true
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}
func RetrySourceEdit(ctx context.Context, title, diagnostic string, mouse bool, theme string) (bool, error) {
	m := &sourceRetry{title: title, diagnostic: diagnostic, mouse: mouse, theme: theme, dark: theme != "light", width: 80, height: 24}
	_, err := RunModel(ctx, m)
	return m.retry, err
}

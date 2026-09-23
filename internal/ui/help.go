package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	concept "github.com/daviddwlee84/lazycrontab/internal/help"
)

type HelpBrowser struct {
	width, height, selected, scroll  int
	query                            textinput.Model
	filtering, embedded, mouse, dark bool
	topic, press                     string
	nested                           bool
	finished                         bool
}
type helpClosedMsg struct{ owner *HelpBrowser }

func (h *HelpBrowser) SetMouse(v bool) { h.mouse = v }

func NewHelpBrowser(topic string, mouse bool) *HelpBrowser {
	q := textinput.New()
	q.Prompt = "/ "
	q.SetVirtualCursor(true)
	return &HelpBrowser{width: 80, height: 24, query: q, topic: topic, mouse: mouse}
}
func (h *HelpBrowser) SetEmbedded(v bool) { h.embedded = v }
func (h *HelpBrowser) Init() tea.Cmd      { return tea.RequestBackgroundColor }
func (h *HelpBrowser) items() []concept.Topic {
	var out []concept.Topic
	q := strings.ToLower(h.query.Value())
	for _, t := range concept.Topics() {
		if strings.Contains(strings.ToLower(t.Title+" "+t.Summary+" "+t.Body), q) {
			out = append(out, t)
		}
	}
	return out
}
func (h *HelpBrowser) listRows() int  { return max(1, (h.height-5)/3) }
func (h *HelpBrowser) listStart() int { return max(0, h.selected-h.listRows()+1) }
func (h *HelpBrowser) back() tea.Cmd {
	h.press = ""
	if h.nested {
		return func() tea.Msg { return helpClosedMsg{h} }
	}
	if h.topic != "" {
		h.topic = ""
		h.scroll = 0
		return nil
	}
	if h.embedded {
		if h.finished {
			return nil
		}
		h.finished = true
		return func() tea.Msg { return WorkflowDoneMsg{Owner: h} }
	}
	return tea.Quit
}
func (h *HelpBrowser) open() {
	items := h.items()
	if len(items) > 0 {
		h.topic = items[min(h.selected, len(items)-1)].Name
		h.scroll = 0
		h.filtering = false
		h.query.Blur()
	}
}
func (h *HelpBrowser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		h.width = max(1, m.Width)
		h.height = max(1, m.Height)
		h.query.SetWidth(max(1, h.width-4))
		h.press = ""
		return h, nil
	case tea.BackgroundColorMsg:
		h.dark = m.IsDark()
		return h, nil
	case tea.MouseWheelMsg:
		if !h.mouse {
			return h, nil
		}
		delta := 1
		if strings.Contains(m.String(), "up") {
			delta = -1
		}
		if h.topic != "" {
			h.scroll = max(0, h.scroll+delta*3)
		} else {
			h.selected = max(0, min(max(0, len(h.items())-1), h.selected+delta))
		}
		h.press = ""
		return h, nil
	case tea.MouseClickMsg:
		if !h.mouse {
			return h, nil
		}
		mouse := m.Mouse()
		h.press = ""
		if mouse.Y == h.height-2 && mouse.X < 12 {
			h.press = "back"
		} else if h.topic == "" && mouse.Y >= 3 && mouse.Y < h.height-2 && (mouse.Y-3)/3 < h.listRows() && h.listStart()+(mouse.Y-3)/3 < len(h.items()) {
			h.selected = h.listStart() + (mouse.Y-3)/3
			h.press = fmt.Sprint(h.selected)
		}
		return h, nil
	case tea.MouseReleaseMsg:
		if !h.mouse {
			return h, nil
		}
		mouse := m.Mouse()
		p := h.press
		h.press = ""
		if p == "back" && mouse.Y == h.height-2 && mouse.X < 12 {
			return h, h.back()
		}
		if p == fmt.Sprint(h.selected) && h.topic == "" && mouse.Y >= 3 && mouse.Y < h.height-2 && h.listStart()+(mouse.Y-3)/3 == h.selected {
			h.open()
		}
		return h, nil
	case tea.KeyPressMsg:
		h.press = ""
		key := m.String()
		if key == "ctrl+c" {
			if h.nested {
				return h, h.back()
			}
			if h.embedded {
				if h.finished {
					return h, nil
				}
				h.finished = true
				return h, func() tea.Msg { return WorkflowDoneMsg{Owner: h} }
			}
			return h, tea.Quit
		}
		if h.filtering {
			switch key {
			case "esc":
				h.filtering = false
				h.query.Blur()
				h.query.SetValue("")
				return h, nil
			case "enter":
				h.open()
				return h, nil
			case "up":
				h.selected = max(0, h.selected-1)
				return h, nil
			case "down":
				h.selected = min(max(0, len(h.items())-1), h.selected+1)
				return h, nil
			}
		} else {
			switch key {
			case "esc", "q":
				return h, h.back()
			case "/":
				if h.topic == "" {
					h.filtering = true
					return h, h.query.Focus()
				}
			case "enter":
				h.open()
			case "up", "k":
				if h.topic != "" {
					h.scroll = max(0, h.scroll-1)
				} else {
					h.selected = max(0, h.selected-1)
				}
			case "down", "j":
				if h.topic != "" {
					h.scroll++
				} else {
					h.selected = min(max(0, len(h.items())-1), h.selected+1)
				}
			case "pgdown":
				h.scroll += max(1, h.height-6)
			case "pgup":
				h.scroll = max(0, h.scroll-max(1, h.height-6))
			}
			return h, nil
		}
	}
	if h.filtering {
		before := h.query.Value()
		var cmd tea.Cmd
		h.query, cmd = h.query.Update(msg)
		if before != h.query.Value() {
			h.selected = 0
		}
		return h, cmd
	}
	return h, nil
}
func (h *HelpBrowser) View() tea.View {
	t := styles(h.dark)
	lines := []string{t.Title.Render("Concepts · learn how your job will run"), ""}
	if topic, ok := concept.Find(h.topic); ok {
		body := strings.Split(ansi.Hardwrap(safe(topic.Body), max(1, h.width-2), true), "\n")
		start := min(h.scroll, max(0, len(body)-1))
		lines = append(lines, body[start:min(len(body), start+max(1, h.height-5))]...)
	} else {
		lines = append(lines, h.query.View())
		items := h.items()
		start := h.listStart()
		for i := start; i < min(len(items), start+h.listRows()); i++ {
			item := items[i]
			line := item.Title
			if i == h.selected {
				line = t.Selected.Render(pad("› "+line, h.width))
			} else {
				line = "  " + line
			}
			lines = append(lines, line, "  "+t.Muted.Render(item.Summary), "")
		}
		if len(h.items()) == 0 {
			lines = append(lines, "No matching concepts.")
		}
	}
	lines = lines[:min(len(lines), max(0, h.height-2))]
	for len(lines) < h.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, t.Accent.Render("[ Back Esc ]  / search  ·  Enter open  ·  ↑↓ scroll"), "")
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), h.width, h.height))
	v.AltScreen = true
	if h.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

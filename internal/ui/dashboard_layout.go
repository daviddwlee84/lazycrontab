package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type dashboardLayout struct {
	body  rect
	panes [3]rect
}

func (m *dashboard) layout() dashboardLayout {
	l := dashboardLayout{body: rect{0, 2, m.width, max(1, m.height-5)}}
	if m.width >= 120 {
		left := 24
		middle := (m.width - left) / 2
		l.panes = [3]rect{{0, 2, left, l.body.h}, {left, 2, middle, l.body.h}, {left + middle, 2, m.width - left - middle, l.body.h}}
	} else if m.width >= 80 && m.focus != 0 {
		middle := m.width / 2
		l.panes[1] = rect{0, 2, middle, l.body.h}
		l.panes[2] = rect{middle, 2, m.width - middle, l.body.h}
	} else {
		l.panes[max(0, min(2, m.focus))] = l.body
	}
	return l
}
func (m *dashboard) visibleRows() int { return max(1, m.layout().body.h-3) }
func (m *dashboard) scopeStart() int  { return max(0, m.scope-m.visibleRows()+1) }
func (m *dashboard) agendaStart() int { return max(0, m.agendaSelected-max(1, m.layout().body.h-2)+1) }
func (m *dashboard) weekStart() int   { return max(0, m.weekHour-max(1, m.layout().body.h-3)+1) }
func (m *dashboard) isDark() bool {
	switch m.service.Config.Theme {
	case "dark":
		return true
	case "light":
		return false
	}
	return m.dark
}
func (m *dashboard) footerButtons() [][2]string {
	if m.modal != "" {
		return [][2]string{{"close", "Back Esc"}}
	}
	if m.help {
		return [][2]string{{"concepts", "Concepts F1"}, {"close", "Back Esc"}}
	}
	if m.palette {
		return [][2]string{{"choose", "Choose Enter"}, {"close", "Back Esc"}}
	}
	if m.view == "week" {
		if m.agendaMode {
			return [][2]string{{"inspect", "Inspect Enter"}, {"grid", "Grid Esc"}, {"help", "Help ?"}}
		}
		return [][2]string{{"prev-week", "Previous ["}, {"next-week", "Next ]"}, {"agenda", "Agenda Enter"}, {"help", "Help ?"}}
	}
	var out [][2]string
	for _, id := range []string{"add", "edit", "run", "logs", "authenticate"} {
		for _, a := range m.actions {
			if a.ID == id && m.available(a) {
				if id == "authenticate" {
					h, s := m.scopeTarget()
					if m.snapshots[h+"/"+s].Error == "" {
						continue
					}
				}
				out = append(out, [2]string{a.ID, a.Label + " " + a.Key})
			}
		}
	}
	out = append(out, [2]string{"actions", "Actions :"}, [2]string{"help", "Help ?"})
	return out
}
func (m *dashboard) footerHits() []hitRegion {
	if m.height < 6 {
		return nil
	}
	var hits []hitRegion
	x := 0
	for _, b := range m.footerButtons() {
		w := ansi.StringWidth(b[1]) + 4
		if x+w > m.width {
			break
		}
		hits = append(hits, hitRegion{b[0], rect{x, max(0, m.height-2), w, 1}})
		x += w + 1
	}
	return hits
}
func (m *dashboard) dashboardHits() (hits []hitRegion) {
	defer func() {
		visible := hits[:0]
		body := m.layout().body
		for _, h := range hits {
			if h.rect.x < 0 || h.rect.y < 0 || h.rect.x+h.rect.w > m.width || h.rect.y+h.rect.h > m.height {
				continue
			}
			bodyHit := strings.HasPrefix(h.id, "palette:") || strings.HasPrefix(h.id, "agenda-row:") || strings.HasPrefix(h.id, "cell:") || strings.HasPrefix(h.id, "job:") || strings.HasPrefix(h.id, "scope:") || strings.HasPrefix(h.id, "pane:")
			if bodyHit && (h.rect.y < body.y || h.rect.y+h.rect.h > body.y+body.h) {
				continue
			}
			visible = append(visible, h)
		}
		hits = visible
	}()
	hits = m.footerHits()
	if !m.help && !m.palette && m.modal == "" {
		hits = append(hits, m.headerTabs()...)
	}
	l := m.layout()
	if m.help || m.modal != "" {
		return hits
	}
	if m.palette {
		items := m.paletteActions()
		start := max(0, m.paletteRow-l.body.h+3)
		for i := start; i < min(len(items), start+max(1, l.body.h-2)); i++ {
			hits = append(hits, hitRegion{fmt.Sprintf("palette:%d", i), rect{0, 4 + i - start, m.width, 1}})
		}
		return hits
	}
	if m.childShowing() || m.view == "playground" {
		return hits
	}
	if m.view == "week" {
		if m.agendaMode {
			start := m.agendaStart()
			for i := start; i < min(len(m.agenda), start+max(1, l.body.h-2)); i++ {
				hits = append(hits, hitRegion{fmt.Sprintf("agenda-row:%d", i), rect{0, 4 + i - start, m.width, 1}})
			}
		} else {
			start := m.weekStart()
			for h := start; h < min(24, start+max(1, l.body.h-3)); h++ {
				if m.width < 70 {
					hits = append(hits, hitRegion{fmt.Sprintf("cell:%d:%d", m.weekDay, h), rect{0, 5 + h - start, m.width, 1}})
				} else {
					for d := 0; d < 7; d++ {
						hits = append(hits, hitRegion{fmt.Sprintf("cell:%d:%d", d, h), rect{6 + d*8, 5 + h - start, 8, 1}})
					}
				}
			}
		}
		return hits
	}
	for role, r := range l.panes {
		if r.w == 0 {
			continue
		}
		if role == 0 {
			start := m.scopeStart()
			for i := start; i < min(len(m.targets)+1, start+m.visibleRows()); i++ {
				hits = append(hits, hitRegion{fmt.Sprintf("scope:%d", i), rect{r.x + 1, 4 + i - start, max(0, r.w-2), 1}})
			}
		}
		if role == 1 {
			start := m.rowStart()
			for i := start; i < min(len(m.rows()), start+m.visibleRows()); i++ {
				hits = append(hits, hitRegion{fmt.Sprintf("job:%d", i), rect{r.x + 1, 4 + i - start, max(0, r.w-2), 1}})
			}
		}
		hits = append(hits, hitRegion{fmt.Sprintf("pane:%d", role), r})
	}
	return hits
}
func (m *dashboard) handleMouse(msg tea.Msg) tea.Cmd {
	if !m.mouse {
		return nil
	}
	switch v := msg.(type) {
	case tea.MouseClickMsg:
		mouse := v.Mouse()
		id := hitAt(m.dashboardHits(), mouse.X, mouse.Y)
		m.mousePress = ""
		var i, d, h int
		switch {
		case strings.HasPrefix(id, "scope:"):
			fmt.Sscanf(id, "scope:%d", &i)
			m.focus = 0
			m.changeScope(i)
		case strings.HasPrefix(id, "job:"):
			fmt.Sscanf(id, "job:%d", &i)
			m.focus = 1
			m.selected = i
			if j, ok := m.current(); ok {
				m.selectedKey = j.Key()
			}
		case strings.HasPrefix(id, "pane:"):
			fmt.Sscanf(id, "pane:%d", &i)
			m.focus = i
		case strings.HasPrefix(id, "palette:"):
			fmt.Sscanf(id, "palette:%d", &i)
			m.paletteRow = i
		case strings.HasPrefix(id, "agenda-row:"):
			fmt.Sscanf(id, "agenda-row:%d", &i)
			m.agendaSelected = i
		case strings.HasPrefix(id, "cell:"):
			fmt.Sscanf(id, "cell:%d:%d", &d, &h)
			m.weekDay = d
			m.weekHour = h
		default:
			m.mousePress = id
		}
	case tea.MouseReleaseMsg:
		mouse := v.Mouse()
		id := hitAt(m.dashboardHits(), mouse.X, mouse.Y)
		pressed := m.mousePress
		m.mousePress = ""
		if id != "" && id == pressed {
			return m.activateHit(id)
		}
	case tea.MouseWheelMsg:
		m.mousePress = ""
		delta := 1
		if strings.Contains(v.String(), "up") {
			delta = -1
		}
		if m.help {
			m.helpScroll = max(0, m.helpScroll+delta*3)
			return nil
		}
		if m.modal != "" {
			m.modalScroll = max(0, m.modalScroll+delta*3)
			return nil
		}
		if m.palette {
			m.paletteRow = max(0, min(max(0, len(m.paletteActions())-1), m.paletteRow+delta))
			return nil
		}
		if m.view == "week" {
			if m.agendaMode {
				m.agendaSelected = max(0, min(max(0, len(m.agenda)-1), m.agendaSelected+delta))
			} else {
				m.weekHour = max(0, min(23, m.weekHour+delta))
			}
			return nil
		}
		mouse := v.Mouse()
		for role, r := range m.layout().panes {
			if r.contains(mouse.X, mouse.Y) {
				switch role {
				case 0:
					m.changeScope(m.scope + delta)
				case 1:
					m.selectRow(delta)
				case 2:
					m.detailScroll = max(0, m.detailScroll+delta*3)
				}
				return nil
			}
		}
	}
	return nil
}
func (m *dashboard) header() string {
	t := styles(m.isDark())
	prefix := ""
	if m.width >= 65 {
		prefix = t.Title.Render("lazycrontab") + "  │ "
	}
	var tabs []string
	for _, tab := range m.tabEntries() {
		label := " " + tab[1] + " "
		if m.view == tab[0] {
			label = t.Selected.Render(label)
		} else {
			label = t.Muted.Render(label)
		}
		tabs = append(tabs, label)
	}
	return clip(prefix+strings.Join(tabs, " "), m.width)
}
func (m *dashboard) jobBody() string {
	l := m.layout()
	var panes []string
	for role, r := range l.panes {
		if r.w == 0 {
			continue
		}
		var title string
		var lines []string
		switch role {
		case 0:
			all := m.scopeLines()
			title = "Hosts / sources"
			start := m.scopeStart()
			lines = append([]string{""}, all[2+start:min(len(all), 2+start+m.visibleRows())]...)
		case 1:
			all := m.jobLines(max(1, r.w-2))
			title = all[0]
			lines = all[1:]
		case 2:
			all := m.detailLines(max(1, r.w-2))
			title = "Job details"
			lines = all[1:]
		}
		panes = append(panes, bordered(title, lines, r.w, r.h, role == m.focus, m.isDark()))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, panes...)
}
func (m *dashboard) helpLines() []string {
	lines := []string{"Contextual actions · F1 concepts", "↑↓ / j k select · Tab focus · Alt+1/2/3 switch views", "Typing owns letters and digits · Esc leaves the current interaction", "/ search · : actions · m mouse capture · q quit", ""}
	for _, a := range m.actions {
		if m.available(a) {
			lines = append(lines, fmt.Sprintf("%-10s %s", a.Key, a.Label))
		}
	}
	return lines
}

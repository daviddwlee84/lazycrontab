package ui

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *dashboard) childShowing() bool { return m.child != nil && m.childView == m.view }
func (m *dashboard) openWorkflow(action, expression string) tea.Cmd {
	m.mousePress = ""
	m.help = false
	m.palette = false
	if m.child != nil || m.childPending {
		m.view = m.childView
		m.status = "Resume the open draft, or cancel it before starting another."
		return nil
	}
	if m.factory == nil {
		if action == "guide" {
			m.child = embedded(NewHelpBrowser("", m.mouse))
			m.childView = m.view
			m.child.Update(tea.WindowSizeMsg{Width: m.width, Height: max(1, m.height-2)})
			return m.child.Init()
		}
		return nil
	}
	h, s := m.scopeTarget()
	if action == "add" && expression != "" && m.view == "playground" && m.playgroundHost != "" {
		h, s = m.playgroundHost, m.playgroundSource
	}
	job, _ := m.current()
	request := WorkflowRequest{Action: action, Host: h, Source: s, JobID: job.ID, Schedule: expression}
	if action == "add" {
		request.JobID = ""
	}
	m.childGeneration++
	g := m.childGeneration
	m.childPending = true
	m.childView = m.view
	ctx, cancel := context.WithCancel(m.ctx)
	m.childCancel = cancel
	factory := m.factory
	m.status = "Opening " + action + "…"
	return func() tea.Msg { model, err := factory(ctx, request); return workflowReady{g, model, err} }
}
func (m *dashboard) childInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg = offsetMouse(msg, -2)
	var cmd tea.Cmd
	m.child, cmd = m.child.Update(msg)
	return m, cmd
}
func offsetMouse(msg tea.Msg, dy int) tea.Msg {
	switch v := msg.(type) {
	case tea.MouseClickMsg:
		v.Y += dy
		return v
	case tea.MouseReleaseMsg:
		v.Y += dy
		return v
	case tea.MouseWheelMsg:
		v.Y += dy
		return v
	case tea.MouseMotionMsg:
		v.Y += dy
		return v
	}
	return msg
}

// routeSurface leaves asynchronous snapshot messages with the dashboard while
// the focused workflow owns keyboard, paste and mouse input.
func (m *dashboard) routeSurface(msg tea.Msg) (bool, tea.Cmd) {
	switch v := msg.(type) {
	case workflowReady:
		if v.generation != m.childGeneration {
			return true, nil
		}
		m.childPending = false
		if v.err != nil {
			m.status = v.err.Error()
			m.modal = v.err.Error()
			m.modalScroll = 0
			if m.childCancel != nil {
				m.childCancel()
			}
			return true, nil
		}
		m.child = embedded(v.model)
		if mouse, ok := m.child.(interface{ SetMouse(bool) }); ok {
			mouse.SetMouse(m.mouse)
		}
		_, cmd := m.child.Update(tea.WindowSizeMsg{Width: m.width, Height: max(1, m.height-2)})
		return true, tea.Batch(cmd, m.child.Init())
	case WorkflowDoneMsg:
		if m.child == nil {
			return true, nil
		}
		if v.Owner != nil && v.Owner != m.child {
			return true, nil
		}
		m.child = nil
		m.childPending = false
		if m.childCancel != nil {
			m.childCancel()
			m.childCancel = nil
		}
		m.status = v.Message
		if errors.Is(v.Err, ErrCancelled) && !v.Changed {
			m.status = "Draft closed"
		} else if v.Err != nil {
			m.status = v.Err.Error()
		}
		if v.Changed {
			_, cmd := m.Update(handoffMsg{})
			if v.Message != "" {
				m.status = v.Message
			}
			if v.Err != nil && !errors.Is(v.Err, ErrCancelled) {
				m.status = v.Err.Error()
			}
			return true, cmd
		}
		return true, nil
	case ScheduleUseMsg:
		if m.childShowing() {
			_, cmd := m.child.Update(v)
			return true, cmd
		}
		if m.view == "playground" {
			return true, m.openWorkflow("add", v.Expression)
		}
	case tea.WindowSizeMsg:
		m.mousePress = ""
		if m.child != nil {
			m.child.Update(tea.WindowSizeMsg{Width: v.Width, Height: max(1, v.Height-2)})
		}
		if m.playground != nil {
			m.playground.SetSize(max(1, v.Width), max(1, v.Height-2))
		}
	case tea.BackgroundColorMsg:
		if m.child != nil {
			m.child.Update(v)
		}
		if m.playground != nil {
			m.playground.Update(v)
		}
	case tea.KeyPressMsg:
		key := v.String()
		m.mousePress = ""
		if key != "g" {
			m.prefixG = false
		}
		if m.childPending && key == "esc" {
			m.childGeneration++
			m.childPending = false
			if m.childCancel != nil {
				m.childCancel()
			}
			m.status = "Opening cancelled"
			return true, nil
		}
		if !m.help && !m.palette && m.modal == "" {
			if key == "alt+1" || key == "alt+2" || key == "alt+3" {
				id := map[string]string{"alt+1": "jobs", "alt+2": "week", "alt+3": "playground"}[key]
				return true, m.act(id)
			}
			if m.childShowing() {
				_, cmd := m.childInput(msg)
				return true, cmd
			}
			if m.view == "playground" && m.playground != nil {
				if !m.playground.Editing() {
					for _, a := range m.actions {
						if (a.ID == "jobs" || a.ID == "week" || a.ID == "playground") && key == a.Key {
							return true, m.act(a.ID)
						}
					}
					if key == "q" || key == "ctrl+c" {
						return true, tea.Quit
					}
					if key == "?" {
						m.help = true
						return true, nil
					}
					if key == "m" {
						m.mouse = !m.mouse
						m.playground.SetMouse(m.mouse)
						return true, nil
					}
				}
				_, cmd := m.playground.Update(msg)
				return true, cmd
			}
		}
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		if !m.mouse {
			return true, nil
		}
		x, y := 0, 0
		switch v := msg.(type) {
		case tea.MouseClickMsg:
			x, y = v.X, v.Y
		case tea.MouseReleaseMsg:
			x, y = v.X, v.Y
		case tea.MouseWheelMsg:
			x, y = v.X, v.Y
		case tea.MouseMotionMsg:
			x, y = v.X, v.Y
		}
		_ = x
		if y < 2 {
			if m.childShowing() {
				m.child.Update(offsetMouse(msg, -2))
			}
			if m.playground != nil && m.view == "playground" {
				m.playground.Update(offsetMouse(msg, -2))
			}
		}
		if !m.help && !m.palette && m.modal == "" && y >= 2 {
			if m.childShowing() {
				_, cmd := m.childInput(msg)
				return true, cmd
			}
			if m.view == "playground" && m.playground != nil {
				_, cmd := m.playground.Update(offsetMouse(msg, -2))
				return true, cmd
			}
		}
	case tea.PasteMsg:
		if m.childShowing() {
			_, cmd := m.child.Update(v)
			return true, cmd
		}
		if m.view == "playground" && m.playground != nil && !m.help && !m.palette && m.modal == "" {
			_, cmd := m.playground.Update(v)
			return true, cmd
		}
	}
	return false, nil
}
func (m *dashboard) surfaceMessages(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	// Internal async messages carry their originating model identity. Both the
	// suspended Playground and current workflow may have a pending preview.
	if m.child != nil {
		var cmd tea.Cmd
		m.child, cmd = m.child.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.playground != nil {
		_, cmd := m.playground.Update(msg)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}
func (m *dashboard) closeOverlay() {
	m.help = false
	m.modal = ""
	m.helpScroll = 0
	m.modalScroll = 0
	m.mousePress = ""
	if m.palette {
		m.palette = false
		m.filter.SetValue(m.savedFilter)
		m.filter.Blur()
	}
}
func (m *dashboard) headerTabs() []hitRegion {
	x := 0
	if m.width >= 65 {
		x = 14
	}
	var hits []hitRegion
	for _, tab := range m.tabEntries() {
		w := ansi.StringWidth(tab[1]) + 2
		if x+w <= m.width {
			hits = append(hits, hitRegion{"tab:" + tab[0], rect{x, 0, w, 1}})
		}
		x += w + 1
	}
	return hits
}
func (m *dashboard) tabEntries() [][2]string {
	var out [][2]string
	for _, tab := range [][2]string{{"jobs", "Jobs"}, {"week", "Week"}, {"playground", "Playground"}} {
		for _, a := range m.actions {
			if a.ID == tab[0] {
				out = append(out, [2]string{tab[0], tab[1] + " [" + a.Key + "]"})
				break
			}
		}
	}
	return out
}
func (m *dashboard) activateHit(id string) tea.Cmd {
	if strings.HasPrefix(id, "tab:") {
		return m.act(strings.TrimPrefix(id, "tab:"))
	}
	switch id {
	case "close":
		m.closeOverlay()
	case "concepts":
		return m.act("guide")
	case "choose":
		items := m.paletteActions()
		if len(items) > 0 {
			a := items[min(m.paletteRow, len(items)-1)]
			m.closeOverlay()
			return m.act(a.ID)
		}
	case "agenda":
		m.agendaOffset = 0
		return m.loadAgenda()
	case "grid":
		m.agendaMode = false
	case "prev-week":
		m.weekDate = m.weekDate.AddDate(0, 0, -7)
		m.agendaMode = false
		return m.buildWeek()
	case "next-week":
		m.weekDate = m.weekDate.AddDate(0, 0, 7)
		m.agendaMode = false
		return m.buildWeek()
	case "inspect":
		return m.inspectAgenda()
	case "quit":
		return tea.Quit
	case "search":
		m.filtering = true
		return m.filter.Focus()
	case "actions":
		m.palette = true
		m.paletteRow = 0
		m.savedFilter = m.filter.Value()
		m.filter.SetValue("")
		return m.filter.Focus()
	case "help":
		m.help = true
		m.helpScroll = 0
	default:
		return m.act(id)
	}
	return nil
}
func (m *dashboard) inspectAgenda() tea.Cmd {
	if len(m.agenda) > 0 {
		a := m.agenda[min(m.agendaSelected, len(m.agenda)-1)]
		m.view = "jobs"
		m.selectedKey = a.Host + "/" + a.Source + "/" + a.JobID
		m.clamp()
		m.focus = 2
	}
	return nil
}

package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

type action struct {
	ID, Key, Label string
	Row            bool
}

var defaultActions = []action{
	{"timezone", "z", "Display timezone", false},
	{"add", "n", "Add job", false}, {"edit", "e", "Edit job", true}, {"remove", "d", "Remove job", true}, {"toggle", "x", "Enable / disable", true}, {"run", "r", "Run now", true}, {"script", "E", "Edit script", true}, {"logs", "L", "Read logs", true},
	{"refresh", "ctrl+r", "Refresh", false}, {"jobs", "1", "Jobs", false}, {"week", "2", "Week overview", false}, {"playground", "3", "Playground", false}, {"host-add", "a", "Add host", false}, {"source-add", "s", "Add source", false}, {"authenticate", "A", "Authenticate SSH", false}, {"queue", "Q", "Open lazypueue", false}, {"config", "C", "Edit config", false}, {"reload", "R", "Reload source", false},
	{"guide", "f1", "Concepts and guides", false},
	{"view-source", "v", "View raw source", false}, {"edit-source", "V", "Edit raw source", false},
}

func Actions(keys map[string]string) ([]action, error) {
	a := append([]action(nil), defaultActions...)
	seen := map[string]string{}
	reserved := map[string]bool{"q": true, "esc": true, "?": true, "/": true, ":": true, "tab": true, "shift+tab": true, "j": true, "k": true, "h": true, "l": true, "g": true, "G": true, "up": true, "down": true, "left": true, "right": true, "enter": true, "ctrl+c": true, "m": true}
	for _, key := range []string{"alt+1", "alt+2", "alt+3"} {
		reserved[key] = true
	}
	for i := range a {
		if key, ok := keys[a[i].ID]; ok {
			if key == "" || reserved[key] {
				return nil, fmt.Errorf("key %q is reserved for navigation", key)
			}
			a[i].Key = key
		}
		if other := seen[a[i].Key]; other != "" {
			return nil, fmt.Errorf("key %q conflicts between %s and %s", a[i].Key, other, a[i].ID)
		}
		seen[a[i].Key] = a[i].ID
	}
	for id := range keys {
		found := false
		for _, a := range a {
			if a.ID == id {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown action %q", id)
		}
	}
	return a, nil
}

type snapshotMsg struct {
	key        string
	generation int
	snapshot   service.Snapshot
	err        error
}
type refreshTick time.Time
type handoffMsg struct{ err error }
type weekMsg struct {
	generation int
	overview   service.Overview
	err        error
}
type agendaMsg struct {
	generation int
	rows       []service.Occurrence
	more       bool
	err        error
}
type logsMsg struct {
	key, content string
	err          error
}
type queueAvailableMsg bool
type workflowReady struct {
	generation int
	model      tea.Model
	err        error
}
type viewContext struct {
	key, filter      string
	selected, scroll int
}
type dashboard struct {
	ctx                                                 context.Context
	cancel                                              context.CancelFunc
	service                                             *service.Service
	configPath                                          string
	actions                                             []action
	targets                                             [][2]string
	snapshots                                           map[string]service.Snapshot
	generations                                         map[string]int
	pending                                             map[string]bool
	sem                                                 chan struct{}
	width, height, focus, scope, selected, detailScroll int
	selectedKey                                         string
	filter                                              textinput.Model
	filtering                                           bool
	palette                                             bool
	paletteRow                                          int
	help                                                bool
	modal                                               string
	modalScroll                                         int
	busy                                                bool
	status                                              string
	prefixG                                             bool
	mouse                                               bool
	view                                                string
	weekDate                                            time.Time
	week                                                service.Overview
	weekGen, weekDay, weekHour                          int
	weekPending                                         bool
	weekCancel                                          context.CancelFunc
	agenda                                              []service.Occurrence
	agendaMore                                          bool
	agendaOffset, agendaSelected, agendaGen             int
	agendaPending                                       bool
	agendaMode                                          bool
	contexts                                            map[string]viewContext
	savedFilter                                         string
	queueAvailable                                      bool
	dark                                                bool
	timezoneEditing                                     bool
	displayTimezone                                     string
	factory                                             WorkflowFactory
	child                                               tea.Model
	childView                                           string
	childCancel                                         context.CancelFunc
	childGeneration                                     int
	childPending                                        bool
	playground                                          *ScheduleEditor
	playgroundHost, playgroundSource                    string
	mousePress                                          string
	helpScroll                                          int
}

func Dashboard(ctx context.Context, s *service.Service, host, source, path string, factories ...WorkflowFactory) error {
	a, e := Actions(s.Config.Keys)
	if e != nil {
		return e
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	f := textinput.New()
	f.Prompt = "/ "
	f.SetVirtualCursor(true)
	m := &dashboard{ctx: child, cancel: cancel, service: s, configPath: path, actions: a, targets: s.Targets("all", "all"), snapshots: map[string]service.Snapshot{}, generations: map[string]int{}, pending: map[string]bool{}, sem: make(chan struct{}, s.Config.MaxParallel), width: 100, height: 30, focus: 1, filter: f, mouse: s.Config.Mouse, view: "jobs", weekDate: time.Now()}
	if len(factories) > 0 {
		m.factory = factories[0]
	}
	for i, t := range m.targets {
		if t[0] == host && t[1] == source {
			m.scope = i + 1
		}
	}
	if host == "all" {
		m.scope = 0
	}
	_, e = tea.NewProgram(m, tea.WithContext(child), tea.WithoutSignalHandler()).Run()
	return e
}
func (m *dashboard) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.tick(), tea.RequestBackgroundColor, func() tea.Msg { _, e := exec.LookPath("lazypueue"); return queueAvailableMsg(e == nil) })
}
func (m *dashboard) scopeKey() string {
	if m.scope > 0 && m.scope <= len(m.targets) {
		return m.targets[m.scope-1][0] + "/" + m.targets[m.scope-1][1]
	}
	return "all"
}
func (m *dashboard) changeScope(index int) {
	if m.contexts == nil {
		m.contexts = map[string]viewContext{}
	}
	m.contexts[m.scopeKey()] = viewContext{m.selectedKey, m.filter.Value(), m.selected, m.detailScroll}
	m.scope = max(0, min(len(m.targets), index))
	c := m.contexts[m.scopeKey()]
	m.selectedKey = c.key
	m.selected = c.selected
	m.detailScroll = c.scroll
	m.filter.SetValue(c.filter)
	m.clamp()
}
func (m *dashboard) tick() tea.Cmd {
	return tea.Tick(time.Duration(m.service.Config.RefreshSeconds)*time.Second, func(t time.Time) tea.Msg { return refreshTick(t) })
}
func (m *dashboard) refresh() tea.Cmd {
	svc, ctx, sem := m.service, m.ctx, m.sem
	cmds := []tea.Cmd{}
	for _, t := range m.targets {
		key := t[0] + "/" + t[1]
		if m.pending[key] {
			continue
		}
		m.generations[key]++
		generation := m.generations[key]
		m.pending[key] = true
		cmds = append(cmds, func() tea.Msg {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return snapshotMsg{key, generation, service.Snapshot{}, ctx.Err()}
			}
			snap, e := svc.Snapshot(ctx, t[0], t[1])
			return snapshotMsg{key, generation, snap, e}
		})
	}
	return tea.Batch(cmds...)
}
func (m *dashboard) visibleSnapshots() []service.Snapshot {
	out := []service.Snapshot{}
	for i, t := range m.targets {
		if m.scope != 0 && m.scope != i+1 {
			continue
		}
		if snap, ok := m.snapshots[t[0]+"/"+t[1]]; ok {
			q := m.query()
			if q != "" {
				entries := []service.Entry{}
				for _, j := range snap.Entries {
					if strings.Contains(strings.ToLower(j.Name+" "+j.Command+" "+j.Remark+" "+j.Host), q) {
						entries = append(entries, j)
					}
				}
				snap.Entries = entries
			}
			out = append(out, snap)
		}
	}
	return out
}
func (m *dashboard) query() string {
	if m.palette || m.timezoneEditing {
		return strings.ToLower(m.savedFilter)
	}
	return strings.ToLower(m.filter.Value())
}
func (m *dashboard) rows() []service.Entry {
	out := []service.Entry{}
	query := m.query()
	for _, snap := range m.visibleSnapshots() {
		for _, j := range snap.Entries {
			if query == "" || strings.Contains(strings.ToLower(j.Name+" "+j.Command+" "+j.Remark+" "+j.Host), query) {
				out = append(out, j)
			}
		}
	}
	return out
}
func (m *dashboard) current() (service.Entry, bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return service.Entry{}, false
	}
	return rows[min(m.selected, len(rows)-1)], true
}
func (m *dashboard) clamp() {
	rows := m.rows()
	for i, j := range rows {
		if j.Key() == m.selectedKey {
			m.selected = i
			return
		}
	}
	m.selected = min(m.selected, max(0, len(rows)-1))
	if len(rows) > 0 {
		m.selectedKey = rows[m.selected].Key()
	} else {
		m.selectedKey = ""
	}
}
func (m *dashboard) selectRow(delta int) {
	rows := m.rows()
	m.selected = max(0, min(len(rows)-1, m.selected+delta))
	if len(rows) > 0 {
		m.selectedKey = rows[m.selected].Key()
	}
	m.detailScroll = 0
}
func (m *dashboard) scopeTarget() (string, string) {
	if m.scope > 0 && m.scope <= len(m.targets) {
		t := m.targets[m.scope-1]
		return t[0], t[1]
	}
	if j, ok := m.current(); ok {
		return j.Host, j.Source
	}
	return "local", "user"
}
func (m *dashboard) handoff(args ...string) tea.Cmd {
	host, source := m.scopeTarget()
	return m.handoffTarget(host, source, args...)
}
func (m *dashboard) handoffTarget(host, source string, args ...string) tea.Cmd {
	exe, e := os.Executable()
	if e != nil {
		m.status = e.Error()
		return nil
	}
	flags := []string{"--host", host, "--source", source}
	if m.configPath != "" {
		flags = append(flags, "--config", m.configPath)
	}
	args = append(flags, args...)
	m.busy = true
	// Hold early CLI/editor/auth failures long enough to read them. "$@" keeps
	// every argument intact; job content is never interpolated into this script.
	childArgs := []string{"-c", `"$@"; result=$?; if [ "$result" -ne 0 ] && [ "$result" -ne 130 ]; then printf '\nOperation failed. Press Enter to return.'; IFS= read -r ignored; fi; exit "$result"`, "lazycrontab-handoff", exe}
	childArgs = append(childArgs, args...)
	return tea.ExecProcess(exec.Command("sh", childArgs...), func(e error) tea.Msg { return handoffMsg{e} })
}
func (m *dashboard) available(a action) bool {
	j, ok := m.current()
	if a.Row && (!ok || j.ReadOnly && a.ID != "logs") {
		return false
	}
	host, source := m.scopeTarget()
	h, err := m.service.Config.Host(host)
	if err != nil {
		return false
	}
	src, err := m.service.Config.Source(host, source)
	if err != nil {
		return false
	}
	if a.ID == "view-source" || a.ID == "edit-source" {
		if m.scope == 0 {
			if _, ok := m.current(); !ok {
				return false
			}
		}
		if a.ID == "edit-source" && (src.ReadOnly || src.Kind == "system") {
			return false
		}
	}
	if a.ID == "queue" && (!m.queueAvailable || host != "local" && h.LazypueueConnection == "") {
		return false
	}
	if a.ID == "authenticate" && h.SSH == "" {
		return false
	}
	if a.ID == "reload" && len(src.Reload) == 0 {
		return false
	}
	if a.ID == "add" && m.scope != 0 && (src.ReadOnly || src.Kind == "system") {
		return false
	}
	return !m.busy
}
func (m *dashboard) act(id string) tea.Cmd {
	if m.busy {
		return nil
	}
	if id == "jobs" || id == "week" || id == "playground" {
		if m.child != nil {
			m.child.Update(tea.MouseReleaseMsg{X: -1, Y: -1})
		}
		if m.playground != nil {
			m.playground.SetSize(m.width, max(1, m.height-2))
		}
	}
	for _, a := range m.actions {
		if a.ID == id && !m.available(a) {
			m.status = "This action is unavailable for the selected source"
			return nil
		}
	}
	j, _ := m.current()
	m.palette = false
	m.filter.Blur()
	switch id {
	case "view-source":
		return m.openWorkflow("view-source", "")
	case "edit-source":
		if m.child != nil || m.childPending {
			m.view = m.childView
			m.status = "Close the current draft before editing a whole source."
			return nil
		}
		return m.handoff("sources", "edit-raw")
	case "add":
		if m.factory != nil {
			return m.openWorkflow("add", "")
		}
		return m.handoff("add")
	case "edit":
		if m.factory != nil {
			return m.openWorkflow("edit", "")
		}
		return m.handoff("edit", j.ID)
	case "remove":
		if m.factory != nil {
			return m.openWorkflow("remove", "")
		}
		return m.handoff("remove", j.ID)
	case "toggle":
		if m.factory != nil {
			return m.openWorkflow("toggle", "")
		}
		op := "enable"
		if j.Enabled {
			op = "disable"
		}
		return m.handoff(op, j.ID)
	case "run":
		if m.factory != nil {
			return m.openWorkflow("run", "")
		}
		return m.handoff("run", j.ID, "--interactive")
	case "script":
		return m.handoff("script", "edit", j.ID)
	case "logs":
		m.modal = "Loading logs…"
		key := j.Key()
		svc, ctx := m.service, m.ctx
		return func() tea.Msg { content, e := svc.Logs(ctx, j, "", 200); return logsMsg{key, content, e} }
	case "refresh":
		return m.refresh()
	case "jobs":
		m.view = "jobs"
		m.mousePress = ""
	case "week":
		m.view = "week"
		m.mousePress = ""
		return m.buildWeek()
	case "playground":
		m.view = "playground"
		m.mousePress = ""
		if m.playground == nil {
			h, src := m.scopeTarget()
			m.playgroundHost, m.playgroundSource = h, src
			dialect := schedule.System
			if source, err := m.service.Config.Source(h, src); err == nil {
				if source.Dialect != "" {
					dialect = schedule.Dialect(source.Dialect)
				} else if source.Kind == "file" {
					dialect = schedule.Supercronic
				}
			}
			zone := m.snapshots[h+"/"+src].Timezone
			if zone == "" {
				source, _ := m.service.Config.Source(h, src)
				host, _ := m.service.Config.Host(h)
				zone = source.Timezone
				if zone == "" {
					zone = host.Timezone
				}
				if zone == "" && h == "local" {
					zone = time.Local.String()
				}
			}
			m.playground = NewScheduleEditor(m.ctx, ScheduleEditorOptions{Expression: "0 9 * * 1-5", Dialect: dialect, Timezone: zone, Locale: m.service.Config.Locale, Mouse: m.mouse, Theme: m.service.Config.Theme, Target: h + "/" + src})
			m.playground.SetSize(m.width, max(1, m.height-2))
			return m.playground.Init()
		}
		m.playground.Blur()
	case "host-add":
		if m.factory != nil {
			return m.openWorkflow("host-add", "")
		}
		return m.handoff("hosts", "add")
	case "guide":
		return m.openWorkflow("guide", "")
	case "source-add":
		if m.factory != nil {
			return m.openWorkflow("source-add", "")
		}
		return m.handoff("sources", "add")
	case "authenticate":
		return m.handoff("hosts", "authenticate")
	case "queue":
		return m.handoff("queue")
	case "config":
		return m.handoff("config", "edit")
	case "reload":
		return m.handoff("sources", "reload")
	case "timezone":
		m.savedFilter = m.filter.Value()
		m.timezoneEditing = true
		zone := m.displayTimezone
		if zone == "" {
			zone = m.service.Config.Timezone
		}
		m.filter.SetValue(zone)
		return m.filter.Focus()
	}
	return nil
}
func (m *dashboard) buildWeek() tea.Cmd {
	if m.weekCancel != nil {
		m.weekCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.weekCancel = cancel
	m.weekGen++
	generation := m.weekGen
	m.weekPending = true
	snaps := m.visibleSnapshots()
	date := m.weekDate
	zone := m.displayTimezone
	if zone == "" {
		zone = m.service.Config.Timezone
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		m.status = e.Error()
		return nil
	}
	return func() tea.Msg {
		o, e := service.BuildOverview(ctx, snaps, date, loc, 200)
		return weekMsg{generation, o, e}
	}
}
func (m *dashboard) cellInterval() (time.Time, time.Time) {
	start := m.week.Start.AddDate(0, 0, m.weekDay)
	end := start.AddDate(0, 0, 1)
	var first, last time.Time
	for t := start; t.Before(end); t = t.Add(time.Hour) {
		if t.Hour() == m.weekHour {
			if first.IsZero() {
				first = t
			}
			last = t.Add(time.Hour)
		}
	}
	return first, last
}
func (m *dashboard) loadAgenda() tea.Cmd {
	from, to := m.cellInterval()
	m.agendaMode = true
	m.agendaPending = true
	m.agendaGen++
	generation := m.agendaGen
	snaps := m.visibleSnapshots()
	offset := m.agendaOffset
	return func() tea.Msg {
		if from.IsZero() {
			return agendaMsg{generation, []service.Occurrence{}, false, nil}
		}
		rows, more, e := service.Agenda(m.ctx, snaps, from, to, offset, 200)
		return agendaMsg{generation, rows, more, e}
	}
}
func (m *dashboard) paletteActions() []action {
	out := []action{}
	q := strings.ToLower(m.filter.Value())
	for _, a := range m.actions {
		if m.available(a) && (q == "" || strings.Contains(strings.ToLower(a.Label), q)) {
			out = append(out, a)
		}
	}
	return out
}
func (m *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.routeSurface(msg); handled {
		return m, cmd
	}
	switch v := msg.(type) {
	case queueAvailableMsg:
		m.queueAvailable = bool(v)
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = v.IsDark()
		return m, nil
	case tea.WindowSizeMsg:
		m.width = max(1, v.Width)
		m.height = max(1, v.Height)
		m.filter.SetWidth(max(1, m.width-5))
		return m, nil
	case refreshTick:
		return m, tea.Batch(m.refresh(), m.tick())
	case snapshotMsg:
		if m.generations[v.key] != v.generation {
			return m, nil
		}
		m.pending[v.key] = false
		if v.err != nil {
			old := m.snapshots[v.key]
			parts := strings.SplitN(v.key, "/", 2)
			old.Host = parts[0]
			old.Source = parts[1]
			old.Error = v.err.Error()
			m.snapshots[v.key] = old
		} else {
			m.snapshots[v.key] = v.snapshot
		}
		m.clamp()
		if m.view == "week" {
			return m, m.buildWeek()
		}
		return m, nil
	case handoffMsg:
		m.busy = false
		if v.err != nil {
			m.status = "Returned: " + v.err.Error()
		} else {
			m.status = "Returned; refreshing affected sources"
		}
		if c, e := config.Load(m.configPath); e == nil {
			previous := m.scopeKey()
			previousMouse := m.service.Config.Mouse
			m.service = service.New(c)
			m.targets = m.service.Targets("all", "all")
			m.scope = 0
			for i, t := range m.targets {
				if t[0]+"/"+t[1] == previous {
					m.scope = i + 1
				}
			}
			if previousMouse != c.Mouse {
				m.mouse = c.Mouse
			}
			for key := range m.generations {
				m.generations[key]++
				m.pending[key] = false
			}
			if a, e := Actions(c.Keys); e == nil {
				m.actions = a
			} else {
				m.status = e.Error()
			}
		}
		return m, m.refresh()
	case weekMsg:
		if v.generation != m.weekGen {
			return m, nil
		}
		m.weekPending = false
		if v.err != nil {
			m.status = v.err.Error()
		} else {
			m.week = v.overview
		}
		return m, nil
	case agendaMsg:
		if v.generation != m.agendaGen {
			return m, nil
		}
		m.agendaPending = false
		m.agenda = v.rows
		m.agendaMore = v.more
		m.agendaSelected = 0
		if v.err != nil {
			m.status = v.err.Error()
		}
		return m, nil
	case logsMsg:
		if m.modal != "" {
			if v.err != nil {
				m.modal = v.err.Error()
			} else {
				m.modal = v.content
			}
			m.modalScroll = 0
		}
		return m, nil
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return m, m.handleMouse(msg)
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
		if m.timezoneEditing {
			switch key {
			case "esc":
				m.timezoneEditing = false
				m.filter.SetValue(m.savedFilter)
				m.filter.Blur()
				return m, nil
			case "enter":
				zone := m.filter.Value()
				if _, e := time.LoadLocation(zone); e != nil {
					m.status = e.Error()
					return m, nil
				}
				m.displayTimezone = zone
				m.timezoneEditing = false
				m.filter.SetValue(m.savedFilter)
				m.filter.Blur()
				m.status = "Display timezone: " + zone + " (schedules unchanged)"
				if m.view == "week" {
					return m, m.buildWeek()
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			return m, cmd
		}
		if m.modal != "" {
			switch key {
			case "esc", "q":
				m.modal = ""
			case "up", "k":
				m.modalScroll = max(0, m.modalScroll-1)
			case "down", "j":
				m.modalScroll++
			case "pgdown":
				m.modalScroll += max(1, m.height-6)
			case "pgup":
				m.modalScroll = max(0, m.modalScroll-max(1, m.height-6))
			}
			return m, nil
		}
		if m.help {
			switch key {
			case "esc", "?", "q":
				m.closeOverlay()
			case "up", "k":
				m.helpScroll = max(0, m.helpScroll-1)
			case "down", "j":
				m.helpScroll++
			case "pgdown":
				m.helpScroll += max(1, m.height-6)
			case "pgup":
				m.helpScroll = max(0, m.helpScroll-max(1, m.height-6))
			case "f1":
				return m, m.act("guide")
			}
			return m, nil
		}
		if m.palette {
			switch key {
			case "esc":
				m.palette = false
				m.filter.SetValue(m.savedFilter)
				m.filter.Blur()
			case "up":
				m.paletteRow = max(0, m.paletteRow-1)
			case "down":
				m.paletteRow = min(max(0, len(m.paletteActions())-1), m.paletteRow+1)
			case "enter":
				items := m.paletteActions()
				if len(items) > 0 {
					a := items[min(m.paletteRow, len(items)-1)]
					m.palette = false
					m.filter.SetValue(m.savedFilter)
					return m, m.act(a.ID)
				}
			}
			if key == "esc" || key == "up" || key == "down" || key == "enter" {
				return m, nil
			}
			before := m.filter.Value()
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			if before != m.filter.Value() {
				m.paletteRow = 0
			}
			return m, cmd
		}
		if m.filtering {
			switch key {
			case "esc":
				m.filtering = false
				m.filter.SetValue("")
				m.filter.Blur()
				m.selectedKey = ""
				m.clamp()
				if m.view == "week" {
					return m, m.buildWeek()
				}
				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()
				if m.view == "week" {
					return m, m.buildWeek()
				}
				return m, nil
			case "up":
				m.selectRow(-1)
				return m, nil
			case "down":
				m.selectRow(1)
				return m, nil
			}
			before := m.filter.Value()
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			if before != m.filter.Value() {
				m.selected = 0
				m.selectedKey = ""
				m.clamp()
			}
			return m, cmd
		}
		if key == "q" {
			return m, tea.Quit
		}
		if key == "?" {
			m.help = true
			return m, nil
		}
		if key == "/" {
			m.filtering = true
			return m, m.filter.Focus()
		}
		if key == ":" {
			m.palette = true
			m.paletteRow = 0
			m.savedFilter = m.filter.Value()
			m.filter.SetValue("")
			return m, m.filter.Focus()
		}
		if key == "m" {
			m.mouse = !m.mouse
			return m, nil
		}
		if m.view == "week" {
			switch key {
			case "esc":
				if m.agendaMode {
					m.agendaMode = false
				} else {
					m.view = "jobs"
				}
				return m, nil
			case "[":
				m.weekDate = m.weekDate.AddDate(0, 0, -7)
				m.agendaMode = false
				return m, m.buildWeek()
			case "]":
				m.weekDate = m.weekDate.AddDate(0, 0, 7)
				m.agendaMode = false
				return m, m.buildWeek()
			case "up", "k":
				if m.agendaMode {
					m.agendaSelected = max(0, m.agendaSelected-1)
				} else {
					m.weekHour = (m.weekHour + 23) % 24
				}
				return m, nil
			case "down", "j":
				if m.agendaMode {
					m.agendaSelected = min(max(0, len(m.agenda)-1), m.agendaSelected+1)
				} else {
					m.weekHour = (m.weekHour + 1) % 24
				}
				return m, nil
			case "left", "h":
				m.weekDay = (m.weekDay + 6) % 7
				return m, nil
			case "right", "l":
				m.weekDay = (m.weekDay + 1) % 7
				return m, nil
			case "pgdown":
				if m.agendaMode && m.agendaMore {
					m.agendaOffset += 200
					return m, m.loadAgenda()
				}
			case "pgup":
				if m.agendaMode {
					m.agendaOffset = max(0, m.agendaOffset-200)
					return m, m.loadAgenda()
				}
			case "enter":
				if m.agendaMode {
					if len(m.agenda) > 0 {
						a := m.agenda[m.agendaSelected]
						m.view = "jobs"
						m.selectedKey = a.Host + "/" + a.Source + "/" + a.JobID
						m.clamp()
						m.focus = 2
					}
				} else {
					m.agendaOffset = 0
					return m, m.loadAgenda()
				}
				return m, nil
			}
		}
		for _, a := range m.actions {
			if key == a.Key {
				return m, m.act(a.ID)
			}
		}
		switch key {
		case "tab":
			m.focus = (m.focus + 1) % 3
			m.prefixG = false
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
			m.prefixG = false
		case "left", "h":
			m.focus = max(0, m.focus-1)
		case "right", "l":
			m.focus = min(2, m.focus+1)
		case "esc":
			m.filter.SetValue("")
			m.prefixG = false
		case "up", "k", "down", "j":
			delta := 1
			if key == "up" || key == "k" {
				delta = -1
			}
			if m.focus == 0 {
				m.changeScope(m.scope + delta)
			} else if m.focus == 1 {
				m.selectRow(delta)
			} else {
				m.detailScroll = max(0, m.detailScroll+delta)
			}
		case "g":
			if m.prefixG {
				m.selectRow(-len(m.rows()))
				m.prefixG = false
			} else {
				m.prefixG = true
			}
		case "G", "end":
			m.selectRow(len(m.rows()))
			m.prefixG = false
		case "home":
			m.selectRow(-len(m.rows()))
		case "enter":
			m.focus = 2
		default:
			m.prefixG = false
		}
		return m, nil
	}
	if m.filtering || m.palette || m.timezoneEditing {
		before := m.filter.Value()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		if before != m.filter.Value() {
			if m.palette {
				m.paletteRow = 0
			} else {
				m.selected = 0
				m.selectedKey = ""
				m.clamp()
			}
		}
		return m, cmd
	}
	return m, m.surfaceMessages(msg)
}
func (m *dashboard) rowStart() int { return max(0, m.selected-m.visibleRows()+1) }
func (m *dashboard) scopeLines() []string {
	theme := styles(m.isDark())
	lines := []string{"Hosts / sources", ""}
	label := "  All registered"
	if m.scope == 0 {
		label = theme.Selected.Render("› All registered")
	}
	lines = append(lines, label)
	for i, t := range m.targets {
		prefix := "  "
		if m.scope == i+1 {
			prefix = "› "
		}
		key := t[0] + "/" + t[1]
		state := ""
		if m.pending[key] {
			state = theme.Warning.Render(" …")
		} else if snap := m.snapshots[key]; snap.Error != "" {
			state = theme.Error.Render(" !")
		}
		line := prefix + key + state
		if m.scope == i+1 {
			line = theme.Selected.Render(prefix+key) + state
		}
		lines = append(lines, line)
	}
	return lines
}
func (m *dashboard) jobLines(width int) []string {
	t := styles(m.isDark())
	rows := m.rows()
	lines := []string{"Jobs · " + fmt.Sprint(len(rows)), ""}
	start := m.rowStart()
	for i := start; i < min(len(rows), start+m.visibleRows()); i++ {
		j := rows[i]
		prefix := "  "
		if i == m.selected {
			prefix = "› "
		}
		state := t.Success.Render("●")
		if !j.Enabled {
			state = t.Muted.Render("○")
		}
		name := j.Name
		if name == "" {
			name = j.Command
		}
		if j.Runner == "pueue" {
			name += " [queue]"
		}
		desc := j.Description
		if j.Diagnostic != "" {
			desc = "! " + j.Diagnostic
		}
		next := "—"
		if len(j.Next) > 0 {
			next = j.Next[0].In(m.displayLocation()).Format("01-02 15:04")
		}
		name = clip(safe(name), max(8, min(20, width/3)))
		line := prefix + state + " " + name + " · " + t.Accent.Render(next) + " · " + t.Muted.Render(safe(desc))
		if i == m.selected {
			line = t.Selected.Render(pad(prefix+ansi.Strip(state)+" "+name+" · "+next+" · "+safe(desc), width))
		}
		lines = append(lines, clip(line, width))
	}
	if len(rows) == 0 {
		lines = append(lines, "No matching jobs.", "n: create · /: search")
	}
	return lines
}
func (m *dashboard) displayLocation() *time.Location {
	zone := m.displayTimezone
	if zone == "" {
		zone = m.service.Config.Timezone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Local
	}
	return loc
}
func (m *dashboard) detailLines(width int) []string {
	j, ok := m.current()
	if !ok {
		return []string{"Detail", "", "Select a job to inspect."}
	}
	lines := []string{"Detail · " + j.Host + "/" + j.Source, "", j.Name, j.Description, j.Schedule, "", "COMMAND", j.Command, "", "REMARK", j.Remark, "", "Timezone: " + j.Timezone, "ID: " + j.ID}
	for _, t := range j.Next {
		lines = append(lines, "Next: "+t.In(m.displayLocation()).Format("2006-01-02 15:04 -07:00"))
	}
	if j.Diagnostic != "" {
		lines = append(lines, "! "+j.Diagnostic)
	}
	lines = append(lines, j.Warnings...)
	if snap, ok := m.snapshots[j.Host+"/"+j.Source]; ok {
		lines = append(lines, "Observed: "+snap.Observed.Format("15:04:05"))
		if snap.Error != "" {
			lines = append(lines, "STALE: "+snap.Error)
		}
	}
	wrapped := []string{}
	for _, line := range lines {
		parts := strings.Split(ansi.Hardwrap(safe(line), max(1, width), true), "\n")
		t := styles(m.isDark())
		for _, part := range parts {
			switch {
			case line == j.Schedule:
				part = t.Schedule.Render(part)
			case line == j.Name:
				part = t.Title.Render(part)
			case line == "COMMAND" || line == "REMARK":
				part = t.Muted.Bold(true).Render(part)
			case strings.HasPrefix(line, "!") || strings.HasPrefix(line, "STALE:"):
				part = t.Warning.Render(part)
			}
			wrapped = append(wrapped, part)
		}
	}
	start := min(m.detailScroll, max(0, len(wrapped)-1))
	return wrapped[start:]
}
func pane(lines []string, width, height int, focused bool) string {
	out := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = clip(lines[i], width)
		}
		if i == 0 && focused {
			line = lipgloss.NewStyle().Bold(true).Underline(true).Render(line)
		}
		out = append(out, line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line))))
	}
	return strings.Join(out, "\n")
}
func (m *dashboard) weekLines() []string {
	t := styles(m.isDark())
	if m.agendaMode {
		lines := []string{t.Title.Render("Agenda · exact times and UTC offsets"), t.Muted.Render("PgUp/PgDn pages · Enter inspect job · Esc grid")}
		start := m.agendaStart()
		for i := start; i < min(len(m.agenda), start+max(1, m.layout().body.h-2)); i++ {
			a := m.agenda[i]
			line := "  " + a.Time.Format("Mon 15:04:05 -07:00") + " " + a.Host + "/" + a.Source + " " + a.Name
			if a.Queued {
				line += " [enqueue]"
			}
			if i == m.agendaSelected {
				line = t.Selected.Render(pad("› "+strings.TrimPrefix(line, "  "), m.width))
			}
			lines = append(lines, line)
		}
		if m.agendaPending {
			lines = append(lines, t.Warning.Render("Loading…"))
		} else if len(m.agenda) == 0 {
			lines = append(lines, "No triggers in this hour (including missing DST hours).")
		}
		return lines
	}
	title := "Forecast · week of " + m.week.Start.Format("2006-01-02") + " · " + m.week.Timezone
	if m.weekPending {
		title += " · calculating…"
	}
	days := "       Mon     Tue     Wed     Thu     Fri     Sat     Sun"
	if m.width < 70 {
		days = m.week.Start.AddDate(0, 0, m.weekDay).Format("Monday 02 Jan") + " · ←/→ change day"
	}
	lines := []string{t.Title.Render(title), t.Muted.Render("Configured triggers · Enter exact agenda"), t.Accent.Render(days)}
	start := m.weekStart()
	for h := start; h < min(24, start+max(1, m.layout().body.h-3)); h++ {
		line := fmt.Sprintf("%02d:00 ", h)
		for d := 0; d < 7; d++ {
			if m.width < 70 && d != m.weekDay {
				continue
			}
			cell := m.week.Cells[d][h]
			label := fmt.Sprint(cell.Count)
			if cell.Truncated {
				label += "+"
			}
			style := t.Muted
			if cell.Count > 0 {
				style = t.Accent
			}
			if cell.Count >= 5 {
				style = t.Schedule
			}
			if cell.Truncated {
				style = t.Warning
			}
			if h == m.weekHour && d == m.weekDay {
				label = "[" + label + "]"
				style = t.Selected
			}
			line += style.Render(fmt.Sprintf("%7s ", label))
		}
		lines = append(lines, line)
	}
	return lines
}
func (m *dashboard) View() tea.View {
	t := styles(m.isDark())
	header := m.header()
	contextLine := m.scopeKey() + " · display " + m.displayLocation().String()
	if m.view == "playground" && m.playground != nil && m.playground.Editing() {
		contextLine = "Editing cron · Alt+1/2/3 switch views · Esc leaves input"
	}
	if m.filtering || m.timezoneEditing {
		contextLine = m.filter.View()
	} else if m.filter.Value() != "" && !m.palette {
		contextLine += " · / " + safe(m.filter.Value())
	}
	if m.childPending {
		contextLine += " · opening draft…"
	} else if m.child != nil {
		contextLine += " · open draft"
	}
	var body string
	if m.childShowing() {
		body = m.child.View().Content
	} else if m.view == "playground" && m.playground != nil && !m.help && !m.palette && m.modal == "" {
		body = m.playground.View().Content
	} else {
		height := m.layout().body.h
		switch {
		case m.modal != "":
			lines := strings.Split(ansi.Hardwrap(safe(m.modal), max(1, m.width), true), "\n")
			start := min(m.modalScroll, max(0, len(lines)-1))
			body = fitLines(strings.Join(lines[start:], "\n"), m.width, height)
		case m.help:
			lines := m.helpLines()
			start := min(m.helpScroll, max(0, len(lines)-1))
			body = fitLines(strings.Join(lines[start:], "\n"), m.width, height)
		case m.palette:
			lines := []string{t.Title.Render("Actions · Enter choose"), m.filter.View()}
			items := m.paletteActions()
			start := max(0, m.paletteRow-height+3)
			for i := start; i < min(len(items), start+max(1, height-2)); i++ {
				line := "  " + items[i].Label + " [" + items[i].Key + "]"
				if i == m.paletteRow {
					line = t.Selected.Render(pad("› "+items[i].Label+" ["+items[i].Key+"]", m.width))
				}
				lines = append(lines, line)
			}
			if len(items) == 0 {
				lines = append(lines, "No matching actions.")
			}
			body = fitLines(strings.Join(lines, "\n"), m.width, height)
		case m.view == "week":
			body = fitLines(strings.Join(m.weekLines(), "\n"), m.width, height)
		default:
			body = m.jobBody()
		}
		status := m.status
		if m.prefixG {
			status = "g… (g first row, Esc cancel)"
		}
		if m.busy {
			status = "External interaction owns the terminal"
		}
		if m.focus == 0 && m.scope > 0 && m.scope <= len(m.targets) {
			snap := m.snapshots[m.targets[m.scope-1][0]+"/"+m.targets[m.scope-1][1]]
			if snap.Error != "" {
				status = "! " + snap.Error
			}
		}
		var buttons []string
		x := 0
		for _, b := range m.footerButtons() {
			w := ansi.StringWidth(b[1]) + 4
			if x+w > m.width {
				break
			}
			buttons = append(buttons, t.Accent.Render("[ "+b[1]+" ]"))
			x += w + 1
		}
		hint := "Tab focus · / search · : actions · ? help · q quit · m mouse"
		body += "\n" + t.Muted.Render(clip(safe(status), m.width)) + "\n" + strings.Join(buttons, " ") + "\n" + t.Muted.Render(clip(hint, m.width))
	}
	v := tea.NewView(fitLines(header+"\n"+t.Muted.Render(clip(contextLine, m.width))+"\n"+body, m.width, m.height))
	v.AltScreen = true
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

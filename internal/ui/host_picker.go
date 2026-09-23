package ui

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/hostinventory"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

type HostPickerOptions struct {
	Source string // ssh (default) or the optional dev inventory
}

type HostPickerModel struct {
	ctx             context.Context
	cancel          context.CancelFunc
	config          config.Config
	service         *service.Service
	source, stage   string
	inventory       hostinventory.Inventory
	manual          []hostinventory.Candidate
	selected        map[string]config.Host
	registered      map[string]bool
	filter, alias   textinput.Model
	filtering       bool
	index, scroll   int
	width, height   int
	dark, embedded  bool
	generation      int
	discoveryCancel context.CancelFunc
	discover        func(context.Context, string) (hostinventory.Inventory, error)
	plan            service.HostPlan
	err             error
	message         string
	changed         bool
	press           string
	checks          map[string]string
	verifyQueue     []config.Host
	verifying       int
	authenticating  bool
	applyDispatched bool
	applyFinished   bool
	cancelPending   bool
	finished        bool
	planGeneration  int
}

type hostDiscovered struct {
	owner      *HostPickerModel
	generation int
	inventory  hostinventory.Inventory
	err        error
}
type hostPlanBuilt struct {
	owner      *HostPickerModel
	generation int
	plan       service.HostPlan
	err        error
}
type hostRegistrationsSaved struct {
	owner *HostPickerModel
	err   error
}
type hostVerified struct {
	owner    *HostPickerModel
	host     string
	snapshot service.Snapshot
	err      error
}
type hostAuthPrepared struct {
	owner *HostPickerModel
	host  string
	cmd   *exec.Cmd
	err   error
}
type hostAuthenticated struct {
	owner *HostPickerModel
	host  string
	err   error
}

// NewHostPicker is the same workflow in the standalone CLI and dashboard.
// Construction does no discovery, network I/O or config mutation.
func NewHostPicker(ctx context.Context, cfg config.Config, opts HostPickerOptions) *HostPickerModel {
	child, cancel := context.WithCancel(ctx)
	source := opts.Source
	if source == "" {
		source = "ssh"
	}
	input := func() textinput.Model {
		v := textinput.New()
		v.Prompt = ""
		v.SetWidth(60)
		v.SetVirtualCursor(true)
		v.CharLimit = 255
		return v
	}
	m := &HostPickerModel{ctx: child, cancel: cancel, config: cfg, service: service.New(cfg), source: source, stage: "list", selected: map[string]config.Host{}, registered: map[string]bool{}, filter: input(), alias: input(), width: 80, height: 24, dark: cfg.Theme != "light", discover: hostinventory.Discover, checks: map[string]string{}}
	for _, h := range cfg.AllHosts() {
		m.registered[h.SSH] = true
		m.registered["id:"+h.ID] = true
	}
	return m
}

func (m *HostPickerModel) SetEmbedded(value bool) { m.embedded = value }
func (m *HostPickerModel) SetMouse(value bool) {
	m.config.Mouse = value
	m.press = ""
}
func (m *HostPickerModel) Close()        { m.cancel() }
func (m *HostPickerModel) Err() error    { return m.err }
func (m *HostPickerModel) Changed() bool { return m.changed || m.applyDispatched }
func (m *HostPickerModel) Init() tea.Cmd { return m.load() }

func (m *HostPickerModel) load() tea.Cmd {
	if m.discoveryCancel != nil {
		m.discoveryCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.discoveryCancel = cancel
	m.generation++
	generation, source, discover := m.generation, m.source, m.discover
	if m.inventory.Source != source {
		m.inventory = hostinventory.Inventory{Source: source}
	}
	m.err, m.press, m.message = nil, "", "Reading local SSH aliases…"
	return func() tea.Msg {
		inventory, err := discover(ctx, source)
		return hostDiscovered{m, generation, inventory, err}
	}
}

func (m *HostPickerModel) finish(err error) tea.Cmd {
	if m.finished {
		return nil
	}
	if m.applyDispatched && !m.applyFinished {
		// A local rename may already be in flight. Keep the workflow mounted
		// until its effect returns, so the parent's reload cannot precede it.
		m.cancelPending = true
		m.cancel()
		m.message = "Stopping save; checking whether registrations were written…"
		m.press = ""
		return nil
	}
	m.finished = true
	m.cancel()
	m.err = err
	if m.embedded {
		message, changed := m.message, m.Changed()
		return func() tea.Msg { return WorkflowDoneMsg{Owner: m, Err: err, Message: message, Changed: changed} }
	}
	return tea.Quit
}

func (m *HostPickerModel) candidates() []hostinventory.Candidate {
	items := append([]hostinventory.Candidate(nil), m.manual...)
	seen := map[string]bool{}
	for _, c := range items {
		seen[c.Alias] = true
	}
	for _, c := range m.inventory.Candidates {
		if !seen[c.Alias] {
			items = append(items, c)
		}
	}
	query := strings.ToLower(m.filter.Value())
	out := make([]hostinventory.Candidate, 0, len(items))
	for _, c := range items {
		if strings.Contains(strings.ToLower(c.Alias+" "+strings.Join(c.Fleet, " ")), query) {
			out = append(out, c)
		}
	}
	return out
}

func (m *HostPickerModel) move(delta int) {
	m.index = max(0, min(len(m.candidates())-1, m.index+delta))
	m.press = ""
}

func (m *HostPickerModel) toggle(alias string) {
	if _, exists := m.selected[alias]; exists {
		delete(m.selected, alias)
		return
	}
	for _, c := range m.candidates() {
		if c.Alias != alias {
			continue
		}
		if m.registered[alias] || m.registered["id:"+alias] {
			m.err = fmt.Errorf("%s is already registered", alias)
		} else if !c.Selectable {
			m.err = fmt.Errorf("%s", c.Reason)
		} else {
			m.selected[alias] = config.Host{ID: alias, SSH: alias}
			m.err = nil
		}
		return
	}
}

func (m *HostPickerModel) beginReview() tea.Cmd {
	if len(m.selected) == 0 {
		m.err = fmt.Errorf("select one or more aliases with Space or the checkbox")
		return nil
	}
	hosts := make([]config.Host, 0, len(m.selected))
	for _, h := range m.selected {
		hosts = append(hosts, h)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ID < hosts[j].ID })
	m.stage, m.err, m.press, m.scroll = "building", nil, "", 0
	m.planGeneration++
	generation := m.planGeneration
	cfg := m.config
	return func() tea.Msg {
		plan, err := service.PlanHosts(cfg, hosts)
		return hostPlanBuilt{m, generation, plan, err}
	}
}

func (m *HostPickerModel) save() tea.Cmd {
	m.stage, m.press = "saving", ""
	m.applyDispatched = true
	plan, ctx := m.plan, m.ctx
	return func() tea.Msg { return hostRegistrationsSaved{m, service.ApplyHosts(ctx, plan)} }
}

func (m *HostPickerModel) checkNext() tea.Cmd {
	var cmds []tea.Cmd
	for m.verifying < max(1, m.config.MaxParallel) && len(m.verifyQueue) > 0 {
		host := m.verifyQueue[0]
		m.verifyQueue = m.verifyQueue[1:]
		m.verifying++
		m.checks[host.ID] = "Checking user crontab…"
		backend, ctx := m.service, m.ctx
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := backend.Snapshot(ctx, host.ID, "user")
			return hostVerified{m, host.ID, snapshot, err}
		})
	}
	return tea.Batch(cmds...)
}

func (m *HostPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.finished {
		return m, nil
	}
	switch v := msg.(type) {
	case hostDiscovered:
		if v.owner != m || v.generation != m.generation || m.ctx.Err() != nil || m.stage != "list" && m.stage != "manual" {
			return m, nil
		}
		m.err = v.err
		m.press = ""
		if v.err == nil {
			m.inventory = v.inventory
		}
		m.message = "Choose only the hosts whose schedules you want to see."
		m.index = max(0, min(m.index, len(m.candidates())-1))
		return m, nil
	case hostPlanBuilt:
		if v.owner != m || v.generation != m.planGeneration || m.stage != "building" || m.ctx.Err() != nil {
			return m, nil
		}
		m.plan, m.err = v.plan, v.err
		m.press = ""
		if v.err != nil {
			m.stage = "list"
			return m, nil
		}
		return m, m.save()
	case hostRegistrationsSaved:
		if v.owner != m || m.stage != "saving" || m.ctx.Err() != nil && !m.cancelPending {
			return m, nil
		}
		m.applyFinished, m.press = true, ""
		if m.cancelPending {
			m.stage = "result"
			if v.err == nil {
				m.changed = true
				m.message = fmt.Sprintf("Saved %d host registration(s); connection checks were cancelled.", len(m.plan.Hosts))
			} else {
				m.message = "Save was interrupted; reloading host registrations to check the outcome."
			}
			return m, m.finish(ErrCancelled)
		}
		m.err = v.err
		if v.err != nil {
			m.stage, m.message = "result", "Registration was not verified. Inspect the config before retrying."
			return m, nil
		}
		m.changed, m.stage, m.index = true, "result", 0
		m.message = fmt.Sprintf("Saved %d host registration(s).", len(m.plan.Hosts))
		m.config.Hosts = append(m.config.Hosts, m.plan.Hosts...)
		m.service.Config = m.config
		m.verifyQueue = append([]config.Host(nil), m.plan.Hosts...)
		return m, m.checkNext()
	case hostVerified:
		if v.owner != m || m.stage != "result" || m.ctx.Err() != nil {
			return m, nil
		}
		m.verifying--
		m.press = ""
		if v.err != nil {
			m.checks[v.host] = "Saved · read failed: " + v.err.Error()
		} else {
			m.checks[v.host] = fmt.Sprintf("Ready · %d jobs", len(v.snapshot.Entries))
		}
		return m, m.checkNext()
	case hostAuthPrepared:
		if v.owner != m || m.stage != "result" || m.ctx.Err() != nil {
			return m, nil
		}
		m.press = ""
		if v.err != nil {
			m.authenticating = false
			m.checks[v.host] = "Saved · SSH setup failed: " + v.err.Error()
			return m, nil
		}
		return m, tea.ExecProcess(v.cmd, func(err error) tea.Msg { return hostAuthenticated{m, v.host, err} })
	case hostAuthenticated:
		if v.owner != m || m.stage != "result" || m.ctx.Err() != nil {
			return m, nil
		}
		m.authenticating = false
		m.press = ""
		if v.err != nil {
			m.checks[v.host] = "Saved · native SSH did not complete: " + v.err.Error()
			return m, nil
		}
		for _, h := range m.plan.Hosts {
			if h.ID == v.host {
				m.verifyQueue = append(m.verifyQueue, h)
				break
			}
		}
		return m, m.checkNext()
	case tea.WindowSizeMsg:
		m.width, m.height, m.press = max(1, v.Width), max(1, v.Height), ""
		m.filter.SetWidth(max(1, m.width-12))
		m.alias.SetWidth(max(1, m.width-8))
		return m, nil
	case tea.BackgroundColorMsg:
		if m.config.Theme == "auto" {
			m.dark = v.IsDark()
		}
		return m, nil
	case tea.MouseWheelMsg:
		m.press = ""
		if !m.config.Mouse {
			return m, nil
		}
		delta := 1
		if strings.Contains(v.String(), "up") {
			delta = -1
		}
		if m.stage == "list" {
			m.move(delta)
		} else if m.stage == "result" {
			m.index = max(0, min(len(m.plan.Hosts)-1, m.index+delta))
		} else {
			m.scroll = max(0, m.scroll+delta)
		}
		return m, nil
	case tea.MouseClickMsg:
		if !m.config.Mouse || v.Button != tea.MouseLeft {
			return m, nil
		}
		_, hits := m.layout()
		hit := hitAt(hits, v.X, v.Y)
		m.press = hit
		if strings.HasPrefix(hit, "row:") || strings.HasPrefix(hit, "toggle:") {
			alias := strings.TrimPrefix(strings.TrimPrefix(hit, "row:"), "toggle:")
			for i, c := range m.candidates() {
				if c.Alias == alias {
					m.index = i
					break
				}
			}
			m.filtering = false
			m.filter.Blur()
		}
		return m, nil
	case tea.MouseReleaseMsg:
		if !m.config.Mouse || m.press == "" {
			return m, nil
		}
		_, hits := m.layout()
		hit, pressed := hitAt(hits, v.X, v.Y), m.press
		m.press = ""
		if hit != pressed {
			return m, nil
		}
		return m, m.action(hit)
	case tea.KeyPressMsg:
		m.press = ""
		key := v.String()
		if key == "ctrl+c" {
			return m, m.finish(ErrCancelled)
		}
		if m.stage == "saving" {
			return m, nil
		}
		if m.stage == "result" {
			if key == "enter" || key == "esc" || key == "q" {
				return m, m.finish(m.err)
			}
			if key == "a" && !v.IsRepeat {
				return m, m.action("authenticate")
			}
			if key == "r" && !v.IsRepeat {
				return m, m.action("retry")
			}
			if key == "down" || key == "j" {
				m.index = min(len(m.plan.Hosts)-1, m.index+1)
			} else if key == "up" || key == "k" {
				m.index = max(0, m.index-1)
			}
			return m, nil
		}
		if m.stage == "building" {
			if key == "esc" {
				m.stage = "list"
				m.planGeneration++
			}
			return m, nil
		}
		if m.stage == "manual" {
			switch key {
			case "esc":
				m.stage, m.err = "list", nil
				m.alias.Blur()
				return m, nil
			case "enter":
				return m, m.action("manual-add")
			}
			var cmd tea.Cmd
			m.alias, cmd = m.alias.Update(msg)
			return m, cmd
		}
		if m.filtering {
			switch key {
			case "esc":
				m.filter.SetValue("")
				m.filtering = false
				m.filter.Blur()
				return m, nil
			case "enter", "tab":
				m.filtering = false
				m.filter.Blur()
				return m, nil
			case "up", "down":
				if key == "up" {
					m.move(-1)
				} else {
					m.move(1)
				}
				return m, nil
			}
			old := m.filter.Value()
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			if old != m.filter.Value() {
				m.index = 0
			}
			return m, cmd
		}
		switch key {
		case "esc", "q":
			return m, m.finish(ErrCancelled)
		case "/", "ctrl+f", "tab":
			return m, m.action("filter")
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "home", "g":
			m.index = 0
		case "end", "G":
			m.index = max(0, len(m.candidates())-1)
		case "space", "enter":
			rows := m.candidates()
			if len(rows) > m.index && !v.IsRepeat {
				m.toggle(rows[m.index].Alias)
			}
		case "ctrl+s":
			return m, m.beginReview()
		case "ctrl+n":
			return m, m.action("manual")
		case "ctrl+d":
			source := "dev"
			if m.source == source {
				source = "ssh"
			}
			return m, m.action("source:" + source)
		case "r":
			return m, m.load()
		}
		return m, nil
	}
	if m.stage == "manual" {
		var cmd tea.Cmd
		m.alias, cmd = m.alias.Update(msg)
		return m, cmd
	}
	if m.stage == "list" && m.filtering {
		var cmd tea.Cmd
		old := m.filter.Value()
		m.filter, cmd = m.filter.Update(msg)
		if old != m.filter.Value() {
			m.index = 0
		}
		return m, cmd
	}
	return m, nil
}

func (m *HostPickerModel) action(action string) tea.Cmd {
	switch {
	case action == "close":
		return m.finish(m.err)
	case action == "cancel":
		return m.finish(ErrCancelled)
	case action == "back":
		m.stage, m.err, m.scroll = "list", nil, 0
	case action == "review" && m.stage == "list":
		return m.beginReview()
	case action == "retry" && m.stage == "result" && m.changed && m.verifying == 0 && !m.authenticating:
		m.verifyQueue = append([]config.Host(nil), m.plan.Hosts...)
		return m.checkNext()
	case action == "authenticate" && m.stage == "result" && m.changed && m.verifying == 0 && !m.authenticating:
		if m.index < 0 || m.index >= len(m.plan.Hosts) {
			return nil
		}
		host := m.plan.Hosts[m.index]
		m.authenticating = true
		m.checks[host.ID] = "Preparing native SSH authentication…"
		ctx, timeout := m.ctx, m.config.ConnectTimeoutSeconds
		return func() tea.Msg {
			cmd, _, err := (transport.Native{ConnectTimeout: timeout}).Authentication(ctx, host)
			return hostAuthPrepared{m, host.ID, cmd, err}
		}
	case strings.HasPrefix(action, "result:") && m.stage == "result":
		id := strings.TrimPrefix(action, "result:")
		for i, h := range m.plan.Hosts {
			if h.ID == id {
				m.index = i
			}
		}
	case action == "filter" && m.stage == "list":
		m.filtering = true
		return m.filter.Focus()
	case strings.HasPrefix(action, "source:") && m.stage == "list":
		m.source, m.index = strings.TrimPrefix(action, "source:"), 0
		m.filtering = false
		m.filter.Blur()
		return m.load()
	case action == "manual" && m.stage == "list":
		m.stage, m.err = "manual", nil
		m.filtering = false
		m.filter.Blur()
		return m.alias.Focus()
	case action == "manual-add" && m.stage == "manual":
		alias := strings.TrimSpace(m.alias.Value())
		if !hostinventory.ValidAlias(alias) {
			m.err = fmt.Errorf("enter an OpenSSH alias using letters, digits, '.', '_' or '-'; for user@host use hosts add ID --ssh user@host")
			return nil
		}
		if m.registered[alias] || m.registered["id:"+alias] {
			m.err = fmt.Errorf("%s is already registered", alias)
			return nil
		}
		exists := false
		for _, c := range m.manual {
			exists = exists || c.Alias == alias
		}
		if !exists {
			m.manual = append(m.manual, hostinventory.Candidate{Alias: alias, Status: "manual", Selectable: true})
		}
		m.selected[alias] = config.Host{ID: alias, SSH: alias}
		m.stage, m.err, m.index = "list", nil, 0
		m.alias.Blur()
		m.filter.SetValue("")
	case strings.HasPrefix(action, "toggle:") && m.stage == "list":
		m.toggle(strings.TrimPrefix(action, "toggle:"))
	}
	return nil
}

// layout computes both rendering and hit regions without mutating UI state.
func (m *HostPickerModel) layout() ([]string, []hitRegion) {
	theme := styles(m.dark)
	lines := []string{theme.Title.Render("Add SSH hosts"), theme.Muted.Render("Use your existing OpenSSH aliases. Only selected hosts appear in the dashboard.")}
	var hits []hitRegion
	buttons := func(labels, actions []string) {
		x := 0
		var row strings.Builder
		for i, label := range labels {
			text := "[ " + label + " ]"
			w := ansi.StringWidth(text)
			hits = append(hits, hitRegion{actions[i], rect{x, len(lines), max(0, min(w, m.width-x)), 1}})
			row.WriteString(theme.Accent.Render(text) + " ")
			x += w + 1
		}
		lines = append(lines, row.String())
	}
	footerLabels, footerActions := []string{"Cancel"}, []string{"cancel"}
	hint := ""
	switch m.stage {
	case "list":
		ssh, dev := "SSH config", "dev"
		if m.source == "ssh" {
			ssh += " •"
		} else {
			dev += " •"
		}
		buttons([]string{ssh, dev, "Manual alias"}, []string{"source:ssh", "source:dev", "manual"})
		lines = append(lines, "")
		hits = append(hits, hitRegion{"filter", rect{0, len(lines), m.width, 1}})
		prefix := "Filter: "
		if m.filtering {
			prefix = "Filter› "
		}
		lines = append(lines, prefix+m.filter.View(), "")
		rows := m.candidates()
		room := max(1, m.height-13)
		start := max(0, m.index-room+1)
		for i := start; i < min(len(rows), start+room); i++ {
			c := rows[i]
			check, status := "[ ]", c.Status
			if _, selected := m.selected[c.Alias]; selected {
				check = "[x]"
			}
			if m.registered[c.Alias] || m.registered["id:"+c.Alias] {
				check, status = "[–]", "already registered"
			} else if !c.Selectable {
				check = "[–]"
			}
			marker := " "
			if i == m.index {
				marker = "›"
			}
			row := fmt.Sprintf("%s%s %-24s %s", marker, check, safe(c.Alias), safe(status))
			if i == m.index {
				row = theme.Selected.Render(pad(row, m.width))
			}
			hits = append(hits, hitRegion{"toggle:" + c.Alias, rect{1, len(lines), 3, 1}}, hitRegion{"row:" + c.Alias, rect{0, len(lines), m.width, 1}})
			lines = append(lines, row)
		}
		if len(rows) == 0 {
			lines = append(lines, theme.Muted.Render("No matching aliases. Change the filter or choose Manual alias."))
		}
		if !m.inventory.Complete && m.inventory.Candidates != nil {
			lines = append(lines, theme.Warning.Render("Discovery is incomplete; uncertain aliases are not selected automatically."))
		}
		if len(m.inventory.Diagnostics) > 0 {
			lines = append(lines, theme.Muted.Render(safe(m.inventory.Diagnostics[0])))
		}
		lines = append(lines, theme.Muted.Render(safe(m.message)))
		selected := make([]string, 0, len(m.selected))
		for alias := range m.selected {
			selected = append(selected, alias)
		}
		sort.Strings(selected)
		if len(selected) > 0 {
			lines = append(lines, theme.Accent.Render("Selected: "+safe(strings.Join(selected, ", "))))
		}
		footerLabels, footerActions = []string{fmt.Sprintf("Add %d selected", len(m.selected)), "Cancel"}, []string{"review", "cancel"}
		hint = "↑↓/jk move · Space toggle · / filter · Ctrl+N manual · Ctrl+D source · Ctrl+S add"
		if m.filtering {
			hint = "Type to filter · ↑↓ choose · Enter list · Esc clear filter"
		}
	case "manual":
		lines = append(lines, "", "OpenSSH alias", m.alias.View(), "", "Enter the name you use in: ssh ALIAS", "Connection options stay in ~/.ssh/config.")
		footerLabels, footerActions = []string{"Select alias", "Back"}, []string{"manual-add", "back"}
		hint = "Enter select · Esc back"
	case "building", "saving":
		lines = append(lines, "", "Preparing host registrations…")
		if m.stage == "saving" {
			lines[len(lines)-1] = "Saving reviewed host registrations…"
			if m.cancelPending {
				lines[len(lines)-1] = m.message
			}
		}
		footerLabels, footerActions = nil, nil
	case "result":
		lines = append(lines, "", theme.Success.Render(safe(m.message)), "")
		room := max(1, m.height-11)
		start := max(0, m.index-room+1)
		for i := start; i < min(len(m.plan.Hosts), start+room); i++ {
			h := m.plan.Hosts[i]
			status := m.checks[h.ID]
			if status == "" && m.changed {
				status = "Waiting to check…"
			}
			row := safe(h.ID + ": " + status)
			if i == m.index {
				row = theme.Selected.Render(pad("› "+row, m.width))
			} else {
				row = "  " + row
			}
			hits = append(hits, hitRegion{"result:" + h.ID, rect{0, len(lines), m.width, 1}})
			lines = append(lines, row)
		}
		lines = append(lines, "", theme.Muted.Render("Registrations stay saved. Passwords/MFA use native SSH; no password is stored."))
		footerLabels, footerActions = []string{"Return"}, []string{"close"}
		if m.changed && m.verifying == 0 && !m.authenticating {
			footerLabels, footerActions = []string{"Authenticate", "Retry", "Return"}, []string{"authenticate", "retry", "close"}
		}
		hint = "↑↓ select host · a authenticate · r retry checks · Enter / Esc return"
	}
	if m.err != nil {
		lines = append(lines, theme.Error.Render("Error: "+safe(m.err.Error())))
	}
	contentHeight := max(0, m.height-3)
	lines = lines[:min(len(lines), contentHeight)]
	contentHits := hits[:0]
	for _, hit := range hits {
		if hit.rect.y < contentHeight {
			contentHits = append(contentHits, hit)
		}
	}
	hits = contentHits
	for len(lines) < max(0, m.height-3) {
		lines = append(lines, "")
	}
	buttons(footerLabels, footerActions)
	lines = append(lines, theme.Muted.Render(hint), "")
	// Invisible clipped controls are never clickable after a narrow resize.
	visible := hits[:0]
	for _, hit := range hits {
		if hit.rect.y >= 0 && hit.rect.y < m.height && hit.rect.w > 0 {
			visible = append(visible, hit)
		}
	}
	return lines, visible
}

func (m *HostPickerModel) View() tea.View {
	lines, _ := m.layout()
	v := tea.NewView(fitLines(strings.Join(lines, "\n"), m.width, m.height))
	v.AltScreen = true
	if m.config.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func RunHostPicker(ctx context.Context, cfg config.Config, opts HostPickerOptions) error {
	m := NewHostPicker(ctx, cfg, opts)
	defer m.Close()
	_, err := RunModel(ctx, m)
	if err != nil {
		return err
	}
	return m.Err()
}

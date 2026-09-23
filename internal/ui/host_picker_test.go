package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/hostinventory"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

func fixtureHostPicker(t *testing.T) *HostPickerModel {
	t.Helper()
	dir := t.TempDir()
	for _, variable := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(variable, filepath.Join(dir, variable))
	}
	cfg := config.Defaults()
	cfg.Path = filepath.Join(dir, "config.toml")
	cfg.MaxParallel = 1
	m := NewHostPicker(context.Background(), cfg, HostPickerOptions{})
	t.Cleanup(m.Close)
	m.discover = func(context.Context, string) (hostinventory.Inventory, error) {
		return hostinventory.Inventory{Source: "ssh", Complete: true, Candidates: []hostinventory.Candidate{{Alias: "alpha", Selectable: true, Status: "candidate"}, {Alias: "beta", Selectable: true, Status: "candidate"}, {Alias: "guarded", Status: "unknown", Reason: "Conditional Include"}}}, nil
	}
	m.Update(m.Init()())
	return m
}

func TestHostPickerExplicitSelectionAndTypingOwnership(t *testing.T) {
	m := fixtureHostPicker(t)
	if len(m.selected) != 0 {
		t.Fatal("hosts were preselected")
	}
	m.Update(key('/'))
	for _, r := range "jq/?" {
		m.Update(key(r))
	}
	if m.filter.Value() != "jq/?" || m.ctx.Err() != nil || len(m.selected) != 0 {
		t.Fatal("filter invoked navigation", m.filter.Value())
	}
	m.Update(special(tea.KeyEscape))
	if m.filter.Value() != "" || m.filtering {
		t.Fatal("filter did not clear")
	}
	m.Update(special(tea.KeySpace))
	if len(m.selected) != 1 || m.selected["alpha"].SSH != "alpha" {
		t.Fatal(m.selected)
	}
	m.Update(special(tea.KeyEnd))
	m.Update(special(tea.KeySpace))
	if len(m.selected) != 1 || m.err == nil {
		t.Fatal("uncertain alias became selected")
	}
	m.Update(ctrl('n'))
	m.Update(tea.PasteMsg{Content: "manual-lab"})
	if m.stage != "manual" || m.alias.Value() != "manual-lab" {
		t.Fatal("paste submitted manual form")
	}
	m.Update(special(tea.KeyEnter))
	if m.stage != "list" || len(m.selected) != 2 || m.selected["manual-lab"].SSH != "manual-lab" {
		t.Fatal(m.stage, m.selected)
	}
}

func TestHostPickerRejectsLateMessagesAndReturnsToParent(t *testing.T) {
	m := fixtureHostPicker(t)
	other := fixtureHostPicker(t)
	m.Update(hostDiscovered{owner: other, generation: m.generation, err: errors.New("wrong owner")})
	m.Update(hostDiscovered{owner: m, generation: m.generation - 1, err: errors.New("old generation")})
	if m.err != nil {
		t.Fatal(m.err)
	}
	m.stage = "building"
	m.Update(hostDiscovered{owner: m, generation: m.generation, err: errors.New("late discovery")})
	if m.stage != "building" || m.err != nil {
		t.Fatal("discovery replaced the active registration")
	}
	m.stage = "list"
	m.SetEmbedded(true)
	_, cmd := m.Update(special(tea.KeyEscape))
	if done, ok := cmd().(WorkflowDoneMsg); !ok || !errors.Is(done.Err, ErrCancelled) || done.Changed {
		t.Fatal("embedded picker quit its parent", done)
	}
}

type hostPickerRunner struct{ calls []string }

func (f *hostPickerRunner) Run(_ context.Context, host config.Host, args []string, _ []byte) (transport.Result, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, host.ID+":"+joined)
	if host.ID == "beta" {
		return transport.Result{Code: 255}, errors.New("network unreachable")
	}
	if strings.Contains(joined, "crontab -l") {
		return transport.Result{Stdout: []byte("0 9 * * * echo fixture\n")}, nil
	}
	if strings.Contains(joined, "uname -s") {
		return transport.Result{Stdout: []byte("Linux\nUTC\n")}, nil
	}
	return transport.Result{}, fmt.Errorf("unexpected fixture command: %s", joined)
}

func drainHostPicker(m *HostPickerModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			drainHostPicker(m, child)
		}
		return
	}
	_, next := m.Update(msg)
	drainHostPicker(m, next)
}

func TestHostPickerSelectionSaveAndPartialReadiness(t *testing.T) {
	m := fixtureHostPicker(t)
	runner := &hostPickerRunner{}
	m.service.Runner = runner
	m.toggle("alpha")
	m.toggle("beta")
	_, cmd := m.Update(ctrl('s'))
	if m.stage != "building" || len(runner.calls) > 0 {
		t.Fatal("preparing registrations ran remote operations", m.stage, runner.calls)
	}
	if _, err := os.Stat(m.config.Path); !os.IsNotExist(err) {
		t.Fatal("selection wrote the config before its effect ran")
	}
	drainHostPicker(m, cmd)
	if !m.changed || m.stage != "result" || m.err != nil {
		t.Fatal(m.stage, m.err)
	}
	if !strings.Contains(m.checks["alpha"], "Ready") || !strings.Contains(m.checks["beta"], "network unreachable") {
		t.Fatal(m.checks)
	}
	cfg, err := config.Load(m.config.Path)
	if err != nil || len(cfg.Hosts) != 2 {
		t.Fatal("unreachable host was not preserved", cfg.Hosts, err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "pueue") || strings.HasSuffix(call, "crontab -") {
			t.Fatal("setup checked optional Pueue or modified jobs", call)
		}
	}
	before := len(runner.calls)
	m.authenticating = true
	_, cmd = m.Update(hostAuthenticated{owner: m, host: "alpha"})
	drainHostPicker(m, cmd)
	for _, call := range runner.calls[before:] {
		if !strings.HasPrefix(call, "alpha:") {
			t.Fatal("authentication retried a different host", call)
		}
	}
	if m.authenticating || len(runner.calls) <= before {
		t.Fatal("authentication did not retry its host")
	}
	m.SetEmbedded(true)
	_, cmd = m.Update(special(tea.KeyEnter))
	if done, ok := cmd().(WorkflowDoneMsg); !ok || !done.Changed || done.Err != nil {
		t.Fatal(done)
	}
}

func TestHostPickerMouseUsesVisibleGeometry(t *testing.T) {
	m := fixtureHostPicker(t)
	_, hits := m.layout()
	var checkbox, row hitRegion
	for _, hit := range hits {
		if hit.id == "toggle:beta" {
			checkbox = hit
		}
		if hit.id == "row:beta" {
			row = hit
		}
	}
	m.Update(tea.MouseClickMsg{X: 10, Y: row.rect.y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 10, Y: row.rect.y, Button: tea.MouseLeft})
	if m.index != 1 || len(m.selected) != 0 {
		t.Fatal("row click did more than select", m.index, m.selected)
	}
	m.Update(tea.MouseClickMsg{X: checkbox.rect.x, Y: checkbox.rect.y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: checkbox.rect.x, Y: checkbox.rect.y, Button: tea.MouseLeft})
	if len(m.selected) != 1 || m.selected["beta"].SSH != "beta" {
		t.Fatal(m.selected)
	}
	m.Update(tea.MouseClickMsg{X: checkbox.rect.x, Y: checkbox.rect.y, Button: tea.MouseLeft})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.MouseReleaseMsg{X: checkbox.rect.x, Y: checkbox.rect.y, Button: tea.MouseLeft})
	if len(m.selected) != 1 {
		t.Fatal("resized stale press activated")
	}
	for _, size := range [][2]int{{80, 24}, {40, 12}, {12, 5}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines, hits := m.layout()
		rendered := fitLines(strings.Join(lines, "\n"), size[0], size[1])
		for _, line := range strings.Split(rendered, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("overflow", size, line)
			}
		}
		for _, hit := range hits {
			if hit.rect.y >= size[1] || hit.rect.x+hit.rect.w > size[0] {
				t.Fatal("invisible control remains clickable", size, hit)
			}
		}
	}
}

func TestHostPickerCancellationWaitsForDispatchedSaveResult(t *testing.T) {
	for _, completedWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(completedWrite), func(t *testing.T) {
			m := fixtureHostPicker(t)
			m.SetEmbedded(true)
			m.toggle("alpha")
			_, build := m.Update(ctrl('s'))
			_, save := m.Update(build())
			var outcome tea.Msg
			if completedWrite {
				outcome = save() // The write finishes before its UI result arrives.
			}
			_, close := m.Update(ctrl('c'))
			if close != nil || !m.cancelPending || !m.Changed() {
				t.Fatal("closed before the dispatched save could settle")
			}
			if !completedWrite {
				outcome = save()
			}
			_, close = m.Update(outcome)
			if close == nil {
				t.Fatal("save completion did not release cancelled workflow")
			}
			done, ok := close().(WorkflowDoneMsg)
			if !ok || !done.Changed || !errors.Is(done.Err, ErrCancelled) || done.Message == "" {
				t.Fatal("parent cannot reconcile possible config changes", done)
			}
			_, err := os.Stat(m.config.Path)
			if completedWrite && err != nil || !completedWrite && !os.IsNotExist(err) {
				t.Fatal("unexpected persisted config", err)
			}
			if _, duplicate := m.Update(ctrl('c')); duplicate != nil {
				t.Fatal("duplicate completion can close a later workflow")
			}
		})
	}
}

func TestHostPickerSupersededPlanCannotRegisterOldSelection(t *testing.T) {
	m := fixtureHostPicker(t)
	m.toggle("alpha")
	_, old := m.Update(ctrl('s'))
	m.Update(special(tea.KeyEscape))
	m.toggle("alpha")
	m.toggle("beta")
	_, current := m.Update(ctrl('s'))
	if _, save := m.Update(old()); save != nil || m.stage != "building" {
		t.Fatal("stale preparation applied an old host selection")
	}
	_, save := m.Update(current())
	if save == nil || len(m.plan.Hosts) != 1 || m.plan.Hosts[0].ID != "beta" {
		t.Fatal("latest host selection was lost", m.plan.Hosts)
	}
}

func TestHostPickerMouseOverrideAndEmbeddedOffset(t *testing.T) {
	picker := fixtureHostPicker(t)
	picker.SetMouse(true)
	_, hits := picker.layout()
	var target hitRegion
	for _, h := range hits {
		if h.id == "toggle:beta" {
			target = h
		}
	}
	picker.Update(tea.MouseClickMsg{X: target.rect.x, Y: target.rect.y, Button: tea.MouseLeft})
	picker.SetMouse(false)
	picker.Update(tea.MouseReleaseMsg{X: target.rect.x, Y: target.rect.y, Button: tea.MouseLeft})
	if len(picker.selected) != 0 || picker.press != "" || picker.View().MouseMode == tea.MouseModeCellMotion {
		t.Fatal("mouse override retained capture or a pending activation")
	}
	picker.SetMouse(true)
	dashboard := fixtureDashboard(t)
	dashboard.mouse = true
	dashboard.child = picker
	dashboard.childView = dashboard.view
	picker.SetEmbedded(true)
	dashboard.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_, hits = picker.layout()
	for _, h := range hits {
		if h.id == "toggle:beta" {
			target = h
		}
	}
	before := dashboard.selectedKey
	dashboard.Update(tea.MouseClickMsg{X: target.rect.x, Y: target.rect.y + 2, Button: tea.MouseLeft})
	dashboard.Update(tea.MouseReleaseMsg{X: target.rect.x, Y: target.rect.y + 2, Button: tea.MouseLeft})
	if picker.selected["beta"].ID != "beta" || dashboard.selectedKey != before {
		t.Fatal("embedded click used the wrong origin or leaked to dashboard")
	}
	// A wheel/keyboard selection change must revoke an earlier footer press.
	picker.press = "authenticate"
	picker.Update(tea.MouseWheelMsg{X: 1, Y: 5, Button: tea.MouseWheelDown})
	if picker.press != "" {
		t.Fatal("wheel retained authority for a different host")
	}
	picker.press = "review"
	picker.Update(key('j'))
	if picker.press != "" {
		t.Fatal("keyboard navigation retained a mouse action")
	}
}

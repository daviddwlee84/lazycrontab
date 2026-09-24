package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func sourceFixture(readonly bool) *SourceView {
	raw := "# untouched header\nPATH=/usr/bin:/bin\n" + strings.Repeat("# long "+strings.Repeat("界", 80)+"\n", 20) + "0 9 * * * echo TARGET\n"
	snap := service.Snapshot{Host: "lab", Source: "user", Exists: true, Document: document.Parse(raw, schedule.System, false)}
	return NewSourceView(snap, config.Source{ID: "user", Host: "lab", Kind: "user", ReadOnly: readonly}, "dark", true, "V")
}
func TestRawSourceViewerScrollSearchAndTarget(t *testing.T) {
	m := sourceFixture(false)
	m.SetEmbedded(true)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(special(tea.KeyRight))
	m.Update(special(tea.KeyPgDown))
	if m.left == 0 || m.top == 0 {
		t.Fatal("raw source cannot pan or scroll")
	}
	m.Update(key('/'))
	m.Update(key('V'))
	if !m.filtering || m.filter.Value() != "V" || m.editingRequested {
		t.Fatal("search invoked edit")
	}
	m.Update(special(tea.KeyEscape))
	m.Update(key('/'))
	m.Update(tea.PasteMsg{Content: "TARGET"})
	m.Update(special(tea.KeyEnter))
	if len(m.matches) != 1 || m.matches[0] != 22 {
		t.Fatal(m.matches)
	}
	_, cmd := m.Update(key('V'))
	request := cmd().(RawSourceEditMsg)
	if request.Host != "lab" || request.Source != "user" || request.Owner != m {
		t.Fatal(request)
	}
	_, cmd = m.Update(key('V'))
	if cmd != nil {
		t.Fatal("repeat launched editor twice")
	}
}
func TestRawSourceReadonlyResizeAndMouse(t *testing.T) {
	m := sourceFixture(true)
	_, cmd := m.Update(key('V'))
	if cmd != nil || !strings.Contains(m.status, "Read-only") {
		t.Fatal("readonly edit accepted")
	}
	for _, size := range [][2]int{{120, 32}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) > size[1] {
			t.Fatal("height overflow")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("width overflow")
			}
		}
		for _, h := range m.hits() {
			if h.id == "edit" {
				t.Fatal("readonly edit button")
			}
			if h.rect.y >= len(lines) || !strings.Contains(ansi.Strip(lines[h.rect.y]), "Back") {
				t.Fatal("invisible mouse hit")
			}
		}
	}
	m = sourceFixture(false)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.MouseClickMsg{X: 14, Y: 22})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 14, Y: 22})
	if cmd != nil {
		t.Fatal("resize retained press")
	}
	m.Update(tea.MouseClickMsg{X: 14, Y: 22})
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 14, Y: 22})
	if _, ok := cmd().(RawSourceEditMsg); !ok {
		t.Fatal("edit button failed")
	}
}
func TestRawActionsWorkOnSelectedEmptySourceButNotAmbiguousAll(t *testing.T) {
	m := fixtureDashboard(t)
	m.snapshots["local/user"] = service.Snapshot{Host: "local", Source: "user"}
	m.service.Config.Sources = []config.Source{{ID: "user", Host: "local", Kind: "system", Path: "/etc/crontab"}}
	actionByID := func(id string) action {
		for _, a := range m.actions {
			if a.ID == id {
				return a
			}
		}
		t.Fatal(id)
		return action{}
	}
	if !m.available(actionByID("view-source")) || m.available(actionByID("edit-source")) {
		t.Fatal("readonly empty-source availability")
	}
	m.service.Config.Sources = nil
	if !m.available(actionByID("edit-source")) {
		t.Fatal("empty writable source cannot edit")
	}
	m.scope = 0
	m.filter.SetValue("does-not-match")
	if m.available(actionByID("view-source")) || m.available(actionByID("edit-source")) {
		t.Fatal("All silently chose an unrelated source")
	}
}
func TestRawSourceWorkflowUsesSelectedSourceAndRejectsLateCompletion(t *testing.T) {
	m := fixtureDashboard(t)
	m.factory = func(ctx context.Context, r WorkflowRequest) (tea.Model, error) {
		if r.Host != "local" || r.Source != "user" {
			t.Fatal(r)
		}
		return sourceFixture(false), nil
	}
	cmd := m.act("view-source")
	m.Update(cmd())
	viewer, ok := m.child.(*SourceView)
	if !ok {
		t.Fatal("not a source viewer")
	}
	m.mouse = true
	m.Update(tea.MouseClickMsg{X: 42, Y: 0})
	m.Update(tea.MouseReleaseMsg{X: 42, Y: 0})
	if m.view != "jobs" || m.child != viewer {
		t.Fatal("source viewer leaked header click")
	}
	old := sourceFixture(false)
	_, cmd = m.Update(RawSourceEditMsg{Owner: old, Host: "lab", Source: "user"})
	if cmd != nil || m.child != viewer {
		t.Fatal("stale viewer took terminal")
	}
	_, cmd = m.Update(special(tea.KeyEscape))
	m.Update(cmd())
	if m.child != nil {
		t.Fatal("viewer did not close")
	}
	if m.selectedKey != "local/user/a" {
		t.Fatal("view changed job context")
	}
}

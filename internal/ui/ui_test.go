package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func key(r rune) tea.KeyPressMsg        { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func special(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }
func ctrl(r rune) tea.KeyPressMsg       { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
func TestFormTextOwnershipReviewAndCancel(t *testing.T) {
	applied := 0
	f := NewForm(context.Background(), FormSpec{Title: "fixture", Fields: []Field{{Key: "command", Label: "Command"}}, Build: func(_ context.Context, v map[string]string) (Review, error) { return Review{Text: v["command"]}, nil }, Apply: func(context.Context, map[string]string, Review) (string, error) { applied++; return "saved", nil }})
	defer f.cancel()
	for _, r := range "jkhql/?" {
		f.Update(key(r))
	}
	if f.Values()["command"] != "jkhql/?" {
		t.Fatal(f.Values())
	}
	f.Update(tea.PasteMsg{Content: " pasted\ntext"})
	if f.stage != "edit" || applied != 0 {
		t.Fatal("paste submitted")
	}
	_, cmd := f.Update(ctrl('s'))
	f.Update(cmd())
	if f.stage != "review" {
		t.Fatal(f.stage)
	}
	_, cmd = f.Update(special(tea.KeyEnter))
	if cmd != nil || applied != 0 {
		t.Fatal("Enter approved review")
	}
	f.Update(special(tea.KeyEscape))
	if f.stage != "edit" || !strings.HasPrefix(f.Values()["command"], "jkhql/?") {
		t.Fatal("Back discarded draft")
	}
	_, cmd = f.Update(ctrl('s'))
	f.Update(cmd())
	_, cmd = f.Update(ctrl('s'))
	f.Update(cmd())
	if applied != 1 || !f.result.Submitted {
		t.Fatal(applied, f.result)
	}
}
func TestUnavailableChoicesAndLateDiscovery(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{Fields: []Field{{Key: "runner", Options: []string{"direct", "pueue"}, Value: "direct", Unavailable: map[string]string{"pueue": "not installed"}}}})
	defer f.cancel()
	f.Update(special(tea.KeyRight))
	if f.Values()["runner"] != "direct" {
		t.Fatal("selected disabled Pueue")
	}
	f.loadGeneration = 2
	f.Update(formLoaded{1, []FieldUpdate{{Key: "runner", Options: []string{"wrong"}}}})
	if len(f.spec.Fields[0].Options) != 2 {
		t.Fatal("late discovery accepted")
	}
}
func TestConfirmationEscapeCancelsWithoutEmptyForm(t *testing.T) {
	f := NewForm(context.Background(), FormSpec{})
	defer f.cancel()
	f.stage = "review"
	_, cmd := f.Update(special(tea.KeyEscape))
	if cmd == nil || f.err != ErrCancelled {
		t.Fatal("confirmation opened an empty edit form")
	}
}
func fixtureDashboard(t *testing.T) *dashboard {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{ID: "lab", SSH: "lab"}}
	a, _ := Actions(nil)
	m := &dashboard{ctx: ctx, cancel: cancel, service: service.New(cfg), width: 120, height: 30, actions: a, targets: [][2]string{{"local", "user"}, {"lab", "user"}}, snapshots: map[string]service.Snapshot{}, generations: map[string]int{}, pending: map[string]bool{}, sem: make(chan struct{}, 4), filter: textinput.New(), view: "jobs", scope: 1}
	m.snapshots["local/user"] = service.Snapshot{Host: "local", Source: "user", Observed: time.Now(), Entries: []service.Entry{{Job: document.Job{Metadata: document.Metadata{ID: "a", Name: "專案 é 👩🏽‍💻"}, Enabled: true}, Host: "local", Source: "user"}, {Job: document.Job{Metadata: document.Metadata{ID: "b", Name: "second"}}, Host: "local", Source: "user"}}}
	m.clamp()
	return m
}
func TestStaleRepliesAndStableSelection(t *testing.T) {
	m := fixtureDashboard(t)
	m.selectRow(1)
	m.generations["local/user"] = 2
	m.Update(snapshotMsg{key: "local/user", generation: 1, snapshot: service.Snapshot{Entries: nil}})
	if len(m.rows()) != 2 {
		t.Fatal("stale reply accepted")
	}
	snap := m.snapshots["local/user"]
	snap.Entries[0], snap.Entries[1] = snap.Entries[1], snap.Entries[0]
	m.Update(snapshotMsg{key: "local/user", generation: 2, snapshot: snap})
	if j, _ := m.current(); j.ID != "b" {
		t.Fatal("refresh lost identity")
	}
	m.filter.SetValue("second")
	m.changeScope(2)
	m.changeScope(1)
	if m.filter.Value() != "second" || m.selectedKey != "local/user/b" {
		t.Fatal("scope context lost")
	}
}
func TestFilteringPaletteAndNarrowUnicode(t *testing.T) {
	m := fixtureDashboard(t)
	m.Update(key('/'))
	m.Update(key('q'))
	if !m.filtering || m.filter.Value() != "q" {
		t.Fatal("q quit instead of typing")
	}
	m.Update(special(tea.KeyEscape))
	m.filter.SetValue("second")
	m.Update(key(':'))
	m.Update(key('x'))
	m.Update(special(tea.KeyEscape))
	if m.filter.Value() != "second" {
		t.Fatal("palette lost job filter")
	}
	m.filter.SetValue("")
	for _, size := range [][2]int{{120, 30}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View().Content
		for _, line := range strings.Split(v, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Errorf("overflow at %v: %q", size, line)
			}
		}
	}
}
func TestBindingsRejectCollisions(t *testing.T) {
	if _, e := Actions(map[string]string{"add": "e"}); e == nil {
		t.Fatal("collision accepted")
	}
	if _, e := Actions(map[string]string{"add": "q"}); e == nil {
		t.Fatal("quit rebound")
	}
	a, e := Actions(map[string]string{"add": "N"})
	if e != nil || a[1].Key != "N" {
		t.Fatal(a, e)
	}
}
func TestPaletteKeepsRowActionsAndMouseGeometry(t *testing.T) {
	m := fixtureDashboard(t)
	m.Update(key(':'))
	for _, r := range "Edit job" {
		m.Update(key(r))
	}
	items := m.paletteActions()
	if len(items) != 1 || items[0].ID != "edit" {
		t.Fatal("palette query hid the selected job", items)
	}
	m.Update(special(tea.KeyEscape))
	m.mouse = true
	m.selectRow(1)
	m.Update(tea.MouseClickMsg{X: 30, Y: 4})
	if j, _ := m.current(); j.ID != "a" {
		t.Fatal("first displayed row selected wrong job")
	}
	m.Update(tea.MouseClickMsg{X: 2, Y: 4})
	if m.scope != 0 {
		t.Fatal("All scope hit rectangle is wrong")
	}
}

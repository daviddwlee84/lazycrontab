package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMouseConfigurationReloadUpdatesRetainedSurfaces(t *testing.T) {
	m := fixtureDashboard(t)
	m.configPath = filepath.Join(t.TempDir(), "config.toml")
	m.mouse = true
	m.act("playground")
	m.act("jobs")
	f := NewForm(m.ctx, FormSpec{Mouse: true, Fields: []Field{{Key: "command", Value: "keep this draft"}}})
	defer f.cancel()
	f.SetEmbedded(true)
	f.help = NewHelpBrowser("", true)
	m.child, m.childView = f, "jobs"
	for _, enabled := range []bool{false, true} {
		m.mousePress = "tab:playground"
		m.playground.pressed = "use"
		f.mousePress, f.help.press = "review", "back"
		if err := os.WriteFile(m.configPath, []byte(fmt.Sprintf("mouse = %t\n", enabled)), 0600); err != nil {
			t.Fatal(err)
		}
		// Only handle the config reload message. Do not run its source-read Cmd.
		m.Update(handoffMsg{})
		if m.mouse != enabled || m.playground.options.Mouse != enabled || f.spec.Mouse != enabled || f.help.mouse != enabled {
			t.Fatal("config mouse preference did not reach existing surfaces", enabled)
		}
		if m.mousePress != "" || m.playground.pressed != "" || f.mousePress != "" || f.help.press != "" {
			t.Fatal("config reload retained an old click")
		}
		if (m.View().MouseMode == tea.MouseModeCellMotion) != enabled || f.Values()["command"] != "keep this draft" {
			t.Fatal("reload changed draft or retained terminal capture")
		}
	}
}

func TestMouseToggleKeyIsFreeAndAltNavigationHasNoDefaultBinding(t *testing.T) {
	m := fixtureDashboard(t)
	for _, enabled := range []bool{true, false} {
		m.playground = nil
		m.mouse = enabled
		m.Update(key('m'))
		if m.mouse != enabled {
			t.Fatal("m still toggles mouse capture")
		}
		m.act("playground")
		m.Update(key('m'))
		if m.mouse != enabled || m.playground.options.Mouse != enabled {
			t.Fatal("Playground still has a mouse override")
		}
		m.act("jobs")
	}
	for _, digit := range "123" {
		m.Update(tea.KeyPressMsg{Code: digit, Mod: tea.ModAlt})
		if m.view != "jobs" {
			t.Fatal("Alt digit still switches views")
		}
	}
	text := m.View().Content + strings.Join(m.helpLines(), "\n")
	if strings.Contains(text, "Alt+") || strings.Contains(text, "m mouse") {
		t.Fatal("removed shortcuts are still advertised")
	}
	var err error
	m.actions, err = Actions(map[string]string{"guide": "m"})
	if err != nil {
		t.Fatal("m cannot be assigned through config", err)
	}
	m.Update(key('m'))
	if _, ok := m.child.(*HelpBrowser); !ok {
		t.Fatal("m was intercepted instead of running its configured action")
	}
}

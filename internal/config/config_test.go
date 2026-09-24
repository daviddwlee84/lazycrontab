package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestXDGAndReadOnlyLoad(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("LAZYCRONTAB_CONFIG", "")
	c, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	if c.Path != filepath.Join(base, "lazycrontab", "config.toml") {
		t.Fatal(c.Path)
	}
	if _, e = os.Stat(filepath.Dir(c.Path)); !os.IsNotExist(e) {
		t.Fatal("loading created config directory")
	}
	if _, e = Load(c.Path); e == nil {
		t.Fatal("explicit missing file accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	p, _ := Base("config")
	if !filepath.IsAbs(p) {
		t.Fatal(p)
	}
}
func TestEntityEditsPreserveCommentsAndClearValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := "# personal\nmouse = false\n\n[[hosts]]\nid = 'lab'\nssh = 'old' # keep this\ntimezone = 'UTC'\n# remark\n\n[keys]\nadd = 'N' # keep key\n"
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	if e := SaveEntity(path, "hosts", "lab", "", Host{ID: "lab", SSH: "new"}, false); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	for _, text := range []string{"# personal", "# keep this", "# remark", "[keys]\nadd = 'N' # keep key"} {
		if !strings.Contains(string(b), text) {
			t.Errorf("lost %s\n%s", text, b)
		}
	}
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	h, _ := c.Host("lab")
	if h.SSH != "new" || h.Timezone != "" || c.Mouse {
		t.Fatal(c)
	}
	if e = SaveEntity(path, "sources", "worker", "local", Source{ID: "worker", Host: "local", Kind: "file", Dialect: "supercronic", Path: "/tmp/cron"}, false); e != nil {
		t.Fatal(e)
	}
	if e = SaveEntity(path, "hosts", "lab", "", Host{}, true); e != nil {
		t.Fatal(e)
	}
	c, e = Load(path)
	if e != nil || c.Keys["add"] != "N" || len(c.Sources) != 1 {
		t.Fatalf("remove destroyed neighboring table: %+v %v", c, e)
	}
}
func TestValidationRejectsWrongDialectAndRelativeSource(t *testing.T) {
	for _, src := range []Source{{ID: "x", Host: "local", Kind: "user", Dialect: "supercronic"}, {ID: "x", Host: "local", Kind: "file", Path: "relative"}} {
		c := Defaults()
		c.Sources = []Source{src}
		if c.Validate() == nil {
			t.Fatal(src)
		}
	}
}

func TestMousePreferenceDefaultsOnlyWhenOmitted(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	for _, name := range []string{"LAZYCRONTAB_CONFIG", "LAZYCRONTAB_HOST", "LAZYCRONTAB_SOURCE", "LAZYCRONTAB_LOCALE"} {
		t.Setenv(name, "")
	}
	if !Defaults().Mouse {
		t.Fatal("mouse must be enabled in the built-in defaults")
	}
	missing, err := Load("")
	if err != nil || !missing.Mouse {
		t.Fatal("missing XDG config did not keep the default", missing.Mouse, err)
	}
	if _, err := os.Stat(filepath.Join(base, "lazycrontab")); !os.IsNotExist(err) {
		t.Fatal("loading default mouse preference created a config", err)
	}
	for _, test := range []struct {
		name, content string
		want          bool
	}{
		{"omitted", "# mouse inherits the default\ntheme = 'dark'\n", true},
		{"explicit false", "mouse = false\n", false},
		{"explicit true", "mouse = true\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil || loaded.Mouse != test.want {
				t.Fatal("explicit bool was overwritten by defaults", loaded.Mouse, err)
			}
			// This is the same serialization and private atomic write used by
			// config initialization. False must remain present in the TOML.
			encoded, err := toml.Marshal(loaded)
			if err != nil {
				t.Fatal(err)
			}
			assignment := "mouse = false"
			if test.want {
				assignment = "mouse = true"
			}
			if !strings.Contains(string(encoded), assignment) {
				t.Fatalf("mouse preference omitted during serialization: %s", encoded)
			}
			saved := filepath.Join(t.TempDir(), "saved", "config.toml")
			if err := AtomicWrite(saved, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			roundTrip, err := Load(saved)
			if err != nil || roundTrip.Mouse != test.want {
				t.Fatal("mouse preference changed across save/load", roundTrip.Mouse, err)
			}
			// A later explicit false must replace either an omitted/default true
			// or a previously loaded value when the configuration is re-read.
			if err := AtomicWrite(saved, []byte("mouse = false\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if reloaded, err := Load(saved); err != nil || reloaded.Mouse {
				t.Fatal("explicit false lost on reload", reloaded.Mouse, err)
			}
		})
	}
}

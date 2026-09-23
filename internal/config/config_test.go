package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

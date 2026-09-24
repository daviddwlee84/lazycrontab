package completioncache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

func fixture(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := config.Defaults()
	c.Path = filepath.Join(t.TempDir(), "config.toml")
	c.Hosts = []config.Host{{ID: "lab", SSH: "test-alias"}}
	c.Sources = []config.Source{{ID: "jobs", Host: "lab", Kind: "file", Path: "/srv/tasks.cron", Dialect: "supercronic"}}
	return c
}

func TestCacheRoundTripPrivateMinimalAndReadOnly(t *testing.T) {
	c := fixture(t)
	entries := []Entry{{ID: "abc", Name: "Nightly backup 中文", Enabled: true}, {ID: "line-4-xyz", Name: "disabled", ReadOnly: true}}
	Save(c, "lab", "jobs", entries)
	if got := Load(c, "lab", "jobs"); !reflect.DeepEqual(got, entries) {
		t.Fatal(got)
	}
	path, _, err := location(c, "lab", "jobs")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"command", "script", "environment", "test-alias", "/srv/tasks.cron"} {
		if strings.Contains(string(before), value) {
			t.Fatal("cache contains unnecessary source details", value)
		}
	}
	for _, p := range []string{path, filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0700)
		if p == path {
			want = 0600
		}
		if info.Mode().Perm() != want {
			t.Fatal(p, info.Mode())
		}
	}
	info, _ := os.Stat(path)
	Load(c, "lab", "jobs")
	afterInfo, _ := os.Stat(path)
	after, _ := os.ReadFile(path)
	if !info.ModTime().Equal(afterInfo.ModTime()) || string(before) != string(after) {
		t.Fatal("completion load changed its cache")
	}
	Invalidate(c, "lab", "jobs")
	if len(Load(c, "lab", "jobs")) != 0 {
		t.Fatal("invalidated cache is still visible")
	}
}

func TestCacheScopeAndEffectiveSourceIdentity(t *testing.T) {
	c := fixture(t)
	Save(c, "lab", "jobs", []Entry{{ID: "private-target-job"}})
	for _, mutate := range []func(*config.Config){
		func(v *config.Config) { v.Path += ".another" },
		func(v *config.Config) { v.Hosts[0].SSH = "different-target" },
		func(v *config.Config) { v.Sources[0].Path = "/srv/other.cron" },
		func(v *config.Config) { v.Sources[0].Dialect = "system" },
		func(v *config.Config) { v.Sources[0].Kind = "user" },
		func(v *config.Config) { v.Sources[0].ReadOnly = true },
	} {
		other := c
		other.Hosts = append([]config.Host(nil), c.Hosts...)
		other.Sources = append([]config.Source(nil), c.Sources...)
		mutate(&other)
		if got := Load(other, "lab", "jobs"); len(got) != 0 {
			t.Fatal("different target reused completion cache", got)
		}
	}
	for _, pair := range [][2]string{{"all", "jobs"}, {"lab", "all"}, {"unseen", "user"}, {"lab", "missing"}, {"local", "user"}} {
		if got := Load(c, pair[0], pair[1]); len(got) != 0 {
			t.Fatal("unseen target reused a cache", pair, got)
		}
	}
	c.Path = filepath.Join(filepath.Dir(c.Path), "nested", "..", "config.toml")
	if len(Load(c, "lab", "jobs")) != 1 {
		t.Fatal("normalized effective config path lost its scope")
	}
}

func TestCacheRejectsStaleFutureMalformedAndOversizedFiles(t *testing.T) {
	c := fixture(t)
	Save(c, "lab", "jobs", []Entry{{ID: "cached"}})
	path, identity, _ := location(c, "lab", "jobs")
	valid := record{version, identity, time.Now().UTC(), []Entry{{ID: "cached"}}}
	for name, modify := range map[string]func(*record){
		"stale":     func(r *record) { r.Observed = time.Now().Add(-25 * time.Hour) },
		"future":    func(r *record) { r.Observed = time.Now().Add(time.Hour) },
		"version":   func(r *record) { r.Version = 900 },
		"identity":  func(r *record) { r.Identity = "other-source" },
		"controls":  func(r *record) { r.Entries = []Entry{{ID: "bad\ncompletion"}} },
		"duplicate": func(r *record) { r.Entries = []Entry{{ID: "same"}, {ID: "same"}} },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			modify(&r)
			content, _ := json.Marshal(r)
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			if got := Load(c, "lab", "jobs"); len(got) != 0 {
				t.Fatal("invalid cache yielded suggestions", got)
			}
		})
	}
	for _, content := range []string{"not JSON", "{}{}", strings.Repeat("x", maxBytes+1)} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if len(Load(c, "lab", "jobs")) != 0 {
			t.Fatal("untrusted file was accepted")
		}
	}
}

func TestCacheMissAndFailureNeverCreateStateDuringLoad(t *testing.T) {
	c := fixture(t)
	root := os.Getenv("XDG_CACHE_HOME")
	Load(c, "lab", "jobs")
	Invalidate(c, "lab", "jobs")
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("cache miss created state", files, err)
	}
	if err := os.WriteFile(filepath.Join(root, "lazycrontab"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	Save(c, "lab", "jobs", []Entry{{ID: "not-written"}})
	if len(Load(c, "lab", "jobs")) != 0 {
		t.Fatal("blocked cache yielded data")
	}
}

func TestCacheRejectsSymlinksAndNonPrivateFiles(t *testing.T) {
	c := fixture(t)
	Save(c, "lab", "jobs", []Entry{{ID: "cached"}})
	path, _, _ := location(c, "lab", "jobs")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if len(Load(c, "lab", "jobs")) != 0 {
		t.Fatal("non-private cache accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "other-file")
	if err := os.WriteFile(external, []byte("external sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if len(Load(c, "lab", "jobs")) != 0 {
		t.Fatal("symlink cache accepted")
	}
	Save(c, "lab", "jobs", []Entry{{ID: "cached"}})
	content, _ := os.ReadFile(external)
	if string(content) != "external sentinel" {
		t.Fatal("cache save followed a file symlink")
	}
}

func TestCacheSaveFiltersUnusableIDsAndDropsOversizedSnapshots(t *testing.T) {
	c := fixture(t)
	Save(c, "lab", "jobs", []Entry{{ID: "same", Name: "first"}, {ID: "same", Name: "second"}, {ID: "bad\nID"}, {ID: "controls-name", Name: "unsafe\x1b[31m"}, {ID: "okay"}})
	if got := Load(c, "lab", "jobs"); len(got) != 1 || got[0].ID != "okay" {
		t.Fatal("unsafe or duplicate entries were suggested", got)
	}
	entries := make([]Entry, 300)
	for i := range entries {
		entries[i] = Entry{ID: fmt.Sprintf("large-%d", i), Name: strings.Repeat("x", 4096)}
	}
	Save(c, "lab", "jobs", entries)
	if got := Load(c, "lab", "jobs"); len(got) != 0 {
		t.Fatal("oversized snapshot left earlier suggestions visible", got)
	}
}

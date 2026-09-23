package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/hostinventory"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

func TestHostDiscoveryAndSelectedImportStayOffline(t *testing.T) {
	dir, _ := isolated(t)
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ssh", "config"), []byte("Host alpha beta # ignored\nHost *\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "ssh-called")
	t.Setenv("FIXTURE_SSH_MARKER", marker)
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte("#!/bin/sh\ntouch \"$FIXTURE_SSH_MARKER\"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("hosts", "discover", "--json")
	var inv hostinventory.Inventory
	if err != nil || json.Unmarshal([]byte(out), &inv) != nil || len(inv.Candidates) != 2 {
		t.Fatal(out, err)
	}
	if _, err := invoke("hosts", "import", "--alias", "beta", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("")
	if err != nil || len(cfg.Hosts) != 1 || cfg.Hosts[0].SSH != "beta" {
		t.Fatal(cfg.Hosts, err)
	}
	if _, err := invoke("hosts", "import", "--alias", "missing", "--yes"); err == nil {
		t.Fatal("missing alias imported")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("discovery or noninteractive registration contacted SSH")
	}
	for _, args := range [][]string{{"hosts", "discover", "--from", "bad"}, {"hosts", "add", "--from", "bad", "--json"}, {"hosts", "import", "--from", "bad"}} {
		if _, err := invoke(args...); err == nil {
			t.Fatal("invalid provider accepted", args)
		}
	}
}

func TestDevImportRejectsConflictingAlias(t *testing.T) {
	dir, _ := isolated(t)
	dev := "#!/bin/sh\nprintf '%s' '{\"schema_version\":1,\"kind\":\"ssh_list\",\"complete\":false,\"aliases\":[{\"name\":\"alpha\",\"status\":\"active\",\"selectable\":true},{\"name\":\"conflict\",\"status\":\"conflict\",\"selectable\":false,\"definitions\":[{\"alias\":\"must-not-be-scraped\"}]}]}'\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "dev"), []byte(dev), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("hosts", "import", "--from", "dev", "--yes", "--json")
	if err != nil || !strings.Contains(out, "alpha") || strings.Contains(out, "conflict") || strings.Contains(out, "must-not") {
		t.Fatal(out, err)
	}
	if _, err := invoke("hosts", "import", "--from", "dev", "--alias", "conflict", "--yes"); err == nil {
		t.Fatal("conflicting alias imported")
	}
}

func TestEmbeddedSourceFormSharesRegistrationValidation(t *testing.T) {
	dir, _ := isolated(t)
	cfg := config.Defaults()
	cfg.Path = filepath.Join(dir, "source-config.toml")
	cfg.Theme = "light"
	spec, values := entityFormSpec(cfg, "sources", "add", "", "local", config.Host{}, config.Source{Host: "local", Kind: "file", Dialect: "supercronic"}, true)
	values["id"], values["path"] = "worker", "/srv/worker/crontab"
	review, err := spec.Build(context.Background(), values)
	if err != nil || spec.Theme != "light" {
		t.Fatal(review, spec.Theme, err)
	}
	if _, err := spec.Apply(context.Background(), values, review); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(cfg.Path)
	if err != nil || len(loaded.Sources) != 1 || loaded.Sources[0].Path != values["path"] {
		t.Fatal(loaded.Sources, err)
	}
	values["path"] = "relative/path"
	if _, err := spec.Build(context.Background(), values); err == nil {
		t.Fatal("embedded form bypassed source path validation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model, err := newSourceModel(ctx, cfg, "local")
	if _, ok := model.(*ui.Form); err != nil || !ok {
		t.Fatal("source workflow did not embed shared form", model, err)
	}
	if _, err := newSourceModel(ctx, cfg, "all"); err == nil {
		t.Fatal("source write was allowed for all hosts")
	}
}

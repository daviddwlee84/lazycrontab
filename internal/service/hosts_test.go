package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

func TestHostRegistrationPreservesConfigAndRechecksRevision(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	c := config.Defaults()
	c.Path = filepath.Join(dir, "config.toml")
	raw := "# keep this comment\nmouse = false\n\n[keys]\nadd = 'N' # keep this too\n"
	if err := os.WriteFile(c.Path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	hosts := []config.Host{{ID: "lab", SSH: "lab"}, {ID: "worker", SSH: "worker-alias"}}
	plan, err := PlanHosts(c, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.Path, []byte(raw+"# edited elsewhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ApplyHosts(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("stale plan applied", err)
	}
	plan, err = PlanHosts(c, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if err = ApplyHosts(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(c.Path)
	if err != nil || !strings.HasPrefix(string(got), raw+"# edited elsewhere\n") {
		t.Fatal(string(got), err)
	}
	loaded, err := config.Load(c.Path)
	if err != nil || len(loaded.Hosts) != 2 || loaded.Mouse {
		t.Fatal(loaded, err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "state", "lazycrontab", "config-backups", "*"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	if _, err = PlanHosts(loaded, []config.Host{{ID: "duplicate", SSH: "lab"}}); err == nil {
		t.Fatal("duplicate destination accepted")
	}
}

func TestHostRegistrationRequiresUnchangedAbsenceAndCancellation(t *testing.T) {
	c := config.Defaults()
	c.Path = filepath.Join(t.TempDir(), "config.toml")
	plan, err := PlanHosts(c, []config.Host{{ID: "lab", SSH: "lab"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ApplyHosts(ctx, plan); err == nil {
		t.Fatal("cancelled registration applied")
	}
	if err := os.WriteFile(c.Path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyHosts(context.Background(), plan); err == nil {
		t.Fatal("newly created file did not invalidate review")
	}
}

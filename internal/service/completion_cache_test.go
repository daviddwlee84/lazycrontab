package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/completioncache"
	"github.com/daviddwlee84/lazycrontab/internal/document"
)

func TestSnapshotCachesOnlyCompletionMetadata(t *testing.T) {
	raw := "PRIVATE_ENV=must-never-be-cached\n" + document.Marker + `{"v":1,"id":"known","name":"Daily backup"}` + "\n* * * * * echo secret-command\n"
	s, cron := fixtureService(t, raw)
	if _, err := s.Snapshot(context.Background(), "local", "user"); err != nil {
		t.Fatal(err)
	}
	entries := completioncache.Load(s.Config, "local", "user")
	if len(entries) != 1 || entries[0].ID != "known" || entries[0].Name != "Daily backup" || !entries[0].Enabled {
		t.Fatal(entries)
	}
	files, err := filepath.Glob(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lazycrontab", "completion", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil || strings.Contains(string(content), "secret-command") || strings.Contains(string(content), "PRIVATE_ENV") {
		t.Fatal(string(content), err)
	}
	cron.failRead = true
	if _, err := s.Snapshot(context.Background(), "local", "user"); err == nil {
		t.Fatal("fixture read should fail")
	}
	if got := completioncache.Load(s.Config, "local", "user"); len(got) != 0 {
		t.Fatal("known read failure retained completion IDs", got)
	}
}

func TestApplyRefreshesCompletionAndUnknownWriteInvalidates(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, cron := fixtureService(t, document.Marker+`{"v":1,"id":"remove-me"}`+"\n* * * * * echo before\n")
		snap, err := s.Snapshot(context.Background(), "local", "user")
		if err != nil {
			t.Fatal(err)
		}
		if len(completioncache.Load(s.Config, "local", "user")) != 1 {
			t.Fatal("snapshot did not populate cache")
		}
		plan, err := s.PlanRawSource(context.Background(), snap, document.Marker+`{"v":1,"id":"new-job","name":"replacement"}`+"\n* * * * * echo after\n")
		if err != nil {
			t.Fatal(err)
		}
		cron.failWrite = fail
		receipt, err := s.Apply(context.Background(), plan)
		entries := completioncache.Load(s.Config, "local", "user")
		if fail {
			if err == nil || receipt.Status != "unknown" || len(entries) != 0 {
				t.Fatal("unknown write retained old IDs", receipt, err, entries)
			}
		} else if err != nil || len(entries) != 1 || entries[0].ID != "new-job" {
			t.Fatal("verified write did not refresh completion", receipt, err, entries)
		}
	}
}

func TestCacheFailureCannotFailSnapshotOrApply(t *testing.T) {
	s, _ := fixtureService(t, "* * * * * echo before\n")
	if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lazycrontab"), []byte("cache unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal("cache failure broke read", err)
	}
	plan, err := s.PlanRawSource(context.Background(), snap, "* * * * * echo after\n")
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.Apply(context.Background(), plan); err != nil || receipt.Status != "saved" {
		t.Fatal("cache failure broke write", receipt, err)
	}
}

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

func TestRawSourcePlanPreservesExactBytesAndUsesExistingTransaction(t *testing.T) {
	before := "# header\r\nPATH=/bin\n0 9 * * * echo before\nunknown existing extension\n"
	after := "# user-chosen order\r\nunknown existing extension\nPATH=/usr/bin:/bin\n\n  0  10 * * * printf '\\%s' 'after # literal'\\ \n"
	s, cron := fixtureService(t, before)
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanRawSource(context.Background(), snap, after)
	if err != nil || plan.Before != before || plan.After != after || plan.Operation != "edit-source" || plan.JobID != "" || plan.ManagedScript != nil {
		t.Fatal(plan, err)
	}
	if cron.writes != 0 {
		t.Fatal("raw plan installed a source")
	}
	if backups, err := Backups(); err != nil || len(backups) != 0 {
		t.Fatal("planning made a backup", backups, err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "line 2: preserved existing unsupported") {
		t.Fatal("missing preservation warning", plan.Warnings)
	}
	receipt, err := s.Apply(context.Background(), plan)
	if err != nil || receipt.Status != "saved" || cron.raw != after {
		t.Fatal(receipt, err, cron.raw)
	}
	backups, err := Backups()
	if err != nil || len(backups) != 1 || backups[0].Content != before || backups[0].FilePath != "" {
		t.Fatal(backups, err)
	}
}

func TestRawSourceValidationAndReadOnly(t *testing.T) {
	s, _ := fixtureService(t, "# source\n")
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct{ body, diagnostic string }{
		{"* * * * * echo hi", "final newline"},
		{"# first\n* * * * * echo\x00bad\n", "line 2"},
		{"# first\n*5 * * * * echo bad\n", "line 2"},
		{strings.Repeat("#", MaxRawSourceBytes+1), "source limit"},
	} {
		if _, err := s.PlanRawSource(context.Background(), snap, invalid.body); err == nil || !strings.Contains(err.Error(), invalid.diagnostic) {
			t.Fatal("wrong raw validation result", invalid.diagnostic, err)
		}
	}
	s.Config.Sources = append(s.Config.Sources, config.Source{Host: "local", ID: "user", Kind: "user", ReadOnly: true})
	if _, err := s.PlanRawSource(context.Background(), snap, ""); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatal(err)
	}
	if _, err := s.PlanRawSource(context.Background(), Snapshot{}, ""); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.PlanRawSource(ctx, snap, ""); err != context.Canceled {
		t.Fatal("cancellation ignored", err)
	}
}

func TestRawSourceSourceConflictAndUnknownWrite(t *testing.T) {
	for _, conflict := range []bool{true, false} {
		s, cron := fixtureService(t, "0 9 * * * echo before\n")
		snap, err := s.Snapshot(context.Background(), "local", "user")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.PlanRawSource(context.Background(), snap, "0 10 * * * echo after\n")
		if err != nil {
			t.Fatal(err)
		}
		if conflict {
			cron.raw += "# edited by another process\n"
		} else {
			cron.failWrite = true
		}
		receipt, err := s.Apply(context.Background(), plan)
		if err == nil {
			t.Fatal("expected fixture failure", receipt)
		}
		if conflict && cron.writes != 0 || !conflict && (cron.writes != 1 || receipt.Status != "unknown") {
			t.Fatal(receipt, cron.writes)
		}
	}
}

func TestRawSourceDoesNotRewriteRecipesOrAllocateIDs(t *testing.T) {
	before := document.Marker + `{"v":1,"id":"existing","name":"name"}` + "\n0 9 * * * echo before\n"
	s, cron := fixtureService(t, before)
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	entry := snap.Entries[0]
	if err := SaveRecipe(entry, Recipe{Runner: "direct", Original: "echo before"}); err != nil {
		t.Fatal(err)
	}
	path, err := recipePath(entry)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(before, "echo before", "echo changed", 1) + "5 8 * * * echo unmanaged\n"
	plan, err := s.PlanRawSource(context.Background(), snap, after)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if cron.raw != after || strings.Count(cron.raw, document.Marker) != 1 {
		t.Fatal("raw edit inserted metadata", cron.raw)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(metadata) {
		t.Fatal("raw source edit rewrote helper sidecar", err)
	}
	entry.Command = "echo changed"
	if _, err := LoadRecipe(entry); err == nil {
		t.Fatal("externally changed command retained valid old recipe")
	}
}

func TestRawSourceSupercronicAndEmptyUserSource(t *testing.T) {
	s, cron := fixtureService(t, "")
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanRawSource(context.Background(), snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.Apply(context.Background(), plan); err != nil || receipt.Status != "unchanged" || cron.writes != 0 {
		t.Fatal(receipt, err, cron.writes)
	}
	s.Config.Sources = append(s.Config.Sources, config.Source{Host: "local", ID: "super", Kind: "file", Path: filepath.Join(t.TempDir(), "crontab"), Dialect: "supercronic"})
	snap = Snapshot{Host: "local", Source: "super", Document: document.Parse("", schedule.Supercronic, false)}
	plan, err = s.PlanRawSource(context.Background(), snap, "*/5 * * * * * * echo seconds")
	if err != nil || plan.After != "*/5 * * * * * * echo seconds" || !strings.Contains(strings.Join(plan.Warnings, "\n"), "reloaded") {
		t.Fatal(plan, err)
	}
}

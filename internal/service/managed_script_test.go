package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

// Native file operations run only under t.TempDir. Cron remains an in-memory
// fixture, including tests whose logical target is an SSH host.
type managedRunner struct {
	cron                *fakeCron
	home, data          string
	loseScriptWrite     bool
	loseScriptReadBack  bool
	changeSourceOnWrite bool
	scriptWrites        int
}

func (r *managedRunner) Run(ctx context.Context, h config.Host, argv []string, input []byte) (transport.Result, error) {
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "crontab -l") || strings.HasSuffix(joined, "crontab -") || strings.Contains(joined, "uname -s") {
		return r.cron.Run(ctx, h, argv, input)
	}
	inspect := len(argv) > 2 && argv[2] == inspectManagedScript
	install := len(argv) > 2 && argv[2] == installManagedScript
	if inspect && r.scriptWrites > 0 && r.loseScriptReadBack {
		return transport.Result{Code: 255}, errors.New("fixture connection lost during read-back")
	}
	if h.SSH != "" {
		argv = append([]string{"env", "HOME=" + r.home, "XDG_DATA_HOME=" + r.data}, argv...)
	}
	result, err := (transport.Native{}).Run(ctx, config.Host{ID: "local"}, argv, input)
	if install {
		r.scriptWrites++
		if err == nil && r.changeSourceOnWrite {
			r.cron.mu.Lock()
			r.cron.raw += "# concurrent external edit\n"
			r.cron.exists = true
			r.cron.mu.Unlock()
		}
		if err == nil && r.loseScriptWrite {
			return transport.Result{Code: 255}, errors.New("fixture connection lost after publication")
		}
	}
	return result, err
}

func managedFixture(t *testing.T, remote bool) (*Service, *managedRunner, Entry) {
	t.Helper()
	s, cron := fixtureService(t, "# keep this source\n")
	runner := &managedRunner{cron: cron, home: t.TempDir(), data: t.TempDir()}
	s.Runner = runner
	e := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: document.Job{
		Metadata: document.Metadata{Version: 1, ID: document.NewID(), Name: "Managed fixture"},
		Schedule: "* * * * *", Enabled: true, Environment: map[string]string{},
	}}
	if remote {
		e.Host = "remote"
		s.Config.Hosts = append(s.Config.Hosts, config.Host{ID: e.Host, SSH: "fixture-never-connect"})
	}
	return s, runner, e
}

func managedJobPlan(t *testing.T, s *Service, e Entry, body string) (Plan, Recipe, Entry) {
	t.Helper()
	pending, err := s.PrepareManagedScript(context.Background(), e, body)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := s.ResolveTargetPath(context.Background(), e.Host, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := Recipe{Runner: "direct", Script: pending.Path, Directory: directory,
		ScriptTask:    &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh"},
		ManagedScript: &ManagedScript{Version: 1, Digest: pending.Digest},
	}
	e.Command, err = Compile(e, r)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(context.Background(), e.Host, e.Source)
	if err != nil {
		t.Fatal(err)
	}
	op, id := "add", ""
	for _, existing := range snap.Entries {
		if existing.ID == e.ID {
			op, id = "edit", e.ID
		}
	}
	p, err := s.Plan(context.Background(), snap, id, &e.Job, op)
	if err != nil {
		t.Fatal(err)
	}
	p.ManagedScript = &pending
	return p, r, e
}

func TestManagedScriptPreviewAndApplyLocalAndSSH(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "ssh"}[remote], func(t *testing.T) {
			s, runner, entry := managedFixture(t, remote)
			marker := filepath.Join(t.TempDir(), "must-not-run")
			body := "#!/bin/sh\nprintf '%s\\n' \"hi ' quoted % $HOME\"\ntouch " + transport.Quote(marker) + "\n"
			plan, recipe, entry := managedJobPlan(t, s, entry, body)
			if runner.scriptWrites != 0 || runner.cron.writes != 0 {
				t.Fatal("preview wrote target state")
			}
			if _, err := os.Stat(plan.ManagedScript.Path); !os.IsNotExist(err) {
				t.Fatalf("preview created script: %v", err)
			}
			if remote && !strings.HasPrefix(plan.ManagedScript.Path, filepath.Join(runner.data, "lazycrontab")) {
				t.Fatal("remote path did not use target XDG root", plan.ManagedScript.Path)
			}
			report := s.CheckManagedRecipe(context.Background(), entry, recipe, *plan.ManagedScript)
			for _, finding := range report.Findings {
				if finding.Severity == "error" || finding.Severity == "warning" || finding.Severity == "unknown" {
					t.Fatalf("new script produced an incorrect readiness failure: %+v", finding)
				}
			}
			receipt, err := s.Apply(context.Background(), plan)
			if err != nil || receipt.Status != "saved" || receipt.ScriptStatus != "saved" || receipt.ScriptPath != recipe.Script {
				t.Fatal(receipt, err)
			}
			content, err := s.ReadManagedScript(context.Background(), entry, recipe)
			if err != nil || content != body {
				t.Fatal(content, err)
			}
			for _, path := range []string{recipe.Script, filepath.Dir(recipe.Script), filepath.Dir(filepath.Dir(recipe.Script))} {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				want := os.FileMode(0700)
				if path == recipe.Script {
					want = 0600
				}
				if info.Mode().Perm() != want {
					t.Fatal(path, info.Mode())
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("preview or apply executed user script", err)
			}
			if err := SaveRecipe(entry, recipe); err != nil {
				t.Fatal(err)
			}
			snap, err := s.Snapshot(context.Background(), entry.Host, entry.Source)
			if err != nil {
				t.Fatal(err)
			}
			checked, err := s.CheckJob(context.Background(), snap, entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range checked.Findings {
				found = found || finding.Code == "managed-script-integrity" && finding.Severity == "ok"
			}
			if !found {
				t.Fatal("saved job did not verify managed content", checked)
			}
		})
	}
}

func TestManagedVersionsPreserveOldSchedulesAndArguments(t *testing.T) {
	s, runner, entry := managedFixture(t, false)
	first, firstRecipe, firstEntry := managedJobPlan(t, s, entry, "printf '%s' 'old value'\n")
	if _, err := s.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second, secondRecipe, _ := managedJobPlan(t, s, entry, "printf '%s' 'new value'\n")
	if firstRecipe.Script == secondRecipe.Script {
		t.Fatal("edited content reused the active script path")
	}
	if _, err := s.Apply(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	oldBody, err := s.ReadManagedScript(context.Background(), firstEntry, firstRecipe)
	if err != nil || oldBody != first.ManagedScript.Content {
		t.Fatal("old version changed", oldBody, err)
	}
	// Execute only a harmless fixture. A queued command containing the old
	// path continues using that content even after the schedule was edited.
	code, input := SplitPercent(firstEntry.Command)
	result, err := (transport.Native{}).Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", code}, []byte(input))
	if err != nil || string(result.Stdout) != "old value" {
		t.Fatal(result, err)
	}
	if strings.Contains(runner.cron.raw, firstRecipe.Script) || !strings.Contains(runner.cron.raw, secondRecipe.Script) {
		t.Fatal("cron did not switch to the new version", runner.cron.raw)
	}
	snap, err := s.Snapshot(context.Background(), entry.Host, entry.Source)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := s.Plan(context.Background(), snap, entry.ID, nil, "remove")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), remove); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{firstRecipe.Script, secondRecipe.Script} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("removing a job removed a version still usable by Pueue", err)
		}
	}
}

func TestManagedScriptConflictsAndPartialOutcomes(t *testing.T) {
	for _, failure := range []string{"source-before", "source-after", "script-outcome", "script-read-back", "cron-outcome"} {
		t.Run(failure, func(t *testing.T) {
			s, runner, entry := managedFixture(t, false)
			plan, _, _ := managedJobPlan(t, s, entry, "echo fixture\n")
			switch failure {
			case "source-before":
				runner.cron.raw += "# changed\n"
			case "source-after":
				runner.changeSourceOnWrite = true
			case "script-outcome":
				runner.loseScriptWrite = true
			case "script-read-back":
				runner.loseScriptReadBack = true
			case "cron-outcome":
				runner.cron.failWrite = true
			}
			receipt, err := s.Apply(context.Background(), plan)
			if err == nil {
				t.Fatal("fixture failure succeeded", receipt)
			}
			if failure == "source-before" {
				if runner.scriptWrites != 0 || runner.cron.writes != 0 {
					t.Fatal("stale source caused a write")
				}
				return
			}
			if runner.scriptWrites != 1 {
				t.Fatal("managed write was retried", runner.scriptWrites)
			}
			if _, err := os.Stat(plan.ManagedScript.Path); err != nil {
				t.Fatal("partial apply deleted a possibly referenced script", err)
			}
			if failure == "cron-outcome" {
				if receipt.Status != "unknown" || receipt.ScriptStatus != "saved" || runner.cron.writes != 1 {
					t.Fatal(receipt, runner.cron.writes)
				}
			} else if runner.cron.writes != 0 {
				t.Fatal("cron was installed despite unverified script/source", receipt)
			}
			if failure == "script-outcome" || failure == "script-read-back" {
				if receipt.Status != "unknown" || receipt.ScriptStatus != "unknown" {
					t.Fatal("script outcome reported as certain", receipt)
				}
			}
		})
	}
}

func TestManagedScriptRejectsChangedContentAndUnsafePaths(t *testing.T) {
	s, runner, entry := managedFixture(t, false)
	plan, recipe, _ := managedJobPlan(t, s, entry, "echo original\n")
	if _, err := s.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipe.Script, []byte("echo changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadManagedScript(context.Background(), entry, recipe); err == nil {
		t.Fatal("changed version passed integrity check")
	}
	if _, err := s.PrepareManagedScript(context.Background(), entry, plan.ManagedScript.Content); err == nil {
		t.Fatal("changed version was prepared for overwrite")
	}
	before := runner.scriptWrites
	draft, err := s.ScriptDraft(context.Background(), entry.Host, recipe.Script)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(draft.File)
	if _, err := s.SaveScript(context.Background(), draft, "replacement"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatal("generic script editor can replace a managed version", err)
	}
	alias := filepath.Join(t.TempDir(), "script-directory")
	if err := os.Symlink(filepath.Dir(recipe.Script), alias); err != nil {
		t.Fatal(err)
	}
	draft.Path = filepath.Join(alias, filepath.Base(recipe.Script))
	if _, err := s.SaveScript(context.Background(), draft, "replacement"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatal("parent symlink bypassed immutable managed script guard", err)
	}
	if runner.scriptWrites != before {
		t.Fatal("rejected edit wrote managed content")
	}
	broken := recipe
	broken.Script = filepath.Join(t.TempDir(), "external.sh")
	if _, err := Compile(entry, broken); err == nil {
		t.Fatal("managed metadata accepted an unrelated path")
	}
	wrongEntry := entry
	wrongEntry.ID = "different-job"
	if _, err := s.ReadManagedScript(context.Background(), wrongEntry, recipe); err == nil {
		t.Fatal("managed script accepted another job identity")
	}
	checked := s.CheckRecipe(context.Background(), entry, recipe)
	found := false
	for _, finding := range checked.Findings {
		found = found || finding.Code == "managed-script-integrity" && finding.Severity == "error"
	}
	if !found {
		t.Fatal("check omitted modified managed content", checked)
	}
}

func TestManagedScriptSymlinksAndXDGRoots(t *testing.T) {
	for _, part := range []string{"root", "scripts", "job", "file"} {
		t.Run(part, func(t *testing.T) {
			s, _, entry := managedFixture(t, false)
			p, err := s.PrepareManagedScript(context.Background(), entry, "echo safe\n")
			if err != nil {
				t.Fatal(err)
			}
			link := p.Path
			for n := map[string]int{"root": 3, "scripts": 2, "job": 1, "file": 0}[part]; n > 0; n-- {
				link = filepath.Dir(link)
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), link); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PrepareManagedScript(context.Background(), entry, p.Content); err == nil {
				t.Fatal("accepted symlink inside managed namespace")
			}
		})
	}
	t.Run("target relative XDG falls back to target HOME", func(t *testing.T) {
		s, runner, entry := managedFixture(t, true)
		runner.data = "relative-not-a-data-home"
		p, err := s.PrepareManagedScript(context.Background(), entry, "echo safe\n")
		if err != nil || !strings.HasPrefix(p.Path, filepath.Join(runner.home, ".local", "share", "lazycrontab")) {
			t.Fatal(p, err)
		}
	})
	t.Run("data root mode and existing root symlink are respected", func(t *testing.T) {
		s, _, entry := managedFixture(t, false)
		data := t.TempDir()
		if err := os.Chmod(data, 0755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "user-chosen-data")
		if err := os.Symlink(data, link); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_DATA_HOME", link)
		p, _, _ := managedJobPlan(t, s, entry, "echo fixture\n")
		if _, err := s.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(data)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatal("user-owned data root mode changed", info, err)
		}
	})
}

func TestManagedScriptInputLimitsAndReusedVersion(t *testing.T) {
	s, runner, entry := managedFixture(t, false)
	for _, body := range []string{"", " \n\t", "echo\x00bad", strings.Repeat("x", MaxManagedScriptBytes+1)} {
		if _, err := s.PrepareManagedScript(context.Background(), entry, body); err == nil {
			t.Fatal("accepted invalid script body")
		}
	}
	p, _, _ := managedJobPlan(t, s, entry, "echo reused\n")
	if _, err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p, _, _ = managedJobPlan(t, s, entry, "echo reused\n")
	receipt, err := s.Apply(context.Background(), p)
	if err != nil || receipt.Status != "unchanged" || receipt.ScriptStatus != "unchanged" || runner.scriptWrites != 1 || runner.cron.writes != 1 {
		t.Fatal("unchanged immutable version was rewritten", receipt, err, runner.scriptWrites, runner.cron.writes)
	}
}

func TestManagedEditRejectsPreviousContentChangedAfterReview(t *testing.T) {
	for _, previousChange := range []string{"modify", "remove"} {
		t.Run(previousChange, func(t *testing.T) {
			s, runner, entry := managedFixture(t, false)
			first, firstRecipe, _ := managedJobPlan(t, s, entry, "echo original\n")
			if _, err := s.Apply(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			second, _, _ := managedJobPlan(t, s, entry, "echo edited\n")
			second.ManagedScript.PreviousPath = firstRecipe.Script
			second.ManagedScript.PreviousDigest = firstRecipe.ManagedScript.Digest
			var err error
			if previousChange == "modify" {
				err = os.WriteFile(firstRecipe.Script, []byte("echo external change\n"), 0600)
			} else {
				err = os.Remove(firstRecipe.Script)
			}
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := s.Apply(context.Background(), second)
			if err == nil || !strings.Contains(err.Error(), "previous script changed since review") {
				t.Fatal("stale managed edit was accepted", receipt, err)
			}
			if runner.scriptWrites != 1 || runner.cron.writes != 1 {
				t.Fatal("changed prior version caused new writes", runner.scriptWrites, runner.cron.writes)
			}
			if _, err := os.Stat(second.ManagedScript.Path); !os.IsNotExist(err) {
				t.Fatal("stale edit created a script version", err)
			}
			// Supplying replacement content explicitly is an intentional recovery:
			// no old-body guard, but the original source revision still applies.
			second.ManagedScript.PreviousPath = ""
			second.ManagedScript.PreviousDigest = ""
			if _, err := s.Apply(context.Background(), second); err != nil {
				t.Fatal("explicit replacement cannot recover old script", err)
			}
		})
	}
}

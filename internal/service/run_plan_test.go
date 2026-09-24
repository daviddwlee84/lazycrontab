package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

type runFactsOnly struct{}

func (runFactsOnly) Run(_ context.Context, _ config.Host, args []string, _ []byte) (transport.Result, error) {
	if len(args) == 3 && args[0] == "sh" && strings.Contains(args[2], "id -un") {
		return transport.Result{Stdout: []byte("/fixture/home\nfixture\n")}, nil
	}
	return transport.Result{}, fmt.Errorf("unexpected preflight command: %v", args)
}

func TestDefaultRunKeepsStoredWrapperWhenSourceShellChanges(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := New(config.Defaults())
	s.Runner = runFactsOnly{}
	original := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: document.Job{Metadata: document.Metadata{ID: "fixture", Version: 1}, Command: "printf '\\%s' '{1..3}'", Environment: map[string]string{"SHELL": "/bin/sh"}}}
	recipe := Recipe{Original: original.Command, Runner: "direct", Directory: "/fixture/work"}
	compiled, err := Compile(original, recipe)
	if err != nil {
		t.Fatal(err)
	}
	raw := "SHELL=/bin/bash\n# lazycrontab: {\"v\":1,\"id\":\"fixture\"}\n0 9 * * * " + compiled + "\n"
	doc := document.Parse(raw, schedule.System, false)
	job, err := doc.Find("fixture")
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: job}
	if err := SaveRecipe(entry, recipe); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Host: "local", Source: "user", Document: doc}
	plan, err := s.RunPlan(context.Background(), snap, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	command, stdin := SplitPercent(compiled)
	if plan.Command != command || plan.Stdin != stdin || plan.Shell != "/bin/bash" || plan.Runner != "direct" {
		t.Fatalf("default run changed stored command: %+v", plan)
	}
	override, err := s.RunPlan(context.Background(), snap, "fixture", &recipe)
	if err != nil {
		t.Fatal(err)
	}
	if override.Command == plan.Command || !strings.Contains(override.Command, "/bin/bash") {
		t.Fatal("explicit override did not rebuild recipe", override.Command)
	}
}

func TestDefaultRunDerivesQueuedStatusOnlyFromMatchingRecipe(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := New(config.Defaults())
	s.Runner = runFactsOnly{}
	raw := "# lazycrontab: {\"v\":1,\"id\":\"queued\"}\n0 9 * * * /old/pueue add -- echo job\n"
	doc := document.Parse(raw, schedule.System, false)
	job, err := doc.Find("queued")
	if err != nil {
		t.Fatal(err)
	}
	e := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: job}
	if err := SaveRecipe(e, Recipe{Runner: "pueue", PueuePath: "/different/pueue", Original: "should never be recompiled"}); err != nil {
		t.Fatal(err)
	}
	plan, err := s.RunPlan(context.Background(), Snapshot{Host: "local", Source: "user", Document: doc}, "queued", nil)
	if err != nil || plan.Runner != "pueue" || plan.Command != job.Command {
		t.Fatal(plan, err)
	}
	// A matching command-like word is insufficient when the sidecar is stale.
	doc = document.Parse(strings.Replace(raw, "echo job", "echo changed", 1), schedule.System, false)
	plan, err = s.RunPlan(context.Background(), Snapshot{Host: "local", Source: "user", Document: doc}, "queued", nil)
	if err != nil || plan.Runner != "direct" || !strings.Contains(plan.Command, "echo changed") {
		t.Fatal(plan, err)
	}
}

func TestDefaultRunRevealsOnlyVerifiedFrozenPueueSubmission(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := New(config.Defaults())
	s.Runner = runFactsOnly{}
	original := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: document.Job{Metadata: document.Metadata{ID: "frozen", Version: 1}, Command: `printf '\%s' '{1..3}'`, Environment: map[string]string{"SHELL": "/old/shell"}}}
	scheduled, recipe, err := CompileRecipe(original, Recipe{Runner: "pueue", PueuePath: "/old/pueue", EnqueueOutput: EnqueueQuiet})
	if err != nil {
		t.Fatal(err)
	}
	raw := "SHELL=/new/shell\n# lazycrontab: {\"v\":1,\"id\":\"frozen\"}\n0 9 * * * " + scheduled + "\n"
	doc := document.Parse(raw, schedule.System, false)
	job, err := doc.Find("frozen")
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: job}
	if err := SaveRecipe(entry, recipe); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Host: "local", Source: "user", Document: doc}
	plan, err := s.RunPlan(context.Background(), snap, "frozen", nil)
	want, input := SplitPercent(recipe.PueueSubmission.Command)
	if err != nil || plan.Command != want || plan.Stdin != input || plan.Runner != "pueue" || plan.TaskIDUnavailable || plan.Shell != "/new/shell" {
		t.Fatal("default Run rebuilt or lost its frozen task ID", plan, err)
	}
	for _, failure := range []string{"missing-frozen", "version", "command", "dialect"} {
		t.Run(failure, func(t *testing.T) {
			r := recipe
			copy := *recipe.PueueSubmission
			r.PueueSubmission = &copy
			switch failure {
			case "missing-frozen":
				r.PueueSubmission = nil
			case "version":
				r.PueueSubmission.Version++
			case "command":
				r.PueueSubmission.Command = "echo unrelated"
			case "dialect":
				r.PueueSubmission.Dialect = schedule.Supercronic
			}
			if err := SaveRecipe(entry, r); err != nil {
				t.Fatal(err)
			}
			fallback, err := s.RunPlan(context.Background(), snap, "frozen", nil)
			command, stdin := SplitPercent(scheduled)
			if err != nil || fallback.Command != command || fallback.Stdin != stdin || fallback.Runner != "pueue" || !fallback.TaskIDUnavailable || !strings.Contains(fallback.Warning, "task ID will be unavailable") || !strings.Contains(fallback.Warning, "Cron-like environment") {
				t.Fatal("unverified metadata altered execution or blocked safe fallback", fallback, err)
			}
		})
	}
	path, err := recipePath(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fallback, err := s.RunPlan(context.Background(), snap, "frozen", nil)
	command, _ := SplitPercent(scheduled)
	if err != nil || fallback.Command != command || fallback.Runner != "direct" || fallback.TaskIDUnavailable {
		t.Fatal("missing sidecar did not preserve native execution", fallback, err)
	}
	if err := SaveRecipe(entry, recipe); err != nil {
		t.Fatal(err)
	}
	changed := document.Parse(strings.Replace(raw, scheduled, scheduled+" # external", 1), schedule.System, false)
	fallback, err = s.RunPlan(context.Background(), Snapshot{Host: "local", Source: "user", Document: changed}, "frozen", nil)
	if err != nil || fallback.Command != command+" # external" || !strings.Contains(fallback.Warning, "native source command unchanged") {
		t.Fatal("stale sidecar changed native execution", fallback, err)
	}
}

type pueueRunFixture struct {
	cron        *fakeCron
	home, pueue string
}

func (r pueueRunFixture) Run(ctx context.Context, _ config.Host, args []string, input []byte) (transport.Result, error) {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "crontab -l") || strings.Contains(joined, "uname -s") {
		return r.cron.Run(ctx, config.Host{ID: "local"}, args, input)
	}
	if len(args) == 3 && args[0] == "sh" && strings.Contains(args[2], "id -un") {
		return transport.Result{Stdout: []byte(r.home + "\nfixture\n")}, nil
	}
	if len(args) == 3 && args[2] == "command -v pueue" {
		return transport.Result{Stdout: []byte(r.pueue + "\n")}, nil
	}
	return (transport.Native{}).Run(ctx, config.Host{ID: "local"}, args, input)
}

func TestManualRunRecordsTaskIDWithoutChangingScheduledOutput(t *testing.T) {
	for _, mode := range []string{"default", "override", "invalid-frozen", "invalid-frozen-inherit", "enqueue-failure"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			fake, calls, sentinel := filepath.Join(dir, "pueue"), filepath.Join(dir, "calls"), filepath.Join(dir, "must-not-run")
			exit := "0"
			if mode == "enqueue-failure" {
				exit = "17"
			}
			body := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.2'; exit;;\ngroup) echo '{\"default\":{}}'; exit;;\nesac\nprintf 'add\\n' >> " + transport.Quote(calls) + "\nprintf '140\\n'\nprintf 'enqueue diagnostic\\n' >&2\nexit " + exit + "\n"
			if err := os.WriteFile(fake, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			entry := Entry{Host: "local", Source: "user", Dialect: schedule.System, Job: document.Job{Metadata: document.Metadata{ID: "queued", Version: 1}, Command: "touch " + transport.Quote(sentinel)}}
			enqueue := EnqueueQuiet
			if mode == "invalid-frozen-inherit" {
				enqueue = EnqueueInherit
			}
			scheduled, recipe, err := CompileRecipe(entry, Recipe{Runner: "pueue", PueuePath: fake, Original: entry.Command, EnqueueOutput: enqueue, OutputPolicy: OutputDiscard})
			if err != nil {
				t.Fatal(err)
			}
			raw := "# lazycrontab: {\"v\":1,\"id\":\"queued\"}\n* * * * * " + scheduled + "\n"
			s, cron := fixtureService(t, raw)
			s.Runner = pueueRunFixture{cron: cron, home: dir, pueue: fake}
			snap, err := s.Snapshot(context.Background(), "local", "user")
			if err != nil {
				t.Fatal(err)
			}
			entry.Job, err = snap.Document.Find("queued")
			if err != nil {
				t.Fatal(err)
			}
			unavailable := strings.HasPrefix(mode, "invalid-frozen")
			if unavailable {
				copy := *recipe.PueueSubmission
				copy.Command = "echo unrelated"
				recipe.PueueSubmission = &copy
			}
			if err := SaveRecipe(entry, recipe); err != nil {
				t.Fatal(err)
			}
			var override *Recipe
			if mode == "override" {
				override = &recipe
			}
			plan, err := s.RunPlan(context.Background(), snap, "queued", override)
			if err != nil {
				t.Fatal(err)
			}
			record, err := s.Run(context.Background(), plan)
			if record.Stderr != "enqueue diagnostic\n" || record.TaskIDUnavailable != unavailable {
				t.Fatal("manual diagnostics changed", record, err)
			}
			switch mode {
			case "invalid-frozen", "invalid-frozen-inherit":
				output := ""
				if mode == "invalid-frozen-inherit" {
					output = "140\n"
				}
				if err != nil || record.Status != "queued" || record.TaskID != "" || record.Output != output {
					t.Fatal("unverified submission exposed/guessed a task ID", record, err)
				}
			case "enqueue-failure":
				if err == nil || record.Status != "failed" || record.ExitCode != 17 || record.TaskID != "" || record.Output != "140\n" {
					t.Fatal("enqueue failure reported as a queued task", record, err)
				}
			default:
				if err != nil || record.Status != "queued" || record.TaskID != "140" || record.Output != "140\n" {
					t.Fatal("manual task-ID receipt lost", record, err)
				}
			}
			got, err := os.ReadFile(calls)
			if err != nil || string(got) != "add\n" || cron.raw != raw || cron.writes != 0 {
				t.Fatal("run submitted more than once or changed source", string(got), err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatal("fixture executed the queued payload", err)
			}
		})
	}
}

package service

import (
	"context"
	"fmt"
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

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func TestScriptPresetCLIAndCheckAreReadOnlyUntilApproved(t *testing.T) {
	dir, cron := isolated(t)
	script := filepath.Join(dir, "never-run.sh")
	marker := filepath.Join(dir, "RAN")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"add", "--schedule", "*/5 * * * *", "--preset", "shell", "--script", script, "--runtime", "/bin/sh", "--arg", "space arg", "--arg", `literal\%`, "--env", "SAMPLE=literal $HOME", "--json"}
	out, err := invoke(append(args, "--dry-run")...)
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(cron); !os.IsNotExist(err) {
		t.Fatal("preview installed a crontab")
	}
	out, err = invoke(append(args, "--yes")...)
	if err != nil {
		t.Fatal(out, err)
	}
	var result struct {
		ID string `json:"job_id"`
	}
	if err = json.Unmarshal([]byte(out), &result); err != nil || result.ID == "" {
		t.Fatal(out, err)
	}
	out, err = invoke("check", result.ID, "--json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "dependencies-unverified") {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("add/check ran the script")
	}
	if _, err := invoke("edit", result.ID, "--remark", "new remark", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("show", result.ID, "--json")
	if err != nil || !strings.Contains(out, "space arg") {
		t.Fatal(out, err)
	}
	if _, err := invoke("edit", result.ID, "--command", "echo raw replacement", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("check", result.ID, "--json")
	if err != nil || !strings.Contains(out, "raw-command") {
		t.Fatal(out, err)
	}
}

func TestScriptPresetFlagErrorsNeverLaunchWizard(t *testing.T) {
	isolated(t)
	for _, args := range [][]string{
		{"add", "--schedule", "* * * * *", "--preset", "typo", "--script", "anything"},
		{"add", "--schedule", "* * * * *", "--preset", "shell", "--command", "echo both", "--script", "anything"},
		{"add", "--schedule", "* * * * *", "--command", "echo raw", "--arg", "not-used"},
		{"add", "--schedule", "* * * * *", "--command", "echo raw", "--env", "BAD"},
		{"add", "--schedule", "* * * * *", "--preset", "uv-project", "--script", "anything"},
	} {
		if out, err := invoke(args...); err == nil {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
}

func TestSharedJobModelHasPresetAndSchedulePickers(t *testing.T) {
	isolated(t)
	s := service.New(config.Defaults())
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["schedule"] != "0 9 * * *" {
		t.Fatal(values)
	}
	found := map[string]bool{}
	for _, field := range spec.Fields {
		if field.Key == "schedule" && field.Kind == "schedule" {
			found["schedule"] = true
		}
		if (field.Key == "script" || field.Key == "project" || field.Key == "runtime") && field.Pick != nil {
			found[field.Key] = true
		}
	}
	for _, key := range []string{"schedule", "script", "project", "runtime"} {
		if !found[key] {
			t.Fatal("missing shared field", key)
		}
	}
}

func TestJobScheduleContextFollowsSelectedTarget(t *testing.T) {
	isolated(t)
	c := config.Defaults()
	c.Hosts = append(c.Hosts, config.Host{ID: "lab", SSH: "never-connect", Timezone: "UTC"})
	c.Sources = append(c.Sources, config.Source{ID: "worker", Host: "lab", Kind: "file", Path: "/srv/cron", Dialect: "supercronic", Timezone: "Europe/Berlin"})
	spec, _, err := newJobFormSpec(context.Background(), service.New(c), "local", "user", "add", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	options := spec.ScheduleContext(map[string]string{"host": "lab", "source": "worker", "schedule": "* * * * * 2030"})
	if options.Dialect != schedule.Supercronic || options.Timezone != "Europe/Berlin" {
		t.Fatalf("%+v", options)
	}
}

func TestSharedAddModelNeverReplacesSelectedJob(t *testing.T) {
	_, cron := isolated(t)
	original := "# lazycrontab: {\"v\":1,\"id\":\"seed\",\"name\":\"keep\"}\n0 9 * * * echo original\n"
	if err := os.WriteFile(cron, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Defaults())
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "seed", map[string]string{"command": "echo new", "name": "new"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(review.Data.(jobReview).Plan.After, original) || strings.Count(review.Data.(jobReview).Plan.After, "lazycrontab:") != 2 {
		t.Fatal(review.Text)
	}
}

func TestScriptSuggestionsFillOnlyEmptyDraftChoices(t *testing.T) {
	dir, _ := isolated(t)
	script := filepath.Join(dir, "job.py")
	os.WriteFile(script, []byte("print('must not run')\n"), 0600)
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='fixture'\n"), 0600)
	bin := filepath.Join(dir, ".venv", "bin")
	os.MkdirAll(bin, 0700)
	python := filepath.Join(bin, "python")
	os.WriteFile(python, []byte("#!/bin/sh\nexit 99\n"), 0700)
	s := service.New(config.Defaults())
	values := map[string]string{"preset": "python", "script": script}
	updates := jobScriptUpdates(context.Background(), s, "local", "user", values, nil)
	found := map[string]string{}
	for _, update := range updates {
		if update.Value != nil {
			found[update.Key] = *update.Value
		}
	}
	if found["runtime"] != python || found["directory"] != dir {
		t.Fatal(found, updates)
	}
	values["runtime"] = "/my/custom/python"
	values["directory"] = dir
	updates = jobScriptUpdates(context.Background(), s, "local", "user", values, nil)
	for _, update := range updates {
		if (update.Key == "runtime" || update.Key == "directory") && update.Value != nil {
			t.Fatal("overwrote explicit choice", update)
		}
	}
	values["script"] = ""
	if updates := jobScriptUpdates(context.Background(), s, "local", "user", values, nil); len(updates) != 0 {
		t.Fatal("empty script triggered discovery")
	}
}

func TestRelativeScriptRemainsStableAfterWorkingDirectorySuggestion(t *testing.T) {
	dir, _ := isolated(t)
	t.Chdir(dir)
	parent := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(parent, "job.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Defaults())
	_, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", map[string]string{"preset": "shell", "script": "scripts/job.sh"})
	if err != nil {
		t.Fatal(err)
	}
	updates := jobScriptUpdates(context.Background(), s, "local", "user", values, nil)
	for _, update := range updates {
		if update.Value != nil {
			values[update.Key] = *update.Value
		}
	}
	if values["script"] != script || values["directory"] != parent {
		t.Fatal(values)
	}
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", values)
	if err != nil {
		t.Fatal(err)
	}
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if actual := review.Data.(jobReview).Recipe.Script; actual != script {
		t.Fatalf("script rebased after suggestion: %q", actual)
	}
}

func TestRelativeProjectRemainsStableAfterWorkingDirectorySuggestion(t *testing.T) {
	dir, _ := isolated(t)
	t.Chdir(dir)
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "job.py")
	if err := os.WriteFile(script, []byte("print('not run')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "pyproject.toml"), []byte("[project]\nname='fixture'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	uv := filepath.Join(dir, "fixture-uv")
	if err := os.WriteFile(uv, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Defaults())
	_, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", map[string]string{"preset": "uv-project", "script": "job.py", "project": "project", "runtime": uv})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range jobScriptUpdates(context.Background(), s, "local", "user", values, nil) {
		if update.Value != nil {
			values[update.Key] = *update.Value
		}
	}
	if values["project"] != project || values["directory"] != project {
		t.Fatal(values)
	}
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", values)
	if err != nil {
		t.Fatal(err)
	}
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if actual := review.Data.(jobReview).Recipe.ScriptTask.Project; actual != project {
		t.Fatalf("project rebased after suggestion: %q", actual)
	}
}

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func outputPueueFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "bin", "pueue")
	body := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.0';;\ngroup) echo '{\"default\":{}}';;\n*) echo 'unexpected submission' >&2; exit 99;;\nesac\n"
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOutputFlagConflictsAreUsageErrors(t *testing.T) {
	isolated(t)
	for _, flags := range [][]string{
		{"--output-policy", "wrong"}, {"--output-policy", ""},
		{"--enqueue-output", "wrong"}, {"--enqueue-output", "quiet"},
		{"--output-policy", "files"},
		{"--output-policy", "discard", "--output", "/out"},
		{"--output-policy", "stderr-only", "--stderr", "/err"},
		{"--output-policy", "inherit", "--output", "/out"},
	} {
		args := append([]string{"add", "--schedule", "* * * * *", "--command", "echo task", "--dry-run"}, flags...)
		out, err := invoke(args...)
		var usageErr usageError
		if !errors.As(err, &usageErr) {
			t.Fatalf("%v: wanted usage error, got %v %s", flags, err, out)
		}
	}
}

func TestOutputPoliciesAreSavedAndLegacyPathFlagsCanClear(t *testing.T) {
	for _, runner := range []string{"direct", "pueue"} {
		for _, policy := range outputPolicies {
			t.Run(runner+"/"+policy, func(t *testing.T) {
				dir, _ := isolated(t)
				outputPueueFixture(t, dir)
				args := []string{"add", "--schedule", "* * * * *", "--command", "echo task", "--runner", runner, "--output-policy", policy, "--yes", "--json"}
				if policy == "files" {
					args = append(args, "--output", filepath.Join(dir, "stdout.log"), "--stderr", filepath.Join(dir, "stderr.log"))
				}
				out, err := invoke(args...)
				id := createdJobID(t, out, err)
				entry, recipe := managedEntry(t, id)
				if recipe.OutputPolicy != policy {
					t.Fatal("policy was not saved", recipe)
				}
				if runner == "pueue" && (recipe.EnqueueOutput != "quiet" || recipe.PueueSubmission == nil || entry.Command != recipe.PueueSubmission.Command+" > /dev/null") {
					t.Fatal("new Pueue job did not default to quiet with a frozen unsuppressed submission", entry, recipe)
				}
				if policy == "files" {
					if out, err := invoke("edit", id, "--output", "", "--yes"); err != nil {
						t.Fatal(out, err)
					}
					_, recipe = managedEntry(t, id)
					if recipe.OutputPolicy != "files" || recipe.Output != "" || recipe.Stderr == "" {
						t.Fatal("clearing stdout also cleared the remaining stderr redirect", recipe)
					}
					if out, err := invoke("edit", id, "--stderr", "", "--yes"); err != nil {
						t.Fatal(out, err)
					}
					entry, recipe = managedEntry(t, id)
					if recipe.OutputPolicy != "inherit" || recipe.Output != "" || recipe.Stderr != "" || strings.Contains(entry.Command, "stdout.log") || strings.Contains(entry.Command, "stderr.log") {
						t.Fatal("clearing the last legacy redirect did not restore inherit", entry, recipe)
					}
				}
			})
		}
	}
	for _, flag := range []string{"--output", "--stderr"} {
		t.Run("infer/"+flag, func(t *testing.T) {
			dir, _ := isolated(t)
			out, err := invoke("add", "--schedule", "* * * * *", "--command", "echo task", flag, filepath.Join(dir, "log"), "--yes", "--json")
			_, recipe := managedEntry(t, createdJobID(t, out, err))
			if recipe.OutputPolicy != "files" {
				t.Fatal("legacy path flag did not infer files", recipe)
			}
		})
	}
}

func TestPolicyDraftsKeepPathsWithoutCompilingInactiveValues(t *testing.T) {
	isolated(t)
	spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "add", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	values["command"] = "echo task"
	values["output"], values["stderr"], values["enqueue_output"] = "bad\x00inactive-output", "bad\nstderr", "invalid inactive notice"
	before := maps.Clone(values)
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal("inactive output draft blocked build", err)
	}
	data := review.Data.(jobReview)
	if data.Entry.Command != "echo task" || data.Recipe.Output != "" || data.Recipe.Stderr != "" || data.Recipe.EnqueueOutput != "" || !maps.Equal(values, before) {
		t.Fatal("inactive output values changed execution or draft", data, values)
	}
	values["output_policy"] = "files"
	if _, err := spec.Build(context.Background(), values); err == nil {
		t.Fatal("active invalid file draft accepted")
	}
	advanced := []string{}
	for _, field := range spec.Fields {
		if field.Advanced {
			advanced = append(advanced, field.Key)
		}
	}
	want := []string{"runner", "group", "enqueue_output", "environment", "output_policy", "output", "stderr", "log"}
	if !reflect.DeepEqual(advanced, want) {
		t.Fatal("advanced fields changed position", advanced)
	}
}

func TestLegacyPueueEnqueueMigrationPreservesExactSubmissionWithoutProbing(t *testing.T) {
	for _, preset := range []string{"command", "shell", "managed-shell"} {
		t.Run(preset, func(t *testing.T) {
			dir, cron := isolated(t)
			pueue := outputPueueFixture(t, dir)
			args := []string{"add", "--schedule", "* * * * *", "--runner", "pueue", "--enqueue-output", "inherit", "--yes", "--json"}
			switch preset {
			case "command":
				args = append(args, "--command", "printf 'literal \\% text' | cat")
			case "shell":
				script := filepath.Join(dir, "job.sh")
				if err := os.WriteFile(script, []byte("exit 99\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--preset", "shell", "--script", script, "--runtime", "/bin/sh")
			case "managed-shell":
				args = append(args, "--script-content", "exit 99\n")
			}
			out, err := invoke(args...)
			id := createdJobID(t, out, err)
			original, recipe := managedEntry(t, id)
			recipe.EnqueueOutput, recipe.OutputPolicy, recipe.PueueSubmission = "", "", nil
			if err := service.SaveRecipe(original, recipe); err != nil {
				t.Fatal(err)
			}
			// Changing scheduler SHELL, losing the old script and unavailable
			// Pueue must not alter the already generated submission.
			raw, _ := os.ReadFile(cron)
			if err := os.WriteFile(cron, append([]byte("SHELL=/bin/bash\n"), raw...), 0600); err != nil {
				t.Fatal(err)
			}
			if recipe.Script != "" {
				if err := os.Remove(recipe.Script); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(dir, "unwanted-probe")
			if err := os.WriteFile(pueue, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 99\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if preset == "command" {
				if out, err := invoke("edit", id, "--remark", "metadata-only", "--yes"); err != nil {
					t.Fatal(out, err)
				}
				entry, unchanged := managedEntry(t, id)
				if entry.Command != original.Command || unchanged.EnqueueOutput != "" || unchanged.PueueSubmission != nil || unchanged.OutputPolicy != "" {
					t.Fatal("metadata-only edit migrated legacy policy implicitly", entry, unchanged)
				}
			}
			for _, policy := range []string{"inherit", "quiet", "inherit"} {
				out, err := invoke("edit", id, "--enqueue-output", policy, "--yes", "--json")
				if err != nil {
					t.Fatal(out, err)
				}
				entry, updated := managedEntry(t, id)
				want := original.Command
				if policy == "quiet" {
					want += " > /dev/null"
				}
				if entry.Command != want || updated.PueuePath != recipe.PueuePath || !reflect.DeepEqual(updated.ScriptTask, recipe.ScriptTask) || !reflect.DeepEqual(updated.ManagedScript, recipe.ManagedScript) {
					t.Fatal("enqueue-only edit recompiled the task", entry, updated)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("enqueue-only edit probed Pueue", err)
			}
		})
	}
}

func TestInteractiveEnqueuePrefillRetainsManagedBodyForFurtherEdits(t *testing.T) {
	dir, cron := isolated(t)
	outputPueueFixture(t, dir)
	body := "echo kept\nprintf 'second line\\n'\n"
	out, err := invoke("add", "--schedule", "* * * * *", "--script-content", body, "--runner", "pueue", "--yes", "--json")
	id := createdJobID(t, out, err)
	before, err := os.ReadFile(cron)
	if err != nil {
		t.Fatal(err)
	}
	// The normal form constructor is used for mounted interactions. Prefilling
	// only enqueue output must not make another execution edit lose its script.
	spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "edit", id, map[string]string{"enqueue_output": "inherit"})
	if err != nil {
		t.Fatal(err)
	}
	if values["script_content"] != body {
		t.Fatal("prefilled interactive form did not load the managed body", values["script_content"])
	}
	values["output_policy"] = "stderr-only"
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal("another execution edit lost the preloaded body", err)
	}
	data := review.Data.(jobReview)
	if data.Plan.ManagedScript == nil || data.Plan.ManagedScript.Content != body || data.Recipe.OutputPolicy != "stderr-only" || data.Recipe.EnqueueOutput != "inherit" {
		t.Fatal("interactive execution edit did not preserve the managed content", data)
	}
	after, err := os.ReadFile(cron)
	if err != nil || string(after) != string(before) {
		t.Fatal("interactive review changed cron", err)
	}
}

func TestOutputReviewSeparatesTaskSubmissionAndCronMail(t *testing.T) {
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		for _, environment := range []map[string]string{nil, {"MAILTO": ""}, {"MAILTO": "ops@example.test"}} {
			r := service.Recipe{Runner: "pueue", EnqueueOutput: "quiet", Environment: map[string]string{"MAILTO": "inside@example.test"}}
			text := describeOutputRouting(r, environment, dialect)
			for _, want := range []string{"Pueue task logs", "submission stdout suppressed; stderr and exit status retained", "task failures do not directly become cron mail", "does not configure cron's mail delivery"} {
				if !strings.Contains(text, want) {
					t.Fatal("review conflates output boundaries", text)
				}
			}
			if dialect == schedule.Supercronic {
				if !strings.Contains(text, "does not provide cron mail") || strings.Contains(text, "may mail") || strings.Contains(text, "may be mailed") {
					t.Fatal("Supercronic review promises cron mail", text)
				}
			} else if mailto, set := environment["MAILTO"]; set {
				if mailto == "" && !strings.Contains(text, "MAILTO is empty") || mailto != "" && !strings.Contains(text, mailto) {
					t.Fatal("wrong scoped MAILTO", text)
				}
			} else if !strings.Contains(text, "MAILTO is unset") {
				t.Fatal("unset MAILTO described as empty", text)
			}
		}
	}
}

func TestStaleRecipeRunUsesNativeSourceButRejectsOverrides(t *testing.T) {
	_, cron := isolated(t)
	out, err := invoke("add", "--schedule", "* * * * *", "--command", "echo original", "--yes", "--json")
	id := createdJobID(t, out, err)
	raw, _ := os.ReadFile(cron)
	changed := strings.Replace(string(raw), "echo original", "echo exact-new-source > /dev/null", 1)
	if err := os.WriteFile(cron, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("run", id, "--dry-run", "--json")
	var plan service.ExecutionPlan
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.Command != "echo exact-new-source > /dev/null" || !strings.Contains(plan.Warning, "unchanged") {
		t.Fatal("ordinary Run rejected or transformed the native fallback", out, err)
	}
	if out, err := invoke("run", id, "--runner", "direct", "--dry-run"); err == nil || !strings.Contains(err.Error(), "unverified recipe") {
		t.Fatal("explicit override guessed a stale payload", out, err)
	}
	s := service.New(config.Defaults())
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	j, err := entry(snap, id)
	if err != nil {
		t.Fatal(err)
	}
	_, metadataErr := service.LoadRecipe(j)
	spec := runFormSpec(s, snap, j, service.Recipe{Runner: "direct", Original: j.Command}, false, metadataErr)
	if _, err := spec.Build(context.Background(), map[string]string{"runner": "pueue"}); err == nil {
		t.Fatal("TUI override guessed a stale payload")
	}
	text := runRecordText(service.RunRecord{Status: "queued"})
	if !strings.Contains(text, "task ID unavailable") {
		t.Fatal("empty task ID looked like a known ID", text)
	}
}

func TestLogOnlyEditValidatesPathWithoutRecompiling(t *testing.T) {
	dir, _ := isolated(t)
	pueue := outputPueueFixture(t, dir)
	out, err := invoke("add", "--schedule", "* * * * *", "--command", "echo task", "--runner", "pueue", "--yes", "--json")
	id := createdJobID(t, out, err)
	before, _ := managedEntry(t, id)
	if err := os.Remove(pueue); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"relative.log", "/bad\nlog", "/bad\x00log"} {
		if _, err := invoke("edit", id, "--log", invalid, "--dry-run"); err == nil {
			t.Fatal("log-only edit bypassed path validation", invalid)
		}
	}
	log := filepath.Join(dir, "inspect.log")
	if out, err := invoke("edit", id, "--log", log, "--yes"); err != nil {
		t.Fatal(out, err)
	}
	after, recipe := managedEntry(t, id)
	if after.Command != before.Command || recipe.Log != log || recipe.CommandDigest != document.Digest(before.Command) {
		t.Fatal("inspection metadata changed execution", after, recipe)
	}
}

func TestDirectToPueueDefaultsQuietAndScriptLogOnlyResolvesIndependently(t *testing.T) {
	dir, _ := isolated(t)
	outputPueueFixture(t, dir)
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte("exit 99\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("add", "--schedule", "* * * * *", "--preset", "shell", "--script", script, "--runtime", "/bin/sh", "--directory", dir, "--yes", "--json")
	id := createdJobID(t, out, err)
	if out, err := invoke("edit", id, "--runner", "pueue", "--yes"); err != nil {
		t.Fatal(out, err)
	}
	before, recipe := managedEntry(t, id)
	if recipe.EnqueueOutput != "quiet" || recipe.PueueSubmission == nil {
		t.Fatal("switching direct to Pueue did not default quiet", recipe)
	}
	// A relative inspection file resolves against the frozen script directory,
	// without needing the previously selected executable or Pueue daemon.
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recipe.PueuePath); err != nil {
		t.Fatal(err)
	}
	if out, err := invoke("edit", id, "--log", "logs/inspect.log", "--yes"); err != nil {
		t.Fatal(out, err)
	}
	after, updated := managedEntry(t, id)
	if after.Command != before.Command || updated.Log != filepath.Join(dir, "logs", "inspect.log") || !reflect.DeepEqual(updated.ScriptTask, recipe.ScriptTask) {
		t.Fatal("relative inspection path rebound task execution", after, updated)
	}
}

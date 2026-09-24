package cli

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

func TestEffectiveJobValuesRetainDraftButExcludeInactiveInputs(t *testing.T) {
	draft := map[string]string{"preset": "command", "command": "echo active", "script": "relative inactive file", "script_content": "old body\n", "runtime": "inactive runtime", "project": "inactive project", "args": "'unfinished", "runner": "direct", "group": "inactive group", "directory": "/work", "environment": "ENV=literal", "output": "/out", "stderr": "/err", "log": "/log"}
	before := maps.Clone(draft)
	effective := effectiveJobValues(draft, "/legacy/script.sh")
	for _, key := range []string{"runtime", "project", "args", "script_content", "group"} {
		if effective[key] != "" {
			t.Fatal("inactive value escaped projection", key, effective[key])
		}
	}
	if effective["command"] != draft["command"] || effective["script"] != "/legacy/script.sh" {
		t.Fatal(effective)
	}
	for _, key := range []string{"directory", "environment", "output", "stderr", "log"} {
		if effective[key] != draft[key] {
			t.Fatal("shared execution setting was discarded", key)
		}
	}
	if !maps.Equal(draft, before) {
		t.Fatal("projection discarded the user's inactive draft")
	}
	draft["preset"] = "shell"
	script := effectiveJobValues(draft, "")
	if script["script"] != before["script"] || script["args"] != before["args"] || script["runtime"] != before["runtime"] || script["command"] != "" || script["script_content"] != "" || script["project"] != "" {
		t.Fatal("switching back lost script draft or retained another payload", script)
	}
}

func TestInactivePayloadDraftsCannotBlockOrAlterBuild(t *testing.T) {
	for _, preset := range []string{"command", "executable", "shell", "python", "uv-project", "uv-script", "managed-shell"} {
		t.Run(preset, func(t *testing.T) {
			dir, cron := isolated(t)
			script := filepath.Join(dir, "existing script.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 97\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='fixture'\nversion='1'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "add", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			values["preset"], values["command"], values["script"], values["script_content"] = preset, "unused\ncommand", "relative\x00inactive-script", "unused\x00script"
			values["runtime"], values["project"], values["args"], values["group"] = "unused\x00runtime", "unused\x00project", "'incomplete", "ignored direct group"
			switch preset {
			case "command":
				values["command"] = "echo active"
			case "managed-shell":
				values["script_content"], values["runtime"], values["args"] = "echo active\nprintf 'second line\\n'\n", "/bin/sh", "'literal argument'"
			default:
				values["script"], values["args"] = script, "'literal argument'"
				if preset != "executable" {
					values["runtime"] = "/bin/sh"
				}
				if preset == "uv-project" {
					values["project"] = dir
				}
			}
			before := maps.Clone(values)
			review, err := spec.Build(context.Background(), values)
			if err != nil {
				t.Fatal("inactive draft blocked active preset", err)
			}
			data := review.Data.(jobReview)
			if strings.Contains(data.Entry.Command, "unused") || strings.Contains(data.Entry.Command, "inactive") || data.Recipe.Group != "" {
				t.Fatal("inactive inputs altered execution", data.Entry.Command, data.Recipe)
			}
			if preset == "command" && (data.Recipe.Script != "" || data.Recipe.ScriptTask != nil || data.Recipe.ManagedScript != nil) {
				t.Fatal("command retained an inactive script", data.Recipe)
			}
			if preset == "executable" && (data.Recipe.ScriptTask.Runtime != "" || data.Recipe.ScriptTask.Project != "") {
				t.Fatal("executable retained inactive interpreter/project", data.Recipe)
			}
			if preset == "managed-shell" {
				if data.Plan.ManagedScript == nil || data.Plan.ManagedScript.Content != values["script_content"] {
					t.Fatal("managed content was replaced by another payload", data.Plan)
				}
				if _, err := os.Stat(data.Plan.ManagedScript.Path); !os.IsNotExist(err) {
					t.Fatal("preview created a managed script", err)
				}
			}
			if !maps.Equal(values, before) {
				t.Fatal("Build changed retained draft values")
			}
			if _, err := os.Stat(cron); !os.IsNotExist(err) {
				t.Fatal("review wrote crontab", err)
			}
		})
	}
}

func TestCommandScriptMetadataComesFromBaselineOrExplicitFlag(t *testing.T) {
	dir, _ := isolated(t)
	legacy := filepath.Join(dir, "legacy.sh")
	if err := os.WriteFile(legacy, []byte("exit 98\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("add", "--schedule", "* * * * *", "--command", "echo original", "--script", legacy, "--yes", "--json")
	id := createdJobID(t, out, err)
	before, _ := managedEntry(t, id)
	s := service.New(config.Defaults())
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "edit", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	values["script"] = "inactive/relative/draft"
	values["args"], values["runtime"], values["project"], values["script_content"] = "'unfinished", "bad\x00runtime", "bad\x00project", "bad\x00body"
	values["remark"] = "keep the original metadata association"
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	data := review.Data.(jobReview)
	if data.Entry.Command != before.Command || data.Recipe.Script != legacy {
		t.Fatal("inactive script replaced legacy metadata", data)
	}
	updated := filepath.Join(dir, "explicit.sh")
	out, err = invoke("edit", id, "--command", "echo explicit", "--script", updated, "--yes", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	entry, recipe := managedEntry(t, id)
	if entry.Command != "echo explicit" || recipe.Script != updated || recipe.ScriptTask != nil {
		t.Fatal("explicit command+script compatibility changed", entry, recipe)
	}
}

func TestInactiveEditsPreservePinnedPueueCommandAndManagedVersions(t *testing.T) {
	for _, preset := range []string{"command", "shell", "managed-shell"} {
		t.Run(preset, func(t *testing.T) {
			dir, cron := isolated(t)
			pueue := filepath.Join(dir, "bin", "pueue")
			ready := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.0';;\ngroup) echo '{\"default\":{}}';;\n*) exit 99;;\nesac\n"
			if err := os.WriteFile(pueue, []byte(ready), 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"add", "--schedule", "* * * * *", "--runner", "pueue", "--group", "default", "--yes", "--json"}
			switch preset {
			case "command":
				args = append(args, "--command", "echo active")
			case "shell":
				path := filepath.Join(dir, "existing.sh")
				if err := os.WriteFile(path, []byte("exit 97\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--preset", "shell", "--script", path, "--runtime", "/bin/sh")
			case "managed-shell":
				args = append(args, "--script-content", "echo managed\n")
			}
			out, err := invoke(args...)
			id := createdJobID(t, out, err)
			before, originalRecipe := managedEntry(t, id)
			raw, err := os.ReadFile(cron)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cron, append([]byte("SHELL=/bin/bash\n"), raw...), 0600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "unwanted-pueue-probe")
			if err := os.WriteFile(pueue, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 99\n"), 0700); err != nil {
				t.Fatal(err)
			}
			spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "edit", id, nil)
			if err != nil {
				t.Fatal(err)
			}
			values["project"], values["remark"] = "inactive\x00project", "metadata after visiting another preset"
			if preset != "command" {
				values["command"] = "inactive\ncommand"
			}
			if preset != "managed-shell" {
				values["script_content"] = "inactive\x00body"
			}
			if preset != "shell" {
				values["script"] = "inactive\x00script"
			}
			if preset == "command" {
				values["runtime"], values["args"] = "inactive\x00runtime", "'incomplete"
			}
			review, err := spec.Build(context.Background(), values)
			if err != nil {
				t.Fatal("inactive edit caused recompilation", err)
			}
			data := review.Data.(jobReview)
			if data.Entry.Command != before.Command || !reflect.DeepEqual(data.Recipe, originalRecipe) {
				t.Fatal("inactive changes rebound pinned execution", data.Entry.Command, data.Recipe)
			}
			if _, err := spec.Apply(context.Background(), values, review); err != nil {
				t.Fatal(err)
			}
			after, recipeAfter := managedEntry(t, id)
			if after.Command != before.Command || recipeAfter.Script != originalRecipe.Script || recipeAfter.PueuePath != originalRecipe.PueuePath {
				t.Fatal("saved metadata edit changed execution", after, recipeAfter)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("inactive edit probed unavailable Pueue", err)
			}
		})
	}
}

func TestJobFieldBindingsReservePayloadAndConditionalDetails(t *testing.T) {
	isolated(t)
	spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "add", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]ui.Field{}
	for _, field := range spec.Fields {
		fields[field.Key] = field
	}
	for _, key := range []string{"command", "script", "script_content"} {
		if fields[key].Slot != "payload" {
			t.Fatal("payload does not share a fixed row", key)
		}
	}
	for _, preset := range jobPresets() {
		values["preset"] = preset
		count := 0
		for _, key := range []string{"command", "script", "script_content"} {
			if fields[key].Show(values) {
				count++
			}
		}
		if count != 1 {
			t.Fatal("payload slot does not have exactly one active member", preset, count)
		}
	}
	for _, key := range []string{"runtime", "project", "args"} {
		if !fields[key].Reserve || fields[key].Show == nil {
			t.Fatal("inactive detail does not reserve a blank row", key)
		}
	}
	for _, runner := range []string{"direct", "pueue"} {
		values["runner"] = runner
		for _, key := range []string{"group", "enqueue_output"} {
			field := fields[key]
			if field.Show != nil || field.DisabledWhen == nil || (field.DisabledWhen(values) != "") != (runner == "direct") {
				t.Fatal("Pueue row is hidden or has wrong enabled state", runner, key)
			}
		}
		for _, policy := range outputPolicies {
			values["output_policy"] = policy
			for _, key := range []string{"output", "stderr"} {
				field := fields[key]
				for _, path := range []string{"", "/keep/explicit.log"} {
					values[key] = path
					if field.Show != nil || field.DisabledWhen == nil || (field.DisabledWhen(values) != "") != (policy != "files") {
						t.Fatal("file row did not follow selected policy", runner, policy, key, path)
					}
				}
			}
		}
		if fields["log"].Show != nil || fields["log"].DisabledWhen != nil {
			t.Fatal("inspection path must stay independently enabled", runner)
		}
	}
}

func TestDirectRunIgnoresInactiveGroupDraft(t *testing.T) {
	dir, cron := isolated(t)
	out, err := invoke("add", "--schedule", "* * * * *", "--command", "echo active", "--directory", dir, "--yes", "--json")
	id := createdJobID(t, out, err)
	j, recipe := managedEntry(t, id)
	s := service.New(config.Defaults())
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	spec := runFormSpec(s, snap, j, recipe, false)
	group := spec.Fields[1]
	if group.Show != nil || group.DisabledWhen(map[string]string{"runner": "direct"}) == "" || group.DisabledWhen(map[string]string{"runner": "pueue"}) != "" {
		t.Fatal("run group row must remain rendered but conditionally enabled")
	}
	review, err := spec.Build(context.Background(), map[string]string{"runner": "direct", "group": "an inactive edited group"})
	if err != nil {
		t.Fatal(err)
	}
	plan := review.Data.(service.ExecutionPlan)
	want, _ := service.SplitPercent(j.Command)
	if plan.Command != want {
		t.Fatal("inactive group caused direct-run recompilation", plan.Command, want)
	}
	raw, err := os.ReadFile(cron)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cron, append([]byte("SHELL=/bin/bash\n"), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("run", id, "--group", "ignored-direct-group", "--dry-run", "--json")
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.Command != want {
		t.Fatal("inactive CLI group flag recompiled stored direct command", out, err)
	}
}

func TestManagedToCommandDetachesInactivePayloadWithoutDeletingVersions(t *testing.T) {
	isolated(t)
	out, err := invoke("add", "--schedule", "* * * * *", "--script-content", "echo managed\n", "--yes", "--json")
	id := createdJobID(t, out, err)
	_, original := managedEntry(t, id)
	spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "edit", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	values["preset"], values["command"] = "command", "echo only-command"
	values["script"], values["script_content"], values["runtime"] = "inactive\x00path", "inactive\x00body", "inactive\x00runtime"
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	data := review.Data.(jobReview)
	if data.Recipe.Script != "" || data.Recipe.ScriptTask != nil || data.Recipe.ManagedScript != nil || data.Plan.ManagedScript != nil || strings.Contains(data.Entry.Command, original.Script) {
		t.Fatal("inactive managed payload remained attached", data)
	}
	if _, err := spec.Apply(context.Background(), values, review); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(original.Script); err != nil || string(content) != "echo managed\n" {
		t.Fatal("switching preset removed an existing managed version", string(content), err)
	}
}

func TestPueueExplicitRedirectValuesRemainEffective(t *testing.T) {
	dir, _ := isolated(t)
	pueue := filepath.Join(dir, "bin", "pueue")
	ready := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.0';;\ngroup) echo '{\"default\":{}}';;\n*) exit 99;;\nesac\n"
	if err := os.WriteFile(pueue, []byte(ready), 0700); err != nil {
		t.Fatal(err)
	}
	spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "add", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	values["command"], values["runner"] = "echo active", "pueue"
	values["output_policy"] = "files"
	values["output"], values["stderr"], values["log"] = filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), filepath.Join(dir, "inspect.log")
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	data := review.Data.(jobReview)
	if data.Recipe.Output != values["output"] || data.Recipe.Stderr != values["stderr"] || data.Recipe.Log != values["log"] || !strings.Contains(data.Entry.Command, "stdout.log") || !strings.Contains(data.Entry.Command, "stderr.log") {
		t.Fatal("reserved Pueue rows discarded explicit redirects", data)
	}
	values["output"] = ""
	review, err = spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	data = review.Data.(jobReview)
	if data.Recipe.Output != "" || strings.Contains(data.Entry.Command, "stdout.log") || data.Recipe.Stderr != values["stderr"] || data.Recipe.Log != values["log"] {
		t.Fatal("cleared output retained a redirect or changed other paths", data)
	}
}

func TestScriptSuggestionsIgnoreInactiveProjectAndRuntime(t *testing.T) {
	dir, _ := isolated(t)
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 97\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Defaults())
	for _, preset := range []string{"executable", "shell"} {
		values := map[string]string{"preset": preset, "script": path, "runtime": "/bin/sh", "project": "inactive\x00project"}
		if preset == "executable" {
			values["runtime"] = "inactive\x00runtime"
		}
		before := maps.Clone(values)
		for _, update := range jobScriptUpdates(context.Background(), s, "local", "user", values, nil) {
			if strings.Contains(update.Hint, "NUL") || strings.Contains(update.Hint, "inactive") {
				t.Fatal("inactive value contaminated read-only discovery", preset, update)
			}
		}
		if !maps.Equal(values, before) {
			t.Fatal("discovery erased inactive drafts")
		}
	}
}

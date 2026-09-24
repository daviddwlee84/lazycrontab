package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func managedEntry(t *testing.T, id string) (service.Entry, service.Recipe) {
	t.Helper()
	snap, err := service.New(config.Defaults()).Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snap.Entries {
		if entry.ID == id {
			recipe, err := service.LoadRecipe(entry)
			if err != nil {
				t.Fatal(err)
			}
			return entry, recipe
		}
	}
	t.Fatalf("job %q was not saved", id)
	return service.Entry{}, service.Recipe{}
}

func createdJobID(t *testing.T, out string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(out, err)
	}
	var result struct {
		ID string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.ID == "" {
		t.Fatal(out, err)
	}
	return result.ID
}

func TestManagedScriptCLIReviewAndLifecycle(t *testing.T) {
	dir, cron := isolated(t)
	t.Chdir(dir)
	body := "#!/bin/sh\nprintf '%s\\n' 'hello %'\nprintf '%s\\n' 'second line'\n"
	input := filepath.Join(dir, "local source.sh")
	if err := os.WriteFile(input, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"add", "--schedule", "* * * * *", "--script-content-file", input}
	out, err := invoke(append(args, "--dry-run", "--json")...)
	if err != nil {
		t.Fatal(out, err)
	}
	var plan service.Plan
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.ManagedScript == nil || plan.ManagedScript.Content != body {
		t.Fatal(out, err)
	}
	data, _ := config.Base("data")
	if !strings.HasPrefix(plan.ManagedScript.Path, filepath.Join(data, "scripts")+string(filepath.Separator)) {
		t.Fatal(plan.ManagedScript.Path)
	}
	for _, path := range []string{cron, data, plan.ManagedScript.Path} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", path, err)
		}
	}
	if _, err := invoke(append(args, "--json")...); err == nil {
		t.Fatal("noninteractive mutation skipped approval")
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("unapproved creation wrote helper files")
	}
	out, err = invoke(append(args, "--yes", "--json")...)
	id := createdJobID(t, out, err)
	entry, recipe := managedEntry(t, id)
	if recipe.ManagedScript == nil || recipe.ScriptTask == nil || recipe.ScriptTask.Preset != "shell" || recipe.ScriptTask.Runtime != "/bin/sh" || recipe.Directory != dir {
		t.Fatalf("%+v", recipe)
	}
	stored, err := os.ReadFile(recipe.Script)
	if err != nil || string(stored) != body {
		t.Fatal(string(stored), err)
	}
	originalPath, originalCommand := recipe.Script, entry.Command
	s := service.New(config.Defaults())
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "edit", id, nil)
	if err != nil || values["preset"] != "managed-shell" || values["script_content"] != body {
		t.Fatal(values, err)
	}
	values["remark"] = "metadata only"
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(review.Text, "Managed shell script") || !strings.Contains(review.Text, body) || strings.Contains(review.Text, "script is not ready") {
		t.Fatal(review.Text)
	}
	if _, err := spec.Apply(context.Background(), values, review); err != nil {
		t.Fatal(err)
	}
	entry, recipe = managedEntry(t, id)
	if recipe.Script != originalPath || entry.Command != originalCommand || entry.Remark != "metadata only" {
		t.Fatalf("metadata edit changed script version: %+v %+v", entry, recipe)
	}
	newBody := "echo 'new body'\n"
	if out, err := invoke("edit", id, "--script-content", newBody, "--yes"); err != nil {
		t.Fatal(out, err)
	}
	_, recipe = managedEntry(t, id)
	if recipe.Script == originalPath {
		t.Fatal("content edit reused the original immutable path")
	}
	if original, err := os.ReadFile(originalPath); err != nil || string(original) != body {
		t.Fatal("old version was not retained", string(original), err)
	}
	if out, err := invoke("edit", id, "--command", `echo "hi"`, "--yes"); err != nil {
		t.Fatal(out, err)
	}
	entry, recipe = managedEntry(t, id)
	if recipe.ManagedScript != nil || recipe.ScriptTask != nil || recipe.Script != "" || !strings.Contains(entry.Command, `echo "hi"`) {
		t.Fatalf("command replacement retained hidden script metadata: %+v %+v", entry, recipe)
	}
}

func TestManagedScriptContentFlagsAreUnambiguous(t *testing.T) {
	dir, cron := isolated(t)
	file := filepath.Join(dir, "content.sh")
	if err := os.WriteFile(file, []byte("echo content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{
		{"--script-content", "echo one", "--script-content-file", file},
		{"--script-content", "echo one", "--command", "echo two"},
		{"--script-content-file", file, "--script", file},
		{"--script-content", "echo one", "--preset", "shell"},
		{"--preset", "managed-shell", "--script", file},
		{"--preset", "managed-shell"},
		{"--script-content", "  \n\t"},
		{"--script-content", "echo bad\x00content"},
		{"--script-content-file", filepath.Join(dir, "missing")},
	} {
		args := append([]string{"add", "--schedule", "* * * * *", "--dry-run", "--json"}, extra...)
		if out, err := invoke(args...); err == nil {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
	if _, err := os.Stat(cron); !os.IsNotExist(err) {
		t.Fatal("validation changed crontab")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("x", service.MaxManagedScriptBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManagedContentFile(file); err == nil {
		t.Fatal("oversized content was accepted")
	}
}

func TestManagedScriptSharedDraftHasContentAndSafeDefaults(t *testing.T) {
	dir, _ := isolated(t)
	t.Chdir(dir)
	s := service.New(config.Defaults())
	spec, values, err := newJobFormSpec(context.Background(), s, "local", "user", "add", "", map[string]string{"preset": "managed-shell", "script_content": "echo 'draft'\n"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range spec.Fields {
		if field.Key == "script_content" {
			found = field.Kind == "multiline" && field.Show(values) && field.Value == values["script_content"]
		}
		if field.Key == "script" && field.Show(values) {
			t.Fatal("managed script asks the user for a file path")
		}
	}
	if !found {
		t.Fatal("managed content field missing")
	}
	for _, update := range jobScriptUpdates(context.Background(), s, "local", "user", values, nil) {
		if update.Value != nil {
			values[update.Key] = *update.Value
		}
	}
	if values["runtime"] != "/bin/sh" || values["directory"] != dir {
		t.Fatal(values)
	}
	review, err := spec.Build(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	planned := review.Data.(jobReview).Plan.ManagedScript
	if planned == nil {
		t.Fatal("draft lacks script plan")
	}
	if _, err := os.Stat(filepath.Dir(planned.Path)); !os.IsNotExist(err) {
		t.Fatal("a draft cancelled after review created storage")
	}
}

func TestCommandAndManagedPueueReviewsDoNotSubmit(t *testing.T) {
	dir, _ := isolated(t)
	pueue := filepath.Join(dir, "bin", "pueue")
	fixture := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.0';;\ngroup) echo '{\"default\":{}}';;\n*) echo 'unexpected Pueue submission' >&2; exit 99;;\nesac\n"
	if err := os.WriteFile(pueue, []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	for _, task := range [][]string{{"--command", `echo "hi"`}, {"--script-content", "echo hi\nprintf 'done\\n'\n"}} {
		args := append([]string{"add", "--schedule", "* * * * *", "--runner", "pueue", "--group", "default", "--dry-run"}, task...)
		out, err := invoke(args...)
		if err != nil || !strings.Contains(out, "Output: captured by Pueue") || strings.Contains(out, "script is not ready") {
			t.Fatal(out, err)
		}
	}
}

func TestManagedMetadataEditPreservesPinnedCommandWhenEnvironmentChanges(t *testing.T) {
	dir, cron := isolated(t)
	pueue := filepath.Join(dir, "bin", "pueue")
	fixture := "#!/bin/sh\ncase \"$1\" in\n--version) echo 'pueue 4.0.0';;\ngroup) echo '{\"default\":{}}';;\n*) exit 99;;\nesac\n"
	if err := os.WriteFile(pueue, []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("add", "--schedule", "* * * * *", "--script-content", "echo hi\n", "--runner", "pueue", "--group", "default", "--yes", "--json")
	id := createdJobID(t, out, err)
	before, recipeBefore := managedEntry(t, id)
	raw, err := os.ReadFile(cron)
	if err != nil {
		t.Fatal(err)
	}
	// A new cron SHELL and unavailable Pueue daemon must not turn a remark
	// edit into a rewrite of the already installed execution command.
	if err := os.WriteFile(cron, append([]byte("SHELL=/bin/bash\n"), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pueue, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("edit", id, "--remark", "new reason", "--schedule", "*/5 * * * *", "--yes", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	after, recipeAfter := managedEntry(t, id)
	if after.Command != before.Command || recipeAfter.Script != recipeBefore.Script || recipeAfter.PueuePath != recipeBefore.PueuePath || after.Remark != "new reason" || after.Schedule != "*/5 * * * *" {
		t.Fatalf("metadata edit changed execution: %+v %+v", after, recipeAfter)
	}
	if out, err := invoke("edit", id, "--script-content", "echo changed\n", "--dry-run"); err == nil || !strings.Contains(err.Error(), "Pueue unavailable") {
		t.Fatal("execution change skipped capability validation", out, err)
	}
}

func TestManagedSameContentEditUsesStoredDataPath(t *testing.T) {
	dir, _ := isolated(t)
	out, err := invoke("add", "--schedule", "* * * * *", "--script-content", "echo old data root\n", "--yes", "--json")
	id := createdJobID(t, out, err)
	before, recipeBefore := managedEntry(t, id)
	oldBase, _ := config.Base("data")
	newRoot := filepath.Join(dir, "new-data-root")
	newBase := filepath.Join(newRoot, "lazycrontab")
	if err := os.MkdirAll(filepath.Join(newBase, "jobs"), 0700); err != nil {
		t.Fatal(err)
	}
	// Move the local helper metadata, retaining authoritative target scripts at
	// their original absolute paths. New script storage is unavailable here.
	files, err := os.ReadDir(filepath.Join(oldBase, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(oldBase, "jobs", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(newBase, "jobs", file.Name()), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(newBase, "scripts"), []byte("storage unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", newRoot)
	out, err = invoke("edit", id, "--remark", "same script version", "--yes", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	after, recipeAfter := managedEntry(t, id)
	if after.Command != before.Command || recipeAfter.Script != recipeBefore.Script || after.Remark != "same script version" {
		t.Fatalf("same-content edit used new data root: %+v %+v", after, recipeAfter)
	}
}

func TestManagedDraftPreviousGuardOnlyFollowsVerifiedContent(t *testing.T) {
	isolated(t)
	out, err := invoke("add", "--schedule", "* * * * *", "--script-content", "echo before\n", "--yes", "--json")
	id := createdJobID(t, out, err)
	_, recipe := managedEntry(t, id)
	for _, explicit := range []bool{false, true} {
		overrides := map[string]string{}
		if explicit {
			overrides["preset"] = "managed-shell"
			overrides["script_content"] = "echo after\n"
		}
		spec, values, err := newJobFormSpec(context.Background(), service.New(config.Defaults()), "local", "user", "edit", id, overrides)
		if err != nil {
			t.Fatal(err)
		}
		values["script_content"] = "echo after\n"
		review, err := spec.Build(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		plan := review.Data.(jobReview).Plan.ManagedScript
		if !explicit && (plan.PreviousPath != recipe.Script || plan.PreviousDigest != recipe.ManagedScript.Digest) {
			t.Fatal("verified draft lost previous version guard", plan)
		}
		if explicit && (plan.PreviousPath != "" || plan.PreviousDigest != "") {
			t.Fatal("explicit replacement unexpectedly requires old content", plan)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/spf13/cobra"
)

func managedEditFixture(t *testing.T) (string, string, *service.Service, service.Entry, service.Recipe) {
	t.Helper()
	dir, cron := isolated(t)
	out, err := invoke("add", "--schedule", "*/5 * * * *", "--script-content", "echo before\n", "--name", "managed example", "--remark", "keep this", "--env", "SAMPLE=space value", "--arg", "literal arg", "--disabled", "--yes", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	var result struct {
		ID string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.ID == "" {
		t.Fatal(out, err)
	}
	s := service.New(config.Defaults())
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	j, err := entry(snap, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := service.LoadRecipe(j)
	if err != nil || r.ManagedScript == nil {
		t.Fatal(r, err)
	}
	return dir, cron, s, j, r
}

func TestManagedScriptEditDryRunDoesNotOpenEditor(t *testing.T) {
	dir, cron, _, j, recipe := managedEditFixture(t)
	marker := filepath.Join(dir, "editor-ran")
	t.Setenv("EDIT_MARKER", marker)
	editor := filepath.Join(dir, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ntouch \"$EDIT_MARKER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	before, err := os.ReadFile(cron)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", recipe.Script} {
		args := []string{"script", "edit", j.ID, "--dry-run", "--json"}
		if path != "" {
			args = append(args, "--path", path)
		}
		out, err := invoke(args...)
		var preview map[string]string
		if err != nil || json.Unmarshal([]byte(out), &preview) != nil || preview["path"] != recipe.Script || preview["host"] != "local" {
			t.Fatal(out, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("dry-run opened the editor")
	}
	after, err := os.ReadFile(cron)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("dry-run changed the source", err)
	}
}

func TestManagedScriptEditorPublishesNewVersionAndPreservesJob(t *testing.T) {
	dir, _, s, j, recipe := managedEditFixture(t)
	t.Setenv("EDIT_DRAFT_PATH", filepath.Join(dir, "draft-path"))
	editor := filepath.Join(dir, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$EDIT_DRAFT_PATH\"\nprintf 'echo after\\necho second line\\n' > \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	if err := (&options{yes: true}).editManagedScript(cmd, s, j, recipe); err != nil {
		t.Fatal(output.String(), err)
	}
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := entry(snap, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := service.LoadRecipe(updated)
	if err != nil || r.Script == recipe.Script || r.ManagedScript == nil || r.Environment["SAMPLE"] != "space value" || r.Runner != recipe.Runner || r.ScriptTask.Runtime != recipe.ScriptTask.Runtime || len(r.ScriptTask.Args) != 1 || r.ScriptTask.Args[0] != "literal arg" {
		t.Fatal(r, err)
	}
	if updated.Name != j.Name || updated.Remark != j.Remark || updated.Schedule != j.Schedule || updated.Enabled != j.Enabled {
		t.Fatalf("edit changed job metadata: %+v", updated)
	}
	oldBody, err := os.ReadFile(recipe.Script)
	if err != nil || string(oldBody) != "echo before\n" {
		t.Fatal("changed an existing immutable version", string(oldBody), err)
	}
	newBody, err := os.ReadFile(r.Script)
	if err != nil || string(newBody) != "echo after\necho second line\n" {
		t.Fatal(string(newBody), err)
	}
	draft, err := os.ReadFile(os.Getenv("EDIT_DRAFT_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(draft)); !os.IsNotExist(err) {
		t.Fatal("private editor draft was not removed")
	}
}

func TestManagedScriptEditorRejectsSourceChangedDuringEditor(t *testing.T) {
	dir, cron, s, j, recipe := managedEditFixture(t)
	editor := filepath.Join(dir, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'echo replacement\\n' > \"$1\"\nprintf '# concurrent edit\\n' >> \"$FIXTURE_CRON\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	if err := (&options{yes: true}).editManagedScript(cmd, s, j, recipe); err == nil {
		t.Fatal("accepted source changed in the editor")
	}
	raw, err := os.ReadFile(cron)
	if err != nil || !strings.Contains(string(raw), "# concurrent edit") || !strings.Contains(string(raw), recipe.Script) {
		t.Fatal(string(raw), err)
	}
	versions, err := filepath.Glob(filepath.Join(filepath.Dir(recipe.Script), "*.sh"))
	if err != nil || len(versions) != 1 {
		t.Fatal("published script despite stale source", versions, err)
	}
}

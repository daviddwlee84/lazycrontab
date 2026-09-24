package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
)

func TestSourcesShowPreservesExactContentAndReportsMetadata(t *testing.T) {
	_, cron := isolated(t)
	raw := "# original\r\nPATH=/bin\r\n* * * * * echo hi  "
	if err := os.WriteFile(cron, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("sources", "show")
	if err != nil || out != raw {
		t.Fatalf("raw bytes changed: %q, %v", out, err)
	}
	out, err = invoke("sources", "show", "--json")
	var view rawSourceView
	if err != nil || json.Unmarshal([]byte(out), &view) != nil {
		t.Fatal(out, err)
	}
	if view.Host != "local" || view.Source != "user" || view.Kind != "user" || view.Path != "" || view.Revision != document.Digest(raw) || view.Content != raw || view.ReadOnly || !view.Exists {
		t.Fatalf("%+v", view)
	}
	if err := os.Remove(cron); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("sources", "show", "--json")
	if err != nil || json.Unmarshal([]byte(out), &view) != nil || view.Exists || view.Content != "" {
		t.Fatal(out, err)
	}
}

func TestSourcesRawFilePreviewApprovalAndBackup(t *testing.T) {
	dir, cron := isolated(t)
	before, after := "# keep exact original\n* * * * * echo original\n", "# replacement\n*/5 * * * * echo changed\n"
	if err := os.WriteFile(cron, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "replacement.cron")
	if err := os.WriteFile(input, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("sources", "edit-raw", "--file", input, "--dry-run", "--json")
	var plan service.Plan
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.Before != before || plan.After != after || plan.Operation != "edit-source" || plan.Revision != document.Digest(before) {
		t.Fatal(out, err)
	}
	if actual, err := os.ReadFile(cron); err != nil || string(actual) != before {
		t.Fatal("dry run changed source", string(actual), err)
	}
	if backups, err := service.Backups(); err != nil || len(backups) != 0 {
		t.Fatal("dry run created backups", backups, err)
	}
	if out, err := invoke("sources", "edit-raw", "--file", input, "--json"); err == nil {
		t.Fatal("unapproved replacement succeeded", out)
	}
	out, err = invoke("sources", "edit-raw", "--file", input, "--yes", "--json")
	var receipt service.Receipt
	if err != nil || json.Unmarshal([]byte(out), &receipt) != nil || receipt.Status != "saved" || receipt.Backup == "" {
		t.Fatal(out, err)
	}
	if actual, err := os.ReadFile(cron); err != nil || string(actual) != after {
		t.Fatal(string(actual), err)
	}
	backups, err := service.Backups()
	if err != nil || len(backups) != 1 || backups[0].Content != before {
		t.Fatal(backups, err)
	}
	if out, err := invoke("sources", "edit-raw", "--file", input, "--yes", "--json"); err != nil || !strings.Contains(out, "unchanged") {
		t.Fatal(out, err)
	}
	if backups, err := service.Backups(); err != nil || len(backups) != 1 {
		t.Fatal("unchanged input created a backup", backups, err)
	}
}

func TestSourcesRawEditHeadlessPolicy(t *testing.T) {
	dir, _ := isolated(t)
	marker := filepath.Join(dir, "editor-opened")
	t.Setenv("RAW_EDITOR_MARKER", marker)
	editorPath := filepath.Join(dir, "editor")
	if err := os.WriteFile(editorPath, []byte("#!/bin/sh\ntouch \"$RAW_EDITOR_MARKER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editorPath)
	for _, args := range [][]string{
		{"sources", "edit-raw", "--dry-run"},
		{"sources", "edit-raw", "--json", "--yes"},
		{"sources", "edit-raw", "--yes"},
		{"sources", "edit-raw", "--file", "", "--dry-run"},
		{"sources", "show", "--host", "all"},
		{"sources", "show", "--source", "all"},
	} {
		if out, err := invoke(args...); err == nil {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("headless/dry-run command opened editor")
	}
}

func rawEditorFixture(t *testing.T, script string) (string, string, *service.Service, service.Snapshot, *cobra.Command, *bytes.Buffer) {
	t.Helper()
	dir, cron := isolated(t)
	if err := os.WriteFile(cron, []byte("# original\n* * * * * echo original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Defaults())
	snap, err := s.Snapshot(context.Background(), "local", "user")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAW_DRAFT_PATH", filepath.Join(dir, "draft-path"))
	t.Setenv("RAW_DRAFT_MODE", filepath.Join(dir, "draft-mode"))
	editorPath := filepath.Join(dir, "editor")
	if err := os.WriteFile(editorPath, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$RAW_DRAFT_PATH\"\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editorPath)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	output := &bytes.Buffer{}
	cmd.SetOut(output)
	return dir, cron, s, snap, cmd, output
}

func assertRawDraftRemoved(t *testing.T) {
	t.Helper()
	path, err := os.ReadFile(os.Getenv("RAW_DRAFT_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
		t.Fatal("raw editor draft not removed", string(path), err)
	}
}

func TestSourcesRawEditorPrivateCopyAndApply(t *testing.T) {
	_, cron, s, snap, cmd, output := rawEditorFixture(t, "ls -ld \"$1\" | cut -c1-10 > \"$RAW_DRAFT_MODE\"\nprintf '# revised\\n0 * * * * echo revised\\n' > \"$1\"\n")
	if err := (&options{yes: true}).editRawSource(cmd, s, snap); err != nil {
		t.Fatal(output.String(), err)
	}
	if raw, err := os.ReadFile(cron); err != nil || string(raw) != "# revised\n0 * * * * echo revised\n" {
		t.Fatal(string(raw), err)
	}
	if mode, err := os.ReadFile(os.Getenv("RAW_DRAFT_MODE")); err != nil || strings.TrimSpace(string(mode)) != "-rw-------" {
		t.Fatal("editor draft permissions", string(mode), err)
	}
	assertRawDraftRemoved(t)
}

func TestSourcesRawEditorUnchangedAndErrorsLeaveSource(t *testing.T) {
	for name, script := range map[string]string{
		"unchanged":         "exit 0\n",
		"editor-failed":     "printf '* * * * * echo unsaved\\n' > \"$1\"\nexit 9\n",
		"validation-failed": "printf 'not valid cron\\n' > \"$1\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, cron, s, snap, cmd, output := rawEditorFixture(t, script)
			err := (&options{yes: true}).editRawSourceWithRetry(cmd, s, snap, func(context.Context, string, string, bool, string) (bool, error) {
				return false, nil
			})
			if name == "unchanged" && (err != nil || !strings.Contains(output.String(), "No changes")) {
				t.Fatal(output.String(), err)
			}
			if name != "unchanged" && err == nil {
				t.Fatal("expected error", output.String())
			}
			if raw, err := os.ReadFile(cron); err != nil || string(raw) != snap.Document.Raw {
				t.Fatal("source changed", string(raw), err)
			}
			if backups, err := service.Backups(); err != nil || len(backups) != 0 {
				t.Fatal("failed/unchanged editor created backup", backups, err)
			}
			assertRawDraftRemoved(t)
		})
	}
}

func TestSourcesRawEditorRetryKeepsDraftAndOriginalRevision(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint("conflict=", conflict), func(t *testing.T) {
			_, cron, s, snap, cmd, output := rawEditorFixture(t, "if [ ! -f \"$RAW_DRAFT_MODE\" ]; then\n  touch \"$RAW_DRAFT_MODE\"\n  printf 'invalid draft\\n' > \"$1\"\nfi\n")
			retries := 0
			err := (&options{yes: true}).editRawSourceWithRetry(cmd, s, snap, func(_ context.Context, title, diagnostic string, _ bool, _ string) (bool, error) {
				retries++
				if retries != 1 || !strings.Contains(title, "local/user") || diagnostic == "" {
					t.Fatal(retries, title, diagnostic)
				}
				path, err := os.ReadFile(os.Getenv("RAW_DRAFT_PATH"))
				if err != nil {
					t.Fatal(err)
				}
				if draft, err := os.ReadFile(string(path)); err != nil || string(draft) != "invalid draft\n" {
					t.Fatal("invalid draft was lost before retry", string(draft), err)
				}
				if raw, err := os.ReadFile(cron); err != nil || string(raw) != snap.Document.Raw {
					t.Fatal("validation changed source", string(raw), err)
				}
				if conflict {
					if err := os.WriteFile(cron, []byte(snap.Document.Raw+"# concurrent\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// The next editor invocation keeps this same draft. Correcting the
				// content cannot replace the snapshot used for its original review.
				if err := os.WriteFile(string(path), []byte("*/5 * * * * echo corrected\n"), 0600); err != nil {
					t.Fatal(err)
				}
				return true, nil
			})
			if conflict && (err == nil || !strings.Contains(err.Error(), "source changed")) {
				t.Fatal("retry lost base revision", output.String(), err)
			}
			if !conflict && err != nil {
				t.Fatal(output.String(), err)
			}
			if retries != 1 {
				t.Fatal("unexpected retry count", retries)
			}
			want := "*/5 * * * * echo corrected\n"
			if conflict {
				want = snap.Document.Raw + "# concurrent\n"
			}
			if raw, err := os.ReadFile(cron); err != nil || string(raw) != want {
				t.Fatal(string(raw), err)
			}
			assertRawDraftRemoved(t)
		})
	}
}

func TestSourcesRawEditorDiscardInvalidDraft(t *testing.T) {
	_, cron, s, snap, cmd, _ := rawEditorFixture(t, "printf 'invalid draft\\n' > \"$1\"\n")
	err := (&options{yes: true}).editRawSourceWithRetry(cmd, s, snap, func(context.Context, string, string, bool, string) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ui.ErrCancelled) {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(cron); err != nil || string(raw) != snap.Document.Raw {
		t.Fatal("discard changed source", string(raw), err)
	}
	assertRawDraftRemoved(t)
}

func TestSourcesRawEditorConflictsAreNotOverwritten(t *testing.T) {
	_, cron, s, snap, cmd, output := rawEditorFixture(t, "printf '* * * * * echo mine\\n' > \"$1\"\nprintf '# concurrent editor\\n' >> \"$FIXTURE_CRON\"\n")
	if err := (&options{yes: true}).editRawSource(cmd, s, snap); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatal(output.String(), err)
	}
	if raw, err := os.ReadFile(cron); err != nil || string(raw) != snap.Document.Raw+"# concurrent editor\n" {
		t.Fatal("concurrent change was overwritten", string(raw), err)
	}
	assertRawDraftRemoved(t)
}

func TestSourcesRawEditorCancellationCleansPrivateDraft(t *testing.T) {
	_, cron, s, snap, cmd, _ := rawEditorFixture(t, "exec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	if err := (&options{yes: true}).editRawSource(cmd, s, snap); err == nil || ctx.Err() == nil {
		t.Fatal("cancelled editor did not terminate", err)
	}
	if raw, err := os.ReadFile(cron); err != nil || string(raw) != snap.Document.Raw {
		t.Fatal("cancelled editor changed source", string(raw), err)
	}
	assertRawDraftRemoved(t)
}

func TestSourcesRawReadOnlySourcesNeverOpenEditor(t *testing.T) {
	dir, _, s, snap, cmd, _ := rawEditorFixture(t, "exit 99\n")
	for _, kind := range []string{"user", "system"} {
		s.Config.Sources = []config.Source{{ID: "user", Host: "local", Kind: kind, ReadOnly: kind == "user"}}
		if err := (&options{yes: true}).editRawSource(cmd, s, snap); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatal(kind, err)
		}
	}
	if _, err := os.Stat(os.Getenv("RAW_DRAFT_PATH")); !os.IsNotExist(err) {
		t.Fatal("read-only source opened editor")
	}
	input := filepath.Join(dir, "replacement")
	if err := os.WriteFile(input, []byte("* * * * * echo changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "readonly.toml")
	if err := os.WriteFile(configPath, []byte("[[sources]]\nid='user'\nhost='local'\nkind='user'\nread_only=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := invoke("--config", configPath, "sources", "edit-raw", "--file", input, "--dry-run"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatal(out, err)
	}
}

func TestReadRawSourceFileHasAnIndependentBound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.cron")
	content := "# " + strings.Repeat("x", service.MaxManagedScriptBytes+1) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readRawSourceFile(path); err != nil || got != content {
		t.Fatal("raw source was limited by managed script size", len(got), err)
	}
	if err := os.Truncate(path, int64(service.MaxRawSourceBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := readRawSourceFile(path); err == nil {
		t.Fatal("unbounded source accepted")
	}
	if _, err := readRawSourceFile(dir); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestSourcesShowFileAndReadOnlyMetadata(t *testing.T) {
	dir, _ := isolated(t)
	path := filepath.Join(dir, "system.cron")
	content := "# system fixture\n* * * * * root echo hi\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "sources.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("[[sources]]\nid='system'\nhost='local'\nkind='system'\npath=%q\n", path)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke("--config", configPath, "--source", "system", "sources", "show", "--json")
	var view rawSourceView
	if err != nil || json.Unmarshal([]byte(out), &view) != nil || view.Path != path || view.Kind != "system" || !view.ReadOnly || view.Content != content || !view.Exists {
		t.Fatal(out, err)
	}
}

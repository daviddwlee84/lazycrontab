package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/completioncache"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

func invokeCompletion(t *testing.T, args ...string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	root := newRoot(&options{version: "test"})
	root.SetArgs(append([]string{"__complete"}, args...))
	var out, diagnostics bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(out.String(), diagnostics.String(), err)
	}
	if strings.Contains(diagnostics.String(), "[Debug]") || strings.Contains(diagnostics.String(), "[Error]") {
		t.Fatal("completion leaked an error", diagnostics.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, ":") {
		t.Fatal("missing completion directive", out.String(), diagnostics.String())
	}
	directive, err := strconv.Atoi(strings.TrimPrefix(last, ":"))
	if err != nil {
		t.Fatal(out.String(), err)
	}
	return lines[:len(lines)-1], cobra.ShellCompDirective(directive)
}

func completionValues(values []string) []string {
	result := []string{}
	for _, value := range values {
		id, _, _ := strings.Cut(value, "\t")
		result = append(result, id)
	}
	return result
}

func completionFixture(t *testing.T) (string, config.Config) {
	t.Helper()
	dir, _ := isolated(t)
	c := config.Defaults()
	c.Hosts = []config.Host{{ID: "lab", SSH: "lab-alias"}, {ID: "alpha", SSH: "alpha-alias"}}
	c.Sources = []config.Source{
		{ID: "finance", Host: "local", Kind: "file", Path: filepath.Join(dir, "finance.cron"), Dialect: "system"},
		{ID: "system", Host: "local", Kind: "system", Path: filepath.Join(dir, "system.cron")},
		{ID: "remoteWork", Host: "lab", Kind: "file", Path: "/srv/worker.cron"},
	}
	c.Path = filepath.Join(dir, "completion.toml")
	data, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPLETION_BACKEND_MARKER", filepath.Join(dir, "backend-called"))
	for _, name := range []string{"crontab", "ssh", "dev", "pueue", "supercronic", "sh"} {
		if err := os.WriteFile(filepath.Join(dir, "bin", name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$COMPLETION_BACKEND_MARKER\"\nexit 97\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if content, err := os.ReadFile(os.Getenv("COMPLETION_BACKEND_MARKER")); !os.IsNotExist(err) {
			t.Errorf("completion invoked a backend: %s %v", content, err)
		}
	})
	return dir, c
}

func TestCompletionSourceAndHostIDsAreOfflineAndScoped(t *testing.T) {
	_, c := completionFixture(t)
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"sources", "edit", ""}, []string{"finance", "system", "user"}},
		{[]string{"sources", "remove", ""}, []string{"finance", "system"}},
		{[]string{"--host", "lab", "sources", "edit", ""}, []string{"remoteWork", "user"}},
		{[]string{"sources", "edit", "-H", "lab", "remote"}, []string{"remoteWork"}},
		{[]string{"sources", "edit", "--host=lab", "remote"}, []string{"remoteWork"}},
		{[]string{"--host", "lab", "sources", "remove", ""}, []string{"remoteWork"}},
		{[]string{"--host", "all", "sources", "edit", ""}, []string{}},
		{[]string{"hosts", "edit", ""}, []string{"alpha", "lab", "local"}},
		{[]string{"hosts", "remove", ""}, []string{"alpha", "lab"}},
		{[]string{"hosts", "test", "l"}, []string{"lab", "local"}},
		{[]string{"hosts", "authenticate", ""}, []string{"alpha", "lab"}},
		{[]string{"--host", ""}, []string{"all", "alpha", "lab", "local"}},
		{[]string{"--host", "lab", "--source", ""}, []string{"all", "remoteWork", "user"}},
		{[]string{"--host", "all", "--source", ""}, []string{"all", "finance", "remoteWork", "system", "user"}},
		{[]string{"--host", "unknown", "--source", ""}, []string{}},
		{[]string{"sources", "edit", "finance", ""}, []string{}},
		{[]string{"hosts", "remove", "lab", ""}, []string{}},
		{[]string{"hosts", "add", ""}, []string{}},
	} {
		args := append([]string{"--config", c.Path}, test.args...)
		got, directive := invokeCompletion(t, args...)
		if values := completionValues(got); !reflect.DeepEqual(values, test.want) || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%v: got %v (%v), want %v", test.args, got, directive, test.want)
		}
	}
	t.Setenv("LAZYCRONTAB_HOST", "lab")
	got, _ := invokeCompletion(t, "--config", c.Path, "sources", "edit", "")
	if !reflect.DeepEqual(completionValues(got), []string{"remoteWork", "user"}) {
		t.Fatal("target env did not override default", got)
	}
	got, _ = invokeCompletion(t, "--config", c.Path, "-H", "local", "sources", "edit", "")
	if !reflect.DeepEqual(completionValues(got), []string{"finance", "system", "user"}) {
		t.Fatal("explicit target did not override env", got)
	}
}

func TestCompletionJobIDsUseOnlyTargetCache(t *testing.T) {
	_, c := completionFixture(t)
	completioncache.Save(c, "local", "user", []completioncache.Entry{
		{ID: "seed-enabled", Name: "Named local job", Enabled: true},
		{ID: "seed-disabled", Name: "Stopped local job", Enabled: false},
		{ID: "-unsafe", Name: "must not become a flag", Enabled: true},
		{ID: "unsafe;command", Name: "must not become a shell token", Enabled: true},
	})
	completioncache.Save(c, "lab", "user", []completioncache.Entry{{ID: "remote-seed", Name: "Remote job", Enabled: true}})
	completioncache.Save(c, "local", "system", []completioncache.Entry{{ID: "system-seed", Name: "System job", Enabled: true, ReadOnly: true}})
	before := completionCacheFiles(t)
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"show", ""}, []string{"seed-disabled", "seed-enabled"}},
		{[]string{"edit", "seed-"}, []string{"seed-disabled", "seed-enabled"}},
		{[]string{"enable", ""}, []string{"seed-disabled"}},
		{[]string{"disable", ""}, []string{"seed-enabled"}},
		{[]string{"logs", "seed-enabled", ""}, []string{}},
		{[]string{"script", "edit", ""}, []string{"seed-disabled", "seed-enabled"}},
		{[]string{"--host", "lab", "run", ""}, []string{"remote-seed"}},
		{[]string{"--source", "finance", "edit", ""}, []string{}},
		{[]string{"--source", "system", "show", ""}, []string{"system-seed"}},
		{[]string{"--source", "system", "edit", ""}, []string{}},
		{[]string{"--source", "system", "run", ""}, []string{}},
		{[]string{"--host", "all", "edit", ""}, []string{}},
		{[]string{"--source", "all", "show", ""}, []string{}},
	} {
		got, directive := invokeCompletion(t, append([]string{"--config", c.Path}, test.args...)...)
		if values := completionValues(got); !reflect.DeepEqual(values, test.want) || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%v: got %v (%v), want %v", test.args, got, directive, test.want)
		}
	}
	got, _ := invokeCompletion(t, "--config", c.Path, "show", "seed-enabled")
	if len(got) != 1 || !strings.Contains(got[0], "Named local job") || !strings.Contains(got[0], "enabled") || !strings.Contains(got[0], "cached") {
		t.Fatal("cached descriptive name missing", got)
	}
	t.Setenv("LAZYCRONTAB_HOST", "lab")
	got, _ = invokeCompletion(t, "--config", c.Path, "show", "")
	if !reflect.DeepEqual(completionValues(got), []string{"remote-seed"}) {
		t.Fatal("wrong cache target", got)
	}
	if after := completionCacheFiles(t); !reflect.DeepEqual(before, after) {
		t.Fatal("Tab changed its cache")
	}
}

func completionCacheFiles(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(os.Getenv("XDG_CACHE_HOME"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%v %d %s", info.Mode(), info.ModTime().UnixNano(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCompletionSelectionPrecedenceMatchesCommands(t *testing.T) {
	_, c := completionFixture(t)
	c.DefaultHost, c.DefaultSource = "lab", "remoteWork"
	data, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	completioncache.Save(c, "lab", "remoteWork", []completioncache.Entry{{ID: "config-default", Enabled: true}})
	completioncache.Save(c, "local", "user", []completioncache.Entry{{ID: "env-default", Enabled: true}})
	completioncache.Save(c, "alpha", "user", []completioncache.Entry{{ID: "explicit-default", Enabled: true}})
	got, _ := invokeCompletion(t, "--config", c.Path, "edit", "")
	if !reflect.DeepEqual(completionValues(got), []string{"config-default"}) {
		t.Fatal("ignored config defaults", got)
	}
	t.Setenv("LAZYCRONTAB_HOST", "local")
	t.Setenv("LAZYCRONTAB_SOURCE", "user")
	got, _ = invokeCompletion(t, "--config", c.Path, "edit", "")
	if !reflect.DeepEqual(completionValues(got), []string{"env-default"}) {
		t.Fatal("ignored env overrides", got)
	}
	got, _ = invokeCompletion(t, "--config", c.Path, "edit", "--host=alpha", "--source=user", "")
	if !reflect.DeepEqual(completionValues(got), []string{"explicit-default"}) {
		t.Fatal("ignored explicit target flags", got)
	}
}

func TestCompletionFlagsAndFreeformArgumentsUseCorrectDirectives(t *testing.T) {
	_, c := completionFixture(t)
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"add", "--runner", ""}, []string{"direct", "pueue"}},
		{[]string{"add", "--output-policy", ""}, []string{"discard", "files", "inherit", "stderr-only"}},
		{[]string{"edit", "job", "--enqueue-output", ""}, []string{"inherit", "quiet"}},
		{[]string{"add", "--preset", "managed"}, []string{"managed-shell"}},
		{[]string{"sources", "add", "--kind", ""}, []string{"file", "system", "user"}},
		{[]string{"schedule", "next", "--dialect", "s"}, []string{"supercronic", "system"}},
		{[]string{"schedule", "explain", "--locale", ""}, []string{"en", "zh_TW"}},
		{[]string{"hosts", "add", "--from", ""}, []string{"dev", "ssh"}},
		{[]string{"hosts", "discover", "--from", "s"}, []string{"ssh"}},
		{[]string{"schedule", "next", "--from", ""}, []string{}},
		{[]string{"--color", ""}, []string{"always", "auto", "never"}},
		{[]string{"completion", ""}, []string{"bash", "fish", "powershell", "zsh"}},
		{[]string{"completion", "zsh", ""}, []string{}},
		{[]string{"add", "--command", ""}, []string{}},
		{[]string{"add", "--script", ""}, []string{}},
		{[]string{"--host", "lab", "add", "--directory", ""}, []string{}},
		{[]string{"sources", "edit", "finance", "--path", ""}, []string{}},
		{[]string{"schedule", "explain", ""}, []string{}},
		{[]string{"list", ""}, []string{}},
	} {
		got, directive := invokeCompletion(t, append([]string{"--config", c.Path}, test.args...)...)
		if values := completionValues(got); !reflect.DeepEqual(values, test.want) || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%v: got %v (%v), want %v", test.args, got, directive, test.want)
		}
	}
	for _, args := range [][]string{{"--config", ""}, {"add", "--script-content-file", ""}, {"sources", "edit-raw", "--file", ""}} {
		got, directive := invokeCompletion(t, args...)
		if len(got) != 0 || directive != cobra.ShellCompDirectiveDefault {
			t.Fatalf("local path %v disabled file completion: %v (%v)", args, got, directive)
		}
	}
}

func TestCompletionBadSettingsKeepStaticCommandsAndEnums(t *testing.T) {
	dir, _ := completionFixture(t)
	broken := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(broken, []byte("[[not valid"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"sources", "edit", ""}, {"--host", ""}, {"show", ""}, {"backup", "restore", ""}} {
		got, directive := invokeCompletion(t, append([]string{"--config", broken}, args...)...)
		if len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatal(got, directive)
		}
	}
	got, directive := invokeCompletion(t, "--config", broken, "add", "--runner", "p")
	if !reflect.DeepEqual(completionValues(got), []string{"pueue"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatal(got, directive)
	}
	got, _ = invokeCompletion(t, "--config", broken, "sou")
	if !reflect.DeepEqual(completionValues(got), []string{"sources"}) {
		t.Fatal("static commands disappeared with broken config", got)
	}
	got, _ = invokeCompletion(t, "sources", "edit", "")
	if !reflect.DeepEqual(completionValues(got), []string{"user"}) {
		t.Fatal("missing default config did not use implicit user source", got)
	}
}

func TestCompletionBackupMetadataIsBoundedAndFiltered(t *testing.T) {
	dir, c := completionFixture(t)
	base, _ := config.Base("state")
	backupDir := filepath.Join(base, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, backup := range []service.Backup{
		{ID: "001-local", Host: "local", Source: "user", Time: time.Now(), Content: strings.Repeat("SECRET COMMAND BODY", 1<<16)},
		{ID: "002-lab", Host: "lab", Source: "user", Time: time.Now(), Content: "MUST NOT BE DISPLAYED"},
		{ID: "003-file", Host: "lab", Source: "remoteWork", Time: time.Now()},
	} {
		data, err := json.Marshal(backup)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backupDir, backup.ID+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(backupDir, "malformed.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"backup", "restore", ""}, []string{"001-local", "002-lab", "003-file"}},
		{[]string{"--host", "lab", "backup", "restore", ""}, []string{"002-lab", "003-file"}},
		{[]string{"--source", "user", "backup", "restore", ""}, []string{"001-local", "002-lab"}},
		{[]string{"--host", "lab", "--source", "user", "backup", "restore", ""}, []string{"002-lab"}},
		{[]string{"backup", "restore", "001-local", ""}, []string{}},
	} {
		got, directive := invokeCompletion(t, append([]string{"--config", c.Path}, test.args...)...)
		if values := completionValues(got); !reflect.DeepEqual(values, test.want) || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%v: %v %v", test.args, got, directive)
		}
		if strings.Contains(strings.Join(got, ""), "BODY") || strings.Contains(strings.Join(got, ""), "DISPLAYED") {
			t.Fatal("completion exposed backup content", got)
		}
	}
	oversizedHeader := filepath.Join(dir, "oversized-header.json")
	if err := os.WriteFile(oversizedHeader, []byte(fmt.Sprintf(`{"unknown":%q,"id":"id","host":"local","source":"user","time":"2026-01-01T00:00:00Z"}`, strings.Repeat("x", 65<<10))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBackupCompletion(oversizedHeader); ok {
		t.Fatal("backup metadata reader exceeded its limit")
	}
}

func TestCompletionDescriptionsAndHelpAreSanitized(t *testing.T) {
	completionFixture(t)
	choice := completionChoice("safe-id", "\x1b[31mname\x1b[0m\nsecond\tcolumn\u202e"+strings.Repeat("x", 200))
	if strings.Count(choice, "\t") != 1 || strings.ContainsAny(choice, "\n\x1b\u202e") || len([]rune(strings.SplitN(choice, "\t", 2)[1])) > 120 {
		t.Fatal(choice)
	}
	values := completionResults([]string{"-flag", "bad\nvalue", "a;bad", "good-id\tOne", "good-id\tTwo", "another\tOther"}, "g")
	if !reflect.DeepEqual(values, []string{"good-id\tOne"}) {
		t.Fatal(values)
	}
	got, directive := invokeCompletion(t, "help", "cron-")
	if !reflect.DeepEqual(completionValues(got), []string{"cron-fields"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatal(got, directive)
	}
	got, _ = invokeCompletion(t, "help", "sources", "edit")
	if !reflect.DeepEqual(completionValues(got), []string{"edit", "edit-raw"}) {
		t.Fatal(got)
	}
}

func TestBackupCompletionSkipsNonregularInputsBeforeOpening(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBackupCompletion(fifo); ok {
		t.Fatal("completion opened a FIFO")
	}
	path := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(path, []byte(`{"id":"valid","host":"local","source":"user","time":"2026-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBackupCompletion(link); ok {
		t.Fatal("completion followed a backup symlink")
	}
}

func TestBashGeneratorUsesLiveCompletionBridge(t *testing.T) {
	isolated(t)
	out, err := invoke("completion", "bash")
	if err != nil || !strings.Contains(out, "__complete") || !strings.Contains(out, "requestComp=") {
		t.Fatal("Bash generator lacks live completion bridge", err)
	}
}

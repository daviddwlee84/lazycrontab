package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolated(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, filepath.Join(dir, key))
	}
	t.Setenv("LAZYCRONTAB_CONFIG", "")
	t.Setenv("LAZYCRONTAB_HOST", "")
	t.Setenv("LAZYCRONTAB_SOURCE", "")
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0700)
	cron := filepath.Join(dir, "crontab")
	t.Setenv("FIXTURE_CRON", cron)
	script := "#!/bin/sh\ncase \"$1\" in\n-l) if [ -f \"$FIXTURE_CRON\" ]; then cat \"$FIXTURE_CRON\"; else echo 'no crontab for test' >&2; exit 1; fi;;\n-) cat > \"$FIXTURE_CRON\";;\n*) exit 2;;\nesac\n"
	os.WriteFile(filepath.Join(bin, "crontab"), []byte(script), 0700)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return dir, cron
}
func invoke(args ...string) (string, error) {
	o := &options{version: "test"}
	root := newRoot(o)
	root.SetArgs(args)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}
func TestCommandLifecycleWithIsolatedCrontab(t *testing.T) {
	_, cron := isolated(t)
	raw := "# retain this header\nPATH=/usr/bin:/bin\n"
	os.WriteFile(cron, []byte(raw), 0600)
	out, e := invoke("add", "--when", "every weekday at 09:00", "--command", "printf 'hello'", "--name", "backup", "--dry-run", "--json")
	if e != nil || !json.Valid([]byte(out)) {
		t.Fatal(out, e)
	}
	b, _ := os.ReadFile(cron)
	if string(b) != raw {
		t.Fatal("dry-run changed crontab")
	}
	out, e = invoke("add", "--when", "every weekday at 09:00", "--command", "printf 'hello'", "--name", "backup", "--yes", "--json")
	if e != nil {
		t.Fatal(out, e)
	}
	var result struct {
		ID string `json:"job_id"`
	}
	json.Unmarshal([]byte(out), &result)
	if result.ID == "" {
		t.Fatal(out)
	}
	out, e = invoke("list", "--json")
	if e != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "backup") {
		t.Fatal(out, e)
	}
	if _, e = invoke("disable", result.ID, "--yes"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(cron)
	if !strings.Contains(string(b), "# lazycrontab-disabled:") || !strings.HasPrefix(string(b), raw) {
		t.Fatal(string(b))
	}
	if _, e = invoke("enable", result.ID, "--yes"); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("edit", result.ID, "--remark", "changed", "--yes"); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("remove", result.ID, "--yes"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(cron)
	if string(b) != raw {
		t.Fatal(string(b))
	}
}
func TestNoPromptErrorsAndOfflineHelp(t *testing.T) {
	dir, _ := isolated(t)
	for _, args := range [][]string{{"add", "--command", "echo hi"}, {"add", "--when", "every 90 minutes", "--interactive"}, {"add", "--unknwon"}, {"add", "--interactive", "--json"}, {"--json"}, {"schedule", "next", "* * * * *", "--count", "0"}} {
		if out, e := invoke(args...); e == nil {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
	broken := filepath.Join(dir, "broken.toml")
	os.WriteFile(broken, []byte("[invalid"), 0600)
	for _, cmd := range []string{"--help", "version"} {
		if _, e := invoke("--config", broken, cmd); e != nil {
			t.Fatal(cmd, e)
		}
	}
}
func TestRegistrationAndRecipeMetadata(t *testing.T) {
	_, _ = isolated(t)
	if _, e := invoke("config", "init", "--yes"); e != nil {
		t.Fatal(e)
	}
	if _, e := invoke("hosts", "add", "lab", "--ssh", "lab-alias", "--yes"); e != nil {
		t.Fatal(e)
	}
	if _, e := invoke("sources", "add", "jobs", "--host", "lab", "--path", "/srv/crontab", "--yes"); e != nil {
		t.Fatal(e)
	}
	out, e := invoke("sources", "list", "--host", "lab", "--json")
	if e != nil || !strings.Contains(out, "/srv/crontab") {
		t.Fatal(out, e)
	}
	if _, e = invoke("sources", "add", "bad", "--kind", "user", "--dialect", "supercronic", "--yes"); e == nil {
		t.Fatal("wrong user dialect accepted")
	}
}
func TestEnglishAndEditorArguments(t *testing.T) {
	if _, e := invoke("schedule", "build", "--when", "every monday at 08:30", "--json"); e != nil {
		t.Fatal(e)
	}
	args, e := splitArgs(`nvim --cmd 'set nowrap'`)
	if e != nil || len(args) != 3 || args[2] != "set nowrap" {
		t.Fatal(args, e)
	}
	args, e = splitArgs(`editor '$(touch ignored)'`)
	if e != nil || args[1] != "$(touch ignored)" {
		t.Fatal(args, e)
	}
}

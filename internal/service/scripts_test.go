package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

func TestStructuredScriptArgumentsAndEnvironmentSurviveCron(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script ' \\% literal.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\000' \"$PWD\" \"$SAMPLE\" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"", "a b", "literal $(not-executed)", "x\\%y", "a\\\\%b", "100%", "quote'\""}
	r := Recipe{Runner: "direct", Script: path, Directory: dir, ScriptTask: &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh", Args: args}, Environment: map[string]string{"SAMPLE": "literal\\% $HOME"}}
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		e := Entry{Job: document.Job{Environment: map[string]string{}}, Dialect: dialect}
		command, err := Compile(e, r)
		if err != nil {
			t.Fatal(err)
		}
		input := ""
		if dialect == schedule.System {
			command, input = SplitPercent(command)
		}
		if input != "" {
			t.Fatalf("argument became cron stdin: %q", input)
		}
		result, err := (transport.Native{}).Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", command}, nil)
		if err != nil {
			t.Fatalf("%s: %v %s\n%s", dialect, err, result.Stderr, command)
		}
		got := strings.Split(strings.TrimSuffix(string(result.Stdout), "\x00"), "\x00")
		want := append([]string{dir, "literal\\% $HOME"}, args...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s\ngot %#v\nwant %#v", dialect, got, want)
		}
	}
}

func TestLegacyRecipeAndScriptMetadataDoNotGenerateCommand(t *testing.T) {
	e := Entry{Job: document.Job{Command: "printf 'unchanged'  "}, Dialect: schedule.System}
	got, err := Compile(e, Recipe{Runner: "direct", Script: "/a/metadata-only.sh"})
	if err != nil || got != e.Command {
		t.Fatal(got, err)
	}
}

func TestUVPresetsPinProjectAndStandaloneMode(t *testing.T) {
	for _, preset := range []string{"uv-project", "uv-script"} {
		r := Recipe{Script: "/work/a.py", ScriptTask: &ScriptTask{Version: 1, Preset: preset, Runtime: "/tools/uv", Project: "/work", Args: []string{"--flag"}}}
		got, err := scriptCommand(r)
		if err != nil {
			t.Fatal(err)
		}
		if preset == "uv-project" && !strings.Contains(got, "'run' '--project' '/work' '/work/a.py'") {
			t.Fatal(got)
		}
		if preset == "uv-script" && !strings.Contains(got, "'run' '--no-project' '--script' '/work/a.py'") {
			t.Fatal(got)
		}
	}
}

func TestStructuredPueuePayloadPreservesArguments(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "payload")
	pueue := filepath.Join(dir, "pueue")
	fake := "#!/bin/sh\nfor arg do last=$arg; done\nprintf '%s' \"$last\" > " + transport.Quote(capture) + "\n"
	if err := os.WriteFile(pueue, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "job.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e := Entry{Job: document.Job{Metadata: document.Metadata{ID: "fixture"}}, Dialect: schedule.System}
	r := Recipe{Runner: "pueue", PueuePath: pueue, Script: script, Directory: dir, ScriptTask: &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh", Args: []string{"literal\\% $(not-run)"}}}
	compiled, err := Compile(e, r)
	if err != nil {
		t.Fatal(err)
	}
	command, input := SplitPercent(compiled)
	if input != "" {
		t.Fatal(input)
	}
	native := transport.Native{}
	if result, err := native.Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", command}, nil); err != nil {
		t.Fatal(result, err)
	}
	payload, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	result, err := native.Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", string(payload)}, nil)
	if err != nil || string(result.Stdout) != "literal\\% $(not-run)" {
		t.Fatal(result, err, string(payload))
	}
}

func TestScriptDiscoveryAndChecksNeverExecuteRuntime(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "SHOULD_NOT_EXIST")
	script := filepath.Join(dir, "job.py")
	if err := os.WriteFile(script, []byte("# /// script\n# dependencies = []\n# ///\nraise Exception('must not execute')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='fixture'\nversion='1'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, ".venv", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(bin, "python")
	if err := os.WriteFile(runtime, []byte("#!/bin/sh\ntouch "+transport.Quote(marker)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := New(config.Defaults())
	d, err := s.DiscoverScript(context.Background(), "local", script)
	if err != nil {
		t.Fatal(err)
	}
	if !d.InlineMetadata || len(d.Projects) == 0 || d.Projects[0].Path != dir || len(d.Runtimes) == 0 || d.Runtimes[0].Path != runtime {
		t.Fatalf("%+v", d)
	}
	r := Recipe{Script: script, ScriptTask: &ScriptTask{Version: 1, Preset: "python"}}
	e := Entry{Host: "local", Source: "user", Dialect: schedule.System}
	r, err = s.ResolveScriptRecipe(context.Background(), e, r)
	if err != nil {
		t.Fatal(err)
	}
	if r.Directory != dir || r.ScriptTask.Runtime != runtime {
		t.Fatal(r)
	}
	report := s.CheckRecipe(context.Background(), e, r)
	for _, f := range report.Findings {
		if f.Severity == "warning" || f.Severity == "unknown" {
			t.Fatalf("%+v", report)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preflight executed the interpreter")
	}
	r.Script = filepath.Join(dir, "missing")
	report = s.CheckRecipe(context.Background(), e, r)
	found := false
	for _, f := range report.Findings {
		if f.Code == "path-unavailable" && f.Field == "script" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing script did not produce finding: %+v", report)
	}
}

type remoteBaseRunner struct{ calls [][]string }

func (r *remoteBaseRunner) Run(_ context.Context, _ config.Host, argv []string, _ []byte) (transport.Result, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	return transport.Result{Stdout: []byte("/remote/home")}, nil
}
func TestRemoteRelativePathsNeverUseLocalCWD(t *testing.T) {
	c := config.Defaults()
	c.Hosts = append(c.Hosts, config.Host{ID: "server", SSH: "server"})
	s := New(c)
	s.Runner = &remoteBaseRunner{}
	e := Entry{Host: "server"}
	r, err := s.ResolveScriptRecipe(context.Background(), e, Recipe{Script: "job.sh", Directory: "projects/app", Output: "logs/result.log", ScriptTask: &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Script != "/remote/home/projects/app/job.sh" || r.Directory != "/remote/home/projects/app" || r.Output != "/remote/home/projects/app/logs/result.log" {
		t.Fatalf("%+v", r)
	}
}

func TestStructuredTildeUsesAccountHomeWithoutChangingRuntimeHOME(t *testing.T) {
	c := config.Defaults()
	c.Hosts = append(c.Hosts, config.Host{ID: "server", SSH: "server"})
	s := New(c)
	s.Runner = &remoteBaseRunner{}
	e := Entry{Host: "server", Dialect: schedule.System, Job: document.Job{Command: "echo old", Environment: map[string]string{"HOME": "/cron/runtime-home"}}}
	r, err := s.ResolveScriptRecipe(context.Background(), e, Recipe{Script: "~/job.sh", ScriptTask: &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Script != "/remote/home/job.sh" || r.Directory != "/remote/home" {
		t.Fatalf("%+v", r)
	}
	if e.Environment["HOME"] != "/cron/runtime-home" {
		t.Fatal("helper changed runtime HOME")
	}
	legacy, err := Compile(e, Recipe{Runner: "direct", Directory: "~/legacy", Original: "echo legacy"})
	if err != nil || !strings.Contains(legacy, "\"$HOME\"/") {
		t.Fatal("legacy runtime expansion changed", legacy, err)
	}
}
func TestInlineMetadataRequiresValidCompleteBlock(t *testing.T) {
	for _, valid := range []string{"# /// script\n# dependencies = []\n# ///\n", "# /// script\r\n# dependencies = ['rich']\r\n# ///\r\n"} {
		if !hasInlineMetadata(valid) {
			t.Fatal(valid)
		}
	}
	for _, invalid := range []string{"# /// script\n# dependencies = [\n# ///\n", "# /// script\n# dependencies=[]\n", "# /// script\n# requires-python='>=3.12'\n# ///\n", "# /// script\n# dependencies=[12]\n# ///\n"} {
		if hasInlineMetadata(invalid) {
			t.Fatal(invalid)
		}
	}
}

func TestEnvironmentAssignmentsAreLiteralAndValidated(t *testing.T) {
	got, err := ParseEnvironment([]string{"A=one two", "EMPTY=", "LITERAL=$(touch never)", "A=last"})
	if err != nil || got["A"] != "last" || got["LITERAL"] != "$(touch never)" {
		t.Fatal(got, err)
	}
	for _, value := range []string{"MISSING", "-BAD=value", "A=new\nline", "=empty"} {
		if _, err := ParseEnvironment([]string{value}); err == nil {
			t.Fatal(value)
		}
	}
}

package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

// The fake client records the actual argv produced after native cron's percent
// processing and shell parsing. It never connects to or submits a real queue.
func capturePueue(t *testing.T, e Entry, recipe Recipe) (args []string, payload, directory string) {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "arguments")
	fake := filepath.Join(dir, "pueue")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\" > "+transport.Quote(capture)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	recipe.Runner, recipe.PueuePath = "pueue", fake
	compiled, err := Compile(e, recipe)
	if err != nil {
		t.Fatal(err)
	}
	code, input := compiled, ""
	if e.Dialect == schedule.System {
		code, input = SplitPercent(compiled)
	}
	if input != "" {
		t.Fatalf("generated Pueue arguments leaked into cron stdin: %q", input)
	}
	result, err := (transport.Native{}).Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", code}, nil)
	if err != nil {
		t.Fatal(err, result.Stderr, compiled)
	}
	content, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	args = strings.Split(strings.TrimSuffix(string(content), "\x00"), "\x00")
	for i, arg := range args {
		if arg == "--working-directory" && i+1 < len(args) {
			directory = args[i+1]
		}
		if arg == "--" {
			if i+2 != len(args) {
				t.Fatalf("payload is not one exact command argument: %#v", args)
			}
			payload = args[i+1]
		}
	}
	if payload == "" {
		t.Fatalf("no queued command captured: %#v", args)
	}
	return args, payload, directory
}

func executeQueuedFixture(t *testing.T, shell, payload, directory string, environment ...string) string {
	t.Helper()
	cmd := exec.Command(shell, "-c", payload)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), environment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("queued fixture failed: %v\n%s\n%s", err, output, payload)
	}
	return string(output)
}

func TestPueuePlainCommandUsesExactPayloadAndWorkingDirectory(t *testing.T) {
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		t.Run(string(dialect), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "work ' \\% space")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			e := Entry{Dialect: dialect, Job: document.Job{Metadata: document.Metadata{ID: "fixture"}, Command: `echo "hi"`}}
			args, payload, cwd := capturePueue(t, e, Recipe{Directory: directory, Group: "group ' quote"})
			if payload != e.Command || cwd != directory {
				t.Fatalf("unnecessary command wrapping: payload=%q cwd=%q argv=%#v", payload, cwd, args)
			}
			if output := executeQueuedFixture(t, "/bin/sh", payload, cwd); output != "hi\n" {
				t.Fatal(output)
			}
			if strings.Count(strings.Join(args, "\x00"), directory) != 1 {
				t.Fatal("working directory is duplicated in payload", args)
			}
			_, payload, cwd = capturePueue(t, e, Recipe{})
			if payload != e.Command || cwd != "" {
				t.Fatal("unspecified directory must retain Pueue's client cwd", payload, cwd)
			}
		})
	}
}

func TestPueueConfiguredShellAndExplicitCronShell(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("bash is required to distinguish the queue's shell from explicit sh")
	}
	e := Entry{Dialect: schedule.System, Job: document.Job{Command: `printf '\%s' "$0"`}}
	_, payload, cwd := capturePueue(t, e, Recipe{Directory: t.TempDir()})
	if payload != `printf '%s' "$0"` {
		t.Fatal("plain payload did not preserve native percent decoding", payload)
	}
	if output := executeQueuedFixture(t, "/bin/bash", payload, cwd); output != "/bin/bash" {
		t.Fatal("default queued command did not use configured queue shell", output)
	}
	e.Environment = map[string]string{"SHELL": "/bin/sh"}
	_, payload, cwd = capturePueue(t, e, Recipe{Directory: t.TempDir()})
	if !strings.Contains(payload, "'/bin/sh' '-c'") || strings.HasPrefix(payload, "cd ") {
		t.Fatal("explicit shell was dropped or cwd duplicated", payload)
	}
	if output := executeQueuedFixture(t, "/bin/bash", payload, cwd); output != "/bin/sh" {
		t.Fatal("queue shell overrode explicit cron shell", output)
	}
}

func TestPueueEnvironmentStdinAndRedirectScopesSurviveSimplification(t *testing.T) {
	t.Run("per-job environment covers every command", func(t *testing.T) {
		e := Entry{Dialect: schedule.System, Job: document.Job{Command: `printf '\%s\n' "$SAMPLE"; printf '\%s\n' "$SAMPLE"`}}
		value := "literal $HOME ' \\% value"
		_, payload, cwd := capturePueue(t, e, Recipe{Directory: t.TempDir(), Environment: map[string]string{"SAMPLE": value}})
		if strings.HasPrefix(payload, "cd ") || !strings.Contains(payload, "'-c'") {
			t.Fatal("environment scope wrapper missing or cwd duplicated", payload)
		}
		if output := executeQueuedFixture(t, "/bin/sh", payload, cwd, "SAMPLE=ambient"); output != value+"\n"+value+"\n" {
			t.Fatal("per-job literal environment changed", output)
		}
	})
	t.Run("native percent stdin remains inside queued payload", func(t *testing.T) {
		e := Entry{Dialect: schedule.System, Job: document.Job{Command: "cat%hello%quoted '$HOME'"}}
		_, payload, cwd := capturePueue(t, e, Recipe{Directory: t.TempDir()})
		if !strings.Contains(payload, " | ") || strings.HasPrefix(payload, "cd ") {
			t.Fatal(payload)
		}
		if output := executeQueuedFixture(t, "/bin/sh", payload, cwd); output != "hello\nquoted '$HOME'\n" {
			t.Fatal("cron stdin changed", output)
		}
	})
	t.Run("redirect keeps trailing comments inside shell string", func(t *testing.T) {
		dir := t.TempDir()
		output := filepath.Join(dir, "out ' \\%.log")
		e := Entry{Dialect: schedule.System, Job: document.Job{Command: `echo output; echo error >&2 # trailing comment`}}
		_, payload, cwd := capturePueue(t, e, Recipe{Directory: dir, Output: output})
		if strings.Contains(payload, "cd ") {
			t.Fatal("cwd duplicated", payload)
		}
		if output := executeQueuedFixture(t, "/bin/sh", payload, cwd); output != "" {
			t.Fatal("redirected output leaked", output)
		}
		content, err := os.ReadFile(output)
		if err != nil || string(content) != "output\nerror\n" {
			t.Fatal(string(content), err)
		}
	})
	t.Run("tilde directory retains target HOME expansion", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, "work")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		e := Entry{Dialect: schedule.System, Job: document.Job{Command: `pwd`}}
		_, payload, cwd := capturePueue(t, e, Recipe{Directory: "~/work"})
		if cwd != "" || !strings.HasPrefix(payload, `cd "$HOME"/`) {
			t.Fatal("tilde path lost its deferred expansion", payload, cwd)
		}
		if got := executeQueuedFixture(t, "/bin/sh", payload, cwd, "HOME="+home); strings.TrimSpace(got) != dir {
			t.Fatal(got, dir)
		}
	})
}

func TestPueueStructuredScriptKeepsRuntimeAndArgumentsWithoutExtraShell(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "script ' \\%.sh")
	if err := os.WriteFile(script, []byte("printf '%s\\n' \"$PWD\" \"$1\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value := "literal ' $(not-run) \\%"
	e := Entry{Dialect: schedule.System, Job: document.Job{Metadata: document.Metadata{ID: "script"}}}
	recipe := Recipe{Directory: dir, Script: script, ScriptTask: &ScriptTask{Version: 1, Preset: "shell", Runtime: "/bin/sh", Args: []string{value}}}
	_, payload, cwd := capturePueue(t, e, recipe)
	if strings.Contains(payload, "'-c'") || strings.HasPrefix(payload, "cd ") {
		t.Fatal("structured script has extra shell/cwd wrappers", payload)
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if output := executeQueuedFixture(t, "/bin/sh", payload, cwd); output != physical+"\n"+value+"\n" {
		t.Fatal("script runtime or arguments changed", output)
	}
}

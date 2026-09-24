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

func executeCompiledFixture(t *testing.T, dialect schedule.Dialect, command string) (transport.Result, error) {
	t.Helper()
	input := ""
	if dialect == schedule.System {
		command, input = SplitPercent(command)
	}
	return (transport.Native{}).Run(context.Background(), config.Host{ID: "local"}, []string{"/bin/sh", "-c", command}, []byte(input))
}

func TestOutputPoliciesPreserveLegacyMapping(t *testing.T) {
	for _, tc := range []struct {
		recipe Recipe
		want   string
	}{
		{Recipe{}, OutputInherit},
		{Recipe{Output: "/tmp/out"}, OutputFiles},
		{Recipe{Stderr: "/tmp/err"}, OutputFiles},
		{Recipe{OutputPolicy: OutputInherit, Output: "inactive"}, OutputInherit},
		{Recipe{OutputPolicy: OutputStderrOnly}, OutputStderrOnly},
		{Recipe{OutputPolicy: OutputDiscard}, OutputDiscard},
	} {
		if got := EffectiveOutputPolicy(tc.recipe); got != tc.want {
			t.Fatal(tc.recipe, got, tc.want)
		}
	}
	if EffectiveEnqueueOutput(Recipe{Runner: "pueue"}) != EnqueueInherit {
		t.Fatal("legacy Pueue output changed silently")
	}
	for _, recipe := range []Recipe{
		{OutputPolicy: "invalid"}, {OutputPolicy: OutputFiles}, {EnqueueOutput: "invalid"},
	} {
		if _, err := Compile(Entry{Job: document.Job{Command: "echo harmless"}}, recipe); err == nil {
			t.Fatal("invalid policy accepted", recipe)
		}
	}
}

func TestTaskOutputPoliciesPreserveStreamsStatusAndComments(t *testing.T) {
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		for _, mode := range []string{OutputInherit, OutputStderrOnly, OutputDiscard, "merged", "split", "stderr-file"} {
			t.Run(string(dialect)+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				outPath, errPath := filepath.Join(dir, "out ' \\%.log"), filepath.Join(dir, "err ' \\%.log")
				for _, path := range []string{outPath, errPath} {
					if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				raw := `printf '%s\n' 'out% literal'; printf '%s\n' 'err% literal' >&2; exit 7 # trailing comment`
				if dialect == schedule.System {
					raw = cronEscape(raw)
				}
				e := Entry{Dialect: dialect, Job: document.Job{Command: raw}}
				r := Recipe{Runner: "direct", OutputPolicy: mode}
				wantOut, wantErr := "out% literal\n", "err% literal\n"
				wantOutFile, wantErrFile := "before\n", "before\n"
				switch mode {
				case OutputInherit:
					// Inactive paths must not be validated, compiled or written.
					r.Output, r.Stderr = "inactive\x00out", "inactive\x00err"
				case OutputStderrOnly:
					wantOut = ""
				case OutputDiscard:
					wantOut, wantErr = "", ""
				case "merged":
					r.OutputPolicy, r.Output = OutputFiles, outPath
					wantOut, wantErr, wantOutFile = "", "", "before\nout% literal\nerr% literal\n"
				case "split":
					r.OutputPolicy, r.Output, r.Stderr = OutputFiles, outPath, errPath
					wantOut, wantErr = "", ""
					wantOutFile, wantErrFile = "before\nout% literal\n", "before\nerr% literal\n"
				case "stderr-file":
					// No policy field is the legacy stderr-path-only recipe.
					r.OutputPolicy, r.Stderr = "", errPath
					wantErr, wantErrFile = "", "before\nerr% literal\n"
				}
				command, err := Compile(e, r)
				if err != nil {
					t.Fatal(err)
				}
				if mode == OutputInherit && command != raw {
					t.Fatal("inherit changed the original direct command", command)
				}
				result, err := executeCompiledFixture(t, dialect, command)
				if err == nil || result.Code != 7 || string(result.Stdout) != wantOut || result.Stderr != wantErr {
					t.Fatalf("status or streams changed: %+v %v\n%s", result, err, command)
				}
				for path, want := range map[string]string{outPath: wantOutFile, errPath: wantErrFile} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("%s = %q, want %q (%v)", path, got, want, err)
					}
				}
			})
		}
	}
}

func TestNativeCronStdinWithOutputPolicyRemainsOnePhysicalLine(t *testing.T) {
	raw := `cat; printf 'diagnostic\n' >&2 # comment%literal \c \n '$HOME' \%done%second`
	want := "literal \\c \\n '$HOME' %done\nsecond\n"
	for _, mode := range []string{OutputInherit, OutputFiles, OutputStderrOnly, OutputDiscard} {
		t.Run(mode, func(t *testing.T) {
			e := Entry{Dialect: schedule.System, Job: document.Job{Command: raw}}
			r := Recipe{OutputPolicy: mode}
			if mode == OutputFiles {
				r.Output, r.Stderr = filepath.Join(t.TempDir(), "stdout"), filepath.Join(t.TempDir(), "stderr")
			}
			command, err := Compile(e, r)
			if err != nil || strings.ContainsAny(command, "\r\n") {
				t.Fatal("generated command is not one native cron line", command, err)
			}
			doc := document.Parse("* * * * * "+command+"\n", schedule.System, false)
			if len(doc.Jobs) != 1 || doc.Jobs[0].Command != command {
				t.Fatal("compiled stdin command cannot round-trip through native cron", doc)
			}
			result, err := executeCompiledFixture(t, schedule.System, command)
			if err != nil || result.Code != 0 {
				t.Fatal(result, err)
			}
			switch mode {
			case OutputInherit:
				if string(result.Stdout) != want || result.Stderr != "diagnostic\n" {
					t.Fatal("cron stdin changed", result)
				}
			case OutputFiles:
				got, readErr := os.ReadFile(r.Output)
				if readErr != nil || string(got) != want || len(result.Stdout) != 0 || result.Stderr != "" {
					t.Fatal("redirected cron stdin changed", string(got), result, readErr)
				}
			case OutputStderrOnly:
				if len(result.Stdout) != 0 || result.Stderr != "diagnostic\n" {
					t.Fatal(result)
				}
			case OutputDiscard:
				if len(result.Stdout) != 0 || result.Stderr != "" {
					t.Fatal(result)
				}
			}
		})
	}
}

func TestPueueSubmissionAndTaskOutputHaveSeparateScopes(t *testing.T) {
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		for _, exit := range []string{"0", "17"} {
			t.Run(string(dialect)+"/exit"+exit, func(t *testing.T) {
				fake := filepath.Join(t.TempDir(), "pueue ' \\%")
				body := "#!/bin/sh\nprintf '140\\n'\nprintf 'enqueue diagnostic\\n' >&2\nexit " + exit + "\n"
				if err := os.WriteFile(fake, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
				e := Entry{Dialect: dialect, Job: document.Job{Metadata: document.Metadata{ID: "task"}, Command: `echo payload; echo payload-error >&2 # comment`}}
				r := Recipe{Runner: "pueue", PueuePath: fake, OutputPolicy: OutputDiscard, EnqueueOutput: EnqueueQuiet}
				scheduled, compiled, err := CompileRecipe(e, r)
				if err != nil || compiled.PueueSubmission == nil || compiled.CommandDigest != document.Digest(scheduled) {
					t.Fatal(compiled, err)
				}
				manual, err := CompileManual(e, r)
				if err != nil || manual != compiled.PueueSubmission.Command {
					t.Fatal(manual, err)
				}
				for _, run := range []struct{ command, stdout string }{{scheduled, ""}, {manual, "140\n"}} {
					result, err := executeCompiledFixture(t, dialect, run.command)
					if string(result.Stdout) != run.stdout || result.Stderr != "enqueue diagnostic\n" || (err != nil) != (exit != "0") || (exit == "17" && result.Code != 17) {
						t.Fatal("enqueue output or status altered", result, err, run.command)
					}
				}
			})
		}
	}
	for _, policy := range []string{OutputInherit, OutputStderrOnly, OutputDiscard} {
		t.Run("payload/"+policy, func(t *testing.T) {
			e := Entry{Dialect: schedule.System, Job: document.Job{Command: "printf 'payload\\n'; printf 'payload-error\\n' >&2 # comment"}}
			_, payload, _ := capturePueue(t, e, Recipe{OutputPolicy: policy, EnqueueOutput: EnqueueQuiet})
			result, err := executeCompiledFixture(t, schedule.Supercronic, payload)
			if err != nil {
				t.Fatal(result, err)
			}
			wantOut, wantErr := "payload\n", "payload-error\n"
			if policy != OutputInherit {
				wantOut = ""
			}
			if policy == OutputDiscard {
				wantErr = ""
			}
			if string(result.Stdout) != wantOut || result.Stderr != wantErr {
				t.Fatal("task policy was applied outside the queued payload", result, payload)
			}
		})
	}
}

func TestEnqueueOutputMigrationPreservesFrozenCommand(t *testing.T) {
	for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
		t.Run(string(dialect), func(t *testing.T) {
			e := Entry{Dialect: dialect, Job: document.Job{Metadata: document.Metadata{ID: "migration"}, Command: "printf 'literal\\%s' hi", Environment: map[string]string{"SHELL": "/old/shell"}}}
			r := Recipe{Runner: "pueue", PueuePath: "/old/pueue ' \\%", Original: e.Command, Directory: "/old/work", Output: "/old/output", Stderr: "/old/error"}
			original, compiled, err := CompileRecipe(e, r)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a v0.1.0 sidecar. The source SHELL and current recipe
			// reconstruction inputs must not affect an output-only migration.
			compiled.PueueSubmission = nil
			e.Command = original
			e.Environment["SHELL"] = "/new/unavailable-shell"
			quiet, migrated, err := UpdateEnqueueOutput(e, compiled, EnqueueQuiet)
			if err != nil || quiet != original+" > /dev/null" || migrated.PueueSubmission == nil || migrated.PueueSubmission.Command != original {
				t.Fatal(quiet, migrated, err)
			}
			if migrated.PueuePath != r.PueuePath || migrated.Original != r.Original || migrated.Output != r.Output || migrated.Stderr != r.Stderr {
				t.Fatal("migration rebound the execution recipe", migrated)
			}
			e.Command = quiet
			manual, err := storedManualSubmission(e, migrated)
			if err != nil || manual != original {
				t.Fatal("manual run rebuilt instead of revealing frozen submission", manual, err)
			}
			inherit, restored, err := UpdateEnqueueOutput(e, migrated, EnqueueInherit)
			if err != nil || inherit != original || restored.PueueSubmission.Command != original {
				t.Fatal("migration is not reversible without recompilation", inherit, restored, err)
			}
			e.Command = inherit
			unchanged, same, err := UpdateEnqueueOutput(e, restored, EnqueueInherit)
			if err != nil || unchanged != inherit || !reflect.DeepEqual(same, restored) {
				t.Fatal("unchanged policy rewrote metadata", same, err)
			}
		})
	}
}

func TestFrozenSubmissionRejectsUnboundOrChangedMetadata(t *testing.T) {
	e := Entry{Dialect: schedule.System, Job: document.Job{Metadata: document.Metadata{ID: "bound"}, Command: "echo task"}}
	command, original, err := CompileRecipe(e, Recipe{Runner: "pueue", PueuePath: "/pueue", EnqueueOutput: EnqueueQuiet})
	if err != nil {
		t.Fatal(err)
	}
	e.Command = command
	for _, name := range []string{"missing-digest", "stale-digest", "missing-frozen", "version", "dialect", "command", "policy"} {
		t.Run(name, func(t *testing.T) {
			r := original
			frozen := *r.PueueSubmission
			r.PueueSubmission = &frozen
			switch name {
			case "missing-digest":
				r.CommandDigest = ""
			case "stale-digest":
				r.CommandDigest = "stale"
			case "missing-frozen":
				r.PueueSubmission = nil
			case "version":
				r.PueueSubmission.Version++
			case "dialect":
				r.PueueSubmission.Dialect = schedule.Supercronic
			case "command":
				r.PueueSubmission.Command = "echo unrelated"
			case "policy":
				r.EnqueueOutput = EnqueueInherit
			}
			if _, err := storedManualSubmission(e, r); err == nil {
				t.Fatal("untrusted submission bypassed output suppression", r)
			}
			if _, _, err := UpdateEnqueueOutput(e, r, EnqueueInherit); err == nil {
				t.Fatal("untrusted submission migrated", r)
			}
		})
	}
	// A legacy redirect belongs to the native source. It is never removed by
	// recognizing a suffix or looking for the word "pueue" in arbitrary text.
	legacy := Recipe{Runner: "pueue", CommandDigest: document.Digest(command)}
	if got, err := storedManualSubmission(e, legacy); err != nil || got != command {
		t.Fatal("legacy user redirect was removed", got, err)
	}
	if _, _, err := UpdateEnqueueOutput(e, legacy, EnqueueQuiet); err == nil {
		t.Fatal("unrecognized legacy command was migrated")
	}
}

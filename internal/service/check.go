package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

type Finding struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Field      string `json:"field,omitempty"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}
type CheckReport struct {
	Host              string    `json:"host"`
	Source            string    `json:"source"`
	JobID             string    `json:"job_id"`
	Directory         string    `json:"directory,omitempty"`
	EnvironmentSource string    `json:"environment_source"`
	Findings          []Finding `json:"findings"`
}

func (r CheckReport) Text() string {
	lines := []string{"Execution checks · " + r.Host + " / " + r.Source, r.EnvironmentSource}
	if r.Directory != "" {
		lines = append(lines, "Working directory: "+r.Directory)
	}
	for _, f := range r.Findings {
		line := f.Severity + " · " + f.Message
		if f.Suggestion != "" {
			line += "\n  " + f.Suggestion
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// CheckRecipe reports observed file/runtime facts without running the job or
// loading startup files. Failed/unavailable checks remain distinct from passed.
func (s *Service) CheckRecipe(ctx context.Context, e Entry, r Recipe) CheckReport {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	report := CheckReport{Host: e.Host, Source: e.Source, JobID: e.ID, Directory: r.Directory, Findings: []Finding{}, EnvironmentSource: "Cron defaults plus crontab variables; interactive shell startup files are not loaded."}
	if e.Dialect == schedule.Supercronic {
		report.EnvironmentSource = "Supercronic uses its process environment; the running daemon/container environment is not available to these checks."
	}
	add := func(code, severity, field, message, suggestion string) {
		report.Findings = append(report.Findings, Finding{code, severity, field, message, suggestion})
	}
	h, base, home, err := s.targetBase(ctx, e.Host)
	if err != nil {
		add("target-unavailable", "unknown", "host", err.Error(), "Retry when the target is reachable.")
		return report
	}
	if value := e.Environment["HOME"]; filepath.IsAbs(value) {
		home = value
	}
	if r.Directory != "" {
		base, err = resolveTargetPath(r.Directory, base, home)
		if err != nil {
			add("invalid-directory", "error", "directory", err.Error(), "")
			return report
		}
	} else {
		base = home
	}
	report.Directory = base
	type pathCheck struct{ field, path, kind string }
	paths := []pathCheck{{"directory", base, "directory"}}
	if r.Script != "" {
		p, _ := resolveTargetPath(r.Script, base, home)
		kind := "readable"
		if r.ScriptTask != nil && r.ScriptTask.Preset == "executable" {
			kind = "executable"
		}
		paths = append(paths, pathCheck{"script", p, kind})
	}
	if r.ScriptTask != nil && r.ScriptTask.Runtime != "" {
		paths = append(paths, pathCheck{"runtime", r.ScriptTask.Runtime, "executable"})
	}
	if r.ScriptTask != nil && r.ScriptTask.Preset == "uv-project" {
		paths = append(paths, pathCheck{"project", filepath.Join(r.ScriptTask.Project, "pyproject.toml"), "readable"})
	}
	for _, pair := range []struct{ field, value string }{{"output", r.Output}, {"stderr", r.Stderr}} {
		if pair.value != "" {
			p, _ := resolveTargetPath(pair.value, base, home)
			paths = append(paths, pathCheck{pair.field, p, "output"})
		}
	}
	args := []string{"sh", "-c", `set -eu; while [ "$#" -ge 3 ]; do field=$1; p=$2; kind=$3; shift 3; ok=no; case "$kind" in directory) [ -d "$p" ] && [ -x "$p" ] && ok=yes;; readable) [ -f "$p" ] && [ -r "$p" ] && ok=yes;; executable) [ -f "$p" ] && [ -x "$p" ] && ok=yes;; output) if [ -e "$p" ]; then [ -f "$p" ] && [ -w "$p" ] && ok=yes; else d=$(dirname "$p"); [ -d "$d" ] && [ -w "$d" ] && ok=yes; fi;; esac; printf '%s\000%s\000' "$field" "$ok"; done`, "lazycrontab-check"}
	for _, p := range paths {
		args = append(args, p.field, p.path, p.kind)
	}
	result, err := s.Runner.Run(ctx, h, args, nil)
	if err != nil {
		add("checks-unavailable", "unknown", "", err.Error(), "No user code was run.")
		return report
	}
	parts := strings.Split(string(result.Stdout), "\x00")
	observed := map[string]string{}
	for i := 0; i+1 < len(parts); i += 2 {
		observed[parts[i]] = parts[i+1]
	}
	for _, p := range paths {
		switch observed[p.field] {
		case "yes":
			add("path-ready", "ok", p.field, p.field+": "+p.path, "")
		case "no":
			add("path-unavailable", "warning", p.field, p.field+" is not ready: "+p.path, "Choose an existing accessible path, adjust permissions, or save the job disabled until it is ready.")
		default:
			add("path-unknown", "unknown", p.field, "Could not inspect "+p.path, "Retry the check.")
		}
	}
	if r.ScriptTask == nil {
		add("raw-command", "info", "command", "Shell command dependencies are not inferred.", "Use a script preset to check its runtime and working directory.")
		return report
	}
	d, err := s.DiscoverScript(ctx, e.Host, r.Script)
	if err != nil {
		add("discovery-unavailable", "unknown", "script", err.Error(), "")
		return report
	}
	if r.ScriptTask.Preset == "executable" {
		if d.Shebang == "" {
			add("shebang-missing", "warning", "script", "No shebang found; direct execution may depend on shell fallback.", "Choose the Shell or Python preset for an explicit interpreter.")
		}
		if strings.HasPrefix(d.Shebang, "/usr/bin/env ") {
			add("shebang-path", "warning", "runtime", "The shebang resolves its interpreter through PATH: "+d.Shebang, "Choose an explicit interpreter preset if the program is only available in your terminal PATH.")
		}
	}
	if strings.HasPrefix(r.ScriptTask.Preset, "uv-") {
		add("uv-sync", "info", "runtime", "uv may synchronize dependencies or download Python when the scheduled job runs.", "These checks do not run uv or install anything.")
		if r.ScriptTask.Preset == "uv-project" && d.InlineMetadata {
			add("uv-inline-project", "warning", "project", "The script declares inline metadata; uv runs it independently of project dependencies.", "Choose uv standalone, or remove inline metadata yourself if the project environment is intended.")
		}
		if r.ScriptTask.Preset == "uv-script" && !d.InlineMetadata {
			add("uv-no-metadata", "info", "script", "No inline dependency metadata was detected.", "The standalone preset does not use surrounding project dependencies.")
		}
	}
	add("dependencies-unverified", "info", "runtime", "Library imports and external services have not been executed or verified.", "Use the separately reviewed Run action when you want to execute the job.")
	return report
}

func (s *Service) CheckJob(ctx context.Context, snap Snapshot, id string) (CheckReport, error) {
	j, err := snap.Document.Find(id)
	if err != nil {
		return CheckReport{}, err
	}
	e := Entry{Job: j, Host: snap.Host, Source: snap.Source, Dialect: snap.Document.Dialect}
	r, err := LoadRecipe(e)
	if err != nil {
		return CheckReport{}, fmt.Errorf("cannot inspect helper recipe: %w", err)
	}
	return s.CheckRecipe(ctx, e, r), nil
}

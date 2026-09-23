package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

type Recipe struct {
	CommandDigest string            `json:"command_digest,omitempty"`
	Original      string            `json:"original,omitempty"`
	Runner        string            `json:"runner,omitempty"`
	Group         string            `json:"group,omitempty"`
	PueuePath     string            `json:"pueue_path,omitempty"`
	Directory     string            `json:"directory,omitempty"`
	Output        string            `json:"output,omitempty"`
	Stderr        string            `json:"stderr,omitempty"`
	Script        string            `json:"script,omitempty"`
	Log           string            `json:"log,omitempty"`
	ScriptTask    *ScriptTask       `json:"script_task,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
}
type Capabilities struct {
	PueuePath    string   `json:"pueue_path,omitempty"`
	PueueVersion string   `json:"pueue_version,omitempty"`
	Groups       []string `json:"groups"`
	Ready        bool     `json:"ready"`
	Error        string   `json:"error,omitempty"`
}

func recipePath(e Entry) (string, error) {
	base, err := config.Base("data")
	return filepath.Join(base, "jobs", document.Digest(e.Key())+".json"), err
}
func LoadRecipe(e Entry) (Recipe, error) {
	var r Recipe
	p, err := recipePath(e)
	if err != nil {
		return r, err
	}
	err = ReadJSON(p, &r)
	if err == nil && r.CommandDigest != "" && r.CommandDigest != document.Digest(e.Command) {
		return Recipe{}, fmt.Errorf("helper metadata does not match the current command; edit raw command or explicitly rebuild the recipe")
	}
	return r, err
}
func SaveRecipe(e Entry, r Recipe) error {
	p, err := recipePath(e)
	if err != nil {
		return err
	}
	r.CommandDigest = document.Digest(e.Command)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWrite(p, data, 0600)
}

func (s *Service) Capabilities(ctx context.Context, host string) Capabilities {
	c := Capabilities{Groups: []string{}}
	h, e := s.Config.Host(host)
	if e != nil {
		c.Error = e.Error()
		return c
	}
	p := h.Pueue
	if p == "" {
		r, e := s.Runner.Run(ctx, h, []string{"sh", "-c", "command -v pueue"}, nil)
		if e != nil {
			c.Error = "Pueue is not installed on this host"
			return c
		}
		p = strings.TrimSpace(string(r.Stdout))
	}
	c.PueuePath = p
	r, e := s.Runner.Run(ctx, h, []string{p, "--version"}, nil)
	if e != nil {
		c.Error = e.Error()
		return c
	}
	c.PueueVersion = strings.TrimSpace(string(r.Stdout))
	if !strings.HasPrefix(c.PueueVersion, "pueue 4.") {
		c.Error = "Pueue integration requires version 4.x"
		return c
	}
	r, e = s.Runner.Run(ctx, h, []string{p, "group", "--json"}, nil)
	if e != nil {
		c.Error = "Pueue daemon unavailable: " + e.Error()
		return c
	}
	var groups map[string]json.RawMessage
	if e = json.Unmarshal(r.Stdout, &groups); e != nil {
		c.Error = "invalid group response: " + e.Error()
		return c
	}
	for g := range groups {
		c.Groups = append(c.Groups, g)
	}
	c.Ready = true
	return c
}

// SplitPercent reproduces cron's command/stdin separator before shell parsing.
func SplitPercent(raw string) (string, string) {
	var command, input strings.Builder
	dst := &command
	split := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\\' && i+1 < len(raw) {
			if raw[i+1] == '%' {
				dst.WriteByte('%')
				i++
				continue
			}
			dst.WriteByte(ch)
			dst.WriteByte(raw[i+1])
			i++
			continue
		}
		if ch == '%' {
			if !split {
				split = true
				dst = &input
			} else {
				dst.WriteByte('\n')
			}
			continue
		}
		dst.WriteByte(ch)
	}
	if split {
		input.WriteByte('\n')
	}
	return command.String(), input.String()
}
func cronEscape(s string) string { return strings.ReplaceAll(s, "%", "\\%") }
func Compile(e Entry, r Recipe) (string, error) {
	for name, path := range map[string]string{"directory": r.Directory, "output": r.Output, "stderr": r.Stderr, "script": r.Script, "log": r.Log} {
		if path != "" && (!filepath.IsAbs(path) && !strings.HasPrefix(path, "~/") || strings.ContainsAny(path, "\r\n\x00")) {
			return "", fmt.Errorf("%s must be an absolute target path or start with ~/", name)
		}
	}
	if r.Runner == "" {
		r.Runner = "direct"
	}
	if r.Runner != "direct" && r.Runner != "pueue" {
		return "", fmt.Errorf("runner must be direct or pueue")
	}
	raw := r.Original
	if raw == "" {
		raw = e.Command
	}
	if r.ScriptTask != nil {
		var err error
		raw, err = scriptCommand(r)
		if err != nil {
			return "", err
		}
		// Structured values are shell arguments, never cron's percent/stdin
		// syntax. Encode here so the existing wrapper can decode/re-encode once.
		if e.Dialect == schedule.System {
			raw = cronEscape(raw)
		}
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("use a script for multiline commands")
	}
	if r.Runner == "direct" && r.Directory == "" && r.Output == "" && r.Stderr == "" && len(r.Environment) == 0 {
		return raw, nil
	}
	code, input := raw, ""
	if e.Dialect == schedule.System {
		code, input = SplitPercent(raw)
	}
	shell := e.Environment["SHELL"]
	if shell == "" {
		shell = "/bin/sh"
	}
	payload := commandJoin([]string{shell, "-c", code})
	if len(r.Environment) > 0 {
		args := []string{"env"}
		for _, key := range sortedKeys(r.Environment) {
			if !environmentName.MatchString(key) || strings.ContainsAny(r.Environment[key], "\r\n\x00") {
				return "", fmt.Errorf("invalid environment assignment %q", key)
			}
			args = append(args, key+"="+r.Environment[key])
		}
		payload = commandJoin(args) + " " + payload
	}
	if input != "" {
		payload = "printf '%s' " + commandQuote(input) + " | " + payload
	}
	if r.Directory != "" {
		payload = "cd " + commandPath(r.Directory) + " && " + payload
	}
	if r.Output != "" || r.Stderr != "" {
		payload = "( " + payload + " )"
		if r.Output != "" {
			payload += " >> " + commandPath(r.Output)
		}
		if r.Stderr != "" {
			payload += " 2>> " + commandPath(r.Stderr)
		} else if r.Output != "" {
			payload += " 2>&1"
		}
	}
	if r.Runner == "pueue" {
		if r.PueuePath == "" {
			return "", fmt.Errorf("select an available Pueue executable first")
		}
		args := []string{r.PueuePath, "add", "--print-task-id", "--label", "lazycrontab:" + e.ID}
		if r.Group != "" {
			args = append(args, "--group", r.Group)
		}
		if r.Directory != "" && !strings.HasPrefix(r.Directory, "~") {
			args = append(args, "--working-directory", r.Directory)
		}
		args = append(args, "--", payload)
		payload = commandJoin(args)
	}
	if e.Dialect == schedule.System {
		payload = cronEscape(payload)
	}
	return payload, nil
}

type ExecutionPlan struct {
	InheritEnvironment bool              `json:"inherit_environment"`
	User               string            `json:"user"`
	Host               string            `json:"host"`
	Source             string            `json:"source"`
	JobID              string            `json:"job_id"`
	Command            string            `json:"command"`
	Shell              string            `json:"shell"`
	Directory          string            `json:"directory"`
	Environment        map[string]string `json:"environment"`
	Stdin              string            `json:"stdin,omitempty"`
	Runner             string            `json:"runner"`
	Revision           string            `json:"revision"`
	Warning            string            `json:"warning,omitempty"`
}
type RunRecord struct {
	OutputTruncated bool      `json:"output_truncated,omitempty"`
	ID              string    `json:"id"`
	Host            string    `json:"host"`
	Source          string    `json:"source"`
	JobID           string    `json:"job_id"`
	Started         time.Time `json:"started"`
	Finished        time.Time `json:"finished"`
	ExitCode        int       `json:"exit_code"`
	Status          string    `json:"status"`
	Output          string    `json:"output"`
	Stderr          string    `json:"stderr"`
	TaskID          string    `json:"task_id,omitempty"`
	Error           string    `json:"error,omitempty"`
}

func (s *Service) RunPlan(ctx context.Context, snap Snapshot, id string, r *Recipe) (ExecutionPlan, error) {
	j, e := snap.Document.Find(id)
	if e != nil {
		return ExecutionPlan{}, e
	}
	h, src, e := s.source(snap.Host, snap.Source)
	if e != nil {
		return ExecutionPlan{}, e
	}
	if snap.Document.SystemFile || src.ReadOnly {
		return ExecutionPlan{}, fmt.Errorf("system sources are read-only")
	}
	entry := Entry{Job: j, Host: snap.Host, Source: snap.Source, Dialect: snap.Document.Dialect}
	command := j.Command
	runner := "direct"
	if r == nil {
		// The source command stays authoritative for an ordinary Run. A valid
		// sidecar may identify queue submission, but must not rebuild wrappers
		// after an external environment change (for example SHELL).
		if stored, err := LoadRecipe(entry); err == nil && stored.Runner == "pueue" {
			runner = "pueue"
		}
	}
	if r != nil {
		if r.Runner == "pueue" {
			caps := s.Capabilities(ctx, snap.Host)
			if !caps.Ready {
				return ExecutionPlan{}, fmt.Errorf("Pueue unavailable: %s", caps.Error)
			}
			found := r.Group == ""
			for _, g := range caps.Groups {
				if g == r.Group {
					found = true
				}
			}
			if !found {
				return ExecutionPlan{}, fmt.Errorf("Pueue group %q no longer exists", r.Group)
			}
			r.PueuePath = caps.PueuePath
		}
		command, e = Compile(entry, *r)
		if e != nil {
			return ExecutionPlan{}, e
		}
		runner = r.Runner
	}
	facts, e := s.Runner.Run(ctx, h, []string{"sh", "-c", `printf '%s\n' "$HOME"; id -un`}, nil)
	if e != nil {
		return ExecutionPlan{}, e
	}
	parts := strings.Split(strings.TrimSpace(string(facts.Stdout)), "\n")
	if len(parts) < 2 {
		return ExecutionPlan{}, fmt.Errorf("cannot determine target HOME and user")
	}
	env := map[string]string{"HOME": parts[0], "LOGNAME": parts[1], "USER": parts[1], "SHELL": "/bin/sh", "PATH": "/usr/bin:/bin"}
	for k, v := range j.Environment {
		if k != "LOGNAME" && k != "USER" {
			env[k] = v
		}
	}
	p := ExecutionPlan{Host: snap.Host, Source: snap.Source, JobID: id, Command: command, Shell: env["SHELL"], Directory: env["HOME"], Environment: env, Runner: runner, Revision: snap.Document.Revision, User: parts[1]}
	if entry.Dialect == schedule.System {
		p.Command, p.Stdin = SplitPercent(command)
		p.Warning = "Cron-like environment; daemon-specific PAM, limits and runtime conditions may differ."
	} else {
		p.InheritEnvironment = true
		p.Environment = map[string]string{}
		for k, v := range j.Environment {
			p.Environment[k] = v
		}
		p.Warning = "Runs on the selected host. An existing Supercronic/container process environment is not available."
	}
	return p, nil
}
func (s *Service) Run(ctx context.Context, p ExecutionPlan) (RunRecord, error) {
	record := RunRecord{ID: s.Now().UTC().Format("20060102T150405.000000000Z") + "-" + document.NewID()[:6], Host: p.Host, Source: p.Source, JobID: p.JobID, Started: s.Now()}
	h, src, e := s.source(p.Host, p.Source)
	if e != nil {
		return record, e
	}
	raw, _, e := s.read(ctx, h, src)
	if e != nil {
		return record, e
	}
	if document.Digest(raw) != p.Revision {
		return record, fmt.Errorf("source changed after run review")
	}
	args := []string{"env", "-i"}
	if p.InheritEnvironment {
		args = []string{"env"}
	}
	for _, k := range sortedKeys(p.Environment) {
		args = append(args, k+"="+p.Environment[k])
	}
	args = append(args, "sh", "-c", "cd "+transport.Quote(p.Directory)+" && exec "+transport.Join([]string{p.Shell, "-c", p.Command}))
	r, e := s.Runner.Run(ctx, h, args, []byte(p.Stdin))
	if errors.Is(e, transport.ErrOutputLimit) && r.Code == 0 {
		e = nil
	}
	record.Finished = s.Now()
	record.ExitCode = r.Code
	record.Output = string(r.Stdout)
	record.Stderr = r.Stderr
	record.OutputTruncated = r.Truncated
	record.Status = "completed"
	if e != nil {
		record.Error = e.Error()
		record.Status = "failed"
		if ctx.Err() != nil || r.Code == 255 {
			record.Status = "unknown"
		}
	} else if p.Runner == "pueue" {
		record.Status = "queued"
		record.TaskID = strings.TrimSpace(string(r.Stdout))
	}
	base, pathErr := config.Base("state")
	if pathErr != nil {
		return record, pathErr
	}
	data, _ := json.MarshalIndent(record, "", "  ")
	if saveErr := config.AtomicWrite(filepath.Join(base, "runs", record.ID+".json"), data, 0600); saveErr != nil {
		return record, fmt.Errorf("execution %s; failed to save run record: %w", record.Status, saveErr)
	}
	return record, e
}
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
func Runs(host, source, id string) ([]RunRecord, error) {
	base, e := config.Base("state")
	if e != nil {
		return nil, e
	}
	paths, e := filepath.Glob(filepath.Join(base, "runs", "*.json"))
	if e != nil {
		return nil, e
	}
	out := []RunRecord{}
	for i := len(paths) - 1; i >= 0; i-- {
		var r RunRecord
		if e = ReadJSON(paths[i], &r); e != nil {
			return nil, e
		}
		if r.Host == host && r.Source == source && r.JobID == id {
			out = append(out, r)
			if len(out) == 20 {
				break
			}
		}
	}
	return out, nil
}
func (s *Service) Logs(ctx context.Context, e Entry, path string, lines int) (string, error) {
	if lines < 1 || lines > 10000 {
		return "", fmt.Errorf("lines must be 1..10000")
	}
	if path == "" {
		r, err := LoadRecipe(e)
		if err != nil {
			return "", err
		}
		path = r.Log
		if path == "" {
			path = r.Output
		}
	}
	if path != "" {
		h, err := s.Config.Host(e.Host)
		if err != nil {
			return "", err
		}
		r, err := s.Runner.Run(ctx, h, []string{"sh", "-c", "tail -n " + strconv.Itoa(lines) + " " + transport.Path(path)}, nil)
		return string(r.Stdout), err
	}
	runs, err := Runs(e.Host, e.Source, e.ID)
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "No recorded manual runs or configured log file. Scheduled cron history is unknown.", nil
	}
	var b strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&b, "%s  %s  exit=%d\n%s%s\n", r.Started.Format(time.RFC3339), r.Status, r.ExitCode, r.Output, r.Stderr)
	}
	return b.String(), nil
}

type ScriptDraft struct {
	Host   string
	Path   string
	Before string
	File   string
}

func (s *Service) ScriptDraft(ctx context.Context, host, path string) (ScriptDraft, error) {
	h, err := s.Config.Host(host)
	if err != nil {
		return ScriptDraft{}, err
	}
	raw, exists, err := s.read(ctx, h, config.Source{Kind: "file", Path: path})
	if err != nil {
		return ScriptDraft{}, err
	}
	if !exists {
		return ScriptDraft{}, fmt.Errorf("script does not exist: %s", path)
	}
	f, err := os.CreateTemp("", "lazycrontab-script-*")
	if err != nil {
		return ScriptDraft{}, err
	}
	if _, err = f.WriteString(raw); err != nil {
		f.Close()
		os.Remove(f.Name())
		return ScriptDraft{}, err
	}
	f.Close()
	return ScriptDraft{Host: host, Path: path, Before: raw, File: f.Name()}, nil
}
func (s *Service) SaveScript(ctx context.Context, d ScriptDraft, after string) (Receipt, error) {
	h, err := s.Config.Host(d.Host)
	r := Receipt{Host: d.Host, Source: d.Path, Status: "failed"}
	if err != nil {
		return r, err
	}
	current, exists, err := s.read(ctx, h, config.Source{Kind: "file", Path: d.Path})
	if err != nil {
		return r, err
	}
	if !exists || current != d.Before {
		return r, fmt.Errorf("script changed during editing")
	}
	p := Plan{Host: d.Host, Source: d.Path, Before: d.Before, Existed: true, Operation: "script"}
	r.Backup, err = s.backup(p)
	if err != nil {
		return r, err
	}
	err = s.writeFile(ctx, h, d.Path, d.Before, after, true)
	if err != nil {
		r.Status = "unknown"
		return r, err
	}
	current, _, err = s.read(ctx, h, config.Source{Kind: "file", Path: d.Path})
	if err != nil || current != after {
		r.Status = "unknown"
		return r, fmt.Errorf("script write could not be verified")
	}
	r.Status = "saved"
	return r, nil
}

package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

// newJobModel is the shared add/edit workflow mounted by CLI, dashboard and
// playground. It never owns a second terminal when embedded in another model.
func newJobModel(ctx context.Context, s *service.Service, host, source, op, id, prefillSchedule string) (tea.Model, error) {
	var overrides map[string]string
	if prefillSchedule != "" {
		overrides = map[string]string{"schedule": prefillSchedule}
	}
	spec, _, err := newJobFormSpec(ctx, s, host, source, op, id, overrides)
	if err != nil {
		return nil, err
	}
	return ui.NewForm(ctx, spec), nil
}

func sourceDialect(src config.Source) schedule.Dialect {
	if src.Dialect != "" {
		return schedule.Dialect(src.Dialect)
	}
	if src.Kind == "file" {
		return schedule.Supercronic
	}
	return schedule.System
}

var presetLabels = map[string]string{
	"command":       "Shell command (one line)",
	"executable":    "Existing executable script",
	"shell":         "Existing shell script",
	"python":        "Existing Python script",
	"uv-project":    "Existing script in a uv project",
	"uv-script":     "Existing standalone uv script",
	"managed-shell": "Managed shell script (write content)",
}

func jobPresets() []string {
	return append(append([]string(nil), service.ScriptPresets...), "managed-shell")
}

func validJobPreset(preset string) bool {
	return preset == "managed-shell" || service.ValidPreset(preset)
}

func describeJobExecution(r service.Recipe, command string) string {
	preset := "command"
	if r.ScriptTask != nil {
		preset = r.ScriptTask.Preset
	}
	if r.ManagedScript != nil {
		preset = "managed-shell"
	}
	lines := []string{"Task: " + presetLabels[preset]}
	if preset == "command" {
		lines = append(lines, "Command: "+command)
	} else {
		lines = append(lines, "Script file: "+r.Script)
		if r.ScriptTask.Runtime != "" {
			lines = append(lines, "Interpreter: "+r.ScriptTask.Runtime)
		}
		if r.ScriptTask.Project != "" {
			lines = append(lines, "Project: "+r.ScriptTask.Project)
		}
		if len(r.ScriptTask.Args) > 0 {
			lines = append(lines, "Arguments: "+transport.Join(r.ScriptTask.Args))
		}
	}
	if r.Runner == "pueue" {
		group := r.Group
		if group == "" {
			group = "default"
		}
		lines = append(lines, "Runner: Pueue · group "+group)
	} else {
		lines = append(lines, "Runner: cron directly")
	}
	if r.Directory != "" {
		lines = append(lines, "Working directory: "+r.Directory)
	}
	return strings.Join(lines, "\n")
}

func recipeValues(j document.Job, r service.Recipe) map[string]string {
	v := map[string]string{"schedule": j.Schedule, "command": j.Command, "name": j.Name, "remark": j.Remark, "enabled": fmt.Sprint(j.Enabled), "runner": r.Runner, "group": r.Group, "directory": r.Directory, "output": r.Output, "stderr": r.Stderr, "script": r.Script, "log": r.Log, "preset": "command", "runtime": "", "project": "", "args": "", "environment": ""}
	v["output_policy"] = service.EffectiveOutputPolicy(r)
	v["enqueue_output"] = "quiet"
	if r.Runner == "pueue" {
		v["enqueue_output"] = service.EffectiveEnqueueOutput(r)
	}
	if r.ScriptTask != nil {
		v["preset"] = r.ScriptTask.Preset
		v["runtime"] = r.ScriptTask.Runtime
		v["project"] = r.ScriptTask.Project
		v["args"] = transport.Join(r.ScriptTask.Args)
	}
	if r.ManagedScript != nil {
		v["preset"] = "managed-shell"
	}
	keys := make([]string, 0, len(r.Environment))
	for k := range r.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assignments := []string{}
	for _, k := range keys {
		assignments = append(assignments, k+"="+r.Environment[k])
	}
	v["environment"] = transport.Join(assignments)
	return v
}

func newJobFormSpec(ctx context.Context, s *service.Service, host, source, op, id string, overrides map[string]string) (ui.FormSpec, map[string]string, error) {
	return jobFormSpecWithOptions(ctx, s, host, source, op, id, overrides, jobFormOptions{})
}

type jobFormOptions struct {
	Headless bool
}

func jobFormSpecWithOptions(ctx context.Context, s *service.Service, host, source, op, id string, overrides map[string]string, options jobFormOptions) (ui.FormSpec, map[string]string, error) {
	if op == "add" {
		id = ""
	}
	c := s.Config
	if host == "all" || source == "all" {
		return ui.FormSpec{}, nil, usage("choose one host and one source")
	}
	if _, err := c.Source(host, source); err != nil {
		return ui.FormSpec{}, nil, err
	}
	j := document.Job{Enabled: true}
	r := service.Recipe{Runner: "direct"}
	var snap service.Snapshot
	var err error
	if op == "edit" {
		snap, err = s.Snapshot(ctx, host, source)
		if err != nil {
			return ui.FormSpec{}, nil, err
		}
		j, err = snap.Document.Find(id)
		if err != nil {
			return ui.FormSpec{}, nil, err
		}
		r, err = service.LoadRecipe(service.Entry{Job: j, Host: host, Source: source, Dialect: snap.Document.Dialect})
		if err != nil {
			if _, replace := overrides["command"]; !replace {
				return ui.FormSpec{}, nil, fmt.Errorf("%w; pass --command to explicitly replace the recipe", err)
			}
			r = service.Recipe{Runner: "direct"}
		}
		if r.Original != "" {
			j.Command = r.Original
		}
	}
	if r.Runner == "" {
		r.Runner = "direct"
	}
	values := recipeValues(j, r)
	verifiedManagedBody := false
	if op == "edit" && r.ManagedScript != nil {
		_, replaceCommand := overrides["command"]
		_, replaceBody := overrides["script_content"]
		// Mounted forms must preload the body: a user can change other execution
		// fields after arriving with an enqueue policy prefilled. Only the
		// headless migration can promise not to need or recompile the script.
		if !replaceCommand && !replaceBody && !(options.Headless && r.Runner == "pueue" && enqueueOnlyOverrides(overrides)) {
			saved, _ := snap.Document.Find(id)
			values["script_content"], err = s.ReadManagedScript(ctx, service.Entry{Job: saved, Host: host, Source: source, Dialect: snap.Document.Dialect}, r)
			if err != nil {
				return ui.FormSpec{}, nil, fmt.Errorf("read managed script: %w; provide --script-content-file to replace it or --command to switch to a command", err)
			}
			verifiedManagedBody = true
		}
	}
	if op == "add" && values["schedule"] == "" {
		values["schedule"] = "0 9 * * *"
	}
	values["host"] = host
	values["source"] = source
	initialValues := make(map[string]string, len(values))
	for key, value := range values {
		initialValues[key] = value
	}
	commandScript := r.Script
	if r.ManagedScript != nil {
		commandScript = ""
	}
	initialExecution := effectiveJobValues(initialValues, commandScript)
	if explicitScript, ok := overrides["script"]; ok {
		commandScript = explicitScript
	}
	if _, replace := overrides["command"]; replace {
		values["preset"] = "command"
		values["runtime"] = ""
		values["project"] = ""
		values["args"] = ""
		if r.ManagedScript != nil {
			values["script"] = ""
			values["script_content"] = ""
		}
	}
	for k, v := range overrides {
		values[k] = v
	}
	applyOutputOverrides(values, overrides)
	build := func(ctx context.Context, v map[string]string) (ui.Review, error) {
		v = effectiveJobValues(v, commandScript)
		executionUnchanged := op == "edit" && sameExecutionValues(v, initialExecution)
		enqueueOnly := op == "edit" && r.Runner == "pueue" && sameExecutionExceptEnqueue(v, initialExecution) && (!executionUnchanged || enqueueOnlyOverrides(overrides))
		if !validOutputPolicy(v["output_policy"]) {
			return ui.Review{}, fmt.Errorf("task output must be inherit, files, stderr-only or discard")
		}
		if v["output_policy"] == "files" && v["output"] == "" && v["stderr"] == "" {
			return ui.Review{}, fmt.Errorf("Files requires an output or stderr path; choose Inherit to stop redirecting")
		}
		targetHost, targetSource := host, source
		if op == "add" {
			if v["host"] != "" {
				targetHost = v["host"]
			}
			if v["source"] != "" {
				targetSource = v["source"]
			}
		}
		src, err := c.Source(targetHost, targetSource)
		if err != nil {
			return ui.Review{}, err
		}
		dialect := sourceDialect(src)
		job := j
		job.Schedule = v["schedule"]
		job.Command = v["command"]
		job.Name = v["name"]
		job.Remark = v["remark"]
		job.Enabled = v["enabled"] != "false"
		if _, err = schedule.Parse(job.Schedule, dialect, time.UTC, c.Locale); err != nil {
			return ui.Review{}, err
		}
		preset := v["preset"]
		if preset == "" {
			preset = "command"
		}
		if !validJobPreset(preset) {
			return ui.Review{}, fmt.Errorf("unknown preset %q", preset)
		}
		if preset == "command" && strings.TrimSpace(job.Command) == "" {
			return ui.Review{}, fmt.Errorf("command is required, or choose a script preset")
		}
		current := snap
		if current.Document == nil {
			current, err = s.Snapshot(ctx, targetHost, targetSource)
			if err != nil {
				return ui.Review{}, err
			}
		}
		if op == "add" {
			job.Environment = current.Document.Environment
		}
		if job.Version != 1 || strings.HasPrefix(job.ID, "line-") {
			job.ID = document.NewID()
			job.Version = 1
		}
		recipe := r
		recipe.Original = job.Command
		recipe.Runner = v["runner"]
		recipe.Group = v["group"]
		recipe.Directory = v["directory"]
		recipe.Output = v["output"]
		recipe.Stderr = v["stderr"]
		recipe.OutputPolicy = v["output_policy"]
		recipe.EnqueueOutput = v["enqueue_output"]
		recipe.Script = v["script"]
		recipe.Log = v["log"]
		assignments, err := splitArgs(v["environment"])
		if err != nil {
			return ui.Review{}, fmt.Errorf("environment quoting is incomplete")
		}
		recipe.Environment, err = service.ParseEnvironment(assignments)
		if err != nil {
			return ui.Review{}, err
		}
		recipe.ScriptTask = nil
		recipe.ManagedScript = nil
		var managedPlan *service.ManagedScriptPlan
		entry := service.Entry{Job: job, Host: targetHost, Source: targetSource, Dialect: dialect}
		if preset == "managed-shell" && !enqueueOnly {
			body := v["script_content"]
			if strings.TrimSpace(body) == "" {
				return ui.Review{}, fmt.Errorf("managed script content is required")
			}
			var prepared service.ManagedScriptPlan
			if op == "edit" && r.ManagedScript != nil && r.ManagedScript.Digest == document.Digest(body) {
				// Metadata-only edits retain the current native command even if
				// XDG_DATA_HOME has changed or is unavailable. Apply inspects this
				// stored path before any write; no new data root is consulted.
				prepared = service.ManagedScriptPlan{Host: targetHost, Path: r.Script, Content: body, Digest: r.ManagedScript.Digest}
			} else {
				prepared, err = s.PrepareManagedScript(ctx, entry, body)
				if err != nil {
					return ui.Review{}, err
				}
			}
			if verifiedManagedBody {
				prepared.PreviousPath = r.Script
				prepared.PreviousDigest = r.ManagedScript.Digest
			}
			managedPlan = &prepared
			recipe.Script = prepared.Path
			recipe.ManagedScript = &service.ManagedScript{Version: 1, Digest: prepared.Digest}
			if recipe.Directory == "" {
				recipe.Directory, err = s.ResolveTargetPath(ctx, targetHost, "", "")
				if err != nil {
					return ui.Review{}, err
				}
			}
		}
		if preset != "command" && !enqueueOnly {
			args, err := splitArgs(v["args"])
			if err != nil {
				return ui.Review{}, fmt.Errorf("argument quoting is incomplete")
			}
			scriptPreset, runtime := preset, v["runtime"]
			if preset == "managed-shell" {
				scriptPreset = "shell"
				if runtime == "" {
					runtime = "/bin/sh"
				}
			}
			recipe.ScriptTask = &service.ScriptTask{Version: 1, Preset: scriptPreset, Runtime: runtime, Project: v["project"], Args: args}
		}
		if executionUnchanged || enqueueOnly {
			// Metadata and schedule edits must not silently rebind a pinned
			// runtime/Pueue executable or rebuild wrappers after SHELL changes.
			recipe = r
			recipe.Log = v["log"]
			if recipe.Log != r.Log && recipe.Log != "" {
				if strings.ContainsAny(recipe.Log, "\r\n\x00") {
					return ui.Review{}, fmt.Errorf("log paths cannot contain newlines or NUL")
				}
				if recipe.ScriptTask != nil {
					recipe.Log, err = s.ResolveTargetPath(ctx, targetHost, recipe.Log, recipe.Directory)
					if err != nil {
						return ui.Review{}, err
					}
				} else if !filepath.IsAbs(recipe.Log) && !strings.HasPrefix(recipe.Log, "~/") {
					return ui.Review{}, fmt.Errorf("log must be an absolute target path or start with ~/")
				}
			}
		} else {
			recipe, err = s.ResolveScriptRecipe(ctx, entry, recipe)
			if err != nil {
				return ui.Review{}, err
			}
		}
		if recipe.Runner == "pueue" && !executionUnchanged && !enqueueOnly {
			caps := s.Capabilities(ctx, targetHost)
			if !caps.Ready {
				return ui.Review{}, fmt.Errorf("Pueue unavailable: %s", caps.Error)
			}
			found := recipe.Group == ""
			for _, g := range caps.Groups {
				if g == recipe.Group {
					found = true
				}
			}
			if !found {
				return ui.Review{}, fmt.Errorf("Pueue group %q does not exist", recipe.Group)
			}
			recipe.PueuePath = caps.PueuePath
		}
		if enqueueOnly {
			saved, _ := snap.Document.Find(id)
			stored := service.Entry{Job: saved, Host: targetHost, Source: targetSource, Dialect: dialect}
			job.Command, recipe, err = service.UpdateEnqueueOutput(stored, recipe, v["enqueue_output"])
			if err != nil {
				return ui.Review{}, err
			}
		} else if executionUnchanged {
			saved, _ := snap.Document.Find(id)
			job.Command = saved.Command
		} else {
			job.Command, recipe, err = service.CompileRecipe(entry, recipe)
			if err != nil {
				return ui.Review{}, err
			}
		}
		entry.Job = job
		plan, err := s.Plan(ctx, current, id, &job, op)
		if err != nil {
			return ui.Review{}, err
		}
		plan.ManagedScript = managedPlan
		zone := current.Timezone
		if value := job.Environment["CRON_TZ"]; value != "" && (dialect == schedule.Supercronic || src.CronTZ) {
			zone = value
		}
		preview := "Timezone unknown; set source timezone to preview next runs."
		if loc, e := time.LoadLocation(zone); e == nil && zone != "" {
			if sc, e := schedule.Parse(job.Schedule, dialect, loc, c.Locale); e == nil {
				next, _ := sc.NextN(ctx, time.Now(), 5)
				lines := []string{sc.Description + " · " + zone}
				for _, at := range next {
					lines = append(lines, "Next: "+at.Format("Mon 2006-01-02 15:04:05 -07:00"))
				}
				preview = strings.Join(lines, "\n")
			}
		}
		checks := ""
		if managedPlan != nil {
			checks = "\n\n" + s.CheckManagedRecipe(ctx, entry, recipe, *managedPlan).Text()
		} else if !executionUnchanged && !enqueueOnly && (recipe.ScriptTask != nil || recipe.Script != "" || recipe.Directory != "" || recipe.Output != "" || recipe.Stderr != "") {
			checks = "\n\n" + s.CheckRecipe(ctx, entry, recipe).Text()
		}
		scriptPreview := ""
		if managedPlan != nil {
			scriptPreview = "\n\nManaged script content\n" + managedPlan.Content + "\nSaved only when you Apply; previous versions are retained."
		}
		return ui.Review{Text: targetHost + " / " + targetSource + "\n" + describeJobExecution(recipe, v["command"]) + "\n" + describeOutputRouting(recipe, job.Environment, dialect) + "\n\nSchedule: " + job.Schedule + "\n" + preview + scriptPreview + checks + "\n" + strings.Join(plan.Warnings, "\n"), Diff: plan.Diff, Data: jobReview{plan, recipe, entry}}, nil
	}
	apply := func(ctx context.Context, _ map[string]string, review ui.Review) (string, error) {
		data := review.Data.(jobReview)
		receipt, err := applyJobReview(ctx, s, data)
		return receiptText(receipt, data.Plan.Operation, data.Plan.JobID), err
	}
	chosenHost := func(v map[string]string) string {
		if v["host"] != "" {
			return v["host"]
		}
		return host
	}
	chosenSource := func(v map[string]string) string {
		if v["source"] != "" {
			return v["source"]
		}
		return source
	}
	contextFor := func(v map[string]string) ui.ScheduleEditorOptions {
		h, _ := c.Host(chosenHost(v))
		src, _ := c.Source(chosenHost(v), chosenSource(v))
		zone := src.Timezone
		if zone == "" {
			zone = h.Timezone
		}
		if zone == "" && snap.Host == h.ID && snap.Source == src.ID {
			zone = snap.Timezone
		}
		if override := j.Environment["CRON_TZ"]; override != "" && (sourceDialect(src) == schedule.Supercronic || src.CronTZ) {
			zone = override
		}
		return ui.ScheduleEditorOptions{Expression: v["schedule"], Dialect: sourceDialect(src), Timezone: zone, Locale: c.Locale, Mouse: c.Mouse, Theme: c.Theme}
	}
	showScript := func(v map[string]string) bool { return v["preset"] != "command" }
	fields := []ui.Field{}
	if op == "add" {
		hosts := []string{}
		for _, h := range c.AllHosts() {
			hosts = append(hosts, h.ID)
		}
		sources := []string{}
		for _, src := range c.HostSources(host) {
			sources = append(sources, src.ID)
		}
		fields = append(fields, ui.Field{Key: "host", Label: "Host", Options: hosts}, ui.Field{Key: "source", Label: "Source", Options: sources})
	}
	fields = append(fields,
		ui.Field{Key: "name", Label: "Name"},
		ui.Field{Key: "preset", Label: "What to run", Options: jobPresets(), OptionLabels: presetLabels, Hint: "Shell command needs no file. Choose Managed shell script to write multiple lines without managing a path."},
		ui.Field{Key: "command", Slot: "payload", Label: "Command to execute (e.g. echo \"hi\")", Hint: "A shell command runs directly; no script file is needed.", Show: func(v map[string]string) bool { return v["preset"] == "command" }},
		ui.Field{Key: "script", Slot: "payload", Label: "Existing script file on selected host", Hint: "Enter a file path, e.g. ./job.sh. For echo \"hi\", choose Shell command.", Show: func(v map[string]string) bool { return showScript(v) && v["preset"] != "managed-shell" }, Pick: jobPathPicker(s, chosenHost, "script", false)},
		ui.Field{Key: "script_content", Slot: "payload", Label: "Script content · Enter to edit", Kind: "multiline", Hint: "Write shell commands; lazycrontab saves a private version on the selected host only after Apply.", Show: func(v map[string]string) bool { return v["preset"] == "managed-shell" }},
		ui.Field{Key: "runtime", Reserve: true, Label: "Interpreter / uv executable (Browse to choose)", Show: func(v map[string]string) bool { return showScript(v) && v["preset"] != "executable" }, Pick: jobRuntimePicker(s, chosenHost)},
		ui.Field{Key: "project", Reserve: true, Label: "Python project (Browse to choose)", Show: func(v map[string]string) bool { return v["preset"] == "uv-project" }, Pick: jobProjectPicker(s, chosenHost)},
		ui.Field{Key: "directory", Label: "Working directory (script presets suggest a default)", Pick: jobPathPicker(s, chosenHost, "directory", true)},
		ui.Field{Key: "args", Reserve: true, Label: "Arguments (quotes group spaces; no expansion)", Show: showScript},
		ui.Field{Key: "schedule", Label: "Schedule · Enter to open builder", Kind: "schedule"},
		ui.Field{Key: "enabled", Label: "Enabled", Options: []string{"true", "false"}, Hint: "Choose false to save the job without scheduling it while you fix a runtime finding."},
		ui.Field{Key: "remark", Label: "Remark / why"},
	)
	for _, key := range []string{"runner", "group", "enqueue_output", "environment", "output_policy", "output", "stderr", "log"} {
		field := ui.Field{Key: key, Label: key, Advanced: true}
		switch key {
		case "runner":
			field.Label = "Run directly or enqueue"
			field.Options = []string{"direct", "pueue"}
		case "group":
			field.Label = "Existing Pueue group"
			field.DisabledWhen = pueueGroupDisabled
			field.Hint = pueueOutputHint
		case "enqueue_output":
			field.Label = "Enqueue notices"
			field.Options = enqueueOutputs
			field.OptionLabels = enqueueOutputLabels
			field.DisabledWhen = pueueEnqueueDisabled
			field.Hint = "Quiet suppresses Pueue submission stdout; enqueue errors on stderr remain visible. Task logs are separate."
		case "environment":
			field.Label = "Variables (literal NAME=value; quote spaces)"
		case "output_policy":
			field.Label = "Task output"
			field.Options = outputPolicies
			field.OptionLabels = outputPolicyLabels
			field.Hint = "Inherit uses cron output or Pueue task logs. Files appends to your paths; no automatic rotation."
		case "output":
			field.Label = "Append output to file"
			field.Pick = jobPathPicker(s, chosenHost, key, false)
		case "stderr":
			field.Label = "Separate error output (optional)"
			field.Pick = jobPathPicker(s, chosenHost, key, false)
		case "log":
			field.Label = "Existing log to inspect"
			field.Pick = jobPathPicker(s, chosenHost, key, false)
			field.Hint = "Optional inspection file; does not redirect output. Pueue task output is also available via pueue log or lazypueue."
		}
		if key == "output" || key == "stderr" {
			field.DisabledWhen = outputPathDisabled
		}
		fields = append(fields, field)
	}
	for i := range fields {
		if value, ok := values[fields[i].Key]; ok {
			fields[i].Value = value
		}
	}
	spec := ui.FormSpec{Title: op + " job · " + host + " / " + source, Popup: true, Fields: fields, Mouse: c.Mouse, Theme: c.Theme, Build: build, Apply: apply, TextEditor: editor, ScheduleContext: contextFor, LoadKeys: []string{"host", "source", "runner", "preset", "script", "project", "directory", "runtime", "output_policy", "output", "stderr"}}
	spec.Load = func(ctx context.Context, v map[string]string) []ui.FieldUpdate {
		h := chosenHost(v)
		srcID := chosenSource(v)
		updates := []ui.FieldUpdate{}
		if op == "add" {
			ids := []string{}
			found := false
			for _, src := range c.HostSources(h) {
				ids = append(ids, src.ID)
				if src.ID == srcID {
					found = true
				}
			}
			u := ui.FieldUpdate{Key: "source", Options: ids}
			if !found && len(ids) > 0 {
				srcID = ids[0]
				u.Value = &srcID
			}
			updates = append(updates, u)
		}
		if v["runner"] == "pueue" {
			updates = append(updates, pueueFields(ctx, s, h)...)
		}
		snapshot, err := s.Snapshot(ctx, h, srcID)
		environment := j.Environment
		if err == nil {
			copyValues := map[string]string{}
			for k, val := range v {
				copyValues[k] = val
			}
			copyValues["source"] = srcID
			options := contextFor(copyValues)
			options.Timezone = snapshot.Timezone
			src, _ := c.Source(h, srcID)
			if op == "add" {
				environment = snapshot.Document.Environment
			}
			if override := environment["CRON_TZ"]; override != "" && (options.Dialect == schedule.Supercronic || src.CronTZ) {
				options.Timezone = override
			}
			updates = append(updates, ui.FieldUpdate{Key: "schedule", Schedule: &options})
		}
		updates = append(updates, jobScriptUpdates(ctx, s, h, srcID, v, environment)...)
		return updates
	}
	return spec, values, nil
}

func jobScriptUpdates(ctx context.Context, s *service.Service, host, source string, v map[string]string, environment map[string]string) []ui.FieldUpdate {
	preset := v["preset"]
	if preset == "managed-shell" {
		updates := []ui.FieldUpdate{{Key: "script_content", Hint: "Managed on " + host + "; previewing or cancelling creates no file."}}
		if v["runtime"] == "" {
			runtime := "/bin/sh"
			updates = append(updates, ui.FieldUpdate{Key: "runtime", Value: &runtime, Hint: "Runs with /bin/sh by default; choose a different shell if your script needs it."})
		}
		if v["directory"] == "" {
			directory, err := s.ResolveTargetPath(ctx, host, "", "")
			if err != nil {
				updates = append(updates, ui.FieldUpdate{Key: "directory", Hint: err.Error()})
			} else {
				updates = append(updates, ui.FieldUpdate{Key: "directory", Value: &directory, Hint: "Your script's relative files resolve here; independent of managed storage."})
			}
		}
		return updates
	}
	if preset == "" || preset == "command" || strings.TrimSpace(v["script"]) == "" {
		return nil
	}
	// Discovery follows the selected preset too: an inactive project/runtime
	// draft must not produce an error hint while inspecting another task type.
	v = effectiveJobValues(v, "")
	updates := []ui.FieldUpdate{}
	set := func(key, value, hint string) {
		update := ui.FieldUpdate{Key: key, Hint: hint}
		if v[key] == "" && value != "" {
			copy := value
			update.Value = &copy
		}
		updates = append(updates, update)
	}
	script, err := s.ResolveTargetPath(ctx, host, v["script"], v["directory"])
	if err != nil {
		return []ui.FieldUpdate{{Key: "script", Hint: "Unable to inspect target path: " + err.Error()}}
	}
	d, err := s.DiscoverScript(ctx, host, script)
	if err != nil {
		return []ui.FieldUpdate{{Key: "script", Hint: "Unable to inspect target path: " + err.Error()}}
	}
	if !d.Exists || !d.Readable {
		return []ui.FieldUpdate{{Key: "script", Hint: "Script is not readable on " + host + ": " + script + ". You can save disabled and fix the path later."}}
	}
	set("script", "", "Found on "+host+": "+script)
	// Freeze the inspected path before adding a working-directory default.
	// Otherwise "scripts/job.py" would be reinterpreted inside ".../scripts".
	// Form's load-value guard applies this only while the input is unchanged.
	if v["script"] != script {
		resolved := script
		updates[len(updates)-1].Value = &resolved
	}
	set("preset", "", "Suggested for this file: "+d.SuggestedPreset()+". Your selected preset is kept.")
	project := v["project"]
	if preset == "uv-project" && project != "" {
		resolved, err := s.ResolveTargetPath(ctx, host, project, v["directory"])
		if err != nil {
			return append(updates, ui.FieldUpdate{Key: "project", Hint: err.Error()})
		}
		project = resolved
	}
	if preset == "uv-project" && project == "" && len(d.Projects) > 0 {
		project = d.Projects[0].Path
	}
	if preset == "uv-project" {
		hint := "Choose the Python project whose dependencies this job should use."
		if len(d.Projects) > 0 {
			hint = "Detected " + d.Projects[0].Path + "/pyproject.toml. Browse to choose a different project."
		}
		if d.InlineMetadata {
			hint = "This file has inline dependencies; uv ignores project dependencies. Suggested preset: uv-script."
		}
		set("project", project, hint)
		if v["project"] != "" && v["project"] != project {
			resolved := project
			updates[len(updates)-1].Value = &resolved
		}
	}
	runtime := v["runtime"]
	suggested := d.RuntimeFor(preset, "")
	if runtime == "" {
		runtime = suggested
	}
	if preset != "executable" {
		hint := "Interpreter runs directly; no shell startup files or virtualenv activation."
		if suggested != "" {
			hint = "Detected " + suggested + ". Browse to choose another executable."
		}
		if runtime == "" {
			hint = "No runtime found. Browse or enter the target executable; nothing will be installed."
		}
		set("runtime", runtime, hint)
	}
	directory := v["directory"]
	if directory == "" {
		directory = filepath.Dir(script)
		if preset == "uv-project" && project != "" {
			directory = project
		}
	}
	set("directory", directory, "Explicit working directory; relative files used by your script resolve here.")
	if (preset != "executable" && runtime == "") || (preset == "uv-project" && project == "") {
		return updates
	}
	src, _ := s.Config.Source(host, source)
	entry := service.Entry{Host: host, Source: source, Dialect: sourceDialect(src), Job: document.Job{Environment: environment}}
	recipe := service.Recipe{Script: script, Directory: directory, Output: v["output"], Stderr: v["stderr"], Runner: "direct", ScriptTask: &service.ScriptTask{Version: 1, Preset: preset, Runtime: runtime, Project: project}}
	recipe, err = s.ResolveScriptRecipe(ctx, entry, recipe)
	if err != nil {
		updates = append(updates, ui.FieldUpdate{Key: "runtime", Hint: err.Error()})
		return updates
	}
	report := s.CheckRecipe(ctx, entry, recipe)
	hints := map[string][]string{}
	for _, finding := range report.Findings {
		if finding.Severity == "ok" || finding.Field == "" {
			continue
		}
		hints[finding.Field] = append(hints[finding.Field], finding.Message)
	}
	for key, lines := range hints {
		found := false
		for i := range updates {
			if updates[i].Key == key {
				updates[i].Hint += " " + strings.Join(lines, " ")
				found = true
				break
			}
		}
		if !found {
			updates = append(updates, ui.FieldUpdate{Key: key, Hint: strings.Join(lines, " ")})
		}
	}
	return updates
}

func jobPathPicker(s *service.Service, host func(map[string]string) string, key string, directories bool) func(context.Context, map[string]string) ([]ui.PickOption, error) {
	return func(ctx context.Context, v map[string]string) ([]ui.PickOption, error) {
		base := v["directory"]
		if key == "directory" {
			base = ""
		}
		start, err := s.ResolveTargetPath(ctx, host(v), v[key], base)
		if err != nil {
			return nil, err
		}
		candidates, err := s.PathCandidates(ctx, host(v), start, directories)
		if err != nil {
			return nil, err
		}
		out := []ui.PickOption{}
		for _, candidate := range candidates {
			if candidate.Kind == "directory" {
				if directories {
					out = append(out, ui.PickOption{Label: "Use " + candidate.Path, Value: candidate.Path})
				}
				out = append(out, ui.PickOption{Label: candidate.Path + "/", Value: candidate.Path, Navigate: true})
			} else {
				out = append(out, ui.PickOption{Label: candidate.Path, Value: candidate.Path})
			}
		}
		return out, nil
	}
}
func jobRuntimePicker(s *service.Service, host func(map[string]string) string) func(context.Context, map[string]string) ([]ui.PickOption, error) {
	return func(ctx context.Context, v map[string]string) ([]ui.PickOption, error) {
		script, err := s.ResolveTargetPath(ctx, host(v), v["script"], v["directory"])
		if err != nil {
			return nil, err
		}
		d, err := s.DiscoverScript(ctx, host(v), script)
		if err != nil {
			return nil, err
		}
		kind := v["preset"]
		if kind == "managed-shell" {
			kind = "shell"
		}
		if strings.HasPrefix(kind, "uv-") {
			kind = "uv"
		}
		out := []ui.PickOption{}
		for _, candidate := range d.Runtimes {
			if candidate.Kind == kind {
				out = append(out, ui.PickOption{Label: candidate.Path, Value: candidate.Path})
			}
		}
		if strings.Contains(v["runtime"], "/") {
			more, err := jobPathPicker(s, host, "runtime", false)(ctx, v)
			if err == nil {
				out = append(out, more...)
			}
		}
		return out, nil
	}
}
func jobProjectPicker(s *service.Service, host func(map[string]string) string) func(context.Context, map[string]string) ([]ui.PickOption, error) {
	return func(ctx context.Context, v map[string]string) ([]ui.PickOption, error) {
		script, err := s.ResolveTargetPath(ctx, host(v), v["script"], v["directory"])
		if err != nil {
			return nil, err
		}
		d, err := s.DiscoverScript(ctx, host(v), script)
		if err != nil {
			return nil, err
		}
		out := []ui.PickOption{}
		for _, candidate := range d.Projects {
			out = append(out, ui.PickOption{Label: candidate.Path + " · pyproject.toml", Value: candidate.Path})
		}
		copyValues := map[string]string{}
		for k, value := range v {
			copyValues[k] = value
		}
		if copyValues["project"] == "" {
			copyValues["project"] = filepath.Dir(d.Script)
		}
		more, err := jobPathPicker(s, host, "project", true)(ctx, copyValues)
		if err == nil {
			out = append(out, more...)
		}
		return out, nil
	}
}

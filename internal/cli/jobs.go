package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func addJobs(root *cobra.Command, o *options) {
	var search string
	list := &cobra.Command{Use: "list", Short: "List jobs with human-readable schedules", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		snaps := s.List(cmd.Context(), o.hostID(c), o.sourceID(c))
		if len(snaps) == 0 {
			return usage("no matching host/source")
		}
		var lines []string
		failed := false
		for i := range snaps {
			snap := &snaps[i]
			if snap.Error != "" {
				failed = true
				lines = append(lines, snap.Host+"/"+snap.Source+": "+snap.Error)
				continue
			}
			filtered := []service.Entry{}
			for _, j := range snap.Entries {
				if search != "" && !strings.Contains(strings.ToLower(j.Name+" "+j.Command+" "+j.Remark), strings.ToLower(search)) {
					continue
				}
				filtered = append(filtered, j)
				state := "on"
				if !j.Enabled {
					state = "off"
				}
				next := "—"
				if len(j.Next) > 0 {
					next = j.Next[0].Format("01-02 15:04 MST")
				}
				lines = append(lines, fmt.Sprintf("%s/%s  %s  [%s] %s\n  %s · next %s\n  %s", j.Host, j.Source, j.ID, state, j.Name, j.Description, next, j.Command))
				if j.Diagnostic != "" {
					lines = append(lines, "  ! "+j.Diagnostic)
				}
			}
			snap.Entries = filtered
		}
		if len(lines) == 0 {
			lines = append(lines, "No jobs. Use lazycrontab add to create one.")
		}
		if e = o.emit(cmd, snaps, strings.Join(lines, "\n")); e != nil {
			return e
		}
		if failed {
			return fmt.Errorf("some sources could not be read")
		}
		return nil
	}}
	list.Flags().StringVar(&search, "search", "", "Filter name, command and remark")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "show ID", Short: "Inspect a job", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		for _, j := range snap.Entries {
			if j.ID == args[0] {
				return o.emit(cmd, j, pretty(j))
			}
		}
		return fmt.Errorf("job not found")
	}})
	root.AddCommand(&cobra.Command{Use: "export", Short: "Print the original crontab document", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		_, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		if o.json {
			return o.emit(cmd, map[string]string{"content": snap.Document.Raw}, "")
		}
		_, e = fmt.Fprint(cmd.OutOrStdout(), snap.Document.Raw)
		return e
	}})
	for _, op := range []string{"enable", "disable", "remove"} {
		cmd := &cobra.Command{Use: op + " ID", Short: op + " one job after review", Args: exactArgs(1)}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, snap, e := o.snapshot(cmd)
			if e != nil {
				return e
			}
			j, e := snap.Document.Find(args[0])
			if e != nil {
				return e
			}
			var replacement *document.Job
			if op != "remove" {
				j.Enabled = op == "enable"
				replacement = &j
			}
			p, e := s.Plan(cmd.Context(), snap, args[0], replacement, op)
			if e != nil {
				return e
			}
			return o.applyPlan(cmd, s, p)
		}
		root.AddCommand(cmd)
	}
	for _, op := range []string{"add", "edit"} {
		addJobCommand(root, o, op)
	}
}
func (o *options) snapshot(cmd *cobra.Command) (*service.Service, service.Snapshot, error) {
	c, e := o.load()
	if e != nil {
		return nil, service.Snapshot{}, e
	}
	s := service.New(c)
	snap, e := s.Snapshot(cmd.Context(), o.hostID(c), o.sourceID(c))
	return s, snap, e
}

type jobReview struct {
	Plan   service.Plan
	Recipe service.Recipe
	Entry  service.Entry
}

func addJobCommand(root *cobra.Command, o *options, op string) {
	var name, expr, when, command, remark, runner, group, cwd, output, stderr, script, log string
	var disabled bool
	cmd := &cobra.Command{Use: op, Short: op + " a job using flags or the shared wizard"}
	if op == "edit" {
		cmd.Use += " ID"
		cmd.Args = exactArgs(1)
	} else {
		cmd.Args = exactArgs(0)
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Short job name")
	f.StringVar(&expr, "schedule", "", "Cron expression")
	f.StringVar(&when, "when", "", "Limited English schedule phrase")
	f.StringVar(&command, "command", "", "Complete shell command as one argument")
	f.StringVar(&remark, "remark", "", "Why this job exists")
	f.BoolVar(&disabled, "disabled", false, "Save the job disabled")
	f.StringVar(&runner, "runner", "direct", "direct or pueue")
	f.StringVar(&group, "group", "", "Existing Pueue group")
	f.StringVar(&cwd, "directory", "", "Target-side working directory")
	f.StringVar(&output, "output", "", "Append stdout and, by default, stderr to this file")
	f.StringVar(&stderr, "stderr", "", "Append stderr to a separate file")
	f.StringVar(&script, "script", "", "Explicit editable script path")
	f.StringVar(&log, "log", "", "Explicit existing log path")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if when != "" && expr != "" {
			return usage("choose --when or --schedule")
		}
		if runner != "direct" && runner != "pueue" {
			return usage("runner must be direct or pueue")
		}
		if when != "" {
			var e error
			expr, e = schedule.FromHuman(when)
			if e != nil {
				return usage("%v", e)
			}
		}
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		host, source := o.hostID(c), o.sourceID(c)
		if host == "all" || source == "all" {
			return usage("choose one host and one source")
		}
		src, e := c.Source(host, source)
		if e != nil {
			return e
		}
		dialect := schedule.Dialect(src.Dialect)
		if dialect == "" {
			dialect = schedule.System
			if src.Kind == "file" {
				dialect = schedule.Supercronic
			}
		}
		if expr != "" {
			if _, e = schedule.Parse(expr, dialect, time.UTC, c.Locale); e != nil {
				return usage("%v", e)
			}
		}
		id := ""
		var snap service.Snapshot
		j := document.Job{Enabled: !disabled}
		recipe := service.Recipe{Runner: "direct"}
		if op == "edit" {
			id = args[0]
			snap, e = s.Snapshot(cmd.Context(), host, source)
			if e != nil {
				return e
			}
			j, e = snap.Document.Find(id)
			if e != nil {
				return e
			}
			entry := service.Entry{Job: j, Host: host, Source: source, Dialect: dialect}
			recipe, e = service.LoadRecipe(entry)
			if e != nil {
				if !f.Changed("command") {
					return fmt.Errorf("%w; pass --command to explicitly replace the recipe", e)
				}
				recipe = service.Recipe{Runner: "direct"}
			}
			if recipe.Original != "" {
				j.Command = recipe.Original
			}
		}
		if op == "add" || f.Changed("name") {
			j.Name = name
		}
		if expr != "" {
			j.Schedule = expr
		}
		if op == "add" || f.Changed("command") {
			j.Command = command
		}
		if op == "add" || f.Changed("remark") {
			j.Remark = remark
		}
		if op == "add" || f.Changed("disabled") {
			j.Enabled = !disabled
		}
		assign := func(flag string, target *string, value string) {
			if op == "add" || f.Changed(flag) {
				*target = value
			}
		}
		assign("runner", &recipe.Runner, runner)
		assign("group", &recipe.Group, group)
		assign("directory", &recipe.Directory, cwd)
		assign("output", &recipe.Output, output)
		assign("stderr", &recipe.Stderr, stderr)
		assign("script", &recipe.Script, script)
		assign("log", &recipe.Log, log)
		if recipe.Runner == "" {
			recipe.Runner = "direct"
		}
		business := false
		f.Visit(func(flag *pflag.Flag) {
			if root.PersistentFlags().Lookup(flag.Name) == nil {
				business = true
			}
		})
		wizard := o.interactive || (!business && tty() && !o.json && !o.dry)
		build := func(ctx context.Context, v map[string]string) (ui.Review, error) {
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
			targetDialect := schedule.Dialect(src.Dialect)
			if targetDialect == "" {
				targetDialect = schedule.System
				if src.Kind == "file" {
					targetDialect = schedule.Supercronic
				}
			}
			expression, e := expression(v)
			if e != nil {
				return ui.Review{}, e
			}
			job := j
			job.Schedule = expression
			job.Command = v["command"]
			job.Name = v["name"]
			job.Remark = v["remark"]
			job.Enabled = v["enabled"] != "false"
			if strings.TrimSpace(job.Command) == "" {
				return ui.Review{}, fmt.Errorf("command is required")
			}
			current := snap
			if current.Document == nil {
				current, e = s.Snapshot(ctx, targetHost, targetSource)
				if e != nil {
					return ui.Review{}, e
				}
			}
			if op == "add" {
				job.Environment = current.Document.Environment
			}
			if job.Version != 1 || strings.HasPrefix(job.ID, "line-") {
				job.ID = document.NewID()
				job.Version = 1
			}
			r := recipe
			r.Original = job.Command
			r.Runner = v["runner"]
			r.Group = v["group"]
			r.Directory = v["directory"]
			r.Output = v["output"]
			r.Stderr = v["stderr"]
			r.Script = v["script"]
			r.Log = v["log"]
			entry := service.Entry{Job: job, Host: targetHost, Source: targetSource, Dialect: targetDialect}
			if r.Runner == "pueue" {
				caps := s.Capabilities(ctx, targetHost)
				if !caps.Ready {
					return ui.Review{}, fmt.Errorf("Pueue unavailable: %s", caps.Error)
				}
				found := r.Group == ""
				for _, g := range caps.Groups {
					if g == r.Group {
						found = true
					}
				}
				if !found {
					return ui.Review{}, fmt.Errorf("group %q does not exist; available: %s", r.Group, strings.Join(caps.Groups, ", "))
				}
				r.PueuePath = caps.PueuePath
			}
			job.Command, e = service.Compile(entry, r)
			if e != nil {
				return ui.Review{}, e
			}
			entry.Job = job
			p, e := s.Plan(ctx, current, id, &job, op)
			if e != nil {
				return ui.Review{}, e
			}
			entry.Job = job
			preview := "Timezone unknown; next runs unavailable."
			zone := current.Timezone
			if v := job.Environment["CRON_TZ"]; v != "" && (targetDialect == schedule.Supercronic || src.CronTZ) {
				zone = v
			}
			if loc, err := time.LoadLocation(zone); err == nil && zone != "" {
				if sc, err := schedule.Parse(job.Schedule, targetDialect, loc, c.Locale); err == nil {
					next, _ := sc.NextN(ctx, time.Now(), 5)
					preview = sc.Description + " · " + zone + "\n" + pretty(next)
				}
			}
			return ui.Review{Text: targetHost + " / " + targetSource + "\n" + preview + "\n" + p.Diff + "\n" + strings.Join(p.Warnings, "\n"), Data: jobReview{p, r, entry}}, nil
		}
		apply := func(ctx context.Context, _ map[string]string, r ui.Review) (string, error) {
			data := r.Data.(jobReview)
			receipt, e := s.Apply(ctx, data.Plan)
			if e != nil {
				return pretty(receipt), e
			}
			if e = service.SaveRecipe(data.Entry, data.Recipe); e != nil {
				return pretty(receipt), fmt.Errorf("crontab saved; helper metadata save failed: %w", e)
			}
			return pretty(receipt) + "\nJob ID: " + data.Plan.JobID, nil
		}
		values := map[string]string{"template": "Custom cron", "schedule": j.Schedule, "command": j.Command, "name": j.Name, "remark": j.Remark, "enabled": fmt.Sprint(j.Enabled), "runner": recipe.Runner, "group": recipe.Group, "directory": recipe.Directory, "output": recipe.Output, "stderr": recipe.Stderr, "script": recipe.Script, "log": recipe.Log}
		if wizard {
			fields := []ui.Field{{Key: "name", Label: "Name", Value: j.Name}, {Key: "command", Label: "Command (one shell line)", Value: j.Command}, {Key: "remark", Label: "Remark / why", Value: j.Remark}}
			if op == "add" {
				hosts := []string{}
				for _, h := range c.AllHosts() {
					hosts = append(hosts, h.ID)
				}
				sources := []string{}
				for _, src := range c.HostSources(host) {
					sources = append(sources, src.ID)
				}
				fields = append([]ui.Field{{Key: "host", Label: "Host", Value: host, Options: hosts}, {Key: "source", Label: "Source", Value: source, Options: sources}}, fields...)
			}
			fields = append(fields, scheduleFields(j.Schedule)...)
			if op == "add" && j.Schedule == "" {
				for i := range fields {
					if fields[i].Key == "template" {
						fields[i].Value = "Daily"
					}
				}
			}
			for _, key := range []string{"enabled", "runner", "group", "directory", "output", "stderr", "script", "log"} {
				field := ui.Field{Key: key, Label: key, Value: values[key], Advanced: true}
				if key == "enabled" {
					field.Options = []string{"true", "false"}
				}
				if key == "runner" {
					field.Options = []string{"direct", "pueue"}
					field.Unavailable = map[string]string{"pueue": "checking target capabilities…"}
				}
				fields = append(fields, field)
			}
			_, e := ui.RunForm(cmd.Context(), ui.FormSpec{Title: op + " job · " + host + " / " + source, Fields: fields, Mouse: c.Mouse, Live: liveSchedule(dialect, time.UTC, c.Locale), Build: build, Apply: apply, Load: func(ctx context.Context, v map[string]string) []ui.FieldUpdate {
				h := host
				if v["host"] != "" {
					h = v["host"]
				}
				updates := pueueFields(ctx, s, h)
				if op == "add" {
					ids := []string{}
					for _, src := range c.HostSources(h) {
						ids = append(ids, src.ID)
					}
					updates = append(updates, ui.FieldUpdate{Key: "source", Options: ids})
				}
				return updates
			}})
			return e
		}
		if j.Schedule == "" || j.Command == "" {
			return usage("--schedule (or --when) and --command are required; example: add --when 'daily at 03:00' --command '/path/backup' --yes; use --interactive for a wizard")
		}
		r, e := build(cmd.Context(), values)
		if e != nil {
			return e
		}
		return o.approve(cmd, op+" job", r.Data.(jobReview).Plan, r.Text, func(ctx context.Context) (any, string, error) {
			msg, e := apply(ctx, values, r)
			return map[string]any{"job_id": r.Data.(jobReview).Plan.JobID, "result": msg}, msg, e
		})
	}
	root.AddCommand(cmd)
}
